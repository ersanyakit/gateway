package repositories

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"core/constants"
	"core/helpers"
	"core/models"
	"core/types"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestTransactionUniqueHashIncludesChainHashAndLogIndex(t *testing.T) {
	repo := NewTransactionRepo(nil)
	unique, err := repo.UniqueHash(types.TransactionParam{
		ChainID:  constants.Ethereum,
		Hash:     helpers.StrPtr("0xabc"),
		LogIndex: helpers.StrPtr("log:1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if unique != "1-0xabc-log:1" {
		t.Fatalf("unique hash = %q", unique)
	}
}

func TestTransactionUniqueHashNormalizesHashAndLogIndex(t *testing.T) {
	repo := NewTransactionRepo(nil)
	unique, err := repo.UniqueHash(types.TransactionParam{
		ChainID:  constants.Ethereum,
		Hash:     helpers.StrPtr("  0xABCDEF  "),
		LogIndex: helpers.StrPtr(" LOG:0x0a "),
	})
	if err != nil {
		t.Fatal(err)
	}
	if unique != "1-0xabcdef-log:10" {
		t.Fatalf("unique hash = %q", unique)
	}
}

func TestTransactionUniqueHashNormalizesNilAndEmptyLogIndex(t *testing.T) {
	repo := NewTransactionRepo(nil)
	nilLogIndex, err := repo.UniqueHash(types.TransactionParam{
		ChainID: constants.Bitcoin,
		Hash:    helpers.StrPtr("btc-hash"),
	})
	if err != nil {
		t.Fatal(err)
	}
	emptyLogIndex, err := repo.UniqueHash(types.TransactionParam{
		ChainID:  constants.Bitcoin,
		Hash:     helpers.StrPtr("btc-hash"),
		LogIndex: helpers.StrPtr("   "),
	})
	if err != nil {
		t.Fatal(err)
	}
	if nilLogIndex != "0-btc-hash-" {
		t.Fatalf("unique hash = %q", nilLogIndex)
	}
	if emptyLogIndex != nilLogIndex {
		t.Fatalf("empty logIndex unique hash = %q, want %q", emptyLogIndex, nilLogIndex)
	}
}

func TestTransactionUniqueHashRequiresHash(t *testing.T) {
	repo := NewTransactionRepo(nil)
	if _, err := repo.UniqueHash(types.TransactionParam{ChainID: constants.Ethereum}); err == nil {
		t.Fatal("missing hash should fail")
	}
	if _, err := repo.UniqueHash(types.TransactionParam{ChainID: constants.Ethereum, Hash: helpers.StrPtr("   ")}); err == nil {
		t.Fatal("blank hash should fail")
	}
}

func TestTransactionRepoFindFinalizedByHashRequiresFinality(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Transaction{}); err != nil {
		t.Fatalf("automigrate transactions: %v", err)
	}
	ctx := context.Background()
	now := time.Now()
	pending := models.Transaction{
		ID:                    uuid.New(),
		ChainID:               constants.Ethereum,
		UniqueHash:            "1-0xoutbound-pending-",
		Hash:                  "0xoutbound",
		BlockNumber:           "100",
		BlockHash:             "0xblock",
		Symbol:                "ETH",
		FromAddress:           "0xfrom",
		ToAddress:             "0xto",
		Amount:                "10",
		Status:                models.TransactionStatusPendingConfirmation,
		Confirmations:         1,
		ConfirmationsRequired: 12,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	finalizedAt := now.Add(time.Minute)
	finalized := pending
	finalized.ID = uuid.New()
	finalized.UniqueHash = "1-0xoutbound-final-"
	finalized.Hash = "0xfinalized"
	finalized.Status = models.TransactionStatusConfirmed
	finalized.Confirmations = 12
	finalized.FinalizedAt = &finalizedAt
	if err := db.WithContext(ctx).Create(&[]models.Transaction{pending, finalized}).Error; err != nil {
		t.Fatalf("seed transactions: %v", err)
	}
	repo := NewTransactionRepo(db)
	if _, err := repo.FindFinalizedByHash(ctx, constants.Ethereum, "0xoutbound"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("pending hash lookup err = %v, want gorm.ErrRecordNotFound", err)
	}
	got, err := repo.FindFinalizedByHash(ctx, constants.Ethereum, " 0xFINALIZED ")
	if err != nil {
		t.Fatalf("find finalized by hash: %v", err)
	}
	if got.UniqueHash != finalized.UniqueHash {
		t.Fatalf("finalized lookup = %s, want %s", got.UniqueHash, finalized.UniqueHash)
	}
}

func TestTransactionInitialStatusDefersConfirmedUntilFinality(t *testing.T) {
	confirmed := models.TransactionStatusConfirmed
	if got := transactionInitialStatus(&confirmed); got != models.TransactionStatusPendingConfirmation {
		t.Fatalf("initial confirmed status = %q, want %q", got, models.TransactionStatusPendingConfirmation)
	}
}

func TestTransactionInitialStatusKeepsTerminalStatuses(t *testing.T) {
	failed := models.TransactionStatusFailed
	if got := transactionInitialStatus(&failed); got != models.TransactionStatusFailed {
		t.Fatalf("initial failed status = %q, want failed", got)
	}
	reorged := models.TransactionStatusReorged
	if got := transactionInitialStatus(&reorged); got != models.TransactionStatusReorged {
		t.Fatalf("initial reorged status = %q, want reorged", got)
	}
}

func TestTransactionBlockIdentityChanged(t *testing.T) {
	tx := models.Transaction{BlockNumber: "100", BlockHash: "0xABC"}
	if transactionBlockIdentityChanged(tx, "100", "0xabc") {
		t.Fatal("same block number and hash should not be identity change")
	}
	if !transactionBlockIdentityChanged(tx, "101", "0xABC") {
		t.Fatal("different block number should be identity change")
	}
	if !transactionBlockIdentityChanged(tx, "100", "0xDEF") {
		t.Fatal("different non-empty block hash should be identity change")
	}
}

func TestTransactionBlockIdentityIgnoresMissingHash(t *testing.T) {
	tx := models.Transaction{BlockNumber: "100", BlockHash: ""}
	if transactionBlockIdentityChanged(tx, "100", "0xabc") {
		t.Fatal("missing stored block hash should not force identity change")
	}
}

func TestTransactionReorgReasonIsBounded(t *testing.T) {
	reason := transactionReorgReason("tx_block_identity_changed_with_a_long_prefix", constants.Ethereum, strings.Repeat("9", 200))
	if len(reason) > 120 {
		t.Fatalf("reason length = %d, want <= 120", len(reason))
	}
}

func TestTransactionRepoReorgsFinalizedTransactionWhenBlockIdentityChanges(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(
		&models.Block{},
		&models.Transaction{},
		&models.ChainFact{},
		&models.Deposit{},
		&models.LedgerEntry{},
		&models.PaymentSession{},
		&models.SweepJob{},
		&models.ReconciliationJob{},
	); err != nil {
		t.Fatalf("automigrate reorg identity models: %v", err)
	}

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	oldTx := transactionReorgTestTx(merchantID, domainID, walletID, "1-0xidentity-0", "0xidentity", "0xwallet", now)
	if err := db.WithContext(ctx).Create(&oldTx).Error; err != nil {
		t.Fatalf("seed finalized transaction: %v", err)
	}

	reappeared := types.TransactionParam{
		Context:   ctx,
		ChainID:   constants.Ethereum,
		Hash:      helpers.StrPtr(oldTx.Hash),
		Block:     helpers.StrPtr("101"),
		BlockHash: helpers.StrPtr("0xnew-identity-block"),
		From:      helpers.StrPtr(oldTx.FromAddress),
		To:        helpers.StrPtr(oldTx.ToAddress),
		Symbol:    helpers.StrPtr(oldTx.Symbol),
		Decimals:  oldTx.Decimals,
		Amount:    helpers.StrPtr(oldTx.Amount),
		LogIndex:  helpers.StrPtr("0"),
	}
	if err := NewTransactionRepo(db).Create(reappeared); err != nil {
		t.Fatalf("create reappeared transaction: %v", err)
	}

	var corrected models.Transaction
	if err := db.WithContext(ctx).First(&corrected, "id = ?", oldTx.ID).Error; err != nil {
		t.Fatalf("load corrected transaction: %v", err)
	}
	if corrected.Status != models.TransactionStatusReorged || corrected.EventType != constants.WebhookEventTransactionReorged {
		t.Fatalf("corrected transaction status/event = %q/%q", corrected.Status, corrected.EventType)
	}
	if corrected.ReorgedAt == nil || !strings.HasPrefix(corrected.CorrectionReason, "tx_block_identity_changed:") {
		t.Fatalf("correction metadata = %#v", corrected)
	}
	requireTransactionRepoCount(t, db, &models.ReconciliationJob{}, "chain_id = ? AND from_block = ? AND to_block = ?", []any{constants.Ethereum, int64(100), int64(100)}, 1)
}

func TestTransactionRepoReorgCorrectionIsAtomicAndIdempotent(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(
		&models.Merchant{},
		&models.Domain{},
		&models.Wallet{},
		&models.Block{},
		&models.Transaction{},
		&models.ChainFact{},
		&models.Deposit{},
		&models.LedgerEntry{},
		&models.PaymentSession{},
		&models.SweepJob{},
		&models.ReconciliationJob{},
	); err != nil {
		t.Fatalf("automigrate reorg correction models: %v", err)
	}

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	merchant := models.Merchant{
		ID:        merchantID,
		Name:      "Reorg Correction Merchant",
		Email:     "reorg-" + uuid.NewString() + "@example.test",
		CreatedAt: now,
		UpdatedAt: now,
	}
	domain := models.Domain{
		ID:            domainID,
		MerchantID:    merchantID,
		DomainURL:     "reorg-correction.example.test",
		APIKey:        "pk_" + uuid.NewString(),
		APISecret:     "secret",
		HDAccountID:   9301,
		WebhookURL:    "https://merchant.example.test/webhooks",
		WebhookSecret: "whsec",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	wallet := models.Wallet{
		ID:              walletID,
		HDAccountID:     9301,
		HDAddressId:     1,
		MerchantID:      merchantID,
		DomainID:        domainID,
		ProductID:       "checkout:reorg",
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

	oldOne := transactionReorgTestTx(merchantID, domainID, walletID, "1-0xold-one-0", "0xold-one", wallet.EthereumAddress, now)
	oldTwo := transactionReorgTestTx(merchantID, domainID, walletID, "1-0xold-two-0", "0xold-two", wallet.EthereumAddress, now)
	if err := db.WithContext(ctx).Create(&[]models.Transaction{oldOne, oldTwo}).Error; err != nil {
		t.Fatalf("seed old transactions: %v", err)
	}
	oldBlock := models.Block{
		ID:         uuid.New(),
		ChainID:    constants.Ethereum,
		Number:     100,
		Hash:       "0xold-block",
		ParentHash: "0xparent",
		Processed:  true,
		Canonical:  true,
		Status:     models.BlockStatusCanonical,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := db.WithContext(ctx).Create(&oldBlock).Error; err != nil {
		t.Fatalf("seed old canonical block: %v", err)
	}

	fact := transactionReorgTestChainFact(oldOne, wallet.EthereumAddress, now)
	deposit := transactionReorgTestDeposit(fact, oldOne.UniqueHash, merchantID, domainID, walletID, wallet.ProductID, wallet.UserID, now)
	if err := db.WithContext(ctx).Create(&fact).Error; err != nil {
		t.Fatalf("seed chain fact: %v", err)
	}
	if err := db.WithContext(ctx).Create(&deposit).Error; err != nil {
		t.Fatalf("seed deposit: %v", err)
	}

	originalCredit := testLedgerEntryWithType("reorg-test:credit", merchantID, &domainID, &walletID, models.LedgerEntryTypeDepositAvailable, models.LedgerAccountMerchantAvailable, models.LedgerDirectionCredit, models.LedgerStatusPosted, "100")
	originalDebit := testLedgerEntryWithType("reorg-test:debit", merchantID, &domainID, &walletID, models.LedgerEntryTypeDepositAvailable, models.LedgerAccountPlatformClearing, models.LedgerDirectionDebit, models.LedgerStatusPosted, "100")
	originalCredit.TransactionUniqueHash = oldOne.UniqueHash
	originalCredit.TransactionHash = oldOne.Hash
	originalDebit.TransactionUniqueHash = oldOne.UniqueHash
	originalDebit.TransactionHash = oldOne.Hash
	if err := db.WithContext(ctx).Create(&[]models.LedgerEntry{originalCredit, originalDebit}).Error; err != nil {
		t.Fatalf("seed ledger originals: %v", err)
	}

	txUniqueHash := oldOne.UniqueHash
	txHash := oldOne.Hash
	webhookSentAt := now.Add(-time.Minute)
	session := models.PaymentSession{
		ID:                   uuid.New(),
		SessionToken:         "reorg-session-" + uuid.NewString(),
		MerchantID:           merchantID,
		DomainID:             domainID,
		WalletID:             walletID,
		OrderID:              "order-" + uuid.NewString(),
		ProductID:            wallet.ProductID,
		UserID:               wallet.UserID,
		Amount:               "100.00",
		Currency:             "USD",
		Status:               models.PaymentStatusOverpaid,
		PaymentOutcome:       models.PaymentOutcomeOverpaid,
		PaymentOutcomeReason: "amount exceeds expected",
		MatchedAmountRaw:     "100",
		ExcessAmountRaw:      "5",
		TxUniqueHash:         &txUniqueHash,
		TxHash:               &txHash,
		PaidAt:               &webhookSentAt,
		ConfirmedAt:          &webhookSentAt,
		WebhookEvent:         constants.WebhookEventPaymentOverpaid,
		WebhookSentAt:        &webhookSentAt,
		WebhookAttempts:      2,
		WebhookLastError:     "previous delivery failed",
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if err := db.WithContext(ctx).Create(&session).Error; err != nil {
		t.Fatalf("seed payment session: %v", err)
	}

	nextRunAt := now.Add(-time.Minute)
	lockedUntil := now.Add(time.Minute)
	deadLetterCandidate := models.SweepJob{
		ID:                    uuid.New(),
		TransactionUniqueHash: oldOne.UniqueHash,
		TransactionHash:       oldOne.Hash,
		WalletID:              walletID,
		MerchantID:            merchantID,
		ChainID:               constants.Ethereum,
		Status:                models.SweepJobStatusFailed,
		Attempts:              3,
		LastError:             "temporary rpc error",
		NextRunAt:             &nextRunAt,
		LockedUntil:           &lockedUntil,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	succeededSweep := models.SweepJob{
		ID:                    uuid.New(),
		TransactionUniqueHash: oldTwo.UniqueHash,
		TransactionHash:       oldTwo.Hash,
		WalletID:              walletID,
		MerchantID:            merchantID,
		ChainID:               constants.Ethereum,
		Status:                models.SweepJobStatusSucceeded,
		SweepTxHash:           "0xsweep",
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	if err := db.WithContext(ctx).Create(&[]models.SweepJob{deadLetterCandidate, succeededSweep}).Error; err != nil {
		t.Fatalf("seed sweep jobs: %v", err)
	}

	repo := NewTransactionRepo(db)
	reorgTrigger := types.TransactionParam{
		Context:    ctx,
		ChainID:    constants.Ethereum,
		Hash:       helpers.StrPtr("0xnew-canonical"),
		Block:      helpers.StrPtr("100"),
		BlockHash:  helpers.StrPtr("0xnew-block"),
		ParentHash: helpers.StrPtr("0xparent"),
		From:       helpers.StrPtr("0xsender"),
		To:         helpers.StrPtr(wallet.EthereumAddress),
		Symbol:     helpers.StrPtr("ETH"),
		Decimals:   18,
		Amount:     helpers.StrPtr("100"),
		LogIndex:   helpers.StrPtr("0"),
	}
	if err := repo.Create(reorgTrigger); err != nil {
		t.Fatalf("create canonical replacement transaction: %v", err)
	}
	if err := repo.Create(reorgTrigger); err != nil {
		t.Fatalf("duplicate canonical replacement transaction: %v", err)
	}

	var correctedTx models.Transaction
	if err := db.WithContext(ctx).First(&correctedTx, "id = ?", oldOne.ID).Error; err != nil {
		t.Fatalf("load corrected transaction: %v", err)
	}
	if correctedTx.Status != models.TransactionStatusReorged || correctedTx.EventType != constants.WebhookEventTransactionReorged {
		t.Fatalf("corrected transaction status/event = %q/%q", correctedTx.Status, correctedTx.EventType)
	}
	if correctedTx.OriginalEventID != oldOne.UniqueHash+":"+constants.WebhookEventNativeTransfer || correctedTx.OriginalResourceID != oldOne.ID.String() || correctedTx.CorrectionReason == "" {
		t.Fatalf("correction reference fields = %#v", correctedTx)
	}
	if correctedTx.WebhookSentAt != nil || correctedTx.WebhookAttempts != 0 || correctedTx.WebhookLastError != "" || correctedTx.WebhookLockedUntil != nil {
		t.Fatalf("transaction webhook retry fields not reset: %#v", correctedTx)
	}

	var correctedFact models.ChainFact
	if err := db.WithContext(ctx).First(&correctedFact, "event_id = ?", fact.EventID).Error; err != nil {
		t.Fatalf("load corrected chain fact: %v", err)
	}
	if correctedFact.Status != models.ChainFactStatusReorged || correctedFact.ReorgedAt == nil || correctedFact.CorrectionReason == "" {
		t.Fatalf("chain fact correction state = %#v", correctedFact)
	}

	var correctedDeposit models.Deposit
	if err := db.WithContext(ctx).First(&correctedDeposit, "id = ?", deposit.ID).Error; err != nil {
		t.Fatalf("load corrected deposit: %v", err)
	}
	if correctedDeposit.Status != models.DepositStatusReorged || correctedDeposit.ReorgedAt == nil || correctedDeposit.CorrectionReason == "" {
		t.Fatalf("deposit correction state = %#v", correctedDeposit)
	}

	requireLedgerCount(t, db, 2, "entry_type = ? AND reference = ?", models.LedgerEntryTypeReorgReversal, oldOne.UniqueHash)
	requireLedgerCount(t, db, 2, "transaction_unique_hash = ? AND status = ? AND entry_type = ?", oldOne.UniqueHash, models.LedgerStatusPosted, models.LedgerEntryTypeDepositAvailable)

	var correctedSession models.PaymentSession
	if err := db.WithContext(ctx).First(&correctedSession, "id = ?", session.ID).Error; err != nil {
		t.Fatalf("load corrected payment session: %v", err)
	}
	if correctedSession.Status != models.PaymentStatusFailed || correctedSession.WebhookEvent != constants.WebhookEventPaymentFailed || correctedSession.PaidAt != nil || correctedSession.ConfirmedAt != nil {
		t.Fatalf("payment correction state = %#v", correctedSession)
	}
	if correctedSession.WebhookSentAt != nil || correctedSession.WebhookAttempts != 0 || correctedSession.WebhookLastError != "" {
		t.Fatalf("payment webhook retry fields not reset: %#v", correctedSession)
	}

	var blockedSweep models.SweepJob
	if err := db.WithContext(ctx).First(&blockedSweep, "id = ?", deadLetterCandidate.ID).Error; err != nil {
		t.Fatalf("load blocked sweep: %v", err)
	}
	if blockedSweep.Status != models.SweepJobStatusDeadLetter || blockedSweep.NextRunAt != nil || blockedSweep.LockedUntil != nil {
		t.Fatalf("blocked sweep state = %#v", blockedSweep)
	}
	var alreadySucceeded models.SweepJob
	if err := db.WithContext(ctx).First(&alreadySucceeded, "id = ?", succeededSweep.ID).Error; err != nil {
		t.Fatalf("load succeeded sweep: %v", err)
	}
	if alreadySucceeded.Status != models.SweepJobStatusSucceeded || alreadySucceeded.SweepTxHash != "0xsweep" {
		t.Fatalf("succeeded sweep should route to reconciliation without blind retry: %#v", alreadySucceeded)
	}

	reason := transactionReorgReason("reorg_detected", constants.Ethereum, "100")
	requireTransactionRepoCount(t, db, &models.ReconciliationJob{}, "chain_id = ? AND from_block = ? AND to_block = ? AND reason = ?", []any{constants.Ethereum, int64(100), int64(100), reason}, 1)
	requireLedgerCount(t, db, 2, "entry_type = ? AND reference = ?", models.LedgerEntryTypeReorgReversal, oldOne.UniqueHash)

	var correctedBlock models.Block
	if err := db.WithContext(ctx).First(&correctedBlock, "hash = ?", oldBlock.Hash).Error; err != nil {
		t.Fatalf("load corrected old block: %v", err)
	}
	if correctedBlock.Canonical || correctedBlock.Status != models.BlockStatusReorged || correctedBlock.ReorgedAt == nil || correctedBlock.SupersededByHash != "0xnew-block" {
		t.Fatalf("old block correction state = %#v", correctedBlock)
	}
	var canonicalBlock models.Block
	if err := db.WithContext(ctx).First(&canonicalBlock, "hash = ?", "0xnew-block").Error; err != nil {
		t.Fatalf("load new canonical block: %v", err)
	}
	if !canonicalBlock.Canonical || canonicalBlock.Status != models.BlockStatusCanonical || canonicalBlock.ReorgedAt != nil || canonicalBlock.Number != 100 {
		t.Fatalf("new canonical block state = %#v", canonicalBlock)
	}

	pending, err := repo.ListPendingWebhooks(ctx, 10)
	if err != nil {
		t.Fatalf("list pending webhook corrections: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("pending reorg webhooks = %d, want 2 rows=%#v", len(pending), pending)
	}
	for _, row := range pending {
		if row.Status != models.TransactionStatusReorged || row.EventType != constants.WebhookEventTransactionReorged || row.OriginalEventID == "" || row.CorrectionReason == "" {
			t.Fatalf("pending correction webhook row = %#v", row)
		}
	}
	locked, err := repo.ListPendingWebhooks(ctx, 10)
	if err != nil {
		t.Fatalf("list locked webhook corrections: %v", err)
	}
	if len(locked) != 0 {
		t.Fatalf("locked correction webhooks returned again: %#v", locked)
	}
}

func TestTransactionRepoParentHashMismatchReorgsCanonicalBlockRange(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(
		&models.Block{},
		&models.Transaction{},
		&models.ChainFact{},
		&models.Deposit{},
		&models.LedgerEntry{},
		&models.PaymentSession{},
		&models.SweepJob{},
		&models.ReconciliationJob{},
	); err != nil {
		t.Fatalf("automigrate parent reorg models: %v", err)
	}

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	parentBlock := models.Block{
		ID:         uuid.New(),
		ChainID:    constants.Ethereum,
		Number:     100,
		Hash:       "0xold-parent",
		ParentHash: "0xgrandparent",
		Processed:  true,
		Canonical:  true,
		Status:     models.BlockStatusCanonical,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	childBlock := models.Block{
		ID:         uuid.New(),
		ChainID:    constants.Ethereum,
		Number:     101,
		Hash:       "0xold-child",
		ParentHash: parentBlock.Hash,
		Processed:  true,
		Canonical:  true,
		Status:     models.BlockStatusCanonical,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := db.WithContext(ctx).Create(&[]models.Block{parentBlock, childBlock}).Error; err != nil {
		t.Fatalf("seed canonical block range: %v", err)
	}

	parentTx := transactionReorgTestTx(merchantID, domainID, walletID, "1-0xold-parent-tx-0", "0xold-parent-tx", "0xwallet", now)
	parentTx.BlockHash = parentBlock.Hash
	childTx := transactionReorgTestTx(merchantID, domainID, walletID, "1-0xold-child-tx-0", "0xold-child-tx", "0xwallet", now)
	childTx.BlockNumber = "101"
	childTx.BlockHash = childBlock.Hash
	if err := db.WithContext(ctx).Create(&[]models.Transaction{parentTx, childTx}).Error; err != nil {
		t.Fatalf("seed canonical range transactions: %v", err)
	}

	repo := NewTransactionRepo(db)
	trigger := types.TransactionParam{
		Context:    ctx,
		ChainID:    constants.Ethereum,
		Hash:       helpers.StrPtr("0xnew-child-tx"),
		Block:      helpers.StrPtr("101"),
		BlockHash:  helpers.StrPtr("0xnew-child"),
		ParentHash: helpers.StrPtr("0xnew-parent"),
		From:       helpers.StrPtr("0xsender"),
		To:         helpers.StrPtr("0xwallet"),
		Symbol:     helpers.StrPtr("ETH"),
		Decimals:   18,
		Amount:     helpers.StrPtr("100"),
		LogIndex:   helpers.StrPtr("0"),
	}
	if err := repo.Create(trigger); err != nil {
		t.Fatalf("create transaction on mismatched parent: %v", err)
	}
	if err := repo.Create(trigger); err != nil {
		t.Fatalf("duplicate mismatched parent transaction: %v", err)
	}

	for _, oldTx := range []models.Transaction{parentTx, childTx} {
		var corrected models.Transaction
		if err := db.WithContext(ctx).First(&corrected, "id = ?", oldTx.ID).Error; err != nil {
			t.Fatalf("load corrected tx %s: %v", oldTx.UniqueHash, err)
		}
		if corrected.Status != models.TransactionStatusReorged || corrected.EventType != constants.WebhookEventTransactionReorged || corrected.ReorgedAt == nil {
			t.Fatalf("corrected parent-range tx = %#v", corrected)
		}
		if !strings.HasPrefix(corrected.CorrectionReason, "parent_mismatch:") {
			t.Fatalf("correction reason = %q, want parent_mismatch prefix", corrected.CorrectionReason)
		}
	}

	for _, oldBlock := range []models.Block{parentBlock, childBlock} {
		var correctedBlock models.Block
		if err := db.WithContext(ctx).First(&correctedBlock, "id = ?", oldBlock.ID).Error; err != nil {
			t.Fatalf("load corrected block %s: %v", oldBlock.Hash, err)
		}
		if correctedBlock.Canonical || correctedBlock.Status != models.BlockStatusReorged || correctedBlock.ReorgedAt == nil || correctedBlock.SupersededByHash != "0xnew-parent" {
			t.Fatalf("corrected parent-range block = %#v", correctedBlock)
		}
	}
	var newBlock models.Block
	if err := db.WithContext(ctx).First(&newBlock, "hash = ?", "0xnew-child").Error; err != nil {
		t.Fatalf("load new child block: %v", err)
	}
	if !newBlock.Canonical || newBlock.Status != models.BlockStatusCanonical || newBlock.ParentHash != "0xnew-parent" || newBlock.Number != 101 {
		t.Fatalf("new child canonical block = %#v", newBlock)
	}

	reason := transactionReorgReason("parent_mismatch", constants.Ethereum, "100")
	requireTransactionRepoCount(t, db, &models.ReconciliationJob{}, "chain_id = ? AND from_block = ? AND to_block = ? AND reason = ?", []any{constants.Ethereum, int64(100), int64(101), reason}, 1)
}

func transactionReorgTestTx(merchantID, domainID, walletID uuid.UUID, uniqueHash, hash, toAddress string, now time.Time) models.Transaction {
	logIndex := "0"
	finalizedAt := now.Add(-time.Minute)
	return models.Transaction{
		ID:                    uuid.New(),
		ChainID:               constants.Ethereum,
		UniqueHash:            uniqueHash,
		Hash:                  hash,
		LogIndex:              &logIndex,
		BlockNumber:           "100",
		BlockHash:             "0xold-block",
		Symbol:                "ETH",
		Decimals:              18,
		FromAddress:           "0xsender",
		ToAddress:             toAddress,
		Amount:                "100",
		Status:                models.TransactionStatusConfirmed,
		Confirmations:         12,
		ConfirmationsRequired: 12,
		FinalizedAt:           &finalizedAt,
		EventType:             constants.WebhookEventNativeTransfer,
		WalletID:              &walletID,
		MerchantID:            &merchantID,
		DomainID:              &domainID,
		ProductID:             "checkout:reorg",
		UserID:                "user-reorg",
		WebhookSentAt:         &finalizedAt,
		WebhookAttempts:       4,
		WebhookLastError:      "previous transaction webhook error",
		WebhookLockedUntil:    &finalizedAt,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
}

func transactionReorgTestChainFact(txModel models.Transaction, observedAddress string, now time.Time) models.ChainFact {
	logIndex := ""
	if txModel.LogIndex != nil {
		logIndex = *txModel.LogIndex
	}
	return models.ChainFact{
		ID:                    uuid.New(),
		EventID:               ChainFactEventID(txModel.ChainID, txModel.Hash, logIndex),
		ChainID:               txModel.ChainID,
		BlockNumber:           100,
		BlockHash:             txModel.BlockHash,
		TxHash:                txModel.Hash,
		LogIndex:              logIndex,
		ObservedAddress:       observedAddress,
		Direction:             models.ChainFactDirectionTo,
		Symbol:                txModel.Symbol,
		Decimals:              txModel.Decimals,
		AmountRaw:             txModel.Amount,
		Confirmations:         txModel.Confirmations,
		ConfirmationsRequired: txModel.ConfirmationsRequired,
		Finalized:             true,
		Status:                models.ChainFactStatusObserved,
		SourceEventType:       txModel.EventType,
		RawMetadataJSON:       `{}`,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
}

func transactionReorgTestDeposit(fact models.ChainFact, uniqueHash string, merchantID, domainID, walletID uuid.UUID, productID, userID string, now time.Time) models.Deposit {
	return models.Deposit{
		ID:                    uuid.New(),
		ChainFactID:           fact.ID,
		ChainFactEventID:      fact.EventID,
		Status:                models.DepositStatusFinalized,
		WalletID:              &walletID,
		MerchantID:            &merchantID,
		DomainID:              &domainID,
		ProductID:             productID,
		UserID:                userID,
		ChainID:               fact.ChainID,
		BlockNumber:           fact.BlockNumber,
		BlockHash:             fact.BlockHash,
		TxHash:                fact.TxHash,
		LogIndex:              fact.LogIndex,
		ObservedAddress:       fact.ObservedAddress,
		Direction:             fact.Direction,
		Token:                 fact.Token,
		Symbol:                fact.Symbol,
		Decimals:              fact.Decimals,
		AmountRaw:             fact.AmountRaw,
		Confirmations:         fact.Confirmations,
		ConfirmationsRequired: fact.ConfirmationsRequired,
		TransactionUniqueHash: uniqueHash,
		SourceEventType:       fact.SourceEventType,
		DetectedAt:            now.Add(-2 * time.Minute),
		FinalizedAt:           &now,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
}

func requireTransactionRepoCount(t *testing.T, db *gorm.DB, model any, query string, args []any, want int64) {
	t.Helper()
	var count int64
	if err := db.Model(model).Where(query, args...).Count(&count).Error; err != nil {
		t.Fatalf("count %s: %v", query, err)
	}
	if count != want {
		t.Fatalf("count %s args=%v = %d, want %d", query, args, count, want)
	}
}
