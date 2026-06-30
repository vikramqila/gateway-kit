package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gatewaykit/internal/config"
)

func TestRateLimiterResetsAfterWindow(t *testing.T) {
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	limiter := newRateLimiter()
	limiter.now = func() time.Time { return now }

	route := config.Route{Path: "/api/users"}
	rule := config.RateLimit{
		Requests: 1,
		Window:   "1s",
		Strategy: "fixed_window",
		Per:      "global",
	}
	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)

	if !limiter.allow(route, req, rule) {
		t.Fatal("first request was rate limited, want allowed")
	}
	if limiter.allow(route, req, rule) {
		t.Fatal("second request was allowed, want rate limited")
	}

	now = now.Add(time.Second)
	if !limiter.allow(route, req, rule) {
		t.Fatal("request after window reset was rate limited, want allowed")
	}
}

func TestSlidingWindowRateLimiterDropsExpiredRequests(t *testing.T) {
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	limiter := newRateLimiter()
	limiter.now = func() time.Time { return now }

	route := config.Route{Path: "/api/users"}
	rule := config.RateLimit{
		Requests: 2,
		Window:   "1s",
		Strategy: "sliding_window",
		Per:      "global",
	}
	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)

	if !limiter.allow(route, req, rule) {
		t.Fatal("first request was rate limited, want allowed")
	}
	now = now.Add(500 * time.Millisecond)
	if !limiter.allow(route, req, rule) {
		t.Fatal("second request was rate limited, want allowed")
	}
	now = now.Add(400 * time.Millisecond)
	if limiter.allow(route, req, rule) {
		t.Fatal("third request inside sliding window was allowed, want rate limited")
	}

	now = now.Add(200 * time.Millisecond)
	if !limiter.allow(route, req, rule) {
		t.Fatal("request after oldest timestamp expired was rate limited, want allowed")
	}
}
