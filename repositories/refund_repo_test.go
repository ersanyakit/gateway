package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
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

func TestRefundRepoSumActiveAmountRawByMerchantSince(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.Refund{}); err != nil {
		t.Fatalf("automigrate refund tables: %v", err)
	}
	ctx := context.Background()
	merchantID, domainID, _ := seedWithdrawalOwner(t, db)
	otherMerchantID, otherDomainID, _ := seedWithdrawalOwner(t, db)
	now := time.Now().UTC()
	rows := []models.Refund{
		{ID: uuid.New(), MerchantID: merchantID, DomainID: domainID, PaymentID: uuid.New(), Chain: "ethereum", Symbol: "ETH", AmountRaw: "10", Status: models.RefundStatusPending, CreatedAt: now},
		{ID: uuid.New(), MerchantID: merchantID, DomainID: domainID, PaymentID: uuid.New(), Chain: "Ethereum", Symbol: "ETH", AmountRaw: "15", Status: models.RefundStatusProcessing, CreatedAt: now},
		{ID: uuid.New(), MerchantID: merchantID, DomainID: domainID, PaymentID: uuid.New(), Chain: "ethereum", Symbol: "ETH", AmountRaw: "50", Status: models.RefundStatusRejected, CreatedAt: now},
		{ID: uuid.New(), MerchantID: merchantID, DomainID: domainID, PaymentID: uuid.New(), Chain: "polygon", Symbol: "POL", AmountRaw: "20", Status: models.RefundStatusPending, CreatedAt: now},
		{ID: uuid.New(), MerchantID: merchantID, DomainID: domainID, PaymentID: uuid.New(), Chain: "ethereum", Symbol: "ETH", AmountRaw: "40", Status: models.RefundStatusPending, CreatedAt: now.Add(-48 * time.Hour)},
		{ID: uuid.New(), MerchantID: otherMerchantID, DomainID: otherDomainID, PaymentID: uuid.New(), Chain: "ethereum", Symbol: "ETH", AmountRaw: "60", Status: models.RefundStatusPending, CreatedAt: now},
	}
	if err := db.WithContext(ctx).Create(&rows).Error; err != nil {
		t.Fatalf("seed refunds: %v", err)
	}

	total, err := NewRefundRepo(db).SumActiveAmountRawByMerchantSince(ctx, merchantID, " ethereum ", nil, "ETH", now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("sum refunds: %v", err)
	}
	if total.String() != "25" {
		t.Fatalf("refund active total = %s, want 25", total.String())
	}
}

func TestRefundRepoMarkFailedReleasesPreBroadcastHold(t *testing.T) {
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
	requireLedgerCount(t, db, 2, "refund_id = ? AND entry_type = ? AND status = ?", refund.ID, models.LedgerEntryTypeRefundHold, models.LedgerStatusPending)
	requireLedgerCount(t, db, 0, "refund_id = ? AND entry_type = ? AND status = ?", refund.ID, models.LedgerEntryTypeRefundHold, models.LedgerStatusVoided)
	requireLedgerCount(t, db, 2, "refund_id = ? AND entry_type = ? AND idempotency_key = ?", refund.ID, models.LedgerEntryTypeRefundRelease, refundReleaseKey(refund.ID))
	if err := ledgerRepo.VoidRefundHold(ctx, refund.ID); err != nil {
		t.Fatalf("duplicate void refund hold: %v", err)
	}
	requireLedgerCount(t, db, 2, "refund_id = ? AND entry_type = ? AND idempotency_key = ?", refund.ID, models.LedgerEntryTypeRefundRelease, refundReleaseKey(refund.ID))
}

func TestRefundRepoClaimPendingPersistsSourceWalletAndBroadcastMetadata(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.Refund{}, &models.LedgerEntry{}); err != nil {
		t.Fatalf("automigrate refund lifecycle tables: %v", err)
	}
	ctx := context.Background()
	merchantID, domainID, walletID := seedWithdrawalOwner(t, db)
	chainID := constants.Ethereum
	ledgerRepo := NewLedgerRepo(db)
	depositTx := ledgerTestTransaction(merchantID, domainID, walletID, "refund-source-"+uuid.NewString()+":deposit", "100")
	if err := ledgerRepo.PostStandaloneDepositAvailable(ctx, depositTx); err != nil {
		t.Fatalf("post available balance: %v", err)
	}
	session := models.PaymentSession{
		ID:               uuid.New(),
		MerchantID:       merchantID,
		DomainID:         domainID,
		WalletID:         walletID,
		SelectedChainID:  &chainID,
		SelectedToken:    nil,
		SelectedSymbol:   "ETH",
		SelectedDecimals: 18,
	}
	refund := &models.Refund{
		ID:             uuid.New(),
		MerchantID:     merchantID,
		DomainID:       domainID,
		PaymentID:      session.ID,
		AmountRaw:      "25",
		Status:         models.RefundStatusPending,
		IdempotencyKey: "refund-key",
		CorrelationID:  "corr-refund",
	}
	repo := NewRefundRepo(db)
	if err := repo.CreateWithHold(ctx, refund, session, ledgerRepo); err != nil {
		t.Fatalf("create refund with hold: %v", err)
	}
	sourceWallet := models.Wallet{ID: walletID, MerchantID: merchantID, DomainID: domainID}

	claimed, err := repo.ClaimPendingWithHoldAndSource(ctx, refund.ID, "admin@example.com", session, sourceWallet, " 0xoriginal-sender ", ledgerRepo)
	if err != nil {
		t.Fatalf("claim refund: %v", err)
	}
	if claimed.WalletID == nil || *claimed.WalletID != walletID || claimed.Chain != "ethereum" || claimed.Symbol != "ETH" || claimed.ToAddress != "0xoriginal-sender" {
		t.Fatalf("claimed source metadata = %#v", claimed)
	}
	if err := repo.RecordBroadcast(ctx, refund.ID, "admin@example.com", " "); !errors.Is(err, ErrRefundTxHashRequired) {
		t.Fatalf("blank refund tx err = %v, want ErrRefundTxHashRequired", err)
	}
	if err := repo.RecordBroadcast(ctx, refund.ID, "admin@example.com", " 0xrefund "); err != nil {
		t.Fatalf("record refund broadcast: %v", err)
	}
	var after models.Refund
	if err := db.WithContext(ctx).First(&after, "id = ?", refund.ID).Error; err != nil {
		t.Fatal(err)
	}
	if after.Status != models.RefundStatusProcessing || after.TxHash != "0xrefund" || after.BroadcastedAt == nil || after.FinalizedAt != nil {
		t.Fatalf("refund broadcast state = status:%s tx:%q broadcasted:%v finalized:%v", after.Status, after.TxHash, after.BroadcastedAt, after.FinalizedAt)
	}
}

func TestRefundRepoClaimPendingUsesSourceWalletForLedgerHoldAndDebit(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.Refund{}, &models.LedgerEntry{}); err != nil {
		t.Fatalf("automigrate refund source wallet tables: %v", err)
	}
	ctx := context.Background()
	merchantID, domainID, sessionWalletID := seedWithdrawalOwner(t, db)
	sourceWalletID := uuid.New()
	sourceWallet := models.Wallet{
		ID:              sourceWalletID,
		MerchantID:      merchantID,
		DomainID:        domainID,
		ProductID:       "refund-source",
		UserID:          "refund-source",
		HDAccountID:     2,
		HDAddressId:     0,
		EthereumAddress: "0xsource" + sourceWalletID.String(),
	}
	if err := db.WithContext(ctx).Create(&sourceWallet).Error; err != nil {
		t.Fatalf("seed source wallet: %v", err)
	}
	chainID := constants.Ethereum
	ledgerRepo := NewLedgerRepo(db)
	depositTx := ledgerTestTransaction(merchantID, domainID, sourceWalletID, "refund-source-wallet-"+uuid.NewString()+":deposit", "100")
	if err := ledgerRepo.PostStandaloneDepositAvailable(ctx, depositTx); err != nil {
		t.Fatalf("post available balance: %v", err)
	}
	session := models.PaymentSession{
		ID:               uuid.New(),
		MerchantID:       merchantID,
		DomainID:         domainID,
		WalletID:         sessionWalletID,
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
	if _, err := repo.ClaimPendingWithHoldAndSource(ctx, refund.ID, "admin@example.com", session, sourceWallet, "0xoriginal-sender", ledgerRepo); err != nil {
		t.Fatalf("claim refund: %v", err)
	}
	requireLedgerCount(t, db, 2, "refund_id = ? AND entry_type = ? AND status = ? AND wallet_id = ?", refund.ID, models.LedgerEntryTypeRefundHold, models.LedgerStatusPending, sourceWalletID)
	requireLedgerCount(t, db, 2, "refund_id = ? AND entry_type = ? AND status = ? AND wallet_id = ?", refund.ID, models.LedgerEntryTypeRefundHold, models.LedgerStatusPending, sessionWalletID)
	requireLedgerCount(t, db, 2, "refund_id = ? AND entry_type = ? AND wallet_id = ?", refund.ID, models.LedgerEntryTypeRefundRelease, sessionWalletID)
	if err := repo.RecordBroadcast(ctx, refund.ID, "admin@example.com", "0xrefund"); err != nil {
		t.Fatalf("record refund broadcast: %v", err)
	}
	if err := repo.MarkSucceededWithLedger(ctx, refund.ID, "admin@example.com", "0xrefund", session, ledgerRepo); err != nil {
		t.Fatalf("mark refund succeeded: %v", err)
	}
	requireLedgerCount(t, db, 2, "refund_id = ? AND entry_type = ? AND status = ? AND wallet_id = ?", refund.ID, models.LedgerEntryTypeRefundDebit, models.LedgerStatusPosted, sourceWalletID)
	requireLedgerCount(t, db, 0, "refund_id = ? AND entry_type = ? AND status = ? AND wallet_id = ?", refund.ID, models.LedgerEntryTypeRefundDebit, models.LedgerStatusPosted, sessionWalletID)
}

func TestRefundRepoFinalizationSetsTerminalTimestampAndIsIdempotent(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.Refund{}, &models.LedgerEntry{}); err != nil {
		t.Fatalf("automigrate refund finalization tables: %v", err)
	}
	ctx := context.Background()
	merchantID, domainID, walletID := seedWithdrawalOwner(t, db)
	chainID := constants.Ethereum
	ledgerRepo := NewLedgerRepo(db)
	depositTx := ledgerTestTransaction(merchantID, domainID, walletID, "refund-finalize-"+uuid.NewString()+":deposit", "100")
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
	if _, err := repo.ClaimPendingWithHoldAndSource(ctx, refund.ID, "admin@example.com", session, models.Wallet{ID: walletID, MerchantID: merchantID, DomainID: domainID}, "0xto", ledgerRepo); err != nil {
		t.Fatalf("claim refund: %v", err)
	}
	if err := repo.RecordBroadcast(ctx, refund.ID, "admin@example.com", "0xrefund"); err != nil {
		t.Fatalf("record refund broadcast: %v", err)
	}

	if err := repo.MarkSucceededWithLedger(ctx, refund.ID, "admin@example.com", "0xrefund", session, ledgerRepo); err != nil {
		t.Fatalf("mark refund succeeded: %v", err)
	}
	var succeeded models.Refund
	if err := db.WithContext(ctx).First(&succeeded, "id = ?", refund.ID).Error; err != nil {
		t.Fatal(err)
	}
	if succeeded.Status != models.RefundStatusSucceeded || succeeded.FinalizedAt == nil || succeeded.TxHash != "0xrefund" {
		t.Fatalf("succeeded state = status:%s tx:%q finalized:%v", succeeded.Status, succeeded.TxHash, succeeded.FinalizedAt)
	}
	requireLedgerCount(t, db, 2, "refund_id = ? AND entry_type = ?", refund.ID, models.LedgerEntryTypeRefundDebit)
	if err := repo.MarkSucceededWithLedger(ctx, refund.ID, "admin@example.com", "0xrefund", session, ledgerRepo); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("duplicate refund finalization err = %v, want gorm.ErrRecordNotFound", err)
	}
	requireLedgerCount(t, db, 2, "refund_id = ? AND entry_type = ?", refund.ID, models.LedgerEntryTypeRefundDebit)
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

func TestRefundRepoRecordBroadcastRequiresTxHashAndStoresTimestamp(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Refund{}); err != nil {
		t.Fatalf("automigrate refund tables: %v", err)
	}
	ctx := context.Background()
	refund := models.Refund{
		ID:         uuid.New(),
		MerchantID: uuid.New(),
		DomainID:   uuid.New(),
		PaymentID:  uuid.New(),
		AmountRaw:  "10",
		Status:     models.RefundStatusProcessing,
	}
	if err := db.WithContext(ctx).Create(&refund).Error; err != nil {
		t.Fatalf("seed refund: %v", err)
	}
	repo := NewRefundRepo(db)
	if err := repo.RecordBroadcast(ctx, refund.ID, "admin@example.com", ""); !errors.Is(err, ErrTxHashRequired) {
		t.Fatalf("blank tx hash err = %v, want ErrTxHashRequired", err)
	}
	if err := repo.RecordBroadcast(ctx, refund.ID, "admin@example.com", "0xrefund"); err != nil {
		t.Fatalf("record broadcast: %v", err)
	}
	var after models.Refund
	if err := db.WithContext(ctx).First(&after, "id = ?", refund.ID).Error; err != nil {
		t.Fatal(err)
	}
	if after.TxHash != "0xrefund" || after.BroadcastedAt == nil {
		t.Fatalf("broadcast metadata not persisted: tx=%q broadcasted_at=%v", after.TxHash, after.BroadcastedAt)
	}
	if after.Status != models.RefundStatusProcessing {
		t.Fatalf("broadcast must remain non-terminal processing, got %s", after.Status)
	}
}

func TestRefundLifecycleTransitionsRecordAtomicOutboxEvents(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Refund{}, &models.LedgerEntry{}); err != nil {
		t.Fatalf("automigrate refund lifecycle tables: %v", err)
	}
	ctx := context.Background()
	merchantID := uuid.New()
	domainID := uuid.New()
	repo := NewRefundRepo(db)
	newRefund := func(status string) models.Refund {
		return models.Refund{
			ID:             uuid.New(),
			MerchantID:     merchantID,
			DomainID:       domainID,
			PaymentID:      uuid.New(),
			AmountRaw:      "10",
			Status:         status,
			IdempotencyKey: "business-" + uuid.NewString(),
			CorrelationID:  "correlation-" + uuid.NewString(),
		}
	}

	refund := newRefund(models.RefundStatusPending)
	if err := repo.Create(ctx, &refund); err != nil {
		t.Fatalf("create refund: %v", err)
	}
	if err := db.WithContext(ctx).Model(&models.Refund{}).Where("id = ?", refund.ID).Update("status", models.RefundStatusProcessing).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordBroadcast(ctx, refund.ID, "admin@example.com", "0xrefund"); err != nil {
		t.Fatalf("record refund broadcast: %v", err)
	}
	if err := repo.MarkSucceeded(ctx, refund.ID, "admin@example.com", "0xrefund"); err != nil {
		t.Fatalf("succeed refund: %v", err)
	}
	if err := repo.MarkSucceeded(ctx, refund.ID, "admin@example.com", "0xrefund"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("duplicate success error = %v", err)
	}

	rejected := newRefund(models.RefundStatusPending)
	failed := newRefund(models.RefundStatusPending)
	if err := db.WithContext(ctx).Create(&[]models.Refund{rejected, failed}).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkRejected(ctx, rejected.ID, "admin@example.com", "policy rejected"); err != nil {
		t.Fatalf("reject refund: %v", err)
	}
	if err := repo.MarkFailed(ctx, failed.ID, "worker", "signer failed"); err != nil {
		t.Fatalf("fail refund: %v", err)
	}

	var rows []models.MoneyEventOutbox
	if err := db.WithContext(ctx).Where("aggregate_id = ?", refund.ID.String()).Order("sequence ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	want := []string{
		constants.WebhookEventRefundRequestedV1,
		constants.WebhookEventRefundBroadcastV1,
		constants.WebhookEventRefundSucceededV1,
	}
	if len(rows) != len(want) {
		t.Fatalf("refund lifecycle rows = %d, want %d", len(rows), len(want))
	}
	for i := range want {
		if rows[i].EventType != want[i] || rows[i].Sequence != int64(i+1) || rows[i].IdempotencyKey != rows[i].EventID {
			t.Fatalf("refund lifecycle row %d = %#v", i, rows[i])
		}
	}
	var succeededPayload map[string]any
	if err := json.Unmarshal([]byte(rows[2].PayloadJSON), &succeededPayload); err != nil {
		t.Fatal(err)
	}
	if succeededPayload["status"] != models.RefundStatusSucceeded || succeededPayload["tx_hash"] != "0xrefund" {
		t.Fatalf("succeeded refund payload = %#v", succeededPayload)
	}
	requirePostgresCount(t, db, &models.MoneyEventOutbox{}, "event_id = ?", rejected.ID.String()+":"+constants.WebhookEventRefundRejectedV1, 1)
	requirePostgresCount(t, db, &models.MoneyEventOutbox{}, "event_id = ?", failed.ID.String()+":"+constants.WebhookEventRefundFailedV1, 1)
}

func TestRefundSuccessRollsBackWhenLifecycleOutboxInsertFails(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Refund{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	refund := models.Refund{
		ID: uuid.New(), MerchantID: uuid.New(), DomainID: uuid.New(), PaymentID: uuid.New(),
		AmountRaw: "10", Status: models.RefundStatusProcessing, TxHash: "0xrefund",
	}
	if err := db.WithContext(ctx).Create(&refund).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`ALTER TABLE money_event_outboxes ADD CONSTRAINT reject_refund_success CHECK (event_type <> 'refund.succeeded.v1')`).Error; err != nil {
		t.Fatal(err)
	}

	err := NewRefundRepo(db).MarkSucceeded(ctx, refund.ID, "admin@example.com", "0xrefund")
	if err == nil {
		t.Fatal("refund success transition succeeded despite rejected lifecycle insert")
	}
	var after models.Refund
	if err := db.WithContext(ctx).First(&after, "id = ?", refund.ID).Error; err != nil {
		t.Fatal(err)
	}
	if after.Status != models.RefundStatusProcessing || after.FinalizedAt != nil || after.ReviewedAt != nil {
		t.Fatalf("refund transition did not roll back: %#v", after)
	}
	requirePostgresCount(t, db, &models.MoneyEventOutbox{}, "aggregate_id = ?", refund.ID.String(), 0)
}
