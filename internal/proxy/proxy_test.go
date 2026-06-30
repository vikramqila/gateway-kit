package proxy

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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
	route := config.Route{}

	err := NewForwarder(nil).ServeHTTP(rec, req, route, 0)
	if !errors.Is(err, ErrUnsupportedUpstream) {
		t.Fatalf("ServeHTTP() error = %v, want ErrUnsupportedUpstream", err)
	}
}

func TestForwarderUsesRoundRobinTargets(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("first"))
	}))
	defer first.Close()

	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("second"))
	}))
	defer second.Close()

	forwarder := NewForwarder(first.Client())
	route := config.Route{
		Path: "/api/products",
		Upstream: config.Upstream{
			Balance: "round_robin",
			Targets: []config.Target{
				{URL: first.URL, Weight: 1},
				{URL: second.URL, Weight: 1},
			},
		},
	}

	want := []string{"first", "second", "first"}
	for i, expected := range want {
		req := httptest.NewRequest(http.MethodGet, "/api/products", nil)
		rec := httptest.NewRecorder()

		if err := forwarder.ServeHTTP(rec, req, route, 0); err != nil {
			t.Fatalf("request %d ServeHTTP() error = %v", i+1, err)
		}
		if got := rec.Body.String(); got != expected {
			t.Fatalf("request %d body = %q, want %q", i+1, got, expected)
		}
	}
}

func TestForwarderUsesWeightedRoundRobinTargets(t *testing.T) {
	heavy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("heavy"))
	}))
	defer heavy.Close()

	light := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("light"))
	}))
	defer light.Close()

	forwarder := NewForwarder(heavy.Client())
	route := config.Route{
		Path: "/api/products",
		Upstream: config.Upstream{
			Balance: "weighted_round_robin",
			Targets: []config.Target{
				{URL: heavy.URL, Weight: 2},
				{URL: light.URL, Weight: 1},
			},
		},
	}

	want := []string{"heavy", "heavy", "light", "heavy"}
	for i, expected := range want {
		req := httptest.NewRequest(http.MethodGet, "/api/products", nil)
		rec := httptest.NewRecorder()

		if err := forwarder.ServeHTTP(rec, req, route, 0); err != nil {
			t.Fatalf("request %d ServeHTTP() error = %v", i+1, err)
		}
		if got := rec.Body.String(); got != expected {
			t.Fatalf("request %d body = %q, want %q", i+1, got, expected)
		}
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

func TestForwarderRetriesConfiguredStatuses(t *testing.T) {
	var attempts int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := atomic.AddInt32(&attempts, 1)
		if attempt < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("try again"))
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/orders", nil)
	rec := httptest.NewRecorder()
	route := config.Route{
		Upstream: config.Upstream{URL: upstream.URL},
		Retry: &config.Retry{
			Attempts:     3,
			Backoff:      "fixed",
			InitialDelay: "0s",
			On:           []int{http.StatusServiceUnavailable},
		},
	}

	err := NewForwarder(upstream.Client()).ServeHTTP(rec, req, route, 0)
	if err != nil {
		t.Fatalf("ServeHTTP() error = %v", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "ok" {
		t.Fatalf("body = %q, want ok", got)
	}
}

func TestForwarderDoesNotRetryUnconfiguredStatus(t *testing.T) {
	var attempts int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("not retried"))
	}))
	defer upstream.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/orders", nil)
	rec := httptest.NewRecorder()
	route := config.Route{
		Upstream: config.Upstream{URL: upstream.URL},
		Retry: &config.Retry{
			Attempts:     3,
			Backoff:      "fixed",
			InitialDelay: "0s",
			On:           []int{http.StatusServiceUnavailable},
		},
	}

	err := NewForwarder(upstream.Client()).ServeHTTP(rec, req, route, 0)
	if err != nil {
		t.Fatalf("ServeHTTP() error = %v", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestForwarderRetriesPreserveRequestBody(t *testing.T) {
	var attempts int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := atomic.AddInt32(&attempts, 1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if got := string(body); got != `{"id":42}` {
			t.Fatalf("attempt %d body = %q, want original body", attempt, got)
		}

		if attempt == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer upstream.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/orders", strings.NewReader(`{"id":42}`))
	rec := httptest.NewRecorder()
	route := config.Route{
		Upstream: config.Upstream{URL: upstream.URL},
		Retry: &config.Retry{
			Attempts:     2,
			Backoff:      "fixed",
			InitialDelay: "0s",
			On:           []int{http.StatusBadGateway},
		},
	}

	err := NewForwarder(upstream.Client()).ServeHTTP(rec, req, route, 0)
	if err != nil {
		t.Fatalf("ServeHTTP() error = %v", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
}
