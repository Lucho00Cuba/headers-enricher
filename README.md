# Headers Enricher Middleware

[![Lint & Test](https://github.com/Lucho00Cuba/headers-enricher/actions/workflows/ci.yaml/badge.svg)](https://github.com/Lucho00Cuba/headers-enricher/actions/workflows/ci.yaml)
[![Go Matrix](https://github.com/Lucho00Cuba/headers-enricher/actions/workflows/go-cross.yaml/badge.svg)](https://github.com/Lucho00Cuba/headers-enricher/actions/workflows/go-cross.yaml)
[![Go Version](https://img.shields.io/github/go-mod/go-version/lucho00cuba/headers-enricher)](https://github.com/Lucho00Cuba/headers-enricher)
[![License](https://img.shields.io/github/license/lucho00cuba/headers-enricher)](./LICENSE)
[![Last Commit](https://img.shields.io/github/last-commit/lucho00cuba/headers-enricher/main)](https://github.com/Lucho00Cuba/headers-enricher/commits/main)

<p align="center">
  <img src="assets/icon.png" alt="Headers Enricher" width="200" />
</p>

Traefik middleware plugin for generating, copying, and composing HTTP headers from request data, response data, templates, and container environment variables.

## Overview

The public API is centered on `from`.

Each header rule declares:

- `from`: where the value comes from
- `name`: the source key when the source needs one
- `value`: the literal or template body when the source needs inline content
- `default`: fallback when the source is missing or empty

This keeps the configuration predictable and avoids mixing styles such as `requestHeader`, `responseHeader`, and `template` at the top level.

## Quick Start

### Static Configuration

```yaml
experimental:
  localPlugins:
    headers-enricher:
      moduleName: github.com/lucho00cuba/headers-enricher
```

For catalog mode:

```yaml
experimental:
  plugins:
    headers-enricher:
      moduleName: github.com/lucho00cuba/headers-enricher
      version: v0.1.0
```

### Dynamic Configuration

```yaml
http:
  middlewares:
    headers-enricher:
      plugin:
        headers-enricher:
          allowedEnv:
            - NODE_NAME
            - REGION
          headers:
            request:
              X-Request-Id:
                from: uuid
              X-User:
                from: request.header
                name: XAuthUser
                default: anonymous
              X-Request-Meta:
                from: template
                value: '[[ .Method ]] [[ .Path ]] [[ .ClientIP ]]'
              X-Node-Name:
                from: env
                name: NODE_NAME
            response:
              X-Plugin:
                from: literal
                value: headers-enricher
              X-Plugin-Copy:
                from: response.header
                name: X-Plugin
              X-Response-Meta:
                from: template
                value: '[[ .StatusCode ]] [[ .Method ]] [[ .Host ]]'
```

### Docker Compose Example

```yaml
services:
  traefik:
    image: traefik:latest
    command:
      - "--providers.file.directory=/etc/traefik"
      - "--providers.file.watch=true"
      - "--experimental.localPlugins.headers-enricher.moduleName=github.com/lucho00cuba/headers-enricher"
    environment:
      - NODE_NAME=my-node-01
    volumes:
      - "./dynamic.yaml:/etc/traefik/dynamic.yml"
      - ".:/plugins-local/src/github.com/lucho00cuba/headers-enricher"
```

### Kubernetes (minikube + Helm)

The files in `kubernetes/` provide a ready-to-use development environment.

**Prerequisites:** `minikube`, `helm`, `kubectl`.

#### 1. Start minikube

```bash
minikube start
```

#### 2. Add the Traefik Helm repo

```bash
helm repo add traefik https://traefik.github.io/charts
helm repo update
```

#### 3. Install Traefik with the plugin enabled

```bash
helm install traefik traefik/traefik \
  --namespace traefik \
  --create-namespace \
  -f kubernetes/values.yaml
```

`kubernetes/values.yaml` declares the plugin under `experimental.plugins`:

```yaml
experimental:
  plugins:
    headers-enricher:
      moduleName: "github.com/lucho00cuba/headers-enricher"
      version: "v0.1.0"
```

Update `version` to match the release you want to use.

#### 4. Apply the middleware and the test workload

```bash
kubectl apply -f kubernetes/middleware.yaml
kubectl apply -f kubernetes/whoami.yaml
```

`kubernetes/middleware.yaml` creates a `Middleware` CRD in the `default` namespace.
`kubernetes/whoami.yaml` deploys a `whoami` pod with an `IngressRoute` that applies the middleware.

#### 5. Expose Traefik (separate terminal)

```bash
minikube tunnel
```

#### 6. Test

```bash
curl -H "Host: whoami.local" http://localhost
```

You should see the enriched headers (`X-Request-Id`, `X-Node-Name`, `X-Plugin`, etc.) in the response.

#### Using the middleware in your own IngressRoute

```yaml
apiVersion: traefik.io/v1alpha1
kind: IngressRoute
metadata:
  name: my-app
  namespace: default
spec:
  entryPoints:
    - web
  routes:
    - match: Host(`my-app.local`)
      kind: Rule
      services:
        - name: my-app
          port: 80
      middlewares:
        - name: headers-enricher
```

For cross-namespace references use `namespace: default` on the middleware ref:

```yaml
middlewares:
  - name: headers-enricher
    namespace: default
```

## Configuration Shape

```yaml
allowedEnv:
  - VAR_ONE
  - VAR_TWO
headers:
  request:
    Header-Name:
      from: ...
      name: ...
      value: ...
      default: ...
  response:
    Header-Name:
      from: ...
      name: ...
      value: ...
      default: ...
```

Each rule must be declared as an object inside either `request` or `response`.

## Source Reference

### `from: uuid`

Generates one UUID per request. The same UUID is reused by request and response rules for that request.

```yaml
X-Request-Id:
  from: uuid
```

### `from: now`

Emits the request timestamp in RFC3339 UTC format.

```yaml
X-Generated-At:
  from: now
```

### `from: literal`

Uses `value` as a literal string.

```yaml
X-Plugin:
  from: literal
  value: headers-enricher
```

### `from: template`

Renders `value` as a Go `text/template` using `[[ ... ]]` delimiters.

```yaml
X-Request-Meta:
  from: template
  value: '[[ .Method ]] [[ .Path ]] [[ .UUID ]]'
```

### `from: env`

Reads an environment variable captured at plugin startup.

```yaml
X-Node-Name:
  from: env
  name: NODE_NAME
```

### `from: request.header`

Copies a header from the incoming request.

```yaml
X-User:
  from: request.header
  name: XAuthUser
  default: anonymous
```

### `from: response.header`

Copies a header from the upstream response. Only valid in the `response` section.

```yaml
X-Plugin-Copy:
  from: response.header
  name: X-Plugin
```

## Field Reference

### `from`

Supported values:

- `uuid`
- `now`
- `literal`
- `template`
- `env`
- `request.header`
- `response.header`

### `name`

Used by:

- `env`
- `request.header`
- `response.header`

### `value`

Used by:

- `literal`
- `template`

### `default`

Fallback used when the selected source is missing or resolves to an empty string.

Applies to:

- `env`
- `request.header`
- `response.header`

### `allowedEnv`

Top-level list of environment variable names exposed to templates via `.Env`.

```yaml
allowedEnv:
  - NODE_NAME
  - REGION
  - APP_VERSION
```

When `allowedEnv` is absent or empty, only `HOSTNAME` is exposed by default. When set, only the listed variables are populated in `.Env`; any other variable resolves to an empty string in templates.

This secure-by-default behavior prevents accidental exposure of secrets (tokens, passwords, API keys) that are commonly injected as environment variables in containerised workloads.

`allowedEnv` also governs `from: env` rules. A variable not listed in `allowedEnv` will not be accessible via `from: env` and will resolve to the rule's `default` (or empty string if no default is set).

## Template Syntax

Templates use `[[` and `]]` delimiters.

```yaml
X-Request-Meta:
  from: template
  value: '[[ .Method ]] [[ .Path ]]'
```

This avoids collisions with Traefik file-provider templating, which uses `{{ ... }}`.

Built-in helper functions:

- `lower`
- `upper`
- all standard `text/template` built-ins such as `and`, `or`, `not`, `len`, `eq`, `ne`, `index`, and `printf`

Examples:

```yaml
X-User-Template:
  from: template
  value: '[[ or (index .RequestHeaders "xauthuser") "guest" ]]'

X-Method-Lower:
  from: template
  value: '[[ lower .Method ]]'

X-Env-Count:
  from: template
  value: '[[ len .Env ]]'
```

Missing map keys (absent headers, unset env vars) resolve to an empty string. In that case the plugin removes the target header rather than setting it to a literal `<no value>`.

## Template Context Reference

### Request Metadata

- `UUID string`
- `Method string`
- `Host string`
- `Scheme string`
- `Path string`
- `RequestURI string`
- `QueryString string`
- `ClientIP string`
- `Now time.Time`

### Headers

- `RequestHeaders map[string]string`
- `ResponseHeaders map[string]string`

Notes:

- header maps expose both original-case and lower-case keys
- only the first value of a multi-value header is exposed

Examples:

```yaml
value: '[[ index .RequestHeaders "xauthuser" ]]'
value: '[[ .ResponseHeaders.X-Plugin ]]'
```

### Query

- `Query map[string]string`

Only the first value of each query parameter is exposed.

```yaml
value: '[[ .Query.foo ]]'
```

### Response Metadata

- `StatusCode int`

```yaml
value: 'status=[[ .StatusCode ]]'
```

### Environment

- `Env map[string]string`

Keyed access to environment variables. Populated according to `allowedEnv`.

```yaml
value: '[[ .Env.NODE_NAME ]]'
value: '[[ .Env.HOSTNAME ]]'
```

Notes:

- values come from the Traefik process environment, captured once at startup
- in containers, that means the Traefik container environment
- in Kubernetes, these variables must be injected into the Traefik pod
- only `HOSTNAME` is exposed when `allowedEnv` is not configured (secure by default)
- use `allowedEnv` to explicitly declare which additional variables templates may access

## Examples

### Generate a Request ID

```yaml
X-Request-Id:
  from: uuid
```

### Copy a Request Header with Fallback

```yaml
X-User:
  from: request.header
  name: XAuthUser
  default: anonymous
```

### Compose a Header with a Template

```yaml
X-Request-Meta:
  from: template
  value: '[[ .Method ]] [[ .Path ]] [[ .UUID ]] [[ .ClientIP ]]'
```

### Read a Container Environment Variable

```yaml
X-Node-Name:
  from: env
  name: NODE_NAME
```

### Copy a Response Header Generated Earlier

```yaml
response:
  X-Plugin:
    from: literal
    value: headers-enricher
  X-Plugin-Copy:
    from: response.header
    name: X-Plugin
```

### Restrict Environment Exposure

```yaml
allowedEnv:
  - NODE_NAME
  - REGION
headers:
  request:
    X-Node:
      from: template
      value: '[[ .Env.NODE_NAME ]]'
```

With this configuration `.Env.DB_PASSWORD` and any other variable not listed in `allowedEnv` resolve to an empty string in templates.

## Execution Model

For each request, the middleware:

1. Builds a request context (UUID, timestamp, headers, query, client IP).
2. Applies all `request` header rules to `req.Header`.
3. Calls the upstream handler.
4. Once the upstream commits its status and headers, applies all `response` header rules.
5. Streams the response body directly to the client without buffering.

Behavior details:

- request rules run before the upstream service receives the request
- response rules run after the upstream commits its status code and headers, but before the body reaches the client
- the response body is never buffered; streaming responses, SSE, and large downloads work normally
- if a resolved value is empty, the target header is removed
- templates are compiled once when the middleware instance is created
- `from: env` values are captured once at startup; they do not reflect runtime changes to the process environment
- `from: response.header` is only valid in the `response` section; using it in `request` rules is rejected at startup

## API Reference Summary

```go
type Config struct {
    Headers    map[string]interface{} `json:"headers,omitempty"`
    AllowedEnv []string               `json:"allowedEnv,omitempty"`
}

type HeaderRule struct {
    From    string `json:"from,omitempty"`
    Name    string `json:"name,omitempty"`
    Value   string `json:"value,omitempty"`
    Default string `json:"default,omitempty"`
}
```

## Development

Run tests:

```bash
go test ./...
```

Run tests with race detector:

```bash
go test -race ./...
```

## License

This project is licensed under the MIT License. See the [LICENSE](./LICENSE) file for more details.
