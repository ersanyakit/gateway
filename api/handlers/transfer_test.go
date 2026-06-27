package handlers

import (
	"context"
	"testing"

	"core/services/signer"
	requesttypes "core/types"
)

func TestTransferContextWithSignerAuditPropagatesActorAndCorrelation(t *testing.T) {
	ctx := transferContextWithSignerAudit(context.Background(), requesttypes.TransferParams{
		ActorID:       " admin@example.com ",
		JobID:         " payout-job-1 ",
		CorrelationID: " request-123:withdrawal-456 ",
	})

	audit := signer.AuditContextFrom(ctx)
	if audit.ActorID != "admin@example.com" {
		t.Fatalf("ActorID = %q, want admin@example.com", audit.ActorID)
	}
	if audit.JobID != "payout-job-1" {
		t.Fatalf("JobID = %q, want payout-job-1", audit.JobID)
	}
	if audit.CorrelationID != "request-123:withdrawal-456" {
		t.Fatalf("CorrelationID = %q, want request-123:withdrawal-456", audit.CorrelationID)
	}
}
