package blockchain

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"core/constants"
	"core/models"
)

type testChain struct {
	BaseChain
	createErr   error
	hdCreateErr error
}

func newTestChain(id constants.ChainID, name string) *testChain {
	return &testChain{BaseChain: BaseChain{ID: id, ChainName: name}}
}

func (t *testChain) Create(ctx context.Context) (*WalletDetails, error) {
	if t.createErr != nil {
		return nil, t.createErr
	}
	return &WalletDetails{Address: t.Name() + "-address"}, nil
}

func (t *testChain) CreateHDWallet(ctx context.Context, hdAccountId, hdWalletId int) (*WalletDetails, error) {
	if t.hdCreateErr != nil {
		return nil, t.hdCreateErr
	}
	return &WalletDetails{Address: t.Name() + "-hd-address"}, nil
}

func (t *testChain) Deposit(ctx context.Context, wallet WalletDetails, amountRaw string, toAddress string) (*TransactionResult, error) {
	return nil, errors.New("not used")
}

func (t *testChain) Withdraw(ctx context.Context, wallet WalletDetails, amountRaw string, toAddress string) (*TransactionResult, error) {
	return nil, errors.New("not used")
}

func (t *testChain) WithdrawToken(ctx context.Context, wallet WalletDetails, tokenAddr string, amountRaw string, toAddress string) (*TransactionResult, error) {
	return nil, errors.New("not used")
}

func (t *testChain) Sweep(ctx context.Context, wallet WalletDetails) (*TransactionResult, error) {
	return nil, errors.New("not used")
}

func (t *testChain) SweepTo(ctx context.Context, wallet WalletDetails, toAddress string) (*TransactionResult, error) {
	return nil, errors.New("not used")
}

func (t *testChain) SweepERC20To(ctx context.Context, wallet WalletDetails, contractAddr, toAddress string) (*TransactionResult, error) {
	return nil, errors.New("not used")
}

func (t *testChain) PrefundGas(ctx context.Context, reserveWallet WalletDetails, userAddress string) (bool, error) {
	return false, errors.New("not used")
}

func (t *testChain) ValidateAddress(address string) bool { return address != "" }

func (t *testChain) BatchBalances(ctx context.Context, addresses []string, workers int) []models.BalanceResult {
	return nil
}

type blockingStopChain struct {
	*testChain
	started chan struct{}
	release chan struct{}
}

func (t *blockingStopChain) StopWorkers() error {
	close(t.started)
	<-t.release
	return nil
}

func TestChainFactoryLookupAliasesAndIDs(t *testing.T) {
	factory := NewChainFactory()
	eth := newTestChain(constants.Ethereum, "ethereum")
	factory.RegisterChain("ethereum", eth)
	factory.RegisterAlias("eth", "ethereum")

	byName, err := factory.GetChain("eth")
	if err != nil {
		t.Fatal(err)
	}
	if byName != eth {
		t.Fatal("alias lookup returned wrong chain")
	}
	byID, err := factory.GetChainByID(constants.Ethereum)
	if err != nil {
		t.Fatal(err)
	}
	if byID != eth {
		t.Fatal("id lookup returned wrong chain")
	}
	if _, err := factory.GetChain("missing"); !errors.Is(err, ErrChainNotFound) {
		t.Fatalf("missing chain error = %v", err)
	}
}

func TestChainFactoryCreateWalletsSeparatesErrors(t *testing.T) {
	factory := NewChainFactory()
	factory.RegisterChain("ethereum", newTestChain(constants.Ethereum, "ethereum"))
	factory.RegisterChain("solana", &testChain{BaseChain: BaseChain{ID: constants.Solana, ChainName: "solana"}, createErr: errors.New("boom")})

	wallets, errs := factory.CreateWallets(context.Background())
	if wallets["ethereum"] == nil {
		t.Fatal("ethereum wallet should be created")
	}
	if errs["solana"] == nil {
		t.Fatal("solana error should be recorded")
	}
}

func TestChainFactoryCreateHDWalletsSeparatesErrors(t *testing.T) {
	factory := NewChainFactory()
	factory.RegisterChain("ethereum", newTestChain(constants.Ethereum, "ethereum"))
	factory.RegisterChain("solana", &testChain{BaseChain: BaseChain{ID: constants.Solana, ChainName: "solana"}, hdCreateErr: errors.New("walletcorefallback cannot derive wallet addresses")})

	wallets, errs := factory.CreateHDWallets(context.Background(), 1, 2)
	if wallets["ethereum"] == nil {
		t.Fatal("ethereum HD wallet should be created")
	}
	if wallets["solana"] != nil {
		t.Fatal("solana HD wallet should not be created after derivation error")
	}
	if errs["solana"] == nil || !strings.Contains(errs["solana"].Error(), "walletcorefallback") {
		t.Fatalf("solana error = %v, want walletcorefallback", errs["solana"])
	}
}

func TestChainFactoryStopAllWorkersHonorsContext(t *testing.T) {
	factory := NewChainFactory()
	chain := &blockingStopChain{
		testChain: newTestChain(constants.Ethereum, "ethereum"),
		started:   make(chan struct{}),
		release:   make(chan struct{}),
	}
	factory.RegisterChain("ethereum", chain)
	defer close(chain.release)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	errs := factory.StopAllWorkers(ctx)
	<-chain.started

	if !errors.Is(errs["ethereum"], context.DeadlineExceeded) {
		t.Fatalf("stop error = %v, want deadline exceeded", errs["ethereum"])
	}
}
