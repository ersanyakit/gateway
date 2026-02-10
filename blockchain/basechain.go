package blockchain

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

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
	Name() string
	Create(ctx context.Context) (*WalletDetails, error)
	Deposit(ctx context.Context, wallet WalletDetails, amount float64, toAddress string) (*TransactionResult, error)
	Withdraw(ctx context.Context, wallet WalletDetails, amount float64, toAddress string) (*TransactionResult, error)
	Sweep(ctx context.Context, wallet WalletDetails) (*TransactionResult, error)
	ValidateAddress(address string) bool

	AddWorker(listener Worker) error
	RemoveWorker(listener Worker) error
}

type BaseChain struct {
	ChainName   string
	ExplorerURL string
	RPCHttp     []string
	RPCSocket   []string

	Workers []Worker
}

func (b *BaseChain) Name() string {
	return b.ChainName
}

func (b *BaseChain) Create(ctx context.Context) (*WalletDetails, error) {
	return nil, errors.New("not implemented")
}

func (b *BaseChain) Deposit(ctx context.Context, wallet WalletDetails, amount float64, toAddress string) (*TransactionResult, error) {
	return nil, errors.New("not implemented")
}

func (b *BaseChain) Withdraw(ctx context.Context, wallet WalletDetails, amount float64, toAddress string) (*TransactionResult, error) {
	return nil, errors.New("not implemented")
}

func (b *BaseChain) Sweep(ctx context.Context, wallet WalletDetails) (*TransactionResult, error) {
	return nil, errors.New("not implemented")
}

func (f *BaseChain) GenerateMnemonic() (string, error) {
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

func (f *BaseChain) GetDerivedPath(purpose, coinType, account, change, index int) string {
	return fmt.Sprintf("m/%d'/%d'/%d'/%d/%d", purpose, coinType, account, change, index)
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

func (b *BaseChain) StartWorkers() error {
	for _, listener := range b.Workers {
		if err := listener.Start(); err != nil {
			return err
		}

		go func(l Worker) {
			for event := range l.Events() {
				fmt.Printf("[%s] Event: %v\n", b.ChainName, event)
			}
		}(listener)
	}
	return nil
}

func (b *BaseChain) StopWorkers() error {
	for _, listener := range b.Workers {
		if err := listener.Stop(); err != nil {
			return err
		}
	}
	return nil
}
