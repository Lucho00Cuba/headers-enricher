# AGENTS.md

Context for AI coding agents working in this repository.

## What this project is

A [Traefik](https://traefik.io) middleware plugin written in Go. It enriches HTTP request and response headers from multiple sources: UUID generation, timestamps, environment variables, incoming headers, upstream response headers, and Go templates.

The plugin is published to the Traefik Plugin Catalog under the import path `github.com/lucho00cuba/headers-enricher`. The entry point Traefik calls is `New()` in `headers_enricher.go`.

**Important**: Traefik executes plugins through the [Yaegi](https://github.com/traefik/yaegi) Go interpreter, not native Go. This means only a subset of the standard library and no CGO. Do not introduce packages or language features unsupported by Yaegi — always validate with `task yaegi:test`.

## Repository layout

```
headers_enricher.go           # all plugin logic (single file)
headers_enricher_test.go      # all tests (single file)
.traefik.yml                  # plugin catalog metadata + testData used by Traefik validation
dynamic.yaml                  # local dev dynamic config (Docker Compose)
docker-compose.yaml           # local dev environment (Traefik + whoami)
api.http                      # HTTP request examples for manual testing (REST client)
go.mod / go.sum               # module: github.com/lucho00cuba/headers-enricher
hooks/pre-commit              # git pre-commit hook (install with: task hooks:install)
scripts/changelog.sh          # generates CHANGELOG.md from git log + tags
kubernetes/                   # minikube + Helm dev environment
vendor/                       # vendored dependencies (go mod vendor)
.golangci.yml                 # golangci-lint configuration
.github/workflows/ci.yaml     # CI: lint + test + yaegi test (runs on push to main and PRs)
.github/workflows/go-cross.yaml   # CI: cross-platform Go tests
.github/workflows/release.yaml   # CI: release automation
```

## Yaegi constraints

Traefik loads plugins via the Yaegi interpreter. Consequences:

- Only packages present in the Yaegi standard-library allowlist can be imported. Current imports (`text/template`, `sync`, `bytes`, `net/http`, etc.) are all safe; verify any new import with `task yaegi:test`.
- No CGO, no `unsafe`, no `init()` side-effects that rely on native linking.
- The Go version used in CI is **Go 1.25** — use only language features available in that version.
- Always run `task yaegi:test` after adding or changing imports. CI will also catch this via `yaegi test -tags yaegi -v .`.

## Key architectural decisions

- **Single-file plugin**: Traefik's plugin loader restricts what is allowed. All logic lives in `headers_enricher.go` — do not split it into packages.
- **Rules compile once at startup**: `compileRule` returns a `func(TemplateContext) (string, error)` closure. No parsing happens per-request.
- **No buffering**: `streamingResponseWriter` intercepts headers and status code but streams the body directly. Never buffer the response body.
- **Template delimiters are `[[ ]]`**, not `{{ }}`, to avoid collisions with Traefik's file-provider templating.
- **`allowedEnv` is the single authority** for env var access — both `from: env` rules and `.Env` in templates read from the same pre-filtered map built at startup.
- **Fail-safe**: rule errors are logged but never returned; a bad rule never takes down a request.
- **`bufPool`** (`sync.Pool`) reuses `bytes.Buffer` across template executions to reduce allocations.

## Development workflow

```bash
# Run tests
go test ./...
go test -race ./...
task test:coverage          # tests + coverage report (coverage.out)

# Linting
task lint                   # runs golangci-lint via Docker

# Yaegi (run after any import or logic change)
task yaegi:install          # install Yaegi interpreter once
task yaegi:test             # run tests under Yaegi (mirrors CI)

# Local dev environment
task docker:up              # start Traefik + whoami
task docker:restart         # force-recreate Traefik to reload plugin
task docker:down

# Run CI workflows locally with act
task act:lint               # run ci.yaml locally
task act:test               # run go-cross.yaml locally
task act                    # run all workflows

# Changelog
task changelog              # regenerate CHANGELOG.md
task changelog:preview      # dry-run, no file written

# Install git hooks (run once after cloning)
task hooks:install
```

The pre-commit hook regenerates `CHANGELOG.md` automatically on every commit (except the initial one).

## Testing conventions

- Tests are table-driven and use `net/http/httptest`.
- The test handler (`http.HandlerFunc`) is defined inline per test case.
- Assertions use `t.Errorf` (not `t.Fatalf`) unless the test cannot proceed.
- No mocks — the middleware is tested end-to-end by driving it with a real `httptest.ResponseRecorder`.

## Commit conventions

This project uses [Conventional Commits](https://www.conventionalcommits.org/):

```
feat:      new capability
fix:       bug fix
perf:      performance improvement
refactor:  internal restructuring, no behavior change
docs:      documentation only
chore:     tooling, deps, config
ci:        CI/CD changes
test:      test-only changes
security:  security fix
```

The `scripts/changelog.sh` groups commits into changelog sections based on these prefixes.

## Things to be careful about

- **`from: response.header` is invalid in request rules** — the check is in `New()` and returns an error at startup.
- **`allowedEnv` defaults to `["HOSTNAME"]` when empty** — never expose all env vars; secrets like tokens and passwords are commonly present.
- **`flattenHeaders` stores both original-case and lower-case keys** — template lookups by either case work, but be aware values are duplicated in the map.
- **Vendor directory must stay in sync** — run `go mod vendor` after any dependency change.
- **Do not add new top-level packages** — Traefik's plugin sandbox only loads the root package.
