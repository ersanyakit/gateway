package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
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

func TestChainFactReappearanceRequiresExactCanonicalBlockAndEconomicIdentity(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(
		&models.Block{},
		&models.ChainFact{},
		&models.MoneyEventInbox{},
		&models.ReconciliationJob{},
	); err != nil {
		t.Fatalf("automigrate chain-fact reappearance models: %v", err)
	}
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	reorgedAt := now.Add(-time.Minute)
	eventID := ChainFactEventID(constants.Ethereum, "0xreappear", "log:4")
	fact := models.ChainFact{
		ID:                    uuid.New(),
		EventID:               eventID,
		ChainID:               constants.Ethereum,
		BlockNumber:           100,
		BlockHash:             "0xorphan",
		TxHash:                "0xreappear",
		LogIndex:              "log:4",
		ObservedAddress:       "0x00000000000000000000000000000000000000aa",
		Direction:             models.ChainFactDirectionTo,
		ObservationStatus:     models.ChainFactObservationConfirmed,
		Symbol:                "ETH",
		Decimals:              18,
		AmountRaw:             "100",
		Confirmations:         12,
		ConfirmationsRequired: 12,
		Finalized:             true,
		Status:                models.ChainFactStatusReorged,
		ReorgedAt:             &reorgedAt,
		CorrectionReason:      "test reorg",
		SourceEventType:       constants.WebhookEventNativeTransfer,
		RawMetadataJSON:       `{}`,
		CreatedAt:             now.Add(-2 * time.Minute),
		UpdatedAt:             now,
	}
	if err := db.Create(&fact).Error; err != nil {
		t.Fatalf("seed reorged fact: %v", err)
	}
	processedAt := now.Add(-90 * time.Second)
	inbox := models.MoneyEventInbox{
		ID:               uuid.New(),
		EventID:          eventID,
		ConsumerName:     "deposit_fact_processor",
		IdempotencyScope: "deposit_fact_processor:" + eventID,
		EventType:        fact.SourceEventType,
		ResourceType:     "chain_fact",
		ResourceID:       fact.ID.String(),
		Status:           models.MoneyEventInboxStatusSucceeded,
		Attempts:         3,
		MaxAttempts:      8,
		ProcessedAt:      &processedAt,
		EvidenceJSON:     `{}`,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := db.Create(&inbox).Error; err != nil {
		t.Fatalf("seed succeeded inbox: %v", err)
	}
	if err := db.Create(&models.Block{
		ID: uuid.New(), ChainID: constants.Ethereum, Number: 100, Hash: "0xorphan",
		Canonical: false, Status: models.BlockStatusReorged, ReorgedAt: &reorgedAt,
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed orphan block: %v", err)
	}
	if err := db.Create(&models.Block{
		ID: uuid.New(), ChainID: constants.Ethereum, Number: 101, Hash: "0xcanonical",
		Canonical: true, Status: models.BlockStatusCanonical, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed canonical block: %v", err)
	}

	repo := NewChainFactRepo(db)
	stale := fact
	stale.ID = uuid.New()
	stale.Status = models.ChainFactStatusObserved
	stale.ReorgedAt = nil
	stale.CorrectionReason = ""
	if got, created, err := repo.RecordOrUpdate(ctx, &stale); err != nil {
		t.Fatalf("record stale orphan replay: %v", err)
	} else if created || got.Status != models.ChainFactStatusReorged || got.BlockHash != "0xorphan" {
		t.Fatalf("stale orphan revived fact: created=%v fact=%#v", created, got)
	}

	mismatch := stale
	mismatch.BlockNumber = 101
	mismatch.BlockHash = "0xcanonical"
	mismatch.AmountRaw = "101"
	if got, created, err := repo.RecordOrUpdate(ctx, &mismatch); err != nil {
		t.Fatalf("record canonical payload mismatch: %v", err)
	} else if created || got.Status != models.ChainFactStatusReorged {
		t.Fatalf("payload mismatch revived fact: created=%v fact=%#v", created, got)
	}
	requireTransactionRepoCount(t, db, &models.ReconciliationJob{}, "resource_type = ? AND resource_id = ? AND reason = ?", []any{"chain_fact", eventID, "chain_fact_reappearance_payload_mismatch"}, 1)

	reappeared := stale
	reappeared.BlockNumber = 101
	reappeared.BlockHash = "0xcanonical"
	reappeared.Confirmations = 2
	reappeared.ConfirmationsRequired = 12
	reappeared.Finalized = false
	got, created, err := repo.RecordOrUpdate(ctx, &reappeared)
	if err != nil {
		t.Fatalf("record exact canonical reappearance: %v", err)
	}
	if created || got.Status != models.ChainFactStatusObserved || got.ReorgedAt != nil || got.CorrectionReason != "" || got.BlockNumber != 101 || got.BlockHash != "0xcanonical" || got.Confirmations != 2 || got.Finalized {
		t.Fatalf("revived fact = created:%v %#v", created, got)
	}
	var reset models.MoneyEventInbox
	if err := db.First(&reset, "id = ?", inbox.ID).Error; err != nil {
		t.Fatalf("load reset inbox: %v", err)
	}
	if reset.Status != models.MoneyEventInboxStatusReceived || reset.Attempts != 0 || reset.ProcessedAt != nil || reset.LockedUntil != nil {
		t.Fatalf("inbox was not reset for reprocessing: %#v", reset)
	}
}

func TestChainFactRecordWaitsForCanonicalObserverAndQuarantinesStaleInsert(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Block{}, &models.ChainFact{}, &models.MoneyEventInbox{}, &models.ReconciliationJob{}); err != nil {
		t.Fatalf("automigrate chain-fact observation race models: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(4)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := db.Create(&models.Block{
		ID: uuid.New(), ChainID: constants.Ethereum, Number: 100, Hash: "0xorphan", ParentHash: "0xparent",
		Canonical: true, Status: models.BlockStatusCanonical, Processed: true, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed pre-correction canonical block: %v", err)
	}

	observerTx := db.WithContext(ctx).Begin()
	if observerTx.Error != nil {
		t.Fatal(observerTx.Error)
	}
	defer observerTx.Rollback()
	if err := AcquireCanonicalBlockLockWithDB(ctx, observerTx, constants.Ethereum, 100); err != nil {
		t.Fatalf("lock canonical observer: %v", err)
	}
	reorgedAt := time.Now().UTC().Truncate(time.Microsecond)
	if err := observerTx.Model(&models.Block{}).Where("chain_id = ? AND number = ?", constants.Ethereum, 100).Updates(map[string]any{
		"canonical": false, "status": models.BlockStatusReorged, "reorged_at": &reorgedAt,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := observerTx.Create(&models.Block{
		ID: uuid.New(), ChainID: constants.Ethereum, Number: 100, Hash: "0xcanonical", ParentHash: "0xparent",
		Canonical: true, Status: models.BlockStatusCanonical, Processed: true, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	attemptedCanonicalLock := make(chan struct{}, 1)
	callbackName := "test:chain-fact-canonical-lock:" + uuid.NewString()
	if err := db.Callback().Raw().Before("gorm:raw").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement != nil && strings.Contains(tx.Statement.SQL.String(), "pg_advisory_xact_lock") {
			select {
			case attemptedCanonicalLock <- struct{}{}:
			default:
			}
		}
	}); err != nil {
		t.Fatalf("register canonical observer callback: %v", err)
	}
	defer db.Callback().Raw().Remove(callbackName)

	fact := models.ChainFact{
		ID: uuid.New(), EventID: ChainFactEventID(constants.Ethereum, "0xrace", "tx:0"), ChainID: constants.Ethereum,
		BlockNumber: 100, BlockHash: "0xorphan", TxHash: "0xrace", LogIndex: "tx:0",
		ObservedAddress: "0x0000000000000000000000000000000000000001", Direction: models.ChainFactDirectionTo,
		ObservationStatus: models.ChainFactObservationConfirmed, Symbol: "ETH", Decimals: 18, AmountRaw: "100",
		Confirmations: 12, ConfirmationsRequired: 12, Finalized: true, Status: models.ChainFactStatusObserved,
		SourceEventType: constants.WebhookEventNativeTransfer, RawMetadataJSON: `{}`, CreatedAt: now, UpdatedAt: now,
	}
	type recordResult struct {
		fact    *models.ChainFact
		created bool
		err     error
	}
	done := make(chan recordResult, 1)
	go func() {
		stored, created, err := NewChainFactRepo(db).RecordOrUpdate(ctx, &fact)
		done <- recordResult{fact: stored, created: created, err: err}
	}()
	select {
	case <-attemptedCanonicalLock:
	case <-time.After(5 * time.Second):
		t.Fatal("chain fact record did not wait on canonical observer")
	}
	if err := observerTx.Commit().Error; err != nil {
		t.Fatal(err)
	}
	var first recordResult
	select {
	case first = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stale fact insert remained blocked after observer commit")
	}
	if first.err != nil || !first.created || first.fact == nil || first.fact.Status != models.ChainFactStatusReorged || first.fact.Finalized || first.fact.BlockHash != "0xorphan" {
		t.Fatalf("stale fact was not quarantined: %#v err=%v", first, first.err)
	}

	reappeared := fact
	reappeared.BlockHash = "0xcanonical"
	reappeared.Status = models.ChainFactStatusObserved
	reappeared.ReorgedAt = nil
	reappeared.CorrectionReason = ""
	reappeared.Finalized = true
	stored, created, err := NewChainFactRepo(db).RecordOrUpdate(ctx, &reappeared)
	if err != nil {
		t.Fatalf("record exact canonical reappearance: %v", err)
	}
	if created || stored.Status != models.ChainFactStatusObserved || stored.ReorgedAt != nil || stored.BlockHash != "0xcanonical" || !stored.Finalized {
		t.Fatalf("canonical reappearance = created:%v %#v", created, stored)
	}
}

func TestCanonicalTransactionReappearanceRestoresLedgerAcrossRepeatedReorgCycles(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(
		&models.Block{},
		&models.Transaction{},
		&models.ChainFact{},
		&models.Deposit{},
		&models.LedgerEntry{},
		&models.LedgerBalanceProjection{},
		&models.PaymentSession{},
		&models.PaymentDepositAllocation{},
		&models.SweepJob{},
		&models.MoneyEventOutbox{},
		&models.ReconciliationJob{},
	); err != nil {
		t.Fatalf("automigrate transaction restoration models: %v", err)
	}
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	txModel := transactionReorgTestTx(merchantID, domainID, walletID, "1-0xledger-reappear-0", "0xledger-reappear", "0x00000000000000000000000000000000000000bb", now)
	txModel.Status = models.TransactionStatusReorged
	txModel.EventType = constants.WebhookEventTransactionReorged
	txModel.CorrectionReason = "test reorg"
	reorgedAt := now.Add(-time.Minute)
	txModel.ReorgedAt = &reorgedAt
	if err := db.Create(&txModel).Error; err != nil {
		t.Fatalf("seed reorged transaction: %v", err)
	}
	if err := db.Create(&models.Block{ID: uuid.New(), ChainID: constants.Ethereum, Number: 100, Hash: "0xold-block", Canonical: false, Status: models.BlockStatusReorged, ReorgedAt: &reorgedAt, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("seed old block: %v", err)
	}
	if err := db.Create(&models.Block{ID: uuid.New(), ChainID: constants.Ethereum, Number: 101, Hash: "0xnew-block-1", Canonical: true, Status: models.BlockStatusCanonical, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("seed first canonical block: %v", err)
	}

	credit := testLedgerEntryWithType("reappearance-original-credit:"+uuid.NewString(), merchantID, &domainID, &walletID, models.LedgerEntryTypeDepositAvailable, models.LedgerAccountMerchantAvailable, models.LedgerDirectionCredit, models.LedgerStatusPosted, "100")
	debit := testLedgerEntryWithType("reappearance-original-debit:"+uuid.NewString(), merchantID, &domainID, &walletID, models.LedgerEntryTypeDepositAvailable, models.LedgerAccountPlatformClearing, models.LedgerDirectionDebit, models.LedgerStatusPosted, "100")
	for _, entry := range []*models.LedgerEntry{&credit, &debit} {
		entry.TransactionUniqueHash = txModel.UniqueHash
		entry.TransactionHash = txModel.Hash
	}
	if err := db.Create(&[]models.LedgerEntry{credit, debit}).Error; err != nil {
		t.Fatalf("seed original ledger entries: %v", err)
	}
	if err := NewLedgerRepo(db).PostTransactionReversal(ctx, txModel); err != nil {
		t.Fatalf("seed first reorg reversal: %v", err)
	}
	assertTransactionLedgerBalance(t, db, txModel.UniqueHash, models.LedgerAccountMerchantAvailable, "0")
	assertMerchantProjectedBalance(t, db, merchantID, models.LedgerAccountMerchantAvailable, "0")

	repo := NewTransactionRepo(db)
	firstParams := canonicalReappearanceParams(ctx, txModel, "101", "0xnew-block-1")
	if err := repo.Create(firstParams); err != nil {
		t.Fatalf("revive transaction first cycle: %v", err)
	}
	firstRevived, err := repo.FindByUniqueHash(ctx, txModel.UniqueHash)
	if err != nil {
		t.Fatalf("load first revived transaction: %v", err)
	}
	if firstRevived.Status != models.TransactionStatusPendingConfirmation || firstRevived.FinalizedAt != nil || !transactionIsCanonicalReappearance(*firstRevived) {
		t.Fatalf("first revived lifecycle not reset: %#v", firstRevived)
	}
	firstFinal, err := repo.MarkFinality(ctx, txModel.UniqueHash, 12, 12, true)
	if err != nil {
		t.Fatalf("finalize first revival: %v", err)
	}
	if firstFinal.Status != models.TransactionStatusConfirmed || firstFinal.FinalizedAt == nil {
		t.Fatalf("first revival was not finalized: %#v", firstFinal)
	}
	assertTransactionLedgerBalance(t, db, txModel.UniqueHash, models.LedgerAccountMerchantAvailable, "100")
	assertMerchantProjectedBalance(t, db, merchantID, models.LedgerAccountMerchantAvailable, "100")
	requireTransactionRepoCount(t, db, &models.LedgerEntry{}, "transaction_unique_hash = ? AND description LIKE ?", []any{txModel.UniqueHash, "Canonical reappearance restoration%"}, 2)
	if _, err := repo.MarkFinality(ctx, txModel.UniqueHash, 13, 12, true); err != nil {
		t.Fatalf("repeat first finality: %v", err)
	}
	requireTransactionRepoCount(t, db, &models.LedgerEntry{}, "transaction_unique_hash = ? AND description LIKE ?", []any{txModel.UniqueHash, "Canonical reappearance restoration%"}, 2)

	secondReorgedAt := now.Add(time.Minute)
	if err := db.Transaction(func(tx *gorm.DB) error {
		return NewTransactionRepo(tx).markTransactionsReorgedWithDB(
			ctx,
			tx,
			[]models.Transaction{*firstFinal},
			secondReorgedAt,
			constants.Ethereum,
			101,
			101,
			"second test reorg",
		)
	}); err != nil {
		t.Fatalf("atomically mark transaction reorged for second cycle: %v", err)
	}
	assertTransactionLedgerBalance(t, db, txModel.UniqueHash, models.LedgerAccountMerchantAvailable, "0")
	assertMerchantProjectedBalance(t, db, merchantID, models.LedgerAccountMerchantAvailable, "0")
	requireTransactionRepoCount(t, db, &models.MoneyEventOutbox{}, "aggregate_type = ? AND aggregate_id = ? AND event_type = ?", []any{"transaction", txModel.ID.String(), "transaction.reorged.v1"}, 1)
	if err := db.Model(&models.Block{}).Where("chain_id = ? AND hash = ?", constants.Ethereum, "0xnew-block-1").Updates(map[string]any{"canonical": false, "status": models.BlockStatusReorged, "reorged_at": &secondReorgedAt}).Error; err != nil {
		t.Fatalf("mark first replacement noncanonical: %v", err)
	}
	if err := db.Create(&models.Block{ID: uuid.New(), ChainID: constants.Ethereum, Number: 102, Hash: "0xnew-block-2", Canonical: true, Status: models.BlockStatusCanonical, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("seed second canonical block: %v", err)
	}

	secondParams := canonicalReappearanceParams(ctx, txModel, "102", "0xnew-block-2")
	if err := repo.Create(secondParams); err != nil {
		t.Fatalf("revive transaction second cycle: %v", err)
	}
	if _, err := repo.MarkFinality(ctx, txModel.UniqueHash, 12, 12, true); err != nil {
		t.Fatalf("finalize second revival: %v", err)
	}
	assertTransactionLedgerBalance(t, db, txModel.UniqueHash, models.LedgerAccountMerchantAvailable, "100")
	assertMerchantProjectedBalance(t, db, merchantID, models.LedgerAccountMerchantAvailable, "100")
	// The mutable transaction is confirmed again, but the immutable correction
	// must remain available for the outbox relay.
	requireTransactionRepoCount(t, db, &models.MoneyEventOutbox{}, "aggregate_type = ? AND aggregate_id = ? AND event_type = ?", []any{"transaction", txModel.ID.String(), "transaction.reorged.v1"}, 1)
	requireTransactionRepoCount(t, db, &models.MoneyEventOutbox{}, "aggregate_type = ? AND aggregate_id = ? AND event_type = ?", []any{"transaction", txModel.ID.String(), constants.WebhookEventTransactionRestored}, 2)
	var transactionEvents []models.MoneyEventOutbox
	if err := db.Where("aggregate_type = ? AND aggregate_id = ?", "transaction", txModel.ID.String()).Order("created_at ASC").Find(&transactionEvents).Error; err != nil {
		t.Fatalf("load transaction correction/restoration history: %v", err)
	}
	if len(transactionEvents) != 3 ||
		transactionEvents[0].EventType != constants.WebhookEventTransactionRestored ||
		transactionEvents[1].EventType != constants.WebhookEventTransactionReorged ||
		transactionEvents[2].EventType != constants.WebhookEventTransactionRestored ||
		transactionEvents[0].EventID == transactionEvents[1].EventID ||
		transactionEvents[1].EventID == transactionEvents[2].EventID {
		t.Fatalf("transaction correction/restoration history = %#v", transactionEvents)
	}
	var transactionRestorationPayload map[string]any
	if err := json.Unmarshal([]byte(transactionEvents[2].PayloadJSON), &transactionRestorationPayload); err != nil {
		t.Fatalf("decode transaction restoration payload: %v", err)
	}
	if transactionRestorationPayload["reorg_event_id"] != transactionEvents[1].EventID || transactionRestorationPayload["canonical_block_hash"] != "0xnew-block-2" {
		t.Fatalf("transaction restoration payload = %#v", transactionRestorationPayload)
	}
	requireTransactionRepoCount(t, db, &models.LedgerEntry{}, "transaction_unique_hash = ? AND description LIKE ?", []any{txModel.UniqueHash, "Canonical reappearance restoration%"}, 4)
	if _, err := repo.MarkFinality(ctx, txModel.UniqueHash, 13, 12, true); err != nil {
		t.Fatalf("repeat second finality: %v", err)
	}
	requireTransactionRepoCount(t, db, &models.LedgerEntry{}, "transaction_unique_hash = ? AND description LIKE ?", []any{txModel.UniqueHash, "Canonical reappearance restoration%"}, 4)
}

func TestTransactionReorgSerializesWithWithdrawalAndRefreshesProjection(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.LedgerEntry{}, &models.LedgerBalanceProjection{}); err != nil {
		t.Fatalf("automigrate concurrent reorg ledger models: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get concurrent reorg sql db: %v", err)
	}
	// The shared test helper intentionally uses one connection by default. This
	// test needs independent PostgreSQL sessions so the advisory lock, rather
	// than the client pool, is what serializes the two operations.
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(4)

	ctx := context.Background()
	repo := NewLedgerRepo(db)
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	txModel := transactionReorgTestTx(
		merchantID,
		domainID,
		walletID,
		"concurrent-reorg-withdrawal-"+uuid.NewString(),
		"0xconcurrent-reorg-withdrawal",
		"0x00000000000000000000000000000000000000ef",
		now,
	)
	if err := repo.PostStandaloneDepositAvailable(ctx, txModel); err != nil {
		t.Fatalf("seed spendable deposit: %v", err)
	}
	assertMerchantProjectedBalance(t, db, merchantID, models.LedgerAccountMerchantAvailable, "100")

	reorgReady := make(chan error, 1)
	releaseReorg := make(chan struct{})
	reorgDone := make(chan error, 1)
	go func() {
		reorgDone <- db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			err := NewLedgerRepo(tx).PostTransactionReversalWithDB(ctx, tx, txModel)
			reorgReady <- err
			if err != nil {
				return err
			}
			<-releaseReorg
			return nil
		})
	}()

	released := false
	release := func() {
		if !released {
			close(releaseReorg)
			released = true
		}
	}
	defer release()
	select {
	case err := <-reorgReady:
		if err != nil {
			t.Fatalf("append uncommitted reorg reversal: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for uncommitted reorg reversal")
	}

	withdrawal := models.WithdrawalRequest{
		ID:         uuid.New(),
		MerchantID: merchantID,
		DomainID:   &domainID,
		WalletID:   walletID,
		Chain:      "ethereum",
		Symbol:     "ETH",
		Decimals:   18,
		AmountRaw:  "80",
		Status:     models.WithdrawalStatusPending,
		ToAddress:  "0x00000000000000000000000000000000000000ff",
	}
	withdrawalDone := make(chan error, 1)
	go func() {
		withdrawalDone <- repo.CreateWithdrawalHold(ctx, withdrawal)
	}()

	// The reversal has already been appended but is intentionally uncommitted.
	// A withdrawal must wait on its asset advisory lock instead of observing the
	// old spendable balance and creating a hold.
	select {
	case err := <-withdrawalDone:
		release()
		<-reorgDone
		t.Fatalf("withdrawal escaped the uncommitted reorg lock: %v", err)
	case <-time.After(250 * time.Millisecond):
	}

	release()
	select {
	case err := <-reorgDone:
		if err != nil {
			t.Fatalf("commit reorg reversal: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out committing reorg reversal")
	}
	select {
	case err := <-withdrawalDone:
		if !errors.Is(err, ErrInsufficientAvailableBalance) {
			t.Fatalf("withdrawal after committed reorg = %v, want insufficient balance", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for serialized withdrawal")
	}

	requireTransactionRepoCount(t, db, &models.LedgerEntry{}, "withdrawal_id = ?", []any{withdrawal.ID}, 0)
	assertTransactionLedgerBalance(t, db, txModel.UniqueHash, models.LedgerAccountMerchantAvailable, "0")
	assertMerchantProjectedBalance(t, db, merchantID, models.LedgerAccountMerchantAvailable, "0")
}

func TestReorgedPaymentAllocationRestoresOncePerCanonicalGeneration(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(
		&models.Merchant{},
		&models.Domain{},
		&models.Wallet{},
		&models.Block{},
		&models.Deposit{},
		&models.PaymentSession{},
		&models.PaymentDepositAllocation{},
		&models.MoneyEventOutbox{},
		&models.ReconciliationJob{},
	); err != nil {
		t.Fatalf("automigrate payment restoration models: %v", err)
	}
	ctx := context.Background()
	merchantID, domainID, walletID := seedPaymentMatchScope(t, ctx, db, "canonical-reappearance")
	var wallet models.Wallet
	if err := db.First(&wallet, "id = ?", walletID).Error; err != nil {
		t.Fatalf("load wallet: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	chainID := constants.Ethereum
	txUniqueHash := "1-0xpayment-reappear-0"
	txHash := "0xpayment-reappear"
	finalizedAt := now
	reorgedAt := now.Add(-time.Minute)
	txModel := models.Transaction{
		ID: uuid.New(), ChainID: chainID, UniqueHash: txUniqueHash, Hash: txHash,
		BlockNumber: "201", BlockHash: "0xpayment-canonical-1", Symbol: "ETH", Decimals: 18,
		FromAddress: "0x00000000000000000000000000000000000000cc", ToAddress: wallet.EthereumAddress,
		Amount: "100", Status: models.TransactionStatusConfirmed, Confirmations: 12, ConfirmationsRequired: 12,
		FinalizedAt: &finalizedAt, CorrectionReason: transactionReappearanceMarker(&reorgedAt, "201", "0xpayment-canonical-1"),
		WalletID: &walletID, MerchantID: &merchantID, DomainID: &domainID, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&models.Block{ID: uuid.New(), ChainID: chainID, Number: 201, Hash: txModel.BlockHash, Canonical: true, Status: models.BlockStatusCanonical, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("seed payment canonical block: %v", err)
	}
	deposit := models.Deposit{
		ID: uuid.New(), ChainFactID: uuid.New(), ChainFactEventID: ChainFactEventID(chainID, txHash, "0"), Status: models.DepositStatusFinalized,
		WalletID: &walletID, MerchantID: &merchantID, DomainID: &domainID,
		ChainID: chainID, BlockNumber: 201, BlockHash: txModel.BlockHash, TxHash: txHash, LogIndex: "0",
		ObservedAddress: wallet.EthereumAddress, Direction: models.ChainFactDirectionTo, ObservationStatus: models.DepositObservationConfirmed,
		MemoStatus: models.DepositMemoStatusNotRequired, Symbol: "ETH", Decimals: 18, AmountRaw: "100",
		Confirmations: 12, ConfirmationsRequired: 12, TransactionUniqueHash: txUniqueHash, SourceEventType: constants.WebhookEventNativeTransfer,
		DetectedAt: now, FinalizedAt: &finalizedAt, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&deposit).Error; err != nil {
		t.Fatalf("seed restored deposit: %v", err)
	}
	session := models.PaymentSession{
		ID: uuid.New(), SessionToken: "reappearance-" + uuid.NewString(), MerchantID: merchantID, DomainID: domainID, WalletID: walletID,
		OrderID: "order-reappearance", Amount: "1", Currency: "ETH", SelectedChainID: &chainID,
		SelectedSymbol: "ETH", SelectedDecimals: 18, ExpectedAmountRaw: "100", DepositAddress: wallet.EthereumAddress,
		SettlementPolicy: models.PaymentSettlementPolicySingle, Status: models.PaymentStatusFailed,
		PaymentOutcome: models.PaymentOutcomeExact, PaymentOutcomeReason: models.PaymentOutcomeReasonReorged,
		MatchedAmountRaw: "100", TxUniqueHash: &txUniqueHash, TxHash: &txHash,
		ConfirmationsRequired: 12, WebhookEvent: constants.WebhookEventPaymentFailed, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&session).Error; err != nil {
		t.Fatalf("seed reorged payment session: %v", err)
	}
	allocation := models.PaymentDepositAllocation{
		ID: uuid.New(), PaymentSessionID: session.ID, DepositID: &deposit.ID,
		TransactionUniqueHash: txUniqueHash, ChainFactEventID: deposit.ChainFactEventID, TxHash: txHash,
		ChainID: chainID, ObservedAddress: deposit.ObservedAddress, ObservedAddressNormalized: NormalizeWalletLookupAddress(chainID, deposit.ObservedAddress),
		Symbol: "ETH", Decimals: 18, AmountRaw: "100", MemoStatus: models.DepositMemoStatusNotRequired,
		Status: models.PaymentDepositAllocationStatusReorged, Outcome: models.PaymentOutcomeExact,
		Reason: models.PaymentOutcomeReasonReorged, ReorgedAt: &reorgedAt, CreatedAt: now.Add(-2 * time.Minute), UpdatedAt: now,
	}
	if err := db.Create(&allocation).Error; err != nil {
		t.Fatalf("seed reorged allocation: %v", err)
	}

	repo := NewPaymentRepo(db)
	result, err := repo.MatchFinalizedDeposit(ctx, txModel, &deposit)
	if err != nil {
		t.Fatalf("restore payment allocation: %v", err)
	}
	if result == nil || !result.Changed || result.Session == nil || result.Status != models.PaymentStatusPaid || result.Outcome != models.PaymentOutcomeExact || !result.LedgerEligible {
		t.Fatalf("payment restoration result = %#v", result)
	}
	var restoredAllocation models.PaymentDepositAllocation
	if err := db.First(&restoredAllocation, "id = ?", allocation.ID).Error; err != nil {
		t.Fatalf("load restored allocation: %v", err)
	}
	if restoredAllocation.Status != models.PaymentDepositAllocationStatusApplied || restoredAllocation.ReorgedAt != nil || restoredAllocation.Outcome != models.PaymentOutcomeExact {
		t.Fatalf("restored allocation = %#v", restoredAllocation)
	}
	var events []models.MoneyEventOutbox
	if err := db.Where("aggregate_id = ?", session.ID.String()).Order("created_at ASC").Find(&events).Error; err != nil {
		t.Fatalf("load payment restoration events: %v", err)
	}
	if len(events) != 1 || !strings.Contains(events[0].EventID, allocation.ID.String()) || !strings.Contains(events[0].EventID, strconv.FormatInt(moneyEventGenerationUnixNano(reorgedAt), 10)) {
		t.Fatalf("first restoration event identity = %#v", events)
	}
	var firstPayload map[string]any
	if err := json.Unmarshal([]byte(events[0].PayloadJSON), &firstPayload); err != nil {
		t.Fatalf("decode first restoration payload: %v", err)
	}
	if firstPayload["restoration"] != true || firstPayload["canonical_block_hash"] != "0xpayment-canonical-1" {
		t.Fatalf("first restoration payload = %#v", firstPayload)
	}
	if _, err := repo.MatchFinalizedDeposit(ctx, txModel, &deposit); err != nil {
		t.Fatalf("repeat restored allocation match: %v", err)
	}
	requirePostgresCount(t, db, &models.MoneyEventOutbox{}, "aggregate_id = ?", session.ID.String(), 1)

	if err := db.Transaction(func(tx *gorm.DB) error {
		return NewPaymentRepo(tx).MarkReorgedByTransactionWithDB(ctx, tx, txUniqueHash)
	}); err != nil {
		t.Fatalf("mark restored payment reorged again: %v", err)
	}
	secondReorgedAt := time.Now().UTC().Add(time.Minute)
	if err := db.Model(&models.PaymentDepositAllocation{}).Where("id = ?", allocation.ID).Update("reorged_at", &secondReorgedAt).Error; err != nil {
		t.Fatalf("set second allocation generation: %v", err)
	}
	if err := db.Model(&models.Block{}).Where("chain_id = ? AND hash = ?", chainID, "0xpayment-canonical-1").Updates(map[string]any{"canonical": false, "status": models.BlockStatusReorged, "reorged_at": &secondReorgedAt}).Error; err != nil {
		t.Fatalf("mark first payment block noncanonical: %v", err)
	}
	txModel.BlockNumber = "202"
	txModel.BlockHash = "0xpayment-canonical-2"
	txModel.CorrectionReason = transactionReappearanceMarker(&secondReorgedAt, txModel.BlockNumber, txModel.BlockHash)
	deposit.BlockNumber = 202
	deposit.BlockHash = txModel.BlockHash
	if err := db.Create(&models.Block{ID: uuid.New(), ChainID: chainID, Number: 202, Hash: txModel.BlockHash, Canonical: true, Status: models.BlockStatusCanonical, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("seed second payment canonical block: %v", err)
	}
	if _, err := repo.MatchFinalizedDeposit(ctx, txModel, &deposit); err != nil {
		t.Fatalf("restore second payment generation: %v", err)
	}
	if err := db.Where("aggregate_id = ?", session.ID.String()).Order("created_at ASC").Find(&events).Error; err != nil {
		t.Fatalf("load both restoration events: %v", err)
	}
	if len(events) != 3 ||
		events[0].EventType != "payment.succeeded.v1" ||
		events[1].EventType != "payment.failed.v1" ||
		events[2].EventType != "payment.succeeded.v1" ||
		events[0].EventID == events[1].EventID ||
		events[1].EventID == events[2].EventID ||
		!strings.Contains(events[2].EventID, strconv.FormatInt(moneyEventGenerationUnixNano(secondReorgedAt), 10)) {
		t.Fatalf("restoration generations are not unique: %#v", events)
	}
	var correctionPayload map[string]any
	if err := json.Unmarshal([]byte(events[1].PayloadJSON), &correctionPayload); err != nil {
		t.Fatalf("decode immutable payment correction payload: %v", err)
	}
	if correctionPayload["correction"] != true || correctionPayload["reorged_tx_unique_hash"] != txUniqueHash {
		t.Fatalf("payment correction payload = %#v", correctionPayload)
	}
	var currentSession models.PaymentSession
	if err := db.First(&currentSession, "id = ?", session.ID).Error; err != nil {
		t.Fatalf("load immediately restored payment session: %v", err)
	}
	if currentSession.Status != models.PaymentStatusPaid || currentSession.PaymentOutcome != models.PaymentOutcomeExact {
		t.Fatalf("payment session not restored after immutable correction: %#v", currentSession)
	}
}

func TestAggregatePaymentReorgRecomputesFromRemainingAppliedAllocations(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(
		&models.Merchant{},
		&models.Domain{},
		&models.Wallet{},
		&models.PaymentSession{},
		&models.PaymentDepositAllocation{},
		&models.ReconciliationJob{},
	); err != nil {
		t.Fatalf("automigrate aggregate recomputation models: %v", err)
	}
	ctx := context.Background()
	merchantID, domainID, walletID := seedPaymentMatchScope(t, ctx, db, "aggregate-reorg-recompute")
	now := time.Now().UTC().Truncate(time.Microsecond)
	targetUniqueHash := "aggregate-target-" + uuid.NewString()
	remainingUniqueHash := "aggregate-remaining-" + uuid.NewString()
	sentAt := now.Add(-time.Minute)
	session := models.PaymentSession{
		ID: uuid.New(), SessionToken: "aggregate-recompute-" + uuid.NewString(),
		MerchantID: merchantID, DomainID: domainID, WalletID: walletID,
		OrderID: "aggregate-order", Amount: "10", Currency: "USD",
		ExpectedAmountRaw: "1000", SettlementPolicy: models.PaymentSettlementPolicyAggregate,
		Status: models.PaymentStatusPaid, PaymentOutcome: models.PaymentOutcomeAggregateComplete,
		PaymentOutcomeReason: "aggregate finalized deposits exactly match expected amount",
		MatchedAmountRaw:     "1000", TxUniqueHash: &targetUniqueHash,
		WebhookEvent: constants.WebhookEventPaymentSucceeded, WebhookSentAt: &sentAt, WebhookAttempts: 2,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&session).Error; err != nil {
		t.Fatalf("seed aggregate session: %v", err)
	}
	allocations := []models.PaymentDepositAllocation{
		{
			ID: uuid.New(), PaymentSessionID: session.ID, TransactionUniqueHash: targetUniqueHash,
			TxHash: "0xtarget", ChainID: constants.Ethereum, Symbol: "ETH", Decimals: 18, AmountRaw: "400",
			Status: models.PaymentDepositAllocationStatusApplied, Outcome: models.PaymentOutcomePartialAggregating,
			CreatedAt: now.Add(-time.Minute), UpdatedAt: now,
		},
		{
			ID: uuid.New(), PaymentSessionID: session.ID, TransactionUniqueHash: remainingUniqueHash,
			TxHash: "0xremaining", ChainID: constants.Ethereum, Symbol: "ETH", Decimals: 18, AmountRaw: "600",
			Status: models.PaymentDepositAllocationStatusApplied, Outcome: models.PaymentOutcomeAggregateComplete,
			CreatedAt: now, UpdatedAt: now,
		},
	}
	if err := db.Create(&allocations).Error; err != nil {
		t.Fatalf("seed aggregate allocations: %v", err)
	}

	if err := db.Transaction(func(tx *gorm.DB) error {
		return NewPaymentRepo(tx).MarkReorgedByTransactionWithDB(ctx, tx, targetUniqueHash)
	}); err != nil {
		t.Fatalf("mark one aggregate allocation reorged: %v", err)
	}
	var updated models.PaymentSession
	if err := db.First(&updated, "id = ?", session.ID).Error; err != nil {
		t.Fatalf("load recomputed aggregate session: %v", err)
	}
	if updated.Status != models.PaymentStatusPartialPaid ||
		updated.PaymentOutcome != models.PaymentOutcomePartialAggregating ||
		updated.MatchedAmountRaw != "600" ||
		updated.ShortfallAmountRaw != "400" ||
		updated.ExcessAmountRaw != "" ||
		updated.WebhookEvent != constants.WebhookEventPaymentPartialPaid ||
		updated.TxUniqueHash != nil ||
		updated.PaidAt != nil ||
		updated.WebhookSentAt != nil ||
		updated.WebhookAttempts != 0 {
		t.Fatalf("aggregate session was not recomputed from remaining allocation: %#v", updated)
	}
	var target, remaining models.PaymentDepositAllocation
	if err := db.First(&target, "id = ?", allocations[0].ID).Error; err != nil {
		t.Fatalf("load target allocation: %v", err)
	}
	if err := db.First(&remaining, "id = ?", allocations[1].ID).Error; err != nil {
		t.Fatalf("load remaining allocation: %v", err)
	}
	if target.Status != models.PaymentDepositAllocationStatusReorged || target.ReorgedAt == nil || remaining.Status != models.PaymentDepositAllocationStatusApplied {
		t.Fatalf("aggregate allocation states target=%#v remaining=%#v", target, remaining)
	}
}

func canonicalReappearanceParams(ctx context.Context, txModel models.Transaction, blockNumber, blockHash string) types.TransactionParam {
	status := models.TransactionStatusConfirmed
	logIndex := ""
	if txModel.LogIndex != nil {
		logIndex = *txModel.LogIndex
	}
	return types.TransactionParam{
		Context: ctx, ChainID: txModel.ChainID, Hash: helpers.StrPtr(txModel.Hash), LogIndex: helpers.StrPtr(logIndex),
		Block: helpers.StrPtr(blockNumber), BlockHash: helpers.StrPtr(blockHash),
		From: helpers.StrPtr(txModel.FromAddress), To: helpers.StrPtr(txModel.ToAddress), Token: txModel.Token,
		Symbol: helpers.StrPtr(txModel.Symbol), Decimals: txModel.Decimals, Amount: helpers.StrPtr(txModel.Amount), Status: &status,
	}
}

func assertTransactionLedgerBalance(t *testing.T, db *gorm.DB, uniqueHash, account, want string) {
	t.Helper()
	var raw string
	if err := db.Model(&models.LedgerEntry{}).
		Select("COALESCE(SUM(CASE WHEN direction = 'credit' THEN amount_raw::numeric ELSE -amount_raw::numeric END), 0)::text").
		Where("transaction_unique_hash = ? AND account = ? AND status IN ?", uniqueHash, account, []string{models.LedgerStatusPending, models.LedgerStatusPosted}).
		Scan(&raw).Error; err != nil {
		t.Fatalf("sum ledger balance: %v", err)
	}
	if raw != want {
		t.Fatalf("ledger balance transaction=%s account=%s = %s, want %s", uniqueHash, account, raw, want)
	}
}

func assertMerchantProjectedBalance(t *testing.T, db *gorm.DB, merchantID uuid.UUID, account, want string) {
	t.Helper()
	rows, err := NewLedgerRepo(db).MerchantBalances(context.Background(), merchantID)
	if err != nil {
		t.Fatalf("load merchant projected balances: %v", err)
	}
	for _, row := range rows {
		if row.Account == account && row.Symbol == "ETH" && row.ChainID == int64(constants.Ethereum) {
			if row.BalanceRaw != want {
				t.Fatalf("merchant projected balance account=%s = %s, want %s", account, row.BalanceRaw, want)
			}
			return
		}
	}
	t.Fatalf("merchant projected balance account=%s not found in %#v", account, rows)
}
