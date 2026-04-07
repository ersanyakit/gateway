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

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
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

const AVALANCHE_SYMBOL = "AVAX"
const AVALANCHE_TOKEN_SYMBOL = "WBTC"
const AVALANCHE_TOKEN_ADDRESS = "0xb2a85C5ECea99187A977aC34303b80AcbDdFa208"

type avalancheMulticallBalance struct {
	AVAX     *big.Int
	Token    *big.Int
	AVAXErr  error
	TokenErr error
}

func (e *AvalancheChain) BatchBalances(ctx context.Context, addresses []string, workers int) []models.BalanceResult {
	if len(addresses) == 0 {
		return nil
	}

	client, err := ethclient.Dial(e.RPCHttp[0])
	if err != nil {
		log.Println("RPC dial error:", err)
		return nil
	}
	defer client.Close()

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
				invalidAddresses[addr] = fmt.Errorf("invalid avalanche address: %s", addr)
				continue
			}
			validBatch = append(validBatch, addr)
		}

		balances := make(map[string]avalancheMulticallBalance, len(validBatch))
		if len(validBatch) > 0 {
			balances, err = e.getAVAXTokenBalances(ctx, client, validBatch)
			if err != nil {
				log.Printf("[%s] balance batch recovered with partial fallback: %v\n", e.Name(), err)
			}
		}

		for _, addr := range batch {
			if invalidErr := invalidAddresses[addr]; invalidErr != nil {
				fmt.Printf("[%s] balance %s ERROR: %v\n", e.Name(), addr, invalidErr)
				out = append(out, models.BalanceResult{
					Address: addr,
					Balance: fmt.Sprintf("%s:%s | %s:%s", AVALANCHE_SYMBOL, formatWei(big.NewInt(0)), AVALANCHE_TOKEN_SYMBOL, formatWei(big.NewInt(0))),
					Error:   invalidErr,
				})
				continue
			}

			balance, ok := balances[addr]
			if !ok {
				balance = avalancheMulticallBalance{
					AVAX:     big.NewInt(0),
					Token:    big.NewInt(0),
					AVAXErr:  err,
					TokenErr: err,
				}
			}

			avaxBalance := ensureBigInt(balance.AVAX)
			tokenBalance := ensureBigInt(balance.Token)
			callErr := errors.Join(balance.AVAXErr, balance.TokenErr)
			if callErr == nil && avaxBalance.Sign() == 0 && tokenBalance.Sign() == 0 {
				continue
			}

			fmt.Printf(
				"[%s] balance %s %s=%s wei (%s) %s=%s wei (%s)\n",
				e.Name(),
				addr,
				AVALANCHE_SYMBOL,
				avaxBalance.String(),
				formatWei(avaxBalance),
				AVALANCHE_TOKEN_SYMBOL,
				tokenBalance.String(),
				formatWei(tokenBalance),
			)

			out = append(out, models.BalanceResult{
				Address: addr,
				Balance: fmt.Sprintf("%s:%s | %s:%s", AVALANCHE_SYMBOL, formatWei(avaxBalance), AVALANCHE_TOKEN_SYMBOL, formatWei(tokenBalance)),
				Error:   callErr,
			})
		}
	}

	return out
}

func (e *AvalancheChain) getAVAXTokenBalances(ctx context.Context, client *ethclient.Client, addresses []string) (map[string]avalancheMulticallBalance, error) {
	balances, err := e.getAVAXTokenBalancesMulticall(ctx, client, addresses)
	if err == nil || len(addresses) <= 1 {
		if err == nil {
			return balances, nil
		}
		return e.getAVAXTokenBalancesDirect(ctx, client, addresses)
	}

	mid := len(addresses) / 2
	leftBalances, leftErr := e.getAVAXTokenBalances(ctx, client, addresses[:mid])
	rightBalances, rightErr := e.getAVAXTokenBalances(ctx, client, addresses[mid:])

	merged := make(map[string]avalancheMulticallBalance, len(addresses))
	for address, balance := range leftBalances {
		merged[address] = balance
	}
	for address, balance := range rightBalances {
		merged[address] = balance
	}

	return merged, errors.Join(leftErr, rightErr)
}

func (e *AvalancheChain) getAVAXTokenBalancesMulticall(ctx context.Context, client *ethclient.Client, addresses []string) (map[string]avalancheMulticallBalance, error) {
	if len(addresses) == 0 {
		return map[string]avalancheMulticallBalance{}, nil
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
	tokenAddress := common.HexToAddress(AVALANCHE_TOKEN_ADDRESS)
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

	balances := make(map[string]avalancheMulticallBalance, len(addresses))
	for i, rawAddress := range addresses {
		nativeCallResult := results[i*2]
		tokenCallResult := results[i*2+1]

		entry := avalancheMulticallBalance{
			AVAX:  big.NewInt(0),
			Token: big.NewInt(0),
		}

		if nativeCallResult.Success {
			entry.AVAX, entry.AVAXErr = unpackUint256(multicallABI, "getEthBalance", nativeCallResult.ReturnData)
		} else {
			entry.AVAXErr = fmt.Errorf("multicall3 getEthBalance failed for %s", rawAddress)
		}

		if tokenCallResult.Success {
			entry.Token, entry.TokenErr = unpackUint256(erc20ABI, "balanceOf", tokenCallResult.ReturnData)
		} else {
			entry.TokenErr = fmt.Errorf("%s balanceOf failed for %s", AVALANCHE_TOKEN_SYMBOL, rawAddress)
		}

		entry.AVAX = ensureBigInt(entry.AVAX)
		entry.Token = ensureBigInt(entry.Token)
		balances[rawAddress] = entry
	}

	return balances, nil
}

func (e *AvalancheChain) getAVAXTokenBalancesDirect(ctx context.Context, client *ethclient.Client, addresses []string) (map[string]avalancheMulticallBalance, error) {
	balances := make(map[string]avalancheMulticallBalance, len(addresses))
	var errs []error

	tokenContract, contractErr := erc20.NewERC20Caller(common.HexToAddress(AVALANCHE_TOKEN_ADDRESS), client)
	if contractErr != nil {
		errs = append(errs, contractErr)
	}

	for _, rawAddress := range addresses {
		address := common.HexToAddress(rawAddress)
		entry := avalancheMulticallBalance{
			AVAX:  big.NewInt(0),
			Token: big.NewInt(0),
		}

		avaxBalance, err := client.BalanceAt(ctx, address, nil)
		if err != nil {
			entry.AVAXErr = err
			errs = append(errs, fmt.Errorf("%s balance %s: %w", AVALANCHE_SYMBOL, rawAddress, err))
		} else {
			entry.AVAX = avaxBalance
		}

		if tokenContract == nil {
			entry.TokenErr = contractErr
		} else {
			tokenBalance, err := tokenContract.BalanceOf(&bind.CallOpts{Context: ctx}, address)
			if err != nil {
				entry.TokenErr = err
				errs = append(errs, fmt.Errorf("%s balance %s: %w", AVALANCHE_TOKEN_SYMBOL, rawAddress, err))
			} else {
				entry.Token = tokenBalance
			}
		}

		entry.AVAX = ensureBigInt(entry.AVAX)
		entry.Token = ensureBigInt(entry.Token)
		balances[rawAddress] = entry
	}

	return balances, errors.Join(errs...)
}
