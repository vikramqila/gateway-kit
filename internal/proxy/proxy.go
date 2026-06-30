// Package proxy forwards matched requests to configured upstream services.
package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"gatewaykit/internal/config"
)

var ErrUnsupportedUpstream = errors.New("unsupported upstream")
var ErrUpstreamTimeout = errors.New("upstream timeout")

type Forwarder struct {
	client *http.Client
	mu     sync.Mutex
	next   map[string]int
}

func NewForwarder(client *http.Client) *Forwarder {
	if client == nil {
		client = http.DefaultClient
	}
	return &Forwarder{
		client: client,
		next:   map[string]int{},
	}
}

func (f *Forwarder) ServeHTTP(w http.ResponseWriter, r *http.Request, route config.Route, timeout time.Duration) error {
	upstreamURL, err := f.selectUpstream(route)
	if err != nil {
		return ErrUnsupportedUpstream
	}

	requestPath := forwardedPath(route, r.URL.Path)
	targetURL, err := buildTargetURL(upstreamURL, r.URL, requestPath)
	if err != nil {
		return fmt.Errorf("build upstream request: %w", err)
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return fmt.Errorf("read downstream request body: %w", err)
	}

	ctx := r.Context()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	resp, err := f.doWithRetries(ctx, r, targetURL, body, route)
	if err != nil {
		if isTimeout(ctx, err) {
			return ErrUpstreamTimeout
		}
		return fmt.Errorf("send upstream request: %w", err)
	}
	defer resp.Body.Close()

	copyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("copy upstream response: %w", err)
	}

	return nil
}

func (f *Forwarder) selectUpstream(route config.Route) (string, error) {
	if route.Upstream.URL != "" {
		return route.Upstream.URL, nil
	}
	if len(route.Upstream.Targets) == 0 {
		return "", ErrUnsupportedUpstream
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	key := route.Path
	index := f.next[key]
	selected := selectTarget(route.Upstream, index)
	f.next[key] = index + 1
	return selected, nil
}

func selectTarget(upstream config.Upstream, index int) string {
	if upstream.Balance == "weighted_round_robin" {
		return selectWeightedTarget(upstream.Targets, index)
	}
	return upstream.Targets[index%len(upstream.Targets)].URL
}

func selectWeightedTarget(targets []config.Target, index int) string {
	totalWeight := 0
	for _, target := range targets {
		totalWeight += target.Weight
	}
	if totalWeight <= 0 {
		return targets[index%len(targets)].URL
	}

	slot := index % totalWeight
	for _, target := range targets {
		slot -= target.Weight
		if slot < 0 {
			return target.URL
		}
	}
	return targets[len(targets)-1].URL
}

func (f *Forwarder) doWithRetries(ctx context.Context, r *http.Request, targetURL string, body []byte, route config.Route) (*http.Response, error) {
	attempts := retryAttempts(route.Retry)
	var lastErr error

	for attempt := 1; attempt <= attempts; attempt++ {
		resp, err := f.doAttempt(ctx, r, targetURL, body)
		if err != nil {
			lastErr = err
			if !canRetryAttempt(ctx, attempt, attempts) {
				return nil, err
			}
			if err := sleepBeforeRetry(ctx, route.Retry, attempt); err != nil {
				return nil, err
			}
			continue
		}

		if shouldRetryStatus(route.Retry, resp.StatusCode) && attempt < attempts {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if err := sleepBeforeRetry(ctx, route.Retry, attempt); err != nil {
				return nil, err
			}
			continue
		}

		return resp, nil
	}

	return nil, lastErr
}

func (f *Forwarder) doAttempt(ctx context.Context, r *http.Request, targetURL string, body []byte) (*http.Response, error) {
	upstreamReq, err := http.NewRequestWithContext(ctx, r.Method, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create upstream request: %w", err)
	}
	copyRequestHeaders(upstreamReq.Header, r.Header)
	upstreamReq.Host = upstreamReq.URL.Host
	appendForwardedFor(upstreamReq.Header, r.RemoteAddr)

	resp, err := f.client.Do(upstreamReq)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func forwardedPath(route config.Route, requestPath string) string {
	if !route.StripPrefix {
		return requestPath
	}

	trimmed := strings.TrimPrefix(requestPath, route.Path)
	if trimmed == "" {
		return "/"
	}
	if strings.HasPrefix(trimmed, "/") {
		return trimmed
	}
	return "/" + trimmed
}

func buildTargetURL(upstream string, requestURL *url.URL, requestPath string) (string, error) {
	base, err := url.Parse(upstream)
	if err != nil {
		return "", err
	}

	out := *base
	out.Path = singleJoiningSlash(base.Path, requestPath)
	out.RawQuery = requestURL.RawQuery
	out.Fragment = ""
	return out.String(), nil
}

func retryAttempts(retry *config.Retry) int {
	if retry == nil || retry.Attempts <= 1 {
		return 1
	}
	return retry.Attempts
}

func canRetryAttempt(ctx context.Context, attempt int, attempts int) bool {
	return attempt < attempts && ctx.Err() == nil
}

func shouldRetryStatus(retry *config.Retry, statusCode int) bool {
	if retry == nil {
		return false
	}
	for _, retryStatus := range retry.On {
		if retryStatus == statusCode {
			return true
		}
	}
	return false
}

func sleepBeforeRetry(ctx context.Context, retry *config.Retry, completedAttempt int) error {
	delay := retryDelay(retry, completedAttempt)
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func retryDelay(retry *config.Retry, completedAttempt int) time.Duration {
	if retry == nil {
		return 0
	}

	delay := parseDuration(retry.InitialDelay)
	if retry.Backoff != "exponential" {
		return delay
	}

	for i := 1; i < completedAttempt; i++ {
		delay *= 2
	}
	return delay
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

func singleJoiningSlash(left string, right string) string {
	leftSlash := strings.HasSuffix(left, "/")
	rightSlash := strings.HasPrefix(right, "/")
	switch {
	case leftSlash && rightSlash:
		return left + right[1:]
	case !leftSlash && !rightSlash:
		return left + "/" + right
	default:
		return left + right
	}
}

func copyRequestHeaders(dst http.Header, src http.Header) {
	for key, values := range src {
		if isHopByHopHeader(key) {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func copyResponseHeaders(dst http.Header, src http.Header) {
	for key, values := range src {
		if isHopByHopHeader(key) {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func appendForwardedFor(header http.Header, remoteAddr string) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return
	}

	prior := header.Get("X-Forwarded-For")
	if prior == "" {
		header.Set("X-Forwarded-For", host)
		return
	}
	header.Set("X-Forwarded-For", prior+", "+host)
}

func isHopByHopHeader(header string) bool {
	switch strings.ToLower(header) {
	case "connection",
		"keep-alive",
		"proxy-authenticate",
		"proxy-authorization",
		"te",
		"trailer",
		"transfer-encoding",
		"upgrade":
		return true
	default:
		return false
	}
}

func isTimeout(ctx context.Context, err error) bool {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
