package rpcutil

import (
	"context"
	"errors"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	throttleBackoffBase = 30 * time.Second
	maxThrottleBackoff  = 2 * time.Minute
	endpointTimeout     = 12 * time.Second
	endpointCooldown    = 5 * time.Second
	maxEndpointCooldown = 2 * time.Minute
)

type ThrottleError struct {
	Err        error
	RetryAfter time.Duration
}

type EndpointCircuit struct {
	mu           sync.Mutex
	failures     map[string]int
	blockedUntil map[string]time.Time
}

func NewEndpointCircuit() *EndpointCircuit {
	return &EndpointCircuit{
		failures:     make(map[string]int),
		blockedUntil: make(map[string]time.Time),
	}
}

func (e *ThrottleError) Error() string {
	return e.Err.Error()
}

func (e *ThrottleError) Unwrap() error {
	return e.Err
}

func NewThrottleError(err error, retryAfter time.Duration) error {
	if err == nil {
		err = errors.New("RPC throttled")
	}
	return &ThrottleError{Err: err, RetryAfter: retryAfter}
}

func IsThrottle(err error) bool {
	if err == nil {
		return false
	}
	var throttleErr *ThrottleError
	if errors.As(err, &throttleErr) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "rate-limit") ||
		strings.Contains(msg, "throttled")
}

func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if IsThrottle(err) {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "client.timeout exceeded") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "tls handshake timeout") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "server misbehaving") ||
		strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "temporary failure") ||
		strings.Contains(msg, "unexpected eof") ||
		strings.Contains(msg, " unavailable")
}

func ThrottleDelay(err error, consecutive int, normal time.Duration) time.Duration {
	if !IsRetryable(err) {
		return normal
	}
	if consecutive < 1 {
		consecutive = 1
	}

	delay := throttleBackoffBase
	for i := 1; i < consecutive && delay < maxThrottleBackoff; i++ {
		delay *= 2
	}
	if delay > maxThrottleBackoff {
		delay = maxThrottleBackoff
	}

	var throttleErr *ThrottleError
	if errors.As(err, &throttleErr) && throttleErr.RetryAfter > delay {
		delay = throttleErr.RetryAfter
	}
	if delay < normal {
		return normal
	}
	if delay > maxThrottleBackoff {
		return maxThrottleBackoff
	}
	return delay
}

func StatusThrottled(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests ||
		statusCode == http.StatusBadGateway ||
		statusCode == http.StatusServiceUnavailable ||
		statusCode == http.StatusGatewayTimeout
}

func JSONRPCThrottled(code int, message string) bool {
	if code == http.StatusTooManyRequests {
		return true
	}
	msg := strings.ToLower(message)
	return strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "rate-limit") ||
		strings.Contains(msg, "limit exceeded") ||
		strings.Contains(msg, "throttled")
}

func RetryAfter(header http.Header) time.Duration {
	raw := strings.TrimSpace(header.Get("Retry-After"))
	if raw == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(raw)
	if err != nil {
		return 0
	}
	delay := time.Until(when)
	if delay <= 0 {
		return 0
	}
	return delay
}

func EndpointTimeout() time.Duration {
	for _, key := range []string{"CHAIN_RPC_ENDPOINT_TIMEOUT", "RPC_ENDPOINT_TIMEOUT"} {
		raw := strings.TrimSpace(os.Getenv(key))
		if raw == "" {
			continue
		}
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			return parsed
		}
		if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	return endpointTimeout
}

func WithEndpointTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	timeout := EndpointTimeout()
	if timeout <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, timeout)
}

func (c *EndpointCircuit) Rank(urls []string) []string {
	if c == nil || len(urls) <= 1 {
		return cloneStrings(urls)
	}
	now := time.Now()
	type rankedEndpoint struct {
		url     string
		blocked bool
		until   time.Time
		order   int
	}
	items := make([]rankedEndpoint, 0, len(urls))
	c.mu.Lock()
	for i, url := range urls {
		until := c.blockedUntil[url]
		blocked := !until.IsZero() && now.Before(until)
		if !blocked && !until.IsZero() {
			delete(c.blockedUntil, url)
		}
		items = append(items, rankedEndpoint{url: url, blocked: blocked, until: until, order: i})
	}
	c.mu.Unlock()
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if left.blocked != right.blocked {
			return !left.blocked
		}
		if left.blocked && !left.until.Equal(right.until) {
			return left.until.Before(right.until)
		}
		return left.order < right.order
	})
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.url)
	}
	return out
}

func (c *EndpointCircuit) RecordSuccess(url string) {
	if c == nil || strings.TrimSpace(url) == "" {
		return
	}
	c.mu.Lock()
	delete(c.failures, url)
	delete(c.blockedUntil, url)
	c.mu.Unlock()
}

func (c *EndpointCircuit) RecordFailure(url string, err error) {
	if c == nil || strings.TrimSpace(url) == "" || !IsRetryable(err) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures[url]++
	delay := endpointCooldown
	for i := 1; i < c.failures[url] && delay < maxEndpointCooldown; i++ {
		delay *= 2
	}
	if delay > maxEndpointCooldown {
		delay = maxEndpointCooldown
	}
	var throttleErr *ThrottleError
	if errors.As(err, &throttleErr) && throttleErr.RetryAfter > delay {
		delay = throttleErr.RetryAfter
	}
	if delay > maxEndpointCooldown {
		delay = maxEndpointCooldown
	}
	c.blockedUntil[url] = time.Now().Add(delay)
}

func cloneStrings(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	return out
}
