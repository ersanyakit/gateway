package blockchain

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"core/blockchain/walletcore"
	"core/constants"
	"core/helpers"
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

func TestBaseChainRPCsUsesOptionalRanker(t *testing.T) {
	t.Cleanup(func() { SetRPCURLRanker(nil) })
	chain := BaseChain{
		ID:        constants.Ethereum,
		ChainName: "ethereum",
		RPCHttp:   []string{"https://primary", "https://fallback"},
	}
	SetRPCURLRanker(func(chainID constants.ChainID, chainName string, urls []string) []string {
		if chainID != constants.Ethereum || chainName != "ethereum" {
			t.Fatalf("ranker chain = %d/%s, want ethereum", chainID, chainName)
		}
		return []string{urls[1], urls[0]}
	})
	got := chain.RPCs()
	if len(got) != 2 || got[0] != "https://fallback" || got[1] != "https://primary" {
		t.Fatalf("ranked RPCs = %#v", got)
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
		if strings.Contains(err.Error(), "walletcorefallback") {
			t.Skip(err)
		}
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

func TestWalletDetailsJSONOmitsSecretMaterial(t *testing.T) {
	body, err := json.Marshal(WalletDetails{
		Address:        "0xabc",
		PrivateKey:     "private-secret",
		MnemonicPhrase: "seed secret words",
		KeyReference:   "chain:1:path:m/44'/60'/0'/0/1",
		DerivationPath: "m/44'/60'/0'/0/1",
		SignerMode:     signer.ModeSoftware,
	})
	if err != nil {
		t.Fatal(err)
	}
	out := string(body)
	if strings.Contains(out, "private-secret") || strings.Contains(out, "seed secret words") || strings.Contains(out, "PrivateKey") || strings.Contains(out, "MnemonicPhrase") {
		t.Fatalf("WalletDetails JSON leaked secret material: %s", out)
	}
	if !strings.Contains(out, "key_reference") || !strings.Contains(out, "derivation_path") {
		t.Fatalf("WalletDetails JSON missing non-secret custody metadata: %s", out)
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

func TestBaseChainBlocksProductionSecretMaterialHelpers(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("SIGNER_MODE", "vault")
	restore := signer.RegisterCustodyAdapter(baseChainTestCustodyAdapter{})
	defer restore()

	chain := BaseChain{ID: constants.Ethereum, ChainName: "ethereum"}
	if _, err := chain.GenerateMnemonicPhrase(); !errors.Is(err, signer.ErrProductionSecretMaterialAccessDisabled) {
		t.Fatalf("GenerateMnemonicPhrase err=%v, want ErrProductionSecretMaterialAccessDisabled", err)
	}
	if _, err := chain.GetMnemonicForPath(context.Background(), "m/44'/60'/0'/0/1"); !errors.Is(err, signer.ErrProductionSecretMaterialAccessDisabled) {
		t.Fatalf("GetMnemonicForPath err=%v, want ErrProductionSecretMaterialAccessDisabled", err)
	}
	if _, err := chain.GetDerivedPrivateKey("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", "m/44'/60'/0'/0/1"); !errors.Is(err, signer.ErrProductionSecretMaterialAccessDisabled) {
		t.Fatalf("GetDerivedPrivateKey err=%v, want ErrProductionSecretMaterialAccessDisabled", err)
	}
	if _, err := chain.GetDerivedWallet("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", "m/44'/60'/0'/0/1"); !errors.Is(err, signer.ErrProductionSecretMaterialAccessDisabled) {
		t.Fatalf("GetDerivedWallet err=%v, want ErrProductionSecretMaterialAccessDisabled", err)
	}
}

func TestBaseChainProductionWatchOnlyWalletUsesCustodyAdapter(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("SIGNER_MODE", "vault")
	restore := signer.RegisterCustodyAdapter(baseChainTestCustodyAdapter{})
	defer restore()

	chain := BaseChain{ID: constants.Ethereum, ChainName: "ethereum"}
	wallet, err := chain.CreateHDWalletFromPath(context.Background(), "m/44'/60'/0'/0/7")
	if err != nil {
		t.Fatal(err)
	}
	if wallet.Address != "0x1111111111111111111111111111111111111111" {
		t.Fatalf("address = %q, want adapter address", wallet.Address)
	}
	if wallet.PrivateKey != "" || wallet.MnemonicPhrase != "" {
		t.Fatalf("watch-only wallet contains secret material: %#v", wallet)
	}
	if !wallet.WatchOnly || wallet.SignerMode != "vault" || wallet.CustodyProvider != "vault-primary" {
		t.Fatalf("wallet metadata = %#v, want watch-only vault metadata", wallet)
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

func TestBaseChainRejectsNilWorkers(t *testing.T) {
	chain := &BaseChain{}
	if err := chain.AddWorker(nil); !errors.Is(err, ErrNilWorker) {
		t.Fatalf("AddWorker(nil) error = %v, want ErrNilWorker", err)
	}
	var typedNil *baseChainTestWorker
	if err := chain.AddWorker(typedNil); !errors.Is(err, ErrNilWorker) {
		t.Fatalf("AddWorker(typed nil) error = %v, want ErrNilWorker", err)
	}

	chain.Workers = []Worker{nil}
	if err := chain.StartWorkers(context.Background()); !errors.Is(err, ErrNilWorker) {
		t.Fatalf("StartWorkers error = %v, want ErrNilWorker", err)
	}
}

func TestBaseChainStartWorkersAcceptsNilContext(t *testing.T) {
	chain := &BaseChain{ChainName: "test"}
	worker := &baseChainTestWorker{events: make(chan interface{})}
	if err := chain.AddWorker(worker); err != nil {
		t.Fatal(err)
	}
	if err := chain.StartWorkers(nil); err != nil {
		t.Fatalf("StartWorkers(nil) error = %v", err)
	}
	if chain.ctx == nil {
		t.Fatal("StartWorkers(nil) did not install a background context")
	}
	if err := chain.StopWorkers(); err != nil {
		t.Fatal(err)
	}
}

func TestBaseChainStartWorkersReturnsRecoveredWorkerPanic(t *testing.T) {
	chain := &BaseChain{ChainName: "test"}
	worker := &baseChainTestWorker{events: make(chan interface{}), panicOnStart: true}
	if err := chain.AddWorker(worker); err != nil {
		t.Fatal(err)
	}
	err := chain.StartWorkers(context.Background())
	var recovered *helpers.RecoveredPanicError
	if !errors.As(err, &recovered) {
		t.Fatalf("StartWorkers error = %T %v, want RecoveredPanicError", err, err)
	}
}

type baseChainTestWorker struct {
	events       chan interface{}
	panicOnStart bool
}

func (w *baseChainTestWorker) Start() error {
	if w.panicOnStart {
		panic("worker start failed")
	}
	return nil
}

func (w *baseChainTestWorker) Stop() error { return nil }

func (w *baseChainTestWorker) Events() <-chan interface{} { return w.events }

type baseChainTestCustodyAdapter struct{}

func (baseChainTestCustodyAdapter) DeriveAddress(context.Context, signer.DeriveAddressRequest) (signer.DeriveAddressResponse, error) {
	return signer.DeriveAddressResponse{
		Address:         "0x1111111111111111111111111111111111111111",
		KeyReference:    "vault:key:ethereum-hot",
		SignerMode:      "vault",
		CustodyProvider: "vault-primary",
	}, nil
}

func (baseChainTestCustodyAdapter) SignTransaction(context.Context, signer.SignTransactionRequest) (signer.SignTransactionResponse, error) {
	return signer.SignTransactionResponse{}, nil
}

func (baseChainTestCustodyAdapter) SignMessage(context.Context, signer.SignMessageRequest) (signer.SignMessageResponse, error) {
	return signer.SignMessageResponse{}, nil
}

func (baseChainTestCustodyAdapter) KeyReference(context.Context, signer.DeriveAddressRequest) (string, error) {
	return "vault:key:ref", nil
}

func (baseChainTestCustodyAdapter) Health(context.Context) signer.AdapterHealth {
	return signer.AdapterHealth{Ready: true, Mode: "vault", Provider: "vault-primary"}
}
