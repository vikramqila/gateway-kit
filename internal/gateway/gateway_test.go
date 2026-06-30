package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
