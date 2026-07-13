package chains

import (
	"context"
	blockchain "core/blockchain"
	"core/constants"
	"core/contracts/erc20"
	"core/contracts/multicall3"
	"core/models"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

type BinanceChain struct {
	blockchain.BaseChain
}

func NewBinanceChain() *BinanceChain {
	return &BinanceChain{
		blockchain.BaseChain{
			ID:          constants.Binance,
			ChainName:   "bnbchain",
			ExplorerURL: "https://bscscan.com/",
			RPCHttp:     []string{"https://bsc-dataseed1.bnbchain.org", "https://bsc-dataseed2.bnbchain.org", "https://bsc-dataseed3.bnbchain.org", "https://bsc-dataseed4.bnbchain.org", "https://bsc-rpc.publicnode.com", "https://bsc.drpc.org"},
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
	return e.BaseChain.RPCs()
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
	return validateAddressWithWalletCore(s.ChainID(), address)
}

func (s *BinanceChain) Create(ctx context.Context) (*blockchain.WalletDetails, error) {
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
		return nil, errors.New("invalid ethereum address format")

	}

	return wallet, nil
}

func (s *BinanceChain) CreateHDWallet(ctx context.Context, hdAccountId, hdWalletId int) (*blockchain.WalletDetails, error) {
	hdPath := s.BaseChain.GetDerivedPath(44, 60, hdAccountId, 0, hdWalletId)
	wallet, err := s.BaseChain.CreateHDWalletFromPath(ctx, hdPath)
	if err != nil {
		return nil, err
	}

	if !s.ValidateAddress(wallet.Address) {
		return nil, errors.New("invalid ethereum address format")

	}
	return wallet, nil
}

func (s *BinanceChain) Deposit(ctx context.Context, wallet blockchain.WalletDetails, amountRaw string, toAddress string) (*blockchain.TransactionResult, error) {
	return evmDepositNative(ctx, s.Name(), s.ChainID(), s.RPCs(), wallet, amountRaw, toAddress)
}

func (s *BinanceChain) Withdraw(ctx context.Context, wallet blockchain.WalletDetails, amountRaw string, toAddress string) (*blockchain.TransactionResult, error) {
	return evmWithdrawNative(ctx, s.Name(), s.ChainID(), s.RPCs(), wallet, amountRaw, toAddress)
}

func (s *BinanceChain) WithdrawToken(ctx context.Context, wallet blockchain.WalletDetails, tokenAddr string, amountRaw string, toAddress string) (*blockchain.TransactionResult, error) {
	return evmWithdrawERC20(ctx, s.Name(), s.ChainID(), s.RPCs(), wallet, tokenAddr, amountRaw, toAddress)
}

func (s *BinanceChain) Sweep(ctx context.Context, wallet blockchain.WalletDetails) (*blockchain.TransactionResult, error) {
	return evmSweepNative(ctx, s.Name(), s.ChainID(), s.RPCs(), wallet)
}

func (s *BinanceChain) SweepTo(ctx context.Context, wallet blockchain.WalletDetails, toAddress string) (*blockchain.TransactionResult, error) {
	return evmSweepNativeTo(ctx, s.Name(), s.ChainID(), s.RPCs(), wallet, toAddress)
}

func (s *BinanceChain) SweepERC20To(ctx context.Context, wallet blockchain.WalletDetails, contractAddr, toAddress string) (*blockchain.TransactionResult, error) {
	return evmSweepERC20To(ctx, s.Name(), s.ChainID(), s.RPCs(), wallet, contractAddr, toAddress)
}

func (s *BinanceChain) PrefundGas(ctx context.Context, reserveWallet blockchain.WalletDetails, userAddress string) (bool, error) {
	return evmPrefundGas(ctx, s.Name(), s.ChainID(), s.RPCs(), reserveWallet, userAddress, evmGasThreshold(), evmGasPrefundAmount())
}

const BINANCE_SYMBOL = "BNB"
const BINANCE_TOKEN_SYMBOL = "WBTC"
const BINANCE_TOKEN_ADDRESS = "0xbb4CdB9CBd36B01bD1cBaEBF2De08d9173bc095c"

type binanceMulticallBalance struct {
	BNB      *big.Int
	Token    *big.Int
	BNBErr   error
	TokenErr error
}

func (e *BinanceChain) BatchBalances(ctx context.Context, addresses []string, workers int) []models.BalanceResult {
	if len(addresses) == 0 {
		return nil
	}

	client, selectedRPC, err := dialFirstHealthyEVMRPCWithURL(ctx, e.RPCHttp)
	if err != nil {
		log.Println("RPC dial error:", err)
		return failedBalanceResults(addresses, "BNB:0 | WETH:0", err)
	}
	defer client.Close()
	if len(e.RPCHttp) > 0 && strings.TrimSpace(e.RPCHttp[0]) != "" && selectedRPC != strings.TrimSpace(e.RPCHttp[0]) {
		log.Printf("[%s] balance RPC failover selected %s\n", e.Name(), selectedRPC)
	}

	out := make([]models.BalanceResult, 0, len(addresses))
	batchSize := 100

	for i := 0; i < len(addresses); i += batchSize {
		end := i + batchSize
		if end > len(addresses) {
			end = len(addresses)
		}
		batch := addresses[i:end]
		validBatch := make([]string, 0, len(batch))
		invalidAddresses := make(map[string]error)

		for _, addr := range batch {
			if !common.IsHexAddress(addr) {
				invalidAddresses[addr] = fmt.Errorf("invalid binance smart chain address: %s", addr)
				continue
			}
			validBatch = append(validBatch, addr)
		}

		balances := make(map[string]binanceMulticallBalance, len(validBatch))
		if len(validBatch) > 0 {
			balances, err = e.getBNBTokenBalances(ctx, client, validBatch)
			if err != nil {
				log.Printf("[%s] balance batch recovered with partial fallback: %v\n", e.Name(), err)
			}
		}

		for _, addr := range batch {
			if invalidErr := invalidAddresses[addr]; invalidErr != nil {
				out = append(out, models.BalanceResult{
					Address: addr,
					Balance: fmt.Sprintf("%s:%s | %s:%s", BINANCE_SYMBOL, formatWei(big.NewInt(0)), BINANCE_TOKEN_SYMBOL, formatWei(big.NewInt(0))),
					Error:   invalidErr,
				})
				continue
			}

			balance, ok := balances[addr]
			if !ok {
				balance = binanceMulticallBalance{
					BNB:      big.NewInt(0),
					Token:    big.NewInt(0),
					BNBErr:   err,
					TokenErr: err,
				}
			}

			bnbBalance := ensureBigInt(balance.BNB)
			tokenBalance := ensureBigInt(balance.Token)
			callErr := errors.Join(balance.BNBErr, balance.TokenErr)
			if callErr == nil && bnbBalance.Sign() == 0 && tokenBalance.Sign() == 0 {
				continue
			}

			out = append(out, models.BalanceResult{
				Address: addr,
				Balance: fmt.Sprintf("%s:%s | %s:%s", BINANCE_SYMBOL, formatWei(bnbBalance), BINANCE_TOKEN_SYMBOL, formatWei(tokenBalance)),
				Error:   callErr,
			})
		}
	}

	return out
}

func (e *BinanceChain) getBNBTokenBalances(ctx context.Context, client *ethclient.Client, addresses []string) (map[string]binanceMulticallBalance, error) {
	balances, err := e.getBNBTokenBalancesMulticall(ctx, client, addresses)
	if err == nil || len(addresses) <= 1 {
		if err == nil {
			return balances, nil
		}
		return e.getBNBTokenBalancesDirect(ctx, client, addresses)
	}

	mid := len(addresses) / 2
	leftBalances, leftErr := e.getBNBTokenBalances(ctx, client, addresses[:mid])
	rightBalances, rightErr := e.getBNBTokenBalances(ctx, client, addresses[mid:])

	merged := make(map[string]binanceMulticallBalance, len(addresses))
	for address, balance := range leftBalances {
		merged[address] = balance
	}
	for address, balance := range rightBalances {
		merged[address] = balance
	}

	return merged, errors.Join(leftErr, rightErr)
}

func (e *BinanceChain) getBNBTokenBalancesMulticall(ctx context.Context, client *ethclient.Client, addresses []string) (map[string]binanceMulticallBalance, error) {
	if len(addresses) == 0 {
		return map[string]binanceMulticallBalance{}, nil
	}

	multicallABI, err := multicall3.Multicall3MetaData.GetAbi()
	if err != nil {
		return nil, fmt.Errorf("parse multicall3 abi: %w", err)
	}

	erc20ABI, err := erc20.ERC20MetaData.GetAbi()
	if err != nil {
		return nil, fmt.Errorf("parse erc20 abi: %w", err)
	}

	multicallAddress := common.HexToAddress(MULTICALL3_ADDRESS)
	tokenAddress := common.HexToAddress(BINANCE_TOKEN_ADDRESS)
	calls := make([]multicall3.Multicall3Call3, 0, len(addresses)*2)

	for _, rawAddress := range addresses {
		address := common.HexToAddress(rawAddress)
		nativeCallData, err := multicallABI.Pack("getEthBalance", address)
		if err != nil {
			return nil, fmt.Errorf("pack getEthBalance for %s: %w", rawAddress, err)
		}

		tokenCallData, err := erc20ABI.Pack("balanceOf", address)
		if err != nil {
			return nil, fmt.Errorf("pack balanceOf for %s: %w", rawAddress, err)
		}

		calls = append(calls,
			multicall3.Multicall3Call3{
				Target:       multicallAddress,
				AllowFailure: true,
				CallData:     nativeCallData,
			},
			multicall3.Multicall3Call3{
				Target:       tokenAddress,
				AllowFailure: true,
				CallData:     tokenCallData,
			},
		)
	}

	contract := bind.NewBoundContract(multicallAddress, *multicallABI, client, nil, nil)

	var output []interface{}
	if err := contract.Call(&bind.CallOpts{Context: ctx}, &output, "aggregate3", calls); err != nil {
		return nil, fmt.Errorf("aggregate3 call failed: %w", err)
	}

	if len(output) != 1 {
		return nil, fmt.Errorf("unexpected aggregate3 output count: %d", len(output))
	}

	results := *abi.ConvertType(output[0], new([]multicall3.Multicall3Result)).(*[]multicall3.Multicall3Result)
	if len(results) != len(calls) {
		return nil, fmt.Errorf("unexpected aggregate3 result count: got %d want %d", len(results), len(calls))
	}

	balances := make(map[string]binanceMulticallBalance, len(addresses))
	for i, rawAddress := range addresses {
		nativeCallResult := results[i*2]
		tokenCallResult := results[i*2+1]

		entry := binanceMulticallBalance{
			BNB:   big.NewInt(0),
			Token: big.NewInt(0),
		}

		if nativeCallResult.Success {
			entry.BNB, entry.BNBErr = unpackUint256(multicallABI, "getEthBalance", nativeCallResult.ReturnData)
		} else {
			entry.BNBErr = fmt.Errorf("multicall3 getEthBalance failed for %s", rawAddress)
		}

		if tokenCallResult.Success {
			entry.Token, entry.TokenErr = unpackUint256(erc20ABI, "balanceOf", tokenCallResult.ReturnData)
		} else {
			entry.TokenErr = fmt.Errorf("%s balanceOf failed for %s", BINANCE_TOKEN_SYMBOL, rawAddress)
		}

		entry.BNB = ensureBigInt(entry.BNB)
		entry.Token = ensureBigInt(entry.Token)
		balances[rawAddress] = entry
	}

	return balances, nil
}

func (e *BinanceChain) getBNBTokenBalancesDirect(ctx context.Context, client *ethclient.Client, addresses []string) (map[string]binanceMulticallBalance, error) {
	balances := make(map[string]binanceMulticallBalance, len(addresses))
	var errs []error

	tokenContract, contractErr := erc20.NewERC20Caller(common.HexToAddress(BINANCE_TOKEN_ADDRESS), client)
	if contractErr != nil {
		errs = append(errs, contractErr)
	}

	for _, rawAddress := range addresses {
		address := common.HexToAddress(rawAddress)
		entry := binanceMulticallBalance{
			BNB:   big.NewInt(0),
			Token: big.NewInt(0),
		}

		bnbBalance, err := client.BalanceAt(ctx, address, nil)
		if err != nil {
			entry.BNBErr = err
			errs = append(errs, fmt.Errorf("%s balance %s: %w", BINANCE_SYMBOL, rawAddress, err))
		} else {
			entry.BNB = bnbBalance
		}

		if tokenContract == nil {
			entry.TokenErr = contractErr
		} else {
			tokenBalance, err := tokenContract.BalanceOf(&bind.CallOpts{Context: ctx}, address)
			if err != nil {
				entry.TokenErr = err
				errs = append(errs, fmt.Errorf("%s balance %s: %w", BINANCE_TOKEN_SYMBOL, rawAddress, err))
			} else {
				entry.Token = tokenBalance
			}
		}

		entry.BNB = ensureBigInt(entry.BNB)
		entry.Token = ensureBigInt(entry.Token)
		balances[rawAddress] = entry
	}

	return balances, errors.Join(errs...)
}
