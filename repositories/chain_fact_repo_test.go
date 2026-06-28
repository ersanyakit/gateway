package repositories

import (
	"context"
	"errors"
	"testing"

	"core/constants"
	"core/models"
	"core/types"
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

func TestChainFactRepoListForDepositProcessingSkipsUnownedUnmatchedFacts(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.ChainFact{}, &models.Deposit{}, &models.Wallet{}, &models.MoneyEventOutbox{}); err != nil {
		t.Fatalf("automigrate deposit processing models: %v", err)
	}

	ctx := context.Background()
	repo := NewChainFactRepo(db)
	depositRepo := NewDepositRepo(db)

	unowned := testDepositChainFact("1:0xunowned:log:1", true)
	unowned.TxHash = "0xunowned"
	unowned.ObservedAddress = "0xmissing"
	unowned.Status = models.ChainFactStatusObserved
	if err := db.WithContext(ctx).Create(&unowned).Error; err != nil {
		t.Fatalf("create unowned fact: %v", err)
	}
	if _, _, err := depositRepo.ConsumeChainFact(ctx, unowned, nil); err != nil {
		t.Fatalf("consume unowned fact: %v", err)
	}

	rematchable := testDepositChainFact("1:0xrematch:log:1", true)
	rematchable.TxHash = "0xrematch"
	rematchable.ObservedAddress = "0xto"
	rematchable.Status = models.ChainFactStatusObserved
	if err := db.WithContext(ctx).Create(&rematchable).Error; err != nil {
		t.Fatalf("create rematchable fact: %v", err)
	}
	if _, _, err := depositRepo.ConsumeChainFact(ctx, rematchable, nil); err != nil {
		t.Fatalf("consume rematchable fact: %v", err)
	}
	wallet := testDepositWallet()
	wallet.TronAddress = "TLa2f6VPqDgRE67v1736s7bJ8Ray5wYjU7"
	if err := db.WithContext(ctx).Create(&wallet).Error; err != nil {
		t.Fatalf("create wallet: %v", err)
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
	if _, _, err := depositRepo.ConsumeChainFact(ctx, tronTestnetRematchable, nil); err != nil {
		t.Fatalf("consume tron testnet rematchable fact: %v", err)
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
		t.Fatal("unowned unmatched fact must not be reprocessed forever")
	}
	if !seen[rematchable.EventID] {
		t.Fatal("unmatched fact must be selected after its wallet appears")
	}
	if !seen[tronTestnetRematchable.EventID] {
		t.Fatal("tron testnet unmatched fact must be selected after its wallet appears")
	}
	if !seen[newFact.EventID] {
		t.Fatal("new fact without a deposit must still be selected")
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
