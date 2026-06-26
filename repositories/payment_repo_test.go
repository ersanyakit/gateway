package repositories

import (
	"testing"

	"core/models"
)

func TestPaymentStatusBlocksCancelIncludesUnderpaid(t *testing.T) {
	terminalStatuses := []string{
		models.PaymentStatusPaid,
		models.PaymentStatusCanceled,
		models.PaymentStatusExpired,
		models.PaymentStatusFailed,
		models.PaymentStatusUnderpaid,
	}
	for _, status := range terminalStatuses {
		if !paymentStatusBlocksCancel(status) {
			t.Fatalf("status %q should block cancel mutation", status)
		}
	}
	if paymentStatusBlocksCancel(models.PaymentStatusAwaitingPayment) {
		t.Fatal("awaiting_payment should remain cancelable")
	}
}
