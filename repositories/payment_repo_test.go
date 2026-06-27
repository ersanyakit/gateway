package repositories

import (
	"context"
	"testing"
	"time"

	"core/models"

	"github.com/google/uuid"
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

func TestMarkPaidByTransactionRequiresConfirmedFinalizedTransaction(t *testing.T) {
	walletID := uuid.New()
	finalizedAt := time.Now()
	repo := &PaymentRepo{}

	cases := []models.Transaction{
		{
			WalletID: &walletID,
			Amount:   "100",
			Status:   models.TransactionStatusPendingConfirmation,
		},
		{
			WalletID: &walletID,
			Amount:   "100",
			Status:   models.TransactionStatusConfirmed,
		},
		{
			WalletID:              &walletID,
			Amount:                "100",
			Status:                models.TransactionStatusPendingConfirmation,
			FinalizedAt:           &finalizedAt,
			UniqueHash:            "pending-finality",
			Hash:                  "0xhash",
			ConfirmationsRequired: 12,
		},
	}

	for _, txModel := range cases {
		session, changed, err := repo.MarkPaidByTransaction(context.Background(), txModel)
		if err != nil {
			t.Fatalf("pre-finality mark paid returned error: %v", err)
		}
		if session != nil || changed {
			t.Fatalf("pre-finality mark paid changed state: session=%#v changed=%v", session, changed)
		}
	}
}
