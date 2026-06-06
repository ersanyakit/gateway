package chains

import (
	"context"
	blockchain "core/blockchain"
	"core/constants"
	"core/models"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
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
			RPCHttp:     []string{"https://blockstream.info/api", "https://mempool.space/api"},
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

func (b *BitcoinChain) Deposit(ctx context.Context, wallet blockchain.WalletDetails, amountRaw string, toAddress string) (*blockchain.TransactionResult, error) {
	return b.Withdraw(ctx, wallet, amountRaw, toAddress)
}

func (b *BitcoinChain) Withdraw(ctx context.Context, wallet blockchain.WalletDetails, amountRaw string, toAddress string) (*blockchain.TransactionResult, error) {
	amount, err := nativeAmountRaw(amountRaw)
	if err != nil {
		return nil, err
	}
	if !amount.IsInt64() {
		return nil, fmt.Errorf("bitcoin amount_raw exceeds int64 satoshis")
	}
	return b.sendTo(ctx, wallet, toAddress, amount.Int64())
}

func (b *BitcoinChain) WithdrawToken(_ context.Context, _ blockchain.WalletDetails, _ string, _ string, _ string) (*blockchain.TransactionResult, error) {
	return nil, fmt.Errorf("bitcoin does not support token withdrawal")
}

func (b *BitcoinChain) Sweep(ctx context.Context, wallet blockchain.WalletDetails) (*blockchain.TransactionResult, error) {
	for _, key := range []string{"BITCOIN_SWEEP_ADDRESS", "BTC_SWEEP_ADDRESS", "SWEEP_ADDRESS"} {
		if addr := strings.TrimSpace(os.Getenv(key)); addr != "" {
			return b.SweepTo(ctx, wallet, addr)
		}
	}
	return nil, fmt.Errorf("sweep destination required: set BITCOIN_SWEEP_ADDRESS or SWEEP_ADDRESS")
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
	var lastErr error
	for _, rpc := range e.RPCHttp {
		req, err := http.NewRequest(http.MethodGet, strings.TrimRight(rpc, "/")+"/address/"+address, nil)
		if err != nil {
			lastErr = err
			continue
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("bitcoin API returned HTTP %d: %s", resp.StatusCode, string(body))
			continue
		}

		var res struct {
			ChainStats struct {
				Funded int64 `json:"funded_txo_sum"`
				Spent  int64 `json:"spent_txo_sum"`
			} `json:"chain_stats"`
			MempoolStats struct {
				Funded int64 `json:"funded_txo_sum"`
				Spent  int64 `json:"spent_txo_sum"`
			} `json:"mempool_stats"`
		}
		if err := json.Unmarshal(body, &res); err != nil {
			lastErr = err
			continue
		}

		balance := (res.ChainStats.Funded - res.ChainStats.Spent) + (res.MempoolStats.Funded - res.MempoolStats.Spent)
		return fmt.Sprintf("%d", balance), nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("no bitcoin API endpoint configured")
	}
	return "", lastErr
}
