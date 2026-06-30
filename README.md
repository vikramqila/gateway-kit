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
- [ ] Proxy requests to single upstream routes
- [ ] Support prefix stripping
- [ ] Support global and route-level timeouts
- [ ] Support API key authentication
- [ ] Support fixed-window rate limiting
- [ ] Support retries for transient upstream failures
- [ ] Support multiple upstream targets

Current CLI output after a successful startup:

```text
GatewayKit listening on :8080 with 5 routes
```

Implemented route matches currently return `501 Not Implemented` until proxying is added.

## Project Layout

- `cmd/gatewaykit`: CLI entrypoint for the gateway process
- `internal/config`: configuration loading and validation
- `internal/gateway`: HTTP server, routing, and request pipeline
- `internal/proxy`: upstream request forwarding
- `gateway.yaml`: sample configuration from the take-home prompt
- `DECISIONS.md`: implementation choices, trade-offs, and deferred work
