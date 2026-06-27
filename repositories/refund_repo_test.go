package repositories

import (
	"context"
	"errors"
	"testing"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
)

func TestRefundRepoHoldPathsRequireLedger(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Refund{}); err != nil {
		t.Fatalf("automigrate refunds: %v", err)
	}
	ctx := context.Background()
	merchantID := uuid.New()
	domainID := uuid.New()
	chainID := constants.Ethereum
	session := models.PaymentSession{
		ID:               uuid.New(),
		MerchantID:       merchantID,
		DomainID:         domainID,
		WalletID:         uuid.New(),
		SelectedChainID:  &chainID,
		SelectedSymbol:   "ETH",
		SelectedDecimals: 18,
	}
	refund := &models.Refund{
		ID:         uuid.New(),
		MerchantID: merchantID,
		DomainID:   domainID,
		PaymentID:  session.ID,
		AmountRaw:  "10",
		Status:     models.RefundStatusPending,
	}

	err := NewRefundRepo(db).CreateWithHold(ctx, refund, session, nil)
	if !errors.Is(err, ErrLedgerReservationRequired) {
		t.Fatalf("CreateWithHold err = %v, want ErrLedgerReservationRequired", err)
	}

	if err := db.WithContext(ctx).Create(refund).Error; err != nil {
		t.Fatalf("seed refund: %v", err)
	}
	_, err = NewRefundRepo(db).ClaimPendingWithHold(ctx, refund.ID, "admin@example.com", session, nil)
	if !errors.Is(err, ErrLedgerReservationRequired) {
		t.Fatalf("ClaimPendingWithHold err = %v, want ErrLedgerReservationRequired", err)
	}
}

func TestRefundRepoMarkFailedVoidsPreBroadcastHold(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Refund{}, &models.LedgerEntry{}); err != nil {
		t.Fatalf("automigrate refund hold tables: %v", err)
	}
	ctx := context.Background()
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	chainID := constants.Ethereum
	prefix := "refund-pre-broadcast-failure-" + uuid.NewString()
	depositTx := ledgerTestTransaction(merchantID, domainID, walletID, prefix+":deposit", "100")
	ledgerRepo := NewLedgerRepo(db)
	if err := ledgerRepo.PostStandaloneDepositAvailable(ctx, depositTx); err != nil {
		t.Fatalf("post available balance: %v", err)
	}
	session := models.PaymentSession{
		ID:               uuid.New(),
		MerchantID:       merchantID,
		DomainID:         domainID,
		WalletID:         walletID,
		SelectedChainID:  &chainID,
		SelectedSymbol:   "ETH",
		SelectedDecimals: 18,
	}
	refund := &models.Refund{
		ID:         uuid.New(),
		MerchantID: merchantID,
		DomainID:   domainID,
		PaymentID:  session.ID,
		AmountRaw:  "25",
		Status:     models.RefundStatusPending,
	}
	repo := NewRefundRepo(db)
	if err := repo.CreateWithHold(ctx, refund, session, ledgerRepo); err != nil {
		t.Fatalf("create refund with hold: %v", err)
	}
	requireLedgerCount(t, db, 2, "refund_id = ? AND entry_type = ? AND status = ?", refund.ID, models.LedgerEntryTypeRefundHold, models.LedgerStatusPending)

	if err := repo.MarkFailed(ctx, refund.ID, "admin@example.com", "pre-broadcast signer failure"); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	requireLedgerCount(t, db, 2, "refund_id = ? AND entry_type = ? AND status = ?", refund.ID, models.LedgerEntryTypeRefundHold, models.LedgerStatusVoided)
	if err := ledgerRepo.VoidRefundHold(ctx, refund.ID); err != nil {
		t.Fatalf("duplicate void refund hold: %v", err)
	}
	requireLedgerCount(t, db, 2, "refund_id = ? AND entry_type = ? AND status = ?", refund.ID, models.LedgerEntryTypeRefundHold, models.LedgerStatusVoided)
}

func TestRefundRepoMarkFailedPreservesHoldAfterBroadcast(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Refund{}, &models.LedgerEntry{}); err != nil {
		t.Fatalf("automigrate refund hold tables: %v", err)
	}
	ctx := context.Background()
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	refund := models.Refund{
		ID:         uuid.New(),
		MerchantID: merchantID,
		DomainID:   domainID,
		PaymentID:  uuid.New(),
		AmountRaw:  "10",
		Status:     models.RefundStatusProcessing,
		TxHash:     "0xbroadcast",
	}
	if err := db.WithContext(ctx).Create(&refund).Error; err != nil {
		t.Fatalf("seed refund: %v", err)
	}
	holdRows := []models.LedgerEntry{
		testLedgerEntryWithType("refund-hold-a-"+uuid.NewString(), merchantID, &domainID, &walletID, models.LedgerEntryTypeRefundHold, models.LedgerAccountMerchantAvailable, models.LedgerDirectionDebit, models.LedgerStatusPending, "10"),
		testLedgerEntryWithType("refund-hold-b-"+uuid.NewString(), merchantID, &domainID, &walletID, models.LedgerEntryTypeRefundHold, models.LedgerAccountRefundTransit, models.LedgerDirectionCredit, models.LedgerStatusPending, "10"),
	}
	for i := range holdRows {
		holdRows[i].RefundID = &refund.ID
		holdRows[i].ChainID = constants.Ethereum
	}
	if err := db.WithContext(ctx).Create(&holdRows).Error; err != nil {
		t.Fatalf("seed refund holds: %v", err)
	}

	if err := NewRefundRepo(db).MarkFailed(ctx, refund.ID, "admin@example.com", "post-broadcast failure"); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	requireLedgerCount(t, db, 2, "refund_id = ? AND entry_type = ? AND status = ?", refund.ID, models.LedgerEntryTypeRefundHold, models.LedgerStatusPending)
}
