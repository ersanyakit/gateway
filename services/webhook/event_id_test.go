package webhook

import (
	"testing"

	"core/models"

	"github.com/google/uuid"
)

func TestTransactionEventIDIncludesEventType(t *testing.T) {
	tx := models.Transaction{
		UniqueHash: "1-0xabc-log:1",
		EventType:  "native_transfer",
	}
	if got := TransactionEventID(tx); got != "1-0xabc-log:1:native_transfer" {
		t.Fatalf("transaction event id = %q", got)
	}
}

func TestTransactionEventIDFallsBackForMissingEventType(t *testing.T) {
	tx := models.Transaction{UniqueHash: "1-0xabc-log:1"}
	if got := TransactionEventID(tx); got != "1-0xabc-log:1:transaction" {
		t.Fatalf("transaction event id = %q", got)
	}
}

func TestPaymentEventIDIncludesWebhookEvent(t *testing.T) {
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	session := models.PaymentSession{
		ID:           id,
		WebhookEvent: "payment_succeeded",
	}
	if got := PaymentEventID(session); got != "11111111-1111-1111-1111-111111111111:payment_succeeded" {
		t.Fatalf("payment event id = %q", got)
	}
}
