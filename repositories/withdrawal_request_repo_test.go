package repositories

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestWithdrawalWalletChainLockKeyNormalizesChain(t *testing.T) {
	walletID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	got := withdrawalWalletChainLockKey(walletID, " Ethereum ")
	want := "withdrawal-wallet-chain:11111111-1111-1111-1111-111111111111:ethereum"
	if got != want {
		t.Fatalf("lock key = %q, want %q", got, want)
	}
}

func TestWithdrawalWalletBusyErrorIsComparable(t *testing.T) {
	err := errors.Join(ErrWithdrawalWalletBusy)
	if !errors.Is(err, ErrWithdrawalWalletBusy) {
		t.Fatal("withdrawal wallet busy sentinel should be comparable")
	}
}

func TestWithdrawalRequestRepoCreateWithHoldRequiresLedger(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.WithdrawalRequest{}); err != nil {
		t.Fatalf("automigrate withdrawal requests: %v", err)
	}
	request := &models.WithdrawalRequest{
		ID:         uuid.New(),
		MerchantID: uuid.New(),
		WalletID:   uuid.New(),
		Chain:      "ethereum",
		Symbol:     "ETH",
		Decimals:   18,
		ToAddress:  "0xto",
		AmountRaw:  "10",
		Status:     models.WithdrawalStatusPending,
	}
	err := NewWithdrawalRequestRepo(db).CreateWithHold(context.Background(), request, nil)
	if !errors.Is(err, ErrLedgerReservationRequired) {
		t.Fatalf("CreateWithHold err = %v, want ErrLedgerReservationRequired", err)
	}
}

func TestWithdrawalRequestRepoApproveRequiresExistingHoldBeforeTransfer(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.WithdrawalRequest{}, &models.LedgerEntry{}); err != nil {
		t.Fatalf("automigrate withdrawal hold tables: %v", err)
	}
	ctx := context.Background()
	merchantID, domainID, walletID := seedWithdrawalOwner(t, db)
	request := models.WithdrawalRequest{
		ID:         uuid.New(),
		MerchantID: merchantID,
		DomainID:   &domainID,
		WalletID:   walletID,
		Chain:      "ethereum",
		Symbol:     "ETH",
		Decimals:   18,
		ToAddress:  "0xto",
		AmountRaw:  "10",
		Status:     models.WithdrawalStatusPending,
	}
	if err := db.WithContext(ctx).Create(&request).Error; err != nil {
		t.Fatalf("seed withdrawal request: %v", err)
	}

	called := false
	_, err := NewWithdrawalRequestRepo(db).ApproveWithTransfer(ctx, request.ID, "admin@example.com", NewLedgerRepo(db), func(*models.WithdrawalRequest) (string, error) {
		called = true
		return "0xtx", nil
	})
	if !errors.Is(err, ErrLedgerReservationRequired) {
		t.Fatalf("ApproveWithTransfer err = %v, want ErrLedgerReservationRequired", err)
	}
	if called {
		t.Fatal("transfer callback ran without an existing ledger hold")
	}
	var after models.WithdrawalRequest
	if err := db.WithContext(ctx).First(&after, "id = ?", request.ID).Error; err != nil {
		t.Fatal(err)
	}
	if after.Status != models.WithdrawalStatusPending || after.TxHash != "" {
		t.Fatalf("withdrawal mutated without hold: status=%s tx=%s", after.Status, after.TxHash)
	}
}

func TestWithdrawalRequestRepoApproveTransferFailureVoidsPreBroadcastHold(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.WithdrawalRequest{}, &models.LedgerEntry{}); err != nil {
		t.Fatalf("automigrate withdrawal hold tables: %v", err)
	}
	ctx := context.Background()
	merchantID, domainID, walletID := seedWithdrawalOwner(t, db)
	prefix := "withdrawal-transfer-failure-" + uuid.NewString()
	depositTx := ledgerTestTransaction(merchantID, domainID, walletID, prefix+":deposit", "100")
	ledgerRepo := NewLedgerRepo(db)
	if err := ledgerRepo.PostStandaloneDepositAvailable(ctx, depositTx); err != nil {
		t.Fatalf("post available balance: %v", err)
	}
	request := &models.WithdrawalRequest{
		ID:         uuid.New(),
		MerchantID: merchantID,
		DomainID:   &domainID,
		WalletID:   walletID,
		Chain:      "ethereum",
		Symbol:     "ETH",
		Decimals:   18,
		ToAddress:  "0xto",
		AmountRaw:  "25",
		Status:     models.WithdrawalStatusPending,
	}
	repo := NewWithdrawalRequestRepo(db)
	if err := repo.CreateWithHold(ctx, request, ledgerRepo); err != nil {
		t.Fatalf("create withdrawal with hold: %v", err)
	}
	requireLedgerCount(t, db, 2, "withdrawal_id = ? AND entry_type = ? AND status = ?", request.ID, models.LedgerEntryTypeWithdrawalHold, models.LedgerStatusPending)

	transferErr := fmt.Errorf("signer unavailable")
	after, err := repo.ApproveWithTransfer(ctx, request.ID, "admin@example.com", ledgerRepo, func(*models.WithdrawalRequest) (string, error) {
		return "", transferErr
	})
	if !errors.Is(err, transferErr) {
		t.Fatalf("ApproveWithTransfer err = %v, want %v", err, transferErr)
	}
	if after == nil || after.Status != models.WithdrawalStatusFailed || after.TxHash != "" {
		t.Fatalf("failed withdrawal state = %#v", after)
	}
	requireLedgerCount(t, db, 2, "withdrawal_id = ? AND entry_type = ? AND status = ?", request.ID, models.LedgerEntryTypeWithdrawalHold, models.LedgerStatusVoided)
	if err := ledgerRepo.VoidWithdrawalHold(ctx, request.ID); err != nil {
		t.Fatalf("duplicate void withdrawal hold: %v", err)
	}
	requireLedgerCount(t, db, 2, "withdrawal_id = ? AND entry_type = ? AND status = ?", request.ID, models.LedgerEntryTypeWithdrawalHold, models.LedgerStatusVoided)
}

func TestWithdrawalRequestRepoRecordBroadcastRequiresHashAndKeepsProcessing(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.WithdrawalRequest{}); err != nil {
		t.Fatalf("automigrate withdrawal tables: %v", err)
	}
	ctx := context.Background()
	merchantID, domainID, walletID := seedWithdrawalOwner(t, db)
	request := models.WithdrawalRequest{
		ID:         uuid.New(),
		MerchantID: merchantID,
		DomainID:   &domainID,
		WalletID:   walletID,
		Chain:      "ethereum",
		Symbol:     "ETH",
		Decimals:   18,
		ToAddress:  "0xto",
		AmountRaw:  "10",
		Status:     models.WithdrawalStatusProcessing,
	}
	if err := db.WithContext(ctx).Create(&request).Error; err != nil {
		t.Fatalf("seed withdrawal: %v", err)
	}
	repo := NewWithdrawalRequestRepo(db)

	if err := repo.RecordBroadcast(ctx, request.ID, "admin@example.com", "  "); !errors.Is(err, ErrWithdrawalTxHashRequired) {
		t.Fatalf("blank tx hash err = %v, want ErrWithdrawalTxHashRequired", err)
	}
	if err := repo.RecordBroadcast(ctx, request.ID, "admin@example.com", " 0xtx "); err != nil {
		t.Fatalf("record broadcast: %v", err)
	}
	var after models.WithdrawalRequest
	if err := db.WithContext(ctx).First(&after, "id = ?", request.ID).Error; err != nil {
		t.Fatal(err)
	}
	if after.Status != models.WithdrawalStatusProcessing || after.TxHash != "0xtx" || after.BroadcastedAt == nil || after.FinalizedAt != nil {
		t.Fatalf("broadcast state = status:%s tx:%q broadcasted:%v finalized:%v", after.Status, after.TxHash, after.BroadcastedAt, after.FinalizedAt)
	}
}

func TestWithdrawalRequestRepoApproveStopsAtBroadcastAndFinalizesLater(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.WithdrawalRequest{}, &models.LedgerEntry{}); err != nil {
		t.Fatalf("automigrate withdrawal lifecycle tables: %v", err)
	}
	ctx := context.Background()
	merchantID, domainID, walletID := seedWithdrawalOwner(t, db)
	prefix := "withdrawal-broadcast-finalize-" + uuid.NewString()
	depositTx := ledgerTestTransaction(merchantID, domainID, walletID, prefix+":deposit", "100")
	ledgerRepo := NewLedgerRepo(db)
	if err := ledgerRepo.PostStandaloneDepositAvailable(ctx, depositTx); err != nil {
		t.Fatalf("post available balance: %v", err)
	}
	request := &models.WithdrawalRequest{
		ID:             uuid.New(),
		MerchantID:     merchantID,
		DomainID:       &domainID,
		WalletID:       walletID,
		Chain:          "ethereum",
		Symbol:         "ETH",
		Decimals:       18,
		ToAddress:      "0xto",
		AmountRaw:      "25",
		Status:         models.WithdrawalStatusPending,
		IdempotencyKey: "payout-key",
		CorrelationID:  "corr-payout",
	}
	repo := NewWithdrawalRequestRepo(db)
	if err := repo.CreateWithHold(ctx, request, ledgerRepo); err != nil {
		t.Fatalf("create withdrawal with hold: %v", err)
	}

	after, err := repo.ApproveWithTransfer(ctx, request.ID, "admin@example.com", ledgerRepo, func(*models.WithdrawalRequest) (string, error) {
		return " 0xbroadcast ", nil
	})
	if err != nil {
		t.Fatalf("approve transfer: %v", err)
	}
	if after == nil || after.Status != models.WithdrawalStatusProcessing || after.TxHash != "0xbroadcast" || after.BroadcastedAt == nil || after.FinalizedAt != nil {
		t.Fatalf("approved broadcast state = %#v", after)
	}
	requireLedgerCount(t, db, 0, "withdrawal_id = ? AND entry_type = ?", request.ID, models.LedgerEntryTypeWithdrawalDebit)

	if err := repo.FinalizeProcessingWithLedger(ctx, request.ID, ledgerRepo); err != nil {
		t.Fatalf("finalize withdrawal: %v", err)
	}
	var finalized models.WithdrawalRequest
	if err := db.WithContext(ctx).First(&finalized, "id = ?", request.ID).Error; err != nil {
		t.Fatal(err)
	}
	if finalized.Status != models.WithdrawalStatusFinalized || finalized.FinalizedAt == nil || finalized.TxHash != "0xbroadcast" {
		t.Fatalf("finalized state = status:%s tx:%q finalized:%v", finalized.Status, finalized.TxHash, finalized.FinalizedAt)
	}
	requireLedgerCount(t, db, 2, "withdrawal_id = ? AND entry_type = ?", request.ID, models.LedgerEntryTypeWithdrawalDebit)
	if err := repo.FinalizeProcessingWithLedger(ctx, request.ID, ledgerRepo); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("duplicate finalize err = %v, want gorm.ErrRecordNotFound", err)
	}
	requireLedgerCount(t, db, 2, "withdrawal_id = ? AND entry_type = ?", request.ID, models.LedgerEntryTypeWithdrawalDebit)
}

func TestWithdrawalRequestRepoMarkFailedPreservesHoldAfterBroadcast(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.WithdrawalRequest{}, &models.LedgerEntry{}); err != nil {
		t.Fatalf("automigrate withdrawal hold tables: %v", err)
	}
	ctx := context.Background()
	merchantID, domainID, walletID := seedWithdrawalOwner(t, db)
	request := models.WithdrawalRequest{
		ID:         uuid.New(),
		MerchantID: merchantID,
		DomainID:   &domainID,
		WalletID:   walletID,
		Chain:      "ethereum",
		Symbol:     "ETH",
		Decimals:   18,
		ToAddress:  "0xto",
		AmountRaw:  "10",
		Status:     models.WithdrawalStatusProcessing,
		TxHash:     "0xbroadcast",
	}
	if err := db.WithContext(ctx).Create(&request).Error; err != nil {
		t.Fatalf("seed withdrawal request: %v", err)
	}
	holdRows := []models.LedgerEntry{
		testLedgerEntryWithType("hold-a-"+uuid.NewString(), merchantID, &domainID, &walletID, models.LedgerEntryTypeWithdrawalHold, models.LedgerAccountMerchantAvailable, models.LedgerDirectionDebit, models.LedgerStatusPending, "10"),
		testLedgerEntryWithType("hold-b-"+uuid.NewString(), merchantID, &domainID, &walletID, models.LedgerEntryTypeWithdrawalHold, models.LedgerAccountWithdrawalTransit, models.LedgerDirectionCredit, models.LedgerStatusPending, "10"),
	}
	for i := range holdRows {
		holdRows[i].WithdrawalID = &request.ID
		holdRows[i].ChainID = constants.Ethereum
	}
	if err := db.WithContext(ctx).Create(&holdRows).Error; err != nil {
		t.Fatalf("seed withdrawal holds: %v", err)
	}

	if err := NewWithdrawalRequestRepo(db).MarkFailed(ctx, request.ID, "admin@example.com", "post-broadcast failure"); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	requireLedgerCount(t, db, 2, "withdrawal_id = ? AND entry_type = ? AND status = ?", request.ID, models.LedgerEntryTypeWithdrawalHold, models.LedgerStatusPending)
}

func TestWithdrawalRequestRepoRecordBroadcastRequiresTxHashAndStoresTimestamp(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.WithdrawalRequest{}); err != nil {
		t.Fatalf("automigrate withdrawal tables: %v", err)
	}
	ctx := context.Background()
	merchantID, domainID, walletID := seedWithdrawalOwner(t, db)
	request := models.WithdrawalRequest{
		ID:         uuid.New(),
		MerchantID: merchantID,
		DomainID:   &domainID,
		WalletID:   walletID,
		Chain:      "ethereum",
		Symbol:     "ETH",
		Decimals:   18,
		ToAddress:  "0xto",
		AmountRaw:  "10",
		Status:     models.WithdrawalStatusProcessing,
	}
	if err := db.WithContext(ctx).Create(&request).Error; err != nil {
		t.Fatalf("seed withdrawal request: %v", err)
	}
	repo := NewWithdrawalRequestRepo(db)
	if err := repo.RecordBroadcast(ctx, request.ID, "admin@example.com", "   "); !errors.Is(err, ErrTxHashRequired) {
		t.Fatalf("blank tx hash err = %v, want ErrTxHashRequired", err)
	}
	if err := repo.RecordBroadcast(ctx, request.ID, "admin@example.com", "0xbroadcast"); err != nil {
		t.Fatalf("record broadcast: %v", err)
	}
	var after models.WithdrawalRequest
	if err := db.WithContext(ctx).First(&after, "id = ?", request.ID).Error; err != nil {
		t.Fatal(err)
	}
	if after.TxHash != "0xbroadcast" || after.BroadcastedAt == nil {
		t.Fatalf("broadcast metadata not persisted: tx=%q broadcasted_at=%v", after.TxHash, after.BroadcastedAt)
	}
	if after.Status != models.WithdrawalStatusProcessing {
		t.Fatalf("broadcast must remain non-terminal processing, got %s", after.Status)
	}
}

func seedWithdrawalOwner(t *testing.T, db *gorm.DB) (uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	merchant := models.Merchant{ID: merchantID, Name: "Test Merchant", Email: "merchant-" + merchantID.String() + "@example.com", IsActive: true}
	domain := models.Domain{ID: domainID, MerchantID: merchantID, DomainURL: "https://example.com", APIKey: "key-" + domainID.String(), APISecret: "secret", HDAccountID: 1}
	wallet := models.Wallet{ID: walletID, MerchantID: merchantID, DomainID: domainID, HDAccountID: 1, HDAddressId: 0, EthereumAddress: "0x" + strings.ReplaceAll(walletID.String(), "-", "")}
	if err := db.Create(&merchant).Error; err != nil {
		t.Fatalf("seed merchant: %v", err)
	}
	if err := db.Create(&domain).Error; err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	if err := db.Create(&wallet).Error; err != nil {
		t.Fatalf("seed wallet: %v", err)
	}
	return merchantID, domainID, walletID
}
