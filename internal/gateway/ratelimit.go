package gateway

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"gatewaykit/internal/config"
)

type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]rateBucket
	now     func() time.Time
}

type rateBucket struct {
	windowStart time.Time
	count       int
	requests    []time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{
		buckets: map[string]rateBucket{},
		now:     time.Now,
	}
}

func (l *rateLimiter) allow(route config.Route, r *http.Request, rule config.RateLimit) bool {
	window := parseDuration(rule.Window)
	if rule.Requests <= 0 || window <= 0 {
		return true
	}

	key := rateLimitKey(route, r, rule)
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	if rule.Strategy == "sliding_window" {
		return l.allowSlidingWindow(key, now, window, rule.Requests)
	}
	if rule.Strategy != "fixed_window" {
		return true
	}

	return l.allowFixedWindow(key, now, window, rule.Requests)
}

func (l *rateLimiter) allowFixedWindow(key string, now time.Time, window time.Duration, limit int) bool {
	bucket := l.buckets[key]
	if bucket.windowStart.IsZero() || now.Sub(bucket.windowStart) >= window {
		l.buckets[key] = rateBucket{
			windowStart: now,
			count:       1,
		}
		return true
	}

	if bucket.count >= limit {
		return false
	}

	bucket.count++
	l.buckets[key] = bucket
	return true
}

func (l *rateLimiter) allowSlidingWindow(key string, now time.Time, window time.Duration, limit int) bool {
	bucket := l.buckets[key]
	cutoff := now.Add(-window)
	requests := bucket.requests[:0]
	for _, requestTime := range bucket.requests {
		if requestTime.After(cutoff) {
			requests = append(requests, requestTime)
		}
	}

	if len(requests) >= limit {
		bucket.requests = requests
		l.buckets[key] = bucket
		return false
	}

	bucket.requests = append(requests, now)
	l.buckets[key] = bucket
	return true
}

func rateLimitKey(route config.Route, r *http.Request, rule config.RateLimit) string {
	scope := "global"
	if rule.Per == "ip" {
		scope = clientIP(r)
	}
	return route.Path + "|" + rule.Per + "|" + scope
}

func clientIP(r *http.Request) string {
	if forwardedFor := r.Header.Get("X-Forwarded-For"); forwardedFor != "" {
		if ip, _, ok := strings.Cut(forwardedFor, ","); ok {
			return strings.TrimSpace(ip)
		}
		return strings.TrimSpace(forwardedFor)
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
