package chains

import (
	"context"
	blockchain "core/blockchain"
	"core/constants"
	"encoding/hex"
	"errors"
	"fmt"
	"log"

	"github.com/ethereum/go-ethereum/crypto"
	ethSDK "github.com/okx/go-wallet-sdk/coins/ethereum"
)

type BinanceChain struct {
	blockchain.BaseChain
}

func NewBinanceChain() *BinanceChain {
	return &BinanceChain{
		blockchain.BaseChain{
			ID:          constants.Binance,
			ChainName:   "binance",
			ExplorerURL: "https://bscscan.com/",
			RPCHttp:     []string{"https://bsc-dataseed.bnbchain.org", "https://bsc-dataseed1.bnbchain.org", "https://bsc-dataseed2.bnbchain.org", "https://bsc-dataseed3.bnbchain.org", "https://bsc-dataseed4.bnbchain.org"},
			WebSockets:  []string{"wss://bsc.drpc.org"},
		}}
}

func (e *BinanceChain) Name() string {
	return e.ChainName
}

func (e *BinanceChain) ChainID() constants.ChainID {
	return e.ID
}

func (e *BinanceChain) RPCs() []string {
	return e.RPCHttp
}

func (e *BinanceChain) Explorer() string {
	return e.ExplorerURL
}

func (e *BinanceChain) WSS() []string {
	return e.WebSockets
}

func (e *BinanceChain) NewAddress(prvHex string) (string, error) {
	prvBytes, err := hex.DecodeString(prvHex)
	if err != nil {
		return "", errors.New("invalid private key hex: " + err.Error())
	}
	privateKey, err := crypto.ToECDSA(prvBytes)
	if err != nil {
		return "", errors.New("invalid private key bytes: " + err.Error())
	}
	address := crypto.PubkeyToAddress(privateKey.PublicKey).Hex()
	return address, nil
}

func (s *BinanceChain) ValidateAddress(address string) bool {
	return ethSDK.ValidateAddress(address)
}

func (s *BinanceChain) CreateHDWallet(ctx context.Context, hdAccountId, hdWalletId int) (*blockchain.WalletDetails, error) {
	fmt.Printf("[%s]: Creating wallet\n", s.Name())

	mnemonic, err := s.BaseChain.GenerateMnemonic()
	if err != nil {
		return nil, err
	}

	hdPath := s.BaseChain.GetDerivedPath(44, 60, 0, hdAccountId, hdWalletId)
	privateKey, err := s.BaseChain.GetDerivedPrivateKey(mnemonic, hdPath)
	if err != nil {
		return nil, err
	}
	address, err := s.NewAddress(privateKey)
	if err != nil {
		log.Printf("[%s] NewAddress error:%s \n", s.BaseChain.Name(), err.Error())
	}

	if !s.ValidateAddress(address) {
		return nil, errors.New("invalid ethereum address format")

	}

	return &blockchain.WalletDetails{
		Address:        address,
		PrivateKey:     privateKey,
		MnemonicPhrase: mnemonic,
	}, nil
}

func (s *BinanceChain) Create(ctx context.Context) (*blockchain.WalletDetails, error) {
	fmt.Printf("[%s]: Creating wallet\n", s.Name())

	mnemonic, err := s.BaseChain.GenerateMnemonic()
	if err != nil {
		return nil, err
	}

	hdPath := s.BaseChain.GetDerivedPath(44, 60, 0, 0, 1)
	privateKey, err := s.BaseChain.GetDerivedPrivateKey(mnemonic, hdPath)
	if err != nil {
		return nil, err
	}
	address, err := s.NewAddress(privateKey)
	if err != nil {
		log.Printf("[%s] NewAddress error:%s \n", s.BaseChain.Name(), err.Error())
	}

	if !s.ValidateAddress(address) {
		return nil, errors.New("invalid ethereum address format")

	}

	return &blockchain.WalletDetails{
		Address:        address,
		PrivateKey:     privateKey,
		MnemonicPhrase: mnemonic,
	}, nil
}

func (s *BinanceChain) Deposit(ctx context.Context, wallet blockchain.WalletDetails, amount float64, toAddress string) (*blockchain.TransactionResult, error) {
	fmt.Printf("[%s]: Depositing %f to %s\n", s.Name(), amount, toAddress)
	return &blockchain.TransactionResult{TxHash: "DepositTxHash", Success: true}, nil
}

func (s *BinanceChain) Withdraw(ctx context.Context, wallet blockchain.WalletDetails, amount float64, toAddress string) (*blockchain.TransactionResult, error) {
	fmt.Printf("[%s]: Withdrawing %f from %s\n", s.Name(), amount, wallet.Address)
	return &blockchain.TransactionResult{TxHash: "WithdrawTxHash", Success: true}, nil
}

func (s *BinanceChain) Sweep(ctx context.Context, wallet blockchain.WalletDetails) (*blockchain.TransactionResult, error) {
	fmt.Printf("[%s]: Sweeping wallet", s.Name())
	return &blockchain.TransactionResult{TxHash: "SweepTxHash", Success: true}, nil
}
