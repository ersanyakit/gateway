package chains

import (
	"bytes"
	"context"
	blockchain "core/blockchain"
	"core/blockchain/addrutil"
	"core/blockchain/walletcore"
	"core/constants"
	"core/helpers"
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
)

type TronChain struct {
	blockchain.BaseChain
}

func NewTronChain() *TronChain {
	return newTronChain(
		constants.TRON,
		"tron",
		"https://tronscan.org/",
		[]string{"https://api.trongrid.io/jsonrpc", "https://tron-rpc.publicnode.com/jsonrpc", "https://tron.drpc.org"},
	)
}

func NewTronTestnetChain() *TronChain {
	return newTronChain(
		constants.TRONTestnet,
		"tron-testnet",
		"https://nile.tronscan.org/",
		[]string{"https://nile.trongrid.io/jsonrpc"},
	)
}

func newTronChain(id constants.ChainID, name string, explorerURL string, rpcHTTP []string) *TronChain {
	return &TronChain{
		blockchain.BaseChain{
			ID:          id,
			ChainName:   name,
			ExplorerURL: explorerURL,
			RPCHttp:     rpcHTTP,
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
	return walletcore.AddressFromPrivateKey(privateKeyHex, t.ChainID())
}

func (s *TronChain) ValidateAddress(address string) bool {
	return validateAddressWithWalletCore(s.ChainID(), address)
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
	wallet, err := s.BaseChain.CreateHDWalletFromPath(ctx, hdPath)
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
	for _, key := range tronSweepAddressEnvKeys(s.Name()) {
		if addr := strings.TrimSpace(os.Getenv(key)); addr != "" {
			return s.SweepTo(ctx, wallet, addr)
		}
	}
	return nil, fmt.Errorf("sweep destination required: set one of %s", strings.Join(tronSweepAddressEnvKeys(s.Name()), ", "))
}

func tronSweepAddressEnvKeys(chainName string) []string {
	if strings.EqualFold(strings.TrimSpace(chainName), "tron-testnet") {
		return []string{"TRON_TESTNET_SWEEP_ADDRESS", "TRX_TESTNET_SWEEP_ADDRESS", "NILE_SWEEP_ADDRESS", "TRON_NILE_SWEEP_ADDRESS", "SHASTA_SWEEP_ADDRESS", "TRON_SWEEP_ADDRESS", "TRX_SWEEP_ADDRESS", "SWEEP_ADDRESS"}
	}
	return []string{"TRON_SWEEP_ADDRESS", "TRX_SWEEP_ADDRESS", "SWEEP_ADDRESS"}
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
		helpers.GoSafely("balance.tron", workerFunc)
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

	addressHash, err := addrutil.TronAddressHash(address)
	if err != nil {
		return "", fmt.Errorf("tron address hash: %w", err)
	}
	reqBody := map[string]interface{}{
		"address": hex.EncodeToString(addressHash),
	}
	data, _ := json.Marshal(reqBody)

	var lastErr error
	for _, apiBase := range tronHTTPAPIEndpointsForChain(e.Name(), e.RPCs()) {
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
	return tronHTTPAPIEndpointsForChain("tron", rpcURLs)
}

func tronHTTPAPIEndpointsForChain(chainName string, rpcURLs []string) []string {
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

	if strings.EqualFold(strings.TrimSpace(chainName), "tron-testnet") {
		add(os.Getenv("TRON_TESTNET_HTTP_ENDPOINTS"))
		add(os.Getenv("TRON_TESTNET_HTTP_ENDPOINT"))
		for _, rpcURL := range rpcURLs {
			add(rpcURL)
		}
		if len(out) == 0 {
			add("https://nile.trongrid.io")
		}
		return out
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
