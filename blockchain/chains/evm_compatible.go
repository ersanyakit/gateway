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
	ethSDK "github.com/okx/go-wallet-sdk/coins/ethereum"
)

func dialFirstHealthyEVMRPC(ctx context.Context, rpcs []string) (*ethclient.Client, error) {
	client, _, err := dialFirstHealthyEVMRPCWithURL(ctx, rpcs)
	return client, err
}

func dialFirstHealthyEVMRPCWithURL(ctx context.Context, rpcs []string) (*ethclient.Client, string, error) {
	var lastErr error
	for _, rpcURL := range rpcs {
		rpcURL = strings.TrimSpace(rpcURL)
		if rpcURL == "" {
			continue
		}

		client, err := ethclient.DialContext(ctx, rpcURL)
		if err != nil {
			lastErr = err
			continue
		}

		if _, err := client.ChainID(ctx); err != nil {
			lastErr = err
			client.Close()
			continue
		}

		return client, rpcURL, nil
	}
	if lastErr != nil {
		return nil, "", lastErr
	}
	return nil, "", errors.New("no RPC endpoint configured")
}

type EVMCompatibleChain struct {
	blockchain.BaseChain
	NativeSymbol string
	TokenSymbol  string
	TokenAddress string
}

func NewBaseChain() *EVMCompatibleChain {
	return &EVMCompatibleChain{
		BaseChain: blockchain.BaseChain{
			ID:          constants.Base,
			ChainName:   "base",
			ExplorerURL: "https://basescan.org",
			RPCHttp:     []string{"https://mainnet.base.org", "https://base-rpc.publicnode.com", "https://rpcfree.com/base-rpc", "https://base.drpc.org"},
			WebSockets:  []string{"wss://mainnet.base.org", "wss://base-rpc.publicnode.com"},
		},
		NativeSymbol: "ETH",
		TokenSymbol:  "WETH",
		TokenAddress: "0x4200000000000000000000000000000000000006",
	}
}

func NewArbitrumChain() *EVMCompatibleChain {
	return &EVMCompatibleChain{
		BaseChain: blockchain.BaseChain{
			ID:          constants.Arbitrum,
			ChainName:   "arbitrum",
			ExplorerURL: "https://arbiscan.io",
			RPCHttp:     []string{"https://arb1.arbitrum.io/rpc", "https://arbitrum-one-rpc.publicnode.com", "https://arbitrum.drpc.org"},
			WebSockets:  []string{"wss://arbitrum-one-rpc.publicnode.com"},
		},
		NativeSymbol: "ETH",
		TokenSymbol:  "WETH",
		TokenAddress: "0x82aF49447D8a07e3bd95BDd56f35241523fBab1",
	}
}

func NewUnichainChain() *EVMCompatibleChain {
	return &EVMCompatibleChain{
		BaseChain: blockchain.BaseChain{
			ID:          constants.Unichain,
			ChainName:   "unichain",
			ExplorerURL: "https://uniscan.xyz",
			RPCHttp:     []string{"https://mainnet.unichain.org", "https://unichain-rpc.publicnode.com", "https://unichain.drpc.org"},
			WebSockets:  []string{"wss://unichain-rpc.publicnode.com"},
		},
		NativeSymbol: "ETH",
		TokenSymbol:  "WETH",
		TokenAddress: "0x4200000000000000000000000000000000000006",
	}
}

func (e *EVMCompatibleChain) NewAddress(prvHex string) (string, error) {
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

func (e *EVMCompatibleChain) ValidateAddress(address string) bool {
	return ethSDK.ValidateAddress(address)
}

func (e *EVMCompatibleChain) Create(ctx context.Context) (*blockchain.WalletDetails, error) {
	mnemonic, err := e.BaseChain.GenerateMnemonicPhrase()
	if err != nil {
		return nil, err
	}

	hdPath := e.BaseChain.GetDerivedPath(44, 60, 0, 0, 1)
	wallet, err := e.BaseChain.GetDerivedWallet(mnemonic, hdPath)
	if err != nil {
		return nil, err
	}

	if !e.ValidateAddress(wallet.Address) {
		return nil, errors.New("invalid ethereum address format")
	}

	return wallet, nil
}

func (e *EVMCompatibleChain) CreateHDWallet(ctx context.Context, hdAccountId, hdWalletId int) (*blockchain.WalletDetails, error) {
	hdPath := e.BaseChain.GetDerivedPath(44, 60, hdAccountId, 0, hdWalletId)
	mnemonic, err := e.BaseChain.GetMnemonicForPath(ctx, hdPath)
	if err != nil {
		return nil, err
	}

	wallet, err := e.BaseChain.GetDerivedWallet(mnemonic, hdPath)
	if err != nil {
		return nil, err
	}

	if !e.ValidateAddress(wallet.Address) {
		return nil, errors.New("invalid ethereum address format")
	}

	return wallet, nil
}

func (e *EVMCompatibleChain) Deposit(ctx context.Context, wallet blockchain.WalletDetails, amountRaw string, toAddress string) (*blockchain.TransactionResult, error) {
	return evmDepositNative(ctx, e.Name(), e.ChainID(), e.RPCs(), wallet, amountRaw, toAddress)
}

func (e *EVMCompatibleChain) Withdraw(ctx context.Context, wallet blockchain.WalletDetails, amountRaw string, toAddress string) (*blockchain.TransactionResult, error) {
	return evmWithdrawNative(ctx, e.Name(), e.ChainID(), e.RPCs(), wallet, amountRaw, toAddress)
}

func (e *EVMCompatibleChain) WithdrawToken(ctx context.Context, wallet blockchain.WalletDetails, tokenAddr string, amountRaw string, toAddress string) (*blockchain.TransactionResult, error) {
	return evmWithdrawERC20(ctx, e.Name(), e.ChainID(), e.RPCs(), wallet, tokenAddr, amountRaw, toAddress)
}

func (e *EVMCompatibleChain) Sweep(ctx context.Context, wallet blockchain.WalletDetails) (*blockchain.TransactionResult, error) {
	return evmSweepNative(ctx, e.Name(), e.ChainID(), e.RPCs(), wallet)
}

func (e *EVMCompatibleChain) SweepTo(ctx context.Context, wallet blockchain.WalletDetails, toAddress string) (*blockchain.TransactionResult, error) {
	return evmSweepNativeTo(ctx, e.Name(), e.ChainID(), e.RPCs(), wallet, toAddress)
}

func (e *EVMCompatibleChain) SweepERC20To(ctx context.Context, wallet blockchain.WalletDetails, contractAddr, toAddress string) (*blockchain.TransactionResult, error) {
	return evmSweepERC20To(ctx, e.Name(), e.ChainID(), e.RPCs(), wallet, contractAddr, toAddress)
}

func (e *EVMCompatibleChain) PrefundGas(ctx context.Context, reserveWallet blockchain.WalletDetails, userAddress string) (bool, error) {
	return evmPrefundGas(ctx, e.Name(), e.ChainID(), e.RPCs(), reserveWallet, userAddress, evmGasThreshold(), evmGasPrefundAmount())
}

type evmCompatibleBalance struct {
	Native    *big.Int
	Token     *big.Int
	NativeErr error
	TokenErr  error
}

func failedBalanceResults(addresses []string, balance string, err error) []models.BalanceResult {
	if len(addresses) == 0 {
		return nil
	}
	out := make([]models.BalanceResult, 0, len(addresses))
	for _, addr := range addresses {
		out = append(out, models.BalanceResult{
			Address: addr,
			Balance: balance,
			Error:   err,
		})
	}
	return out
}

func (e *EVMCompatibleChain) BatchBalances(ctx context.Context, addresses []string, workers int) []models.BalanceResult {
	if len(addresses) == 0 {
		return nil
	}

	client, selectedRPC, err := dialFirstHealthyEVMRPCWithURL(ctx, e.RPCHttp)
	if err != nil {
		log.Println("RPC dial error:", err)
		return failedBalanceResults(addresses, fmt.Sprintf("%s:0 | %s:0", e.NativeSymbol, e.TokenSymbol), err)
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
				invalidAddresses[addr] = fmt.Errorf("invalid %s address: %s", e.Name(), addr)
				continue
			}
			validBatch = append(validBatch, addr)
		}

		balances := make(map[string]evmCompatibleBalance, len(validBatch))
		if len(validBatch) > 0 {
			balances, err = e.getNativeTokenBalances(ctx, client, validBatch)
			if err != nil {
				log.Printf("[%s] balance batch recovered with partial fallback: %v\n", e.Name(), err)
			}
		}

		for _, addr := range batch {
			if invalidErr := invalidAddresses[addr]; invalidErr != nil {
				out = append(out, models.BalanceResult{
					Address: addr,
					Balance: fmt.Sprintf("%s:%s | %s:%s", e.NativeSymbol, formatWei(big.NewInt(0)), e.TokenSymbol, formatWei(big.NewInt(0))),
					Error:   invalidErr,
				})
				continue
			}

			balance, ok := balances[addr]
			if !ok {
				balance = evmCompatibleBalance{
					Native:    big.NewInt(0),
					Token:     big.NewInt(0),
					NativeErr: err,
					TokenErr:  err,
				}
			}
			nativeBalance := ensureBigInt(balance.Native)
			tokenBalance := ensureBigInt(balance.Token)
			callErr := errors.Join(balance.NativeErr, balance.TokenErr)
			if callErr == nil && nativeBalance.Sign() == 0 && tokenBalance.Sign() == 0 {
				continue
			}

			out = append(out, models.BalanceResult{
				Address: addr,
				Balance: fmt.Sprintf("%s:%s | %s:%s", e.NativeSymbol, formatWei(nativeBalance), e.TokenSymbol, formatWei(tokenBalance)),
				Error:   callErr,
			})
		}
	}

	return out
}

func (e *EVMCompatibleChain) getNativeTokenBalances(ctx context.Context, client *ethclient.Client, addresses []string) (map[string]evmCompatibleBalance, error) {
	balances, err := e.getNativeTokenBalancesMulticall(ctx, client, addresses)
	if err == nil || len(addresses) <= 1 {
		if err == nil {
			return balances, nil
		}
		return e.getNativeTokenBalancesDirect(ctx, client, addresses)
	}

	mid := len(addresses) / 2
	leftBalances, leftErr := e.getNativeTokenBalances(ctx, client, addresses[:mid])
	rightBalances, rightErr := e.getNativeTokenBalances(ctx, client, addresses[mid:])

	merged := make(map[string]evmCompatibleBalance, len(addresses))
	for address, balance := range leftBalances {
		merged[address] = balance
	}
	for address, balance := range rightBalances {
		merged[address] = balance
	}

	return merged, errors.Join(leftErr, rightErr)
}

func (e *EVMCompatibleChain) getNativeTokenBalancesMulticall(ctx context.Context, client *ethclient.Client, addresses []string) (map[string]evmCompatibleBalance, error) {
	if len(addresses) == 0 {
		return map[string]evmCompatibleBalance{}, nil
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
	tokenAddress := common.HexToAddress(e.TokenAddress)
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

	balances := make(map[string]evmCompatibleBalance, len(addresses))
	for i, rawAddress := range addresses {
		nativeCallResult := results[i*2]
		tokenCallResult := results[i*2+1]

		entry := evmCompatibleBalance{
			Native: big.NewInt(0),
			Token:  big.NewInt(0),
		}

		if nativeCallResult.Success {
			entry.Native, entry.NativeErr = unpackUint256(multicallABI, "getEthBalance", nativeCallResult.ReturnData)
		} else {
			entry.NativeErr = fmt.Errorf("multicall3 getEthBalance failed for %s", rawAddress)
		}

		if tokenCallResult.Success {
			entry.Token, entry.TokenErr = unpackUint256(erc20ABI, "balanceOf", tokenCallResult.ReturnData)
		} else {
			entry.TokenErr = fmt.Errorf("%s balanceOf failed for %s", e.TokenSymbol, rawAddress)
		}

		entry.Native = ensureBigInt(entry.Native)
		entry.Token = ensureBigInt(entry.Token)
		balances[rawAddress] = entry
	}

	return balances, nil
}

func (e *EVMCompatibleChain) getNativeTokenBalancesDirect(ctx context.Context, client *ethclient.Client, addresses []string) (map[string]evmCompatibleBalance, error) {
	balances := make(map[string]evmCompatibleBalance, len(addresses))
	var errs []error

	tokenContract, contractErr := erc20.NewERC20Caller(common.HexToAddress(e.TokenAddress), client)
	if contractErr != nil {
		errs = append(errs, contractErr)
	}

	for _, rawAddress := range addresses {
		address := common.HexToAddress(rawAddress)
		entry := evmCompatibleBalance{
			Native: big.NewInt(0),
			Token:  big.NewInt(0),
		}

		nativeBalance, err := client.BalanceAt(ctx, address, nil)
		if err != nil {
			entry.NativeErr = err
			errs = append(errs, fmt.Errorf("%s balance %s: %w", e.NativeSymbol, rawAddress, err))
		} else {
			entry.Native = nativeBalance
		}

		if tokenContract == nil {
			entry.TokenErr = contractErr
		} else {
			tokenBalance, err := tokenContract.BalanceOf(&bind.CallOpts{Context: ctx}, address)
			if err != nil {
				entry.TokenErr = err
				errs = append(errs, fmt.Errorf("%s balance %s: %w", e.TokenSymbol, rawAddress, err))
			} else {
				entry.Token = tokenBalance
			}
		}

		entry.Native = ensureBigInt(entry.Native)
		entry.Token = ensureBigInt(entry.Token)
		balances[rawAddress] = entry
	}

	return balances, errors.Join(errs...)
}
