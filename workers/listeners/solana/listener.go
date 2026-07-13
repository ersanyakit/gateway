package solana

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
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
	listenerconfig "core/workers/listeners"
	"core/workers/listeners/rpcutil"
)

const (
	pollInterval    = 10 * time.Second
	maxSlotsPerPoll = int64(8)
	unknownProgram  = "unknown_program"
	unknownSigner   = "unknown_signer"

	solanaMemoProgram    = "MemoSq4gqABAXKb96qnH8TysNcWxMyWCqXgDLGmfcHr"
	solanaMemoProgramOld = "Memo1UhkJRfHyvLMcVucJwxXeuD728EqVDDwQDxFMNo"
)

type RpcListener struct {
	chain       blockchain.Chain
	registry    *asset.Registry
	chainState  *models.ChainState
	stateWriter func(*models.ChainState) error
	bus         *dispatcher.Dispatcher

	client          *http.Client
	endpointCircuit *rpcutil.EndpointCircuit

	mu      sync.Mutex
	quit    chan struct{}
	running bool
	events  chan interface{}

	lastRetryableWarning time.Time
	throttleErrors       int
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
	helpers.GoSafely("listener.solana."+r.chain.Name(), r.pollLoop)
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
		if err := r.writeChainState(slot); err != nil {
			return err
		}
	}

	if r.chainState.LastProcessedBlock < to {
		if err := r.writeChainState(to); err != nil {
			return err
		}
	}

	return nil
}

func (r *RpcListener) writeChainState(block int64) error {
	r.chainState.LastProcessedBlock = block
	r.chainState.LastConfirmedBlock = block
	if r.stateWriter == nil {
		return nil
	}
	if err := r.stateWriter(r.chainState); err != nil {
		return fmt.Errorf("write chain state: %w", err)
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
	var throttleErr error
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
			lastErr = err
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
		if out == nil || string(rpcResp.Result) == "null" {
			r.recordRPCSuccess(rpcURL)
			return nil
		}
		if err := json.Unmarshal(rpcResp.Result, out); err != nil {
			lastErr = err
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
		lastErr = fmt.Errorf("no solana RPC endpoint configured")
	}
	return lastErr
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
	tokenAccounts, tokenBalanceWarnings := tokenAccountMetadataByAddress(tx.Transaction.Message.AccountKeys, tx.Meta)
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
	case program == "system" && instructionType == "transfer":
		source := rawString(parsed.Info, "source")
		destination := rawString(parsed.Info, "destination")
		lamports := rawString(parsed.Info, "lamports")
		if source == "" || destination == "" || lamports == "" {
			return false, nil
		}
		if !positiveRawAmount(lamports) {
			return true, nil
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
			Memo:      optionalMemoPtr(memo),
		}
		return true, r.dispatch("sol_transfer", txParam)

	case strings.HasPrefix(program, "spl-token") && (instructionType == "transfer" || instructionType == "transferchecked"):
		source := rawString(parsed.Info, "source")
		destination := rawString(parsed.Info, "destination")
		mint := rawString(parsed.Info, "mint")
		amount := rawString(parsed.Info, "amount")

		if tokenAmountRaw, ok := parsed.Info["tokenAmount"]; ok {
			var tokenAmount map[string]json.RawMessage
			if err := json.Unmarshal(tokenAmountRaw, &tokenAmount); err == nil {
				if amount == "" {
					amount = rawString(tokenAmount, "amount")
				}
			}
		}
		if source == "" || destination == "" || amount == "" {
			return true, nil
		}
		if !positiveRawAmount(amount) {
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
			return true, nil
		}

		if mint == "" {
			mint = destinationMetadata.Mint
		}
		if mint != destinationMetadata.Mint {
			return true, nil
		}
		if sourceMetadata.ProgramID != "" && destinationMetadata.ProgramID != "" && sourceMetadata.ProgramID != destinationMetadata.ProgramID {
			return true, nil
		}
		instructionProgramID := strings.TrimSpace(instruction.ProgramID)
		if instructionProgramID != "" {
			if sourceMetadata.ProgramID != "" && sourceMetadata.ProgramID != instructionProgramID {
				return true, nil
			}
			if destinationMetadata.ProgramID != "" && destinationMetadata.ProgramID != instructionProgramID {
				return true, nil
			}
		}
		if r.registry == nil {
			return true, nil
		}
		assetInfo, ok := r.registry.Get(r.chain.ChainID(), mint)
		if !ok || assetInfo.GetDecimals() != destinationMetadata.Decimals {
			return true, nil
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
