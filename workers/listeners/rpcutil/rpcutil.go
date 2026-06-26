package rpcutil

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	throttleBackoffBase = 30 * time.Second
	maxThrottleBackoff  = 2 * time.Minute
)

type ThrottleError struct {
	Err        error
	RetryAfter time.Duration
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
		strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "temporary failure")
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
