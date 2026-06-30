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

func TestRouteMatchUsesPathBoundary(t *testing.T) {
	handler := NewHandler(testGateway())
	req := httptest.NewRequest(http.MethodGet, "/api/users2", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
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
