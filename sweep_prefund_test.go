package main

import (
	"testing"
	"time"

	"core/models"
)

func TestShouldAttemptSweepPrefundHonorsRetryWindow(t *testing.T) {
	t.Setenv("SWEEP_PREFUND_RETRY_AFTER", "10m")

	if !shouldAttemptSweepPrefund(nil) {
		t.Fatal("nil job should allow prefund attempt")
	}

	recent := time.Now().Add(-time.Minute)
	if shouldAttemptSweepPrefund(&models.SweepJob{PrefundedAt: &recent}) {
		t.Fatal("recent prefund should not be retried")
	}

	stale := time.Now().Add(-11 * time.Minute)
	if !shouldAttemptSweepPrefund(&models.SweepJob{PrefundedAt: &stale}) {
		t.Fatal("stale prefund should be retried")
	}
}
