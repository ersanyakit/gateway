package blockchain

import (
	"errors"
	"strings"
	"testing"

	"core/blockchain/walletcore"
	"core/constants"
	"core/services/signer"
)

func TestBaseChainRPCsMergesEnvAndDefaults(t *testing.T) {
	t.Setenv("ETHEREUM_RPC_URLS", "https://env-1.example, https://env-2.example")
	t.Setenv("CHAIN_1_RPC_URLS", "https://env-2.example,https://chain-id.example")
	chain := BaseChain{
		ID:        constants.Ethereum,
		ChainName: "ethereum",
		RPCHttp:   []string{"https://default.example", "https://env-1.example"},
	}

	got := chain.RPCs()
	want := []string{"https://env-1.example", "https://env-2.example", "https://chain-id.example", "https://default.example"}
	if len(got) != len(want) {
		t.Fatalf("RPCs len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("RPCs[%d] = %q, want %q; all=%#v", i, got[i], want[i], got)
		}
	}
}

func TestBaseChainDerivedPath(t *testing.T) {
	chain := BaseChain{}
	if got := chain.GetDerivedPath(44, 60, 1, 0, 7); got != "m/44'/60'/1'/0/7" {
		t.Fatalf("derived path = %q", got)
	}
}

func TestBaseChainGetDerivedWalletAddsSignerContext(t *testing.T) {
	chain := BaseChain{ID: constants.Ethereum}
	mnemonic, err := walletcore.GenerateMnemonic(128)
	if err != nil {
		t.Fatal(err)
	}
	path := "m/44'/60'/3'/0/5"

	wallet, err := chain.GetDerivedWallet(mnemonic, path)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.DerivationPath != path {
		t.Fatalf("derivation path = %q, want %q", wallet.DerivationPath, path)
	}
	if !strings.Contains(wallet.KeyReference, "chain:1") || !strings.Contains(wallet.KeyReference, path) {
		t.Fatalf("key reference = %q, want chain and path context", wallet.KeyReference)
	}
	if wallet.SignerMode != signer.ModeSoftware {
		t.Fatalf("signer mode = %q, want %q", wallet.SignerMode, signer.ModeSoftware)
	}
}

func TestBaseChainGetMnemonicBlocksProductionSoftwareSignerOverride(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("SIGNER_MODE", "software")
	t.Setenv("ALLOW_SOFTWARE_SIGNER_IN_PRODUCTION", "true")
	t.Setenv("MNEMONIC_PHRASE", "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about")

	chain := BaseChain{ID: constants.Ethereum}
	_, err := chain.GetMnemonic()
	if !errors.Is(err, signer.ErrProductionSoftwareSignerDisabled) {
		t.Fatalf("GetMnemonic err=%v, want ErrProductionSoftwareSignerDisabled", err)
	}
}

func TestBaseChainWorkerCount(t *testing.T) {
	chain := BaseChain{}
	worker := &baseChainTestWorker{events: make(chan interface{})}

	if got := chain.WorkerCount(); got != 0 {
		t.Fatalf("initial worker count = %d, want 0", got)
	}
	if err := chain.AddWorker(worker); err != nil {
		t.Fatal(err)
	}
	if got := chain.WorkerCount(); got != 1 {
		t.Fatalf("worker count after add = %d, want 1", got)
	}
	if err := chain.RemoveWorker(worker); err != nil {
		t.Fatal(err)
	}
	if got := chain.WorkerCount(); got != 0 {
		t.Fatalf("worker count after remove = %d, want 0", got)
	}
	if err := chain.RemoveWorker(worker); err == nil {
		t.Fatal("expected missing listener error")
	}
}

type baseChainTestWorker struct {
	events chan interface{}
}

func (w *baseChainTestWorker) Start() error { return nil }

func (w *baseChainTestWorker) Stop() error { return nil }

func (w *baseChainTestWorker) Events() <-chan interface{} { return w.events }
