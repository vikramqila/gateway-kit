package proxy

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gatewaykit/internal/config"
)

func TestForwarderProxiesRequestAndResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.URL.RequestURI(); got != "/api/users/42?expand=true" {
			t.Fatalf("request URI = %q, want /api/users/42?expand=true", got)
		}
		if got := r.Header.Get("X-Trace-ID"); got != "trace-123" {
			t.Fatalf("X-Trace-ID = %q, want trace-123", got)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if got := string(body); got != `{"name":"Ada"}` {
			t.Fatalf("body = %q, want JSON payload", got)
		}

		w.Header().Set("X-Upstream", "users")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/users/42?expand=true", strings.NewReader(`{"name":"Ada"}`))
	req.Header.Set("X-Trace-ID", "trace-123")
	rec := httptest.NewRecorder()

	route := config.Route{Upstream: config.Upstream{URL: upstream.URL}}
	err := NewForwarder(upstream.Client()).ServeHTTP(rec, req, route, 0)
	if err != nil {
		t.Fatalf("ServeHTTP() error = %v", err)
	}

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	if got := rec.Header().Get("X-Upstream"); got != "users" {
		t.Fatalf("X-Upstream = %q, want users", got)
	}
	if got := rec.Body.String(); got != `{"ok":true}` {
		t.Fatalf("body = %q, want upstream response", got)
	}
}

func TestForwarderRejectsUnsupportedUpstream(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/products", nil)
	rec := httptest.NewRecorder()
	route := config.Route{Upstream: config.Upstream{Targets: []config.Target{{URL: "http://example.com", Weight: 1}}}}

	err := NewForwarder(nil).ServeHTTP(rec, req, route, 0)
	if !errors.Is(err, ErrUnsupportedUpstream) {
		t.Fatalf("ServeHTTP() error = %v, want ErrUnsupportedUpstream", err)
	}
}

func TestForwarderStripsRoutePrefix(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.RequestURI(); got != "/42?expand=true" {
			t.Fatalf("request URI = %q, want /42?expand=true", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/users/42?expand=true", nil)
	rec := httptest.NewRecorder()
	route := config.Route{
		Path:        "/api/users",
		StripPrefix: true,
		Upstream:    config.Upstream{URL: upstream.URL},
	}

	err := NewForwarder(upstream.Client()).ServeHTTP(rec, req, route, 0)
	if err != nil {
		t.Fatalf("ServeHTTP() error = %v", err)
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestForwarderStripsExactRoutePrefixToRoot(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.RequestURI(); got != "/" {
			t.Fatalf("request URI = %q, want /", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	rec := httptest.NewRecorder()
	route := config.Route{
		Path:        "/api/users",
		StripPrefix: true,
		Upstream:    config.Upstream{URL: upstream.URL},
	}

	err := NewForwarder(upstream.Client()).ServeHTTP(rec, req, route, 0)
	if err != nil {
		t.Fatalf("ServeHTTP() error = %v", err)
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}
