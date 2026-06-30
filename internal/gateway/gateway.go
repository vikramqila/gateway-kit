// Package gateway owns HTTP routing and the gateway request pipeline.
package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"gatewaykit/internal/config"
	"gatewaykit/internal/proxy"
)

type Handler struct {
	startedAt     time.Time
	routes        []config.Route
	globalTimeout time.Duration
	proxy         *proxy.Forwarder
}

func NewHandler(cfg config.Gateway) *Handler {
	return &Handler{
		startedAt:     time.Now(),
		routes:        cfg.Routes,
		globalTimeout: parseDuration(cfg.GlobalTimeout),
		proxy:         proxy.NewForwarder(nil),
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Path == "/health" {
		h.handleHealth(w)
		return
	}

	route, matchedPath := h.matchRoute(r.URL.Path)
	if !matchedPath {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	if !methodAllowed(route, r.Method) {
		w.Header().Set("Allow", strings.Join(route.Methods, ", "))
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}
	if !authorized(route, r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	if err := h.proxy.ServeHTTP(w, r, route, h.timeoutFor(route)); err != nil {
		if errors.Is(err, proxy.ErrUnsupportedUpstream) {
			writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "proxy_not_implemented"})
			return
		}
		if errors.Is(err, proxy.ErrUpstreamTimeout) {
			writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "gateway_timeout"})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "bad_gateway"})
		return
	}
}

func (h *Handler) handleHealth(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "healthy",
		"uptime_seconds": int(time.Since(h.startedAt).Seconds()),
	})
}

func (h *Handler) matchRoute(path string) (config.Route, bool) {
	var best config.Route
	bestLen := -1
	for _, route := range h.routes {
		if routeMatches(route.Path, path) && len(route.Path) > bestLen {
			best = route
			bestLen = len(route.Path)
		}
	}
	return best, bestLen >= 0
}

func routeMatches(routePath string, requestPath string) bool {
	return requestPath == routePath || strings.HasPrefix(requestPath, routePath+"/")
}

func methodAllowed(route config.Route, method string) bool {
	for _, allowed := range route.Methods {
		if allowed == method {
			return true
		}
	}
	return false
}

func authorized(route config.Route, r *http.Request) bool {
	if route.Auth == nil {
		return true
	}
	if route.Auth.Type != "api_key" {
		return false
	}

	value := r.Header.Get(route.Auth.Header)
	if value == "" {
		return false
	}
	for _, key := range route.Auth.Keys {
		if value == key {
			return true
		}
	}
	return false
}

func (h *Handler) timeoutFor(route config.Route) time.Duration {
	if route.Timeout != "" {
		return parseDuration(route.Timeout)
	}
	return h.globalTimeout
}

func parseDuration(value string) time.Duration {
	if value == "" {
		return 0
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0
	}
	return duration
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
