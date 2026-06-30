package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gatewaykit/internal/config"
	"gatewaykit/internal/gateway"
)

type upstreamResponse struct {
	Service string `json:"service"`
	Method  string `json:"method,omitempty"`
	Path    string `json:"path,omitempty"`
	Query   string `json:"query,omitempty"`
	Attempt int64  `json:"attempt,omitempty"`
}

func TestGatewayEndToEndWithConfiguredRoutes(t *testing.T) {
	users := newMockUpstream(t, "users", nil)
	var orderAttempts int64
	orders := newMockUpstream(t, "orders", func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasSuffix(r.URL.Path, "/flaky") {
			attempt := atomic.AddInt64(&orderAttempts, 1)
			if attempt < 3 {
				writeJSON(t, w, http.StatusServiceUnavailable, upstreamResponse{
					Service: "orders",
					Path:    r.URL.Path,
					Attempt: attempt,
				})
				return true
			}
			writeJSON(t, w, http.StatusOK, upstreamResponse{
				Service: "orders",
				Path:    r.URL.Path,
				Attempt: attempt,
			})
			return true
		}
		if strings.HasSuffix(r.URL.Path, "/slow") {
			time.Sleep(50 * time.Millisecond)
			writeJSON(t, w, http.StatusOK, upstreamResponse{Service: "orders", Path: r.URL.Path})
			return true
		}
		return false
	})
	productsA := newMockUpstream(t, "products-a", nil)
	productsB := newMockUpstream(t, "products-b", nil)
	legacy := newMockUpstream(t, "legacy", nil)
	internal := newMockUpstream(t, "internal", nil)

	gw := httptest.NewServer(gateway.NewHandler(config.Gateway{
		Port:          8080,
		GlobalTimeout: "1s",
		GlobalRateLimit: &config.RateLimit{
			Requests: 100,
			Window:   "1m",
			Strategy: "fixed_window",
			Per:      "ip",
		},
		Routes: []config.Route{
			{
				Path:        "/api/users",
				Methods:     []string{http.MethodGet, http.MethodPost},
				StripPrefix: false,
				Upstream:    config.Upstream{URL: users.URL},
				RateLimit: &config.RateLimit{
					Requests: 2,
					Window:   "1m",
					Strategy: "sliding_window",
					Per:      "global",
				},
			},
			{
				Path:        "/api/orders",
				Methods:     []string{http.MethodGet, http.MethodPost, http.MethodPut},
				StripPrefix: false,
				Upstream:    config.Upstream{URL: orders.URL},
				Timeout:     "25ms",
				Retry: &config.Retry{
					Attempts:     3,
					Backoff:      "fixed",
					InitialDelay: "0s",
					On:           []int{http.StatusServiceUnavailable},
				},
			},
			{
				Path:        "/api/products",
				Methods:     []string{http.MethodGet},
				StripPrefix: true,
				Upstream: config.Upstream{
					Targets: []config.Target{
						{URL: productsA.URL, Weight: 2},
						{URL: productsB.URL, Weight: 1},
					},
					Balance: "weighted_round_robin",
				},
			},
			{
				Path:        "/api/legacy",
				Methods:     []string{http.MethodGet, http.MethodPost},
				StripPrefix: true,
				Upstream:    config.Upstream{URL: legacy.URL},
			},
			{
				Path:        "/api/internal",
				Methods:     []string{http.MethodGet, http.MethodPost},
				StripPrefix: false,
				Upstream:    config.Upstream{URL: internal.URL},
				Auth: &config.Auth{
					Type:   "api_key",
					Header: "X-API-Key",
					Keys:   []string{"sk_live_abc123", "sk_live_def456"},
				},
			},
		},
	}))
	defer gw.Close()

	t.Run("health", func(t *testing.T) {
		resp := get(t, gw.URL+"/health")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
	})

	t.Run("not found", func(t *testing.T) {
		resp := get(t, gw.URL+"/missing")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
		}
	})

	t.Run("method not allowed", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodDelete, gw.URL+"/api/products/123", nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		resp := do(t, req)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
		}
		if got := resp.Header.Get("Allow"); got != http.MethodGet {
			t.Fatalf("Allow = %q, want GET", got)
		}
	})

	t.Run("users route proxies and rate limits", func(t *testing.T) {
		first := decodeUpstream(t, get(t, gw.URL+"/api/users/echo?x=1"))
		if first.Service != "users" || first.Path != "/api/users/echo" || first.Query != "x=1" {
			t.Fatalf("first response = %+v, want users /api/users/echo x=1", first)
		}

		second := decodeUpstream(t, get(t, gw.URL+"/api/users/again"))
		if second.Service != "users" {
			t.Fatalf("second service = %q, want users", second.Service)
		}

		resp := get(t, gw.URL+"/api/users/limited")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("third status = %d, want %d", resp.StatusCode, http.StatusTooManyRequests)
		}
	})

	t.Run("orders route retries transient status", func(t *testing.T) {
		got := decodeUpstream(t, get(t, gw.URL+"/api/orders/flaky"))
		if got.Service != "orders" || got.Attempt != 3 {
			t.Fatalf("response = %+v, want recovered orders attempt 3", got)
		}
	})

	t.Run("orders route times out", func(t *testing.T) {
		resp := get(t, gw.URL+"/api/orders/slow")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusGatewayTimeout {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusGatewayTimeout)
		}
	})

	t.Run("products route strips prefix and balances targets", func(t *testing.T) {
		want := []string{"products-a", "products-a", "products-b"}
		for i, service := range want {
			got := decodeUpstream(t, get(t, gw.URL+"/api/products/sku-123"))
			if got.Service != service {
				t.Fatalf("request %d service = %q, want %q", i+1, got.Service, service)
			}
			if got.Path != "/sku-123" {
				t.Fatalf("request %d path = %q, want /sku-123", i+1, got.Path)
			}
		}
	})

	t.Run("legacy route strips prefix", func(t *testing.T) {
		got := decodeUpstream(t, get(t, gw.URL+"/api/legacy/v1/data"))
		if got.Service != "legacy" || got.Path != "/v1/data" {
			t.Fatalf("response = %+v, want legacy /v1/data", got)
		}
	})

	t.Run("internal route requires api key", func(t *testing.T) {
		unauthorized := get(t, gw.URL+"/api/internal/echo")
		defer unauthorized.Body.Close()
		if unauthorized.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unauthorized status = %d, want %d", unauthorized.StatusCode, http.StatusUnauthorized)
		}

		req, err := http.NewRequest(http.MethodGet, gw.URL+"/api/internal/echo", nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("X-API-Key", "sk_live_abc123")
		got := decodeUpstream(t, do(t, req))
		if got.Service != "internal" || got.Path != "/api/internal/echo" {
			t.Fatalf("response = %+v, want internal /api/internal/echo", got)
		}
	})
}

func TestGatewayEndToEndGlobalDefaultsAndRoundRobin(t *testing.T) {
	limited := newMockUpstream(t, "limited", nil)
	slow := newMockUpstream(t, "slow", func(w http.ResponseWriter, r *http.Request) bool {
		time.Sleep(50 * time.Millisecond)
		writeJSON(t, w, http.StatusOK, upstreamResponse{Service: "slow", Path: r.URL.Path})
		return true
	})
	targetA := newMockUpstream(t, "target-a", nil)
	targetB := newMockUpstream(t, "target-b", nil)

	gw := httptest.NewServer(gateway.NewHandler(config.Gateway{
		Port:          8080,
		GlobalTimeout: "25ms",
		GlobalRateLimit: &config.RateLimit{
			Requests: 2,
			Window:   "1m",
			Strategy: "fixed_window",
			Per:      "global",
		},
		Routes: []config.Route{
			{
				Path:     "/api/limited",
				Methods:  []string{http.MethodGet},
				Upstream: config.Upstream{URL: limited.URL},
			},
			{
				Path:     "/api/slow",
				Methods:  []string{http.MethodGet},
				Upstream: config.Upstream{URL: slow.URL},
				RateLimit: &config.RateLimit{
					Requests: 100,
					Window:   "1m",
					Strategy: "fixed_window",
					Per:      "global",
				},
			},
			{
				Path:        "/api/rr",
				Methods:     []string{http.MethodGet},
				StripPrefix: true,
				Upstream: config.Upstream{
					Targets: []config.Target{
						{URL: targetA.URL, Weight: 1},
						{URL: targetB.URL, Weight: 1},
					},
					Balance: "round_robin",
				},
				RateLimit: &config.RateLimit{
					Requests: 100,
					Window:   "1m",
					Strategy: "fixed_window",
					Per:      "global",
				},
			},
		},
	}))
	defer gw.Close()

	t.Run("global rate limit applies when route has no override", func(t *testing.T) {
		for i := 1; i <= 2; i++ {
			got := decodeUpstream(t, get(t, gw.URL+"/api/limited/ok"))
			if got.Service != "limited" {
				t.Fatalf("request %d service = %q, want limited", i, got.Service)
			}
		}

		resp := get(t, gw.URL+"/api/limited/blocked")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusTooManyRequests)
		}
	})

	t.Run("global timeout applies when route has no timeout override", func(t *testing.T) {
		resp := get(t, gw.URL+"/api/slow/wait")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusGatewayTimeout {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusGatewayTimeout)
		}
	})

	t.Run("round robin selects each target in order", func(t *testing.T) {
		want := []string{"target-a", "target-b", "target-a"}
		for i, service := range want {
			got := decodeUpstream(t, get(t, gw.URL+"/api/rr/item"))
			if got.Service != service {
				t.Fatalf("request %d service = %q, want %q", i+1, got.Service, service)
			}
			if got.Path != "/item" {
				t.Fatalf("request %d path = %q, want /item", i+1, got.Path)
			}
		}
	})
}

func newMockUpstream(t *testing.T, name string, override func(http.ResponseWriter, *http.Request) bool) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if override != nil && override(w, r) {
			return
		}
		writeJSON(t, w, http.StatusOK, upstreamResponse{
			Service: name,
			Method:  r.Method,
			Path:    r.URL.Path,
			Query:   r.URL.RawQuery,
		})
	}))
	t.Cleanup(server.Close)
	return server
}

func get(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

func do(t *testing.T, req *http.Request) *http.Response {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", req.Method, req.URL.String(), err)
	}
	return resp
}

func decodeUpstream(t *testing.T, resp *http.Response) upstreamResponse {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("status = %d, want 2xx", resp.StatusCode)
	}
	var out upstreamResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode upstream response: %v", err)
	}
	return out
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, payload upstreamResponse) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}
