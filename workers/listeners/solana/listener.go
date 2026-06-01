package solana

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"core/asset"
	"core/blockchain"
	"core/helpers"
	"core/models"
	"core/types"
	"core/workers/dispatcher"
)

const (
	pollInterval    = 10 * time.Second
	maxSlotsPerPoll = int64(8)
	unknownProgram  = "unknown_program"
	unknownSigner   = "unknown_signer"
)

type RpcListener struct {
	chain       blockchain.Chain
	registry    *asset.Registry
	chainState  *models.ChainState
	stateWriter func(*models.ChainState) error
	bus         *dispatcher.Dispatcher

	client *http.Client

	mu      sync.Mutex
	quit    chan struct{}
	running bool
	events  chan interface{}
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
		chain:       chain,
		registry:    registry,
		chainState:  state,
		bus:         bus,
		stateWriter: stateWriter,
		client:      &http.Client{Timeout: 30 * time.Second},
		quit:        make(chan struct{}),
		events:      make(chan interface{}, 1000),
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
	go r.pollLoop()
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
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		if err := r.catchUp(); err != nil {
			log.Printf("[%s] listener catch-up error: %v\n", r.chain.Name(), err)
		}

		select {
		case <-r.quit:
			return
		case <-ticker.C:
		}
	}
}

func (r *RpcListener) catchUp() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	latest, err := r.latestSlot(ctx)
	if err != nil {
		return err
	}
	if latest <= 0 {
		return nil
	}

	from := r.chainState.LastProcessedBlock + 1
	if from <= 1 {
		from = latest
	}
	if from > latest {
		return nil
	}

	to := from + maxSlotsPerPoll - 1
	if to > latest {
		to = latest
	}

	slots, err := r.blocksInRange(ctx, from, to)
	if err != nil {
		return err
	}

	for _, slot := range slots {
		if err := r.processSlot(ctx, slot); err != nil {
			return err
		}
	}

	r.chainState.LastProcessedBlock = to
	r.chainState.LastConfirmedBlock = to
	if r.stateWriter != nil {
		if err := r.stateWriter(r.chainState); err != nil {
			return fmt.Errorf("write chain state: %w", err)
		}
	}

	return nil
}

func (r *RpcListener) latestSlot(ctx context.Context) (int64, error) {
	var slot int64
	if err := r.rpcCall(ctx, "getSlot", []interface{}{map[string]interface{}{"commitment": "finalized"}}, &slot); err != nil {
		return 0, err
	}
	return slot, nil
}

func (r *RpcListener) blocksInRange(ctx context.Context, from, to int64) ([]int64, error) {
	var slots []int64
	if err := r.rpcCall(ctx, "getBlocks", []interface{}{
		from,
		to,
		map[string]interface{}{"commitment": "finalized"},
	}, &slots); err != nil {
		return nil, err
	}
	return slots, nil
}

type jsonRPCResponse struct {
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
	for _, rpcURL := range r.chain.RPCs() {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, bytes.NewReader(body))
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := r.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("%s RPC returned HTTP %d: %s", r.chain.Name(), resp.StatusCode, string(respBody))
			continue
		}

		var rpcResp jsonRPCResponse
		if err := json.Unmarshal(respBody, &rpcResp); err != nil {
			lastErr = err
			continue
		}
		if rpcResp.Error != nil {
			lastErr = fmt.Errorf("%s RPC %s error %d: %s", r.chain.Name(), method, rpcResp.Error.Code, rpcResp.Error.Message)
			continue
		}
		if out == nil || string(rpcResp.Result) == "null" {
			return nil
		}
		if err := json.Unmarshal(rpcResp.Result, out); err != nil {
			lastErr = err
			continue
		}

		return nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("no solana RPC endpoint configured")
	}
	return lastErr
}

type Block struct {
	Blockhash    string    `json:"blockhash"`
	BlockHeight  *int64    `json:"blockHeight"`
	Transactions []BlockTx `json:"transactions"`
}

type BlockTx struct {
	Meta        TxMeta      `json:"meta"`
	Transaction Transaction `json:"transaction"`
}

type TxMeta struct {
	Err               json.RawMessage     `json:"err"`
	InnerInstructions []InnerInstructions `json:"innerInstructions"`
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

	nativeAsset, ok := r.registry.GetNative(r.chain.ChainID())
	if !ok {
		return fmt.Errorf("native asset is not registered")
	}

	blockNumber := fmt.Sprintf("%d", slot)
	if block.BlockHeight != nil {
		blockNumber = fmt.Sprintf("%d", *block.BlockHeight)
	}

	for txIndex, tx := range block.Transactions {
		if err := r.handleTransaction(blockNumber, block.Blockhash, txIndex, nativeAsset, tx); err != nil {
			return err
		}
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
	if len(tx.Meta.Err) > 0 && string(tx.Meta.Err) != "null" {
		status = "failed"
	}

	accountKeys := parseAccountKeys(tx.Transaction.Message.AccountKeys)
	signer := firstSigner(accountKeys)
	if signer == "" && len(accountKeys) > 0 {
		signer = accountKeys[0].Pubkey
	}
	if signer == "" {
		signer = unknownSigner
	}

	for ixIndex, instruction := range tx.Transaction.Message.Instructions {
		if err := r.handleInstruction(blockNumber, blockHash, hash, fmt.Sprintf("ix:%d", ixIndex), nativeAsset, status, signer, instruction); err != nil {
			return err
		}
	}

	for _, group := range tx.Meta.InnerInstructions {
		for innerIndex, instruction := range group.Instructions {
			logIndex := fmt.Sprintf("inner:%d:%d", group.Index, innerIndex)
			if err := r.handleInstruction(blockNumber, blockHash, hash, logIndex, nativeAsset, status, signer, instruction); err != nil {
				return err
			}
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
	instruction Instruction,
) error {
	handled, err := r.handleParsedTransfer(blockNumber, blockHash, hash, logIndex, nativeAsset, status, instruction)
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
	instruction Instruction,
) (bool, error) {
	if len(instruction.Parsed) == 0 || string(instruction.Parsed) == "null" {
		return false, nil
	}

	var parsed ParsedInstruction
	if err := json.Unmarshal(instruction.Parsed, &parsed); err != nil {
		return false, nil
	}

	instructionType := strings.ToLower(parsed.Type)
	program := strings.ToLower(instruction.Program)
	switch {
	case program == "system" && instructionType == "transfer":
		source := rawString(parsed.Info, "source")
		destination := rawString(parsed.Info, "destination")
		lamports := rawString(parsed.Info, "lamports")
		if source == "" || destination == "" || lamports == "" {
			return false, nil
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
			From:      helpers.StrPtr(source),
			To:        helpers.StrPtr(destination),
			Amount:    helpers.StrPtr(lamports),
			LogIndex:  helpers.StrPtr(logIndex),
			Status:    helpers.StrPtr(status),
		}
		return true, r.dispatch("sol_transfer", txParam)

	case strings.HasPrefix(program, "spl-token") && (instructionType == "transfer" || instructionType == "transferchecked"):
		source := rawString(parsed.Info, "source")
		destination := rawString(parsed.Info, "destination")
		mint := rawString(parsed.Info, "mint")
		amount := rawString(parsed.Info, "amount")
		decimals := uint8(0)

		if tokenAmountRaw, ok := parsed.Info["tokenAmount"]; ok {
			var tokenAmount map[string]json.RawMessage
			if err := json.Unmarshal(tokenAmountRaw, &tokenAmount); err == nil {
				if amount == "" {
					amount = rawString(tokenAmount, "amount")
				}
				decimals = rawUint8(tokenAmount, "decimals")
			}
		}

		symbol := "SPL"
		if mint != "" && r.registry != nil {
			if assetInfo, ok := r.registry.Get(r.chain.ChainID(), mint); ok {
				symbol = assetInfo.GetSymbol()
				decimals = assetInfo.GetDecimals()
			}
		}
		if source == "" || destination == "" || amount == "" {
			return false, nil
		}

		var token *string
		if mint != "" {
			token = helpers.StrPtr(mint)
		}

		txParam := &types.TransactionParam{
			Context:   context.Background(),
			ChainID:   r.chain.ChainID(),
			Symbol:    helpers.StrPtr(symbol),
			Decimals:  decimals,
			Hash:      helpers.StrPtr(hash),
			Block:     helpers.StrPtr(blockNumber),
			BlockHash: helpers.StrPtr(blockHash),
			Token:     token,
			From:      helpers.StrPtr(source),
			To:        helpers.StrPtr(destination),
			Amount:    helpers.StrPtr(amount),
			LogIndex:  helpers.StrPtr(logIndex),
			Status:    helpers.StrPtr(status),
		}
		return true, r.dispatch("spl_transfer", txParam)
	}

	return false, nil
}

func parseAccountKeys(rawKeys []json.RawMessage) []AccountKey {
	keys := make([]AccountKey, 0, len(rawKeys))
	for _, rawKey := range rawKeys {
		var keyString string
		if err := json.Unmarshal(rawKey, &keyString); err == nil && keyString != "" {
			keys = append(keys, AccountKey{Pubkey: keyString})
			continue
		}

		var keyObject struct {
			Pubkey string `json:"pubkey"`
			Signer bool   `json:"signer"`
		}
		if err := json.Unmarshal(rawKey, &keyObject); err == nil && keyObject.Pubkey != "" {
			keys = append(keys, AccountKey{Pubkey: keyObject.Pubkey, Signer: keyObject.Signer})
		}
	}
	return keys
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
