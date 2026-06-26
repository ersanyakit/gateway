package repositories

import (
	"testing"

	"core/types"
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
