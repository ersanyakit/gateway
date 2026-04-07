package chains

import (
	"bytes"
	"context"
	blockchain "core/blockchain"
	"core/constants"
	"core/models"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/okx/go-wallet-sdk/coins/tron"
	tronSDK "github.com/okx/go-wallet-sdk/coins/tron"
)

type TronChain struct {
	blockchain.BaseChain
}

func NewTronChain() *TronChain {
	return &TronChain{
		blockchain.BaseChain{
			ID:          constants.TRON,
			ChainName:   "tron",
			ExplorerURL: "https://tronscan.org/",
			RPCHttp:     []string{"https://api.trongrid.io/jsonrpc", "https://rpc.ankr.com/tron_jsonrpc"},
		},
	}
}

func (e *TronChain) Name() string {
	return e.ChainName
}

func (e *TronChain) ChainID() constants.ChainID {
	return e.ID
}

func (t *TronChain) NewAddress(privateKeyHex string) (string, error) {
	prvBytes, err := hex.DecodeString(privateKeyHex)
	if err != nil {
		return "", fmt.Errorf("hex decode failed: %w", err)
	}

	privKey, pubKey := btcec.PrivKeyFromBytes(prvBytes)
	if privKey == nil || pubKey == nil {
		return "", fmt.Errorf("invalid private key bytes or nil public key")
	}

	address := tron.GetAddress(pubKey)
	return address, nil
}

func (s *TronChain) ValidateAddress(address string) bool {
	return tronSDK.ValidateAddress(address)
}

func (s *TronChain) Create(ctx context.Context) (*blockchain.WalletDetails, error) {
	fmt.Printf("[%s]: Creating wallet\n", s.Name())

	mnemonic, err := s.BaseChain.GenerateMnemonicPhrase()
	if err != nil {
		return nil, err
	}

	hdPath := s.BaseChain.GetDerivedPath(44, 195, 0, 0, 0)
	privateKey, err := s.BaseChain.GetDerivedPrivateKey(mnemonic, hdPath)
	if err != nil {
		return nil, err
	}
	address, err := s.NewAddress(privateKey)
	if err != nil {
		log.Printf("[%s] NewAddress error:%s \n", s.BaseChain.Name(), err.Error())
	}

	if !s.ValidateAddress(address) {
		return nil, errors.New("invalid tron address format")

	}

	return &blockchain.WalletDetails{
		Address:        address,
		PrivateKey:     privateKey,
		MnemonicPhrase: mnemonic,
	}, nil
}

func (s *TronChain) CreateHDWallet(ctx context.Context, hdAccountId, hdWalletId int) (*blockchain.WalletDetails, error) {
	fmt.Printf("[%s]: Creating HD wallet\n", s.Name())

	mnemonic, err := s.BaseChain.GetMnemonic()
	if err != nil {
		return nil, err
	}

	hdPath := s.BaseChain.GetDerivedPath(44, 195, 0, hdAccountId, hdWalletId)
	privateKey, err := s.BaseChain.GetDerivedPrivateKey(mnemonic, hdPath)
	if err != nil {
		return nil, err
	}
	address, err := s.NewAddress(privateKey)
	if err != nil {
		log.Printf("[%s] NewAddress error:%s \n", s.BaseChain.Name(), err.Error())
	}

	if !s.ValidateAddress(address) {
		return nil, errors.New("invalid tron address format")

	}

	return &blockchain.WalletDetails{
		Address:        address,
		PrivateKey:     privateKey,
		MnemonicPhrase: mnemonic,
	}, nil
}

func (s *TronChain) Deposit(ctx context.Context, wallet blockchain.WalletDetails, amount float64, toAddress string) (*blockchain.TransactionResult, error) {
	fmt.Printf("[%s]: Depositing %f to %s\n", s.Name(), amount, toAddress)
	return &blockchain.TransactionResult{TxHash: "DepositTxHash", Success: true}, nil
}

func (s *TronChain) Withdraw(ctx context.Context, wallet blockchain.WalletDetails, amount float64, toAddress string) (*blockchain.TransactionResult, error) {
	fmt.Printf("[%s]: Withdrawing %f from %s\n", s.Name(), amount, wallet.Address)
	return &blockchain.TransactionResult{TxHash: "WithdrawTxHash", Success: true}, nil
}

func (s *TronChain) Sweep(ctx context.Context, wallet blockchain.WalletDetails) (*blockchain.TransactionResult, error) {
	fmt.Printf("[%s]: Sweeping wallet", s.Name())
	return &blockchain.TransactionResult{TxHash: "SweepTxHash", Success: true}, nil
}

func (e *TronChain) BatchBalances(ctx context.Context, addresses []string, workers int) []models.BalanceResult {
	jobs := make(chan string, len(addresses))
	results := make(chan models.BalanceResult, len(addresses))

	client := &http.Client{Timeout: 10 * time.Second}

	var wg sync.WaitGroup

	workerFunc := func() {
		defer wg.Done()
		for addr := range jobs {
			balance, err := e.getBalance(client, addr)
			results <- models.BalanceResult{
				Address: addr,
				Balance: balance,
				Error:   err,
			}
		}
	}

	// worker başlat
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go workerFunc()
	}

	// iş kuyruğuna adresleri koy
	for _, addr := range addresses {
		jobs <- addr
	}
	close(jobs)

	wg.Wait()
	close(results)

	var out []models.BalanceResult
	for r := range results {
		out = append(out, r)
	}
	return out
}

func (e *TronChain) getBalance(client *http.Client, address string) (string, error) {
	// en iyi RPC seçimi (round-robin basit)
	rpc := e.RPCHttp[0]

	reqBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "eth_getBalance",
		"params":  []interface{}{address, "latest"},
	}

	data, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", rpc, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var res struct {
		Result string          `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}
	if res.Error != nil {
		return "", fmt.Errorf("rpc error")
	}

	return res.Result, nil
}
