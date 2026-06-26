package repositories

import (
	"testing"

	"core/constants"
	"core/helpers"
	"core/types"
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

func TestTransactionUniqueHashAllowsEmptyLogIndex(t *testing.T) {
	repo := NewTransactionRepo(nil)
	unique, err := repo.UniqueHash(types.TransactionParam{
		ChainID: constants.Bitcoin,
		Hash:    helpers.StrPtr("btc-hash"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if unique != "0-btc-hash-" {
		t.Fatalf("unique hash = %q", unique)
	}
}

func TestTransactionUniqueHashRequiresHash(t *testing.T) {
	repo := NewTransactionRepo(nil)
	if _, err := repo.UniqueHash(types.TransactionParam{ChainID: constants.Ethereum}); err == nil {
		t.Fatal("missing hash should fail")
	}
}
