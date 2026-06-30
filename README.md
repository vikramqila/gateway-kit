# GatewayKit

GatewayKit is a lightweight, config-driven API gateway take-home project.

This repository is being developed in small, reviewable milestones. The first milestone
sets up the project structure, sample configuration, and documentation placeholders. Gateway
behavior will be added in subsequent commits.

## Planned Setup

Prerequisite:

- Go 1.22 or newer

Run the gateway:

```bash
go run ./cmd/gatewaykit --config gateway.yaml
```

Run tests:

```bash
go test ./...
```

## Planned Feature Checklist

- [x] Load gateway configuration from YAML
- [x] Expose `GET /health`
- [x] Match routes and enforce allowed methods
- [x] Proxy requests to single upstream routes
- [x] Support prefix stripping
- [x] Support global and route-level timeouts
- [x] Support API key authentication
- [x] Support fixed-window rate limiting
- [x] Support retries for transient upstream failures
- [x] Support multiple upstream targets

Current CLI output after a successful startup:

```text
GatewayKit listening on :8080 with 5 routes
```

Single `upstream.url` routes now proxy to their upstream service. Routes using
`upstream.targets` still return `501 Not Implemented` until load balancing is added. If a
configured upstream service is not running, the gateway returns `502 Bad Gateway`. Upstream
requests honor route-level `timeout` first, then `global_timeout`, and return `504 Gateway
Timeout` when exceeded.

Routes configured with `auth.type: api_key` require the configured header to contain one of
the configured keys before proxying.

Fixed-window rate limits are enforced in memory. Route-level limits override the global
limit, and `per: ip` and `per: global` buckets are supported. `sliding_window` is parsed but
not enforced yet.

Single-upstream routes with `retry` retry configured upstream status codes using fixed or
exponential backoff. Request bodies are buffered so retried requests preserve the original
payload.

Routes using `upstream.targets` are forwarded with `round_robin` or `weighted_round_robin`
selection. Target selection is kept in memory per route.

## Project Layout

- `cmd/gatewaykit`: CLI entrypoint for the gateway process
- `internal/config`: configuration loading and validation
- `internal/gateway`: HTTP server, routing, and request pipeline
- `internal/proxy`: upstream request forwarding
- `gateway.yaml`: sample configuration from the take-home prompt
- `DECISIONS.md`: implementation choices, trade-offs, and deferred work
