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

type ChilizChain struct {
	blockchain.BaseChain
}

func NewChilizChain() *ChilizChain {
	return &ChilizChain{
		blockchain.BaseChain{
			ID:          constants.Chiliz,
			ChainName:   "chiliz",
			ExplorerURL: "https://chiliscan.io",
			RPCHttp:     []string{"https://rpc.chiliz.com"},
			WebSockets:  []string{"https://rpc.chiliz.com"},
		}}
}

func (e *ChilizChain) Name() string {
	return e.ChainName
}

func (e *ChilizChain) ChainID() constants.ChainID {
	return e.ID
}

func (e *ChilizChain) RPCs() []string {
	return e.RPCHttp
}

func (e *ChilizChain) Explorer() string {
	return e.ExplorerURL
}

func (e *ChilizChain) WSS() []string {
	return e.WebSockets
}

func (e *ChilizChain) NewAddress(prvHex string) (string, error) {
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

func (s *ChilizChain) ValidateAddress(address string) bool {
	return ethSDK.ValidateAddress(address)
}

func (s *ChilizChain) Create(ctx context.Context) (*blockchain.WalletDetails, error) {
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

func (s *ChilizChain) CreateHDWallet(ctx context.Context, hdAccountId, hdWalletId int) (*blockchain.WalletDetails, error) {
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

func (s *ChilizChain) Deposit(ctx context.Context, wallet blockchain.WalletDetails, amount float64, toAddress string) (*blockchain.TransactionResult, error) {
	fmt.Printf("[%s]: Depositing %f to %s\n", s.Name(), amount, toAddress)
	return &blockchain.TransactionResult{TxHash: "DepositTxHash", Success: true}, nil
}

func (s *ChilizChain) Withdraw(ctx context.Context, wallet blockchain.WalletDetails, amount float64, toAddress string) (*blockchain.TransactionResult, error) {
	fmt.Printf("[%s]: Withdrawing %f from %s\n", s.Name(), amount, wallet.Address)
	return &blockchain.TransactionResult{TxHash: "WithdrawTxHash", Success: true}, nil
}

func (s *ChilizChain) Sweep(ctx context.Context, wallet blockchain.WalletDetails) (*blockchain.TransactionResult, error) {
	fmt.Printf("[%s]: Sweeping wallet", s.Name())
	return &blockchain.TransactionResult{TxHash: "SweepTxHash", Success: true}, nil
}

const CHILIZ_SYMBOL = "CHZ"
const WCHZ_SYMBOL = "WCHZ"
const WCHZ_ADDRESS = "0x721EF6871f1c4Efe730Dce047D40D1743B886946"

type chilizMulticallBalance struct {
	CHZ     *big.Int
	WCHZ    *big.Int
	CHZErr  error
	WCHZErr error
}

func (e *ChilizChain) BatchBalances(ctx context.Context, addresses []string, workers int) []models.BalanceResult {
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
				invalidAddresses[addr] = fmt.Errorf("invalid chiliz address: %s", addr)
				continue
			}
			validBatch = append(validBatch, addr)
		}

		balances := make(map[string]chilizMulticallBalance, len(validBatch))
		if len(validBatch) > 0 {
			balances, err = e.getCHZWCHZBalances(ctx, client, validBatch)
			if err != nil {
				log.Printf("[%s] balance batch recovered with partial fallback: %v\n", e.Name(), err)
			}
		}

		for _, addr := range batch {
			if invalidErr := invalidAddresses[addr]; invalidErr != nil {
				fmt.Printf("[%s] balance %s ERROR: %v\n", e.Name(), addr, invalidErr)
				out = append(out, models.BalanceResult{
					Address: addr,
					Balance: fmt.Sprintf("%s:%s | %s:%s", CHILIZ_SYMBOL, formatWei(big.NewInt(0)), WCHZ_SYMBOL, formatWei(big.NewInt(0))),
					Error:   invalidErr,
				})
				continue
			}

			balance, ok := balances[addr]
			if !ok {
				balance = chilizMulticallBalance{
					CHZ:     big.NewInt(0),
					WCHZ:    big.NewInt(0),
					CHZErr:  err,
					WCHZErr: err,
				}
			}
			chzBalance := ensureBigInt(balance.CHZ)
			wchzBalance := ensureBigInt(balance.WCHZ)
			callErr := errors.Join(balance.CHZErr, balance.WCHZErr)
			if callErr == nil && chzBalance.Sign() == 0 && wchzBalance.Sign() == 0 {
				continue
			}

			fmt.Printf(
				"[%s] balance %s %s=%s wei (%s) %s=%s wei (%s)\n",
				e.Name(),
				addr,
				CHILIZ_SYMBOL,
				chzBalance.String(),
				formatWei(chzBalance),
				WCHZ_SYMBOL,
				wchzBalance.String(),
				formatWei(wchzBalance),
			)

			out = append(out, models.BalanceResult{
				Address: addr,
				Balance: fmt.Sprintf("%s:%s | %s:%s", CHILIZ_SYMBOL, formatWei(chzBalance), WCHZ_SYMBOL, formatWei(wchzBalance)),
				Error:   callErr,
			})
		}
	}

	return out
}

func (e *ChilizChain) getCHZWCHZBalances(ctx context.Context, client *ethclient.Client, addresses []string) (map[string]chilizMulticallBalance, error) {
	balances, err := e.getCHZWCHZBalancesMulticall(ctx, client, addresses)
	if err == nil || len(addresses) <= 1 {
		if err == nil {
			return balances, nil
		}
		return e.getCHZWCHZBalancesDirect(ctx, client, addresses)
	}

	mid := len(addresses) / 2
	leftBalances, leftErr := e.getCHZWCHZBalances(ctx, client, addresses[:mid])
	rightBalances, rightErr := e.getCHZWCHZBalances(ctx, client, addresses[mid:])

	merged := make(map[string]chilizMulticallBalance, len(addresses))
	for address, balance := range leftBalances {
		merged[address] = balance
	}
	for address, balance := range rightBalances {
		merged[address] = balance
	}

	return merged, errors.Join(leftErr, rightErr)
}

func (e *ChilizChain) getCHZWCHZBalancesMulticall(ctx context.Context, client *ethclient.Client, addresses []string) (map[string]chilizMulticallBalance, error) {
	if len(addresses) == 0 {
		return map[string]chilizMulticallBalance{}, nil
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
	wchzAddress := common.HexToAddress(WCHZ_ADDRESS)
	calls := make([]multicall3.Multicall3Call3, 0, len(addresses)*2)

	for _, rawAddress := range addresses {
		address := common.HexToAddress(rawAddress)
		nativeCallData, err := multicallABI.Pack("getEthBalance", address)
		if err != nil {
			return nil, fmt.Errorf("pack getEthBalance for %s: %w", rawAddress, err)
		}

		wchzCallData, err := erc20ABI.Pack("balanceOf", address)
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
				Target:       wchzAddress,
				AllowFailure: true,
				CallData:     wchzCallData,
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

	balances := make(map[string]chilizMulticallBalance, len(addresses))
	for i, rawAddress := range addresses {
		chzCallResult := results[i*2]
		wchzCallResult := results[i*2+1]

		entry := chilizMulticallBalance{
			CHZ:  big.NewInt(0),
			WCHZ: big.NewInt(0),
		}

		if chzCallResult.Success {
			entry.CHZ, entry.CHZErr = unpackUint256(multicallABI, "getEthBalance", chzCallResult.ReturnData)
		} else {
			entry.CHZErr = fmt.Errorf("multicall3 getEthBalance failed for %s", rawAddress)
		}

		if wchzCallResult.Success {
			entry.WCHZ, entry.WCHZErr = unpackUint256(erc20ABI, "balanceOf", wchzCallResult.ReturnData)
		} else {
			entry.WCHZErr = fmt.Errorf("%s balanceOf failed for %s", WCHZ_SYMBOL, rawAddress)
		}

		entry.CHZ = ensureBigInt(entry.CHZ)
		entry.WCHZ = ensureBigInt(entry.WCHZ)
		balances[rawAddress] = entry
	}

	return balances, nil
}

func (e *ChilizChain) getCHZWCHZBalancesDirect(ctx context.Context, client *ethclient.Client, addresses []string) (map[string]chilizMulticallBalance, error) {
	balances := make(map[string]chilizMulticallBalance, len(addresses))
	var errs []error
	wchzContract, contractErr := erc20.NewERC20Caller(common.HexToAddress(WCHZ_ADDRESS), client)
	if contractErr != nil {
		errs = append(errs, contractErr)
	}

	for _, rawAddress := range addresses {
		entry := chilizMulticallBalance{
			CHZ:  big.NewInt(0),
			WCHZ: big.NewInt(0),
		}

		balance, err := client.BalanceAt(ctx, common.HexToAddress(rawAddress), nil)
		if err != nil {
			entry.CHZErr = err
			errs = append(errs, fmt.Errorf("%s balance %s: %w", CHILIZ_SYMBOL, rawAddress, err))
		} else {
			entry.CHZ = balance
		}

		if wchzContract == nil {
			entry.WCHZErr = contractErr
		} else {
			wchzBalance, err := wchzContract.BalanceOf(&bind.CallOpts{Context: ctx}, common.HexToAddress(rawAddress))
			if err != nil {
				entry.WCHZErr = err
				errs = append(errs, fmt.Errorf("%s balance %s: %w", WCHZ_SYMBOL, rawAddress, err))
			} else {
				entry.WCHZ = wchzBalance
			}
		}

		entry.CHZ = ensureBigInt(entry.CHZ)
		entry.WCHZ = ensureBigInt(entry.WCHZ)
		balances[rawAddress] = entry
	}

	return balances, errors.Join(errs...)
}
