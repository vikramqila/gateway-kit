# GatewayKit Decisions

This document explains how I prioritized the implementation, how the gateway is structured,
what trade-offs I made, and what I would build next.

## Prioritization

I started with the non-negotiable baseline requirements:

1. Load configuration from a YAML file.
2. Start an HTTP server on the configured port.
3. Serve `GET /health` independently of route configuration.
4. Match configured routes and enforce allowed methods.
5. Proxy requests to upstream services.
6. Prove the behavior with a self-contained test suite.

After that baseline was solid, I prioritized features that demonstrate production gateway
thinking while staying testable in a short time box:

1. Prefix stripping and timeouts, because they affect the core proxy path.
2. API key auth, because it is a small but important request-gating middleware.
3. Rate limiting, because it exercises concurrency and shared in-memory state.
4. Retries, because they add resilience and force careful request-body handling.
5. Multiple upstream targets, because they demonstrate extensible upstream selection.
6. Sliding-window rate limiting, as a stretch goal once fixed-window support was stable.

I deferred request/response transformation, health checks, and circuit breakers because each
deserves careful behavior design and would have expanded the surface area substantially.

## Architecture

The gateway is structured as a small request pipeline:

```mermaid
flowchart TD
    A["HTTP request"] --> B{"GET /health?"}
    B -- "yes" --> C["Return 200 healthy"]
    B -- "no" --> D{"Route match?"}
    D -- "no" --> E["Return 404 not_found"]
    D -- "yes" --> F{"Method allowed?"}
    F -- "no" --> G["Return 405 with Allow header"]
    F -- "yes" --> H{"API key required?"}
    H -- "missing or invalid" --> I["Return 401 unauthorized"]
    H -- "valid or not required" --> J{"Within rate limit?"}
    J -- "no" --> K["Return 429 rate_limited"]
    J -- "yes" --> L["Select route/global timeout"]
    L --> M["Select upstream URL or target"]
    M --> N["Forward with retry policy"]
    N -- "timeout" --> O["Return 504 gateway_timeout"]
    N -- "transport error" --> P["Return 502 bad_gateway"]
    N -- "upstream response" --> Q["Copy status, headers, and body"]
```

The runtime request flow looks like this:

```mermaid
sequenceDiagram
    autonumber
    participant Client
    participant Gateway as Gateway Handler
    participant Limiter as Rate Limiter
    participant Proxy as Proxy Forwarder
    participant Upstream

    Client->>Gateway: HTTP request
    alt Health request
        Gateway-->>Client: 200 healthy
    else Routed request
        Gateway->>Gateway: Match route and validate method
        Gateway->>Gateway: Check API key auth
        Gateway->>Limiter: Evaluate route/global limit
        Limiter-->>Gateway: allow or reject
        alt Rate limited
            Gateway-->>Client: 429 rate_limited
        else Allowed
            Gateway->>Gateway: Choose timeout
            Gateway->>Proxy: Forward request with route config
            Proxy->>Proxy: Select upstream target
            loop Retry attempts
                Proxy->>Upstream: HTTP request
                Upstream-->>Proxy: Response or error
            end
            Proxy-->>Gateway: Upstream response or proxy error
            Gateway-->>Client: Final gateway response
        end
    end
```

The code is split along that pipeline:

- `internal/config` owns YAML parsing and validation.
- `internal/gateway` owns route matching, health, auth, rate limiting, and middleware order.
- `internal/proxy` owns upstream selection, path rewriting, retry behavior, and HTTP forwarding.
- `cmd/mockupstream` provides a small manual test harness.

This keeps route-level policy decisions separate from upstream transport mechanics. Adding a
new middleware should mostly affect `internal/gateway`; adding new upstream behavior should
mostly affect `internal/proxy`.

## Trade-offs

- **In-memory state:** Rate limit buckets and upstream selection counters are in memory. This is
  appropriate for the prompt because no distributed coordination or persistence is required.
  In production, these would need cross-instance coordination or sticky routing.
- **Exact sliding window:** Sliding-window rate limiting uses timestamp queues. This is easy to
  reason about and test, but it can use more memory than an approximate rolling counter under
  high cardinality.
- **Retry body buffering:** The proxy buffers request bodies once so retries can resend the
  original payload. This is correct for the take-home, but production systems would enforce
  request-size limits and consider streaming behavior.
- **Target health:** Round-robin and weighted round-robin do not yet skip unhealthy targets.
  Active health checks are parsed from config but not implemented.
- **Transformations:** Request and response transformation config is parsed but not applied.
  I deferred it because body mapping semantics can get subtle, especially for non-JSON payloads.
- **Circuit breaker:** Circuit breaker config is parsed and validated, but breaker state,
  failure windows, and cooldown behavior are deferred.

## Implemented

- Config loading and validation for the provided schema
- Health endpoint with uptime
- Route matching and method filtering
- Basic reverse proxying
- Prefix stripping
- Global and route-level timeouts
- API key authentication
- Fixed-window rate limiting
- Sliding-window rate limiting
- Per-IP and global rate-limit buckets
- Retry support for configured upstream statuses
- Fixed and exponential retry backoff
- Round-robin upstream selection
- Weighted round-robin upstream selection
- Mock upstream server for local testing

## Partially Implemented Or Deferred

- `request_transform`: parsed but not applied
- `response_transform`: parsed but not applied
- `health_check`: parsed but not actively used to mark targets healthy/unhealthy
- `circuit_breaker`: parsed but not enforced

## What I Would Build Next

1. Active health checks for target upstreams, including unhealthy thresholds.
2. Circuit breaker state with failure windows and cooldown responses.
3. Request and response transformations for JSON payloads and headers.
4. Request-size limits around retry buffering.
5. Containerize the gateway with a small runtime image, documented config mounting, and a
   compose-based local demo that runs the gateway alongside mock upstreams.

## AI Tooling

I used AI assistance to work in small, reviewable stages, keeping each commit focused on one
behavioral milestone. The commit history is intentionally structured to show the order of
implementation and the trade-offs made along the way.
