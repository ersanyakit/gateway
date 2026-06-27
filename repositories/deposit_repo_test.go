package repositories

import (
	"context"
	"testing"
	"time"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
)

func TestDepositRepoConsumesMatchedChainFactIdempotently(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Deposit{}, &models.ChainFact{}, &models.MoneyEventOutbox{}); err != nil {
		t.Fatalf("automigrate deposits: %v", err)
	}

	repo := NewDepositRepo(db)
	wallet := testDepositWallet()
	fact := testDepositChainFact("1:0xabc:log:1", false)

	first, created, err := repo.ConsumeChainFact(context.Background(), fact, &wallet)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("first matched deposit should be created")
	}
	if first.Status != models.DepositStatusConfirming {
		t.Fatalf("status = %q, want confirming", first.Status)
	}
	if first.WalletID == nil || *first.WalletID != wallet.ID || first.MerchantID == nil || first.DomainID == nil {
		t.Fatalf("matched scope not stored: %#v", first)
	}
	if first.FinalizedAt != nil {
		t.Fatalf("pending deposit finalized at %v", first.FinalizedAt)
	}

	second, created, err := repo.ConsumeChainFact(context.Background(), fact, &wallet)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("duplicate chain fact should not create a second deposit")
	}
	if second.ID != first.ID || second.ChainFactEventID != first.ChainFactEventID {
		t.Fatalf("duplicate returned %#v, want existing %#v", second, first)
	}
	requirePostgresCount(t, db, &models.Deposit{}, "chain_fact_event_id = ?", first.ChainFactEventID, 1)
	requirePostgresCount(t, db, &models.MoneyEventOutbox{}, "event_type = ?", "deposit.finalized.v1", 0)
}

func TestDepositRepoRecordsUnmatchedWithoutTenantScope(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Deposit{}, &models.ChainFact{}, &models.MoneyEventOutbox{}); err != nil {
		t.Fatalf("automigrate deposits: %v", err)
	}

	repo := NewDepositRepo(db)
	deposit, created, err := repo.ConsumeChainFact(context.Background(), testDepositChainFact("1:0xunmatched:log:7", false), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("first unmatched deposit should be created")
	}
	if deposit.Status != models.DepositStatusUnmatched {
		t.Fatalf("status = %q, want unmatched", deposit.Status)
	}
	if deposit.WalletID != nil || deposit.MerchantID != nil || deposit.DomainID != nil {
		t.Fatalf("unmatched deposit leaked tenant scope: %#v", deposit)
	}
	if deposit.UnmatchedReason == "" {
		t.Fatal("unmatched reason should be stored for reconciliation")
	}
	requirePostgresCount(t, db, &models.MoneyEventOutbox{}, "event_type = ?", "deposit.finalized.v1", 0)
}

func TestDepositRepoFinalizedDepositEmitsOutboxOnce(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Deposit{}, &models.ChainFact{}, &models.MoneyEventOutbox{}); err != nil {
		t.Fatalf("automigrate deposits: %v", err)
	}

	repo := NewDepositRepo(db)
	wallet := testDepositWallet()
	fact := testDepositChainFact("1:0xfinal:log:2", true)

	first, created, err := repo.ConsumeChainFact(context.Background(), fact, &wallet)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("first finalized deposit should be created")
	}
	if first.Status != models.DepositStatusFinalized || first.FinalizedAt == nil {
		t.Fatalf("finality state = %#v", first)
	}

	second, created, err := repo.ConsumeChainFact(context.Background(), fact, &wallet)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("duplicate finalized fact should not create a second deposit")
	}
	if second.ID != first.ID {
		t.Fatalf("duplicate returned %#v, want existing %#v", second, first)
	}
	requirePostgresCount(t, db, &models.Deposit{}, "chain_fact_event_id = ?", first.ChainFactEventID, 1)
	requirePostgresCount(t, db, &models.MoneyEventOutbox{}, "event_type = ?", "deposit.finalized.v1", 1)
}

func testDepositWallet() models.Wallet {
	return models.Wallet{
		ID:              uuid.New(),
		MerchantID:      uuid.New(),
		DomainID:        uuid.New(),
		ProductID:       "wallet:user",
		UserID:          "user-1",
		EthereumAddress: "0xto",
	}
}

func testDepositChainFact(eventID string, finalized bool) models.ChainFact {
	now := time.Now().Add(-time.Minute)
	confirmations := uint(3)
	required := uint(12)
	if finalized {
		confirmations = 12
	}
	return models.ChainFact{
		ID:                    uuid.New(),
		EventID:               eventID,
		ChainID:               constants.Ethereum,
		BlockNumber:           123,
		BlockHash:             "0xblock",
		TxHash:                "0xabc",
		LogIndex:              "log:1",
		ObservedAddress:       "0xto",
		Direction:             models.ChainFactDirectionTo,
		Symbol:                "ETH",
		Decimals:              18,
		AmountRaw:             "100",
		Confirmations:         confirmations,
		ConfirmationsRequired: required,
		Finalized:             finalized,
		SourceEventType:       "native_transfer",
		RawMetadataJSON:       `{}`,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
}
