package middleware

import (
	"testing"
	"time"
)

func TestHTTPTimeoutsUseSafeDefaultsAndEnvOverrides(t *testing.T) {
	t.Setenv("HTTP_READ_TIMEOUT", "")
	t.Setenv("HTTP_WRITE_TIMEOUT", "")
	t.Setenv("HTTP_IDLE_TIMEOUT", "")
	if got := HTTPReadTimeout(); got != 15*time.Second {
		t.Fatalf("read timeout = %s, want 15s", got)
	}
	if got := HTTPWriteTimeout(); got != 30*time.Second {
		t.Fatalf("write timeout = %s, want 30s", got)
	}
	if got := HTTPIdleTimeout(); got != 60*time.Second {
		t.Fatalf("idle timeout = %s, want 60s", got)
	}

	t.Setenv("HTTP_READ_TIMEOUT", "2s")
	t.Setenv("HTTP_WRITE_TIMEOUT", "3s")
	t.Setenv("HTTP_IDLE_TIMEOUT", "4s")
	if got := HTTPReadTimeout(); got != 2*time.Second {
		t.Fatalf("read timeout = %s, want 2s", got)
	}
	if got := HTTPWriteTimeout(); got != 3*time.Second {
		t.Fatalf("write timeout = %s, want 3s", got)
	}
	if got := HTTPIdleTimeout(); got != 4*time.Second {
		t.Fatalf("idle timeout = %s, want 4s", got)
	}

	t.Setenv("HTTP_READ_TIMEOUT", "invalid")
	if got := HTTPReadTimeout(); got != 15*time.Second {
		t.Fatalf("invalid read timeout = %s, want fallback", got)
	}
}
