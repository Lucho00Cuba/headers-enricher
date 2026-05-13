package headers_enricher

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/google/uuid"
)

// bufPool reuses byte buffers across template executions to reduce per-request allocations.
var bufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

// Config is the root plugin configuration consumed by Traefik.
//
// Headers maps target header names to rule definitions grouped into request and
// response sections.
//
// AllowedEnv restricts which environment variables are exposed to templates via
// .Env. When empty only HOSTNAME is exposed by default to reduce accidental
// exposure of secrets stored in the process environment.
type Config struct {
	Headers    map[string]interface{} `json:"headers,omitempty"`
	AllowedEnv []string               `json:"allowedEnv,omitempty"`
}

// HeadersConfig stores the normalized request and response rule sets derived
// from Config.Headers.
type HeadersConfig struct {
	Request  map[string]interface{}
	Response map[string]interface{}
}

// CreateConfig returns the default plugin configuration expected by Traefik.
func CreateConfig() *Config {
	return &Config{
		Headers:    make(map[string]interface{}),
		AllowedEnv: []string{"HOSTNAME"},
	}
}

// HeaderRule defines how a target header value is resolved.
//
// The public API is centered on From:
//   - sources that need an identifier use Name
//   - sources that need inline content use Value
//   - sources that may resolve empty can use Default
//
// Templates use [[ ... ]] delimiters so they do not collide with Traefik file
// provider templating.
type HeaderRule struct {
	From    string `json:"from,omitempty"`
	Name    string `json:"name,omitempty"`
	Value   string `json:"value,omitempty"`
	Default string `json:"default,omitempty"`
}

// TemplateContext contains the data exposed to template-based rules.
//
// Header and query maps only expose the first value for each key.
// Env only contains variables explicitly listed in Config.AllowedEnv. When
// AllowedEnv is empty, only HOSTNAME is exposed.
type TemplateContext struct {
	UUID            string
	RequestHeaders  map[string]string
	ResponseHeaders map[string]string
	Method          string
	Host            string
	Scheme          string
	Path            string
	RequestURI      string
	QueryString     string
	Query           map[string]string
	ClientIP        string
	StatusCode      int
	Now             time.Time
	Env             map[string]string
}

// compiledHeader stores a precompiled rule resolver for a target header.
type compiledHeader struct {
	name    string
	target  string
	resolve func(TemplateContext) (string, error)
}

// HeadersEnricher is the Traefik middleware implementation.
type HeadersEnricher struct {
	next      http.Handler
	name      string
	requests  []compiledHeader
	responses []compiledHeader
	env       map[string]string // captured once at startup; shared across requests (read-only)
	logger    *log.Logger
}

// New creates a middleware instance and compiles all configured rules.
//
// Request rules are applied before the upstream handler is called. Response
// rules are applied after the upstream has committed its status and headers,
// but before the body reaches the client.
func New(ctx context.Context, next http.Handler, config *Config, name string) (http.Handler, error) {
	var requestRules []compiledHeader
	var responseRules []compiledHeader

	funcMap := template.FuncMap{
		"lower": strings.ToLower,
		"upper": strings.ToUpper,
	}

	logger := log.New(os.Stderr, fmt.Sprintf("headers-enricher[%s] ", name), 0)

	// Build the filtered env map once here so from: env rules read from the
	// same allowedEnv-scoped source as template .Env access.
	env := buildFilteredEnv(config.AllowedEnv)

	// Build a set for O(1) allowedEnv membership checks during rule compilation.
	effective := effectiveAllowedEnv(config.AllowedEnv)
	allowedSet := make(map[string]bool, len(effective))
	for _, k := range effective {
		allowedSet[k] = true
	}

	headersConfig, err := normalizeHeadersConfig(config.Headers)
	if err != nil {
		return nil, err
	}

	for _, section := range []struct {
		target string
		rules  map[string]interface{}
	}{
		{target: "request", rules: headersConfig.Request},
		{target: "response", rules: headersConfig.Response},
	} {
		headers := sortedKeys(section.rules)
		for _, header := range headers {
			rawRule := section.rules[header]
			rule, err := normalizeRule(rawRule)
			if err != nil {
				return nil, err
			}

			if section.target == "request" && rule.From == "response.header" {
				return nil, fmt.Errorf("header %q: from=response.header is not valid in request rules", header)
			}

			// Warn at startup for from: env misconfigurations so operators can
			// catch problems without sending traffic.
			if rule.From == "env" && rule.Name != "" {
				if !allowedSet[rule.Name] {
					logger.Printf("WARN header=%q: rule skipped — env var %q not in allowedEnv", header, rule.Name)
				} else if _, ok := env[rule.Name]; !ok && rule.Default == "" {
					logger.Printf("WARN header=%q: rule skipped — env var %q not set and has no default", header, rule.Name)
				}
			}

			resolver, err := compileRule(header, rule, funcMap, env)
			if err != nil {
				return nil, fmt.Errorf("header %q: %w", header, err)
			}

			compiled := compiledHeader{
				name:    header,
				target:  section.target,
				resolve: resolver,
			}

			if section.target == "response" {
				responseRules = append(responseRules, compiled)
				continue
			}

			requestRules = append(requestRules, compiled)
		}
	}

	logger.Printf("initialized request_rules=%d response_rules=%d allowed_env=%v",
		len(requestRules), len(responseRules), effective)

	return &HeadersEnricher{
		next:      next,
		name:      name,
		requests:  requestRules,
		responses: responseRules,
		env:       env,
		logger:    logger,
	}, nil
}

// ServeHTTP enriches request and response headers around the next handler.
//
// When no response rules are configured the upstream response is forwarded
// directly, avoiding the interception overhead entirely.
func (t *HeadersEnricher) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	requestUUID := uuid.New().String()
	requestNow := time.Now().UTC()

	requestContext := t.buildTemplateContext(req, http.Header{}, 0, requestUUID, requestNow)
	for _, h := range t.requests {
		t.applyRule(req.Header, h, requestContext)
	}

	if len(t.responses) == 0 {
		t.next.ServeHTTP(rw, req)
		return
	}

	srw := &streamingResponseWriter{
		target:     rw,
		headers:    make(http.Header),
		statusCode: http.StatusOK,
		enricher:   t,
		req:        req,
		uuid:       requestUUID,
		now:        requestNow,
	}
	t.next.ServeHTTP(srw, req)
	// Ensure headers are committed even if the upstream never wrote anything.
	srw.commit(srw.statusCode)
}

// streamingResponseWriter intercepts the upstream response headers and status
// code, applies response rules, then streams the body directly to the client
// without buffering it in memory.
//
// Implements http.Flusher and http.Hijacker so downstream handlers that rely
// on streaming or WebSocket upgrades continue to work.
type streamingResponseWriter struct {
	target     http.ResponseWriter
	headers    http.Header
	statusCode int
	committed  bool
	enricher   *HeadersEnricher
	req        *http.Request
	uuid       string
	now        time.Time
}

func (w *streamingResponseWriter) Header() http.Header {
	return w.headers
}

func (w *streamingResponseWriter) WriteHeader(code int) {
	w.commit(code)
}

func (w *streamingResponseWriter) Write(data []byte) (int, error) {
	if !w.committed {
		w.commit(http.StatusOK)
	}
	return w.target.Write(data)
}

// commit applies response rules and flushes headers + status to the real writer.
// Safe to call multiple times; only the first call takes effect.
func (w *streamingResponseWriter) commit(code int) {
	if w.committed {
		return
	}
	w.committed = true
	w.statusCode = code

	ctx := w.enricher.buildTemplateContext(w.req, w.headers, code, w.uuid, w.now)
	for _, h := range w.enricher.responses {
		w.enricher.applyRule(w.headers, h, ctx)
		// Sync ResponseHeaders incrementally so later rules see outputs from earlier ones.
		// Full flattenHeaders rebuild per rule is replaced with a targeted key update.
		key := http.CanonicalHeaderKey(h.name)
		lower := strings.ToLower(key)
		if vals := w.headers[key]; len(vals) > 0 {
			ctx.ResponseHeaders[key] = vals[0]
			ctx.ResponseHeaders[lower] = vals[0]
		} else {
			delete(ctx.ResponseHeaders, key)
			delete(ctx.ResponseHeaders, lower)
		}
	}

	copyHeaders(w.target.Header(), w.headers)
	w.target.WriteHeader(code)
}

// Flush implements http.Flusher, proxying to the real writer when supported.
func (w *streamingResponseWriter) Flush() {
	if f, ok := w.target.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack implements http.Hijacker, proxying to the real writer when supported.
func (w *streamingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.target.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("hijacking not supported by underlying ResponseWriter")
}

// buildTemplateContext assembles the context shared by rule resolvers.
// Uses pre-computed environment data stored in the enricher to avoid
// os.Environ() syscalls on every request.
func (t *HeadersEnricher) buildTemplateContext(req *http.Request, responseHeaders http.Header, statusCode int, requestUUID string, requestNow time.Time) TemplateContext {
	return TemplateContext{
		UUID:            requestUUID,
		RequestHeaders:  flattenHeaders(req.Header),
		ResponseHeaders: flattenHeaders(responseHeaders),
		Method:          req.Method,
		Host:            req.Host,
		Scheme:          detectScheme(req),
		Path:            req.URL.Path,
		RequestURI:      req.RequestURI,
		QueryString:     req.URL.RawQuery,
		Query:           flattenQuery(req.URL.Query()),
		ClientIP:        detectClientIP(req),
		StatusCode:      statusCode,
		Now:             requestNow,
		Env:             t.env,
	}
}

// buildFilteredEnv returns the environment variables exposed to templates.
// When allowlist is empty, only HOSTNAME is included by default; otherwise only
// the listed keys are populated, limiting accidental secret exposure.
func buildFilteredEnv(allowlist []string) map[string]string {
	keys := effectiveAllowedEnv(allowlist)
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		if val, ok := os.LookupEnv(key); ok {
			result[key] = val
		}
	}
	return result
}

func effectiveAllowedEnv(allowlist []string) []string {
	if len(allowlist) == 0 {
		return []string{"HOSTNAME"}
	}
	return allowlist
}

// applyRule resolves a rule and writes the result into the target header map.
// Empty results remove the target header. Errors are logged but never
// propagate to keep the middleware fail-safe.
func (t *HeadersEnricher) applyRule(targetHeaders http.Header, rule compiledHeader, ctx TemplateContext) {
	value, err := rule.resolve(ctx)
	if err != nil {
		t.logger.Printf("ERROR rule=%q error=%v", rule.name, err)
		return
	}

	if value == "" {
		targetHeaders.Del(rule.name)
		return
	}

	targetHeaders.Set(rule.name, value)
}

// compileRule converts a declarative rule into an executable resolver.
// env is the allowedEnv-filtered variable map; from: env rules read from it
// so that allowedEnv is the single authority for all env var access.
func compileRule(header string, rule HeaderRule, funcMap template.FuncMap, env map[string]string) (func(TemplateContext) (string, error), error) {
	if rule.From == "uuid" {
		return func(ctx TemplateContext) (string, error) {
			return ctx.UUID, nil
		}, nil
	}

	if rule.From == "now" {
		return func(ctx TemplateContext) (string, error) {
			return ctx.Now.Format(time.RFC3339), nil
		}, nil
	}

	if rule.From == "literal" {
		if rule.Value == "" {
			return nil, fmt.Errorf("value is required when from=literal")
		}

		return func(ctx TemplateContext) (string, error) {
			return rule.Value, nil
		}, nil
	}

	if rule.From == "template" {
		if rule.Value == "" {
			return nil, fmt.Errorf("value is required when from=template")
		}

		tpl, err := template.New(header).Funcs(funcMap).Delims("[[", "]]").Option("missingkey=zero").Parse(rule.Value)
		if err != nil {
			return nil, err
		}

		return func(ctx TemplateContext) (string, error) {
			buf := bufPool.Get().(*bytes.Buffer)
			buf.Reset()
			defer bufPool.Put(buf)
			if err := tpl.Execute(buf, ctx); err != nil {
				return "", err
			}
			return buf.String(), nil
		}, nil
	}

	if rule.From == "env" {
		if rule.Name == "" {
			return nil, fmt.Errorf("name is required when from=env")
		}

		// Read from the allowedEnv-filtered map captured at startup.
		// Variables absent from allowedEnv are not in the map and resolve
		// to the default (or empty), consistent with template .Env access.
		value := env[rule.Name]
		if value == "" {
			value = rule.Default
		}

		return func(ctx TemplateContext) (string, error) {
			return value, nil
		}, nil
	}

	if rule.From == "request.header" {
		if rule.Name == "" {
			return nil, fmt.Errorf("name is required when from=request.header")
		}

		headerName := strings.ToLower(rule.Name)
		return func(ctx TemplateContext) (string, error) {
			if value, ok := ctx.RequestHeaders[headerName]; ok && value != "" {
				return value, nil
			}

			if rule.Default == "" {
				return "", nil
			}

			return rule.Default, nil
		}, nil
	}

	if rule.From == "response.header" {
		if rule.Name == "" {
			return nil, fmt.Errorf("name is required when from=response.header")
		}

		headerName := strings.ToLower(rule.Name)
		return func(ctx TemplateContext) (string, error) {
			if value, ok := ctx.ResponseHeaders[headerName]; ok && value != "" {
				return value, nil
			}

			if rule.Default == "" {
				return "", nil
			}

			return rule.Default, nil
		}, nil
	}

	if rule.From != "" {
		return nil, fmt.Errorf("unsupported from value %q", rule.From)
	}

	return func(ctx TemplateContext) (string, error) {
		return rule.Default, nil
	}, nil
}

// normalizeRule converts raw configuration data into a HeaderRule.
func normalizeRule(raw interface{}) (HeaderRule, error) {
	switch value := raw.(type) {
	case map[string]interface{}:
		rule := HeaderRule{}

		if from, ok := value["from"].(string); ok {
			rule.From = from
		}
		if name, ok := value["name"].(string); ok {
			rule.Name = name
		}
		if defaultValue, ok := value["default"].(string); ok {
			rule.Default = defaultValue
		}
		if rawValue, ok := value["value"].(string); ok {
			rule.Value = rawValue
		}

		return rule, nil
	default:
		return HeaderRule{}, fmt.Errorf("unsupported header rule type %T", raw)
	}
}

// normalizeHeadersConfig splits the raw headers configuration into request and
// response rule sets.
func normalizeHeadersConfig(raw map[string]interface{}) (HeadersConfig, error) {
	cfg := HeadersConfig{
		Request:  map[string]interface{}{},
		Response: map[string]interface{}{},
	}

	requestRaw, hasRequest := raw["request"]
	responseRaw, hasResponse := raw["response"]

	if !hasRequest && !hasResponse {
		return cfg, fmt.Errorf("headers config must contain request and/or response sections")
	}

	if hasRequest {
		requestRules, err := normalizeRuleSet(requestRaw)
		if err != nil {
			return cfg, fmt.Errorf("invalid request headers config: %w", err)
		}
		cfg.Request = requestRules
	}

	if hasResponse {
		responseRules, err := normalizeRuleSet(responseRaw)
		if err != nil {
			return cfg, fmt.Errorf("invalid response headers config: %w", err)
		}
		cfg.Response = responseRules
	}

	return cfg, nil
}

// normalizeRuleSet validates that a rule section is expressed as an object.
func normalizeRuleSet(raw interface{}) (map[string]interface{}, error) {
	rules, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("expected object, got %T", raw)
	}
	return rules, nil
}

// sortedKeys returns the map keys in lexical order to keep rule compilation
// deterministic.
func sortedKeys(values map[string]interface{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// flattenHeaders converts an http.Header into a flat string map using the first
// value for each header key. It stores both original-case and lower-case keys
// to make template lookups more forgiving.
func flattenHeaders(h http.Header) map[string]string {
	result := make(map[string]string)

	for k, v := range h {
		if len(v) > 0 {
			result[k] = v[0]
			result[strings.ToLower(k)] = v[0]
		}
	}

	return result
}

// flattenQuery converts a query map into a flat string map using the first
// value for each parameter.
func flattenQuery(values map[string][]string) map[string]string {
	result := make(map[string]string)
	for key, items := range values {
		if len(items) == 0 {
			continue
		}
		result[key] = items[0]
	}
	return result
}

func copyHeaders(dst, src http.Header) {
	for key := range dst {
		dst.Del(key)
	}
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

// detectScheme returns the request scheme, preferring X-Forwarded-Proto.
// Note: X-Forwarded-Proto is only trustworthy when Traefik's
// forwardedHeaders.trustedIPs is configured to strip client-supplied values.
func detectScheme(req *http.Request) string {
	if forwardedProto := req.Header.Get("X-Forwarded-Proto"); forwardedProto != "" {
		return forwardedProto
	}
	if req.TLS != nil {
		return "https"
	}
	return "http"
}

// detectClientIP extracts the client IP, preferring X-Forwarded-For.
// Note: ClientIP is only trustworthy when Traefik's forwardedHeaders.trustedIPs
// is configured, otherwise a client can spoof it via the XFF header.
func detectClientIP(req *http.Request) string {
	if forwardedFor := req.Header.Get("X-Forwarded-For"); forwardedFor != "" {
		parts := strings.Split(forwardedFor, ",")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}
	if realIP := req.Header.Get("X-Real-Ip"); realIP != "" {
		return realIP
	}
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err == nil {
		return host
	}
	return req.RemoteAddr
}
