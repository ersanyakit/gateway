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
	"gorm.io/gorm"
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

func TestPaymentMatchSelectionPrefersExactCandidateBeforeFailure(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	future := now.Add(10 * time.Minute)
	token := "0xToken"
	wrongChain := models.PaymentSession{
		ID:                uuid.New(),
		Status:            models.PaymentStatusAwaitingPayment,
		SelectedChainID:   ptr(constants.Ethereum),
		SelectedSymbol:    "USDC",
		SelectedToken:     &token,
		ExpectedAmountRaw: "1000",
		ExpiresAt:         &future,
	}
	exact := wrongChain
	exact.ID = uuid.New()
	exact.SelectedChainID = ptr(constants.Base)
	txModel := paymentMatchTestTx(constants.Base, "USDC", &token, "1000")

	sessionID, decision, ok := selectPaymentMatchCandidate([]models.PaymentSession{wrongChain, exact}, txModel, now)
	if !ok {
		t.Fatal("expected exact candidate to be selected")
	}
	if sessionID != exact.ID || decision.Status != models.PaymentStatusPaid || decision.Outcome != models.PaymentOutcomeExact {
		t.Fatalf("selected session=%s decision=%#v, want exact paid session %s", sessionID, decision, exact.ID)
	}
}

func TestPaymentMatchSelectionPrefersExactCandidateBeforeSameAssetMismatch(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	future := now.Add(10 * time.Minute)
	token := "0xToken"
	olderPartial := models.PaymentSession{
		ID:                uuid.New(),
		Status:            models.PaymentStatusAwaitingPayment,
		SelectedChainID:   ptr(constants.Ethereum),
		SelectedSymbol:    "USDC",
		SelectedToken:     &token,
		ExpectedAmountRaw: "1000",
		ExpiresAt:         &future,
	}
	exact := olderPartial
	exact.ID = uuid.New()
	exact.ExpectedAmountRaw = "500"
	txModel := paymentMatchTestTx(constants.Ethereum, "USDC", &token, "500")

	sessionID, decision, ok := selectPaymentMatchCandidate([]models.PaymentSession{olderPartial, exact}, txModel, now)
	if !ok {
		t.Fatal("expected exact candidate to be selected")
	}
	if sessionID != exact.ID || decision.Status != models.PaymentStatusPaid || decision.Outcome != models.PaymentOutcomeExact {
		t.Fatalf("selected session=%s decision=%#v, want exact paid session %s", sessionID, decision, exact.ID)
	}
}

func TestPaymentMatchSelectionRecordsFailureWhenNoExactCandidateExists(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	future := now.Add(10 * time.Minute)
	token := "0xToken"
	wrongChain := models.PaymentSession{
		ID:                uuid.New(),
		Status:            models.PaymentStatusAwaitingPayment,
		SelectedChainID:   ptr(constants.Ethereum),
		SelectedSymbol:    "USDC",
		SelectedToken:     &token,
		ExpectedAmountRaw: "1000",
		ExpiresAt:         &future,
	}
	txModel := paymentMatchTestTx(constants.Base, "USDC", &token, "1000")

	sessionID, decision, ok := selectPaymentMatchCandidate([]models.PaymentSession{wrongChain}, txModel, now)
	if !ok {
		t.Fatal("expected wrong-chain candidate to be selected when no exact candidate exists")
	}
	if sessionID != wrongChain.ID || decision.Status != models.PaymentStatusFailed || decision.Outcome != models.PaymentOutcomeWrongChain {
		t.Fatalf("selected session=%s decision=%#v, want wrong-chain failed session %s", sessionID, decision, wrongChain.ID)
	}
}

func TestPaymentMatchDecisionPriorityOrdersExactBeforeMismatchAndFailures(t *testing.T) {
	priorities := []paymentMatchDecision{
		{Status: models.PaymentStatusPaid, Outcome: models.PaymentOutcomeExact},
		{Status: models.PaymentStatusUnderpaid, Outcome: models.PaymentOutcomeUnderpaid},
		{Status: models.PaymentStatusPartialPaid, Outcome: models.PaymentOutcomePartialUnsupported},
		{Status: models.PaymentStatusOverpaid, Outcome: models.PaymentOutcomeOverpaid},
		{Status: models.PaymentStatusExpired, Outcome: models.PaymentOutcomeExpiredAfterDeposit},
		{Status: models.PaymentStatusFailed, Outcome: models.PaymentOutcomeWrongChain},
	}
	for i := 1; i < len(priorities); i++ {
		if paymentMatchDecisionPriority(priorities[i-1]) > paymentMatchDecisionPriority(priorities[i]) {
			t.Fatalf("priority[%d]=%d should be <= priority[%d]=%d", i-1, paymentMatchDecisionPriority(priorities[i-1]), i, paymentMatchDecisionPriority(priorities[i]))
		}
	}
}

func TestPaymentMatchSourceKeepsIdempotencyGuardrails(t *testing.T) {
	source := readPaymentRepoSource(t)
	for _, token := range []string{
		"func (r *PaymentRepo) MatchFinalizedTransaction",
		`pg_advisory_xact_lock(hashtext(?))`,
		`"payment-tx:"+txModel.UniqueHash`,
		`Where("tx_unique_hash = ?", txModel.UniqueHash)`,
		"if used > 0",
		"if matchedSession.TxUniqueHash != nil",
		"LedgerEligible",
	} {
		if !contains(source, token) {
			t.Fatalf("payment matching source missing %q", token)
		}
	}
}

func TestPaymentRepoMarkReorgedUpdatesAllMatchedOutcomeStatuses(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.PaymentSession{}); err != nil {
		t.Fatalf("automigrate payment reorg models: %v", err)
	}
	ctx := context.Background()
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	now := time.Now()
	merchant := models.Merchant{
		ID:        merchantID,
		Name:      "Payment Reorg Test",
		Email:     "payment-reorg-" + uuid.NewString() + "@example.test",
		CreatedAt: now,
		UpdatedAt: now,
	}
	domain := models.Domain{
		ID:          domainID,
		MerchantID:  merchantID,
		DomainURL:   "payment-reorg.example.test",
		APIKey:      "pk_" + uuid.NewString(),
		APISecret:   "secret",
		HDAccountID: 7002,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	wallet := models.Wallet{
		ID:              walletID,
		HDAccountID:     7002,
		HDAddressId:     1,
		MerchantID:      merchantID,
		DomainID:        domainID,
		ProductID:       "checkout:test",
		UserID:          "user-" + uuid.NewString(),
		EthereumAddress: "0x" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.WithContext(ctx).Create(&merchant).Error; err != nil {
		t.Fatalf("seed merchant: %v", err)
	}
	if err := db.WithContext(ctx).Create(&domain).Error; err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	if err := db.WithContext(ctx).Create(&wallet).Error; err != nil {
		t.Fatalf("seed wallet: %v", err)
	}

	statuses := []string{
		models.PaymentStatusPaid,
		models.PaymentStatusUnderpaid,
		models.PaymentStatusOverpaid,
		models.PaymentStatusPartialPaid,
		models.PaymentStatusExpired,
		models.PaymentStatusFailed,
	}
	for _, status := range statuses {
		t.Run(status, func(t *testing.T) {
			txUniqueHash := "reorg-payment-" + status + "-" + uuid.NewString()
			txHash := "0x" + strings.ReplaceAll(uuid.NewString(), "-", "")
			webhookSentAt := now.Add(-time.Minute)
			paidAt := now.Add(-2 * time.Minute)
			session := models.PaymentSession{
				ID:                 uuid.New(),
				SessionToken:       "reorg-" + uuid.NewString(),
				MerchantID:         merchantID,
				DomainID:           domainID,
				WalletID:           walletID,
				OrderID:            "order-" + uuid.NewString(),
				Amount:             "10.00",
				Currency:           "USD",
				Status:             status,
				TxUniqueHash:       &txUniqueHash,
				TxHash:             &txHash,
				PaidAt:             &paidAt,
				ConfirmedAt:        &paidAt,
				WebhookEvent:       constants.WebhookEventPaymentUnderpaid,
				WebhookSentAt:      &webhookSentAt,
				WebhookAttempts:    3,
				WebhookLastError:   "previous failure",
				PaymentOutcome:     models.PaymentOutcomeUnderpaid,
				MatchedAmountRaw:   "997",
				ShortfallAmountRaw: "3",
				CreatedAt:          now,
				UpdatedAt:          now,
			}
			if err := db.WithContext(ctx).Create(&session).Error; err != nil {
				t.Fatalf("seed session: %v", err)
			}
			if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				return NewPaymentRepo(tx).MarkReorgedByTransactionWithDB(ctx, tx, txUniqueHash)
			}); err != nil {
				t.Fatalf("mark reorged: %v", err)
			}
			var updated models.PaymentSession
			if err := db.WithContext(ctx).First(&updated, "id = ?", session.ID).Error; err != nil {
				t.Fatalf("load updated session: %v", err)
			}
			if updated.Status != models.PaymentStatusFailed || updated.WebhookEvent != constants.WebhookEventPaymentFailed {
				t.Fatalf("reorged session status/event = %q/%q", updated.Status, updated.WebhookEvent)
			}
			if updated.WebhookSentAt != nil || updated.WebhookAttempts != 0 || updated.WebhookLastError != "" {
				t.Fatalf("reorged session webhook retry fields not reset: %#v", updated)
			}
			if updated.PaidAt != nil {
				t.Fatalf("reorged session paid_at = %#v, want nil", updated.PaidAt)
			}
			if updated.ConfirmedAt != nil {
				t.Fatalf("reorged session confirmed_at = %#v, want nil", updated.ConfirmedAt)
			}
			if updated.PaymentOutcomeReason != "matched transaction was reorged" {
				t.Fatalf("reorg reason = %q", updated.PaymentOutcomeReason)
			}
			if updated.PaymentOutcome != models.PaymentOutcomeUnderpaid {
				t.Fatalf("explicit payment outcome changed to %q", updated.PaymentOutcome)
			}
		})
	}
}

func TestPaymentRepoMarkReorgedBackfillsLegacyPaidOutcome(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.PaymentSession{}); err != nil {
		t.Fatalf("automigrate payment reorg models: %v", err)
	}
	ctx := context.Background()
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	txUniqueHash := "legacy-paid-reorg-" + uuid.NewString()
	txHash := "0x" + strings.ReplaceAll(uuid.NewString(), "-", "")
	now := time.Now()

	if err := db.WithContext(ctx).Create(&models.Merchant{
		ID:        merchantID,
		Name:      "Legacy Paid Reorg",
		Email:     "legacy-paid-reorg-" + uuid.NewString() + "@example.test",
		CreatedAt: now,
		UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed merchant: %v", err)
	}
	if err := db.WithContext(ctx).Create(&models.Domain{
		ID:          domainID,
		MerchantID:  merchantID,
		DomainURL:   "legacy-paid-reorg.example.test",
		APIKey:      "pk_" + uuid.NewString(),
		APISecret:   "secret",
		HDAccountID: 7004,
		CreatedAt:   now,
		UpdatedAt:   now,
	}).Error; err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	if err := db.WithContext(ctx).Create(&models.Wallet{
		ID:              walletID,
		HDAccountID:     7004,
		HDAddressId:     1,
		MerchantID:      merchantID,
		DomainID:        domainID,
		ProductID:       "checkout:legacy-paid",
		UserID:          "user-" + uuid.NewString(),
		EthereumAddress: "0x" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		CreatedAt:       now,
		UpdatedAt:       now,
	}).Error; err != nil {
		t.Fatalf("seed wallet: %v", err)
	}

	session := models.PaymentSession{
		ID:           uuid.New(),
		SessionToken: "legacy-paid-reorg-" + uuid.NewString(),
		MerchantID:   merchantID,
		DomainID:     domainID,
		WalletID:     walletID,
		OrderID:      "order-" + uuid.NewString(),
		Amount:       "10.00",
		Currency:     "USD",
		Status:       models.PaymentStatusPaid,
		TxUniqueHash: &txUniqueHash,
		TxHash:       &txHash,
		WebhookEvent: constants.WebhookEventPaymentSucceeded,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := db.WithContext(ctx).Create(&session).Error; err != nil {
		t.Fatalf("seed legacy paid session: %v", err)
	}
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return NewPaymentRepo(tx).MarkReorgedByTransactionWithDB(ctx, tx, txUniqueHash)
	}); err != nil {
		t.Fatalf("mark reorged: %v", err)
	}

	var updated models.PaymentSession
	if err := db.WithContext(ctx).First(&updated, "id = ?", session.ID).Error; err != nil {
		t.Fatalf("load updated session: %v", err)
	}
	if updated.PaymentOutcome != models.PaymentOutcomeExact || updated.PaymentOutcomeReason != models.PaymentOutcomeReasonReorged {
		t.Fatalf("legacy paid correction outcome = %q/%q", updated.PaymentOutcome, updated.PaymentOutcomeReason)
	}
}

func TestPaymentRepoMarkReorgedDoesNotReopenAlreadyCorrectedWebhook(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.PaymentSession{}); err != nil {
		t.Fatalf("automigrate payment reorg models: %v", err)
	}
	ctx := context.Background()
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	now := time.Now()
	txUniqueHash := "already-corrected-" + uuid.NewString()
	txHash := "0x" + strings.ReplaceAll(uuid.NewString(), "-", "")
	sentAt := now.Add(-time.Minute)
	session := models.PaymentSession{
		ID:                   uuid.New(),
		SessionToken:         "reorg-idempotent-" + uuid.NewString(),
		MerchantID:           merchantID,
		DomainID:             domainID,
		WalletID:             walletID,
		OrderID:              "order-" + uuid.NewString(),
		Amount:               "10.00",
		Currency:             "USD",
		Status:               models.PaymentStatusFailed,
		PaymentOutcomeReason: paymentReorgOutcomeReason,
		TxUniqueHash:         &txUniqueHash,
		TxHash:               &txHash,
		WebhookEvent:         constants.WebhookEventPaymentFailed,
		WebhookSentAt:        &sentAt,
		WebhookAttempts:      1,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if err := db.WithContext(ctx).Create(&models.Merchant{
		ID:        merchantID,
		Name:      "Payment Reorg Idempotency",
		Email:     "payment-reorg-idempotent-" + uuid.NewString() + "@example.test",
		CreatedAt: now,
		UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed merchant: %v", err)
	}
	if err := db.WithContext(ctx).Create(&models.Domain{
		ID:          domainID,
		MerchantID:  merchantID,
		DomainURL:   "payment-reorg-idempotent.example.test",
		APIKey:      "pk_" + uuid.NewString(),
		APISecret:   "secret",
		HDAccountID: 7003,
		CreatedAt:   now,
		UpdatedAt:   now,
	}).Error; err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	if err := db.WithContext(ctx).Create(&models.Wallet{
		ID:              walletID,
		HDAccountID:     7003,
		HDAddressId:     1,
		MerchantID:      merchantID,
		DomainID:        domainID,
		ProductID:       "checkout:test",
		UserID:          "user-" + uuid.NewString(),
		EthereumAddress: "0x" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		CreatedAt:       now,
		UpdatedAt:       now,
	}).Error; err != nil {
		t.Fatalf("seed wallet: %v", err)
	}
	if err := db.WithContext(ctx).Create(&session).Error; err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return NewPaymentRepo(tx).MarkReorgedByTransactionWithDB(ctx, tx, txUniqueHash)
	}); err != nil {
		t.Fatalf("mark reorged again: %v", err)
	}
	var updated models.PaymentSession
	if err := db.WithContext(ctx).First(&updated, "id = ?", session.ID).Error; err != nil {
		t.Fatalf("load updated session: %v", err)
	}
	if updated.WebhookSentAt == nil || !updated.WebhookSentAt.Equal(sentAt) || updated.WebhookAttempts != 1 {
		t.Fatalf("already corrected webhook state changed: %#v", updated)
	}
}

func TestPaymentRepoMatchFinalizedTransactionPersistsOutcomeAndIsIdempotent(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.PaymentSession{}); err != nil {
		t.Fatalf("automigrate payment match models: %v", err)
	}
	ctx := context.Background()
	repo := NewPaymentRepo(db)
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	token := "0xToken"
	chainID := constants.Ethereum
	future := time.Now().Add(10 * time.Minute)
	now := time.Now()
	merchant := models.Merchant{
		ID:        merchantID,
		Name:      "Payment Match Test",
		Email:     "payment-match-" + uuid.NewString() + "@example.test",
		CreatedAt: now,
		UpdatedAt: now,
	}
	domain := models.Domain{
		ID:          domainID,
		MerchantID:  merchantID,
		DomainURL:   "payment-match.example.test",
		APIKey:      "pk_" + uuid.NewString(),
		APISecret:   "secret",
		HDAccountID: 7001,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	wallet := models.Wallet{
		ID:              walletID,
		HDAccountID:     7001,
		HDAddressId:     1,
		MerchantID:      merchantID,
		DomainID:        domainID,
		ProductID:       "checkout:test",
		UserID:          "user-" + uuid.NewString(),
		EthereumAddress: "0x" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.WithContext(ctx).Create(&merchant).Error; err != nil {
		t.Fatalf("seed merchant: %v", err)
	}
	if err := db.WithContext(ctx).Create(&domain).Error; err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	if err := db.WithContext(ctx).Create(&wallet).Error; err != nil {
		t.Fatalf("seed wallet: %v", err)
	}
	session := models.PaymentSession{
		ID:                uuid.New(),
		SessionToken:      "match-" + uuid.NewString(),
		MerchantID:        merchantID,
		DomainID:          domainID,
		WalletID:          walletID,
		OrderID:           "order-" + uuid.NewString(),
		Amount:            "10.00",
		Currency:          "USD",
		SelectedChainID:   &chainID,
		SelectedToken:     &token,
		SelectedSymbol:    "USDC",
		SelectedDecimals:  6,
		ExpectedAmountRaw: "1000",
		DepositAddress:    "0xdeposit",
		Status:            models.PaymentStatusAwaitingPayment,
		ExpiresAt:         &future,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	if err := db.WithContext(ctx).Create(&session).Error; err != nil {
		t.Fatalf("seed payment session: %v", err)
	}
	walletIDCopy := walletID
	merchantIDCopy := merchantID
	domainIDCopy := domainID
	txModel := paymentMatchTestTx(constants.Ethereum, "USDC", &token, "997")
	txModel.WalletID = &walletIDCopy
	txModel.MerchantID = &merchantIDCopy
	txModel.DomainID = &domainIDCopy
	txModel.UniqueHash = "match-tx-" + uuid.NewString()
	txModel.Hash = "0xmatch"

	result, err := repo.MatchFinalizedTransaction(ctx, txModel)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || !result.Changed || result.Session == nil {
		t.Fatalf("match result = %#v, want changed session", result)
	}
	if result.Status != models.PaymentStatusUnderpaid || result.Outcome != models.PaymentOutcomeUnderpaid || result.WebhookEvent != constants.WebhookEventPaymentUnderpaid {
		t.Fatalf("result outcome = %#v", result)
	}
	if result.Session.MatchedAmountRaw != "997" || result.Session.ShortfallAmountRaw != "3" || result.Session.ExcessAmountRaw != "" {
		t.Fatalf("persisted outcome amounts = %#v", result.Session)
	}

	repeated, err := repo.MatchFinalizedTransaction(ctx, txModel)
	if err != nil {
		t.Fatal(err)
	}
	if repeated == nil || repeated.Changed || repeated.Session == nil {
		t.Fatalf("repeated match = %#v, want existing no-op result", repeated)
	}
	if repeated.Session.ID != session.ID || !repeated.LedgerEligible || repeated.Outcome != models.PaymentOutcomeUnderpaid {
		t.Fatalf("repeated match result = %#v", repeated)
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
