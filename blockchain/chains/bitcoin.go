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
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
)

type AddressType uint32

const (
	Legacy       AddressType = 44
	NestedSegWit AddressType = 49
	NativeSegWit AddressType = 84
	Taproot      AddressType = 86
)

type BitcoinChain struct {
	blockchain.BaseChain
	Params *chaincfg.Params
}

func NewBitcoinChain() *BitcoinChain {
	return &BitcoinChain{
		BaseChain: blockchain.BaseChain{
			ID:          constants.Bitcoin,
			ChainName:   "bitcoin",
			ExplorerURL: "https://www.blockchain.com/explorer",
		},
		Params: &chaincfg.MainNetParams,
	}
}

func (e *BitcoinChain) Name() string {
	return e.ChainName
}

func (e *BitcoinChain) ChainID() constants.ChainID {
	return e.ID
}

func (b *BitcoinChain) NewAddressLegacy(prvHex string) (string, error) {
	prvBytes, err := hex.DecodeString(prvHex)
	if err != nil {
		return "", errors.New("invalid private key hex: " + err.Error())
	}

	privKey, _ := btcec.PrivKeyFromBytes(prvBytes)

	pubKey := privKey.PubKey()
	pubKeyHash := btcutil.Hash160(pubKey.SerializeCompressed())

	address, err := btcutil.NewAddressPubKeyHash(pubKeyHash, b.Params)
	if err != nil {
		return "", err
	}

	return address.EncodeAddress(), nil
}

func (b *BitcoinChain) NewAddressSegwit(prvHex string) (string, error) {
	prvBytes, err := hex.DecodeString(prvHex)
	if err != nil {
		return "", errors.New("invalid private key hex: " + err.Error())
	}

	privKey, _ := btcec.PrivKeyFromBytes(prvBytes)
	pubKey := privKey.PubKey()

	pubKeyHash := btcutil.Hash160(pubKey.SerializeCompressed())

	address, err := btcutil.NewAddressWitnessPubKeyHash(pubKeyHash, b.Params)
	if err != nil {
		return "", err
	}

	return address.EncodeAddress(), nil
}

func (b *BitcoinChain) NewAddress(prvHex string) (string, error) {
	prvBytes, err := hex.DecodeString(prvHex)
	if err != nil {
		return "", errors.New("invalid private key hex: " + err.Error())
	}

	privKey, _ := btcec.PrivKeyFromBytes(prvBytes)
	pubKey := privKey.PubKey()

	pubKeyHash := btcutil.Hash160(pubKey.SerializeCompressed())

	address, err := btcutil.NewAddressWitnessPubKeyHash(pubKeyHash, b.Params)
	if err != nil {
		return "", err
	}

	return address.EncodeAddress(), nil
}

func (b *BitcoinChain) ValidateAddress(address string) bool {
	_, err := btcutil.DecodeAddress(address, b.Params)
	return err == nil
}

func (b *BitcoinChain) Create(ctx context.Context) (*blockchain.WalletDetails, error) {
	fmt.Printf("[%s]: Creating wallet\n", b.Name())

	mnemonic, err := b.BaseChain.GenerateMnemonicPhrase()
	if err != nil {
		return nil, err
	}

	hdPath := b.BaseChain.GetDerivedPath(int(Taproot), 0, 0, 0, 1)
	privateKeyHex, err := b.BaseChain.GetDerivedPrivateKey(mnemonic, hdPath)
	if err != nil {
		return nil, err
	}

	address, err := b.NewAddress(privateKeyHex)
	if err != nil {
		log.Printf("[%s] NewAddress error: %s\n", b.Name(), err.Error())
		return nil, err
	}

	if !b.ValidateAddress(address) {
		return nil, errors.New("invalid bitcoin address format")
	}

	return &blockchain.WalletDetails{
		Address:        address,
		PrivateKey:     privateKeyHex,
		MnemonicPhrase: mnemonic,
	}, nil
}

func (b *BitcoinChain) CreateHDWallet(ctx context.Context, hdAccountId, hdWalletId int) (*blockchain.WalletDetails, error) {
	fmt.Printf("[%s]: Creating wallet\n", b.Name())

	mnemonic, err := b.BaseChain.GetMnemonic()
	if err != nil {
		return nil, err
	}

	hdPath := b.BaseChain.GetDerivedPath(int(Taproot), 0, 0, hdAccountId, hdWalletId)
	privateKeyHex, err := b.BaseChain.GetDerivedPrivateKey(mnemonic, hdPath)
	if err != nil {
		return nil, err
	}

	address, err := b.NewAddress(privateKeyHex)
	if err != nil {
		log.Printf("[%s] NewAddress error: %s\n", b.Name(), err.Error())
		return nil, err
	}

	if !b.ValidateAddress(address) {
		return nil, errors.New("invalid bitcoin address format")
	}

	return &blockchain.WalletDetails{
		Address:        address,
		PrivateKey:     privateKeyHex,
		MnemonicPhrase: mnemonic,
	}, nil
}

func (b *BitcoinChain) Deposit(ctx context.Context, wallet blockchain.WalletDetails, amount float64, toAddress string) (*blockchain.TransactionResult, error) {
	fmt.Printf("[%s]: Depositing %f BTC to %s\n", b.Name(), amount, toAddress)
	return &blockchain.TransactionResult{
		TxHash:  "DepositTxHash",
		Success: true,
	}, nil
}

func (b *BitcoinChain) Withdraw(ctx context.Context, wallet blockchain.WalletDetails, amount float64, toAddress string) (*blockchain.TransactionResult, error) {
	fmt.Printf("[%s]: Withdrawing %f BTC from %s\n", b.Name(), amount, wallet.Address)
	return &blockchain.TransactionResult{
		TxHash:  "WithdrawTxHash",
		Success: true,
	}, nil
}

func (b *BitcoinChain) Sweep(ctx context.Context, wallet blockchain.WalletDetails) (*blockchain.TransactionResult, error) {
	fmt.Printf("[%s]: Sweeping wallet\n", b.Name())
	return &blockchain.TransactionResult{
		TxHash:  "SweepTxHash",
		Success: true,
	}, nil
}

func (e *BitcoinChain) BatchBalances(ctx context.Context, addresses []string, workers int) []models.BalanceResult {
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

func (e *BitcoinChain) getBalance(client *http.Client, address string) (string, error) {
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
