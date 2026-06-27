package listeners

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"core/blockchain"
	"core/constants"
	"core/models"
)

type configTestChain struct {
	id   constants.ChainID
	name string
}

func (c configTestChain) ChainID() constants.ChainID { return c.id }
func (c configTestChain) Name() string               { return c.name }
func (c configTestChain) WSS() []string              { return nil }
func (c configTestChain) RPCs() []string             { return nil }
func (c configTestChain) Create(context.Context) (*blockchain.WalletDetails, error) {
	return nil, errors.New("not used")
}
func (c configTestChain) CreateHDWallet(context.Context, int, int) (*blockchain.WalletDetails, error) {
	return nil, errors.New("not used")
}
func (c configTestChain) Deposit(context.Context, blockchain.WalletDetails, string, string) (*blockchain.TransactionResult, error) {
	return nil, errors.New("not used")
}
func (c configTestChain) Withdraw(context.Context, blockchain.WalletDetails, string, string) (*blockchain.TransactionResult, error) {
	return nil, errors.New("not used")
}
func (c configTestChain) WithdrawToken(context.Context, blockchain.WalletDetails, string, string, string) (*blockchain.TransactionResult, error) {
	return nil, errors.New("not used")
}
func (c configTestChain) Sweep(context.Context, blockchain.WalletDetails) (*blockchain.TransactionResult, error) {
	return nil, errors.New("not used")
}
func (c configTestChain) SweepTo(context.Context, blockchain.WalletDetails, string) (*blockchain.TransactionResult, error) {
	return nil, errors.New("not used")
}
func (c configTestChain) SweepERC20To(context.Context, blockchain.WalletDetails, string, string) (*blockchain.TransactionResult, error) {
	return nil, errors.New("not used")
}
func (c configTestChain) PrefundGas(context.Context, blockchain.WalletDetails, string) (bool, error) {
	return false, errors.New("not used")
}
func (c configTestChain) ValidateAddress(string) bool { return false }
func (c configTestChain) AddWorker(blockchain.Worker) error {
	return errors.New("not used")
}
func (c configTestChain) RemoveWorker(blockchain.Worker) error {
	return errors.New("not used")
}
func (c configTestChain) WorkerCount() int { return 0 }
func (c configTestChain) BatchBalances(context.Context, []string, int) []models.BalanceResult {
	return nil
}
func (c configTestChain) StartWorkers(context.Context) error { return errors.New("not used") }
func (c configTestChain) StopWorkers() error                 { return errors.New("not used") }

func TestConfiguredStartBlockUsesChainSpecificIDFirst(t *testing.T) {
	t.Setenv("CHAIN_1_START_BLOCK", "123")
	t.Setenv("ETHEREUM_START_BLOCK", "456")

	got, ok := ConfiguredStartBlock(configTestChain{id: constants.Ethereum, name: "ethereum"})
	if !ok {
		t.Fatal("ConfiguredStartBlock should find chain id env")
	}
	if got != 123 {
		t.Fatalf("ConfiguredStartBlock = %d, want 123", got)
	}
}

func TestConfiguredStartBlockUsesNormalizedChainName(t *testing.T) {
	t.Setenv("CHILIZ_SPICY_START_BLOCK", "77")

	got, ok := ConfiguredStartBlock(configTestChain{id: constants.ChilizSpicy, name: "chiliz-spicy"})
	if !ok {
		t.Fatal("ConfiguredStartBlock should find normalized chain name env")
	}
	if got != 77 {
		t.Fatalf("ConfiguredStartBlock = %d, want 77", got)
	}
}

func TestConfiguredStartBlockIgnoresZero(t *testing.T) {
	t.Setenv("CHAIN_START_BLOCK_DEFAULT", "0")

	if got, ok := ConfiguredStartBlock(configTestChain{id: constants.Bitcoin, name: "bitcoin"}); ok {
		t.Fatalf("ConfiguredStartBlock ok with value %d, want false", got)
	}
}

func TestSupportedListenersRespectConfiguredStartBlock(t *testing.T) {
	for _, path := range []string{
		"evm/listener.go",
		"bitcoin/bitcoin.go",
		"solana/listener.go",
		"tron/tron.go",
	} {
		t.Run(path, func(t *testing.T) {
			sourceBytes, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			source := string(sourceBytes)
			for _, token := range []string{
				"ConfiguredStartBlock(r.chain)",
				"configuredStart := false",
				"if from <= 1 && !configuredStart",
			} {
				if !strings.Contains(source, token) {
					t.Fatalf("%s does not preserve configured start block behavior %q", path, token)
				}
			}
		})
	}
}
