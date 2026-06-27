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
