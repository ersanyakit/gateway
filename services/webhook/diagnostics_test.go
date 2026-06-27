package webhook

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSanitizeDeliveryTextRedactsSensitiveDiagnostics(t *testing.T) {
	got := SanitizeDeliveryText("callback echoed webhook_secret=plain and raw_signature=abc")
	if got != "redacted sensitive delivery error" {
		t.Fatalf("sanitized = %q, want redacted marker", got)
	}
}

func TestSanitizeDeliveryTextBoundsAndNormalizes(t *testing.T) {
	got := SanitizeDeliveryText("line one\n\tline two " + strings.Repeat("x", 700))
	if strings.Contains(got, "\n") || strings.Contains(got, "\t") {
		t.Fatalf("sanitized contains control whitespace: %q", got)
	}
	if len(got) > maxDeliveryDiagnosticLength {
		t.Fatalf("sanitized length = %d, want <= %d", len(got), maxDeliveryDiagnosticLength)
	}
}

func TestFailureCategory(t *testing.T) {
	if got := FailureCategory(permanent(errors.New("missing config"))); got != "permanent" {
		t.Fatalf("permanent category = %q", got)
	}
	if got := FailureCategory(context.DeadlineExceeded); got != "timeout" {
		t.Fatalf("timeout category = %q", got)
	}
	if got := FailureCategory(errors.New("webhook returned HTTP 500: down")); got != "http_response" {
		t.Fatalf("http category = %q", got)
	}
	if got := FailureCategory(errors.New("connection refused")); got != "network" {
		t.Fatalf("network category = %q", got)
	}
}
