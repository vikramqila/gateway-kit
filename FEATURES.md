# GatewayKit Features

This document maps the sample configuration in [gateway.yaml](gateway.yaml) to the current
implementation. It also calls out which schema fields are parsed but intentionally deferred.

## Request Pipeline

```text
HTTP request
  -> GET /health short-circuit
  -> longest-prefix route match
  -> method guard
  -> API key auth
  -> rate limit check
  -> timeout selection
  -> upstream selection
  -> retrying proxy transport
  -> downstream response
```

Primary code paths:

- [cmd/gatewaykit/main.go](cmd/gatewaykit/main.go): CLI entrypoint, config loading, server binding.
- [internal/config/config.go](internal/config/config.go): YAML schema, parsing, and validation.
- [internal/gateway/gateway.go](internal/gateway/gateway.go): request pipeline, routing, auth, rate limits, health, and errors.
- [internal/gateway/ratelimit.go](internal/gateway/ratelimit.go): fixed-window and sliding-window limiters.
- [internal/proxy/proxy.go](internal/proxy/proxy.go): upstream selection, path rewriting, retries, timeouts, and forwarding.
- [cmd/mockupstream/main.go](cmd/mockupstream/main.go): manual mock upstream service.
- [internal/e2e/gateway_e2e_test.go](internal/e2e/gateway_e2e_test.go): real HTTP E2E coverage using in-process mock upstreams.

## Implemented Features

### Configuration Loading and Validation

Configured by:

```yaml
gateway:
  port: 8080
  global_timeout: "30s"
  routes: [...]
```

Behavior:

- Loads YAML from `--config` or `GATEWAY_CONFIG`.
- Validates port range, route paths, uppercase methods, duration strings, upstream shape, retry settings, rate-limit settings, auth settings, health-check settings, and circuit-breaker settings.
- Rejects routes that define both `upstream.url` and `upstream.targets`.
- Rejects routes that define neither `upstream.url` nor `upstream.targets`.

Tests:

- [internal/config/config_test.go](internal/config/config_test.go)

### Server Startup and Health

Configured by:

```yaml
gateway:
  port: 8080
```

Behavior:

- Starts an HTTP server on the configured port.
- Exposes `GET /health` independently of route configuration.
- Returns JSON containing `status: healthy` and `uptime_seconds`.

Tests:

- [internal/gateway/gateway_test.go](internal/gateway/gateway_test.go)
- [internal/e2e/gateway_e2e_test.go](internal/e2e/gateway_e2e_test.go)

### Route Matching and Method Guarding

Configured by:

```yaml
routes:
  - path: "/api/users"
    methods: ["GET", "POST"]
```

Behavior:

- Uses longest-prefix matching.
- A route matches either the exact path or a slash-bounded child path, so `/api/users/42`
  matches `/api/users`, but `/api/users2` does not.
- Returns `404 Not Found` when no route matches.
- Returns `405 Method Not Allowed` and an `Allow` header when the path matches but the method does not.

Tests:

- [internal/gateway/gateway_test.go](internal/gateway/gateway_test.go)
- [internal/e2e/gateway_e2e_test.go](internal/e2e/gateway_e2e_test.go)

### Proxying to a Single Upstream

Configured by:

```yaml
upstream:
  url: "http://localhost:3001"
```

Behavior:

- Forwards method, path, query string, headers, and body.
- Copies upstream response status, headers, and body back to the client.
- Filters hop-by-hop headers.
- Appends `X-Forwarded-For` using the client address.
- Returns `502 Bad Gateway` when the upstream cannot be reached.

Tests:

- [internal/proxy/proxy_test.go](internal/proxy/proxy_test.go)
- [internal/gateway/gateway_test.go](internal/gateway/gateway_test.go)
- [internal/e2e/gateway_e2e_test.go](internal/e2e/gateway_e2e_test.go)

### Prefix Stripping

Configured by:

```yaml
path: "/api/products"
strip_prefix: true
```

Behavior:

- When `strip_prefix: false`, forwards the original request path.
- When `strip_prefix: true`, removes the matched route path before forwarding.
- Example: `/api/products/sku-123` becomes `/sku-123`.
- Exact route path matches become `/` after stripping.

Tests:

- [internal/proxy/proxy_test.go](internal/proxy/proxy_test.go)
- [internal/e2e/gateway_e2e_test.go](internal/e2e/gateway_e2e_test.go)

### Global and Route-Level Timeouts

Configured by:

```yaml
global_timeout: "30s"

routes:
  - path: "/api/orders"
    timeout: "5s"
```

Behavior:

- Route-level `timeout` overrides `global_timeout`.
- If no route-level timeout exists, the global timeout is used.
- Timeouts are enforced with a request context passed into the upstream HTTP call.
- Returns `504 Gateway Timeout` when the upstream exceeds the selected timeout.

Tests:

- [internal/gateway/gateway_test.go](internal/gateway/gateway_test.go)
- [internal/e2e/gateway_e2e_test.go](internal/e2e/gateway_e2e_test.go)

### API Key Authentication

Configured by:

```yaml
auth:
  type: "api_key"
  header: "X-API-Key"
  keys: ["sk_live_abc123", "sk_live_def456"]
```

Behavior:

- Only `api_key` auth is supported.
- Reads the configured header.
- Rejects missing or invalid keys with `401 Unauthorized`.
- Allows valid keys through to the upstream.

Tests:

- [internal/gateway/gateway_test.go](internal/gateway/gateway_test.go)
- [internal/e2e/gateway_e2e_test.go](internal/e2e/gateway_e2e_test.go)

### Rate Limiting

Configured by:

```yaml
global_rate_limit:
  requests: 100
  window: "60s"
  strategy: "fixed_window"
  per: "ip"

routes:
  - path: "/api/users"
    rate_limit:
      requests: 30
      window: "60s"
      strategy: "sliding_window"
      per: "ip"
```

Behavior:

- Route-level rate limits override the global rate limit.
- Supports `fixed_window` and `sliding_window`.
- Supports `per: ip` and `per: global` buckets.
- Uses `X-Forwarded-For` when present, otherwise `RemoteAddr`.
- Stores limiter state in memory.
- Returns `429 Too Many Requests` when a request exceeds the selected limit.

Tests:

- [internal/gateway/ratelimit_test.go](internal/gateway/ratelimit_test.go)
- [internal/gateway/gateway_test.go](internal/gateway/gateway_test.go)
- [internal/e2e/gateway_e2e_test.go](internal/e2e/gateway_e2e_test.go)

### Retries

Configured by:

```yaml
retry:
  attempts: 3
  backoff: "exponential"
  initial_delay: "1s"
  on: [502, 503, 504]
```

Behavior:

- Buffers the downstream request body once so retries can preserve the original payload.
- Retries upstream transport errors while attempts remain and the timeout context is still active.
- Retries upstream status codes listed in `retry.on`.
- Supports `fixed` and `exponential` backoff.
- Does not retry status codes that are not listed in `retry.on`.

Tests:

- [internal/proxy/proxy_test.go](internal/proxy/proxy_test.go)
- [internal/e2e/gateway_e2e_test.go](internal/e2e/gateway_e2e_test.go)

### Multiple Upstreams and Load Balancing

Configured by:

```yaml
upstream:
  targets:
    - url: "http://localhost:3003"
      weight: 3
    - url: "http://localhost:3004"
      weight: 1
  balance: "weighted_round_robin"
```

Behavior:

- Supports `round_robin`.
- Supports `weighted_round_robin`.
- Keeps target selection counters in memory per route.
- Does not yet skip unhealthy targets because active backend health checks are deferred.

Tests:

- [internal/proxy/proxy_test.go](internal/proxy/proxy_test.go)
- [internal/gateway/gateway_test.go](internal/gateway/gateway_test.go)
- [internal/e2e/gateway_e2e_test.go](internal/e2e/gateway_e2e_test.go)

### Mock Upstream Server

Implemented by:

- [cmd/mockupstream/main.go](cmd/mockupstream/main.go)

Behavior:

- Starts a simple upstream server for manual testing.
- Supports `--port` and `--name` flags, with environment-variable fallbacks.
- Provides:
  - `GET /healthz`
  - `GET /echo?x=1`
  - `GET /slow`
  - `GET /flaky`
  - canned JSON for all other paths

Manual sample:

```bash
go run ./cmd/mockupstream --port 3001 --name users
```

## Parsed But Deferred Features

These fields exist in the configuration schema and validation layer, but are not enforced by
the runtime gateway pipeline yet.

### Request and Response Transforms

Configured by:

```yaml
request_transform:
  headers:
    add:
      X-Gateway: "gatewaykit"
    remove: ["X-Debug"]
  body:
    mapping:
      user.id: "userId"

response_transform:
  headers:
    add:
      X-Served-By: "gatewaykit"
    remove: ["Server"]
  body:
    envelope:
      data: "$body"
```

Current status:

- Parsed into `RequestTransform` and `ResponseTransform`.
- Not applied to request headers, request bodies, response headers, or response bodies.

### Backend Health Checks

Configured by:

```yaml
health_check:
  path: "/healthz"
  interval: "30s"
  unhealthy_threshold: 3
```

Current status:

- Parsed and validated.
- The gateway exposes its own `GET /health`.
- The gateway does not run background health sweeps against backend targets.
- Load balancing does not skip unhealthy targets.

### Circuit Breaker

Configured by:

```yaml
circuit_breaker:
  threshold: 5
  window: "60s"
  cooldown: "30s"
```

Current status:

- Parsed and validated.
- Not integrated into the request lifecycle.
- The gateway does not track failure windows, open circuits, half-open trial requests, or cooldowns.

## Verification

Run all tests:

```bash
go test ./...
```

Run E2E tests only:

```bash
go test ./internal/e2e -v
```

Confirm all packages compile:

```bash
go build ./...
```

## Manual End-to-End Run

Start the sample upstreams:

```bash
go run ./cmd/mockupstream --port 3001 --name users
go run ./cmd/mockupstream --port 3002 --name orders
go run ./cmd/mockupstream --port 3003 --name products-a
go run ./cmd/mockupstream --port 3004 --name products-b
go run ./cmd/mockupstream --port 3005 --name legacy
go run ./cmd/mockupstream --port 3006 --name internal
```

Start the gateway:

```bash
go run ./cmd/gatewaykit --config gateway.yaml
```

Try representative requests:

```bash
curl -i 'http://localhost:8080/health'
curl -i 'http://localhost:8080/api/users/echo?x=1'
curl -i -X DELETE 'http://localhost:8080/api/users'
curl -i 'http://localhost:8080/api/products/sku-123'
curl -i 'http://localhost:8080/api/internal/echo'
curl -i -H 'X-API-Key: sk_live_abc123' 'http://localhost:8080/api/internal/echo'
```

