package rpcutil

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestThrottleDelayUsesExponentialBackoff(t *testing.T) {
	err := NewThrottleError(errors.New("too many requests"), 0)

	if got := ThrottleDelay(err, 1, 10*time.Second); got != 30*time.Second {
		t.Fatalf("first delay = %s, want 30s", got)
	}
	if got := ThrottleDelay(err, 2, 10*time.Second); got != time.Minute {
		t.Fatalf("second delay = %s, want 1m", got)
	}
	if got := ThrottleDelay(err, 8, 10*time.Second); got != 2*time.Minute {
		t.Fatalf("capped delay = %s, want 2m", got)
	}
}

func TestThrottleDelayHonorsRetryAfter(t *testing.T) {
	err := NewThrottleError(errors.New("too many requests"), 90*time.Second)
	if got := ThrottleDelay(err, 1, 10*time.Second); got != 90*time.Second {
		t.Fatalf("delay = %s, want retry-after", got)
	}
}

func TestRetryAfterParsesSeconds(t *testing.T) {
	header := http.Header{}
	header.Set("Retry-After", "45")
	if got := RetryAfter(header); got != 45*time.Second {
		t.Fatalf("retry-after = %s, want 45s", got)
	}
}

func TestJSONRPCThrottled(t *testing.T) {
	if !JSONRPCThrottled(429, "Too many requests for a specific RPC call") {
		t.Fatal("429 JSON-RPC error should be throttled")
	}
	if !JSONRPCThrottled(-32005, "rate limit exceeded") {
		t.Fatal("rate-limit message should be throttled")
	}
	if JSONRPCThrottled(-32601, "method not found") {
		t.Fatal("method-not-found should not be throttled")
	}
}
