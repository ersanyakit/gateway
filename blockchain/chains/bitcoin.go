package chains

import (
	"context"
	blockchain "core/blockchain"
	"core/blockchain/walletcore"
	"core/constants"
	"core/helpers"
	"core/models"
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

type AddressType uint32

const (
	Legacy       AddressType = 44
	NestedSegWit AddressType = 49
	NativeSegWit AddressType = 84
	Taproot      AddressType = 86
)

type BitcoinChain struct {
	blockchain.BaseChain
}

func NewBitcoinChain() *BitcoinChain {
	return &BitcoinChain{
		BaseChain: blockchain.BaseChain{
			ID:          constants.Bitcoin,
			ChainName:   "bitcoin",
			ExplorerURL: "https://www.blockchain.com/explorer",
			RPCHttp:     []string{"https://open-api.unisat.io", "https://blockstream.info/api", "https://mempool.space/api"},
		},
	}
}

func (e *BitcoinChain) Name() string {
	return e.ChainName
}

func (e *BitcoinChain) ChainID() constants.ChainID {
	return e.ID
}

func (b *BitcoinChain) NewAddress(prvHex string) (string, error) {
	return walletcore.AddressFromPrivateKey(prvHex, b.ChainID())
}

func (b *BitcoinChain) ValidateAddress(address string) bool {
	return validateAddressWithWalletCore(b.ChainID(), address)
}

func (b *BitcoinChain) Create(ctx context.Context) (*blockchain.WalletDetails, error) {
	mnemonic, err := b.BaseChain.GenerateMnemonicPhrase()
	if err != nil {
		return nil, err
	}

	hdPath := b.BaseChain.GetDerivedPath(int(Taproot), 0, 0, 0, 1)
	wallet, err := b.BaseChain.GetDerivedWallet(mnemonic, hdPath)
	if err != nil {
		return nil, err
	}

	if !b.ValidateAddress(wallet.Address) {
		return nil, errors.New("invalid bitcoin address format")
	}

	return wallet, nil
}

func (b *BitcoinChain) CreateHDWallet(ctx context.Context, hdAccountId, hdWalletId int) (*blockchain.WalletDetails, error) {
	hdPath := b.BaseChain.GetDerivedPath(int(Taproot), 0, 0, hdAccountId, hdWalletId)
	wallet, err := b.BaseChain.CreateHDWalletFromPath(ctx, hdPath)
	if err != nil {
		return nil, err
	}

	if !b.ValidateAddress(wallet.Address) {
		return nil, errors.New("invalid bitcoin address format")
	}

	return wallet, nil
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
	if len(addresses) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if workers <= 0 {
		workers = 1
	}
	if workers > len(addresses) {
		workers = len(addresses)
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
		helpers.GoSafely("balance.bitcoin", workerFunc)
	}

	// iş kuyruğuna adresleri koy
enqueueBitcoin:
	for _, addr := range addresses {
		select {
		case jobs <- addr:
		case <-ctx.Done():
			break enqueueBitcoin
		}
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

func (e *BitcoinChain) getBalance(ctx context.Context, client *http.Client, address string) (string, error) {
	var lastErr error
	for _, rpc := range e.RPCHttp {
		rpc = strings.TrimSpace(rpc)
		if rpc == "" {
			continue
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(rpc, "/")+"/address/"+address, nil)
		if err != nil {
			lastErr = fmt.Errorf("%s %s request build failed: %w", e.Name(), rpc, err)
			continue
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("%s %s request failed: %w", e.Name(), rpc, err)
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("%s %s response read failed: %w", e.Name(), rpc, readErr)
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("%s %s returned HTTP %d: %s", e.Name(), rpc, resp.StatusCode, string(body))
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
			lastErr = fmt.Errorf("%s %s response decode failed: %w", e.Name(), rpc, err)
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
