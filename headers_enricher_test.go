package headers_enricher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMiddlewareAppliesRequestAndResponseRules(t *testing.T) {
	config := &Config{
		Headers: map[string]any{
			"request": map[string]any{
				"X-Request-Id": map[string]any{
					"from": "uuid",
				},
				"X-User": map[string]any{
					"from":    "request.header",
					"name":    "XAuthUser",
					"default": "anonymous",
				},
				"X-User-Optional": map[string]any{
					"from": "request.header",
					"name": "missing",
				},
				"X-Request-Template": map[string]any{
					"from":  "template",
					"value": "req:[[ .Method ]] [[ .Path ]] [[ index .RequestHeaders \"xauthuser\" ]]",
				},
			},
			"response": map[string]any{
				"X-Plugin": map[string]any{
					"from":  "literal",
					"value": "headers-enricher",
				},
				"X-Response-User": map[string]any{
					"from": "request.header",
					"name": "XAuthUser",
				},
				"X-Response-Plugin-Copy": map[string]any{
					"from": "response.header",
					"name": "X-Plugin",
				},
				"X-Response-Template": map[string]any{
					"from":  "template",
					"value": "res:[[ .StatusCode ]] [[ .Host ]] [[ .RequestURI ]]",
				},
			},
		},
	}

	var upstreamRequest *http.Request
	handler, err := New(context.TODO(), http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		upstreamRequest = req.Clone(req.Context())
		rw.Header().Set("X-Upstream", "ok")
		rw.WriteHeader(http.StatusCreated)
		_, _ = rw.Write([]byte("body"))
	}), config, "test")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "https://example.com/demo?foo=bar", nil)
	req.Header.Set("XAuthUser", "luis") //nolint:canonicalheader
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if upstreamRequest == nil {
		t.Fatal("upstream request was not captured")
	}

	requestID := upstreamRequest.Header.Get("X-Request-Id")
	if _, err := uuid.Parse(requestID); err != nil {
		t.Fatalf("request UUID %q is invalid: %v", requestID, err)
	}

	if got := upstreamRequest.Header.Get("X-User"); got != "luis" {
		t.Fatalf("X-User = %q, want %q", got, "luis")
	}

	if got := upstreamRequest.Header.Get("X-User-Optional"); got != "" {
		t.Fatalf("X-User-Optional = %q, want empty", got)
	}

	if got := upstreamRequest.Header.Get("X-Request-Template"); got != "req:POST /demo luis" {
		t.Fatalf("X-Request-Template = %q", got)
	}

	if got := rec.Header().Get("X-Plugin"); got != "headers-enricher" {
		t.Fatalf("X-Plugin = %q", got)
	}

	if got := rec.Header().Get("X-Response-User"); got != "luis" {
		t.Fatalf("X-Response-User = %q", got)
	}

	if got := rec.Header().Get("X-Response-Plugin-Copy"); got != "headers-enricher" {
		t.Fatalf("X-Response-Plugin-Copy = %q", got)
	}

	if got := rec.Header().Get("X-Response-Template"); got != "res:201 example.com https://example.com/demo?foo=bar" {
		t.Fatalf("X-Response-Template = %q", got)
	}

	if got := rec.Code; got != http.StatusCreated {
		t.Fatalf("status code = %d, want %d", got, http.StatusCreated)
	}

	if got := rec.Body.String(); got != "body" {
		t.Fatalf("body = %q, want %q", got, "body")
	}
}

func TestMiddlewareSupportsRequestHeader(t *testing.T) {
	config := &Config{
		Headers: map[string]any{
			"request": map[string]any{
				"X-User": map[string]any{
					"from":    "request.header",
					"name":    "XAuthUser",
					"default": "anonymous",
				},
			},
		},
	}

	handler, err := New(context.TODO(), http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if got := req.Header.Get("X-User"); got != "legacy" {
			t.Fatalf("X-User = %q", got)
		}
		rw.WriteHeader(http.StatusOK)
	}), config, "test")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	req.Header.Set("XAuthUser", "legacy") //nolint:canonicalheader
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
}

func TestMiddlewareSupportsNowAndQueryContext(t *testing.T) {
	config := &Config{
		Headers: map[string]any{
			"request": map[string]any{
				"X-Now": map[string]any{
					"from": "now",
				},
				"X-Query-Value": map[string]any{
					"from":  "template",
					"value": "[[ .Query.foo ]]|[[ .QueryString ]]|[[ .Scheme ]]|[[ .ClientIP ]]",
				},
			},
			"response": map[string]any{
				"X-Response-Context": map[string]any{
					"from":  "template",
					"value": "[[ .StatusCode ]]|[[ .Method ]]|[[ .Host ]]",
				},
			},
		},
	}

	var upstreamRequest *http.Request
	handler, err := New(context.TODO(), http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		upstreamRequest = req.Clone(req.Context())
		rw.WriteHeader(http.StatusNoContent)
	}), config, "test")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com/path?foo=bar&foo=baz", nil)
	req.RemoteAddr = "203.0.113.7:1234"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if upstreamRequest == nil {
		t.Fatal("upstream request was not captured")
	}

	nowHeader := upstreamRequest.Header.Get("X-Now")
	if _, err := time.Parse(time.RFC3339, nowHeader); err != nil {
		t.Fatalf("X-Now = %q is not RFC3339: %v", nowHeader, err)
	}

	queryHeader := upstreamRequest.Header.Get("X-Query-Value")
	if !strings.Contains(queryHeader, "bar|foo=bar&foo=baz|http|203.0.113.7") {
		t.Fatalf("X-Query-Value = %q", queryHeader)
	}

	if got := rec.Header().Get("X-Response-Context"); got != "204|GET|example.com" {
		t.Fatalf("X-Response-Context = %q", got)
	}
}

func TestMiddlewareExposesEnvironmentToTemplates(t *testing.T) {
	t.Setenv("NODE_NAME", "worker-01")

	config := &Config{
		AllowedEnv: []string{"NODE_NAME"},
		Headers: map[string]any{
			"request": map[string]any{
				"X-Node-Name": map[string]any{
					"from":  "template",
					"value": "[[ .Env.NODE_NAME ]]",
				},
				"X-Environment-Count": map[string]any{
					"from":  "template",
					"value": "[[ len .Env ]]",
				},
			},
		},
	}

	var upstreamRequest *http.Request
	handler, err := New(context.TODO(), http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		upstreamRequest = req.Clone(req.Context())
		rw.WriteHeader(http.StatusOK)
	}), config, "test")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if upstreamRequest == nil {
		t.Fatal("upstream request was not captured")
	}

	if got := upstreamRequest.Header.Get("X-Node-Name"); got != "worker-01" {
		t.Fatalf("X-Node-Name = %q, want %q", got, "worker-01")
	}

	if got := upstreamRequest.Header.Get("X-Environment-Count"); got == "" || got == "0" {
		t.Fatalf("X-Environment-Count = %q, want non-zero value", got)
	}
}

func TestAllowedEnvRestrictsTemplateAccess(t *testing.T) {
	t.Setenv("ALLOWED_VAR", "visible")
	t.Setenv("SECRET_VAR", "hidden")

	config := &Config{
		AllowedEnv: []string{"ALLOWED_VAR"},
		Headers: map[string]any{
			"request": map[string]any{
				"X-Allowed": map[string]any{
					"from":  "template",
					"value": "[[ .Env.ALLOWED_VAR ]]",
				},
				"X-Secret": map[string]any{
					"from":  "template",
					"value": "[[ .Env.SECRET_VAR ]]",
				},
			},
		},
	}

	var captured *http.Request
	handler, err := New(context.TODO(), http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		captured = req.Clone(req.Context())
		rw.WriteHeader(http.StatusOK)
	}), config, "test")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if got := captured.Header.Get("X-Allowed"); got != "visible" {
		t.Fatalf("X-Allowed = %q, want %q", got, "visible")
	}
	if got := captured.Header.Get("X-Secret"); got != "" {
		t.Fatalf("X-Secret = %q, want empty (SECRET_VAR not in allowlist)", got)
	}
}

func TestAllowedEnvDefaultExposesOnlyHostname(t *testing.T) {
	t.Setenv("HOSTNAME", "test-host")
	t.Setenv("NODE_NAME", "node-01")

	config := &Config{
		// no AllowedEnv — default exposes only HOSTNAME
		Headers: map[string]any{
			"request": map[string]any{
				"X-Hostname": map[string]any{
					"from":  "template",
					"value": "[[ .Env.HOSTNAME ]]",
				},
				"X-Node": map[string]any{
					"from":  "template",
					"value": "[[ .Env.NODE_NAME ]]",
				},
			},
		},
	}

	var captured *http.Request
	handler, err := New(context.TODO(), http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		captured = req.Clone(req.Context())
		rw.WriteHeader(http.StatusOK)
	}), config, "test")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if got := captured.Header.Get("X-Hostname"); got != "test-host" {
		t.Fatalf("X-Hostname = %q, want %q (HOSTNAME is in the default allowlist)", got, "test-host")
	}
	if got := captured.Header.Get("X-Node"); got != "" {
		t.Fatalf("X-Node = %q, want empty (NODE_NAME not in default allowlist)", got)
	}
}

func TestEnvRuleMissingVarDoesNotFailAndLeavesHeaderAbsent(t *testing.T) {
	// Ensure the var is definitely absent (not just empty).
	t.Setenv("DEFINITELY_MISSING_VAR", "")

	config := &Config{
		Headers: map[string]any{
			"request": map[string]any{
				"X-Missing": map[string]any{
					"from": "env",
					"name": "DEFINITELY_MISSING_VAR",
					// no default — warning is emitted to stderr at startup
				},
			},
		},
	}

	var captured *http.Request
	handler, err := New(context.TODO(), http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		captured = req.Clone(req.Context())
		rw.WriteHeader(http.StatusOK)
	}), config, "test-warn")
	if err != nil {
		t.Fatalf("New() must not fail for a missing env var, got error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if got := captured.Header.Get("X-Missing"); got != "" {
		t.Fatalf("X-Missing = %q, want empty when env var is not set", got)
	}
}

func TestEnvRuleMissingVarRemovesExistingHeader(t *testing.T) {
	t.Setenv("DEFINITELY_MISSING_VAR", "")

	config := &Config{
		Headers: map[string]any{
			"request": map[string]any{
				"X-Forwarded-Server": map[string]any{
					"from": "env",
					"name": "DEFINITELY_MISSING_VAR",
				},
			},
		},
	}

	var captured *http.Request
	handler, err := New(context.TODO(), http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		captured = req.Clone(req.Context())
		rw.WriteHeader(http.StatusOK)
	}), config, "test-preserve")
	if err != nil {
		t.Fatalf("New() must not fail for a missing env var, got error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	req.Header.Set("X-Forwarded-Server", "traefik-original")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if got := captured.Header.Get("X-Forwarded-Server"); got != "" {
		t.Fatalf("X-Forwarded-Server = %q, want header removed when env var is missing", got)
	}
}

func TestNewRejectsInvalidHeadersSections(t *testing.T) {
	config := &Config{
		Headers: map[string]any{
			"request": "invalid",
		},
	}

	_, err := New(context.TODO(), http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {}), config, "test")
	if err == nil {
		t.Fatal("expected error for invalid request section")
	}
}

func TestNewRejectsMissingNameForRequestHeaderSource(t *testing.T) {
	config := &Config{
		Headers: map[string]any{
			"request": map[string]any{
				"X-User": map[string]any{
					"from": "request.header",
				},
			},
		},
	}

	_, err := New(context.TODO(), http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {}), config, "test")
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestNewRejectsMissingValueForTemplateSource(t *testing.T) {
	config := &Config{
		Headers: map[string]any{
			"request": map[string]any{
				"X-Template": map[string]any{
					"from": "template",
				},
			},
		},
	}

	_, err := New(context.TODO(), http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {}), config, "test")
	if err == nil {
		t.Fatal("expected error for missing value")
	}
}

func TestNewRejectsFlatHeadersConfig(t *testing.T) {
	config := &Config{
		Headers: map[string]any{
			"X-User": map[string]any{
				"from": "request.header",
				"name": "XAuthUser",
			},
		},
	}

	_, err := New(context.TODO(), http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {}), config, "test")
	if err == nil {
		t.Fatal("expected error for flat headers config")
	}
}
