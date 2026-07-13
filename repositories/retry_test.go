package repositories

import (
	"testing"
	"time"
)

func TestWebhookRetryBackoffCapsAtMax(t *testing.T) {
	t.Setenv("WEBHOOK_RETRY_BACKOFF_BASE", "2s")
	t.Setenv("WEBHOOK_RETRY_BACKOFF_MAX", "10s")

	tests := []struct {
		attempt uint
		want    time.Duration
	}{
		{attempt: 1, want: 2 * time.Second},
		{attempt: 2, want: 4 * time.Second},
		{attempt: 3, want: 8 * time.Second},
		{attempt: 4, want: 10 * time.Second},
		{attempt: 9, want: 10 * time.Second},
	}
	for _, tt := range tests {
		if got := webhookRetryBackoff(tt.attempt); got != tt.want {
			t.Fatalf("webhookRetryBackoff(%d) = %s, want %s", tt.attempt, got, tt.want)
		}
	}
}

func TestUintFromEnvFallsBackOnInvalidValues(t *testing.T) {
	t.Setenv("TEST_UINT_FROM_ENV", "0")
	if got := uintFromEnv("TEST_UINT_FROM_ENV", 7); got != 7 {
		t.Fatalf("uintFromEnv zero = %d, want 7", got)
	}
	t.Setenv("TEST_UINT_FROM_ENV", "12")
	if got := uintFromEnv("TEST_UINT_FROM_ENV", 7); got != 12 {
		t.Fatalf("uintFromEnv valid = %d, want 12", got)
	}
}
