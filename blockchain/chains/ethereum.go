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

type EthereumChain struct {
	blockchain.BaseChain
}

func NewEthereumChain() *EthereumChain {
	return &EthereumChain{
		blockchain.BaseChain{
			ID:          constants.Ethereum,
			ChainName:   "ethereum",
			ExplorerURL: "https://etherscan.io",
			RPCHttp:     []string{"https://mainnet.infura.io/v3/ac1242cf6a134cc3a77530953a7b65d5", "https://ethereum-rpc.publicnode.com", "https://1rpc.io/eth", "https://1rpc.io/eth"},
			WebSockets:  []string{"wss://mainnet.infura.io/ws/v3/ac1242cf6a134cc3a77530953a7b65d5", "wss://ethereum-rpc.publicnode.com"},
		}}
}

func (e *EthereumChain) Name() string {
	return e.ChainName
}

func (e *EthereumChain) ChainID() constants.ChainID {
	return e.ID
}

func (e *EthereumChain) RPCs() []string {
	return e.RPCHttp
}

func (e *EthereumChain) Explorer() string {
	return e.ExplorerURL
}

func (e *EthereumChain) WSS() []string {
	return e.WebSockets
}

func (e *EthereumChain) NewAddress(prvHex string) (string, error) {
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

func (s *EthereumChain) ValidateAddress(address string) bool {
	return ethSDK.ValidateAddress(address)
}

func (s *EthereumChain) Create(ctx context.Context) (*blockchain.WalletDetails, error) {
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

func (s *EthereumChain) CreateHDWallet(ctx context.Context, hdAccountId, hdWalletId int) (*blockchain.WalletDetails, error) {
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

func (s *EthereumChain) Deposit(ctx context.Context, wallet blockchain.WalletDetails, amount float64, toAddress string) (*blockchain.TransactionResult, error) {
	fmt.Printf("[%s]: Depositing %f to %s\n", s.Name(), amount, toAddress)
	return &blockchain.TransactionResult{TxHash: "DepositTxHash", Success: true}, nil
}

func (s *EthereumChain) Withdraw(ctx context.Context, wallet blockchain.WalletDetails, amount float64, toAddress string) (*blockchain.TransactionResult, error) {
	fmt.Printf("[%s]: Withdrawing %f from %s\n", s.Name(), amount, wallet.Address)
	return &blockchain.TransactionResult{TxHash: "WithdrawTxHash", Success: true}, nil
}

func (s *EthereumChain) Sweep(ctx context.Context, wallet blockchain.WalletDetails) (*blockchain.TransactionResult, error) {
	fmt.Printf("[%s]: Sweeping wallet", s.Name())
	return &blockchain.TransactionResult{TxHash: "SweepTxHash", Success: true}, nil
}
