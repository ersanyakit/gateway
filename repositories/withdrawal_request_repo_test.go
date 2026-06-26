package repositories

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestWithdrawalWalletChainLockKeyNormalizesChain(t *testing.T) {
	walletID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	got := withdrawalWalletChainLockKey(walletID, " Ethereum ")
	want := "withdrawal-wallet-chain:11111111-1111-1111-1111-111111111111:ethereum"
	if got != want {
		t.Fatalf("lock key = %q, want %q", got, want)
	}
}

func TestWithdrawalWalletBusyErrorIsComparable(t *testing.T) {
	err := errors.Join(ErrWithdrawalWalletBusy)
	if !errors.Is(err, ErrWithdrawalWalletBusy) {
		t.Fatal("withdrawal wallet busy sentinel should be comparable")
	}
}
