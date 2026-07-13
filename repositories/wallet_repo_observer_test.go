package repositories

import (
	"testing"

	"core/models"

	"github.com/google/uuid"
)

func TestWalletRepoAddressObserverRunsSynchronously(t *testing.T) {
	repo := NewWalletRepo(nil)
	wallet := models.Wallet{ID: uuid.New(), EthereumAddress: "0xowned"}
	var observed models.Wallet
	repo.SetAddressObserver(func(got models.Wallet) {
		observed = got
	})

	repo.notifyAddressObserver(wallet)
	if observed.ID != wallet.ID || observed.EthereumAddress != wallet.EthereumAddress {
		t.Fatalf("observed wallet = %#v, want %#v", observed, wallet)
	}
}
