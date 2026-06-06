package chains

import (
	"context"
	blockchain "core/blockchain"
	"core/constants"
	"core/models"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/crypto"
	ethSDK "github.com/okx/go-wallet-sdk/coins/ethereum"
)

type ChilizSpicyChain struct {
	blockchain.BaseChain
}

func NewChilizSpicyChain() *ChilizSpicyChain {
	return &ChilizSpicyChain{
		blockchain.BaseChain{
			ID:          constants.ChilizSpicy,
			ChainName:   "chiliz-spicy",
			ExplorerURL: "https://spicy-explorer.chiliz.com",
			RPCHttp:     []string{"https://spicy-rpc.chiliz.com", "https://chiliz-spicy.publicnode.com"},
			WebSockets:  []string{"wss://chiliz-spicy.publicnode.com"},
		}}
}

func (e *ChilizSpicyChain) Name() string {
	return e.ChainName
}

func (e *ChilizSpicyChain) ChainID() constants.ChainID {
	return e.ID
}

func (e *ChilizSpicyChain) RPCs() []string {
	return e.BaseChain.RPCs()
}

func (e *ChilizSpicyChain) Explorer() string {
	return e.ExplorerURL
}

func (e *ChilizSpicyChain) WSS() []string {
	return e.WebSockets
}

func (e *ChilizSpicyChain) NewAddress(prvHex string) (string, error) {
	prvBytes, err := hex.DecodeString(prvHex)
	if err != nil {
		return "", errors.New("invalid private key hex: " + err.Error())
	}
	privateKey, err := crypto.ToECDSA(prvBytes)
	if err != nil {
		return "", errors.New("invalid private key bytes: " + err.Error())
	}
	return crypto.PubkeyToAddress(privateKey.PublicKey).Hex(), nil
}

func (s *ChilizSpicyChain) ValidateAddress(address string) bool {
	return ethSDK.ValidateAddress(address)
}

func (s *ChilizSpicyChain) Create(ctx context.Context) (*blockchain.WalletDetails, error) {
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
		return nil, errors.New("invalid address format")
	}
	return &blockchain.WalletDetails{
		Address:        address,
		PrivateKey:     privateKey,
		MnemonicPhrase: mnemonic,
	}, nil
}

func (s *ChilizSpicyChain) CreateHDWallet(ctx context.Context, hdAccountId, hdWalletId int) (*blockchain.WalletDetails, error) {
	fmt.Printf("[%s]: Creating HD wallet\n", s.Name())
	mnemonic, err := s.BaseChain.GetMnemonic()
	if err != nil {
		return nil, err
	}
	hdPath := s.BaseChain.GetDerivedPath(44, 60, hdAccountId, 0, hdWalletId)
	privateKey, err := s.BaseChain.GetDerivedPrivateKey(mnemonic, hdPath)
	if err != nil {
		return nil, err
	}
	address, err := s.NewAddress(privateKey)
	if err != nil {
		log.Printf("[%s] NewAddress error:%s \n", s.BaseChain.Name(), err.Error())
	}
	if !s.ValidateAddress(address) {
		return nil, errors.New("invalid address format")
	}
	fmt.Printf("WALLET:%s --- %s \n", s.BaseChain.Name(), address)
	return &blockchain.WalletDetails{
		Address:        address,
		PrivateKey:     privateKey,
		MnemonicPhrase: mnemonic,
	}, nil
}

func (s *ChilizSpicyChain) Deposit(ctx context.Context, wallet blockchain.WalletDetails, amountRaw string, toAddress string) (*blockchain.TransactionResult, error) {
	return evmDepositNative(ctx, s.Name(), s.ChainID(), s.RPCs(), wallet, amountRaw, toAddress)
}

func (s *ChilizSpicyChain) Withdraw(ctx context.Context, wallet blockchain.WalletDetails, amountRaw string, toAddress string) (*blockchain.TransactionResult, error) {
	return evmWithdrawNative(ctx, s.Name(), s.ChainID(), s.RPCs(), wallet, amountRaw, toAddress)
}

func (s *ChilizSpicyChain) Sweep(ctx context.Context, wallet blockchain.WalletDetails) (*blockchain.TransactionResult, error) {
	return evmSweepNative(ctx, s.Name(), s.ChainID(), s.RPCs(), wallet)
}

func (s *ChilizSpicyChain) SweepTo(ctx context.Context, wallet blockchain.WalletDetails, toAddress string) (*blockchain.TransactionResult, error) {
	return evmSweepNativeTo(ctx, s.Name(), s.ChainID(), s.RPCs(), wallet, toAddress)
}

func (s *ChilizSpicyChain) SweepERC20To(ctx context.Context, wallet blockchain.WalletDetails, contractAddr, toAddress string) (*blockchain.TransactionResult, error) {
	return evmSweepERC20To(ctx, s.Name(), s.ChainID(), s.RPCs(), wallet, contractAddr, toAddress)
}

func (s *ChilizSpicyChain) PrefundGas(ctx context.Context, reserveWallet blockchain.WalletDetails, userAddress string) (bool, error) {
	return evmPrefundGas(ctx, s.Name(), s.ChainID(), s.RPCs(), reserveWallet, userAddress, evmGasThreshold(), evmGasPrefundAmount())
}

func (s *ChilizSpicyChain) BatchBalances(ctx context.Context, addresses []string, workers int) []models.BalanceResult {
	if len(addresses) == 0 {
		return nil
	}
	if len(s.RPCHttp) == 0 {
		return nil
	}
	client, err := ethclient.Dial(s.RPCHttp[0])
	if err != nil {
		log.Println("[chiliz-spicy] RPC dial error:", err)
		return nil
	}
	defer client.Close()

	out := make([]models.BalanceResult, 0, len(addresses))
	for _, addr := range addresses {
		if !common.IsHexAddress(addr) {
			out = append(out, models.BalanceResult{Address: addr, Balance: "CHZ:0", Error: fmt.Errorf("invalid address: %s", addr)})
			continue
		}
		balance, err := client.BalanceAt(ctx, common.HexToAddress(addr), nil)
		if err != nil {
			out = append(out, models.BalanceResult{Address: addr, Balance: "CHZ:0", Error: err})
			continue
		}
		if balance == nil || balance.Sign() == 0 {
			continue
		}
		out = append(out, models.BalanceResult{
			Address: addr,
			Balance: fmt.Sprintf("CHZ:%s", formatWei(balance)),
			Error:   nil,
		})
		_ = big.NewInt(0) // suppress unused import
	}
	return out
}
