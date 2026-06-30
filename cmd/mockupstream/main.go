package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"time"
)

type response struct {
	Service string `json:"service"`
	Method  string `json:"method,omitempty"`
	Path    string `json:"path,omitempty"`
	Query   string `json:"query,omitempty"`
	Message string `json:"message,omitempty"`
	Attempt int64  `json:"attempt,omitempty"`
}

func main() {
	port := flag.Int("port", envInt("PORT", 3001), "port to listen on")
	name := flag.String("name", envString("UPSTREAM_NAME", "mock-upstream"), "service name returned in responses")
	flag.Parse()

	var flakyAttempts int64
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, response{Service: *name, Message: "healthy"})
	})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(6 * time.Second)
		writeJSON(w, http.StatusOK, response{Service: *name, Message: "slow response"})
	})
	mux.HandleFunc("/flaky", func(w http.ResponseWriter, r *http.Request) {
		attempt := atomic.AddInt64(&flakyAttempts, 1)
		if attempt%3 != 0 {
			writeJSON(w, http.StatusServiceUnavailable, response{
				Service: *name,
				Message: "temporary failure",
				Attempt: attempt,
			})
			return
		}
		writeJSON(w, http.StatusOK, response{
			Service: *name,
			Message: "recovered",
			Attempt: attempt,
		})
	})
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, response{
			Service: *name,
			Method:  r.Method,
			Path:    r.URL.Path,
			Query:   r.URL.RawQuery,
			Message: "echo",
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, response{
			Service: *name,
			Method:  r.Method,
			Path:    r.URL.Path,
			Query:   r.URL.RawQuery,
			Message: "ok",
		})
	})

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("%s listening on %s", *name, addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("listen and serve: %v", err)
	}
}

func writeJSON(w http.ResponseWriter, status int, payload response) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func envInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envString(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
