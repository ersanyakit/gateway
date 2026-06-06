package blockchain

import (
	"context"
	"errors"
	"testing"

	"core/constants"
	"core/models"
)

type testChain struct {
	BaseChain
	createErr error
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
