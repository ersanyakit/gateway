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

type AvalancheChain struct {
	blockchain.BaseChain
}

func NewAvalancheChain() *AvalancheChain {
	return &AvalancheChain{
		blockchain.BaseChain{
			ID:          constants.Avalanche,
			ChainName:   "avalanche",
			ExplorerURL: "https://snowscan.xyz/",
			RPCHttp:     []string{"https://api.avax.network/ext/bc/C/rpc"},
			WebSockets:  []string{"wss://avalanche.drpc.org"},
		}}
}

func (e *AvalancheChain) Name() string {
	return e.ChainName
}

func (e *AvalancheChain) ChainID() constants.ChainID {
	return e.ID
}

func (e *AvalancheChain) RPCs() []string {
	return e.RPCHttp
}

func (e *AvalancheChain) Explorer() string {
	return e.ExplorerURL
}

func (e *AvalancheChain) WSS() []string {
	return e.WebSockets
}

func (e *AvalancheChain) NewAddress(prvHex string) (string, error) {
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

func (s *AvalancheChain) ValidateAddress(address string) bool {
	return ethSDK.ValidateAddress(address)
}

func (s *AvalancheChain) Create(ctx context.Context) (*blockchain.WalletDetails, error) {
	fmt.Printf("[%s]: Creating wallet\n", s.Name())

	mnemonic, err := s.BaseChain.GenerateMnemonicPhrase()
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

func (s *AvalancheChain) CreateHDWallet(ctx context.Context, hdAccountId, hdWalletId int) (*blockchain.WalletDetails, error) {
	fmt.Printf("[%s]: Creating HD wallet\n", s.Name())

	mnemonic, err := s.BaseChain.GetMnemonic()
	if err != nil {
		return nil, err
	}

	hdPath := s.BaseChain.GetDerivedPath(44, 60, int(s.ChainID()), hdAccountId, hdWalletId)
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

	fmt.Printf("WALLET:%s --- %s \n", s.BaseChain.Name(), address)

	return &blockchain.WalletDetails{
		Address:        address,
		PrivateKey:     privateKey,
		MnemonicPhrase: mnemonic,
	}, nil
}

func (s *AvalancheChain) Deposit(ctx context.Context, wallet blockchain.WalletDetails, amount float64, toAddress string) (*blockchain.TransactionResult, error) {
	fmt.Printf("[%s]: Depositing %f to %s\n", s.Name(), amount, toAddress)
	return &blockchain.TransactionResult{TxHash: "DepositTxHash", Success: true}, nil
}

func (s *AvalancheChain) Withdraw(ctx context.Context, wallet blockchain.WalletDetails, amount float64, toAddress string) (*blockchain.TransactionResult, error) {
	fmt.Printf("[%s]: Withdrawing %f from %s\n", s.Name(), amount, wallet.Address)
	return &blockchain.TransactionResult{TxHash: "WithdrawTxHash", Success: true}, nil
}

func (s *AvalancheChain) Sweep(ctx context.Context, wallet blockchain.WalletDetails) (*blockchain.TransactionResult, error) {
	fmt.Printf("[%s]: Sweeping wallet", s.Name())
	return &blockchain.TransactionResult{TxHash: "SweepTxHash", Success: true}, nil
}
