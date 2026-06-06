package blockchain

import (
	"context"
	"core/constants"
	"core/models"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/okx/go-wallet-sdk/crypto/go-bip32"
	"github.com/okx/go-wallet-sdk/crypto/go-bip39"
)

type WalletDetails struct {
	Address        string
	PrivateKey     string
	MnemonicPhrase string
}

type TransactionResult struct {
	TxHash  string
	Success bool
	Error   error
}

type Worker interface {
	Start() error
	Stop() error
	Events() <-chan interface{}
}

type Chain interface {
	ChainID() constants.ChainID
	Name() string
	WSS() []string
	RPCs() []string
	Create(ctx context.Context) (*WalletDetails, error)
	CreateHDWallet(ctx context.Context, hdAccountId, hdWalletId int) (*WalletDetails, error)

	Deposit(ctx context.Context, wallet WalletDetails, amountRaw string, toAddress string) (*TransactionResult, error)
	Withdraw(ctx context.Context, wallet WalletDetails, amountRaw string, toAddress string) (*TransactionResult, error)
	Sweep(ctx context.Context, wallet WalletDetails) (*TransactionResult, error)
	SweepTo(ctx context.Context, wallet WalletDetails, toAddress string) (*TransactionResult, error)
	SweepERC20To(ctx context.Context, wallet WalletDetails, contractAddr, toAddress string) (*TransactionResult, error)
	PrefundGas(ctx context.Context, reserveWallet WalletDetails, userAddress string) (bool, error)
	ValidateAddress(address string) bool

	AddWorker(listener Worker) error
	RemoveWorker(listener Worker) error
	BatchBalances(ctx context.Context, addresses []string, workers int) []models.BalanceResult
	StartWorkers(ctx context.Context) error
	StopWorkers() error
}

type BaseChain struct {
	ID          constants.ChainID
	ChainName   string
	ExplorerURL string
	RPCHttp     []string
	WebSockets  []string

	Workers []Worker

	ctx    context.Context
	cancel context.CancelFunc
}

func (b *BaseChain) Name() string {
	return b.ChainName
}

func (b *BaseChain) ChainID() constants.ChainID {
	return b.ID
}

func (b *BaseChain) RPCs() []string {
	rpcs := make([]string, 0, len(b.RPCHttp)+4)
	seen := make(map[string]struct{}, len(b.RPCHttp)+4)

	add := func(values []string) {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			rpcs = append(rpcs, value)
		}
	}

	for _, envName := range b.rpcEnvNames() {
		add(splitRPCEnv(os.Getenv(envName)))
	}
	add(splitRPCEnv(os.Getenv("CHAIN_" + strconv.FormatInt(int64(b.ID), 10) + "_RPC_URLS")))
	add(b.RPCHttp)

	return rpcs
}

func (b *BaseChain) Explorer() string {
	return b.ExplorerURL
}

func (b *BaseChain) WSS() []string {
	return b.WebSockets
}

func splitRPCEnv(raw string) []string {
	if raw == "" {
		return nil
	}
	return strings.Split(raw, ",")
}

func (b *BaseChain) rpcEnvNames() []string {
	name := strings.ToUpper(strings.NewReplacer("-", "_").Replace(b.ChainName))
	names := []string{name + "_RPC_URLS"}

	switch b.ChainName {
	case "bnbchain":
		names = append(names, "BSC_RPC_URLS", "BINANCE_RPC_URLS")
	case "tron":
		names = append(names, "TRON_JSONRPC_URLS")
	}

	return names
}

func (b *BaseChain) CreateHDWallet(ctx context.Context, hdAccountId, hdWalletId uint32) (*WalletDetails, error) {
	return nil, errors.New("not implemented")
}

func (b *BaseChain) Create(ctx context.Context) (*WalletDetails, error) {
	return nil, errors.New("not implemented")
}

func (b *BaseChain) Deposit(ctx context.Context, wallet WalletDetails, amountRaw string, toAddress string) (*TransactionResult, error) {
	return nil, errors.New("not implemented")
}

func (b *BaseChain) Withdraw(ctx context.Context, wallet WalletDetails, amountRaw string, toAddress string) (*TransactionResult, error) {
	return nil, errors.New("not implemented")
}

func (b *BaseChain) Sweep(ctx context.Context, wallet WalletDetails) (*TransactionResult, error) {
	return nil, errors.New("not implemented")
}

func (b *BaseChain) SweepTo(ctx context.Context, wallet WalletDetails, toAddress string) (*TransactionResult, error) {
	return nil, errors.New("not implemented")
}

func (b *BaseChain) SweepERC20To(ctx context.Context, wallet WalletDetails, contractAddr, toAddress string) (*TransactionResult, error) {
	return nil, errors.New("not implemented")
}

func (b *BaseChain) PrefundGas(ctx context.Context, reserveWallet WalletDetails, userAddress string) (bool, error) {
	return false, errors.New("not implemented")
}

func (f *BaseChain) GenerateMnemonicPhrase() (string, error) {
	entropy, err := bip39.NewEntropy(256)
	if err != nil {
		return "", err
	}
	mnemonic, err := bip39.NewMnemonic(entropy)
	if !bip39.IsMnemonicValid(mnemonic) {
		return "", errors.New("Invalid Mnemonic")
	}
	return mnemonic, err
}

func (f *BaseChain) GetMnemonic() (string, error) {
	mnemonic := os.Getenv("MNEMONIC_PHRASE")
	if !bip39.IsMnemonicValid(mnemonic) {
		return "", errors.New("Invalid Mnemonic")
	}
	return mnemonic, nil
}

func (f *BaseChain) GetDerivedPath(purpose, coin, account, change, index int) string {
	return fmt.Sprintf("m/%d'/%d'/%d'/%d/%d", purpose, coin, account, change, index)
}

func (f *BaseChain) GetDerivedPrivateKey(mnemonic string, hdPath string) (string, error) {
	seed := bip39.NewSeed(mnemonic, "")
	rp, err := bip32.NewMasterKey(seed)
	if err != nil {
		return "", err
	}
	c, err := rp.NewChildKeyByPathString(hdPath)
	if err != nil {
		return "", err
	}
	childPrivateKey := hex.EncodeToString(c.Key)
	return childPrivateKey, nil
}

func (b *BaseChain) AddWorker(listener Worker) error {
	b.Workers = append(b.Workers, listener)
	return nil
}

func (b *BaseChain) RemoveWorker(listener Worker) error {
	for i, l := range b.Workers {
		if l == listener {
			b.Workers = append(b.Workers[:i], b.Workers[i+1:]...)
			return nil
		}
	}
	return errors.New("listener not found")
}

func (b *BaseChain) StartWorkers(ctx context.Context) error {

	b.ctx, b.cancel = context.WithCancel(ctx)

	for _, listener := range b.Workers {

		if err := listener.Start(); err != nil {
			return err
		}

		go b.Work(listener)
	}

	return nil
}

func (b *BaseChain) Work(l Worker) {

	for {
		select {

		case <-b.ctx.Done():
			return

		case event, ok := <-l.Events():
			if !ok {
				return
			}

			fmt.Printf("[%s] Event: %v\n", b.ChainName, event)
		}
	}
}

func (b *BaseChain) StopWorkers() error {
	if b.cancel != nil {
		b.cancel()
	}
	for _, listener := range b.Workers {
		if err := listener.Stop(); err != nil {
			return err
		}
	}

	return nil
}
