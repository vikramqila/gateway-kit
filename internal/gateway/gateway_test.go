package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gatewaykit/internal/config"
)

func TestHealthAlwaysReturnsHealthy(t *testing.T) {
	handler := NewHandler(config.Gateway{})
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["status"] != "healthy" {
		t.Fatalf("status field = %v, want healthy", body["status"])
	}
	if _, ok := body["uptime_seconds"].(float64); !ok {
		t.Fatalf("uptime_seconds field = %T, want number", body["uptime_seconds"])
	}
}

func TestUnmatchedRouteReturnsNotFound(t *testing.T) {
	handler := NewHandler(testGateway())
	req := httptest.NewRequest(http.MethodGet, "/api/missing", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestMethodNotAllowedWhenPathMatches(t *testing.T) {
	handler := NewHandler(testGateway())
	req := httptest.NewRequest(http.MethodPost, "/api/products/123", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
	if got := rec.Header().Get("Allow"); got != "GET" {
		t.Fatalf("Allow header = %q, want GET", got)
	}
}

func TestMatchedAllowedRouteReturnsProxyPlaceholder(t *testing.T) {
	handler := NewHandler(testGateway())
	req := httptest.NewRequest(http.MethodGet, "/api/users/42", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotImplemented)
	}
}

func TestMatchedAllowedRouteProxiesSingleUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.RequestURI(); got != "/api/users/42?expand=true" {
			t.Fatalf("request URI = %q, want /api/users/42?expand=true", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if got := string(body); got != `{"name":"Ada"}` {
			t.Fatalf("body = %q, want JSON payload", got)
		}

		w.Header().Set("X-Upstream", "users")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	defer upstream.Close()

	handler := NewHandler(config.Gateway{
		Routes: []config.Route{
			{
				Path:    "/api/users",
				Methods: []string{http.MethodPost},
				Upstream: config.Upstream{
					URL: upstream.URL,
				},
			},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/users/42?expand=true", strings.NewReader(`{"name":"Ada"}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if got := rec.Header().Get("X-Upstream"); got != "users" {
		t.Fatalf("X-Upstream = %q, want users", got)
	}
	if got := rec.Body.String(); got != `{"accepted":true}` {
		t.Fatalf("body = %q, want upstream body", got)
	}
}

func TestMatchedAllowedRouteProxiesTargetUpstreams(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("first"))
	}))
	defer first.Close()

	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("second"))
	}))
	defer second.Close()

	handler := NewHandler(config.Gateway{
		Routes: []config.Route{
			{
				Path:    "/api/products",
				Methods: []string{http.MethodGet},
				Upstream: config.Upstream{
					Balance: "round_robin",
					Targets: []config.Target{
						{URL: first.URL, Weight: 1},
						{URL: second.URL, Weight: 1},
					},
				},
			},
		},
	})

	for i, expected := range []string{"first", "second"} {
		req := httptest.NewRequest(http.MethodGet, "/api/products", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, want %d", i+1, rec.Code, http.StatusOK)
		}
		if got := rec.Body.String(); got != expected {
			t.Fatalf("request %d body = %q, want %q", i+1, got, expected)
		}
	}
}

func TestGlobalTimeoutReturnsGatewayTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	handler := NewHandler(config.Gateway{
		GlobalTimeout: "10ms",
		Routes: []config.Route{
			{
				Path:    "/api/users",
				Methods: []string{http.MethodGet},
				Upstream: config.Upstream{
					URL: upstream.URL,
				},
			},
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusGatewayTimeout)
	}
}

func TestRouteTimeoutOverridesGlobalTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	handler := NewHandler(config.Gateway{
		GlobalTimeout: "1s",
		Routes: []config.Route{
			{
				Path:    "/api/users",
				Methods: []string{http.MethodGet},
				Timeout: "10ms",
				Upstream: config.Upstream{
					URL: upstream.URL,
				},
			},
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusGatewayTimeout)
	}
}

func TestAPIKeyAuthRejectsMissingKey(t *testing.T) {
	handler := NewHandler(authGateway("http://example.com"))
	req := httptest.NewRequest(http.MethodGet, "/api/internal", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestAPIKeyAuthRejectsInvalidKey(t *testing.T) {
	handler := NewHandler(authGateway("http://example.com"))
	req := httptest.NewRequest(http.MethodGet, "/api/internal", nil)
	req.Header.Set("X-API-Key", "wrong-key")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestAPIKeyAuthAllowsValidKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	handler := NewHandler(authGateway(upstream.URL))
	req := httptest.NewRequest(http.MethodGet, "/api/internal", nil)
	req.Header.Set("X-API-Key", "sk_live_abc123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestGlobalFixedWindowRateLimit(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	handler := NewHandler(config.Gateway{
		GlobalRateLimit: &config.RateLimit{
			Requests: 1,
			Window:   "1m",
			Strategy: "fixed_window",
			Per:      "global",
		},
		Routes: []config.Route{
			{
				Path:    "/api/users",
				Methods: []string{http.MethodGet},
				Upstream: config.Upstream{
					URL: upstream.URL,
				},
			},
		},
	})

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/api/users", nil))
	if first.Code != http.StatusNoContent {
		t.Fatalf("first status = %d, want %d", first.Code, http.StatusNoContent)
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/api/users", nil))
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want %d", second.Code, http.StatusTooManyRequests)
	}
}

func TestRouteFixedWindowRateLimitOverridesGlobal(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	handler := NewHandler(config.Gateway{
		GlobalRateLimit: &config.RateLimit{
			Requests: 1,
			Window:   "1m",
			Strategy: "fixed_window",
			Per:      "global",
		},
		Routes: []config.Route{
			{
				Path:    "/api/users",
				Methods: []string{http.MethodGet},
				Upstream: config.Upstream{
					URL: upstream.URL,
				},
				RateLimit: &config.RateLimit{
					Requests: 2,
					Window:   "1m",
					Strategy: "fixed_window",
					Per:      "global",
				},
			},
		},
	})

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/users", nil))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("request %d status = %d, want %d", i+1, rec.Code, http.StatusNoContent)
		}
	}

	third := httptest.NewRecorder()
	handler.ServeHTTP(third, httptest.NewRequest(http.MethodGet, "/api/users", nil))
	if third.Code != http.StatusTooManyRequests {
		t.Fatalf("third status = %d, want %d", third.Code, http.StatusTooManyRequests)
	}
}

func TestPerIPFixedWindowRateLimitUsesSeparateBuckets(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	handler := NewHandler(config.Gateway{
		GlobalRateLimit: &config.RateLimit{
			Requests: 1,
			Window:   "1m",
			Strategy: "fixed_window",
			Per:      "ip",
		},
		Routes: []config.Route{
			{
				Path:    "/api/users",
				Methods: []string{http.MethodGet},
				Upstream: config.Upstream{
					URL: upstream.URL,
				},
			},
		},
	})

	first := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	first.RemoteAddr = "192.0.2.1:1234"
	firstRec := httptest.NewRecorder()
	handler.ServeHTTP(firstRec, first)
	if firstRec.Code != http.StatusNoContent {
		t.Fatalf("first status = %d, want %d", firstRec.Code, http.StatusNoContent)
	}

	second := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	second.RemoteAddr = "192.0.2.2:1234"
	secondRec := httptest.NewRecorder()
	handler.ServeHTTP(secondRec, second)
	if secondRec.Code != http.StatusNoContent {
		t.Fatalf("second status = %d, want %d", secondRec.Code, http.StatusNoContent)
	}

	third := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	third.RemoteAddr = "192.0.2.1:5678"
	thirdRec := httptest.NewRecorder()
	handler.ServeHTTP(thirdRec, third)
	if thirdRec.Code != http.StatusTooManyRequests {
		t.Fatalf("third status = %d, want %d", thirdRec.Code, http.StatusTooManyRequests)
	}
}

func TestSlidingWindowRateLimitIsCurrentlyNotEnforced(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	handler := NewHandler(config.Gateway{
		GlobalRateLimit: &config.RateLimit{
			Requests: 1,
			Window:   "1m",
			Strategy: "sliding_window",
			Per:      "global",
		},
		Routes: []config.Route{
			{
				Path:    "/api/users",
				Methods: []string{http.MethodGet},
				Upstream: config.Upstream{
					URL: upstream.URL,
				},
			},
		},
	})

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/users", nil))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("request %d status = %d, want %d", i+1, rec.Code, http.StatusNoContent)
		}
	}
}

func TestRouteMatchUsesPathBoundary(t *testing.T) {
	handler := NewHandler(testGateway())
	req := httptest.NewRequest(http.MethodGet, "/api/users2", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func authGateway(upstreamURL string) config.Gateway {
	return config.Gateway{
		Routes: []config.Route{
			{
				Path:    "/api/internal",
				Methods: []string{http.MethodGet},
				Upstream: config.Upstream{
					URL: upstreamURL,
				},
				Auth: &config.Auth{
					Type:   "api_key",
					Header: "X-API-Key",
					Keys:   []string{"sk_live_abc123", "sk_live_def456"},
				},
			},
		},
	}
}

func testGateway() config.Gateway {
	return config.Gateway{
		Routes: []config.Route{
			{
				Path:    "/api/users",
				Methods: []string{http.MethodGet, http.MethodPost},
			},
			{
				Path:    "/api/products",
				Methods: []string{http.MethodGet},
			},
		},
	}
}
