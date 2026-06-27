package repositories

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
)

func TestPaymentStatusBlocksCancelIncludesExplicitOutcomeStatuses(t *testing.T) {
	terminalStatuses := []string{
		models.PaymentStatusPaid,
		models.PaymentStatusCanceled,
		models.PaymentStatusExpired,
		models.PaymentStatusFailed,
		models.PaymentStatusUnderpaid,
		models.PaymentStatusOverpaid,
		models.PaymentStatusPartialPaid,
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
		result, err := repo.MatchFinalizedTransaction(context.Background(), txModel)
		if err != nil {
			t.Fatalf("pre-finality match returned error: %v", err)
		}
		if result != nil {
			t.Fatalf("pre-finality match changed state: result=%#v", result)
		}
	}
}

func TestPaymentMatchDecisionClassifiesExplicitOutcomes(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	future := now.Add(10 * time.Minute)
	past := now.Add(-time.Minute)
	token := "0xToken"
	session := models.PaymentSession{
		Status:            models.PaymentStatusAwaitingPayment,
		SelectedChainID:   ptr(constants.Ethereum),
		SelectedSymbol:    "USDC",
		SelectedToken:     &token,
		ExpectedAmountRaw: "1000",
		ExpiresAt:         &future,
	}

	tests := []struct {
		name       string
		session    models.PaymentSession
		tx         models.Transaction
		status     string
		outcome    string
		event      string
		shortfall  string
		excess     string
		ledger     bool
		shouldFind bool
	}{
		{
			name:       "exact match succeeds",
			tx:         paymentMatchTestTx(constants.Ethereum, "USDC", &token, "1000"),
			status:     models.PaymentStatusPaid,
			outcome:    models.PaymentOutcomeExact,
			event:      constants.WebhookEventPaymentSucceeded,
			ledger:     true,
			shouldFind: true,
		},
		{
			name:       "minor underpayment is explicit underpaid",
			tx:         paymentMatchTestTx(constants.Ethereum, "USDC", &token, "997"),
			status:     models.PaymentStatusUnderpaid,
			outcome:    models.PaymentOutcomeUnderpaid,
			event:      constants.WebhookEventPaymentUnderpaid,
			shortfall:  "3",
			ledger:     true,
			shouldFind: true,
		},
		{
			name:       "large underpayment is unsupported partial paid",
			tx:         paymentMatchTestTx(constants.Ethereum, "USDC", &token, "500"),
			status:     models.PaymentStatusPartialPaid,
			outcome:    models.PaymentOutcomePartialUnsupported,
			event:      constants.WebhookEventPaymentPartialPaid,
			shortfall:  "500",
			ledger:     true,
			shouldFind: true,
		},
		{
			name:       "overpayment is explicit overpaid",
			tx:         paymentMatchTestTx(constants.Ethereum, "USDC", &token, "1201"),
			status:     models.PaymentStatusOverpaid,
			outcome:    models.PaymentOutcomeOverpaid,
			event:      constants.WebhookEventPaymentOverpaid,
			excess:     "201",
			ledger:     true,
			shouldFind: true,
		},
		{
			name: "expired session cannot become paid",
			session: func() models.PaymentSession {
				s := session
				s.ExpiresAt = &past
				return s
			}(),
			tx:         paymentMatchTestTx(constants.Ethereum, "USDC", &token, "1000"),
			status:     models.PaymentStatusExpired,
			outcome:    models.PaymentOutcomeExpiredAfterDeposit,
			event:      constants.WebhookEventPaymentExpired,
			ledger:     true,
			shouldFind: true,
		},
		{
			name:       "wrong chain is explicit failure",
			tx:         paymentMatchTestTx(constants.Base, "USDC", &token, "1000"),
			status:     models.PaymentStatusFailed,
			outcome:    models.PaymentOutcomeWrongChain,
			event:      constants.WebhookEventPaymentFailed,
			ledger:     true,
			shouldFind: true,
		},
		{
			name:       "wrong asset is explicit failure",
			tx:         paymentMatchTestTx(constants.Ethereum, "ETH", nil, "1000"),
			status:     models.PaymentStatusFailed,
			outcome:    models.PaymentOutcomeWrongAsset,
			event:      constants.WebhookEventPaymentFailed,
			ledger:     true,
			shouldFind: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testSession := tt.session
			if testSession.Status == "" {
				testSession = session
			}
			decision, ok := paymentMatchDecisionForSession(testSession, tt.tx, now)
			if ok != tt.shouldFind {
				t.Fatalf("matched = %v, want %v", ok, tt.shouldFind)
			}
			if !ok {
				return
			}
			if decision.Status != tt.status || decision.Outcome != tt.outcome || decision.WebhookEvent != tt.event {
				t.Fatalf("decision = %#v, want status=%q outcome=%q event=%q", decision, tt.status, tt.outcome, tt.event)
			}
			if decision.ShortfallAmountRaw != tt.shortfall || decision.ExcessAmountRaw != tt.excess || decision.LedgerEligible != tt.ledger {
				t.Fatalf("amount/ledger decision = %#v", decision)
			}
		})
	}
}

func TestPaymentMatchSourceKeepsIdempotencyGuardrails(t *testing.T) {
	source := readPaymentRepoSource(t)
	for _, token := range []string{
		"func (r *PaymentRepo) MatchFinalizedTransaction",
		`pg_advisory_xact_lock(hashtext(?))`,
		`"payment-tx:"+txModel.UniqueHash`,
		`Where("tx_unique_hash = ?", txModel.UniqueHash)`,
		"LedgerEligible",
	} {
		if !contains(source, token) {
			t.Fatalf("payment matching source missing %q", token)
		}
	}
}

func paymentMatchTestTx(chainID constants.ChainID, symbol string, token *string, amount string) models.Transaction {
	finalizedAt := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	return models.Transaction{
		ChainID:               chainID,
		Token:                 token,
		Symbol:                symbol,
		Amount:                amount,
		Status:                models.TransactionStatusConfirmed,
		FinalizedAt:           &finalizedAt,
		UniqueHash:            "tx-" + amount + "-" + symbol,
		Hash:                  "0xhash",
		ConfirmationsRequired: 12,
	}
}

func ptr[T any](value T) *T {
	return &value
}

func readPaymentRepoSource(t *testing.T) string {
	t.Helper()
	sourceBytes, err := os.ReadFile("payment_repo.go")
	if err != nil {
		t.Fatalf("read payment_repo.go: %v", err)
	}
	return string(sourceBytes)
}

func contains(source, token string) bool {
	return strings.Contains(source, token)
}
