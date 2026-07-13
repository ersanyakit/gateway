package evm

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
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
	pollInterval             = 6 * time.Second
	maxBlocksPerPoll         = int64(5)
	recentMaxBlocksPerPoll   = int64(25)
	recentCatchUpLagBlocks   = int64(1000)
	recentCatchUpWindowBlock = int64(1000)
	safeBlockConfirmations   = int64(12)
	receiptBatchSize         = 50
	zeroAddress              = "0x0000000000000000000000000000000000000000"
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
	recentProcessedBlock     int64
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
	helpers.GoSafely("listener.evm."+r.chain.Name(), r.pollLoop)

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
	if err := r.catchUpRecent(ctx, safeLatest, confirmedHead); err != nil {
		return err
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

		listenerconfig.RecordProcessedBlockCheckpoint(r.chainState, blockNumber, r.lastBlockHash, r.lastBlockParentHash)
		r.chainState.LastConfirmedBlock = confirmedHead
		if r.stateWriter != nil {
			if err := r.stateWriter(r.chainState); err != nil {
				return fmt.Errorf("write chain state: %w", err)
			}
		}
	}

	return nil
}

func (r *RpcListener) catchUpRecent(ctx context.Context, safeLatest int64, confirmedHead int64) error {
	if r.chainState == nil {
		return nil
	}
	lag := safeLatest - r.chainState.LastProcessedBlock
	if lag < recentCatchUpLagBlocks {
		return nil
	}

	windowStart := safeLatest - recentCatchUpWindowBlock + 1
	if windowStart < 1 {
		windowStart = 1
	}

	from := r.recentProcessedBlock + 1
	tailStart := safeLatest - recentMaxBlocksPerPoll + 1
	if tailStart < windowStart {
		tailStart = windowStart
	}
	if r.recentProcessedBlock <= 0 || from < windowStart {
		from = tailStart
	}
	historicalNext := r.chainState.LastProcessedBlock + 1
	if from < historicalNext {
		from = historicalNext
	}
	if from > safeLatest {
		return nil
	}

	to := from + recentMaxBlocksPerPoll - 1
	if to > safeLatest {
		to = safeLatest
	}

	for blockNumber := from; blockNumber <= to; blockNumber++ {
		if err := r.processBlock(ctx, blockNumber); err != nil {
			return err
		}
		r.recentProcessedBlock = blockNumber
	}

	r.chainState.LastConfirmedBlock = confirmedHead
	if r.stateWriter != nil {
		if err := r.stateWriter(r.chainState); err != nil {
			return fmt.Errorf("write chain state: %w", err)
		}
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
	body, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      time.Now().UnixNano(),
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return err
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

		var rpcResp jsonRPCResponse
		if err := json.Unmarshal(respBody, &rpcResp); err != nil {
			lastErr = fmt.Errorf("%s %s response decode failed: %w", r.chain.Name(), rpcURL, err)
			r.recordRPCFailure(rpcURL, lastErr)
			continue
		}
		if rpcResp.Error != nil {
			err := fmt.Errorf("%s RPC %s error %d: %s", r.chain.Name(), method, rpcResp.Error.Code, rpcResp.Error.Message)
			if rpcutil.JSONRPCThrottled(rpcResp.Error.Code, rpcResp.Error.Message) {
				lastErr = rpcutil.NewThrottleError(err, 0)
				throttleErr = lastErr
			} else {
				lastErr = err
			}
			r.recordRPCFailure(rpcURL, lastErr)
			continue
		}
		if out == nil {
			r.recordRPCSuccess(rpcURL)
			return nil
		}
		if err := json.Unmarshal(rpcResp.Result, out); err != nil {
			lastErr = fmt.Errorf("%s %s response decode failed: %w", r.chain.Name(), rpcURL, err)
			r.recordRPCFailure(rpcURL, lastErr)
			continue
		}

		r.recordRPCSuccess(rpcURL)
		return nil
	}

	if throttleErr != nil {
		return throttleErr
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no RPC endpoint configured")
	}
	return lastErr
}

func (r *RpcListener) rpcBatchCall(ctx context.Context, requests []jsonRPCRequest) (map[int64]json.RawMessage, error) {
	if len(requests) == 0 {
		return map[int64]json.RawMessage{}, nil
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

func (r *RpcListener) processBlock(ctx context.Context, blockNumber int64) error {
	blockHex := fmt.Sprintf("0x%x", blockNumber)

	var block Block
	if err := r.rpcCall(ctx, "eth_getBlockByNumber", []interface{}{blockHex, true}, &block); err != nil {
		return err
	}
	if block.Number == "" {
		return fmt.Errorf("empty block result for %s", blockHex)
	}

	nativeAsset, ok := r.registry.GetNative(r.chain.ChainID())
	if !ok {
		return fmt.Errorf("native asset is not registered")
	}

	readableBlockNumber := hexToDec(block.Number)
	parsedBlockNumber := hexToInt64(block.Number)
	if err := listenerconfig.ValidateParentContinuity(r.chainState, parsedBlockNumber, block.ParentHash); err != nil {
		if r.observeCanonicalBlock != nil {
			if observeErr := r.observeCanonicalBlock(ctx, r.chain.ChainID(), parsedBlockNumber, block.Hash, block.ParentHash); observeErr != nil {
				return fmt.Errorf("observe canonical block after parent continuity failure: %w", observeErr)
			}
		}
		listenerconfig.RewindParentContinuityCheckpoint(r.chainState, parsedBlockNumber)
		if r.stateWriter != nil {
			if writeErr := r.stateWriter(r.chainState); writeErr != nil {
				return fmt.Errorf("write chain rollback state: %w", writeErr)
			}
		}
		return err
	}
	receiptsByHash, err := r.receiptsByBlock(ctx, blockHex)
	if err != nil {
		return err
	}
	if receiptsByHash == nil {
		receiptsByHash, err = r.receiptsByTransactions(ctx, block.Transactions)
		if err != nil {
			return err
		}
	}
	for idx, tx := range block.Transactions {
		if tx.Hash == "" {
			continue
		}

		receipt := Receipt{}
		if receiptsByHash != nil {
			receipt = receiptsByHash[strings.ToLower(tx.Hash)]
		}
		if receipt.TransactionHash == "" {
			receiptErr := r.rpcCall(ctx, "eth_getTransactionReceipt", []interface{}{tx.Hash}, &receipt)
			if receiptErr != nil {
				return fmt.Errorf("receipt fetch failed for %s: %w", tx.Hash, receiptErr)
			}
		}

		status := receiptStatus(receipt.Status)
		if status == "" {
			return fmt.Errorf("unsupported receipt status %q for transaction %s", receipt.Status, tx.Hash)
		}
		to := tx.To
		if to == "" {
			to = receipt.ContractAddress
		}
		if to == "" {
			to = zeroAddress
		}

		value := hexToBig(tx.Value)
		eventType := "transaction"
		if value.Sign() > 0 {
			eventType = "native_transfer"
		} else if isContractInput(tx.Input) || receipt.ContractAddress != "" {
			eventType = "contract_transaction"
		}

		txIndex := hexToDec(tx.TransactionIndex)
		if txIndex == "" || txIndex == "0x" {
			txIndex = fmt.Sprintf("%d", idx)
		}

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
				if err := r.handleTransferLog(ctx, entry, status, block.ParentHash); err != nil {
					return err
				}
			}
		}
	}

	if err := r.processInternalTransfers(ctx, blockHex, readableBlockNumber, block.Hash, block.ParentHash, nativeAsset); err != nil {
		return err
	}
	r.lastBlockNumber = blockNumber
	r.lastBlockHash = block.Hash
	r.lastBlockParentHash = block.ParentHash
	return nil
}

func (r *RpcListener) receiptsByBlock(ctx context.Context, blockHex string) (map[string]Receipt, error) {
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

	byHash := make(map[string]Receipt, len(receipts))
	for _, receipt := range receipts {
		if receipt.TransactionHash == "" {
			continue
		}
		byHash[strings.ToLower(receipt.TransactionHash)] = receipt
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
			if receiptHash == "" {
				receiptHash = idToHash[id]
				receipt.TransactionHash = receiptHash
			}
			if receiptHash != "" {
				byHash[receiptHash] = receipt
			}
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
	return len(l.Topics) >= 3 && strings.EqualFold(l.Topics[0], TransferEventHash)
}

func (r *RpcListener) handleTransferLog(ctx context.Context, l EVMLog, status string, parentHash string) error {
	token := common.HexToAddress(l.Address)
	from := common.BytesToAddress(common.HexToHash(l.Topics[1]).Bytes()[12:])
	to := common.BytesToAddress(common.HexToHash(l.Topics[2]).Bytes()[12:])

	value := big.NewInt(0)
	if l.Data != "" && l.Data != "0x" {
		if b, err := hexutil.Decode(l.Data); err == nil {
			value.SetBytes(b)
		}
	}
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

type Trace struct {
	Action struct {
		From  string `json:"from"`
		To    string `json:"to"`
		Value string `json:"value"`
	} `json:"action"`
	Result struct {
		Address string `json:"address"`
	} `json:"result"`
	Error           string `json:"error"`
	TransactionHash string `json:"transactionHash"`
}

func (r *RpcListener) processInternalTransfers(ctx context.Context, blockHex, blockNumber, blockHash, parentHash string, nativeAsset asset.Asset) error {
	requireTrace := strings.EqualFold(os.Getenv("REQUIRE_EVM_TRACE"), "true")
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
			log.Printf("[%s] trace_block failed; internal transfers skipped for block %s: %v\n", r.chain.Name(), blockNumber, err)
		}
		return nil
	}

	for idx, trace := range traces {
		if trace.TransactionHash == "" {
			continue
		}

		value := hexToBig(trace.Action.Value)
		if value.Sign() == 0 {
			continue
		}

		to := trace.Action.To
		if to == "" {
			to = trace.Result.Address
		}
		if to == "" {
			to = zeroAddress
		}

		status := "confirmed"
		if trace.Error != "" {
			status = "failed"
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
			From:       helpers.StrPtr(r.normalizeAddress(trace.Action.From)),
			To:         helpers.StrPtr(r.normalizeAddress(to)),
			Amount:     helpers.StrPtr(value.String()),
			LogIndex:   helpers.StrPtr(fmt.Sprintf("internal:%d", idx)),
			Status:     helpers.StrPtr(status),
		}

		if err := r.dispatch(ctx, "internal_transfer", txParam); err != nil {
			return err
		}
	}

	return nil
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
