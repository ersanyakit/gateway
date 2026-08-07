package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

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

func TestWithdrawalRequestRepoCreateRecoverWithHoldReleasesSweepLockedBalance(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.WithdrawalRequest{}, &models.LedgerEntry{}); err != nil {
		t.Fatalf("automigrate recovery withdrawal tables: %v", err)
	}
	ctx := context.Background()
	merchantID, domainID, walletID := seedWithdrawalOwner(t, db)
	ledgerRepo := NewLedgerRepo(db)
	prefix := "recover-sweep-locked-" + uuid.NewString()
	depositTx := ledgerTestTransaction(merchantID, domainID, walletID, prefix+":deposit", "100")
	if err := ledgerRepo.PostStandaloneDepositAvailable(ctx, depositTx); err != nil {
		t.Fatalf("post standalone deposit: %v", err)
	}
	sweepJob := models.SweepJob{
		ID:                    uuid.New(),
		TransactionUniqueHash: depositTx.UniqueHash,
		TransactionHash:       depositTx.Hash,
		WalletID:              walletID,
		MerchantID:            merchantID,
		ChainID:               depositTx.ChainID,
		Token:                 depositTx.Token,
		Status:                models.SweepJobStatusProcessing,
	}
	if err := ledgerRepo.CreateSweepHold(ctx, sweepJob, depositTx); err != nil {
		t.Fatalf("create sweep hold: %v", err)
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
		AmountRaw:  "80",
		Status:     models.WithdrawalStatusPending,
	}
	if err := NewWithdrawalRequestRepo(db).CreateRecoverWithHold(ctx, request, ledgerRepo); err != nil {
		t.Fatalf("create recover withdrawal with hold: %v", err)
	}
	requireLedgerCount(t, db, 2, "sweep_job_id = ? AND entry_type = ? AND idempotency_key = ?", sweepJob.ID, models.LedgerEntryTypeSweepRelease, sweepHoldReleaseKey(sweepJob.ID))
	requireLedgerCount(t, db, 2, "withdrawal_id = ? AND entry_type = ? AND status = ?", request.ID, models.LedgerEntryTypeWithdrawalHold, models.LedgerStatusPending)

	rows, err := ledgerRepo.DomainBalances(ctx, merchantID, domainID)
	if err != nil {
		t.Fatal(err)
	}
	got := ledgerRowsByAccount(rows)
	if got[models.LedgerAccountMerchantAvailable] != "20" || got[models.LedgerAccountSweepTransit] != "0" || got[models.LedgerAccountWithdrawalTransit] != "80" {
		t.Fatalf("recover balances = %#v", got)
	}
}

func TestWithdrawalRequestRepoSumActiveAmountRawByMerchantSince(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.WithdrawalRequest{}); err != nil {
		t.Fatalf("automigrate withdrawal request tables: %v", err)
	}
	ctx := context.Background()
	merchantID, domainID, walletID := seedWithdrawalOwner(t, db)
	otherMerchantID, _, otherWalletID := seedWithdrawalOwner(t, db)
	now := time.Now().UTC()
	rows := []models.WithdrawalRequest{
		{ID: uuid.New(), MerchantID: merchantID, DomainID: &domainID, WalletID: walletID, Chain: "ethereum", Symbol: "ETH", AmountRaw: "10", ToAddress: "0xto", Status: models.WithdrawalStatusPending, CreatedAt: now},
		{ID: uuid.New(), MerchantID: merchantID, DomainID: &domainID, WalletID: walletID, Chain: "Ethereum", Symbol: "ETH", AmountRaw: "15", ToAddress: "0xto", Status: models.WithdrawalStatusProcessing, CreatedAt: now},
		{ID: uuid.New(), MerchantID: merchantID, DomainID: &domainID, WalletID: walletID, Chain: "ethereum", Symbol: "ETH", AmountRaw: "50", ToAddress: "0xto", Status: models.WithdrawalStatusRejected, CreatedAt: now},
		{ID: uuid.New(), MerchantID: merchantID, DomainID: &domainID, WalletID: walletID, Chain: "polygon", Symbol: "POL", AmountRaw: "20", ToAddress: "0xto", Status: models.WithdrawalStatusPending, CreatedAt: now},
		{ID: uuid.New(), MerchantID: merchantID, DomainID: &domainID, WalletID: walletID, Chain: "ethereum", Symbol: "ETH", AmountRaw: "40", ToAddress: "0xto", Status: models.WithdrawalStatusPending, CreatedAt: now.Add(-48 * time.Hour)},
		{ID: uuid.New(), MerchantID: otherMerchantID, WalletID: otherWalletID, Chain: "ethereum", Symbol: "ETH", AmountRaw: "60", ToAddress: "0xto", Status: models.WithdrawalStatusPending, CreatedAt: now},
	}
	if err := db.WithContext(ctx).Create(&rows).Error; err != nil {
		t.Fatalf("seed withdrawals: %v", err)
	}

	total, err := NewWithdrawalRequestRepo(db).SumActiveAmountRawByMerchantSince(ctx, merchantID, " ethereum ", nil, "ETH", now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("sum withdrawals: %v", err)
	}
	if total.String() != "25" {
		t.Fatalf("withdrawal active total = %s, want 25", total.String())
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

func TestWithdrawalRequestRepoApproveTransferFailureReleasesPreBroadcastHold(t *testing.T) {
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
	requireLedgerCount(t, db, 2, "withdrawal_id = ? AND entry_type = ? AND status = ?", request.ID, models.LedgerEntryTypeWithdrawalHold, models.LedgerStatusPending)
	requireLedgerCount(t, db, 0, "withdrawal_id = ? AND entry_type = ? AND status = ?", request.ID, models.LedgerEntryTypeWithdrawalHold, models.LedgerStatusVoided)
	requireLedgerCount(t, db, 2, "withdrawal_id = ? AND entry_type = ? AND idempotency_key = ?", request.ID, models.LedgerEntryTypeWithdrawalRelease, withdrawalReleaseKey(request.ID))
	if err := ledgerRepo.VoidWithdrawalHold(ctx, request.ID); err != nil {
		t.Fatalf("duplicate void withdrawal hold: %v", err)
	}
	requireLedgerCount(t, db, 2, "withdrawal_id = ? AND entry_type = ? AND idempotency_key = ?", request.ID, models.LedgerEntryTypeWithdrawalRelease, withdrawalReleaseKey(request.ID))
}

func TestWithdrawalRequestRepoApproveBroadcastUncertainPreservesProcessingHold(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.WithdrawalRequest{}, &models.LedgerEntry{}); err != nil {
		t.Fatalf("automigrate withdrawal hold tables: %v", err)
	}
	ctx := context.Background()
	merchantID, domainID, walletID := seedWithdrawalOwner(t, db)
	prefix := "withdrawal-broadcast-uncertain-" + uuid.NewString()
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

	transferErr := fmt.Errorf("ethereum tx broadcast failed: context deadline exceeded")
	after, err := repo.ApproveWithTransfer(ctx, request.ID, "admin@example.com", ledgerRepo, func(*models.WithdrawalRequest) (string, error) {
		return "", transferErr
	})
	if !errors.Is(err, transferErr) {
		t.Fatalf("ApproveWithTransfer err = %v, want %v", err, transferErr)
	}
	if after == nil || after.Status != models.WithdrawalStatusProcessing || after.TxHash != "" || !strings.Contains(after.Error, "broadcast outcome uncertain") {
		t.Fatalf("uncertain withdrawal state = %#v", after)
	}
	var stored models.WithdrawalRequest
	if err := db.WithContext(ctx).First(&stored, "id = ?", request.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != models.WithdrawalStatusProcessing || !strings.Contains(stored.Error, "broadcast outcome uncertain") {
		t.Fatalf("stored uncertain withdrawal state = %#v", stored)
	}
	requireLedgerCount(t, db, 2, "withdrawal_id = ? AND entry_type = ? AND status = ?", request.ID, models.LedgerEntryTypeWithdrawalHold, models.LedgerStatusPending)
}

func TestOutboundTransferFailureBroadcastUncertainClassifier(t *testing.T) {
	if !OutboundTransferFailureBroadcastUncertain(errors.New("ethereum tx broadcast failed: already known")) {
		t.Fatal("broadcast failure should be classified as uncertain")
	}
	if OutboundTransferFailureBroadcastUncertain(errors.New("signer unavailable before signing")) {
		t.Fatal("pre-broadcast signer error should not be classified as uncertain")
	}
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

func TestWithdrawalLifecycleTransitionsRecordAtomicOutboxEvents(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.WithdrawalRequest{}, &models.LedgerEntry{}); err != nil {
		t.Fatalf("automigrate payout lifecycle tables: %v", err)
	}
	ctx := context.Background()
	merchantID, domainID, walletID := seedWithdrawalOwner(t, db)
	repo := NewWithdrawalRequestRepo(db)
	newRequest := func(status string) models.WithdrawalRequest {
		return models.WithdrawalRequest{
			ID:             uuid.New(),
			MerchantID:     merchantID,
			DomainID:       &domainID,
			WalletID:       walletID,
			Chain:          "ethereum",
			Symbol:         "ETH",
			Decimals:       18,
			ToAddress:      "0xto",
			AmountRaw:      "10",
			Status:         status,
			IdempotencyKey: "business-" + uuid.NewString(),
			CorrelationID:  "correlation-" + uuid.NewString(),
		}
	}

	request := newRequest(models.WithdrawalStatusPending)
	if err := repo.Create(ctx, &request); err != nil {
		t.Fatalf("create payout: %v", err)
	}
	if err := db.WithContext(ctx).Model(&models.WithdrawalRequest{}).Where("id = ?", request.ID).Update("status", models.WithdrawalStatusProcessing).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordBroadcast(ctx, request.ID, "admin@example.com", "0xbroadcast"); err != nil {
		t.Fatalf("record payout broadcast: %v", err)
	}
	if err := repo.MarkApproved(ctx, request.ID, "admin@example.com", "0xbroadcast"); err != nil {
		t.Fatalf("finalize payout: %v", err)
	}
	if err := repo.MarkApproved(ctx, request.ID, "admin@example.com", "0xbroadcast"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("duplicate finalize error = %v", err)
	}

	rejected := newRequest(models.WithdrawalStatusPending)
	failed := newRequest(models.WithdrawalStatusPending)
	if err := db.WithContext(ctx).Create(&[]models.WithdrawalRequest{rejected, failed}).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkRejected(ctx, rejected.ID, "admin@example.com", "policy rejected"); err != nil {
		t.Fatalf("reject payout: %v", err)
	}
	if err := repo.MarkFailed(ctx, failed.ID, "worker", "signer failed"); err != nil {
		t.Fatalf("fail payout: %v", err)
	}

	var rows []models.MoneyEventOutbox
	if err := db.WithContext(ctx).Where("aggregate_id = ?", request.ID.String()).Order("sequence ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	want := []string{
		constants.WebhookEventPayoutRequestedV1,
		constants.WebhookEventPayoutBroadcastV1,
		constants.WebhookEventPayoutFinalizedV1,
	}
	if len(rows) != len(want) {
		t.Fatalf("payout lifecycle rows = %d, want %d", len(rows), len(want))
	}
	for i := range want {
		if rows[i].EventType != want[i] || rows[i].Sequence != int64(i+1) || rows[i].IdempotencyKey != rows[i].EventID {
			t.Fatalf("payout lifecycle row %d = %#v", i, rows[i])
		}
	}
	var finalizedPayload map[string]any
	if err := json.Unmarshal([]byte(rows[2].PayloadJSON), &finalizedPayload); err != nil {
		t.Fatal(err)
	}
	if finalizedPayload["status"] != models.WithdrawalStatusFinalized || finalizedPayload["tx_hash"] != "0xbroadcast" {
		t.Fatalf("finalized payout payload = %#v", finalizedPayload)
	}
	requirePostgresCount(t, db, &models.MoneyEventOutbox{}, "event_id = ?", rejected.ID.String()+":"+constants.WebhookEventPayoutRejectedV1, 1)
	requirePostgresCount(t, db, &models.MoneyEventOutbox{}, "event_id = ?", failed.ID.String()+":"+constants.WebhookEventPayoutFailedV1, 1)
}

func TestWithdrawalBroadcastRollsBackWhenLifecycleOutboxInsertFails(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.WithdrawalRequest{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	merchantID, domainID, walletID := seedWithdrawalOwner(t, db)
	request := models.WithdrawalRequest{
		ID: uuid.New(), MerchantID: merchantID, DomainID: &domainID, WalletID: walletID,
		Chain: "ethereum", Symbol: "ETH", ToAddress: "0xto", AmountRaw: "10", Status: models.WithdrawalStatusProcessing,
	}
	if err := db.WithContext(ctx).Create(&request).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`ALTER TABLE money_event_outboxes ADD CONSTRAINT reject_payout_broadcast CHECK (event_type <> 'payout.broadcast.v1')`).Error; err != nil {
		t.Fatal(err)
	}

	err := NewWithdrawalRequestRepo(db).RecordBroadcast(ctx, request.ID, "admin@example.com", "0xmust-rollback")
	if err == nil {
		t.Fatal("broadcast transition succeeded despite rejected lifecycle insert")
	}
	var after models.WithdrawalRequest
	if err := db.WithContext(ctx).First(&after, "id = ?", request.ID).Error; err != nil {
		t.Fatal(err)
	}
	if after.TxHash != "" || after.BroadcastedAt != nil || after.ReviewedAt != nil || after.Status != models.WithdrawalStatusProcessing {
		t.Fatalf("payout transition did not roll back: %#v", after)
	}
	requirePostgresCount(t, db, &models.MoneyEventOutbox{}, "aggregate_id = ?", request.ID.String(), 0)
}

func seedWithdrawalOwner(t *testing.T, db *gorm.DB) (uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	hdAccountID := uint32(domainID[0])<<24 | uint32(domainID[1])<<16 | uint32(domainID[2])<<8 | uint32(domainID[3])
	if hdAccountID == 0 {
		hdAccountID = 1
	}
	addressSuffix := strings.ReplaceAll(walletID.String(), "-", "")
	merchant := models.Merchant{ID: merchantID, Name: "Test Merchant", Email: "merchant-" + merchantID.String() + "@example.com", IsActive: true}
	domain := models.Domain{ID: domainID, MerchantID: merchantID, DomainURL: "https://" + domainID.String() + ".example.com", APIKey: "key-" + domainID.String(), APISecret: "secret", HDAccountID: hdAccountID}
	wallet := models.Wallet{
		ID:                 walletID,
		MerchantID:         merchantID,
		DomainID:           domainID,
		HDAccountID:        hdAccountID,
		HDAddressId:        0,
		BitcoinAddress:     "btc-" + addressSuffix,
		EthereumAddress:    "0x" + addressSuffix,
		AvalancheAddress:   "avax-" + addressSuffix,
		BinanceAddress:     "bnb-" + addressSuffix,
		BaseAddress:        "base-" + addressSuffix,
		ArbitrumAddress:    "arb-" + addressSuffix,
		UnichainAddress:    "uni-" + addressSuffix,
		TronAddress:        "tron-" + addressSuffix,
		SolanaAddress:      "sol-" + addressSuffix,
		ChilizAddress:      "chz-" + addressSuffix,
		ChilizSpicyAddress: "spicy-" + addressSuffix,
	}
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
