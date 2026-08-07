package repositories

import (
	"context"
	"errors"
	"sync"
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

func TestDepositRepoRejectsChainFactWithoutWallet(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Deposit{}, &models.ChainFact{}, &models.MoneyEventOutbox{}); err != nil {
		t.Fatalf("automigrate deposits: %v", err)
	}

	repo := NewDepositRepo(db)
	deposit, created, err := repo.ConsumeChainFact(context.Background(), testDepositChainFact("1:0xunmatched:log:7", false), nil)
	if !errors.Is(err, ErrDepositInvalid) {
		t.Fatalf("error = %v, want ErrDepositInvalid", err)
	}
	if deposit != nil || created {
		t.Fatalf("deposit=%#v created=%v, want no insert", deposit, created)
	}
	requirePostgresCount(t, db, &models.Deposit{}, "chain_fact_event_id = ?", "1:0xunmatched:log:7", 0)
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

func TestDepositRepoConcurrentFinalizedFactCreatesDepositAndOutboxOnce(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Deposit{}, &models.ChainFact{}, &models.MoneyEventOutbox{}); err != nil {
		t.Fatalf("automigrate deposits: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(8)

	repo := NewDepositRepo(db)
	wallet := testDepositWallet()
	fact := testDepositChainFact("1:0xconcurrent-final:log:2", true)
	fact.TxHash = "0xconcurrent-final"
	fact.LogIndex = "log:2"

	const writers = 8
	start := make(chan struct{})
	type result struct {
		deposit *models.Deposit
		created bool
		err     error
	}
	results := make(chan result, writers)
	var wg sync.WaitGroup
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			deposit, created, err := repo.ConsumeChainFact(context.Background(), fact, &wallet)
			results <- result{deposit: deposit, created: created, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	created := 0
	var persistedID uuid.UUID
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent consume: %v", result.err)
		}
		if result.deposit == nil {
			t.Fatal("concurrent consume returned nil deposit")
		}
		if result.created {
			created++
		}
		if persistedID == uuid.Nil {
			persistedID = result.deposit.ID
		} else if result.deposit.ID != persistedID {
			t.Fatalf("deposit id = %s, want winning id %s", result.deposit.ID, persistedID)
		}
	}
	if created != 1 {
		t.Fatalf("created count = %d, want exactly one", created)
	}
	requirePostgresCount(t, db, &models.Deposit{}, "chain_fact_event_id = ?", fact.EventID, 1)
	requirePostgresCount(t, db, &models.MoneyEventOutbox{}, "event_type = ?", "deposit.finalized.v1", 1)
}

func TestDepositRepoCarriesObservationAndMemoMetadata(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Deposit{}, &models.ChainFact{}, &models.MoneyEventOutbox{}); err != nil {
		t.Fatalf("automigrate deposits: %v", err)
	}

	repo := NewDepositRepo(db)
	wallet := testDepositWallet()
	fact := testDepositChainFact("1:0xmempool:log:0", false)
	fact.TxHash = "0xmempool"
	fact.BlockNumber = 0
	fact.BlockHash = ""
	fact.Confirmations = 0
	fact.ObservationStatus = models.ChainFactObservationMempool
	fact.Memo = "ORDER-42 "
	fact.MemoNormalized = "order-42"

	deposit, created, err := repo.ConsumeChainFact(context.Background(), fact, &wallet)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("first mempool deposit should be created")
	}
	if deposit.ObservationStatus != models.DepositObservationMempool || deposit.Status != models.DepositStatusPending {
		t.Fatalf("deposit observation/status = %q/%q", deposit.ObservationStatus, deposit.Status)
	}
	if deposit.Memo != "ORDER-42" || deposit.MemoNormalized != "order-42" || deposit.MemoStatus != models.DepositMemoStatusPresent {
		t.Fatalf("deposit memo fields = %#v", deposit)
	}
	requirePostgresCount(t, db, &models.MoneyEventOutbox{}, "event_type = ?", "deposit.finalized.v1", 0)
}

func TestBuildDepositAllowsOnlyMempoolWithoutBlock(t *testing.T) {
	wallet := testDepositWallet()
	mempool := testDepositChainFact("1:0xmempool-unit:log:0", false)
	mempool.TxHash = "0xmempool-unit"
	mempool.BlockNumber = 0
	mempool.BlockHash = ""
	mempool.Confirmations = 0
	mempool.ObservationStatus = models.ChainFactObservationMempool

	deposit, err := buildDepositFromChainFact(mempool, &wallet)
	if err != nil {
		t.Fatalf("mempool deposit build: %v", err)
	}
	if deposit.Status != models.DepositStatusPending || deposit.ObservationStatus != models.DepositObservationMempool {
		t.Fatalf("mempool deposit = %#v", deposit)
	}

	confirmed := mempool
	confirmed.EventID = "1:0xconfirmed-no-block:log:0"
	confirmed.TxHash = "0xconfirmed-no-block"
	confirmed.ObservationStatus = models.ChainFactObservationConfirmed
	if _, err := buildDepositFromChainFact(confirmed, &wallet); !errors.Is(err, ErrDepositInvalid) {
		t.Fatalf("confirmed blockless deposit err = %v, want ErrDepositInvalid", err)
	}
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
