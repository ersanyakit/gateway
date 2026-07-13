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
	"core/types"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestBuildChainFactFromTransactionStableEventIDs(t *testing.T) {
	cases := []struct {
		name     string
		chainID  constants.ChainID
		txHash   string
		logIndex string
		wantID   string
	}{
		{name: "evm-log", chainID: constants.Ethereum, txHash: "0xABC", logIndex: "log:7", wantID: "1:0xabc:log:7"},
		{name: "bitcoin-vout", chainID: constants.Bitcoin, txHash: "btc-tx", logIndex: "vout:1", wantID: "0:btc-tx:vout:1"},
		{name: "solana-instruction", chainID: constants.Solana, txHash: "solsig", logIndex: "ix:2", wantID: "99999999:solsig:ix:2"},
		{name: "tron-log", chainID: constants.TRON, txHash: "tron-tx", logIndex: "log:3", wantID: "99999998:tron-tx:log:3"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fact, err := BuildChainFactFromTransaction("native_transfer", chainFactTestTx(tc.chainID, tc.txHash, tc.logIndex))
			if err != nil {
				t.Fatal(err)
			}
			if fact.EventID != tc.wantID {
				t.Fatalf("event id = %q, want %q", fact.EventID, tc.wantID)
			}
			if fact.BlockNumber != 123 || fact.BlockHash != "0xblock" || fact.ObservedAddress != "0xto" || fact.Direction != models.ChainFactDirectionTo {
				t.Fatalf("fact core fields = %#v", fact)
			}
			if !fact.Finalized || fact.SourceEventType != "native_transfer" || fact.RawMetadataJSON == "" {
				t.Fatalf("fact metadata = %#v", fact)
			}
		})
	}
}

func TestBuildChainFactCarriesFinalityMetadata(t *testing.T) {
	tx := chainFactTestTx(constants.Ethereum, "0xabc", "log:1")
	status := models.TransactionStatusPending
	tx.Status = &status

	fact, err := BuildChainFact(ChainFactBuildParams{
		EventType:             "native_transfer",
		Transaction:           tx,
		Confirmations:         7,
		ConfirmationsRequired: 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	if fact.Finalized {
		t.Fatal("pending transaction must not be finalized")
	}
	if fact.Confirmations != 7 || fact.ConfirmationsRequired != 12 {
		t.Fatalf("finality metadata = confirmations:%d required:%d", fact.Confirmations, fact.ConfirmationsRequired)
	}
}

func TestBuildChainFactAllowsMempoolObservationWithoutBlock(t *testing.T) {
	tx := chainFactTestTx(constants.Ethereum, "0xmempool", "log:0")
	tx.Block = nil
	tx.BlockHash = nil
	tx.Status = strPtr(models.TransactionStatusPending)
	tx.Memo = strPtr("checkout-123")

	fact, err := BuildChainFact(ChainFactBuildParams{
		EventType:             "native_transfer",
		Transaction:           tx,
		Confirmations:         0,
		ConfirmationsRequired: 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	if fact.ObservationStatus != models.ChainFactObservationMempool || fact.BlockNumber != 0 || fact.Finalized {
		t.Fatalf("mempool fact = %#v, want mempool block 0 non-final", fact)
	}
	if fact.Memo != "checkout-123" || fact.MemoNormalized != "checkout-123" {
		t.Fatalf("memo fields = %q/%q", fact.Memo, fact.MemoNormalized)
	}
}

func TestChainFactRepoRecordTransactionKeepsOverlongMemoInRawMetadata(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.ChainFact{}); err != nil {
		t.Fatalf("automigrate chain facts: %v", err)
	}
	repo := NewChainFactRepo(db)
	ctx := context.Background()

	longMemo := strings.Repeat("ballad of the chain ", 12)
	tx := chainFactTestTx(constants.Bitcoin, "2cf15636-2ce7-4fe3-b503-47f3b24ca52b", "vout:0")
	tx.To = strPtr("bc1psy0kp5ktw9e7y0hpyfldrkcmeav2g2vr0xpxy0yn66mprncfsj0qn4xth5")
	tx.Symbol = strPtr("BTC")
	tx.Decimals = 8
	tx.Memo = &longMemo

	fact, created, err := repo.RecordTransaction(ctx, "native_transfer", tx)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("long memo chain fact should be created")
	}
	if fact.Memo != "" || fact.MemoNormalized != "" {
		t.Fatalf("indexed memo fields = %q/%q, want empty for overlong memo", fact.Memo, fact.MemoNormalized)
	}
	var raw map[string]string
	if err := json.Unmarshal([]byte(fact.RawMetadataJSON), &raw); err != nil {
		t.Fatalf("decode raw metadata: %v", err)
	}
	if raw["memo"] != longMemo {
		t.Fatal("raw metadata must retain the full overlong memo")
	}
	requirePostgresCount(t, db, &models.ChainFact{}, "event_id = ?", fact.EventID, 1)
}

func TestBuildChainFactSanitizesNullByteMemoForPostgres(t *testing.T) {
	memo := "TRADE\x00\x00|1780735965119|AAAAAAAAAAA="
	tx := chainFactTestTx(constants.Solana, "nul-memo-sig", "ix:0")
	tx.Symbol = strPtr("SOL")
	tx.Decimals = 9
	tx.Memo = &memo

	fact, err := BuildChainFactFromTransaction("program_call", tx)
	if err != nil {
		t.Fatal(err)
	}
	wantMemo := "TRADE|1780735965119|AAAAAAAAAAA="
	if fact.Memo != wantMemo || fact.MemoNormalized != strings.ToLower(wantMemo) {
		t.Fatalf("memo fields = %q/%q, want sanitized %q", fact.Memo, fact.MemoNormalized, wantMemo)
	}
	if strings.Contains(fact.RawMetadataJSON, "\x00") || strings.Contains(fact.RawMetadataJSON, `\u0000`) {
		t.Fatalf("raw metadata still contains null byte escape: %q", fact.RawMetadataJSON)
	}
	var raw map[string]string
	if err := json.Unmarshal([]byte(fact.RawMetadataJSON), &raw); err != nil {
		t.Fatalf("decode raw metadata: %v", err)
	}
	if raw["memo"] != wantMemo {
		t.Fatalf("raw memo = %q, want %q", raw["memo"], wantMemo)
	}
}

func TestPrepareChainFactSanitizesRawMetadataUnicodeNullEscapes(t *testing.T) {
	fact := mustBuildChainFact(t, ChainFactBuildParams{
		EventType:   "program_call",
		Transaction: chainFactTestTx(constants.Solana, "nul-raw-sig", "ix:1"),
	})
	fact.Memo = ""
	fact.MemoNormalized = ""
	fact.RawMetadataJSON = `{"memo":"PAY\u0000MENT","nested":{"label":"A\u0000B"},"items":["x\u0000y"]}`

	prepared, err := prepareChainFact(fact)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Memo != "PAYMENT" || prepared.MemoNormalized != "payment" {
		t.Fatalf("memo fallback = %q/%q, want sanitized PAYMENT/payment", prepared.Memo, prepared.MemoNormalized)
	}
	if strings.Contains(prepared.RawMetadataJSON, "\x00") || strings.Contains(prepared.RawMetadataJSON, `\u0000`) {
		t.Fatalf("raw metadata still contains null byte escape: %q", prepared.RawMetadataJSON)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(prepared.RawMetadataJSON), &raw); err != nil {
		t.Fatalf("decode raw metadata: %v", err)
	}
	nested, ok := raw["nested"].(map[string]any)
	if !ok || nested["label"] != "AB" {
		t.Fatalf("nested metadata = %#v, want sanitized label", raw["nested"])
	}
	items, ok := raw["items"].([]any)
	if !ok || len(items) != 1 || items[0] != "xy" {
		t.Fatalf("array metadata = %#v, want sanitized item", raw["items"])
	}
}

func TestChainFactRepoRecordTransactionSanitizesNullByteMemo(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.ChainFact{}); err != nil {
		t.Fatalf("automigrate chain facts: %v", err)
	}
	repo := NewChainFactRepo(db)
	ctx := context.Background()

	memo := "TRADE\x00\x00|1780735965119|AAAAAAAAAAA="
	tx := chainFactTestTx(constants.Solana, "nul-db-sig", "ix:0")
	tx.Symbol = strPtr("SOL")
	tx.Decimals = 9
	tx.Memo = &memo

	fact, created, err := repo.RecordTransaction(ctx, "program_call", tx)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("null-byte memo chain fact should be created")
	}
	if fact.Memo != "TRADE|1780735965119|AAAAAAAAAAA=" {
		t.Fatalf("memo = %q, want sanitized memo", fact.Memo)
	}
	requirePostgresCount(t, db, &models.ChainFact{}, "event_id = ?", fact.EventID, 1)
}

func TestPrepareChainFactFallsBackToBoundedMetadataMemoKeys(t *testing.T) {
	fact, err := BuildChainFact(ChainFactBuildParams{
		EventType:   "native_transfer",
		Transaction: chainFactTestTx(constants.Bitcoin, "metadata-fallback", "vout:0"),
	})
	if err != nil {
		t.Fatal(err)
	}
	fact.Memo = ""
	fact.MemoNormalized = ""
	fact.RawMetadataJSON = `{"memo":"` + strings.Repeat("x", chainFactIndexedMemoMaxRunes+1) + `","payment_id":"checkout-123"}`

	prepared, err := prepareChainFact(&fact)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Memo != "checkout-123" || prepared.MemoNormalized != "checkout-123" {
		t.Fatalf("memo fallback = %q/%q, want checkout-123", prepared.Memo, prepared.MemoNormalized)
	}
}

func TestBuildChainFactRejectsConfirmedObservationWithoutBlock(t *testing.T) {
	tx := chainFactTestTx(constants.Ethereum, "0xconfirmednoblock", "log:0")
	tx.Block = nil
	tx.BlockHash = nil
	tx.Status = strPtr(models.TransactionStatusConfirmed)

	if _, err := BuildChainFact(ChainFactBuildParams{
		EventType:             "native_transfer",
		Transaction:           tx,
		Confirmations:         0,
		ConfirmationsRequired: 12,
	}); !errors.Is(err, ErrChainFactInvalid) {
		t.Fatalf("confirmed no-block err = %v, want ErrChainFactInvalid", err)
	}
}

func TestBuildChainFactFromTransactionRejectsInvalidInput(t *testing.T) {
	tx := chainFactTestTx(constants.Ethereum, "0xabc", "log:1")
	tx.Hash = nil
	if _, err := BuildChainFactFromTransaction("native_transfer", tx); !errors.Is(err, ErrChainFactInvalid) {
		t.Fatalf("missing hash err = %v", err)
	}
	tx = chainFactTestTx(constants.Ethereum, "0xabc", "log:1")
	tx.Block = strPtr("not-a-height")
	if _, err := BuildChainFactFromTransaction("native_transfer", tx); !errors.Is(err, ErrChainFactInvalid) {
		t.Fatalf("bad block err = %v", err)
	}
	tx = chainFactTestTx(constants.Ethereum, "0xabc", "log:1")
	tx.To = nil
	tx.From = nil
	if _, err := BuildChainFactFromTransaction("native_transfer", tx); !errors.Is(err, ErrChainFactInvalid) {
		t.Fatalf("missing observed address err = %v", err)
	}
}

func TestChainFactRepoPostgresUpsertIdempotent(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.ChainFact{}); err != nil {
		t.Fatalf("automigrate chain facts: %v", err)
	}
	repo := NewChainFactRepo(db)
	ctx := context.Background()
	tx := chainFactTestTx(constants.Ethereum, "0xabc", "log:1")

	first, created, err := repo.RecordTransaction(ctx, "native_transfer", tx)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("first chain fact should be created")
	}
	second, created, err := repo.RecordTransaction(ctx, "native_transfer", tx)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("duplicate chain fact should no-op")
	}
	if second.EventID != first.EventID || second.ID != first.ID {
		t.Fatalf("duplicate returned %#v, want existing %#v", second, first)
	}
	requirePostgresCount(t, db, &models.ChainFact{}, "event_id = ?", first.EventID, 1)
}

func TestChainFactRepoRecordOrUpdateAdvancesFinality(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.ChainFact{}); err != nil {
		t.Fatalf("automigrate chain facts: %v", err)
	}
	repo := NewChainFactRepo(db)
	ctx := context.Background()

	tx := chainFactTestTx(constants.TRON, "tron-tx", "tx:0")
	pending := models.TransactionStatusPendingConfirmation
	tx.Status = &pending
	first, created, err := repo.RecordOrUpdate(ctx, mustBuildChainFact(t, ChainFactBuildParams{
		EventType:             "native_transfer",
		Transaction:           tx,
		Confirmations:         0,
		ConfirmationsRequired: 2,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !created || first.Finalized {
		t.Fatalf("first fact created=%v finalized=%v", created, first.Finalized)
	}

	confirmed := models.TransactionStatusConfirmed
	tx.Status = &confirmed
	second, created, err := repo.RecordOrUpdate(ctx, mustBuildChainFact(t, ChainFactBuildParams{
		EventType:             "native_transfer",
		Transaction:           tx,
		Confirmations:         3,
		ConfirmationsRequired: 2,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("duplicate fact should update existing row")
	}
	if second.ID != first.ID || second.Confirmations != 3 || second.ConfirmationsRequired != 2 || !second.Finalized {
		t.Fatalf("updated fact = %#v, want same id with finalized 3/2", second)
	}
	requirePostgresCount(t, db, &models.ChainFact{}, "event_id = ?", first.EventID, 1)
}

func TestChainFactRepoRecordOrUpdateUpgradesZeroAmountPayload(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.ChainFact{}); err != nil {
		t.Fatalf("automigrate chain facts: %v", err)
	}
	repo := NewChainFactRepo(db)
	ctx := context.Background()

	tx := chainFactTestTx(constants.Chiliz, "0xnative", "tx:0")
	tx.Amount = strPtr("0")
	first, created, err := repo.RecordOrUpdate(ctx, mustBuildChainFact(t, ChainFactBuildParams{
		EventType:             "transaction",
		Transaction:           tx,
		Confirmations:         1,
		ConfirmationsRequired: 12,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !created || first.AmountRaw != "0" || first.SourceEventType != "transaction" {
		t.Fatalf("first fact = %#v, want zero transaction", first)
	}

	tx.Amount = strPtr("1000000000000000000")
	second, created, err := repo.RecordOrUpdate(ctx, mustBuildChainFact(t, ChainFactBuildParams{
		EventType:             "native_transfer",
		Transaction:           tx,
		Confirmations:         12,
		ConfirmationsRequired: 12,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("corrected duplicate fact should update existing row")
	}
	if second.ID != first.ID ||
		second.AmountRaw != "1000000000000000000" ||
		second.SourceEventType != "native_transfer" ||
		second.Confirmations != 12 ||
		!second.Finalized {
		t.Fatalf("upgraded fact = %#v, want same id with native transfer amount and finality", second)
	}
	requirePostgresCount(t, db, &models.ChainFact{}, "event_id = ?", first.EventID, 1)
}

func TestChainFactRepoListForDepositProcessingSkipsIgnoredFactsAndKeepsLegacyRematch(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.ChainFact{}, &models.Deposit{}, &models.Wallet{}, &models.WalletAddressLookup{}, &models.MoneyEventOutbox{}); err != nil {
		t.Fatalf("automigrate deposit processing models: %v", err)
	}

	ctx := context.Background()
	repo := NewChainFactRepo(db)

	unowned := testDepositChainFact("1:0xunowned:log:1", true)
	unowned.TxHash = "0xunowned"
	unowned.ObservedAddress = "0xmissing"
	unowned.Status = models.ChainFactStatusObserved
	if err := db.WithContext(ctx).Create(&unowned).Error; err != nil {
		t.Fatalf("create unowned fact: %v", err)
	}
	if err := repo.MarkIgnored(ctx, unowned.EventID, "observed address is not owned by a wallet"); err != nil {
		t.Fatalf("mark unowned fact ignored: %v", err)
	}
	var ignored models.ChainFact
	if err := db.WithContext(ctx).First(&ignored, "event_id = ?", unowned.EventID).Error; err != nil {
		t.Fatalf("find ignored fact: %v", err)
	}
	if ignored.Status != models.ChainFactStatusIgnored || ignored.CorrectionReason == "" {
		t.Fatalf("ignored fact = %#v, want ignored with reason", ignored)
	}

	rematchable := testDepositChainFact("1:0xrematch:log:1", true)
	rematchable.TxHash = "0xrematch"
	rematchable.ObservedAddress = "0xto"
	rematchable.Status = models.ChainFactStatusObserved
	if err := db.WithContext(ctx).Create(&rematchable).Error; err != nil {
		t.Fatalf("create rematchable fact: %v", err)
	}
	rematchableDeposit := legacyUnmatchedDepositFromFact(rematchable)
	if err := db.WithContext(ctx).Create(&rematchableDeposit).Error; err != nil {
		t.Fatalf("create legacy unmatched rematchable deposit: %v", err)
	}
	wallet := testDepositWallet()
	wallet.HDAccountID = 9200
	wallet.HDAddressId = 1
	wallet.TronAddress = "TLa2f6VPqDgRE67v1736s7bJ8Ray5wYjU7"
	seedChainFactWalletOwner(t, db, wallet)
	if err := db.WithContext(ctx).Create(&wallet).Error; err != nil {
		t.Fatalf("create wallet: %v", err)
	}
	if err := NewWalletAddressLookupRepo(db).UpsertWallet(ctx, wallet); err != nil {
		t.Fatalf("upsert wallet lookup: %v", err)
	}

	tronTestnetRematchable := testDepositChainFact("99999997:tron-testnet-rematch:tx:0", true)
	tronTestnetRematchable.ChainID = constants.TRONTestnet
	tronTestnetRematchable.TxHash = "tron-testnet-rematch"
	tronTestnetRematchable.LogIndex = "tx:0"
	tronTestnetRematchable.ObservedAddress = wallet.TronAddress
	tronTestnetRematchable.Status = models.ChainFactStatusObserved
	if err := db.WithContext(ctx).Create(&tronTestnetRematchable).Error; err != nil {
		t.Fatalf("create tron testnet rematchable fact: %v", err)
	}
	tronTestnetRematchableDeposit := legacyUnmatchedDepositFromFact(tronTestnetRematchable)
	if err := db.WithContext(ctx).Create(&tronTestnetRematchableDeposit).Error; err != nil {
		t.Fatalf("create legacy unmatched tron testnet deposit: %v", err)
	}

	newFact := testDepositChainFact("1:0xnew:log:1", true)
	newFact.TxHash = "0xnew"
	newFact.ObservedAddress = "0xnew"
	newFact.Status = models.ChainFactStatusObserved
	if err := db.WithContext(ctx).Create(&newFact).Error; err != nil {
		t.Fatalf("create new fact: %v", err)
	}

	facts, err := repo.ListForDepositProcessing(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, fact := range facts {
		seen[fact.EventID] = true
	}
	if seen[unowned.EventID] {
		t.Fatal("ignored unowned fact must not be reprocessed forever")
	}
	if !seen[rematchable.EventID] {
		t.Fatal("legacy unmatched fact must be selected after its wallet appears")
	}
	if !seen[tronTestnetRematchable.EventID] {
		t.Fatal("legacy tron testnet unmatched fact must be selected after its wallet appears")
	}
	if err := repo.MarkIgnored(ctx, rematchable.EventID, "test owned fact processed"); err != nil {
		t.Fatalf("mark rematchable ignored: %v", err)
	}
	if err := repo.MarkIgnored(ctx, tronTestnetRematchable.EventID, "test owned fact processed"); err != nil {
		t.Fatalf("mark tron testnet rematchable ignored: %v", err)
	}
	facts, err = repo.ListForDepositProcessing(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	seen = map[string]bool{}
	for _, fact := range facts {
		seen[fact.EventID] = true
	}
	if seen[newFact.EventID] {
		t.Fatal("new fact without a wallet owner must not be selected for deposit processing")
	}
}

func TestChainFactRepoListForDepositProcessingPrioritizesOwnedWalletFacts(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.ChainFact{}, &models.Deposit{}, &models.Wallet{}, &models.WalletAddressLookup{}, &models.MoneyEventOutbox{}); err != nil {
		t.Fatalf("automigrate deposit processing models: %v", err)
	}

	ctx := context.Background()
	repo := NewChainFactRepo(db)
	wallet := testDepositWallet()
	wallet.HDAccountID = 9201
	wallet.HDAddressId = 1
	wallet.EthereumAddress = "0xAaBbCc0000000000000000000000000000000001"
	seedChainFactWalletOwner(t, db, wallet)
	if err := db.WithContext(ctx).Create(&wallet).Error; err != nil {
		t.Fatalf("create wallet: %v", err)
	}
	if err := NewWalletAddressLookupRepo(db).UpsertWallet(ctx, wallet); err != nil {
		t.Fatalf("upsert wallet lookup: %v", err)
	}

	now := time.Now()
	for i := 0; i < 5; i++ {
		unowned := testDepositChainFact(fmt.Sprintf("1:0xunowned-priority-%d:log:1", i), true)
		unowned.TxHash = fmt.Sprintf("0xunowned-priority-%d", i)
		unowned.ObservedAddress = fmt.Sprintf("0xmissing%d", i)
		unowned.CreatedAt = now.Add(time.Duration(i) * time.Second)
		unowned.UpdatedAt = unowned.CreatedAt
		if err := db.WithContext(ctx).Create(&unowned).Error; err != nil {
			t.Fatalf("create unowned fact %d: %v", i, err)
		}
	}

	owned := testDepositChainFact("1:0xowned-priority:log:1", true)
	owned.TxHash = "0xowned-priority"
	owned.ObservedAddress = strings.ToLower(wallet.EthereumAddress)
	owned.CreatedAt = now.Add(-time.Hour)
	owned.UpdatedAt = owned.CreatedAt
	if err := db.WithContext(ctx).Create(&owned).Error; err != nil {
		t.Fatalf("create owned fact: %v", err)
	}

	facts, err := repo.ListForDepositProcessing(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].EventID != owned.EventID {
		t.Fatalf("first processing fact = %#v, want owned wallet fact %s", facts, owned.EventID)
	}
}

func seedChainFactWalletOwner(t *testing.T, db *gorm.DB, wallet models.Wallet) {
	t.Helper()
	merchant := models.Merchant{
		ID:       wallet.MerchantID,
		Name:     "Chain Fact Test Merchant",
		Email:    "chain-fact-" + wallet.MerchantID.String() + "@example.test",
		IsActive: true,
	}
	domain := models.Domain{
		ID:          wallet.DomainID,
		MerchantID:  wallet.MerchantID,
		DomainURL:   "chain-fact-" + wallet.DomainID.String() + ".example.test",
		APIKey:      "pk_" + wallet.DomainID.String(),
		APISecret:   "secret",
		HDAccountID: wallet.HDAccountID,
	}
	if err := db.Create(&merchant).Error; err != nil {
		t.Fatalf("create wallet merchant: %v", err)
	}
	if err := db.Create(&domain).Error; err != nil {
		t.Fatalf("create wallet domain: %v", err)
	}
}

func legacyUnmatchedDepositFromFact(fact models.ChainFact) models.Deposit {
	now := time.Now()
	detectedAt := fact.CreatedAt
	if detectedAt.IsZero() {
		detectedAt = now
	}
	return models.Deposit{
		ID:                    uuid.New(),
		ChainFactID:           fact.ID,
		ChainFactEventID:      fact.EventID,
		Status:                models.DepositStatusUnmatched,
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
		TransactionUniqueHash: depositTransactionUniqueHash(fact),
		SourceEventType:       fact.SourceEventType,
		UnmatchedReason:       "legacy unmatched deposit",
		DetectedAt:            detectedAt,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
}

func chainFactTestTx(chainID constants.ChainID, txHash, logIndex string) types.TransactionParam {
	status := models.TransactionStatusConfirmed
	return types.TransactionParam{
		ChainID:   chainID,
		Hash:      strPtr(txHash),
		Block:     strPtr("123"),
		BlockHash: strPtr("0xblock"),
		From:      strPtr("0xfrom"),
		To:        strPtr("0xto"),
		Symbol:    strPtr("ETH"),
		Decimals:  18,
		Amount:    strPtr("100"),
		LogIndex:  strPtr(logIndex),
		Status:    &status,
	}
}

func strPtr(value string) *string {
	return &value
}

func mustBuildChainFact(t *testing.T, params ChainFactBuildParams) *models.ChainFact {
	t.Helper()
	fact, err := BuildChainFact(params)
	if err != nil {
		t.Fatal(err)
	}
	return &fact
}
