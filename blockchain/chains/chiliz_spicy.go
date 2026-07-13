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
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
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
	return validateAddressWithWalletCore(s.ChainID(), address)
}

func (s *ChilizSpicyChain) Create(ctx context.Context) (*blockchain.WalletDetails, error) {
	mnemonic, err := s.BaseChain.GenerateMnemonicPhrase()
	if err != nil {
		return nil, err
	}
	hdPath := s.BaseChain.GetDerivedPath(44, 60, 0, 0, 1)
	wallet, err := s.BaseChain.GetDerivedWallet(mnemonic, hdPath)
	if err != nil {
		return nil, err
	}
	if !s.ValidateAddress(wallet.Address) {
		return nil, errors.New("invalid address format")
	}
	return wallet, nil
}

func (s *ChilizSpicyChain) CreateHDWallet(ctx context.Context, hdAccountId, hdWalletId int) (*blockchain.WalletDetails, error) {
	hdPath := s.BaseChain.GetDerivedPath(44, 60, hdAccountId, 0, hdWalletId)
	wallet, err := s.BaseChain.CreateHDWalletFromPath(ctx, hdPath)
	if err != nil {
		return nil, err
	}
	if !s.ValidateAddress(wallet.Address) {
		return nil, errors.New("invalid address format")
	}
	return wallet, nil
}

func (s *ChilizSpicyChain) Deposit(ctx context.Context, wallet blockchain.WalletDetails, amountRaw string, toAddress string) (*blockchain.TransactionResult, error) {
	return evmDepositNative(ctx, s.Name(), s.ChainID(), s.RPCs(), wallet, amountRaw, toAddress)
}

func (s *ChilizSpicyChain) Withdraw(ctx context.Context, wallet blockchain.WalletDetails, amountRaw string, toAddress string) (*blockchain.TransactionResult, error) {
	return evmWithdrawNative(ctx, s.Name(), s.ChainID(), s.RPCs(), wallet, amountRaw, toAddress)
}

func (s *ChilizSpicyChain) WithdrawToken(ctx context.Context, wallet blockchain.WalletDetails, tokenAddr string, amountRaw string, toAddress string) (*blockchain.TransactionResult, error) {
	return evmWithdrawERC20(ctx, s.Name(), s.ChainID(), s.RPCs(), wallet, tokenAddr, amountRaw, toAddress)
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
	client, selectedRPC, err := dialFirstHealthyEVMRPCWithURL(ctx, s.RPCHttp)
	if err != nil {
		log.Println("[chiliz-spicy] RPC dial error:", err)
		return failedBalanceResults(addresses, "CHZ:0", err)
	}
	defer client.Close()
	if len(s.RPCHttp) > 0 && strings.TrimSpace(s.RPCHttp[0]) != "" && selectedRPC != strings.TrimSpace(s.RPCHttp[0]) {
		log.Printf("[%s] balance RPC failover selected %s\n", s.Name(), selectedRPC)
	}

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
