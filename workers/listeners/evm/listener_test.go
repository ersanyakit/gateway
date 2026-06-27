package evm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"core/blockchain"
	"core/constants"
	"core/models"
)

func TestIsTraceUnavailableError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "method missing", err: errors.New("chiliz RPC trace_block error -32601: the method trace_block does not exist/is not available"), want: true},
		{name: "method not allowed", err: errors.New("avalanche RPC trace_block error -32600: Method trace_block not allowed"), want: true},
		{name: "provider tier", err: errors.New("arbitrum returned HTTP 400: method is not available on freetier"), want: true},
		{name: "provider cannot route trace", err: errors.New(`bnbchain https://bsc.drpc.org returned HTTP 400: {"error":{"message":"Can't route your request to suitable provider"}}`), want: true},
		{name: "transient", err: errors.New("context deadline exceeded"), want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTraceUnavailableError(tc.err); got != tc.want {
				t.Fatalf("isTraceUnavailableError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestIsBlockReceiptsUnavailableError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "method missing", err: errors.New("ethereum RPC eth_getBlockReceipts error -32601: method eth_getBlockReceipts does not exist"), want: true},
		{name: "method unavailable", err: errors.New("ethereum RPC eth_getBlockReceipts error -32600: method is not available"), want: true},
		{name: "different method", err: errors.New("ethereum RPC trace_block error -32601: method trace_block does not exist"), want: false},
		{name: "transient", err: errors.New("context deadline exceeded"), want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isBlockReceiptsUnavailableError(tc.err); got != tc.want {
				t.Fatalf("isBlockReceiptsUnavailableError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestReceiptsByTransactionsUsesJSONRPCBatch(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var requests []jsonRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&requests); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(requests) != 2 {
			t.Fatalf("batch request count = %d, want 2", len(requests))
		}
		_ = json.NewEncoder(w).Encode([]jsonRPCResponse{
			{
				ID:     requests[0].ID,
				Result: json.RawMessage(`{"transactionHash":"0xaaa","status":"0x1"}`),
			},
			{
				ID:     requests[1].ID,
				Result: json.RawMessage(`{"transactionHash":"0xbbb","status":"0x1"}`),
			},
		})
	}))
	defer server.Close()

	listener := &RpcListener{
		chain:  evmTestChain{rpcURL: server.URL},
		client: server.Client(),
	}
	receipts, err := listener.receiptsByTransactions(context.Background(), []RawTx{
		{Hash: "0xaaa"},
		{Hash: "0xbbb"},
	})
	if err != nil {
		t.Fatalf("receiptsByTransactions returned error: %v", err)
	}
	if len(receipts) != 2 {
		t.Fatalf("receipt count = %d, want 2", len(receipts))
	}
	if receipts["0xaaa"].Status != "0x1" || receipts["0xbbb"].Status != "0x1" {
		t.Fatalf("unexpected receipts: %#v", receipts)
	}
	if calls != 1 {
		t.Fatalf("batch HTTP calls = %d, want 1", calls)
	}
}

func TestIsBatchReceiptsUnavailableError(t *testing.T) {
	if !isBatchReceiptsUnavailableError(errors.New("batch requests are not supported")) {
		t.Fatal("batch unsupported error should be detected")
	}
	if isBatchReceiptsUnavailableError(errors.New("context deadline exceeded")) {
		t.Fatal("transient timeout should not disable batch receipts")
	}
}

func TestRpcCallAnnotatesTimedOutEndpoint(t *testing.T) {
	listener := &RpcListener{
		chain: evmTestChain{rpcURL: "https://base.example.invalid"},
		client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, context.DeadlineExceeded
		})},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	var out string
	err := listener.rpcCall(ctx, "eth_blockNumber", nil, &out)
	if err == nil {
		t.Fatal("rpcCall returned nil error, want timeout")
	}
	if !strings.Contains(err.Error(), listener.chain.RPCs()[0]) {
		t.Fatalf("rpcCall error %q does not include endpoint URL %q", err.Error(), listener.chain.RPCs()[0])
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type evmTestChain struct {
	rpcURL string
}

func (c evmTestChain) ChainID() constants.ChainID { return constants.Base }
func (c evmTestChain) Name() string               { return "base" }
func (c evmTestChain) WSS() []string              { return nil }
func (c evmTestChain) RPCs() []string             { return []string{c.rpcURL} }
func (c evmTestChain) Create(context.Context) (*blockchain.WalletDetails, error) {
	return nil, errors.New("not used")
}
func (c evmTestChain) CreateHDWallet(context.Context, int, int) (*blockchain.WalletDetails, error) {
	return nil, errors.New("not used")
}
func (c evmTestChain) Deposit(context.Context, blockchain.WalletDetails, string, string) (*blockchain.TransactionResult, error) {
	return nil, errors.New("not used")
}
func (c evmTestChain) Withdraw(context.Context, blockchain.WalletDetails, string, string) (*blockchain.TransactionResult, error) {
	return nil, errors.New("not used")
}
func (c evmTestChain) WithdrawToken(context.Context, blockchain.WalletDetails, string, string, string) (*blockchain.TransactionResult, error) {
	return nil, errors.New("not used")
}
func (c evmTestChain) Sweep(context.Context, blockchain.WalletDetails) (*blockchain.TransactionResult, error) {
	return nil, errors.New("not used")
}
func (c evmTestChain) SweepTo(context.Context, blockchain.WalletDetails, string) (*blockchain.TransactionResult, error) {
	return nil, errors.New("not used")
}
func (c evmTestChain) SweepERC20To(context.Context, blockchain.WalletDetails, string, string) (*blockchain.TransactionResult, error) {
	return nil, errors.New("not used")
}
func (c evmTestChain) PrefundGas(context.Context, blockchain.WalletDetails, string) (bool, error) {
	return false, errors.New("not used")
}
func (c evmTestChain) ValidateAddress(string) bool { return false }
func (c evmTestChain) AddWorker(blockchain.Worker) error {
	return errors.New("not used")
}
func (c evmTestChain) RemoveWorker(blockchain.Worker) error {
	return errors.New("not used")
}
func (c evmTestChain) WorkerCount() int { return 0 }
func (c evmTestChain) BatchBalances(context.Context, []string, int) []models.BalanceResult {
	return nil
}
func (c evmTestChain) StartWorkers(context.Context) error { return errors.New("not used") }
func (c evmTestChain) StopWorkers() error                 { return errors.New("not used") }
