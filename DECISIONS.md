# GatewayKit Decisions

This document captures the implementation plan, trade-offs, and current project status.

## Prioritization

The baseline requirements come first:

1. Load configuration from a YAML file.
2. Serve `GET /health` independently of configured routes.
3. Match configured routes, enforce methods, and return correct `404`/`405` responses.
4. Proxy requests to upstream services.
5. Prove the behavior with a self-contained test suite.

After the baseline is reliable, the next features are API key authentication, fixed-window
rate limiting, retries, and multiple upstream selection. These provide strong production
signal while keeping the implementation testable under the time constraint.

## Architecture

The gateway will be structured as a small request pipeline:

```text
HTTP request
  -> route matcher
  -> method guard
  -> auth middleware
  -> rate limit middleware
  -> upstream selector
  -> proxy transport
  -> response writer
```

Each stage should be independently testable and optional, so new config features can be added
without rewriting the core proxy path.

## Trade-offs

- In-memory state is acceptable for rate limits, circuit breakers, and health status because
  the prompt does not require distributed coordination.
- The first implementation will favor clear, deterministic behavior over feature breadth.
- Unsupported config fields will be documented explicitly rather than silently implied.

## Current Status

- Project scaffold created.
- Gateway behavior is not implemented yet.

## Next Steps

1. Add config structs and YAML loading.
2. Add validation for required fields and durations.
3. Add tests around config loading with the sample schema.
