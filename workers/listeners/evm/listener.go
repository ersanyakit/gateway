package evm

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"core/asset"
	"core/blockchain"
	"core/blockchain/addrutil"
	"core/constants"
	"core/helpers"
	"core/models"
	"core/types"
	"core/workers/dispatcher"
	listenerconfig "core/workers/listeners"
	"core/workers/listeners/rpcutil"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
)

var TransferEventHash = strings.ToLower(crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)")).Hex())

const (
	pollInterval           = 6 * time.Second
	maxBlocksPerPoll       = int64(5)
	safeBlockConfirmations = int64(12)
	receiptBatchSize       = 50
	zeroAddress            = "0x0000000000000000000000000000000000000000"
)

type RpcListener struct {
	chain                 blockchain.Chain
	registry              *asset.Registry
	chainState            *models.ChainState
	stateWriter           func(*models.ChainState) error
	observeCanonicalBlock func(context.Context, constants.ChainID, int64, string, string) error
	bus                   *dispatcher.Dispatcher

	client          *http.Client
	endpointCircuit *rpcutil.EndpointCircuit

	mu      sync.Mutex
	quit    chan struct{}
	running bool
	events  chan interface{}

	traceUnavailable bool
	lastTraceWarning time.Time

	blockReceiptsUnavailable bool
	lastBlockReceiptsWarning time.Time
	batchReceiptsUnavailable bool
	lastBatchReceiptsWarning time.Time
	lastRetryableWarning     time.Time
	throttleErrors           int
	lastBlockNumber          int64
	lastBlockHash            string
	lastBlockParentHash      string
}

func (r *RpcListener) SetCanonicalBlockObserver(observer func(context.Context, constants.ChainID, int64, string, string) error) {
	r.observeCanonicalBlock = observer
}

func NewRpcListener(
	chain blockchain.Chain,
	registry *asset.Registry,
	state *models.ChainState,
	bus *dispatcher.Dispatcher,
	stateWriter func(*models.ChainState) error,
) *RpcListener {
	if state == nil {
		state = &models.ChainState{ChainID: chain.ChainID()}
	}

	return &RpcListener{
		chain:           chain,
		registry:        registry,
		chainState:      state,
		bus:             bus,
		stateWriter:     stateWriter,
		client:          &http.Client{Timeout: 25 * time.Second},
		endpointCircuit: rpcutil.NewEndpointCircuit(),
		quit:            make(chan struct{}),
		events:          make(chan interface{}, 1000),
	}
}

func (r *RpcListener) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running {
		return fmt.Errorf("listener already running")
	}
	if len(r.chain.RPCs()) == 0 {
		return fmt.Errorf("%s has no HTTP RPC configured", r.chain.Name())
	}

	r.running = true
	helpers.GoSafelyRestarting("listener.evm."+r.chain.Name(), r.quit, time.Second, r.pollLoop)

	return nil
}

func (r *RpcListener) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.running {
		return nil
	}

	close(r.quit)
	r.running = false
	return nil
}

func (r *RpcListener) Events() <-chan interface{} {
	return r.events
}

func (r *RpcListener) pollLoop() {
	for {
		delay := pollInterval
		if err := r.catchUp(); err != nil {
			if rpcutil.IsRetryable(err) {
				r.throttleErrors++
				delay = rpcutil.ThrottleDelay(err, r.throttleErrors, pollInterval)
				if time.Since(r.lastRetryableWarning) >= time.Minute {
					r.lastRetryableWarning = time.Now()
					log.Printf("[%s] listener transient RPC; checkpoint held; retrying in %s: %v\n", r.chain.Name(), delay.Round(time.Second), err)
				}
			} else {
				log.Printf("[%s] listener catch-up error: %v\n", r.chain.Name(), err)
				r.throttleErrors = 0
			}
		} else {
			r.throttleErrors = 0
		}

		timer := time.NewTimer(delay)
		select {
		case <-r.quit:
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (r *RpcListener) catchUp() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	latest, err := r.latestBlockNumber(ctx)
	if err != nil {
		return err
	}
	confirmedHead := latest
	safeLatest := latest - safeBlockConfirmations
	if safeLatest <= 0 {
		return nil
	}
	decision, err := listenerconfig.ResolveStartBlock(r.chain, r.chainState.LastProcessedBlock, safeLatest)
	if err != nil {
		return err
	}
	if decision.Warning != "" {
		log.Printf("[%s] scanner start policy: %s\n", r.chain.Name(), decision.Warning)
	}
	listenerconfig.ApplyStartBlockDecision(r.chainState, decision)
	from := decision.From
	if from > safeLatest {
		return nil
	}

	to := from + maxBlocksPerPoll - 1
	if to > safeLatest {
		to = safeLatest
	}

	for blockNumber := from; blockNumber <= to; blockNumber++ {
		if err := r.processBlock(ctx, blockNumber); err != nil {
			return err
		}

		nextState := *r.chainState
		listenerconfig.RecordProcessedBlockCheckpoint(&nextState, blockNumber, r.lastBlockHash, r.lastBlockParentHash)
		nextState.LastConfirmedBlock = confirmedHead
		if r.stateWriter != nil {
			if err := r.stateWriter(&nextState); err != nil {
				return fmt.Errorf("write chain state: %w", err)
			}
		}
		*r.chainState = nextState
	}

	return nil
}

func (r *RpcListener) latestBlockNumber(ctx context.Context) (int64, error) {
	var result string
	if err := r.rpcCall(ctx, "eth_blockNumber", []interface{}{}, &result); err != nil {
		return 0, err
	}
	return hexToInt64(result), nil
}

type jsonRPCRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      int64         `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

type jsonRPCResponse struct {
	ID     int64           `json:"id,omitempty"`
	Result json.RawMessage `json:"result"`
	Error  *jsonRPCError   `json:"error"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (r *RpcListener) rpcCall(ctx context.Context, method string, params []interface{}, out interface{}) error {
	return r.rpcCallValidated(ctx, method, params, out, nil)
}

func (r *RpcListener) rpcCallValidated(ctx context.Context, method string, params []interface{}, out interface{}, validate func() error) error {
	requestID := time.Now().UnixNano()
	body, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      requestID,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return err
	}

	var lastErr error
	var throttleErr error
	var traceNonCapabilityErr error
	rememberFailure := func(err error) {
		lastErr = err
		if method == "trace_block" && !isTraceUnavailableError(err) {
			traceNonCapabilityErr = err
		}
	}
	for _, rpcURL := range r.rpcURLs() {
		endpointCtx, cancel := rpcutil.WithEndpointTimeout(ctx)
		req, err := http.NewRequestWithContext(endpointCtx, http.MethodPost, rpcURL, bytes.NewReader(body))
		if err != nil {
			cancel()
			rememberFailure(fmt.Errorf("%s %s request build failed: %w", r.chain.Name(), rpcURL, err))
			continue
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := r.client.Do(req)
		if err != nil {
			cancel()
			rememberFailure(fmt.Errorf("%s %s request failed: %w", r.chain.Name(), rpcURL, err))
			r.recordRPCFailure(rpcURL, lastErr)
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		cancel()
		if readErr != nil {
			rememberFailure(fmt.Errorf("%s %s response read failed: %w", r.chain.Name(), rpcURL, readErr))
			r.recordRPCFailure(rpcURL, lastErr)
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			err := fmt.Errorf("%s %s returned HTTP %d: %s", r.chain.Name(), rpcURL, resp.StatusCode, string(respBody))
			if rpcutil.StatusThrottled(resp.StatusCode) {
				rememberFailure(rpcutil.NewThrottleError(err, rpcutil.RetryAfter(resp.Header)))
				throttleErr = lastErr
			} else {
				rememberFailure(err)
			}
			r.recordRPCFailure(rpcURL, lastErr)
			continue
		}

		var rpcResp jsonRPCResponse
		if err := json.Unmarshal(respBody, &rpcResp); err != nil {
			rememberFailure(fmt.Errorf("%s %s response decode failed: %w", r.chain.Name(), rpcURL, err))
			r.recordRPCFailure(rpcURL, lastErr)
			continue
		}
		if rpcResp.ID != requestID {
			rememberFailure(fmt.Errorf("%s %s RPC %s response id mismatch: got %d want %d", r.chain.Name(), rpcURL, method, rpcResp.ID, requestID))
			r.recordRPCFailure(rpcURL, lastErr)
			continue
		}
		if rpcResp.Error != nil {
			err := fmt.Errorf("%s RPC %s error %d: %s", r.chain.Name(), method, rpcResp.Error.Code, rpcResp.Error.Message)
			if rpcutil.JSONRPCThrottled(rpcResp.Error.Code, rpcResp.Error.Message) {
				rememberFailure(rpcutil.NewThrottleError(err, 0))
				throttleErr = lastErr
			} else {
				rememberFailure(err)
			}
			r.recordRPCFailure(rpcURL, lastErr)
			continue
		}
		if out == nil {
			r.recordRPCSuccess(rpcURL)
			return nil
		}
		resetRPCOutput(out)
		if err := json.Unmarshal(rpcResp.Result, out); err != nil {
			rememberFailure(fmt.Errorf("%s %s response decode failed: %w", r.chain.Name(), rpcURL, err))
			r.recordRPCFailure(rpcURL, lastErr)
			continue
		}
		if validate != nil {
			if err := validate(); err != nil {
				rememberFailure(fmt.Errorf("%s %s RPC %s response integrity failed: %w", r.chain.Name(), rpcURL, method, err))
				r.recordRPCFailure(rpcURL, lastErr)
				continue
			}
		}

		r.recordRPCSuccess(rpcURL)
		return nil
	}

	if throttleErr != nil {
		return throttleErr
	}
	if traceNonCapabilityErr != nil {
		return traceNonCapabilityErr
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no RPC endpoint configured")
	}
	return lastErr
}

func resetRPCOutput(out interface{}) {
	value := reflect.ValueOf(out)
	if value.Kind() != reflect.Ptr || value.IsNil() || !value.Elem().CanSet() {
		return
	}
	value.Elem().Set(reflect.Zero(value.Elem().Type()))
}

func (r *RpcListener) rpcBatchCall(ctx context.Context, requests []jsonRPCRequest) (map[int64]json.RawMessage, error) {
	if len(requests) == 0 {
		return map[int64]json.RawMessage{}, nil
	}
	expectedIDs := make(map[int64]struct{}, len(requests))
	for _, request := range requests {
		if _, duplicate := expectedIDs[request.ID]; duplicate {
			return nil, fmt.Errorf("duplicate JSON-RPC batch request id %d", request.ID)
		}
		expectedIDs[request.ID] = struct{}{}
	}

	body, err := json.Marshal(requests)
	if err != nil {
		return nil, err
	}

	var lastErr error
	var throttleErr error
	for _, rpcURL := range r.rpcURLs() {
		endpointCtx, cancel := rpcutil.WithEndpointTimeout(ctx)
		req, err := http.NewRequestWithContext(endpointCtx, http.MethodPost, rpcURL, bytes.NewReader(body))
		if err != nil {
			cancel()
			lastErr = fmt.Errorf("%s %s request build failed: %w", r.chain.Name(), rpcURL, err)
			continue
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := r.client.Do(req)
		if err != nil {
			cancel()
			lastErr = fmt.Errorf("%s %s request failed: %w", r.chain.Name(), rpcURL, err)
			r.recordRPCFailure(rpcURL, lastErr)
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		cancel()
		if readErr != nil {
			lastErr = fmt.Errorf("%s %s response read failed: %w", r.chain.Name(), rpcURL, readErr)
			r.recordRPCFailure(rpcURL, lastErr)
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			err := fmt.Errorf("%s %s returned HTTP %d: %s", r.chain.Name(), rpcURL, resp.StatusCode, string(respBody))
			if rpcutil.StatusThrottled(resp.StatusCode) {
				lastErr = rpcutil.NewThrottleError(err, rpcutil.RetryAfter(resp.Header))
				throttleErr = lastErr
			} else {
				lastErr = err
			}
			r.recordRPCFailure(rpcURL, lastErr)
			continue
		}

		var rpcResponses []jsonRPCResponse
		if err := json.Unmarshal(respBody, &rpcResponses); err != nil {
			var rpcResp jsonRPCResponse
			if singleErr := json.Unmarshal(respBody, &rpcResp); singleErr == nil && rpcResp.Error != nil {
				err := fmt.Errorf("%s RPC batch error %d: %s", r.chain.Name(), rpcResp.Error.Code, rpcResp.Error.Message)
				if rpcutil.JSONRPCThrottled(rpcResp.Error.Code, rpcResp.Error.Message) {
					lastErr = rpcutil.NewThrottleError(err, 0)
					throttleErr = lastErr
				} else {
					lastErr = err
				}
				r.recordRPCFailure(rpcURL, lastErr)
				continue
			}
			lastErr = fmt.Errorf("%s %s batch response decode failed: %w", r.chain.Name(), rpcURL, err)
			r.recordRPCFailure(rpcURL, lastErr)
			continue
		}
		if len(rpcResponses) == 0 {
			lastErr = fmt.Errorf("%s returned empty JSON-RPC batch response", r.chain.Name())
			r.recordRPCFailure(rpcURL, lastErr)
			continue
		}

		results := make(map[int64]json.RawMessage, len(rpcResponses))
		failed := false
		for _, rpcResp := range rpcResponses {
			if _, expected := expectedIDs[rpcResp.ID]; !expected {
				lastErr = fmt.Errorf("%s RPC batch returned unexpected response id %d", r.chain.Name(), rpcResp.ID)
				r.recordRPCFailure(rpcURL, lastErr)
				failed = true
				break
			}
			if _, duplicate := results[rpcResp.ID]; duplicate {
				lastErr = fmt.Errorf("%s RPC batch returned duplicate response id %d", r.chain.Name(), rpcResp.ID)
				r.recordRPCFailure(rpcURL, lastErr)
				failed = true
				break
			}
			if rpcResp.Error != nil {
				err := fmt.Errorf("%s RPC batch error %d: %s", r.chain.Name(), rpcResp.Error.Code, rpcResp.Error.Message)
				if rpcutil.JSONRPCThrottled(rpcResp.Error.Code, rpcResp.Error.Message) {
					lastErr = rpcutil.NewThrottleError(err, 0)
					throttleErr = lastErr
				} else {
					lastErr = err
				}
				r.recordRPCFailure(rpcURL, lastErr)
				failed = true
				break
			}
			results[rpcResp.ID] = rpcResp.Result
		}
		if failed {
			continue
		}
		if len(results) != len(expectedIDs) {
			lastErr = fmt.Errorf("%s RPC batch response incomplete: got %d of %d responses", r.chain.Name(), len(results), len(expectedIDs))
			r.recordRPCFailure(rpcURL, lastErr)
			continue
		}

		r.recordRPCSuccess(rpcURL)
		return results, nil
	}

	if throttleErr != nil {
		return nil, throttleErr
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no RPC endpoint configured")
	}
	return nil, lastErr
}

func (r *RpcListener) rpcURLs() []string {
	if r == nil || r.chain == nil {
		return nil
	}
	urls := r.chain.RPCs()
	if r.endpointCircuit == nil {
		return urls
	}
	return r.endpointCircuit.Rank(urls)
}

func (r *RpcListener) recordRPCSuccess(url string) {
	if r != nil && r.endpointCircuit != nil {
		r.endpointCircuit.RecordSuccess(url)
	}
}

func (r *RpcListener) recordRPCFailure(url string, err error) {
	if r != nil && r.endpointCircuit != nil {
		r.endpointCircuit.RecordFailure(url, err)
	}
}

type Block struct {
	Number       string  `json:"number"`
	Hash         string  `json:"hash"`
	ParentHash   string  `json:"parentHash"`
	Transactions []RawTx `json:"transactions"`
}

type RawTx struct {
	Hash             string `json:"hash"`
	From             string `json:"from"`
	To               string `json:"to"`
	Value            string `json:"value"`
	Input            string `json:"input"`
	TransactionIndex string `json:"transactionIndex"`

	// Ethereum represents contract creation with an explicitly present JSON null
	// `to` field. Keep presence and nullness separate so a provider response that
	// silently omits `to` cannot be mistaken for a contract creation.
	toPresent bool
	toNull    bool
}

func (t *RawTx) UnmarshalJSON(data []byte) error {
	type rawTxWire struct {
		Hash             string          `json:"hash"`
		From             string          `json:"from"`
		To               json.RawMessage `json:"to"`
		Value            string          `json:"value"`
		Input            string          `json:"input"`
		TransactionIndex string          `json:"transactionIndex"`
	}

	var wire rawTxWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}

	t.Hash = wire.Hash
	t.From = wire.From
	t.Value = wire.Value
	t.Input = wire.Input
	t.TransactionIndex = wire.TransactionIndex
	t.To = ""
	t.toPresent = wire.To != nil
	t.toNull = false
	if !t.toPresent {
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(wire.To), []byte("null")) {
		t.toNull = true
		return nil
	}
	if err := json.Unmarshal(wire.To, &t.To); err != nil {
		return fmt.Errorf("decode transaction to: %w", err)
	}
	return nil
}

type Receipt struct {
	TransactionHash   string   `json:"transactionHash"`
	TransactionIndex  string   `json:"transactionIndex"`
	BlockNumber       string   `json:"blockNumber"`
	BlockHash         string   `json:"blockHash"`
	Status            string   `json:"status"`
	ContractAddress   string   `json:"contractAddress"`
	GasUsed           string   `json:"gasUsed"`
	EffectiveGasPrice string   `json:"effectiveGasPrice"`
	Logs              []EVMLog `json:"logs"`
}

type EVMLog struct {
	Address         string   `json:"address"`
	Topics          []string `json:"topics"`
	Data            string   `json:"data"`
	TransactionHash string   `json:"transactionHash"`
	BlockNumber     string   `json:"blockNumber"`
	BlockHash       string   `json:"blockHash"`
	LogIndex        string   `json:"logIndex"`
}

func validateBlockResponse(requestedBlockNumber int64, block Block) error {
	actualBlockNumber, err := parseEVMQuantity(block.Number)
	if err != nil {
		return fmt.Errorf("invalid block number %q: %w", block.Number, err)
	}
	if actualBlockNumber != requestedBlockNumber {
		return fmt.Errorf("block number mismatch: got %d want %d", actualBlockNumber, requestedBlockNumber)
	}
	if err := validateEVMHash("block hash", block.Hash, false); err != nil {
		return err
	}
	if err := validateEVMHash("parent hash", block.ParentHash, requestedBlockNumber == 0); err != nil {
		return err
	}
	if strings.EqualFold(block.Hash, block.ParentHash) {
		return errors.New("block hash equals parent hash")
	}
	seenTransactions := make(map[string]struct{}, len(block.Transactions))
	for index, tx := range block.Transactions {
		if err := validateRawTransaction(tx, index); err != nil {
			return err
		}
		txID := strings.ToLower(strings.TrimSpace(tx.Hash))
		if _, duplicate := seenTransactions[txID]; duplicate {
			return fmt.Errorf("block contains duplicate transaction hash %s", tx.Hash)
		}
		seenTransactions[txID] = struct{}{}
	}
	return nil
}

func validateRawTransaction(tx RawTx, expectedIndex int) error {
	if err := validateEVMHash(fmt.Sprintf("transaction %d hash", expectedIndex), tx.Hash, false); err != nil {
		return err
	}
	if err := validateEVMAddress(fmt.Sprintf("transaction %d from", expectedIndex), tx.From, false); err != nil {
		return err
	}
	if !tx.toPresent {
		return fmt.Errorf("transaction %d to field is missing", expectedIndex)
	}
	if tx.toNull {
		if strings.TrimSpace(tx.To) != "" {
			return fmt.Errorf("transaction %d contract creation has a non-empty to address", expectedIndex)
		}
	} else if err := validateEVMAddress(fmt.Sprintf("transaction %d to", expectedIndex), tx.To, true); err != nil {
		return err
	}

	value, err := hexutil.DecodeBig(strings.TrimSpace(tx.Value))
	if err != nil {
		return fmt.Errorf("transaction %d value %q is invalid: %w", expectedIndex, tx.Value, err)
	}
	if value.Sign() < 0 {
		return fmt.Errorf("transaction %d value is negative", expectedIndex)
	}

	transactionIndex, err := parseEVMQuantity(tx.TransactionIndex)
	if err != nil {
		return fmt.Errorf("transaction %d index %q is invalid: %w", expectedIndex, tx.TransactionIndex, err)
	}
	if transactionIndex != int64(expectedIndex) {
		return fmt.Errorf("transaction index mismatch: got %d want %d", transactionIndex, expectedIndex)
	}
	return nil
}

func validateReceiptForTransaction(receipt Receipt, tx RawTx, block Block) error {
	if receipt.TransactionHash == "" {
		return errors.New("receipt transaction hash is empty")
	}
	if !strings.EqualFold(receipt.TransactionHash, tx.Hash) {
		return fmt.Errorf("receipt transaction hash mismatch: got %s want %s", receipt.TransactionHash, tx.Hash)
	}
	if err := validateEVMHash("receipt transaction hash", receipt.TransactionHash, false); err != nil {
		return err
	}
	receiptTransactionIndex, err := parseEVMQuantity(receipt.TransactionIndex)
	if err != nil {
		return fmt.Errorf("invalid receipt transaction index %q: %w", receipt.TransactionIndex, err)
	}
	transactionIndex, err := parseEVMQuantity(tx.TransactionIndex)
	if err != nil {
		return fmt.Errorf("invalid transaction index %q: %w", tx.TransactionIndex, err)
	}
	if receiptTransactionIndex != transactionIndex {
		return fmt.Errorf("receipt transaction index mismatch: got %d want %d", receiptTransactionIndex, transactionIndex)
	}
	if strings.TrimSpace(receipt.ContractAddress) != "" {
		if err := validateEVMAddress("receipt contract address", receipt.ContractAddress, true); err != nil {
			return err
		}
	}

	blockNumber, err := parseEVMQuantity(block.Number)
	if err != nil {
		return fmt.Errorf("invalid enclosing block number %q: %w", block.Number, err)
	}
	receiptBlockNumber, err := parseEVMQuantity(receipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("invalid receipt block number %q: %w", receipt.BlockNumber, err)
	}
	if receiptBlockNumber != blockNumber {
		return fmt.Errorf("receipt block number mismatch: got %d want %d", receiptBlockNumber, blockNumber)
	}
	if err := validateEVMHash("receipt block hash", receipt.BlockHash, false); err != nil {
		return err
	}
	if !strings.EqualFold(receipt.BlockHash, block.Hash) {
		return fmt.Errorf("receipt block hash mismatch: got %s want %s", receipt.BlockHash, block.Hash)
	}

	for index, entry := range receipt.Logs {
		if !strings.EqualFold(entry.TransactionHash, tx.Hash) {
			return fmt.Errorf("receipt log %d transaction hash mismatch: got %s want %s", index, entry.TransactionHash, tx.Hash)
		}
		logBlockNumber, err := parseEVMQuantity(entry.BlockNumber)
		if err != nil {
			return fmt.Errorf("invalid receipt log %d block number %q: %w", index, entry.BlockNumber, err)
		}
		if logBlockNumber != blockNumber {
			return fmt.Errorf("receipt log %d block number mismatch: got %d want %d", index, logBlockNumber, blockNumber)
		}
		if err := validateEVMHash(fmt.Sprintf("receipt log %d block hash", index), entry.BlockHash, false); err != nil {
			return err
		}
		if !strings.EqualFold(entry.BlockHash, block.Hash) {
			return fmt.Errorf("receipt log %d block hash mismatch: got %s want %s", index, entry.BlockHash, block.Hash)
		}
		if _, err := parseEVMQuantity(entry.LogIndex); err != nil {
			return fmt.Errorf("invalid receipt log %d index %q: %w", index, entry.LogIndex, err)
		}
	}
	return nil
}

func parseEVMQuantity(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	value, err := hexutil.DecodeBig(raw)
	if err != nil {
		return 0, err
	}
	if value.Sign() < 0 || !value.IsInt64() {
		return 0, fmt.Errorf("quantity is outside int64 range")
	}
	return value.Int64(), nil
}

func validateEVMHash(field string, raw string, allowZero bool) error {
	raw = strings.TrimSpace(raw)
	if len(raw) != 2+common.HashLength*2 || !strings.HasPrefix(strings.ToLower(raw), "0x") {
		return fmt.Errorf("%s is not a 32-byte hex value", field)
	}
	decoded, err := hex.DecodeString(raw[2:])
	if err != nil {
		return fmt.Errorf("%s is not valid hex: %w", field, err)
	}
	if !allowZero {
		allZero := true
		for _, value := range decoded {
			if value != 0 {
				allZero = false
				break
			}
		}
		if allZero {
			return fmt.Errorf("%s is zero", field)
		}
	}
	return nil
}

func validateEVMAddress(field string, raw string, allowZero bool) error {
	raw = strings.TrimSpace(raw)
	if len(raw) != 2+common.AddressLength*2 || !strings.HasPrefix(strings.ToLower(raw), "0x") {
		return fmt.Errorf("%s is not a 20-byte hex value", field)
	}
	decoded, err := hex.DecodeString(raw[2:])
	if err != nil {
		return fmt.Errorf("%s is not valid hex: %w", field, err)
	}
	if !allowZero {
		allZero := true
		for _, value := range decoded {
			if value != 0 {
				allZero = false
				break
			}
		}
		if allZero {
			return fmt.Errorf("%s is zero", field)
		}
	}
	return nil
}

func (r *RpcListener) processBlock(ctx context.Context, blockNumber int64) error {
	blockHex := fmt.Sprintf("0x%x", blockNumber)

	var block Block
	if err := r.rpcCallValidated(ctx, "eth_getBlockByNumber", []interface{}{blockHex, true}, &block, func() error {
		return validateBlockResponse(blockNumber, block)
	}); err != nil {
		return err
	}

	readableBlockNumber := hexToDec(block.Number)
	parsedBlockNumber := hexToInt64(block.Number)
	continuityState := *r.chainState
	if err := listenerconfig.ValidateParentContinuity(&continuityState, parsedBlockNumber, block.ParentHash); err != nil {
		if r.observeCanonicalBlock != nil {
			if observeErr := r.observeCanonicalBlock(ctx, r.chain.ChainID(), parsedBlockNumber, block.Hash, block.ParentHash); observeErr != nil {
				return fmt.Errorf("observe canonical block after parent continuity failure: %w", observeErr)
			}
		}
		listenerconfig.RewindParentContinuityCheckpoint(&continuityState, parsedBlockNumber)
		if r.stateWriter != nil {
			if writeErr := r.stateWriter(&continuityState); writeErr != nil {
				return fmt.Errorf("write chain rollback state: %w", writeErr)
			}
		}
		*r.chainState = continuityState
		return err
	}
	if r.observeCanonicalBlock != nil {
		if err := r.observeCanonicalBlock(ctx, r.chain.ChainID(), parsedBlockNumber, block.Hash, block.ParentHash); err != nil {
			return fmt.Errorf("observe canonical block: %w", err)
		}
	}

	nativeAsset, ok := r.registry.GetNative(r.chain.ChainID())
	if !ok {
		return fmt.Errorf("native asset is not registered")
	}

	receiptsByHash, err := r.receiptsByBlock(ctx, blockHex, block)
	if err != nil {
		return err
	}
	if receiptsByHash == nil {
		receiptsByHash, err = r.receiptsByTransactions(ctx, block.Transactions)
		if err != nil {
			return err
		}
	}
	txExecutionSucceeded := make(map[string]bool, len(block.Transactions))
	for idx, tx := range block.Transactions {
		if tx.Hash == "" {
			continue
		}

		receipt := Receipt{}
		if receiptsByHash != nil {
			receipt = receiptsByHash[strings.ToLower(tx.Hash)]
		}
		if validateReceiptForTransaction(receipt, tx, block) != nil {
			receiptErr := r.rpcCallValidated(ctx, "eth_getTransactionReceipt", []interface{}{tx.Hash}, &receipt, func() error {
				return validateReceiptForTransaction(receipt, tx, block)
			})
			if receiptErr != nil {
				return fmt.Errorf("receipt fetch failed for %s: %w", tx.Hash, receiptErr)
			}
		}

		status := receiptStatus(receipt.Status)
		if status == "" {
			return fmt.Errorf("unsupported receipt status %q for transaction %s", receipt.Status, tx.Hash)
		}
		txExecutionSucceeded[strings.ToLower(strings.TrimSpace(tx.Hash))] = status == models.TransactionStatusConfirmed
		to := tx.To
		if tx.toNull {
			to = strings.TrimSpace(receipt.ContractAddress)
			if status == models.TransactionStatusConfirmed {
				if err := validateEVMAddress("confirmed contract creation address", to, false); err != nil {
					return fmt.Errorf("transaction %s: %w", tx.Hash, err)
				}
			}
			if to == "" {
				to = zeroAddress
			}
		}

		value, err := hexutil.DecodeBig(strings.TrimSpace(tx.Value))
		if err != nil {
			return fmt.Errorf("transaction %s value became invalid after block validation: %w", tx.Hash, err)
		}
		eventType := "transaction"
		if value.Sign() > 0 {
			eventType = "native_transfer"
		} else if isContractInput(tx.Input) || receipt.ContractAddress != "" {
			eventType = "contract_transaction"
		}

		parsedTransactionIndex, err := parseEVMQuantity(tx.TransactionIndex)
		if err != nil || parsedTransactionIndex != int64(idx) {
			return fmt.Errorf("transaction %s index failed post-validation integrity check", tx.Hash)
		}
		txIndex := strconv.FormatInt(parsedTransactionIndex, 10)

		txParam := &types.TransactionParam{
			Context:    context.Background(),
			ChainID:    r.chain.ChainID(),
			Symbol:     helpers.StrPtr(nativeAsset.GetSymbol()),
			Decimals:   nativeAsset.GetDecimals(),
			Hash:       helpers.StrPtr(tx.Hash),
			Block:      helpers.StrPtr(readableBlockNumber),
			BlockHash:  helpers.StrPtr(block.Hash),
			ParentHash: helpers.StrPtr(block.ParentHash),
			Token:      nil,
			From:       helpers.StrPtr(r.normalizeAddress(tx.From)),
			To:         helpers.StrPtr(r.normalizeAddress(to)),
			Amount:     helpers.StrPtr(value.String()),
			LogIndex:   helpers.StrPtr("tx:" + txIndex),
			Status:     helpers.StrPtr(status),
			GasUsed:    optionalHexBigString(receipt.GasUsed),
			GasPrice:   optionalHexBigString(receipt.EffectiveGasPrice),
		}
		if err := r.dispatch(ctx, eventType, txParam); err != nil {
			return err
		}

		for _, entry := range receipt.Logs {
			if isTransferLog(entry) {
				// ERC-721 uses the same Transfer signature with an indexed token ID
				// in a fourth topic. It is deliberately outside this fungible-token
				// listener; all other shapes carrying this signature are malformed.
				if len(entry.Topics) == 4 {
					continue
				}
				if err := r.handleTransferLogForTransaction(ctx, entry, status, block.ParentHash, tx.Hash); err != nil {
					return err
				}
			}
		}
	}

	if err := r.processInternalTransfers(ctx, blockHex, readableBlockNumber, block.Hash, block.ParentHash, nativeAsset, block.Transactions, txExecutionSucceeded); err != nil {
		return err
	}
	r.lastBlockNumber = blockNumber
	r.lastBlockHash = block.Hash
	r.lastBlockParentHash = block.ParentHash
	return nil
}

func (r *RpcListener) receiptsByBlock(ctx context.Context, blockHex string, block Block) (map[string]Receipt, error) {
	if r.blockReceiptsUnavailable {
		return nil, nil
	}

	var receipts []Receipt
	if err := r.rpcCall(ctx, "eth_getBlockReceipts", []interface{}{blockHex}, &receipts); err != nil {
		if rpcutil.IsThrottle(err) {
			return nil, err
		}
		if isBlockReceiptsUnavailableError(err) {
			r.blockReceiptsUnavailable = true
			log.Printf("[%s] eth_getBlockReceipts unavailable; falling back to per-transaction receipts: %v\n", r.chain.Name(), err)
			return nil, nil
		}
		if time.Since(r.lastBlockReceiptsWarning) >= time.Minute {
			r.lastBlockReceiptsWarning = time.Now()
			log.Printf("[%s] eth_getBlockReceipts failed for block %s; falling back to per-transaction receipts: %v\n", r.chain.Name(), blockHex, err)
		}
		return nil, nil
	}

	if len(receipts) != len(block.Transactions) {
		if time.Since(r.lastBlockReceiptsWarning) >= time.Minute {
			r.lastBlockReceiptsWarning = time.Now()
			log.Printf("[%s] eth_getBlockReceipts returned %d of %d receipts for block %s; falling back to transaction receipts\n", r.chain.Name(), len(receipts), len(block.Transactions), blockHex)
		}
		return nil, nil
	}

	txsByHash := make(map[string]RawTx, len(block.Transactions))
	for _, tx := range block.Transactions {
		txsByHash[strings.ToLower(tx.Hash)] = tx
	}
	byHash := make(map[string]Receipt, len(receipts))
	for _, receipt := range receipts {
		hash := strings.ToLower(receipt.TransactionHash)
		tx, expected := txsByHash[hash]
		if !expected {
			return nil, nil
		}
		if _, duplicate := byHash[hash]; duplicate {
			return nil, nil
		}
		if err := validateReceiptForTransaction(receipt, tx, block); err != nil {
			if time.Since(r.lastBlockReceiptsWarning) >= time.Minute {
				r.lastBlockReceiptsWarning = time.Now()
				log.Printf("[%s] eth_getBlockReceipts integrity failure for block %s; falling back to transaction receipts: %v\n", r.chain.Name(), blockHex, err)
			}
			return nil, nil
		}
		byHash[hash] = receipt
	}
	return byHash, nil
}

func (r *RpcListener) receiptsByTransactions(ctx context.Context, txs []RawTx) (map[string]Receipt, error) {
	if r.batchReceiptsUnavailable || len(txs) == 0 {
		return nil, nil
	}

	byHash := make(map[string]Receipt, len(txs))
	for start := 0; start < len(txs); start += receiptBatchSize {
		end := start + receiptBatchSize
		if end > len(txs) {
			end = len(txs)
		}

		requests := make([]jsonRPCRequest, 0, end-start)
		idToHash := make(map[int64]string, end-start)
		nextID := int64(1)
		for _, tx := range txs[start:end] {
			if tx.Hash == "" {
				continue
			}
			requests = append(requests, jsonRPCRequest{
				JSONRPC: "2.0",
				ID:      nextID,
				Method:  "eth_getTransactionReceipt",
				Params:  []interface{}{tx.Hash},
			})
			idToHash[nextID] = strings.ToLower(tx.Hash)
			nextID++
		}
		if len(requests) == 0 {
			continue
		}

		results, err := r.rpcBatchCall(ctx, requests)
		if err != nil {
			if rpcutil.IsRetryable(err) {
				return nil, err
			}
			if isBatchReceiptsUnavailableError(err) {
				r.batchReceiptsUnavailable = true
			}
			if time.Since(r.lastBatchReceiptsWarning) >= time.Minute {
				r.lastBatchReceiptsWarning = time.Now()
				log.Printf("[%s] batch receipt fetch failed; falling back to per-transaction receipts: %v\n", r.chain.Name(), err)
			}
			return nil, nil
		}

		for id, raw := range results {
			if len(raw) == 0 || string(raw) == "null" {
				continue
			}
			var receipt Receipt
			if err := json.Unmarshal(raw, &receipt); err != nil {
				if time.Since(r.lastBatchReceiptsWarning) >= time.Minute {
					r.lastBatchReceiptsWarning = time.Now()
					log.Printf("[%s] batch receipt decode failed; falling back to per-transaction receipts: %v\n", r.chain.Name(), err)
				}
				return nil, nil
			}
			receiptHash := strings.ToLower(receipt.TransactionHash)
			expectedHash := idToHash[id]
			if receiptHash == "" || !strings.EqualFold(receiptHash, expectedHash) {
				if time.Since(r.lastBatchReceiptsWarning) >= time.Minute {
					r.lastBatchReceiptsWarning = time.Now()
					log.Printf("[%s] batch receipt transaction identity mismatch; falling back to per-transaction receipts\n", r.chain.Name())
				}
				return nil, nil
			}
			byHash[receiptHash] = receipt
		}
	}
	return byHash, nil
}

func (r *RpcListener) dispatch(ctx context.Context, eventType string, txParam *types.TransactionParam) error {
	event := dispatcher.Event{
		Chain:       r.chain.ChainID(),
		Type:        eventType,
		Transaction: txParam,
	}

	if r.bus != nil {
		if err := r.bus.DispatchAndWait(ctx, event); err != nil {
			return err
		}
	}

	select {
	case r.events <- event:
	default:
	}

	return nil
}

func isContractInput(input string) bool {
	input = strings.ToLower(input)
	return input != "" && input != "0x" && input != "0x0"
}

func isTransferLog(l EVMLog) bool {
	return len(l.Topics) > 0 && strings.EqualFold(strings.TrimSpace(l.Topics[0]), TransferEventHash)
}

func (r *RpcListener) handleTransferLog(ctx context.Context, l EVMLog, status string, parentHash string) error {
	return r.handleTransferLogForTransaction(ctx, l, status, parentHash, l.TransactionHash)
}

func (r *RpcListener) handleTransferLogForTransaction(ctx context.Context, l EVMLog, status string, parentHash, expectedTransactionHash string) error {
	if err := validateERC20TransferLog(l, expectedTransactionHash); err != nil {
		return err
	}

	token := common.HexToAddress(l.Address)
	from, err := decodeEVMAddressTopic("transfer from topic", l.Topics[1])
	if err != nil {
		return err
	}
	to, err := decodeEVMAddressTopic("transfer to topic", l.Topics[2])
	if err != nil {
		return err
	}

	decodedValue, err := hexutil.Decode(strings.TrimSpace(l.Data))
	if err != nil {
		return fmt.Errorf("decode transfer data: %w", err)
	}
	value := new(big.Int).SetBytes(decodedValue)
	if value.Sign() <= 0 {
		return nil
	}

	tokenID := r.normalizeTokenAddress(token.Hex())
	symbol := "ERC20"
	decimals := uint8(18)
	if r.chain.ChainID() == constants.TRON {
		symbol = "TRC20"
		decimals = 6
	}

	if r.registry != nil {
		if assetInfo, isRegistered := r.registry.Get(r.chain.ChainID(), tokenID); isRegistered {
			symbol = assetInfo.GetSymbol()
			decimals = assetInfo.GetDecimals()
		}
	}

	txParam := &types.TransactionParam{
		Context:    context.Background(),
		ChainID:    r.chain.ChainID(),
		Symbol:     helpers.StrPtr(symbol),
		Decimals:   decimals,
		Hash:       helpers.StrPtr(l.TransactionHash),
		Block:      helpers.StrPtr(hexToDec(l.BlockNumber)),
		BlockHash:  helpers.StrPtr(l.BlockHash),
		ParentHash: helpers.StrPtr(parentHash),
		Token:      helpers.StrPtr(tokenID),
		From:       helpers.StrPtr(r.normalizeAddress(from.Hex())),
		To:         helpers.StrPtr(r.normalizeAddress(to.Hex())),
		Amount:     helpers.StrPtr(value.String()),
		LogIndex:   helpers.StrPtr("log:" + hexToDec(l.LogIndex)),
		Status:     helpers.StrPtr(status),
	}

	return r.dispatch(ctx, "token_transfer", txParam)
}

func validateERC20TransferLog(l EVMLog, expectedTransactionHash string) error {
	if err := validateEVMHash("expected transfer transaction hash", expectedTransactionHash, false); err != nil {
		return err
	}
	if err := validateEVMHash("transfer transaction hash", l.TransactionHash, false); err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(l.TransactionHash), strings.TrimSpace(expectedTransactionHash)) {
		return fmt.Errorf("transfer log transaction hash mismatch: got %s want %s", l.TransactionHash, expectedTransactionHash)
	}
	if err := validateEVMAddress("transfer token address", l.Address, false); err != nil {
		return err
	}
	if len(l.Topics) != 3 {
		return fmt.Errorf("ERC-20 Transfer log has %d topics, want exactly 3", len(l.Topics))
	}
	if err := validateEVMHash("transfer signature topic", l.Topics[0], true); err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(l.Topics[0]), TransferEventHash) {
		return fmt.Errorf("unexpected transfer signature topic %q", l.Topics[0])
	}
	if _, err := decodeEVMAddressTopic("transfer from topic", l.Topics[1]); err != nil {
		return err
	}
	if _, err := decodeEVMAddressTopic("transfer to topic", l.Topics[2]); err != nil {
		return err
	}

	data := strings.TrimSpace(l.Data)
	if len(data) != 2+common.HashLength*2 || !strings.HasPrefix(strings.ToLower(data), "0x") {
		return errors.New("ERC-20 Transfer data is not exactly 32 bytes")
	}
	decoded, err := hex.DecodeString(data[2:])
	if err != nil {
		return fmt.Errorf("ERC-20 Transfer data is not valid hex: %w", err)
	}
	if len(decoded) != common.HashLength {
		return fmt.Errorf("ERC-20 Transfer data decoded to %d bytes, want 32", len(decoded))
	}
	return nil
}

func decodeEVMAddressTopic(field, raw string) (common.Address, error) {
	if err := validateEVMHash(field, raw, true); err != nil {
		return common.Address{}, err
	}
	decoded, err := hex.DecodeString(strings.TrimSpace(raw)[2:])
	if err != nil {
		return common.Address{}, fmt.Errorf("%s is not valid hex: %w", field, err)
	}
	for _, value := range decoded[:common.HashLength-common.AddressLength] {
		if value != 0 {
			return common.Address{}, fmt.Errorf("%s is not an ABI-encoded address", field)
		}
	}
	return common.BytesToAddress(decoded[common.HashLength-common.AddressLength:]), nil
}

type Trace struct {
	Type   string `json:"type"`
	Action struct {
		From          string `json:"from"`
		To            string `json:"to"`
		Value         string `json:"value"`
		Address       string `json:"address"`
		RefundAddress string `json:"refundAddress"`
		Balance       string `json:"balance"`
		CallType      string `json:"callType"`
	} `json:"action"`
	Result struct {
		Address string `json:"address"`
	} `json:"result"`
	Error           string `json:"error"`
	TransactionHash string `json:"transactionHash"`
	TraceAddress    []int  `json:"traceAddress"`
}

func (r *RpcListener) processInternalTransfers(ctx context.Context, blockHex, blockNumber, blockHash, parentHash string, nativeAsset asset.Asset, blockTransactions []RawTx, txExecutionSucceeded map[string]bool) error {
	requireTrace := evmTraceRequired()
	if r.traceUnavailable && !requireTrace {
		return nil
	}

	var traces []Trace
	if err := r.rpcCall(ctx, "trace_block", []interface{}{blockHex}, &traces); err != nil {
		if requireTrace {
			return fmt.Errorf("trace_block failed: %w", err)
		}
		if isTraceUnavailableError(err) {
			r.traceUnavailable = true
			log.Printf("[%s] trace_block unavailable; internal transfer tracing disabled for this listener: %v\n", r.chain.Name(), err)
			return nil
		}
		if time.Since(r.lastTraceWarning) >= time.Minute {
			r.lastTraceWarning = time.Now()
			log.Printf("[%s] trace_block failed; checkpoint held for block %s: %v\n", r.chain.Name(), blockNumber, err)
		}
		return fmt.Errorf("trace_block failed for block %s: %w", blockNumber, err)
	}

	blockTxIDs := make(map[string]struct{}, len(blockTransactions))
	blockTxByID := make(map[string]RawTx, len(blockTransactions))
	for _, transaction := range blockTransactions {
		if err := validateEVMHash("trace block transaction hash", transaction.Hash, false); err != nil {
			return err
		}
		txID := strings.ToLower(strings.TrimSpace(transaction.Hash))
		if _, duplicate := blockTxIDs[txID]; duplicate {
			return fmt.Errorf("trace block transaction set contains duplicate hash %s", transaction.Hash)
		}
		blockTxIDs[txID] = struct{}{}
		blockTxByID[txID] = transaction
		if _, known := txExecutionSucceeded[txID]; !known {
			return fmt.Errorf("transaction execution status is missing for %s", transaction.Hash)
		}
	}
	if len(blockTransactions) > 0 && len(traces) == 0 {
		return fmt.Errorf("trace_block returned no traces for %d transactions in block %s", len(blockTransactions), blockNumber)
	}

	seenTraceIdentities := make(map[string]struct{}, len(traces))
	rootTraceCounts := make(map[string]int, len(blockTransactions))
	failedTracePaths := make(map[string]map[string]struct{})
	for index, trace := range traces {
		txHash := strings.TrimSpace(trace.TransactionHash)
		traceType := strings.ToLower(strings.TrimSpace(trace.Type))
		if txHash == "" {
			// Parity-style clients may include protocol block-reward traces. They
			// are not transaction executions and therefore have no transactionHash.
			if traceType == "reward" {
				continue
			}
			return fmt.Errorf("trace_block trace %d is missing transactionHash", index)
		}
		if err := validateEVMHash(fmt.Sprintf("trace_block trace %d transaction hash", index), txHash, false); err != nil {
			return err
		}
		txID := strings.ToLower(txHash)
		if _, belongs := blockTxIDs[txID]; !belongs {
			return fmt.Errorf("trace_block returned transaction %s outside block %s", trace.TransactionHash, blockNumber)
		}
		for _, component := range trace.TraceAddress {
			if component < 0 {
				return fmt.Errorf("trace_block transaction %s has negative traceAddress component", trace.TransactionHash)
			}
		}

		identityKey := txID + ":" + traceAddressKey(trace.TraceAddress)
		if _, duplicate := seenTraceIdentities[identityKey]; duplicate {
			return fmt.Errorf("trace_block returned duplicate trace identity %s", identityKey)
		}
		seenTraceIdentities[identityKey] = struct{}{}
		if len(trace.TraceAddress) == 0 {
			if traceType != "call" && traceType != "create" {
				return fmt.Errorf("trace_block transaction %s root has unsupported type %q", trace.TransactionHash, trace.Type)
			}
			transaction := blockTxByID[txID]
			if transaction.toPresent {
				if transaction.toNull && traceType != "create" {
					return fmt.Errorf("contract creation transaction %s has %q root trace", trace.TransactionHash, trace.Type)
				}
				if !transaction.toNull && traceType != "call" {
					return fmt.Errorf("call transaction %s has %q root trace", trace.TransactionHash, trace.Type)
				}
			}
			rootTraceCounts[txID]++
		}
		if strings.TrimSpace(trace.Error) != "" {
			if failedTracePaths[txID] == nil {
				failedTracePaths[txID] = make(map[string]struct{})
			}
			failedTracePaths[txID][traceAddressKey(trace.TraceAddress)] = struct{}{}
		}
	}
	for txID := range blockTxIDs {
		if rootTraceCounts[txID] != 1 {
			return fmt.Errorf("trace_block returned %d root traces for transaction %s; want exactly 1", rootTraceCounts[txID], txID)
		}
	}
	for _, trace := range traces {
		if strings.TrimSpace(trace.TransactionHash) == "" {
			continue
		}
		// trace_block includes the root transaction call with traceAddress=[].
		// Its action.value is already emitted above as native_transfer; treating it
		// as internal would credit every plain native transfer twice.
		if len(trace.TraceAddress) == 0 {
			continue
		}
		txID := strings.ToLower(strings.TrimSpace(trace.TransactionHash))
		if !txExecutionSucceeded[txID] {
			// A reverted root transaction cannot produce a real internal value
			// transfer even when a child trace itself has no local error.
			continue
		}
		if tracePathFailed(trace.TraceAddress, failedTracePaths[txID]) {
			continue
		}
		traceIdentity := internalTraceLogIndex(trace.TraceAddress)

		from := trace.Action.From
		to := trace.Action.To
		valueRaw := trace.Action.Value
		switch strings.ToLower(strings.TrimSpace(trace.Type)) {
		case "call":
			switch strings.ToLower(strings.TrimSpace(trace.Action.CallType)) {
			case "call":
				// Only CALL transfers value to the target account. DELEGATECALL and
				// CALLCODE may expose inherited msg.value without moving funds.
			case "delegatecall", "staticcall", "callcode":
				continue
			default:
				return fmt.Errorf("trace %s has unsupported callType %q", traceIdentity, trace.Action.CallType)
			}
		case "create":
			// handled by the common action/result fields below
		case "suicide", "selfdestruct":
			from = trace.Action.Address
			to = trace.Action.RefundAddress
			valueRaw = trace.Action.Balance
		default:
			return fmt.Errorf("trace %s has unsupported type %q", traceIdentity, trace.Type)
		}
		value, err := hexutil.DecodeBig(strings.TrimSpace(valueRaw))
		if err != nil {
			return fmt.Errorf("trace %s has invalid value %q: %w", traceIdentity, valueRaw, err)
		}
		if value.Sign() == 0 {
			continue
		}

		if to == "" {
			to = trace.Result.Address
		}
		if err := validateEVMAddress(fmt.Sprintf("trace %s from", traceIdentity), from, false); err != nil {
			return err
		}
		if err := validateEVMAddress(fmt.Sprintf("trace %s to", traceIdentity), to, true); err != nil {
			return err
		}

		txParam := &types.TransactionParam{
			Context:    context.Background(),
			ChainID:    r.chain.ChainID(),
			Symbol:     helpers.StrPtr(nativeAsset.GetSymbol()),
			Decimals:   nativeAsset.GetDecimals(),
			Hash:       helpers.StrPtr(trace.TransactionHash),
			Block:      helpers.StrPtr(blockNumber),
			BlockHash:  helpers.StrPtr(blockHash),
			ParentHash: helpers.StrPtr(parentHash),
			Token:      nil,
			From:       helpers.StrPtr(r.normalizeAddress(from)),
			To:         helpers.StrPtr(r.normalizeAddress(to)),
			Amount:     helpers.StrPtr(value.String()),
			LogIndex:   helpers.StrPtr(traceIdentity),
			Status:     helpers.StrPtr(models.TransactionStatusConfirmed),
		}

		if err := r.dispatch(ctx, "internal_transfer", txParam); err != nil {
			return err
		}
	}

	return nil
}

func evmTraceRequired() bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("REQUIRE_EVM_TRACE")))
	if raw != "" {
		return raw == "1" || raw == "true" || raw == "yes" || raw == "on"
	}
	// Production defaults to fail-closed. Operators that intentionally do not
	// index internal value transfers can explicitly set REQUIRE_EVM_TRACE=false.
	return strings.EqualFold(strings.TrimSpace(os.Getenv("APP_ENV")), "production")
}

func internalTraceLogIndex(traceAddress []int) string {
	parts := make([]string, len(traceAddress))
	for i, value := range traceAddress {
		parts[i] = strconv.Itoa(value)
	}
	return "internal:" + strings.Join(parts, ".")
}

func traceAddressKey(traceAddress []int) string {
	parts := make([]string, len(traceAddress))
	for i, value := range traceAddress {
		parts[i] = strconv.Itoa(value)
	}
	return strings.Join(parts, ".")
}

func tracePathFailed(traceAddress []int, failed map[string]struct{}) bool {
	if len(failed) == 0 {
		return false
	}
	if _, rootFailed := failed[""]; rootFailed {
		return true
	}
	for length := 1; length <= len(traceAddress); length++ {
		if _, failedAncestor := failed[traceAddressKey(traceAddress[:length])]; failedAncestor {
			return true
		}
	}
	return false
}

func isTraceUnavailableError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "trace_block not allowed") ||
		strings.Contains(msg, "method trace_block not allowed") ||
		strings.Contains(msg, "method trace_block does not exist") ||
		strings.Contains(msg, "the method trace_block does not exist") ||
		strings.Contains(msg, "can't route your request to suitable provider") ||
		strings.Contains(msg, "method is not available") ||
		strings.Contains(msg, "method not found") ||
		strings.Contains(msg, "-32601")
}

func isBlockReceiptsUnavailableError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "eth_getblockreceipts") {
		return false
	}
	return strings.Contains(msg, "method eth_getblockreceipts not allowed") ||
		strings.Contains(msg, "method eth_getblockreceipts does not exist") ||
		strings.Contains(msg, "the method eth_getblockreceipts does not exist") ||
		strings.Contains(msg, "method is not available") ||
		strings.Contains(msg, "method not found") ||
		strings.Contains(msg, "not supported") ||
		strings.Contains(msg, "-32601")
}

func isBatchReceiptsUnavailableError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "batch") {
		return false
	}
	return strings.Contains(msg, "not supported") ||
		strings.Contains(msg, "unsupported") ||
		strings.Contains(msg, "not available") ||
		strings.Contains(msg, "method not found") ||
		strings.Contains(msg, "invalid request")
}

func (r *RpcListener) normalizeTokenAddress(address string) string {
	if r.chain.ChainID() == constants.TRON {
		return evmToTronAddress(address)
	}
	return strings.ToLower(address)
}

func (r *RpcListener) normalizeAddress(address string) string {
	if address == "" {
		return ""
	}
	if r.chain.ChainID() == constants.TRON {
		return evmToTronAddress(address)
	}
	return strings.ToLower(address)
}

func evmToTronAddress(evmHex string) string {
	evmHex = strings.TrimPrefix(evmHex, "0x")
	if len(evmHex) != 40 {
		return evmHex
	}

	tronHex := "41" + evmHex
	b, err := hex.DecodeString(tronHex)
	if err != nil {
		return evmHex
	}

	return addrutil.Base58CheckEncode(0x41, b[1:])
}

func receiptStatus(statusHex string) string {
	switch strings.ToLower(statusHex) {
	case "0x0":
		return "failed"
	case "0x1":
		return "confirmed"
	default:
		return ""
	}
}

func optionalHexBigString(hexValue string) *string {
	if hexValue == "" {
		return nil
	}
	value := hexToBig(hexValue).String()
	return helpers.StrPtr(value)
}

func hexToDec(hexStr string) string {
	if strings.HasPrefix(hexStr, "0x") {
		n, ok := new(big.Int).SetString(hexStr[2:], 16)
		if ok {
			return n.String()
		}
	}
	return hexStr
}

func hexToInt64(hexStr string) int64 {
	if strings.HasPrefix(hexStr, "0x") {
		n, ok := new(big.Int).SetString(hexStr[2:], 16)
		if ok {
			return n.Int64()
		}
	}
	n, ok := new(big.Int).SetString(hexStr, 10)
	if !ok {
		return 0
	}
	return n.Int64()
}

func hexToBig(hexStr string) *big.Int {
	value := big.NewInt(0)
	if hexStr == "" || hexStr == "0x" {
		return value
	}
	if strings.HasPrefix(hexStr, "0x") {
		if b, err := hexutil.Decode(hexStr); err == nil {
			value.SetBytes(b)
		}
		return value
	}
	if n, ok := new(big.Int).SetString(hexStr, 10); ok {
		return n
	}
	return value
}
