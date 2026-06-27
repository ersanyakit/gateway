package repositories

import (
	"strings"
	"testing"

	"core/constants"
	"core/helpers"
	"core/models"
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
