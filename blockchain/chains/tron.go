package chains

import (
	"context"
	blockchain "core/blockchain"
	"encoding/hex"
	"errors"
	"fmt"
	"log"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/okx/go-wallet-sdk/coins/tron"
	tronSDK "github.com/okx/go-wallet-sdk/coins/tron"
)

type TronChain struct {
	blockchain.BaseChain
}

func NewTronChain() *TronChain {
	return &TronChain{
		blockchain.BaseChain{
			ChainName:   "tron",
			ExplorerURL: "https://tronscan.org/",
			RPCHttp:     []string{"https://api.trongrid.io/jsonrpc", "https://rpc.ankr.com/tron_jsonrpc"},
		},
	}
}

func (t *TronChain) NewAddress(privateKeyHex string) (string, error) {
	prvBytes, err := hex.DecodeString(privateKeyHex)
	if err != nil {
		return "", fmt.Errorf("hex decode failed: %w", err)
	}

	privKey, pubKey := btcec.PrivKeyFromBytes(prvBytes)
	if privKey == nil || pubKey == nil {
		return "", fmt.Errorf("invalid private key bytes or nil public key")
	}

	address := tron.GetAddress(pubKey)
	return address, nil
}

func (s *TronChain) ValidateAddress(address string) bool {
	return tronSDK.ValidateAddress(address)
}

func (s *TronChain) Create(ctx context.Context) (*blockchain.WalletDetails, error) {
	fmt.Printf("[%s]: Creating wallet\n", s.Name())

	mnemonic, err := s.BaseChain.GenerateMnemonic()
	if err != nil {
		return nil, err
	}

	hdPath := s.BaseChain.GetDerivedPath(44, 195, 0, 0, 1)
	privateKey, err := s.BaseChain.GetDerivedPrivateKey(mnemonic, hdPath)
	if err != nil {
		return nil, err
	}
	address, err := s.NewAddress(privateKey)
	if err != nil {
		log.Printf("[%s] NewAddress error:%s \n", s.BaseChain.Name(), err.Error())
	}

	if !s.ValidateAddress(address) {
		return nil, errors.New("invalid tron address format")

	}

	return &blockchain.WalletDetails{
		Address:        address,
		PrivateKey:     privateKey,
		MnemonicPhrase: mnemonic,
	}, nil
}

func (s *TronChain) Deposit(ctx context.Context, wallet blockchain.WalletDetails, amount float64, toAddress string) (*blockchain.TransactionResult, error) {
	fmt.Printf("[%s]: Depositing %f to %s\n", s.Name(), amount, toAddress)
	return &blockchain.TransactionResult{TxHash: "DepositTxHash", Success: true}, nil
}

func (s *TronChain) Withdraw(ctx context.Context, wallet blockchain.WalletDetails, amount float64, toAddress string) (*blockchain.TransactionResult, error) {
	fmt.Printf("[%s]: Withdrawing %f from %s\n", s.Name(), amount, wallet.Address)
	return &blockchain.TransactionResult{TxHash: "WithdrawTxHash", Success: true}, nil
}

func (s *TronChain) Sweep(ctx context.Context, wallet blockchain.WalletDetails) (*blockchain.TransactionResult, error) {
	fmt.Printf("[%s]: Sweeping wallet", s.Name())
	return &blockchain.TransactionResult{TxHash: "SweepTxHash", Success: true}, nil
}
