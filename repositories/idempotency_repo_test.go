package repositories

import (
	"context"
	"testing"
	"time"

	"core/models"
	"core/types"

	"github.com/google/uuid"
)

func TestIdempotencyRequestHashIncludesSelectedAssetFields(t *testing.T) {
	orderID := "order-1"
	amount := "10"
	currency := "USD"
	chainID := int64(1)
	symbol := "USDT"
	tokenA := "0x1111111111111111111111111111111111111111"
	tokenB := "0x2222222222222222222222222222222222222222"

	paramsA := types.PaymentCreateParams{
		OrderID:  &orderID,
		Amount:   &amount,
		Currency: &currency,
		ChainID:  &chainID,
		Symbol:   &symbol,
		Token:    &tokenA,
	}
	paramsB := types.PaymentCreateParams{
		OrderID:  &orderID,
		Amount:   &amount,
		Currency: &currency,
		ChainID:  &chainID,
		Symbol:   &symbol,
		Token:    &tokenB,
	}
	if err := paramsA.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := paramsB.Validate(); err != nil {
		t.Fatal(err)
	}

	repo := &IdempotencyRepo{}
	hashA, err := repo.RequestHash(paramsA)
	if err != nil {
		t.Fatal(err)
	}
	hashB, err := repo.RequestHash(paramsB)
	if err != nil {
		t.Fatal(err)
	}
	if hashA == hashB {
		t.Fatal("request hash should change when selected token changes")
	}
}

func TestIdempotencyRequestHashIsStableForSameNormalizedPayload(t *testing.T) {
	orderID := " order-1 "
	amount := "10"
	currency := " usd "
	chainID := int64(1)
	symbol := " usdt "
	token := " 0x1111111111111111111111111111111111111111 "

	paramsA := types.PaymentCreateParams{
		OrderID:  &orderID,
		Amount:   &amount,
		Currency: &currency,
		ChainID:  &chainID,
		Symbol:   &symbol,
		Token:    &token,
	}
	paramsB := types.PaymentCreateParams{
		OrderID:  &orderID,
		Amount:   &amount,
		Currency: &currency,
		ChainID:  &chainID,
		Symbol:   &symbol,
		Token:    &token,
	}
	if err := paramsA.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := paramsB.Validate(); err != nil {
		t.Fatal(err)
	}

	repo := &IdempotencyRepo{}
	hashA, err := repo.RequestHash(paramsA)
	if err != nil {
		t.Fatal(err)
	}
	hashB, err := repo.RequestHash(paramsB)
	if err != nil {
		t.Fatal(err)
	}
	if hashA != hashB {
		t.Fatalf("same normalized payload hash mismatch: %s != %s", hashA, hashB)
	}
}

func TestIdempotencyRepoCompleteResourceStoresGenericReference(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.IdempotencyKey{}); err != nil {
		t.Fatalf("automigrate idempotency keys: %v", err)
	}
	ctx := context.Background()
	repo := NewIdempotencyRepo(db)
	domainID := uuid.New()
	merchantID := uuid.New()
	record, shouldCreate, err := repo.Begin(ctx, domainID, merchantID, "outbound-key", "hash-a", time.Hour)
	if err != nil {
		t.Fatalf("begin idempotency: %v", err)
	}
	if !shouldCreate {
		t.Fatal("new idempotency key should create")
	}
	resourceID := uuid.New()
	if err := repo.CompleteResource(ctx, record.ID, "payout", resourceID, `{"result":"ok"}`); err != nil {
		t.Fatalf("complete resource: %v", err)
	}
	replayed, shouldCreate, err := repo.Begin(ctx, domainID, merchantID, "outbound-key", "hash-a", time.Hour)
	if err != nil {
		t.Fatalf("replay begin: %v", err)
	}
	if shouldCreate {
		t.Fatal("completed idempotency key should replay instead of creating")
	}
	if replayed.ResourceType != "payout" || replayed.ResourceID == nil || *replayed.ResourceID != resourceID || replayed.PaymentSessionID != nil || replayed.ResponseBody == "" {
		t.Fatalf("generic resource reference not preserved: %#v", replayed)
	}
}
