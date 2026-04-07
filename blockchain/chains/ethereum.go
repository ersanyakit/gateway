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

type EthereumChain struct {
	blockchain.BaseChain
}

func NewEthereumChain() *EthereumChain {
	return &EthereumChain{
		blockchain.BaseChain{
			ID:          constants.Ethereum,
			ChainName:   "ethereum",
			ExplorerURL: "https://etherscan.io",
			RPCHttp:     []string{"https://eth.drpc.org", "https://mainnet.infura.io/v3/ac1242cf6a134cc3a77530953a7b65d5", "https://ethereum-rpc.publicnode.com", "https://1rpc.io/eth", "https://1rpc.io/eth"},
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

const MULTICALL3_ADDRESS = "0xcA11bde05977b3631167028862bE2a173976CA11"
const WETH_ADDRESS = "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2"
const ERC20_SYMBOL = "WETH"

type multicallBalance struct {
	ETH      *big.Int
	ERC20    *big.Int
	ETHErr   error
	ERC20Err error
}

// -------------------- BATCH --------------------
func (e *EthereumChain) BatchBalances(ctx context.Context, addresses []string, workers int) []models.BalanceResult {
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
				invalidAddresses[addr] = fmt.Errorf("invalid ethereum address: %s", addr)
				continue
			}
			validBatch = append(validBatch, addr)
		}

		balances := make(map[string]multicallBalance, len(validBatch))
		if len(validBatch) > 0 {
			balances, err = e.getETHWETHBalances(ctx, client, validBatch)
			if err != nil {
				log.Printf("[%s] balance batch recovered with partial fallback: %v\n", e.Name(), err)
			}
		}

		for _, addr := range batch {
			if invalidErr := invalidAddresses[addr]; invalidErr != nil {
				fmt.Printf("[%s] balance %s ERROR: %v\n", e.Name(), addr, invalidErr)
				out = append(out, models.BalanceResult{
					Address: addr,
					Balance: fmt.Sprintf("ETH:%s | %s:%s", formatWei(big.NewInt(0)), ERC20_SYMBOL, formatWei(big.NewInt(0))),
					Error:   invalidErr,
				})
				continue
			}

			balance, ok := balances[addr]
			if !ok {
				balance = multicallBalance{
					ETH:      big.NewInt(0),
					ERC20:    big.NewInt(0),
					ETHErr:   err,
					ERC20Err: err,
				}
			}
			ethBalance := ensureBigInt(balance.ETH)
			erc20Balance := ensureBigInt(balance.ERC20)
			callErr := errors.Join(balance.ETHErr, balance.ERC20Err)
			if callErr == nil && ethBalance.Sign() == 0 && erc20Balance.Sign() == 0 {
				continue
			}

			fmt.Printf(
				"[%s] balance %s ETH=%s wei (%s) %s=%s wei (%s)\n",
				e.Name(),
				addr,
				ethBalance.String(),
				formatWei(ethBalance),
				ERC20_SYMBOL,
				erc20Balance.String(),
				formatWei(erc20Balance),
			)

			out = append(out, models.BalanceResult{
				Address: addr,
				Balance: fmt.Sprintf("ETH:%s | %s:%s", formatWei(ethBalance), ERC20_SYMBOL, formatWei(erc20Balance)),
				Error:   callErr,
			})
		}
	}

	return out
}

// -------------------- MULTICALL --------------------
func (e *EthereumChain) getETHWETHBalances(ctx context.Context, client *ethclient.Client, addresses []string) (map[string]multicallBalance, error) {
	balances, err := e.getETHWETHBalancesMulticall(ctx, client, addresses)
	if err == nil || len(addresses) <= 1 {
		if err == nil {
			return balances, nil
		}
		return e.getETHWETHBalancesDirect(ctx, client, addresses)
	}

	mid := len(addresses) / 2
	leftBalances, leftErr := e.getETHWETHBalances(ctx, client, addresses[:mid])
	rightBalances, rightErr := e.getETHWETHBalances(ctx, client, addresses[mid:])

	merged := make(map[string]multicallBalance, len(addresses))
	for address, balance := range leftBalances {
		merged[address] = balance
	}
	for address, balance := range rightBalances {
		merged[address] = balance
	}

	return merged, errors.Join(leftErr, rightErr)
}

func (e *EthereumChain) getETHWETHBalancesMulticall(ctx context.Context, client *ethclient.Client, addresses []string) (map[string]multicallBalance, error) {
	if len(addresses) == 0 {
		return map[string]multicallBalance{}, nil
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
	wethAddress := common.HexToAddress(WETH_ADDRESS)

	calls := make([]multicall3.Multicall3Call3, 0, len(addresses)*2)
	for _, rawAddress := range addresses {
		address := common.HexToAddress(rawAddress)

		ethCallData, err := multicallABI.Pack("getEthBalance", address)
		if err != nil {
			return nil, fmt.Errorf("pack getEthBalance for %s: %w", rawAddress, err)
		}

		erc20CallData, err := erc20ABI.Pack("balanceOf", address)
		if err != nil {
			return nil, fmt.Errorf("pack balanceOf for %s: %w", rawAddress, err)
		}

		calls = append(calls,
			multicall3.Multicall3Call3{
				Target:       multicallAddress,
				AllowFailure: true,
				CallData:     ethCallData,
			},
			multicall3.Multicall3Call3{
				Target:       wethAddress,
				AllowFailure: true,
				CallData:     erc20CallData,
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

	balances := make(map[string]multicallBalance, len(addresses))
	for i, rawAddress := range addresses {
		ethCallResult := results[i*2]
		erc20CallResult := results[i*2+1]

		entry := multicallBalance{
			ETH:   big.NewInt(0),
			ERC20: big.NewInt(0),
		}

		if ethCallResult.Success {
			entry.ETH, entry.ETHErr = unpackUint256(multicallABI, "getEthBalance", ethCallResult.ReturnData)
		} else {
			entry.ETHErr = fmt.Errorf("multicall3 getEthBalance failed for %s", rawAddress)
		}

		if erc20CallResult.Success {
			entry.ERC20, entry.ERC20Err = unpackUint256(erc20ABI, "balanceOf", erc20CallResult.ReturnData)
		} else {
			entry.ERC20Err = fmt.Errorf("%s balanceOf failed for %s", ERC20_SYMBOL, rawAddress)
		}

		entry.ETH = ensureBigInt(entry.ETH)
		entry.ERC20 = ensureBigInt(entry.ERC20)
		balances[rawAddress] = entry
	}

	return balances, nil
}

func (e *EthereumChain) getETHWETHBalancesDirect(ctx context.Context, client *ethclient.Client, addresses []string) (map[string]multicallBalance, error) {
	balances := make(map[string]multicallBalance, len(addresses))
	var errs []error

	wethContract, contractErr := erc20.NewERC20Caller(common.HexToAddress(WETH_ADDRESS), client)
	if contractErr != nil {
		errs = append(errs, contractErr)
	}

	for _, rawAddress := range addresses {
		address := common.HexToAddress(rawAddress)
		entry := multicallBalance{
			ETH:   big.NewInt(0),
			ERC20: big.NewInt(0),
		}

		ethBalance, err := client.BalanceAt(ctx, address, nil)
		if err != nil {
			entry.ETHErr = err
			errs = append(errs, fmt.Errorf("eth balance %s: %w", rawAddress, err))
		} else {
			entry.ETH = ethBalance
		}

		if wethContract == nil {
			entry.ERC20Err = contractErr
		} else {
			erc20Balance, err := wethContract.BalanceOf(&bind.CallOpts{Context: ctx}, address)
			if err != nil {
				entry.ERC20Err = err
				errs = append(errs, fmt.Errorf("%s balance %s: %w", ERC20_SYMBOL, rawAddress, err))
			} else {
				entry.ERC20 = erc20Balance
			}
		}

		entry.ETH = ensureBigInt(entry.ETH)
		entry.ERC20 = ensureBigInt(entry.ERC20)
		balances[rawAddress] = entry
	}

	return balances, errors.Join(errs...)
}

func unpackUint256(contractABI *abi.ABI, method string, data []byte) (*big.Int, error) {
	values, err := contractABI.Unpack(method, data)
	if err != nil {
		return nil, fmt.Errorf("unpack %s result: %w", method, err)
	}

	if len(values) != 1 {
		return nil, fmt.Errorf("unexpected %s output count: %d", method, len(values))
	}

	return ensureBigInt(*abi.ConvertType(values[0], new(*big.Int)).(**big.Int)), nil
}

func ensureBigInt(value *big.Int) *big.Int {
	if value == nil {
		return big.NewInt(0)
	}

	return value
}

func formatWei(value *big.Int) string {
	value = ensureBigInt(value)

	numerator := new(big.Float).SetInt(value)
	denominator := new(big.Float).SetInt(big.NewInt(1_000_000_000_000_000_000))

	return new(big.Float).Quo(numerator, denominator).Text('f', 18)
}
