package evm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	configurations "core/application/configuration"
	"core/asset"
	"core/blockchain"
	"core/constants"
	"core/models"
	listenerconfig "core/workers/listeners"
	"core/workers/listeners/rpcutil"

	"github.com/ethereum/go-ethereum/common"
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

func TestRpcCallFallsBackAfterEndpointTimeout(t *testing.T) {
	// Keep enough headroom for the fast fallback even when the full repository
	// test suite is competing for CPU on a loaded CI worker.
	t.Setenv("CHAIN_RPC_ENDPOINT_TIMEOUT", "50ms")

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(jsonRPCResponse{Result: json.RawMessage(`"0x1"`)})
	}))
	defer slow.Close()

	var fastCalls int
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fastCalls++
		var req jsonRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Method != "eth_blockNumber" {
			t.Fatalf("method = %s, want eth_blockNumber", req.Method)
		}
		_ = json.NewEncoder(w).Encode(jsonRPCResponse{ID: req.ID, Result: json.RawMessage(`"0x2a"`)})
	}))
	defer fast.Close()

	listener := &RpcListener{
		chain:           evmTestChain{rpcURLs: []string{slow.URL, fast.URL}},
		client:          &http.Client{Timeout: time.Second},
		endpointCircuit: rpcutil.NewEndpointCircuit(),
	}

	var out string
	if err := listener.rpcCall(context.Background(), "eth_blockNumber", nil, &out); err != nil {
		t.Fatalf("rpcCall returned error: %v", err)
	}
	if out != "0x2a" || fastCalls != 1 {
		t.Fatalf("fallback result=%q fastCalls=%d, want 0x2a/1", out, fastCalls)
	}
	ranked := listener.rpcURLs()
	if len(ranked) != 2 || ranked[0] != fast.URL {
		t.Fatalf("ranked endpoints = %#v, want fast endpoint first after timeout", ranked)
	}
}

func TestCatchUpScansRecentHeadWhenHistoricalCursorLags(t *testing.T) {
	var mu sync.Mutex
	processedBlocks := make([]int64, 0)
	var stateWrites int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		result := json.RawMessage(`null`)
		switch req.Method {
		case "eth_blockNumber":
			result = json.RawMessage(`"0x7d0"`)
		case "eth_getBlockByNumber":
			blockHex, _ := req.Params[0].(string)
			blockNumber := hexToInt64(blockHex)
			mu.Lock()
			processedBlocks = append(processedBlocks, blockNumber)
			mu.Unlock()
			result = json.RawMessage(fmt.Sprintf(
				`{"number":%q,"hash":%q,"parentHash":%q,"transactions":[]}`,
				blockHex,
				fmt.Sprintf("0xblock%d", blockNumber),
				fmt.Sprintf("0xblock%d", blockNumber-1),
			))
		case "eth_getBlockReceipts":
			result = json.RawMessage(`[]`)
		case "trace_block":
			_ = json.NewEncoder(w).Encode(jsonRPCResponse{
				ID: req.ID,
				Error: &jsonRPCError{
					Code:    -32601,
					Message: "the method trace_block does not exist",
				},
			})
			return
		default:
			t.Fatalf("unexpected RPC method: %s", req.Method)
		}

		_ = json.NewEncoder(w).Encode(jsonRPCResponse{ID: req.ID, Result: result})
	}))
	defer server.Close()

	state := &models.ChainState{ChainID: constants.Base, LastProcessedBlock: 100}
	listener := NewRpcListener(
		evmTestChain{rpcURL: server.URL},
		configurations.NewAssetRegistry(),
		state,
		nil,
		func(s *models.ChainState) error {
			stateWrites++
			return nil
		},
	)
	listener.client = server.Client()

	if err := listener.catchUp(); err != nil {
		t.Fatalf("catchUp returned error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(processedBlocks) != int(recentMaxBlocksPerPoll+maxBlocksPerPoll) {
		t.Fatalf("processed block count = %d, want %d: %#v", len(processedBlocks), recentMaxBlocksPerPoll+maxBlocksPerPoll, processedBlocks)
	}
	if processedBlocks[0] != 1964 {
		t.Fatalf("first recent block = %d, want 1964", processedBlocks[0])
	}
	recentEndIndex := int(recentMaxBlocksPerPoll - 1)
	if processedBlocks[recentEndIndex] != 1988 {
		t.Fatalf("last recent block = %d, want 1988", processedBlocks[recentEndIndex])
	}
	historicalStartIndex := int(recentMaxBlocksPerPoll)
	if processedBlocks[historicalStartIndex] != 101 {
		t.Fatalf("first historical block = %d, want 101", processedBlocks[historicalStartIndex])
	}
	if state.LastProcessedBlock != 105 {
		t.Fatalf("last processed block = %d, want 105", state.LastProcessedBlock)
	}
	if listener.recentProcessedBlock != 1988 {
		t.Fatalf("recent processed block = %d, want 1988", listener.recentProcessedBlock)
	}
	if state.LastConfirmedBlock != 2000 {
		t.Fatalf("last confirmed block = %d, want 2000", state.LastConfirmedBlock)
	}
	if stateWrites != int(maxBlocksPerPoll)+1 {
		t.Fatalf("state writes = %d, want %d", stateWrites, int(maxBlocksPerPoll)+1)
	}
}

func TestProcessBlockRewindsCheckpointOnParentContinuityMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		switch req.Method {
		case "eth_getBlockByNumber":
			_ = json.NewEncoder(w).Encode(jsonRPCResponse{
				ID: req.ID,
				Result: json.RawMessage(`{
					"number":"0xb",
					"hash":"0xnew11",
					"parentHash":"0xnew10",
					"transactions":[]
				}`),
			})
		default:
			t.Fatalf("unexpected RPC method: %s", req.Method)
		}
	}))
	defer server.Close()

	state := &models.ChainState{
		ChainID:            constants.Base,
		LastProcessedBlock: 10,
		LastProcessedHash:  "0xold10",
		LastConfirmedBlock: 20,
	}
	var observedCanonical bool
	var wroteRollback bool
	listener := NewRpcListener(
		evmTestChain{rpcURL: server.URL},
		configurations.NewAssetRegistry(),
		state,
		nil,
		func(*models.ChainState) error {
			wroteRollback = true
			return nil
		},
	)
	listener.client = server.Client()
	listener.SetCanonicalBlockObserver(func(_ context.Context, chainID constants.ChainID, blockNumber int64, blockHash string, parentHash string) error {
		observedCanonical = true
		if chainID != constants.Base || blockNumber != 11 || blockHash != "0xnew11" || parentHash != "0xnew10" {
			t.Fatalf("canonical observation = chain=%d block=%d hash=%s parent=%s", chainID, blockNumber, blockHash, parentHash)
		}
		return nil
	})

	err := listener.processBlock(context.Background(), 11)
	if !errors.Is(err, listenerconfig.ErrParentContinuity) {
		t.Fatalf("processBlock error = %v, want ErrParentContinuity", err)
	}
	if !observedCanonical {
		t.Fatal("canonical block observer was not called before rollback")
	}
	if !wroteRollback {
		t.Fatal("rollback chain state was not persisted")
	}
	if state.LastProcessedBlock != 9 {
		t.Fatalf("last processed block = %d, want rewind to 9", state.LastProcessedBlock)
	}
	if state.LastProcessedHash != "" || state.LastProcessedParentHash != "" {
		t.Fatalf("checkpoint hash = %q/%q, want cleared", state.LastProcessedHash, state.LastProcessedParentHash)
	}
	if state.ContinuityStatus != listenerconfig.ContinuityStatusRollback || state.ContinuityReason == "" {
		t.Fatalf("continuity evidence = %q/%q, want rollback evidence", state.ContinuityStatus, state.ContinuityReason)
	}
}

func TestHandleTransferLogSkipsZeroAmountTokenTransfer(t *testing.T) {
	token := "0x00000000000000000000000000000000000000aa"
	registry := asset.NewRegistry()
	registry.Register(asset.NewDeploymentAsset(
		asset.AssetDefinition{Symbol: "USDC", Name: "USD Coin", Decimals: 6},
		asset.Deployment{ChainID: constants.Base, Address: token, Decimals: 6, Type: asset.AssetERC20, Enabled: true},
	))
	listener := &RpcListener{
		chain:    evmTestChain{},
		registry: registry,
		events:   make(chan interface{}, 1),
	}

	err := listener.handleTransferLog(context.Background(), EVMLog{
		Address:         token,
		Topics:          []string{TransferEventHash, evmAddressTopic("0x0000000000000000000000000000000000000001"), evmAddressTopic("0x0000000000000000000000000000000000000002")},
		Data:            "0x" + strings.Repeat("0", 64),
		TransactionHash: "0xabc",
		BlockNumber:     "0x1",
		BlockHash:       "0xblock",
		LogIndex:        "0x0",
	}, "confirmed", "0xparent")
	if err != nil {
		t.Fatal(err)
	}

	select {
	case event := <-listener.events:
		t.Fatalf("unexpected zero amount token event: %#v", event)
	default:
	}
}

func TestReceiptStatusFailsClosedForUnknownValues(t *testing.T) {
	if got := receiptStatus("0x1"); got != models.TransactionStatusConfirmed {
		t.Fatalf("confirmed status = %q", got)
	}
	if got := receiptStatus("0x0"); got != models.TransactionStatusFailed {
		t.Fatalf("failed status = %q", got)
	}
	if got := receiptStatus(""); got != "" {
		t.Fatalf("missing status = %q, want fail-closed empty", got)
	}
}

func evmAddressTopic(address string) string {
	return common.BytesToHash(common.HexToAddress(address).Bytes()).Hex()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type evmTestChain struct {
	rpcURL  string
	rpcURLs []string
}

func (c evmTestChain) ChainID() constants.ChainID { return constants.Base }
func (c evmTestChain) Name() string               { return "base" }
func (c evmTestChain) WSS() []string              { return nil }
func (c evmTestChain) RPCs() []string {
	if len(c.rpcURLs) > 0 {
		return c.rpcURLs
	}
	return []string{c.rpcURL}
}
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
