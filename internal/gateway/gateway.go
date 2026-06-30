// Package gateway owns HTTP routing and the gateway request pipeline.
package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"gatewaykit/internal/config"
)

type Handler struct {
	startedAt time.Time
	routes    []config.Route
}

func NewHandler(cfg config.Gateway) *Handler {
	return &Handler{
		startedAt: time.Now(),
		routes:    cfg.Routes,
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

	writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "proxy_not_implemented"})
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

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
