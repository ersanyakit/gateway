package solana

import (
	"bytes"
	"context"
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
	"core/constants"
	"core/helpers"
	"core/models"
	"core/types"
	"core/workers/dispatcher"
	listenerconfig "core/workers/listeners"
	"core/workers/listeners/rpcutil"
)

const (
	pollInterval           = 10 * time.Second
	backlogPollInterval    = 250 * time.Millisecond
	defaultSlotsPerBatch   = int64(64)
	defaultSlotsPerCatchUp = int64(256)
	maxConfiguredSlots     = int64(4096)
	unknownProgram         = "unknown_program"
	unknownSigner          = "unknown_signer"

	solanaMemoProgram    = "MemoSq4gqABAXKb96qnH8TysNcWxMyWCqXgDLGmfcHr"
	solanaMemoProgramOld = "Memo1UhkJRfHyvLMcVucJwxXeuD728EqVDDwQDxFMNo"
)

type RpcListener struct {
	chain                  blockchain.Chain
	registry               *asset.Registry
	chainState             *models.ChainState
	stateWriter            func(*models.ChainState) error
	bus                    *dispatcher.Dispatcher
	canonicalBlockObserver func(context.Context, constants.ChainID, int64, string, string) error

	client          *http.Client
	endpointCircuit *rpcutil.EndpointCircuit

	mu      sync.Mutex
	quit    chan struct{}
	running bool
	events  chan interface{}

	lastRetryableWarning time.Time
	throttleErrors       int
	backlogRemaining     bool
	lastBlockSlot        int64
	lastBlockHash        string
	lastBlockParentHash  string
}

func (r *RpcListener) SetCanonicalBlockObserver(observer func(context.Context, constants.ChainID, int64, string, string) error) {
	r.canonicalBlockObserver = observer
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
		client:          &http.Client{Timeout: 30 * time.Second},
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
	helpers.GoSafelyRestarting("listener.solana."+r.chain.Name(), r.quit, time.Second, r.pollLoop)
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
			if r.backlogRemaining {
				delay = backlogPollInterval
			}
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
	r.backlogRemaining = false

	latest, err := r.latestSlot(ctx)
	if err != nil {
		return err
	}
	if latest <= 0 {
		return nil
	}

	decision, err := listenerconfig.ResolveStartBlock(r.chain, r.chainState.LastProcessedBlock, latest)
	if err != nil {
		return err
	}
	if decision.Warning != "" {
		log.Printf("[%s] scanner start policy: %s\n", r.chain.Name(), decision.Warning)
	}
	listenerconfig.ApplyStartBlockDecision(r.chainState, decision)
	from := decision.From
	if from > latest {
		return nil
	}
	r.backlogRemaining = true

	catchUpEnd := from + solanaSlotsPerCatchUp() - 1
	if catchUpEnd > latest {
		catchUpEnd = latest
	}
	batchSize := solanaSlotsPerBatch()

	for batchFrom := from; batchFrom <= catchUpEnd; {
		batchTo := batchFrom + batchSize - 1
		if batchTo > catchUpEnd {
			batchTo = catchUpEnd
		}

		slots, err := r.blocksInRange(ctx, batchFrom, batchTo)
		if err != nil {
			return err
		}

		for _, slot := range slots {
			if err := r.processSlot(ctx, slot); err != nil {
				return err
			}
			if err := r.writeChainState(slot); err != nil {
				return err
			}
		}

		// blocksInRange verifies every omitted slot as explicitly skipped before
		// this checkpoint can cross it. An incomplete or unavailable response
		// therefore holds the cursor instead of silently losing transactions.
		if r.chainState.LastProcessedBlock < batchTo {
			if err := r.writeChainState(batchTo); err != nil {
				return err
			}
		}
		batchFrom = batchTo + 1
	}

	r.backlogRemaining = r.chainState.LastProcessedBlock < latest
	return nil
}

func solanaSlotsPerBatch() int64 {
	return positiveBoundedEnv("SOLANA_SCAN_BATCH_SLOTS", defaultSlotsPerBatch, maxConfiguredSlots)
}

func solanaSlotsPerCatchUp() int64 {
	configured := positiveBoundedEnv("SOLANA_MAX_SLOTS_PER_CATCH_UP", defaultSlotsPerCatchUp, maxConfiguredSlots)
	if batch := solanaSlotsPerBatch(); configured < batch {
		return batch
	}
	return configured
}

func positiveBoundedEnv(key string, fallback, maximum int64) int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 || value > maximum {
		return fallback
	}
	return value
}

func (r *RpcListener) writeChainState(block int64) error {
	if r.chainState == nil {
		return fmt.Errorf("solana chain state is nil")
	}
	next := *r.chainState
	checkpointHash := next.LastProcessedHash
	checkpointParentHash := next.LastProcessedParentHash
	if r.lastBlockSlot > 0 && r.lastBlockSlot <= block && strings.TrimSpace(r.lastBlockHash) != "" {
		checkpointHash = r.lastBlockHash
		checkpointParentHash = r.lastBlockParentHash
	}
	// A skipped slot advances the slot cursor but has no block identity of its
	// own. Retain the most recent real block hash so the next produced block can
	// still prove previousBlockhash continuity across the gap.
	listenerconfig.RecordProcessedBlockCheckpoint(&next, block, checkpointHash, checkpointParentHash)
	next.LastConfirmedBlock = block
	if r.stateWriter != nil {
		if err := r.stateWriter(&next); err != nil {
			return fmt.Errorf("write chain state: %w", err)
		}
	}
	*r.chainState = next
	return nil
}

func (r *RpcListener) latestSlot(ctx context.Context) (int64, error) {
	var slot int64
	if err := r.rpcCall(ctx, "getSlot", []interface{}{map[string]interface{}{"commitment": "finalized"}}, &slot); err != nil {
		return 0, err
	}
	if slot <= 0 {
		return 0, fmt.Errorf("%s RPC getSlot returned invalid finalized slot %d", r.chain.Name(), slot)
	}
	return slot, nil
}

func (r *RpcListener) blocksInRange(ctx context.Context, from, to int64) ([]int64, error) {
	if from <= 0 || to < from {
		return nil, fmt.Errorf("invalid solana slot range [%d,%d]", from, to)
	}

	var slots []int64
	if err := r.rpcCall(ctx, "getBlocks", []interface{}{
		from,
		to,
		map[string]interface{}{"commitment": "finalized"},
	}, &slots); err != nil {
		return nil, err
	}
	previous := int64(-1)
	for index, slot := range slots {
		if slot < from || slot > to {
			return nil, fmt.Errorf("%s RPC getBlocks returned out-of-range slot %d for [%d,%d]", r.chain.Name(), slot, from, to)
		}
		if index > 0 && slot <= previous {
			return nil, fmt.Errorf("%s RPC getBlocks returned non-increasing slot sequence at %d after %d", r.chain.Name(), slot, previous)
		}
		previous = slot
	}

	// getBlocks legitimately omits skipped slots, but a truncated/empty RPC
	// response is indistinguishable from that at the list level. Probe every
	// omission before allowing the durable checkpoint to cross it.
	complete := make([]int64, 0, len(slots))
	listedIndex := 0
	for slot := from; slot <= to; slot++ {
		if listedIndex < len(slots) && slots[listedIndex] == slot {
			complete = append(complete, slot)
			listedIndex++
			continue
		}

		hasBlock, err := r.slotHasBlock(ctx, slot)
		if err != nil {
			return nil, fmt.Errorf("verify omitted solana slot %d: %w", slot, err)
		}
		if hasBlock {
			complete = append(complete, slot)
		}
	}
	return complete, nil
}

func (r *RpcListener) slotHasBlock(ctx context.Context, slot int64) (bool, error) {
	var header *struct {
		Blockhash string `json:"blockhash"`
	}
	err := r.rpcCall(ctx, "getBlock", []interface{}{
		slot,
		map[string]interface{}{
			"commitment":         "finalized",
			"transactionDetails": "none",
			"rewards":            false,
		},
	}, &header)
	if err != nil {
		if isExplicitlySkippedSlot(err) {
			return false, nil
		}
		return false, err
	}
	if header == nil || strings.TrimSpace(header.Blockhash) == "" {
		return false, fmt.Errorf("getBlock returned an invalid header")
	}
	return true, nil
}

type jsonRPCResponse struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *jsonRPCError   `json:"error"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type jsonRPCRemoteError struct {
	chainName string
	method    string
	code      int
	message   string
}

func (e *jsonRPCRemoteError) Error() string {
	return fmt.Sprintf("%s RPC %s error %d: %s", e.chainName, e.method, e.code, e.message)
}

func isExplicitlySkippedSlot(err error) bool {
	var remoteErr *jsonRPCRemoteError
	return errors.As(err, &remoteErr) &&
		remoteErr.code == -32007 &&
		strings.Contains(strings.ToLower(remoteErr.message), "skip")
}

func (r *RpcListener) rpcCall(ctx context.Context, method string, params []interface{}, out interface{}) error {
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
	var skippedSlotErr error
	for _, rpcURL := range r.rpcURLs() {
		endpointCtx, cancel := rpcutil.WithEndpointTimeout(ctx)
		req, err := http.NewRequestWithContext(endpointCtx, http.MethodPost, rpcURL, bytes.NewReader(body))
		if err != nil {
			cancel()
			lastErr = err
			continue
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := r.client.Do(req)
		if err != nil {
			cancel()
			lastErr = err
			r.recordRPCFailure(rpcURL, lastErr)
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		cancel()
		if readErr != nil {
			lastErr = readErr
			r.recordRPCFailure(rpcURL, lastErr)
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			err := fmt.Errorf("%s RPC returned HTTP %d: %s", r.chain.Name(), resp.StatusCode, string(respBody))
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
		if rpcResp.ID != requestID {
			lastErr = fmt.Errorf("%s %s RPC %s response id mismatch: got %d want %d", r.chain.Name(), rpcURL, method, rpcResp.ID, requestID)
			r.recordRPCFailure(rpcURL, lastErr)
			continue
		}
		if rpcResp.Error != nil {
			remoteErr := &jsonRPCRemoteError{
				chainName: r.chain.Name(),
				method:    method,
				code:      rpcResp.Error.Code,
				message:   rpcResp.Error.Message,
			}
			if method == "getBlock" && isExplicitlySkippedSlot(remoteErr) {
				skippedSlotErr = remoteErr
				continue
			}
			var err error = remoteErr
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
		if len(bytes.TrimSpace(rpcResp.Result)) == 0 || bytes.Equal(bytes.TrimSpace(rpcResp.Result), []byte("null")) {
			lastErr = fmt.Errorf("%s RPC %s returned unavailable null result", r.chain.Name(), method)
			r.recordRPCFailure(rpcURL, lastErr)
			continue
		}
		resetRPCOutput(out)
		if err := json.Unmarshal(rpcResp.Result, out); err != nil {
			lastErr = fmt.Errorf("%s %s RPC %s result decode failed: %w", r.chain.Name(), rpcURL, method, err)
			r.recordRPCFailure(rpcURL, lastErr)
			continue
		}

		r.recordRPCSuccess(rpcURL)
		return nil
	}

	if throttleErr != nil {
		return throttleErr
	}
	if lastErr == nil && skippedSlotErr != nil {
		return skippedSlotErr
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no solana RPC endpoint configured")
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
	Blockhash         string    `json:"blockhash"`
	PreviousBlockhash string    `json:"previousBlockhash"`
	BlockHeight       *int64    `json:"blockHeight"`
	Transactions      []BlockTx `json:"transactions"`
}

type BlockTx struct {
	Meta        *TxMeta     `json:"meta"`
	Transaction Transaction `json:"transaction"`
}

type TxMeta struct {
	Err               json.RawMessage     `json:"err"`
	InnerInstructions []InnerInstructions `json:"innerInstructions"`
	PreBalances       []uint64            `json:"preBalances"`
	PostBalances      []uint64            `json:"postBalances"`
	PreTokenBalances  []TokenBalance      `json:"preTokenBalances"`
	PostTokenBalances []TokenBalance      `json:"postTokenBalances"`
	LoadedAddresses   LoadedAddresses     `json:"loadedAddresses"`
}

type TokenBalance struct {
	AccountIndex  uint16         `json:"accountIndex"`
	Mint          string         `json:"mint"`
	Owner         string         `json:"owner"`
	ProgramID     string         `json:"programId"`
	UITokenAmount *UITokenAmount `json:"uiTokenAmount"`
}

type UITokenAmount struct {
	Amount   string `json:"amount"`
	Decimals uint8  `json:"decimals"`
}

type LoadedAddresses struct {
	Writable []string `json:"writable"`
	Readonly []string `json:"readonly"`
}

type tokenAccountMetadata struct {
	Owner       string
	Mint        string
	ProgramID   string
	Decimals    uint8
	HasDecimals bool
}

type InnerInstructions struct {
	Index        int           `json:"index"`
	Instructions []Instruction `json:"instructions"`
}

type Transaction struct {
	Signatures []string `json:"signatures"`
	Message    Message  `json:"message"`
}

type Message struct {
	AccountKeys  []json.RawMessage `json:"accountKeys"`
	Instructions []Instruction     `json:"instructions"`
}

type Instruction struct {
	Program   string          `json:"program"`
	ProgramID string          `json:"programId"`
	Parsed    json.RawMessage `json:"parsed"`
	Accounts  []string        `json:"accounts"`
}

type AccountKey struct {
	Pubkey string
	Signer bool
	Source string
}

func (r *RpcListener) processSlot(ctx context.Context, slot int64) error {
	var block *Block
	err := r.rpcCall(ctx, "getBlock", []interface{}{
		slot,
		map[string]interface{}{
			"encoding":                       "jsonParsed",
			"transactionDetails":             "full",
			"rewards":                        false,
			"maxSupportedTransactionVersion": 0,
		},
	}, &block)
	if err != nil {
		return err
	}
	if block == nil {
		return fmt.Errorf("empty block result for solana slot %d", slot)
	}
	if strings.TrimSpace(block.Blockhash) == "" {
		return fmt.Errorf("solana slot %d returned an empty block hash", slot)
	}
	if slot > 0 && strings.TrimSpace(block.PreviousBlockhash) == "" {
		return fmt.Errorf("solana slot %d returned an empty previous block hash", slot)
	}
	if r.chainState != nil {
		continuityState := *r.chainState
		if continuityErr := validateSolanaParentContinuity(&continuityState, slot, block.PreviousBlockhash); continuityErr != nil {
			if r.canonicalBlockObserver != nil {
				if observeErr := r.canonicalBlockObserver(ctx, r.chain.ChainID(), slot, block.Blockhash, block.PreviousBlockhash); observeErr != nil {
					return fmt.Errorf("observe solana canonical slot after parent continuity failure: %w", observeErr)
				}
			}
			listenerconfig.RewindParentContinuityCheckpoint(&continuityState, slot)
			if r.stateWriter != nil {
				if writeErr := r.stateWriter(&continuityState); writeErr != nil {
					return fmt.Errorf("write solana chain rollback state: %w", writeErr)
				}
			}
			*r.chainState = continuityState
			r.lastBlockSlot = 0
			r.lastBlockHash = ""
			r.lastBlockParentHash = ""
			return continuityErr
		}
	}
	if r.canonicalBlockObserver != nil {
		if err := r.canonicalBlockObserver(ctx, r.chain.ChainID(), slot, block.Blockhash, block.PreviousBlockhash); err != nil {
			return fmt.Errorf("observe solana canonical slot %d: %w", slot, err)
		}
	}

	nativeAsset, ok := r.registry.GetNative(r.chain.ChainID())
	if !ok {
		return fmt.Errorf("native asset is not registered")
	}

	// Chain state is slot based, so durable transaction facts must use the same
	// unit. blockHeight is not interchangeable with slot because skipped slots
	// make the two counters diverge.
	blockNumber := fmt.Sprintf("%d", slot)

	for txIndex, tx := range block.Transactions {
		if tx.Meta == nil {
			return fmt.Errorf("solana slot %d transaction %d returned null metadata", slot, txIndex)
		}
		if len(bytes.TrimSpace(tx.Meta.Err)) == 0 {
			return fmt.Errorf("solana slot %d transaction %d metadata is missing execution status", slot, txIndex)
		}
		if len(tx.Transaction.Signatures) == 0 || strings.TrimSpace(tx.Transaction.Signatures[0]) == "" {
			return fmt.Errorf("solana slot %d transaction %d returned no signature", slot, txIndex)
		}
		if err := r.handleTransaction(blockNumber, block.Blockhash, txIndex, nativeAsset, tx); err != nil {
			return err
		}
	}
	r.lastBlockSlot = slot
	r.lastBlockHash = strings.TrimSpace(block.Blockhash)
	r.lastBlockParentHash = strings.TrimSpace(block.PreviousBlockhash)

	return nil
}

func validateSolanaParentContinuity(state *models.ChainState, nextSlot int64, nextParentHash string) error {
	if state == nil || state.LastProcessedBlock <= 0 {
		return nil
	}
	checkpointHash := strings.TrimSpace(state.LastProcessedHash)
	nextParentHash = strings.TrimSpace(nextParentHash)
	if checkpointHash == "" || nextParentHash == "" {
		return nil
	}
	// Solana blockhashes are base58 identifiers and therefore case-sensitive.
	// previousBlockhash points to the last produced block even when one or more
	// intervening slots were skipped, so slot adjacency is intentionally not
	// required here.
	if checkpointHash != nextParentHash {
		state.ContinuityStatus = listenerconfig.ContinuityStatusRollback
		state.ContinuityReason = fmt.Sprintf("slot %d parent %s does not match checkpoint %s", nextSlot, nextParentHash, checkpointHash)
		return fmt.Errorf("%w: %s", listenerconfig.ErrParentContinuity, state.ContinuityReason)
	}
	if state.ContinuityStatus != listenerconfig.ContinuityStatusHistoryTail {
		state.ContinuityStatus = listenerconfig.ContinuityStatusOK
		state.ContinuityReason = ""
	}
	return nil
}

func (r *RpcListener) handleTransaction(blockNumber, blockHash string, txIndex int, nativeAsset asset.Asset, tx BlockTx) error {
	hash := ""
	if len(tx.Transaction.Signatures) > 0 {
		hash = tx.Transaction.Signatures[0]
	}
	if hash == "" {
		return nil
	}

	status := "confirmed"
	if tx.Meta == nil {
		return fmt.Errorf("solana transaction %s has no metadata", hash)
	}
	executionStatus := bytes.TrimSpace(tx.Meta.Err)
	if len(executionStatus) == 0 || !json.Valid(executionStatus) {
		return fmt.Errorf("solana transaction %s has invalid execution status", hash)
	}
	if !bytes.Equal(executionStatus, []byte("null")) {
		status = "failed"
	}

	if tx.Meta.PreBalances == nil || tx.Meta.PostBalances == nil {
		return fmt.Errorf("solana transaction %s metadata is missing preBalances or postBalances", hash)
	}
	if len(tx.Meta.PreBalances) != len(tx.Meta.PostBalances) {
		return fmt.Errorf(
			"solana transaction %s balance vector length mismatch: pre=%d post=%d",
			hash,
			len(tx.Meta.PreBalances),
			len(tx.Meta.PostBalances),
		)
	}
	accountKeys, err := transactionAccountKeys(
		tx.Transaction.Message.AccountKeys,
		tx.Meta.LoadedAddresses,
		len(tx.Meta.PreBalances),
	)
	if err != nil {
		return fmt.Errorf("solana transaction %s account keys: %w", hash, err)
	}
	tokenAccounts, tokenBalanceWarnings := tokenAccountMetadataByAddress(tx.Transaction.Message.AccountKeys, *tx.Meta)
	if len(tokenBalanceWarnings) > 0 {
		log.Printf("[%s] invalid token balance metadata tx=%s: %s\n", r.chain.Name(), hash, strings.Join(tokenBalanceWarnings, "; "))
	}
	signer := firstSigner(accountKeys)
	if signer == "" && len(accountKeys) > 0 {
		signer = accountKeys[0].Pubkey
	}
	if signer == "" {
		signer = unknownSigner
	}

	memo := solanaTransactionMemo(tx.Transaction.Message.Instructions, tx.Meta.InnerInstructions)
	if err := r.dispatchNativeBalanceIncreases(
		blockNumber,
		blockHash,
		hash,
		nativeAsset,
		status,
		signer,
		memo,
		accountKeys,
		tx.Meta.PreBalances,
		tx.Meta.PostBalances,
	); err != nil {
		return err
	}
	for ixIndex, instruction := range tx.Transaction.Message.Instructions {
		if err := r.handleInstruction(blockNumber, blockHash, hash, fmt.Sprintf("ix:%d", ixIndex), nativeAsset, status, signer, memo, instruction, tokenAccounts); err != nil {
			return err
		}
	}

	for _, group := range tx.Meta.InnerInstructions {
		for innerIndex, instruction := range group.Instructions {
			logIndex := fmt.Sprintf("inner:%d:%d", group.Index, innerIndex)
			if err := r.handleInstruction(blockNumber, blockHash, hash, logIndex, nativeAsset, status, signer, memo, instruction, tokenAccounts); err != nil {
				return err
			}
		}
	}

	return nil
}

// dispatchNativeBalanceIncreases derives native SOL receipts from the
// authoritative transaction account state transition. Parsed system-program
// instructions are not a complete source of truth (CPI and transferWithSeed
// are common counterexamples) and emitting both views would double-credit a
// recipient. Account position is part of the signed transaction message, so
// balance:<index> remains deterministic across RPC retries and replays.
func (r *RpcListener) dispatchNativeBalanceIncreases(
	blockNumber string,
	blockHash string,
	hash string,
	nativeAsset asset.Asset,
	status string,
	signer string,
	memo string,
	accountKeys []AccountKey,
	preBalances []uint64,
	postBalances []uint64,
) error {
	if len(preBalances) != len(postBalances) || len(accountKeys) != len(preBalances) {
		return fmt.Errorf(
			"solana transaction %s native balance shape mismatch: accounts=%d pre=%d post=%d",
			hash,
			len(accountKeys),
			len(preBalances),
			len(postBalances),
		)
	}
	// A failed Solana transaction rolls back instruction state changes. Its fee
	// can reduce the payer balance, but it cannot be a merchant receipt.
	if status != "confirmed" {
		return nil
	}
	debitAddresses := make([]string, 0)
	for index, postBalance := range postBalances {
		if postBalance < preBalances[index] {
			debitAddresses = append(debitAddresses, accountKeys[index].Pubkey)
		}
	}

	for index, postBalance := range postBalances {
		preBalance := preBalances[index]
		if postBalance <= preBalance {
			continue
		}
		amount := strconv.FormatUint(postBalance-preBalance, 10)
		destination := accountKeys[index].Pubkey
		txParam := &types.TransactionParam{
			Context:       context.Background(),
			ChainID:       r.chain.ChainID(),
			Symbol:        helpers.StrPtr(nativeAsset.GetSymbol()),
			Decimals:      nativeAsset.GetDecimals(),
			Hash:          helpers.StrPtr(hash),
			Block:         helpers.StrPtr(blockNumber),
			BlockHash:     helpers.StrPtr(blockHash),
			Token:         nil,
			From:          helpers.StrPtr(signer),
			FromAddresses: append([]string(nil), debitAddresses...),
			To:            helpers.StrPtr(destination),
			Amount:        helpers.StrPtr(amount),
			LogIndex:      helpers.StrPtr(fmt.Sprintf("balance:%d", index)),
			Status:        helpers.StrPtr(status),
			Memo:          optionalMemoPtr(memo),
		}
		if err := r.dispatch("sol_transfer", txParam); err != nil {
			return err
		}
	}
	return nil
}

func (r *RpcListener) handleInstruction(
	blockNumber string,
	blockHash string,
	hash string,
	logIndex string,
	nativeAsset asset.Asset,
	status string,
	signer string,
	memo string,
	instruction Instruction,
	tokenAccounts map[string]tokenAccountMetadata,
) error {
	handled, err := r.handleParsedTransfer(blockNumber, blockHash, hash, logIndex, nativeAsset, status, memo, instruction, tokenAccounts)
	if err != nil {
		return err
	}
	if handled {
		return nil
	}

	programID := instruction.ProgramID
	if programID == "" {
		programID = instruction.Program
	}
	if programID == "" {
		programID = unknownProgram
	}

	txParam := &types.TransactionParam{
		Context:   context.Background(),
		ChainID:   r.chain.ChainID(),
		Symbol:    helpers.StrPtr(nativeAsset.GetSymbol()),
		Decimals:  nativeAsset.GetDecimals(),
		Hash:      helpers.StrPtr(hash),
		Block:     helpers.StrPtr(blockNumber),
		BlockHash: helpers.StrPtr(blockHash),
		Token:     nil,
		From:      helpers.StrPtr(signer),
		To:        helpers.StrPtr(programID),
		Amount:    helpers.StrPtr("0"),
		LogIndex:  helpers.StrPtr(logIndex),
		Status:    helpers.StrPtr(status),
		Memo:      optionalMemoPtr(memo),
	}
	return r.dispatch("program_call", txParam)
}

type ParsedInstruction struct {
	Type string                     `json:"type"`
	Info map[string]json.RawMessage `json:"info"`
}

func (r *RpcListener) handleParsedTransfer(
	blockNumber string,
	blockHash string,
	hash string,
	logIndex string,
	nativeAsset asset.Asset,
	status string,
	memo string,
	instruction Instruction,
	tokenAccounts map[string]tokenAccountMetadata,
) (bool, error) {
	parsedJSON := bytes.TrimSpace(instruction.Parsed)
	if len(parsedJSON) == 0 || bytes.Equal(parsedJSON, []byte("null")) {
		return false, nil
	}
	// Parsed memo and other non-transfer instructions may legitimately be JSON
	// scalars. They are not malformed transfer objects and must not stop slot
	// processing.
	if parsedJSON[0] != '{' {
		return false, nil
	}

	var parsed ParsedInstruction
	if err := json.Unmarshal(parsedJSON, &parsed); err != nil {
		return false, fmt.Errorf("decode parsed Solana instruction: %w", err)
	}

	instructionType := strings.ToLower(parsed.Type)
	program := strings.ToLower(instruction.Program)
	switch {
	case program == "system" && (instructionType == "transfer" || instructionType == "transferwithseed"):
		source := rawString(parsed.Info, "source")
		destination := rawString(parsed.Info, "destination")
		lamports := rawString(parsed.Info, "lamports")
		if source == "" || destination == "" || lamports == "" {
			return true, fmt.Errorf("solana system transfer %s is missing source, destination, or lamports", logIndex)
		}
		lamportAmount, ok := new(big.Int).SetString(strings.TrimSpace(lamports), 10)
		if !ok || lamportAmount.Sign() < 0 {
			return true, fmt.Errorf("solana system transfer %s has invalid lamports %q", logIndex, lamports)
		}
		if lamportAmount.Sign() == 0 {
			return true, nil
		}

		// Native SOL is emitted once from the transaction balance vector in
		// handleTransaction. This parsed view is validation-only.
		return true, nil

	case strings.HasPrefix(program, "spl-token") && (instructionType == "transfer" || instructionType == "transferchecked"):
		source := rawString(parsed.Info, "source")
		destination := rawString(parsed.Info, "destination")
		mint := rawString(parsed.Info, "mint")
		amount := rawString(parsed.Info, "amount")

		if tokenAmountRaw, ok := parsed.Info["tokenAmount"]; ok {
			var tokenAmount map[string]json.RawMessage
			if err := json.Unmarshal(tokenAmountRaw, &tokenAmount); err != nil {
				return true, fmt.Errorf("solana token transfer %s has malformed tokenAmount: %w", logIndex, err)
			}
			if amount == "" {
				amount = rawString(tokenAmount, "amount")
			}
		}
		if source == "" || destination == "" || amount == "" {
			return true, fmt.Errorf("solana token transfer %s is missing source, destination, or amount", logIndex)
		}
		tokenAmount, ok := new(big.Int).SetString(strings.TrimSpace(amount), 10)
		if !ok || tokenAmount.Sign() < 0 {
			return true, fmt.Errorf("solana token transfer %s has invalid amount %q", logIndex, amount)
		}
		if tokenAmount.Sign() == 0 {
			return true, nil
		}

		sourceMetadata, sourceOK := tokenAccounts[source]
		destinationMetadata, destinationOK := tokenAccounts[destination]
		if !sourceOK || !destinationOK ||
			sourceMetadata.Owner == "" || destinationMetadata.Owner == "" ||
			sourceMetadata.Mint == "" || destinationMetadata.Mint == "" ||
			sourceMetadata.Mint != destinationMetadata.Mint ||
			!sourceMetadata.HasDecimals || !destinationMetadata.HasDecimals ||
			sourceMetadata.Decimals != destinationMetadata.Decimals {
			return true, fmt.Errorf("solana token transfer %s has incomplete/conflicting token account metadata", logIndex)
		}

		if mint == "" {
			mint = destinationMetadata.Mint
		}
		if mint != destinationMetadata.Mint {
			return true, fmt.Errorf("solana token transfer %s mint does not match account metadata", logIndex)
		}
		if sourceMetadata.ProgramID != "" && destinationMetadata.ProgramID != "" && sourceMetadata.ProgramID != destinationMetadata.ProgramID {
			return true, fmt.Errorf("solana token transfer %s account program ids do not match", logIndex)
		}
		instructionProgramID := strings.TrimSpace(instruction.ProgramID)
		if instructionProgramID != "" {
			if sourceMetadata.ProgramID != "" && sourceMetadata.ProgramID != instructionProgramID {
				return true, fmt.Errorf("solana token transfer %s source program does not match instruction", logIndex)
			}
			if destinationMetadata.ProgramID != "" && destinationMetadata.ProgramID != instructionProgramID {
				return true, fmt.Errorf("solana token transfer %s destination program does not match instruction", logIndex)
			}
		}
		if r.registry == nil {
			return true, errors.New("solana asset registry is not configured")
		}
		assetInfo, ok := r.registry.Get(r.chain.ChainID(), mint)
		if !ok {
			return true, nil
		}
		if assetInfo.GetDecimals() != destinationMetadata.Decimals {
			return true, fmt.Errorf("solana token transfer %s decimals do not match registered asset", logIndex)
		}

		txParam := &types.TransactionParam{
			Context:   context.Background(),
			ChainID:   r.chain.ChainID(),
			Symbol:    helpers.StrPtr(assetInfo.GetSymbol()),
			Decimals:  assetInfo.GetDecimals(),
			Hash:      helpers.StrPtr(hash),
			Block:     helpers.StrPtr(blockNumber),
			BlockHash: helpers.StrPtr(blockHash),
			Token:     helpers.StrPtr(mint),
			From:      helpers.StrPtr(sourceMetadata.Owner),
			To:        helpers.StrPtr(destinationMetadata.Owner),
			Amount:    helpers.StrPtr(amount),
			LogIndex:  helpers.StrPtr(logIndex),
			Status:    helpers.StrPtr(status),
			Memo:      optionalMemoPtr(memo),
		}
		return true, r.dispatch("spl_transfer", txParam)
	}

	return false, nil
}

func positiveRawAmount(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	amount, ok := new(big.Int).SetString(value, 10)
	return ok && amount.Sign() > 0
}

func solanaTransactionMemo(instructions []Instruction, innerGroups []InnerInstructions) string {
	for _, instruction := range instructions {
		if memo := solanaInstructionMemo(instruction); memo != "" {
			return memo
		}
	}
	for _, group := range innerGroups {
		for _, instruction := range group.Instructions {
			if memo := solanaInstructionMemo(instruction); memo != "" {
				return memo
			}
		}
	}
	return ""
}

func solanaInstructionMemo(instruction Instruction) string {
	if !isSolanaMemoInstruction(instruction) {
		return ""
	}
	return solanaParsedMemo(instruction.Parsed)
}

func isSolanaMemoInstruction(instruction Instruction) bool {
	programID := strings.TrimSpace(instruction.ProgramID)
	if programID == solanaMemoProgram || programID == solanaMemoProgramOld {
		return true
	}
	lowerProgram := strings.ToLower(strings.TrimSpace(instruction.Program))
	lowerProgramID := strings.ToLower(programID)
	return strings.Contains(lowerProgram, "memo") || strings.Contains(lowerProgramID, "memo")
}

func solanaParsedMemo(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}

	var parsed ParsedInstruction
	if err := json.Unmarshal(raw, &parsed); err == nil && parsed.Info != nil {
		if memo := firstRawString(parsed.Info, "memo", "data", "text"); memo != "" {
			return memo
		}
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return ""
	}
	if memo := firstRawString(object, "memo", "data", "text"); memo != "" {
		return memo
	}
	if infoRaw, ok := object["info"]; ok {
		var info map[string]json.RawMessage
		if err := json.Unmarshal(infoRaw, &info); err == nil {
			return firstRawString(info, "memo", "data", "text")
		}
	}
	return ""
}

func firstRawString(info map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(rawString(info, key)); value != "" {
			return value
		}
	}
	return ""
}

func optionalMemoPtr(memo string) *string {
	memo = strings.TrimSpace(memo)
	if memo == "" {
		return nil
	}
	return helpers.StrPtr(memo)
}

func transactionAccountKeys(rawKeys []json.RawMessage, loaded LoadedAddresses, expectedCount int) ([]AccountKey, error) {
	if rawKeys == nil {
		return nil, errors.New("message is missing accountKeys")
	}
	if expectedCount <= 0 {
		return nil, fmt.Errorf("invalid balance account count %d", expectedCount)
	}

	keys := make([]AccountKey, 0, len(rawKeys)+len(loaded.Writable)+len(loaded.Readonly))
	seen := make(map[string]struct{}, cap(keys))
	for index, rawKey := range rawKeys {
		key, err := parseTransactionAccountKey(rawKey)
		if err != nil {
			return nil, fmt.Errorf("accountKeys[%d]: %w", index, err)
		}
		if _, duplicate := seen[key.Pubkey]; duplicate {
			return nil, fmt.Errorf("accountKeys[%d] duplicates pubkey %s", index, key.Pubkey)
		}
		seen[key.Pubkey] = struct{}{}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil, errors.New("message contains no account keys")
	}

	loadedKeys := make([]AccountKey, 0, len(loaded.Writable)+len(loaded.Readonly))
	appendLoaded := func(address string, writable bool, index int) error {
		address = strings.TrimSpace(address)
		if address == "" {
			kind := "readonly"
			if writable {
				kind = "writable"
			}
			return fmt.Errorf("loadedAddresses.%s[%d] is empty", kind, index)
		}
		loadedKeys = append(loadedKeys, AccountKey{Pubkey: address, Source: "lookupTable"})
		return nil
	}
	for index, address := range loaded.Writable {
		if err := appendLoaded(address, true, index); err != nil {
			return nil, err
		}
	}
	for index, address := range loaded.Readonly {
		if err := appendLoaded(address, false, index); err != nil {
			return nil, err
		}
	}

	switch {
	case expectedCount == len(keys):
		// With jsonParsed encoding, lookup-table keys are normally already in
		// message.accountKeys. Some providers also redundantly return
		// meta.loadedAddresses; when they do, it must match the account-key tail.
		if len(loadedKeys) > len(keys) {
			return nil, fmt.Errorf(
				"loaded address count %d exceeds complete account key count %d",
				len(loadedKeys),
				len(keys),
			)
		}
		tailStart := len(keys) - len(loadedKeys)
		for index, loadedKey := range loadedKeys {
			if keys[tailStart+index].Pubkey != loadedKey.Pubkey {
				return nil, fmt.Errorf(
					"loaded address %d (%s) does not match accountKeys[%d] (%s)",
					index,
					loadedKey.Pubkey,
					tailStart+index,
					keys[tailStart+index].Pubkey,
				)
			}
		}
		return keys, nil

	case expectedCount == len(keys)+len(loadedKeys):
		// With raw message encoding, only static keys are in the message and
		// lookup-table keys follow as writable then readonly addresses.
		for index, loadedKey := range loadedKeys {
			if _, duplicate := seen[loadedKey.Pubkey]; duplicate {
				return nil, fmt.Errorf("loaded address %d duplicates pubkey %s", index, loadedKey.Pubkey)
			}
			seen[loadedKey.Pubkey] = struct{}{}
			keys = append(keys, loadedKey)
		}
		return keys, nil

	default:
		return nil, fmt.Errorf(
			"account/balance length mismatch: static_or_parsed=%d loaded=%d balances=%d",
			len(keys),
			len(loadedKeys),
			expectedCount,
		)
	}
}

func parseTransactionAccountKey(rawKey json.RawMessage) (AccountKey, error) {
	if len(bytes.TrimSpace(rawKey)) == 0 || bytes.Equal(bytes.TrimSpace(rawKey), []byte("null")) {
		return AccountKey{}, errors.New("account key is empty")
	}

	var keyString string
	if err := json.Unmarshal(rawKey, &keyString); err == nil {
		keyString = strings.TrimSpace(keyString)
		if keyString == "" {
			return AccountKey{}, errors.New("account pubkey is empty")
		}
		return AccountKey{Pubkey: keyString}, nil
	}

	var keyObject struct {
		Pubkey string `json:"pubkey"`
		Signer bool   `json:"signer"`
		Source string `json:"source"`
	}
	if err := json.Unmarshal(rawKey, &keyObject); err != nil {
		return AccountKey{}, fmt.Errorf("decode account key: %w", err)
	}
	keyObject.Pubkey = strings.TrimSpace(keyObject.Pubkey)
	if keyObject.Pubkey == "" {
		return AccountKey{}, errors.New("account pubkey is empty")
	}
	return AccountKey{
		Pubkey: keyObject.Pubkey,
		Signer: keyObject.Signer,
		Source: strings.TrimSpace(keyObject.Source),
	}, nil
}

func tokenAccountMetadataByAddress(rawKeys []json.RawMessage, meta TxMeta) (map[string]tokenAccountMetadata, []string) {
	accountKeys := indexedAccountKeys(rawKeys, meta.LoadedAddresses)
	accounts := make(map[string]tokenAccountMetadata)
	invalid := make(map[string]struct{})
	warnings := make([]string, 0)

	merge := func(balance TokenBalance) {
		index := int(balance.AccountIndex)
		if index < 0 || index >= len(accountKeys) {
			warnings = append(warnings, fmt.Sprintf("account_index=%d is out of range", balance.AccountIndex))
			return
		}
		address := strings.TrimSpace(accountKeys[index])
		if address == "" {
			warnings = append(warnings, fmt.Sprintf("account_index=%d has no parsed pubkey", balance.AccountIndex))
			return
		}
		if _, conflicted := invalid[address]; conflicted {
			return
		}

		next := tokenAccountMetadata{
			Owner:     strings.TrimSpace(balance.Owner),
			Mint:      strings.TrimSpace(balance.Mint),
			ProgramID: strings.TrimSpace(balance.ProgramID),
		}
		if balance.UITokenAmount != nil {
			next.Decimals = balance.UITokenAmount.Decimals
			next.HasDecimals = true
		}

		current, exists := accounts[address]
		if !exists {
			accounts[address] = next
			return
		}
		if tokenAccountMetadataConflicts(current, next) {
			delete(accounts, address)
			invalid[address] = struct{}{}
			warnings = append(warnings, fmt.Sprintf("token account %s has conflicting pre/post metadata", address))
			return
		}
		accounts[address] = mergeTokenAccountMetadata(current, next)
	}

	for _, balance := range meta.PreTokenBalances {
		merge(balance)
	}
	for _, balance := range meta.PostTokenBalances {
		merge(balance)
	}
	for address, account := range accounts {
		if account.Owner == "" || account.Mint == "" || !account.HasDecimals {
			delete(accounts, address)
			warnings = append(warnings, fmt.Sprintf("token account %s is missing owner, mint, or decimals", address))
		}
	}
	return accounts, warnings
}

func indexedAccountKeys(rawKeys []json.RawMessage, loaded LoadedAddresses) []string {
	keys := make([]string, len(rawKeys), len(rawKeys)+len(loaded.Writable)+len(loaded.Readonly))
	for index, rawKey := range rawKeys {
		var keyString string
		if err := json.Unmarshal(rawKey, &keyString); err == nil {
			keys[index] = strings.TrimSpace(keyString)
			continue
		}
		var keyObject struct {
			Pubkey string `json:"pubkey"`
		}
		if err := json.Unmarshal(rawKey, &keyObject); err == nil {
			keys[index] = strings.TrimSpace(keyObject.Pubkey)
		}
	}
	for _, address := range loaded.Writable {
		keys = append(keys, strings.TrimSpace(address))
	}
	for _, address := range loaded.Readonly {
		keys = append(keys, strings.TrimSpace(address))
	}
	return keys
}

func tokenAccountMetadataConflicts(current, next tokenAccountMetadata) bool {
	return conflictingNonEmptyString(current.Owner, next.Owner) ||
		conflictingNonEmptyString(current.Mint, next.Mint) ||
		conflictingNonEmptyString(current.ProgramID, next.ProgramID) ||
		(current.HasDecimals && next.HasDecimals && current.Decimals != next.Decimals)
}

func conflictingNonEmptyString(left, right string) bool {
	return left != "" && right != "" && left != right
}

func mergeTokenAccountMetadata(current, next tokenAccountMetadata) tokenAccountMetadata {
	if current.Owner == "" {
		current.Owner = next.Owner
	}
	if current.Mint == "" {
		current.Mint = next.Mint
	}
	if current.ProgramID == "" {
		current.ProgramID = next.ProgramID
	}
	if !current.HasDecimals && next.HasDecimals {
		current.Decimals = next.Decimals
		current.HasDecimals = true
	}
	return current
}

func firstSigner(keys []AccountKey) string {
	for _, key := range keys {
		if key.Signer {
			return key.Pubkey
		}
	}
	return ""
}

func rawString(info map[string]json.RawMessage, key string) string {
	raw, ok := info[key]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return ""
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var value interface{}
	if err := decoder.Decode(&value); err != nil {
		return ""
	}

	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		return fmt.Sprintf("%.0f", v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func rawUint8(info map[string]json.RawMessage, key string) uint8 {
	value := rawString(info, key)
	if value == "" {
		return 0
	}

	var parsed uint64
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil {
		return 0
	}
	if parsed > 255 {
		return 0
	}
	return uint8(parsed)
}

func (r *RpcListener) dispatch(eventType string, txParam *types.TransactionParam) error {
	event := dispatcher.Event{
		Chain:       r.chain.ChainID(),
		Type:        eventType,
		Transaction: txParam,
	}

	if r.bus != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
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
