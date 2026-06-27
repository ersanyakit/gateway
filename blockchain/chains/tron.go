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
	"io"
	"net/http"
	"os"
	"strings"
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
			RPCHttp:     []string{"https://api.trongrid.io/jsonrpc", "https://tron-rpc.publicnode.com/jsonrpc", "https://tron.drpc.org"},
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
	mnemonic, err := s.BaseChain.GenerateMnemonicPhrase()
	if err != nil {
		return nil, err
	}

	hdPath := s.BaseChain.GetDerivedPath(44, 195, 0, 0, 0)
	wallet, err := s.BaseChain.GetDerivedWallet(mnemonic, hdPath)
	if err != nil {
		return nil, err
	}

	if !s.ValidateAddress(wallet.Address) {
		return nil, errors.New("invalid tron address format")

	}

	return wallet, nil
}

func (s *TronChain) CreateHDWallet(ctx context.Context, hdAccountId, hdWalletId int) (*blockchain.WalletDetails, error) {
	hdPath := s.BaseChain.GetDerivedPath(44, 195, 0, hdAccountId, hdWalletId)
	mnemonic, err := s.BaseChain.GetMnemonicForPath(ctx, hdPath)
	if err != nil {
		return nil, err
	}

	wallet, err := s.BaseChain.GetDerivedWallet(mnemonic, hdPath)
	if err != nil {
		return nil, err
	}

	if !s.ValidateAddress(wallet.Address) {
		return nil, errors.New("invalid tron address format")

	}

	return wallet, nil
}

func (s *TronChain) Deposit(ctx context.Context, wallet blockchain.WalletDetails, amountRaw string, toAddress string) (*blockchain.TransactionResult, error) {
	return s.Withdraw(ctx, wallet, amountRaw, toAddress)
}

func (s *TronChain) Withdraw(ctx context.Context, wallet blockchain.WalletDetails, amountRaw string, toAddress string) (*blockchain.TransactionResult, error) {
	return s.sendTRX(ctx, wallet, amountRaw, toAddress)
}

func (s *TronChain) WithdrawToken(ctx context.Context, wallet blockchain.WalletDetails, tokenAddr string, amountRaw string, toAddress string) (*blockchain.TransactionResult, error) {
	return s.sendTRC20(ctx, wallet, tokenAddr, amountRaw, toAddress)
}

func (s *TronChain) Sweep(ctx context.Context, wallet blockchain.WalletDetails) (*blockchain.TransactionResult, error) {
	for _, key := range []string{"TRON_SWEEP_ADDRESS", "TRX_SWEEP_ADDRESS", "SWEEP_ADDRESS"} {
		if addr := strings.TrimSpace(os.Getenv(key)); addr != "" {
			return s.SweepTo(ctx, wallet, addr)
		}
	}
	return nil, fmt.Errorf("sweep destination required: set TRON_SWEEP_ADDRESS or SWEEP_ADDRESS")
}

func (e *TronChain) BatchBalances(ctx context.Context, addresses []string, workers int) []models.BalanceResult {
	if workers <= 0 {
		workers = 1
	}
	jobs := make(chan string, len(addresses))
	results := make(chan models.BalanceResult, len(addresses))

	client := &http.Client{Timeout: 10 * time.Second}

	var wg sync.WaitGroup

	workerFunc := func() {
		defer wg.Done()
		for addr := range jobs {
			balance, err := e.getBalance(ctx, client, addr)
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

func (e *TronChain) getBalance(ctx context.Context, client *http.Client, address string) (string, error) {
	address = strings.TrimSpace(address)
	if !e.ValidateAddress(address) {
		return "", fmt.Errorf("invalid tron address: %s", address)
	}

	addressHash, err := tronSDK.GetAddressHash(address)
	if err != nil {
		return "", fmt.Errorf("tron address hash: %w", err)
	}
	reqBody := map[string]interface{}{
		"address": hex.EncodeToString(addressHash),
	}
	data, _ := json.Marshal(reqBody)

	var lastErr error
	for _, apiBase := range tronHTTPAPIEndpoints(e.RPCs()) {
		apiBase = strings.TrimRight(strings.TrimSpace(apiBase), "/")
		if apiBase == "" {
			continue
		}

		endpoint := apiBase + "/wallet/getaccount"
		req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(data))
		if err != nil {
			lastErr = fmt.Errorf("%s %s request build failed: %w", e.Name(), endpoint, err)
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		if apiKey := strings.TrimSpace(os.Getenv("TRON_PRO_API_KEY")); apiKey != "" {
			req.Header.Set("TRON-PRO-API-KEY", apiKey)
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("%s %s request failed: %w", e.Name(), endpoint, err)
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("%s %s response read failed: %w", e.Name(), endpoint, readErr)
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("%s %s returned HTTP %d: %s", e.Name(), endpoint, resp.StatusCode, strings.TrimSpace(string(body)))
			continue
		}
		var res struct {
			Balance json.Number     `json:"balance"`
			Error   json.RawMessage `json:"Error"`
		}
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.UseNumber()
		if err := decoder.Decode(&res); err != nil {
			lastErr = fmt.Errorf("%s %s response decode failed: %w", e.Name(), endpoint, err)
			continue
		}
		if len(res.Error) > 0 && string(res.Error) != "null" {
			lastErr = fmt.Errorf("%s %s tron account error: %s", e.Name(), endpoint, strings.TrimSpace(string(res.Error)))
			continue
		}
		balance := strings.TrimSpace(res.Balance.String())
		if balance == "" {
			balance = "0"
		}
		return "TRX:" + balance, nil
	}

	if lastErr == nil {
		lastErr = errors.New("no tron HTTP API endpoint configured")
	}
	return "", lastErr
}

func tronHTTPAPIEndpoints(rpcURLs []string) []string {
	out := make([]string, 0, len(rpcURLs)+2)
	seen := map[string]struct{}{}
	add := func(raw string) {
		for _, part := range strings.Split(raw, ",") {
			value := strings.TrimRight(strings.TrimSpace(part), "/")
			value = strings.TrimSuffix(value, "/jsonrpc")
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			out = append(out, value)
		}
	}

	add(os.Getenv("TRON_HTTP_ENDPOINTS"))
	add(os.Getenv("TRON_HTTP_ENDPOINT"))
	for _, rpcURL := range rpcURLs {
		add(rpcURL)
	}
	if len(out) == 0 {
		add("https://api.trongrid.io")
	}
	return out
}
