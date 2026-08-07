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
	"core/workers/dispatcher"
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

func TestReceiptsByTransactionsRejectsIncompleteJSONRPCBatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var requests []jsonRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&requests); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode([]jsonRPCResponse{{
			ID:     requests[0].ID,
			Result: json.RawMessage(`{"transactionHash":"0xaaa","status":"0x1"}`),
		}})
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
	if receipts != nil {
		t.Fatalf("incomplete batch receipts = %#v, want nil fallback marker", receipts)
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

func TestCatchUpScansOnlyContiguousBlocksWhenHistoricalCursorLags(t *testing.T) {
	var mu sync.Mutex
	processedBlocks := make([]int64, 0)
	observedBlocks := make([]int64, 0)
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
				evmTestHash(blockNumber),
				evmTestHash(blockNumber-1),
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
	listener.SetCanonicalBlockObserver(func(_ context.Context, chainID constants.ChainID, blockNumber int64, blockHash, parentHash string) error {
		if chainID != constants.Base || blockHash != evmTestHash(blockNumber) || parentHash != evmTestHash(blockNumber-1) {
			return fmt.Errorf("unexpected canonical block chain=%d block=%d hash=%s parent=%s", chainID, blockNumber, blockHash, parentHash)
		}
		mu.Lock()
		observedBlocks = append(observedBlocks, blockNumber)
		mu.Unlock()
		return nil
	})

	if err := listener.catchUp(); err != nil {
		t.Fatalf("catchUp returned error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(processedBlocks) != int(maxBlocksPerPoll) {
		t.Fatalf("processed block count = %d, want %d: %#v", len(processedBlocks), maxBlocksPerPoll, processedBlocks)
	}
	if len(observedBlocks) != len(processedBlocks) {
		t.Fatalf("canonical observations = %#v, want one for every processed block %#v", observedBlocks, processedBlocks)
	}
	for index, blockNumber := range processedBlocks {
		want := int64(101 + index)
		if blockNumber != want {
			t.Fatalf("processed block %d = %d, want contiguous block %d; all=%#v", index, blockNumber, want, processedBlocks)
		}
		if observedBlocks[index] != want {
			t.Fatalf("observed block %d = %d, want canonical observation for %d; all=%#v", index, observedBlocks[index], want, observedBlocks)
		}
	}
	if state.LastProcessedBlock != 105 {
		t.Fatalf("last processed block = %d, want 105", state.LastProcessedBlock)
	}
	if state.LastConfirmedBlock != 2000 {
		t.Fatalf("last confirmed block = %d, want 2000", state.LastConfirmedBlock)
	}
	if stateWrites != int(maxBlocksPerPoll) {
		t.Fatalf("state writes = %d, want %d", stateWrites, maxBlocksPerPoll)
	}
}

func TestProcessBlockRewindsCheckpointOnParentContinuityMismatch(t *testing.T) {
	t.Setenv("CHAIN_8453_REORG_REWIND_BLOCKS", "1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		switch req.Method {
		case "eth_getBlockByNumber":
			_ = json.NewEncoder(w).Encode(jsonRPCResponse{
				ID: req.ID,
				Result: json.RawMessage(fmt.Sprintf(`{
					"number":"0xb",
					"hash":%q,
					"parentHash":%q,
					"transactions":[]
				}`, evmTestHash(11), evmTestHash(10))),
			})
		default:
			t.Fatalf("unexpected RPC method: %s", req.Method)
		}
	}))
	defer server.Close()

	state := &models.ChainState{
		ChainID:            constants.Base,
		LastProcessedBlock: 10,
		LastProcessedHash:  evmTestHash(110),
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
		if chainID != constants.Base || blockNumber != 11 || blockHash != evmTestHash(11) || parentHash != evmTestHash(10) {
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

func TestProcessBlockHoldsBeforeDispatchWhenCanonicalObserverFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if req.Method != "eth_getBlockByNumber" {
			t.Errorf("downstream RPC %s called after canonical observer failure", req.Method)
			http.Error(w, "unexpected downstream call", http.StatusInternalServerError)
			return
		}
		result := json.RawMessage(fmt.Sprintf(
			`{"number":"0x65","hash":%q,"parentHash":%q,"transactions":[]}`,
			evmTestHash(101),
			evmTestHash(100),
		))
		_ = json.NewEncoder(w).Encode(jsonRPCResponse{ID: req.ID, Result: result})
	}))
	defer server.Close()

	state := &models.ChainState{
		ChainID:            constants.Base,
		LastProcessedBlock: 100,
		LastProcessedHash:  evmTestHash(100),
	}
	stateWrites := 0
	listener := NewRpcListener(
		evmTestChain{rpcURL: server.URL},
		configurations.NewAssetRegistry(),
		state,
		nil,
		func(*models.ChainState) error {
			stateWrites++
			return nil
		},
	)
	listener.client = server.Client()
	observerErr := errors.New("canonical block store unavailable")
	observerCalls := 0
	listener.SetCanonicalBlockObserver(func(_ context.Context, chainID constants.ChainID, blockNumber int64, blockHash, parentHash string) error {
		observerCalls++
		if chainID != constants.Base || blockNumber != 101 || blockHash != evmTestHash(101) || parentHash != evmTestHash(100) {
			t.Fatalf("canonical observation = chain=%d block=%d hash=%s parent=%s", chainID, blockNumber, blockHash, parentHash)
		}
		return observerErr
	})

	err := listener.processBlock(context.Background(), 101)
	if !errors.Is(err, observerErr) {
		t.Fatalf("processBlock error = %v, want %v", err, observerErr)
	}
	if observerCalls != 1 {
		t.Fatalf("canonical observer calls = %d, want 1", observerCalls)
	}
	if stateWrites != 0 || state.LastProcessedBlock != 100 || listener.lastBlockHash != "" {
		t.Fatalf("checkpoint changed after observer failure: writes=%d state=%#v last_hash=%q", stateWrites, state, listener.lastBlockHash)
	}
}

func TestValidateBlockResponseRejectsIdentityErrors(t *testing.T) {
	valid := Block{Number: "0x65", Hash: evmTestHash(101), ParentHash: evmTestHash(100)}
	cases := []struct {
		name  string
		block Block
	}{
		{name: "wrong number", block: Block{Number: "0x64", Hash: valid.Hash, ParentHash: valid.ParentHash}},
		{name: "missing hash", block: Block{Number: valid.Number, ParentHash: valid.ParentHash}},
		{name: "malformed hash", block: Block{Number: valid.Number, Hash: "0x1234", ParentHash: valid.ParentHash}},
		{name: "missing parent", block: Block{Number: valid.Number, Hash: valid.Hash}},
		{name: "hash equals parent", block: Block{Number: valid.Number, Hash: valid.Hash, ParentHash: valid.Hash}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateBlockResponse(101, tc.block); err == nil {
				t.Fatalf("validateBlockResponse(%#v) returned nil", tc.block)
			}
		})
	}
	if err := validateBlockResponse(101, valid); err != nil {
		t.Fatalf("valid block rejected: %v", err)
	}
}

func TestValidateBlockResponseRequiresCompleteStrictRawTransactions(t *testing.T) {
	validTransaction := fmt.Sprintf(`{
		"hash":%q,
		"from":"0x0000000000000000000000000000000000000001",
		"to":"0x0000000000000000000000000000000000000002",
		"value":"0x1",
		"input":"0x",
		"transactionIndex":"0x0"
	}`, evmTestHash(1001))
	validCreation := fmt.Sprintf(`{
		"hash":%q,
		"from":"0x0000000000000000000000000000000000000001",
		"to":null,
		"value":"0x0",
		"input":"0x6000",
		"transactionIndex":"0x0"
	}`, evmTestHash(1002))

	for name, transactionJSON := range map[string]string{
		"ordinary transfer": validTransaction,
		"contract creation": validCreation,
	} {
		t.Run(name, func(t *testing.T) {
			block := decodeEVMTestBlock(t, transactionJSON)
			if err := validateBlockResponse(101, block); err != nil {
				t.Fatalf("valid transaction rejected: %v", err)
			}
		})
	}

	cases := map[string]string{
		"missing hash":    fmt.Sprintf(`{"from":"0x0000000000000000000000000000000000000001","to":"0x0000000000000000000000000000000000000002","value":"0x1","transactionIndex":"0x0"}`),
		"malformed from":  fmt.Sprintf(`{"hash":%q,"from":"0x1","to":"0x0000000000000000000000000000000000000002","value":"0x1","transactionIndex":"0x0"}`, evmTestHash(1001)),
		"missing to":      fmt.Sprintf(`{"hash":%q,"from":"0x0000000000000000000000000000000000000001","value":"0x1","transactionIndex":"0x0"}`, evmTestHash(1001)),
		"empty to":        fmt.Sprintf(`{"hash":%q,"from":"0x0000000000000000000000000000000000000001","to":"","value":"0x1","transactionIndex":"0x0"}`, evmTestHash(1001)),
		"malformed to":    fmt.Sprintf(`{"hash":%q,"from":"0x0000000000000000000000000000000000000001","to":"0x2","value":"0x1","transactionIndex":"0x0"}`, evmTestHash(1001)),
		"missing value":   fmt.Sprintf(`{"hash":%q,"from":"0x0000000000000000000000000000000000000001","to":"0x0000000000000000000000000000000000000002","transactionIndex":"0x0"}`, evmTestHash(1001)),
		"malformed value": fmt.Sprintf(`{"hash":%q,"from":"0x0000000000000000000000000000000000000001","to":"0x0000000000000000000000000000000000000002","value":"one","transactionIndex":"0x0"}`, evmTestHash(1001)),
		"missing index":   fmt.Sprintf(`{"hash":%q,"from":"0x0000000000000000000000000000000000000001","to":"0x0000000000000000000000000000000000000002","value":"0x1"}`, evmTestHash(1001)),
		"wrong index":     fmt.Sprintf(`{"hash":%q,"from":"0x0000000000000000000000000000000000000001","to":"0x0000000000000000000000000000000000000002","value":"0x1","transactionIndex":"0x1"}`, evmTestHash(1001)),
	}
	for name, transactionJSON := range cases {
		t.Run(name, func(t *testing.T) {
			block := decodeEVMTestBlock(t, transactionJSON)
			if err := validateBlockResponse(101, block); err == nil {
				t.Fatal("malformed raw transaction was accepted")
			}
		})
	}

	t.Run("duplicate hash", func(t *testing.T) {
		second := strings.Replace(validTransaction, `"transactionIndex":"0x0"`, `"transactionIndex":"0x1"`, 1)
		block := decodeEVMTestBlock(t, validTransaction+","+second)
		if err := validateBlockResponse(101, block); err == nil || !strings.Contains(err.Error(), "duplicate transaction hash") {
			t.Fatalf("duplicate hash error = %v", err)
		}
	})
}

func TestProcessBlockHoldsBeforeObservationOnMalformedRawTransaction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		result := json.RawMessage(fmt.Sprintf(`{
			"number":"0x65","hash":%q,"parentHash":%q,
			"transactions":[{
				"hash":%q,
				"from":"0x0000000000000000000000000000000000000001",
				"to":"0x0000000000000000000000000000000000000002",
				"value":"not-a-quantity",
				"transactionIndex":"0x0"
			}]
		}`, evmTestHash(101), evmTestHash(100), evmTestHash(1001)))
		_ = json.NewEncoder(w).Encode(jsonRPCResponse{ID: req.ID, Result: result})
	}))
	defer server.Close()

	state := &models.ChainState{ChainID: constants.Base, LastProcessedBlock: 100, LastProcessedHash: evmTestHash(100)}
	listener := NewRpcListener(evmTestChain{rpcURL: server.URL}, configurations.NewAssetRegistry(), state, nil, nil)
	listener.client = server.Client()
	observations := 0
	listener.SetCanonicalBlockObserver(func(context.Context, constants.ChainID, int64, string, string) error {
		observations++
		return nil
	})

	if err := listener.processBlock(context.Background(), 101); err == nil || !strings.Contains(err.Error(), "value") {
		t.Fatalf("processBlock error = %v, want malformed value", err)
	}
	if observations != 0 || listener.lastBlockNumber != 0 || listener.lastBlockHash != "" || state.LastProcessedBlock != 100 {
		t.Fatalf("malformed block advanced state: observations=%d listener=%#v state=%#v", observations, listener, state)
	}
}

func TestProcessBlockRejectsMismatchedRequestedHeight(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if req.Method != "eth_getBlockByNumber" {
			t.Errorf("unexpected RPC method: %s", req.Method)
			http.Error(w, "unexpected method", http.StatusBadRequest)
			return
		}
		result := json.RawMessage(fmt.Sprintf(
			`{"number":"0x64","hash":%q,"parentHash":%q,"transactions":[]}`,
			evmTestHash(100),
			evmTestHash(99),
		))
		_ = json.NewEncoder(w).Encode(jsonRPCResponse{ID: req.ID, Result: result})
	}))
	defer server.Close()

	state := &models.ChainState{
		ChainID:            constants.Base,
		LastProcessedBlock: 100,
		LastProcessedHash:  evmTestHash(100),
	}
	listener := NewRpcListener(
		evmTestChain{rpcURL: server.URL},
		configurations.NewAssetRegistry(),
		state,
		nil,
		nil,
	)
	listener.client = server.Client()

	err := listener.processBlock(context.Background(), 101)
	if err == nil || !strings.Contains(err.Error(), "block number mismatch") {
		t.Fatalf("processBlock error = %v, want block number mismatch", err)
	}
	if state.LastProcessedBlock != 100 || listener.lastBlockHash != "" {
		t.Fatalf("block advanced after mismatched response: state=%#v last_hash=%q", state, listener.lastBlockHash)
	}
}

func TestValidateReceiptForTransactionRejectsIdentityErrors(t *testing.T) {
	txHash := evmTestHash(1001)
	block := Block{Number: "0x65", Hash: evmTestHash(101), ParentHash: evmTestHash(100)}
	tx := RawTx{Hash: txHash, TransactionIndex: "0x0"}
	valid := Receipt{
		TransactionHash:  txHash,
		TransactionIndex: "0x0",
		BlockNumber:      block.Number,
		BlockHash:        block.Hash,
		Status:           "0x1",
		Logs: []EVMLog{{
			TransactionHash: txHash,
			BlockNumber:     block.Number,
			BlockHash:       block.Hash,
			LogIndex:        "0x0",
		}},
	}
	cases := []struct {
		name   string
		mutate func(*Receipt)
	}{
		{name: "missing transaction", mutate: func(receipt *Receipt) { receipt.TransactionHash = "" }},
		{name: "wrong transaction", mutate: func(receipt *Receipt) { receipt.TransactionHash = evmTestHash(1002) }},
		{name: "wrong block number", mutate: func(receipt *Receipt) { receipt.BlockNumber = "0x64" }},
		{name: "wrong block hash", mutate: func(receipt *Receipt) { receipt.BlockHash = evmTestHash(102) }},
		{name: "wrong log transaction", mutate: func(receipt *Receipt) { receipt.Logs[0].TransactionHash = evmTestHash(1002) }},
		{name: "wrong log block number", mutate: func(receipt *Receipt) { receipt.Logs[0].BlockNumber = "0x64" }},
		{name: "wrong log block hash", mutate: func(receipt *Receipt) { receipt.Logs[0].BlockHash = evmTestHash(102) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			receipt := valid
			receipt.Logs = append([]EVMLog(nil), valid.Logs...)
			tc.mutate(&receipt)
			if err := validateReceiptForTransaction(receipt, tx, block); err == nil {
				t.Fatalf("validateReceiptForTransaction(%#v) returned nil", receipt)
			}
		})
	}
	if err := validateReceiptForTransaction(valid, tx, block); err != nil {
		t.Fatalf("valid receipt rejected: %v", err)
	}
}

func TestProcessBlockHoldsOnReceiptBlockIdentityMismatch(t *testing.T) {
	txHash := evmTestHash(1001)
	blockHash := evmTestHash(101)
	wrongBlockHash := evmTestHash(102)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if len(raw) > 0 && raw[0] == '[' {
			var requests []jsonRPCRequest
			if err := json.Unmarshal(raw, &requests); err != nil {
				t.Errorf("decode batch: %v", err)
				return
			}
			_ = json.NewEncoder(w).Encode(jsonRPCResponse{
				Error: &jsonRPCError{Code: -32600, Message: "batch requests are not supported"},
			})
			return
		}

		var req jsonRPCRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Errorf("decode single request: %v", err)
			return
		}
		switch req.Method {
		case "eth_getBlockByNumber":
			result := json.RawMessage(fmt.Sprintf(`{
				"number":"0x65",
				"hash":%q,
				"parentHash":%q,
				"transactions":[{
					"hash":%q,
					"from":"0x0000000000000000000000000000000000000001",
					"to":"0x0000000000000000000000000000000000000002",
					"value":"0x1",
					"input":"0x",
					"transactionIndex":"0x0"
				}]
			}`, blockHash, evmTestHash(100), txHash))
			_ = json.NewEncoder(w).Encode(jsonRPCResponse{ID: req.ID, Result: result})
		case "eth_getBlockReceipts":
			_ = json.NewEncoder(w).Encode(jsonRPCResponse{ID: req.ID, Result: json.RawMessage(`[]`)})
		case "eth_getTransactionReceipt":
			result := json.RawMessage(fmt.Sprintf(`{
				"transactionHash":%q,
				"transactionIndex":"0x0",
				"blockNumber":"0x65",
				"blockHash":%q,
				"status":"0x1",
				"logs":[]
			}`, txHash, wrongBlockHash))
			_ = json.NewEncoder(w).Encode(jsonRPCResponse{ID: req.ID, Result: result})
		default:
			t.Errorf("unexpected RPC method: %s", req.Method)
			http.Error(w, "unexpected method", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	state := &models.ChainState{
		ChainID:            constants.Base,
		LastProcessedBlock: 100,
		LastProcessedHash:  evmTestHash(100),
	}
	listener := NewRpcListener(
		evmTestChain{rpcURL: server.URL},
		configurations.NewAssetRegistry(),
		state,
		nil,
		nil,
	)
	listener.client = server.Client()

	err := listener.processBlock(context.Background(), 101)
	if err == nil || !strings.Contains(err.Error(), "receipt block hash mismatch") {
		t.Fatalf("processBlock error = %v, want receipt block hash mismatch", err)
	}
	if state.LastProcessedBlock != 100 || listener.lastBlockHash != "" {
		t.Fatalf("block advanced after invalid receipt: state=%#v last_hash=%q", state, listener.lastBlockHash)
	}
}

func TestCatchUpHoldsCheckpointOnTransientTraceFailure(t *testing.T) {
	server := newEVMCheckpointTestServer(t, func(req jsonRPCRequest) jsonRPCResponse {
		return jsonRPCResponse{
			ID: req.ID,
			Error: &jsonRPCError{
				Code:    -32000,
				Message: "temporary backend unavailable",
			},
		}
	})
	defer server.Close()

	state := &models.ChainState{
		ChainID:            constants.Base,
		LastProcessedBlock: 100,
		LastProcessedHash:  evmTestHash(100),
		LastConfirmedBlock: 100,
	}
	stateWrites := 0
	listener := NewRpcListener(
		evmTestChain{rpcURL: server.URL},
		configurations.NewAssetRegistry(),
		state,
		nil,
		func(*models.ChainState) error {
			stateWrites++
			return nil
		},
	)
	listener.client = server.Client()

	if err := listener.catchUp(); err == nil || !strings.Contains(err.Error(), "trace_block failed") {
		t.Fatalf("catchUp error = %v, want transient trace error", err)
	}
	if state.LastProcessedBlock != 100 || state.LastProcessedHash != evmTestHash(100) || state.LastConfirmedBlock != 100 {
		t.Fatalf("checkpoint advanced after trace failure: %#v", state)
	}
	if stateWrites != 0 {
		t.Fatalf("state writes = %d, want 0", stateWrites)
	}
}

func TestInternalTransfersSkipRootTraceAndUseStableTraceAddress(t *testing.T) {
	txHash := evmTestHash(9001)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var rpcReq jsonRPCRequest
		if err := json.NewDecoder(req.Body).Decode(&rpcReq); err != nil {
			t.Fatal(err)
		}
		result := json.RawMessage(fmt.Sprintf(`[
			{"type":"call","action":{"callType":"call","from":"0x0000000000000000000000000000000000000001","to":"0x0000000000000000000000000000000000000002","value":"0x64"},"transactionHash":%q,"traceAddress":[]},
			{"type":"call","action":{"callType":"call","from":"0x0000000000000000000000000000000000000002","to":"0x0000000000000000000000000000000000000003","value":"0x5"},"transactionHash":%q,"traceAddress":[0,1]},
			{"type":"call","action":{"callType":"delegatecall","from":"0x0000000000000000000000000000000000000002","to":"0x0000000000000000000000000000000000000004","value":"0x5"},"transactionHash":%q,"traceAddress":[1]},
			{"type":"call","action":{"callType":"call","from":"0x0000000000000000000000000000000000000002","to":"0x0000000000000000000000000000000000000005","value":"0x5"},"error":"Reverted","transactionHash":%q,"traceAddress":[2]},
			{"type":"call","action":{"callType":"call","from":"0x0000000000000000000000000000000000000005","to":"0x0000000000000000000000000000000000000006","value":"0x5"},"transactionHash":%q,"traceAddress":[2,0]}
		]`, txHash, txHash, txHash, txHash, txHash))
		_ = json.NewEncoder(w).Encode(jsonRPCResponse{ID: rpcReq.ID, Result: result})
	}))
	defer server.Close()
	registry := configurations.NewAssetRegistry()
	native, ok := registry.GetNative(constants.Base)
	if !ok {
		t.Fatal("base native asset missing")
	}
	listener := NewRpcListener(evmTestChain{rpcURL: server.URL}, registry, &models.ChainState{ChainID: constants.Base}, nil, nil)
	listener.client = server.Client()
	listener.events = make(chan interface{}, 2)

	if err := listener.processInternalTransfers(context.Background(), "0x1", "1", evmTestHash(1), evmTestHash(0), native, []RawTx{{Hash: txHash}}, map[string]bool{strings.ToLower(txHash): true}); err != nil {
		t.Fatal(err)
	}
	select {
	case raw := <-listener.events:
		event, ok := raw.(dispatcher.Event)
		if !ok || event.Type != "internal_transfer" || event.Transaction == nil || event.Transaction.LogIndex == nil || *event.Transaction.LogIndex != "internal:0.1" {
			t.Fatalf("internal event = %#v", raw)
		}
	default:
		t.Fatal("internal trace was not emitted")
	}
	select {
	case extra := <-listener.events:
		t.Fatalf("root trace emitted duplicate native transfer: %#v", extra)
	default:
	}
}

func TestInternalTransfersRequireCompleteTraceRootCoverage(t *testing.T) {
	txHash := evmTestHash(9001)
	foreignHash := evmTestHash(9002)
	root := fmt.Sprintf(`{"type":"call","action":{"callType":"call","from":"0x0000000000000000000000000000000000000001","to":"0x0000000000000000000000000000000000000002","value":"0x1"},"transactionHash":%q,"traceAddress":[]}`, txHash)
	child := fmt.Sprintf(`{"type":"call","action":{"callType":"call","from":"0x0000000000000000000000000000000000000002","to":"0x0000000000000000000000000000000000000003","value":"0x1"},"transactionHash":%q,"traceAddress":[0]}`, txHash)

	cases := []struct {
		name       string
		tracesJSON string
		want       string
	}{
		{name: "empty response", tracesJSON: `[]`, want: "returned no traces"},
		{name: "child without root", tracesJSON: `[ ` + child + ` ]`, want: "0 root traces"},
		{name: "duplicate root", tracesJSON: `[ ` + root + `,` + root + ` ]`, want: "duplicate trace identity"},
		{name: "foreign transaction root", tracesJSON: fmt.Sprintf(`[{"type":"call","transactionHash":%q,"traceAddress":[]}]`, foreignHash), want: "outside block"},
		{name: "missing transaction hash", tracesJSON: `[{"type":"call","traceAddress":[]}]`, want: "missing transactionHash"},
	}

	registry := configurations.NewAssetRegistry()
	native, ok := registry.GetNative(constants.Base)
	if !ok {
		t.Fatal("base native asset missing")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				var rpcReq jsonRPCRequest
				if err := json.NewDecoder(req.Body).Decode(&rpcReq); err != nil {
					t.Fatal(err)
				}
				_ = json.NewEncoder(w).Encode(jsonRPCResponse{ID: rpcReq.ID, Result: json.RawMessage(tc.tracesJSON)})
			}))
			defer server.Close()

			listener := NewRpcListener(evmTestChain{rpcURL: server.URL}, registry, &models.ChainState{ChainID: constants.Base}, nil, nil)
			listener.client = server.Client()
			listener.events = make(chan interface{}, 2)
			err := listener.processInternalTransfers(
				context.Background(), "0x1", "1", evmTestHash(1), evmTestHash(0), native,
				[]RawTx{{Hash: txHash}}, map[string]bool{strings.ToLower(txHash): true},
			)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("processInternalTransfers error = %v, want %q", err, tc.want)
			}
			select {
			case event := <-listener.events:
				t.Fatalf("event emitted from incomplete trace response: %#v", event)
			default:
			}
		})
	}
}

func TestCatchUpDoesNotTreatMixedTraceFailuresAsUnsupported(t *testing.T) {
	transientServer := newEVMCheckpointTestServer(t, func(req jsonRPCRequest) jsonRPCResponse {
		return jsonRPCResponse{
			ID:    req.ID,
			Error: &jsonRPCError{Code: -32000, Message: "temporary trace backend unavailable"},
		}
	})
	defer transientServer.Close()
	unsupportedServer := newEVMCheckpointTestServer(t, func(req jsonRPCRequest) jsonRPCResponse {
		return jsonRPCResponse{
			ID:    req.ID,
			Error: &jsonRPCError{Code: -32601, Message: "the method trace_block does not exist"},
		}
	})
	defer unsupportedServer.Close()

	state := &models.ChainState{
		ChainID:            constants.Base,
		LastProcessedBlock: 100,
		LastProcessedHash:  evmTestHash(100),
		LastConfirmedBlock: 100,
	}
	listener := NewRpcListener(
		evmTestChain{rpcURLs: []string{transientServer.URL, unsupportedServer.URL}},
		configurations.NewAssetRegistry(),
		state,
		nil,
		nil,
	)

	if err := listener.catchUp(); err == nil || !strings.Contains(err.Error(), "temporary trace backend unavailable") {
		t.Fatalf("catchUp error = %v, want transient trace failure", err)
	}
	if listener.traceUnavailable {
		t.Fatal("trace capability disabled when at least one endpoint had a transient failure")
	}
	if state.LastProcessedBlock != 100 || state.LastProcessedHash != evmTestHash(100) || state.LastConfirmedBlock != 100 {
		t.Fatalf("checkpoint advanced after mixed trace failures: %#v", state)
	}
}

func TestCatchUpDoesNotAdvanceInMemoryCheckpointWhenStateWriterFails(t *testing.T) {
	var mu sync.Mutex
	requestedBlocks := make([]int64, 0, 2)
	server := newEVMCheckpointTestServerWithBlockObserver(t, func(blockNumber int64) {
		mu.Lock()
		requestedBlocks = append(requestedBlocks, blockNumber)
		mu.Unlock()
	}, func(req jsonRPCRequest) jsonRPCResponse {
		return jsonRPCResponse{
			ID: req.ID,
			Error: &jsonRPCError{
				Code:    -32601,
				Message: "the method trace_block does not exist",
			},
		}
	})
	defer server.Close()

	state := &models.ChainState{
		ChainID:            constants.Base,
		LastProcessedBlock: 100,
		LastProcessedHash:  evmTestHash(100),
		LastConfirmedBlock: 100,
	}
	writeErr := errors.New("checkpoint database unavailable")
	writes := 0
	listener := NewRpcListener(
		evmTestChain{rpcURL: server.URL},
		configurations.NewAssetRegistry(),
		state,
		nil,
		func(*models.ChainState) error {
			writes++
			if writes == 1 {
				return writeErr
			}
			return nil
		},
	)
	listener.client = server.Client()

	if err := listener.catchUp(); !errors.Is(err, writeErr) {
		t.Fatalf("first catchUp error = %v, want %v", err, writeErr)
	}
	if state.LastProcessedBlock != 100 || state.LastProcessedHash != evmTestHash(100) || state.LastConfirmedBlock != 100 {
		t.Fatalf("in-memory checkpoint advanced after write failure: %#v", state)
	}
	if err := listener.catchUp(); err != nil {
		t.Fatalf("second catchUp returned error: %v", err)
	}
	if state.LastProcessedBlock != 101 || state.LastProcessedHash != evmTestHash(101) || state.LastConfirmedBlock != 113 {
		t.Fatalf("checkpoint after successful retry = %#v", state)
	}
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(requestedBlocks) != "[101 101]" {
		t.Fatalf("requested blocks = %v, want retry of block 101", requestedBlocks)
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
		TransactionHash: evmTestHash(1001),
		BlockNumber:     "0x1",
		BlockHash:       evmTestHash(1),
		LogIndex:        "0x0",
	}, "confirmed", evmTestHash(0))
	if err != nil {
		t.Fatal(err)
	}

	select {
	case event := <-listener.events:
		t.Fatalf("unexpected zero amount token event: %#v", event)
	default:
	}
}

func TestValidateERC20TransferLogRejectsMalformedOrForeignLogs(t *testing.T) {
	txHash := evmTestHash(1001)
	valid := EVMLog{
		Address:         "0x00000000000000000000000000000000000000aa",
		Topics:          []string{TransferEventHash, evmAddressTopic("0x0000000000000000000000000000000000000001"), evmAddressTopic("0x0000000000000000000000000000000000000002")},
		Data:            "0x" + strings.Repeat("0", 63) + "1",
		TransactionHash: txHash,
	}
	if err := validateERC20TransferLog(valid, txHash); err != nil {
		t.Fatalf("valid ERC-20 log rejected: %v", err)
	}

	cases := []struct {
		name     string
		mutate   func(*EVMLog)
		expected string
	}{
		{name: "foreign transaction", mutate: func(log *EVMLog) { log.TransactionHash = evmTestHash(1002) }, expected: txHash},
		{name: "bad token address", mutate: func(log *EVMLog) { log.Address = "0xaa" }, expected: txHash},
		{name: "missing topic", mutate: func(log *EVMLog) { log.Topics = log.Topics[:2] }, expected: txHash},
		{name: "short address topic", mutate: func(log *EVMLog) { log.Topics[1] = "0x01" }, expected: txHash},
		{name: "dirty address padding", mutate: func(log *EVMLog) { log.Topics[1] = "0x" + strings.Repeat("1", 64) }, expected: txHash},
		{name: "short data", mutate: func(log *EVMLog) { log.Data = "0x01" }, expected: txHash},
		{name: "non-hex data", mutate: func(log *EVMLog) { log.Data = "0x" + strings.Repeat("z", 64) }, expected: txHash},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := valid
			entry.Topics = append([]string(nil), valid.Topics...)
			tc.mutate(&entry)
			if err := validateERC20TransferLog(entry, tc.expected); err == nil {
				t.Fatal("malformed ERC-20 Transfer log was accepted")
			}
		})
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

func newEVMCheckpointTestServer(t *testing.T, traceResponse func(jsonRPCRequest) jsonRPCResponse) *httptest.Server {
	t.Helper()
	return newEVMCheckpointTestServerWithBlockObserver(t, nil, traceResponse)
}

func newEVMCheckpointTestServerWithBlockObserver(
	t *testing.T,
	observeBlock func(int64),
	traceResponse func(jsonRPCRequest) jsonRPCResponse,
) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		switch req.Method {
		case "eth_blockNumber":
			_ = json.NewEncoder(w).Encode(jsonRPCResponse{ID: req.ID, Result: json.RawMessage(`"0x71"`)})
		case "eth_getBlockByNumber":
			blockHex, _ := req.Params[0].(string)
			blockNumber := hexToInt64(blockHex)
			if observeBlock != nil {
				observeBlock(blockNumber)
			}
			result := json.RawMessage(fmt.Sprintf(
				`{"number":%q,"hash":%q,"parentHash":%q,"transactions":[]}`,
				blockHex,
				evmTestHash(blockNumber),
				evmTestHash(blockNumber-1),
			))
			_ = json.NewEncoder(w).Encode(jsonRPCResponse{ID: req.ID, Result: result})
		case "eth_getBlockReceipts":
			_ = json.NewEncoder(w).Encode(jsonRPCResponse{ID: req.ID, Result: json.RawMessage(`[]`)})
		case "trace_block":
			_ = json.NewEncoder(w).Encode(traceResponse(req))
		default:
			t.Errorf("unexpected RPC method: %s", req.Method)
			http.Error(w, "unexpected method", http.StatusBadRequest)
		}
	}))
}

func evmTestHash(number int64) string {
	return fmt.Sprintf("0x%064x", number)
}

func evmAddressTopic(address string) string {
	return common.BytesToHash(common.HexToAddress(address).Bytes()).Hex()
}

func decodeEVMTestBlock(t *testing.T, transactionsJSON string) Block {
	t.Helper()
	raw := fmt.Sprintf(`{
		"number":"0x65",
		"hash":%q,
		"parentHash":%q,
		"transactions":[%s]
	}`, evmTestHash(101), evmTestHash(100), transactionsJSON)
	var block Block
	if err := json.Unmarshal([]byte(raw), &block); err != nil {
		t.Fatalf("decode block fixture: %v", err)
	}
	return block
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
