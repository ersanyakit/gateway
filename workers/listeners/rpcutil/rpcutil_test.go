package rpcutil

import (
	"context"
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

func TestThrottleDelayBacksOffRetryableTimeouts(t *testing.T) {
	err := errors.Join(errors.New("receipt fetch failed"), context.DeadlineExceeded)
	if got := ThrottleDelay(err, 1, 10*time.Second); got != 30*time.Second {
		t.Fatalf("timeout delay = %s, want 30s", got)
	}
}

func TestIsRetryable(t *testing.T) {
	if !IsRetryable(context.DeadlineExceeded) {
		t.Fatal("context deadline should be retryable")
	}
	if !IsRetryable(errors.New("Post \"https://base.drpc.org\": context deadline exceeded")) {
		t.Fatal("wrapped timeout message should be retryable")
	}
	if !IsRetryable(errors.New(`rpc error: code = Unavailable desc = unexpected HTTP status code received from server: 429 (Too Many Requests); transport: received unexpected content-type "application/octet-stream"`)) {
		t.Fatal("gRPC 429 message should be retryable")
	}
	if IsRetryable(errors.New("method not found")) {
		t.Fatal("non-transient RPC error should not be retryable")
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

func TestEndpointCircuitMovesRetryableFailuresBehindHealthyEndpoints(t *testing.T) {
	circuit := NewEndpointCircuit()
	urls := []string{"https://slow", "https://healthy"}

	circuit.RecordFailure("https://slow", context.DeadlineExceeded)
	got := circuit.Rank(urls)
	if len(got) != 2 || got[0] != "https://healthy" || got[1] != "https://slow" {
		t.Fatalf("ranked endpoints = %#v, want healthy first", got)
	}

	circuit.RecordSuccess("https://slow")
	got = circuit.Rank(urls)
	if len(got) != 2 || got[0] != "https://slow" {
		t.Fatalf("ranked endpoints after success = %#v, want original order restored", got)
	}
}

func TestEndpointTimeoutReadsDurationFromEnv(t *testing.T) {
	t.Setenv("CHAIN_RPC_ENDPOINT_TIMEOUT", "150ms")
	if got := EndpointTimeout(); got != 150*time.Millisecond {
		t.Fatalf("endpoint timeout = %s, want 150ms", got)
	}
}
