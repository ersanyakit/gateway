package bitcoin

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
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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
	pollInterval           = 30 * time.Second
	maxBlocksPerPoll       = int64(2)
	safeBlockConfirmations = int64(6)
)

type RpcListener struct {
	chain                 blockchain.Chain
	registry              *asset.Registry
	chainState            *models.ChainState
	stateWriter           func(*models.ChainState) error
	bus                   *dispatcher.Dispatcher
	observeCanonicalBlock func(context.Context, constants.ChainID, int64, string, string) error

	client          *http.Client
	endpointCircuit *rpcutil.EndpointCircuit

	mu      sync.Mutex
	quit    chan struct{}
	running bool
	events  chan interface{}

	lastRetryableWarning time.Time
	throttleErrors       int
	lastBlockHash        string
	lastBlockParentHash  string
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

func (r *RpcListener) SetCanonicalBlockObserver(observer func(context.Context, constants.ChainID, int64, string, string) error) {
	r.observeCanonicalBlock = observer
}

func (r *RpcListener) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running {
		return fmt.Errorf("listener already running")
	}
	if len(r.chain.RPCs()) == 0 {
		return fmt.Errorf("%s has no Bitcoin RPC/API configured", r.chain.Name())
	}

	r.running = true
	helpers.GoSafelyRestarting("listener.bitcoin."+r.chain.Name(), r.quit, time.Second, r.pollLoop)
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
	latest, err := r.latestHeight()
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

	for height := from; height <= to; height++ {
		if err := r.processBlock(height); err != nil {
			return err
		}
		if err := r.writeProcessedBlockCheckpoint(height, confirmedHead); err != nil {
			return err
		}
	}

	return nil
}

func (r *RpcListener) writeProcessedBlockCheckpoint(height, confirmedHead int64) error {
	if r.chainState == nil {
		return errors.New("bitcoin chain state is nil")
	}
	next := *r.chainState
	listenerconfig.RecordProcessedBlockCheckpoint(&next, height, r.lastBlockHash, r.lastBlockParentHash)
	next.LastConfirmedBlock = confirmedHead
	if r.stateWriter != nil {
		if err := r.stateWriter(&next); err != nil {
			return fmt.Errorf("write chain state: %w", err)
		}
	}
	*r.chainState = next
	return nil
}

func (r *RpcListener) latestHeight() (int64, error) {
	var lastErr error
	for _, endpoint := range r.bitcoinEndpoints() {
		height, err := r.latestHeightFromEndpoint(context.Background(), endpoint)
		if err == nil {
			return height, nil
		}
		lastErr = err
		r.recordRPCFailure(endpoint.Key, err)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no bitcoin RPC/API endpoint configured")
	}
	return 0, lastErr
}

func (r *RpcListener) get(path string) ([]byte, error) {
	var lastErr error
	for _, endpoint := range r.bitcoinEndpoints() {
		if endpoint.Kind != bitcoinEndpointEsplora {
			continue
		}
		body, err := r.getFromEndpoint(context.Background(), endpoint, path)
		if err != nil {
			lastErr = err
			r.recordRPCFailure(endpoint.Key, lastErr)
			continue
		}
		r.recordRPCSuccess(endpoint.Key)
		return body, nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("no bitcoin API endpoint configured")
	}
	return nil, lastErr
}

func (r *RpcListener) getFromEndpoint(ctx context.Context, endpoint bitcoinEndpoint, path string) ([]byte, error) {
	endpointCtx, cancel := rpcutil.WithEndpointTimeout(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(endpointCtx, http.MethodGet, strings.TrimRight(endpoint.URL, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	if endpoint.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+endpoint.BearerToken)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := &bitcoinAPIError{statusCode: resp.StatusCode, body: strings.TrimSpace(string(body))}
		if rpcutil.StatusThrottled(resp.StatusCode) {
			return nil, rpcutil.NewThrottleError(err, rpcutil.RetryAfter(resp.Header))
		}
		return nil, err
	}
	return body, nil
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

type bitcoinEndpointKind string

const (
	bitcoinEndpointEsplora bitcoinEndpointKind = "esplora"
	bitcoinEndpointCore    bitcoinEndpointKind = "core"
	bitcoinEndpointUniSat  bitcoinEndpointKind = "unisat"
)

type bitcoinEndpoint struct {
	Key         string
	URL         string
	Kind        bitcoinEndpointKind
	Username    string
	Password    string
	BearerToken string
}

func (r *RpcListener) bitcoinEndpoints() []bitcoinEndpoint {
	urls := r.rpcURLs()
	endpoints := make([]bitcoinEndpoint, 0, len(urls))
	for _, raw := range urls {
		endpoint, ok := bitcoinEndpointFromURL(raw)
		if ok {
			endpoints = append(endpoints, endpoint)
		}
	}
	return endpoints
}

func bitcoinEndpointFromURL(raw string) (bitcoinEndpoint, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return bitcoinEndpoint{}, false
	}
	endpoint := bitcoinEndpoint{URL: strings.TrimRight(raw, "/"), Key: strings.TrimRight(raw, "/"), Kind: bitcoinEndpointEsplora}
	parsed, err := url.Parse(raw)
	if err != nil {
		return endpoint, true
	}
	host := strings.ToLower(parsed.Host)
	path := strings.ToLower(strings.TrimRight(parsed.Path, "/"))
	if parsed.User != nil {
		endpoint.Username = parsed.User.Username()
		endpoint.Password, _ = parsed.User.Password()
		parsed.User = nil
		endpoint.URL = strings.TrimRight(parsed.String(), "/")
		endpoint.Key = endpoint.URL
	}
	switch {
	case strings.Contains(host, "unisat.io"):
		endpoint.Kind = bitcoinEndpointUniSat
		if endpoint.Password != "" {
			endpoint.BearerToken = endpoint.Password
		} else {
			endpoint.BearerToken = endpoint.Username
		}
		endpoint.Username = ""
		endpoint.Password = ""
	case strings.Contains(host, "blockstream.info") || strings.Contains(host, "mempool.space"):
		endpoint.Kind = bitcoinEndpointEsplora
	case strings.Contains(path, "jsonrpc") || strings.Contains(path, "wallet") || strings.HasSuffix(host, ":8332") || strings.HasSuffix(host, ":18332") || endpoint.Username != "" || endpoint.Password != "":
		endpoint.Kind = bitcoinEndpointCore
	default:
		endpoint.Kind = bitcoinEndpointEsplora
	}
	return endpoint, true
}

func (r *RpcListener) latestHeightFromEndpoint(ctx context.Context, endpoint bitcoinEndpoint) (int64, error) {
	switch endpoint.Kind {
	case bitcoinEndpointUniSat:
		return r.unisatLatestHeight(ctx, endpoint)
	case bitcoinEndpointCore:
		return r.coreLatestHeight(ctx, endpoint)
	default:
		return r.esploraLatestHeight(ctx, endpoint)
	}
}

func (r *RpcListener) esploraLatestHeight(ctx context.Context, endpoint bitcoinEndpoint) (int64, error) {
	body, err := r.getFromEndpoint(ctx, endpoint, "/blocks/tip/height")
	if err != nil {
		return 0, err
	}
	height, err := strconv.ParseInt(strings.TrimSpace(string(body)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse latest bitcoin height: %w", err)
	}
	return height, nil
}

func (r *RpcListener) coreLatestHeight(ctx context.Context, endpoint bitcoinEndpoint) (int64, error) {
	var height int64
	if err := r.coreCall(ctx, endpoint, "getblockcount", nil, &height); err != nil {
		return 0, err
	}
	if height <= 0 {
		return 0, fmt.Errorf("bitcoin core latest height is not positive: %d", height)
	}
	return height, nil
}

func (r *RpcListener) coreBlock(ctx context.Context, endpoint bitcoinEndpoint, height int64) (string, string, []Tx, error) {
	var blockHash string
	if err := r.coreCall(ctx, endpoint, "getblockhash", []any{height}, &blockHash); err != nil {
		return "", "", nil, err
	}
	blockHash = strings.TrimSpace(blockHash)
	if blockHash == "" {
		return "", "", nil, fmt.Errorf("bitcoin core returned empty block hash for height %d", height)
	}

	var block bitcoinCoreBlock
	if err := r.coreCall(ctx, endpoint, "getblock", []any{blockHash, 3}, &block); err != nil {
		// Verbosity 2 omits vin.prevout. Continuing without authoritative input
		// ownership can misclassify an internal custody transfer as an external
		// deposit and credit it twice, so old/pruned nodes must fail closed.
		return "", "", nil, fmt.Errorf("bitcoin core getblock verbosity 3 (prevout data required): %w", err)
	}
	if strings.TrimSpace(block.Hash) != "" {
		if !strings.EqualFold(strings.TrimSpace(block.Hash), blockHash) {
			return "", "", nil, fmt.Errorf("bitcoin core block hash mismatch at height %d: requested %s got %s", height, blockHash, strings.TrimSpace(block.Hash))
		}
		blockHash = strings.TrimSpace(block.Hash)
	}
	if block.Height != height {
		return "", "", nil, fmt.Errorf("bitcoin core block height mismatch: requested %d got %d", height, block.Height)
	}
	if len(block.Tx) == 0 {
		return "", "", nil, fmt.Errorf("bitcoin core returned no transactions for block %d", height)
	}
	parentHash := strings.TrimSpace(block.PreviousBlockHash)
	txs := make([]Tx, 0, len(block.Tx))
	for _, coreTx := range block.Tx {
		tx, err := bitcoinCoreTxToTx(coreTx, height, blockHash)
		if err != nil {
			return "", "", nil, err
		}
		txs = append(txs, tx)
	}
	return blockHash, parentHash, txs, nil
}

func (r *RpcListener) coreCall(ctx context.Context, endpoint bitcoinEndpoint, method string, params []any, out any) error {
	body, err := json.Marshal(bitcoinCoreRPCRequest{
		JSONRPC: "2.0",
		ID:      time.Now().UnixNano(),
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return err
	}

	endpointCtx, cancel := rpcutil.WithEndpointTimeout(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(endpointCtx, http.MethodPost, endpoint.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if endpoint.Username != "" || endpoint.Password != "" {
		req.SetBasicAuth(endpoint.Username, endpoint.Password)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	respBody, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		return readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := &bitcoinAPIError{statusCode: resp.StatusCode, body: strings.TrimSpace(string(respBody))}
		if rpcutil.StatusThrottled(resp.StatusCode) {
			return rpcutil.NewThrottleError(err, rpcutil.RetryAfter(resp.Header))
		}
		return err
	}

	var rpcResp bitcoinCoreRPCResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return err
	}
	if rpcResp.Error != nil {
		return rpcResp.Error
	}
	if out == nil || string(rpcResp.Result) == "null" {
		return nil
	}
	return json.Unmarshal(rpcResp.Result, out)
}

type bitcoinAPIError struct {
	statusCode int
	body       string
}

func (e *bitcoinAPIError) Error() string {
	if e.body == "" {
		return fmt.Sprintf("bitcoin API returned HTTP %d", e.statusCode)
	}
	return fmt.Sprintf("bitcoin API returned HTTP %d: %s", e.statusCode, e.body)
}

type Tx struct {
	TxID   string `json:"txid"`
	Status struct {
		BlockHeight int64  `json:"block_height"`
		BlockHash   string `json:"block_hash"`
		Confirmed   bool   `json:"confirmed"`
	} `json:"status"`
	Vin  []Vin  `json:"vin"`
	Vout []Vout `json:"vout"`
}

type Vin struct {
	IsCoinbase     bool    `json:"is_coinbase"`
	Prevout        Prevout `json:"prevout"`
	prevoutPresent bool
}

type Prevout struct {
	Address          string `json:"scriptpubkey_address"`
	Value            int64  `json:"value"`
	ScriptPubKey     string `json:"scriptpubkey"`
	ScriptPubKeyASM  string `json:"scriptpubkey_asm"`
	ScriptPubKeyType string `json:"scriptpubkey_type"`
	valuePresent     bool
	scriptPresent    bool
}

type Vout struct {
	Address          string `json:"scriptpubkey_address"`
	Value            int64  `json:"value"`
	ScriptPubKey     string `json:"scriptpubkey"`
	ScriptPubKeyASM  string `json:"scriptpubkey_asm"`
	ScriptPubKeyType string `json:"scriptpubkey_type"`
	valuePresent     bool
	scriptPresent    bool
}

func (v *Vin) UnmarshalJSON(data []byte) error {
	type rawPrevout struct {
		Address          string `json:"scriptpubkey_address"`
		Value            *int64 `json:"value"`
		ScriptPubKey     string `json:"scriptpubkey"`
		ScriptPubKeyASM  string `json:"scriptpubkey_asm"`
		ScriptPubKeyType string `json:"scriptpubkey_type"`
	}
	var raw struct {
		IsCoinbase bool        `json:"is_coinbase"`
		Prevout    *rawPrevout `json:"prevout"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	v.IsCoinbase = raw.IsCoinbase
	v.prevoutPresent = raw.Prevout != nil
	if raw.Prevout == nil {
		v.Prevout = Prevout{}
		return nil
	}
	v.Prevout = Prevout{
		Address:          raw.Prevout.Address,
		ScriptPubKey:     raw.Prevout.ScriptPubKey,
		ScriptPubKeyASM:  raw.Prevout.ScriptPubKeyASM,
		ScriptPubKeyType: raw.Prevout.ScriptPubKeyType,
		valuePresent:     raw.Prevout.Value != nil,
		scriptPresent: strings.TrimSpace(raw.Prevout.ScriptPubKey) != "" ||
			strings.TrimSpace(raw.Prevout.ScriptPubKeyASM) != "" ||
			strings.TrimSpace(raw.Prevout.ScriptPubKeyType) != "",
	}
	if raw.Prevout.Value != nil {
		v.Prevout.Value = *raw.Prevout.Value
	}
	return nil
}

func (v *Vout) UnmarshalJSON(data []byte) error {
	var raw struct {
		Address          string `json:"scriptpubkey_address"`
		Value            *int64 `json:"value"`
		ScriptPubKey     string `json:"scriptpubkey"`
		ScriptPubKeyASM  string `json:"scriptpubkey_asm"`
		ScriptPubKeyType string `json:"scriptpubkey_type"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	v.Address = raw.Address
	v.ScriptPubKey = raw.ScriptPubKey
	v.ScriptPubKeyASM = raw.ScriptPubKeyASM
	v.ScriptPubKeyType = raw.ScriptPubKeyType
	v.valuePresent = raw.Value != nil
	v.scriptPresent = strings.TrimSpace(raw.ScriptPubKey) != "" ||
		strings.TrimSpace(raw.ScriptPubKeyASM) != "" ||
		strings.TrimSpace(raw.ScriptPubKeyType) != ""
	if raw.Value != nil {
		v.Value = *raw.Value
	}
	return nil
}

type bitcoinCoreRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type bitcoinCoreRPCResponse struct {
	Result json.RawMessage      `json:"result"`
	Error  *bitcoinCoreRPCError `json:"error"`
}

type bitcoinCoreRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *bitcoinCoreRPCError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("bitcoin core RPC error %d: %s", e.Code, e.Message)
}

type bitcoinCoreBlock struct {
	Hash              string          `json:"hash"`
	PreviousBlockHash string          `json:"previousblockhash"`
	Height            int64           `json:"height"`
	Tx                []bitcoinCoreTx `json:"tx"`
}

type bitcoinCoreTx struct {
	TxID string            `json:"txid"`
	Vin  []bitcoinCoreVin  `json:"vin"`
	Vout []bitcoinCoreVout `json:"vout"`
}

type bitcoinCoreVin struct {
	Coinbase string              `json:"coinbase"`
	Prevout  *bitcoinCorePrevout `json:"prevout"`
}

type bitcoinCorePrevout struct {
	Value        json.Number             `json:"value"`
	ScriptPubKey bitcoinCoreScriptPubKey `json:"scriptPubKey"`
}

type bitcoinCoreVout struct {
	N            int                     `json:"n"`
	Value        json.Number             `json:"value"`
	ScriptPubKey bitcoinCoreScriptPubKey `json:"scriptPubKey"`
}

type bitcoinCoreScriptPubKey struct {
	ASM       string   `json:"asm"`
	Hex       string   `json:"hex"`
	Type      string   `json:"type"`
	Address   string   `json:"address"`
	Addresses []string `json:"addresses"`
}

type BlockInfo struct {
	ID                string `json:"id"`
	Height            int64  `json:"height"`
	PreviousBlockHash string `json:"previousblockhash"`
	TxCount           int    `json:"tx_count"`
}

func (r *RpcListener) processBlock(height int64) error {
	var lastErr error
	for _, endpoint := range r.bitcoinEndpoints() {
		blockHash, parentHash, txs, err := r.blockFromEndpoint(context.Background(), endpoint, height)
		if err != nil {
			lastErr = err
			r.recordRPCFailure(endpoint.Key, err)
			continue
		}
		if r.chainState != nil {
			continuityState := *r.chainState
			if continuityErr := listenerconfig.ValidateParentContinuity(&continuityState, height, parentHash); continuityErr != nil {
				if observeErr := r.observeBlock(height, blockHash, parentHash); observeErr != nil {
					return fmt.Errorf("observe canonical bitcoin block after parent continuity failure: %w", observeErr)
				}
				listenerconfig.RewindParentContinuityCheckpoint(&continuityState, height)
				if r.stateWriter != nil {
					if writeErr := r.stateWriter(&continuityState); writeErr != nil {
						return fmt.Errorf("write bitcoin chain rollback state: %w", writeErr)
					}
				}
				*r.chainState = continuityState
				return continuityErr
			}
		}
		if err := r.observeBlock(height, blockHash, parentHash); err != nil {
			return fmt.Errorf("observe canonical bitcoin block: %w", err)
		}

		nativeAsset, ok := r.registry.GetNative(r.chain.ChainID())
		if !ok {
			return fmt.Errorf("native asset is not registered")
		}
		for _, tx := range txs {
			if err := r.handleTx(height, blockHash, parentHash, nativeAsset, tx); err != nil {
				return err
			}
		}
		r.lastBlockHash = blockHash
		r.lastBlockParentHash = parentHash
		r.recordRPCSuccess(endpoint.Key)
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no bitcoin RPC/API endpoint configured")
	}
	return lastErr
}

func (r *RpcListener) observeBlock(height int64, blockHash, parentHash string) error {
	if r.observeCanonicalBlock == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return r.observeCanonicalBlock(ctx, r.chain.ChainID(), height, blockHash, parentHash)
}

func (r *RpcListener) blockFromEndpoint(ctx context.Context, endpoint bitcoinEndpoint, height int64) (string, string, []Tx, error) {
	var (
		blockHash  string
		parentHash string
		txs        []Tx
		err        error
	)
	switch endpoint.Kind {
	case bitcoinEndpointUniSat:
		blockHash, parentHash, txs, err = r.unisatBlock(ctx, endpoint, height)
	case bitcoinEndpointCore:
		blockHash, parentHash, txs, err = r.coreBlock(ctx, endpoint, height)
	default:
		blockHash, parentHash, txs, err = r.esploraBlock(ctx, endpoint, height)
	}
	if err != nil {
		return "", "", nil, err
	}
	if err := validateBitcoinBlockResponse(height, blockHash, parentHash, txs); err != nil {
		return "", "", nil, err
	}
	return blockHash, parentHash, txs, nil
}

func validateBitcoinBlockResponse(height int64, blockHash, parentHash string, txs []Tx) error {
	blockHash = strings.TrimSpace(blockHash)
	parentHash = strings.TrimSpace(parentHash)
	if blockHash == "" {
		return fmt.Errorf("empty bitcoin block hash for height %d", height)
	}
	if height > 0 && parentHash == "" {
		return fmt.Errorf("empty bitcoin parent hash for height %d", height)
	}
	if len(txs) == 0 {
		return fmt.Errorf("bitcoin provider returned no transactions for block %d", height)
	}
	seen := make(map[string]struct{}, len(txs))
	for index, tx := range txs {
		txID := strings.TrimSpace(tx.TxID)
		if txID == "" {
			return fmt.Errorf("bitcoin provider returned transaction %d without txid for block %d", index, height)
		}
		if _, exists := seen[strings.ToLower(txID)]; exists {
			return fmt.Errorf("bitcoin provider returned duplicate transaction %s for block %d", txID, height)
		}
		seen[strings.ToLower(txID)] = struct{}{}
		if !tx.Status.Confirmed {
			return fmt.Errorf("bitcoin transaction %s from block %d is not explicitly confirmed", txID, height)
		}
		if tx.Status.BlockHeight != height {
			return fmt.Errorf("bitcoin transaction %s block height mismatch: requested %d got %d", txID, height, tx.Status.BlockHeight)
		}
		if txBlockHash := strings.TrimSpace(tx.Status.BlockHash); txBlockHash == "" || !strings.EqualFold(txBlockHash, blockHash) {
			return fmt.Errorf("bitcoin transaction %s block hash mismatch: block %s transaction %s", txID, blockHash, txBlockHash)
		}
		if len(tx.Vin) == 0 || len(tx.Vout) == 0 {
			return fmt.Errorf("bitcoin transaction %s has incomplete vin/vout data", txID)
		}
		for inputIndex, input := range tx.Vin {
			if input.IsCoinbase {
				continue
			}
			if !input.prevoutPresent || !input.Prevout.valuePresent {
				return fmt.Errorf("bitcoin transaction %s input %d is missing prevout value", txID, inputIndex)
			}
			if input.Prevout.Value < 0 {
				return fmt.Errorf("bitcoin transaction %s input %d has negative prevout value", txID, inputIndex)
			}
			if strings.TrimSpace(input.Prevout.Address) == "" || !input.Prevout.scriptPresent {
				return fmt.Errorf("bitcoin transaction %s input %d is missing usable prevout ownership", txID, inputIndex)
			}
		}
		for outputIndex, output := range tx.Vout {
			if !output.valuePresent || output.Value < 0 {
				return fmt.Errorf("bitcoin transaction %s output %d is missing a valid value", txID, outputIndex)
			}
			if !output.scriptPresent {
				return fmt.Errorf("bitcoin transaction %s output %d is missing script identity", txID, outputIndex)
			}
		}
	}
	return nil
}

func (r *RpcListener) esploraBlock(ctx context.Context, endpoint bitcoinEndpoint, height int64) (string, string, []Tx, error) {
	hashBody, err := r.getFromEndpoint(ctx, endpoint, fmt.Sprintf("/block-height/%d", height))
	if err != nil {
		return "", "", nil, err
	}
	blockHash := strings.TrimSpace(string(hashBody))
	if blockHash == "" {
		return "", "", nil, fmt.Errorf("empty block hash for bitcoin height %d", height)
	}
	parentHash, expectedTxCount, err := r.esploraBlockMetadata(ctx, endpoint, blockHash, height)
	if err != nil {
		return "", "", nil, err
	}
	var allTxs []Tx
	for offset := 0; ; offset += 25 {
		body, err := r.getFromEndpoint(ctx, endpoint, fmt.Sprintf("/block/%s/txs/%d", blockHash, offset))
		if err != nil {
			if isBitcoinTxPageEOF(err) {
				return "", "", nil, fmt.Errorf("bitcoin block %d pagination ended at %d of %d transactions", height, len(allTxs), expectedTxCount)
			}
			return "", "", nil, err
		}

		var txs []Tx
		if err := json.Unmarshal(body, &txs); err != nil {
			return "", "", nil, err
		}
		allTxs = append(allTxs, txs...)
		if len(allTxs) > expectedTxCount {
			return "", "", nil, fmt.Errorf("bitcoin block %d pagination returned %d transactions; metadata declared %d", height, len(allTxs), expectedTxCount)
		}
		if len(allTxs) == expectedTxCount {
			return blockHash, parentHash, allTxs, nil
		}
		if len(txs) < 25 {
			return "", "", nil, fmt.Errorf("bitcoin block %d pagination stopped at %d of %d transactions", height, len(allTxs), expectedTxCount)
		}
	}
}

func (r *RpcListener) esploraBlockMetadata(ctx context.Context, endpoint bitcoinEndpoint, blockHash string, height int64) (string, int, error) {
	body, err := r.getFromEndpoint(ctx, endpoint, fmt.Sprintf("/block/%s", blockHash))
	if err != nil {
		return "", 0, err
	}
	var info BlockInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return "", 0, fmt.Errorf("decode bitcoin block metadata %s: %w", blockHash, err)
	}
	if id := strings.TrimSpace(info.ID); id != "" && !strings.EqualFold(id, blockHash) {
		return "", 0, fmt.Errorf("bitcoin block metadata hash mismatch: height hash %s metadata %s", blockHash, id)
	}
	if info.Height != height {
		return "", 0, fmt.Errorf("bitcoin block metadata height mismatch: requested %d got %d", height, info.Height)
	}
	if info.TxCount <= 0 {
		return "", 0, fmt.Errorf("bitcoin block %d metadata has invalid transaction count %d", height, info.TxCount)
	}
	return strings.TrimSpace(info.PreviousBlockHash), info.TxCount, nil
}

type unisatEnvelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func (r *RpcListener) unisatLatestHeight(ctx context.Context, endpoint bitcoinEndpoint) (int64, error) {
	var data json.RawMessage
	if err := r.unisatGet(ctx, endpoint, "/v1/indexer/blockchain/info", &data); err != nil {
		return 0, err
	}
	obj, err := rawObject(data)
	if err != nil {
		return 0, err
	}
	height, ok, err := firstRawInt64(obj, "blocks", "height", "blockHeight", "block_height", "bestHeight")
	if err != nil {
		return 0, err
	}
	if !ok || height <= 0 {
		return 0, fmt.Errorf("unisat blockchain info did not include latest height")
	}
	return height, nil
}

func (r *RpcListener) unisatBlock(ctx context.Context, endpoint bitcoinEndpoint, height int64) (string, string, []Tx, error) {
	var blockRaw json.RawMessage
	if err := r.unisatGet(ctx, endpoint, fmt.Sprintf("/v1/indexer/height/%d/block", height), &blockRaw); err != nil {
		return "", "", nil, err
	}
	blockObj, err := rawObject(blockRaw)
	if err != nil {
		return "", "", nil, err
	}
	blockHash := firstRawString(blockObj, "hash", "id", "blockHash", "block_hash")
	parentHash := firstRawString(blockObj, "previousblockhash", "previousBlockHash", "previous_block_hash", "parentHash", "parent_hash")
	if blockHash == "" {
		return "", "", nil, fmt.Errorf("unisat block %d did not include block hash", height)
	}

	const pageSize = 100
	var allTxs []Tx
	expectedTotal := -1
	for cursor := 0; ; cursor += pageSize {
		var pageRaw json.RawMessage
		path := fmt.Sprintf("/v1/indexer/block/%d/txs?cursor=%d&size=%d", height, cursor, pageSize)
		if err := r.unisatGet(ctx, endpoint, path, &pageRaw); err != nil {
			return "", "", nil, err
		}
		txs, total, err := bitcoinUniSatTxPageToTxs(pageRaw, height, blockHash)
		if err != nil {
			return "", "", nil, err
		}
		if total <= 0 {
			return "", "", nil, fmt.Errorf("unisat block %d transaction page omitted a valid total", height)
		}
		if expectedTotal < 0 {
			expectedTotal = total
		} else if total != expectedTotal {
			return "", "", nil, fmt.Errorf("unisat block %d transaction total changed from %d to %d", height, expectedTotal, total)
		}
		allTxs = append(allTxs, txs...)
		if len(allTxs) > expectedTotal {
			return "", "", nil, fmt.Errorf("unisat block %d returned %d transactions; declared total is %d", height, len(allTxs), expectedTotal)
		}
		if len(allTxs) == expectedTotal {
			return blockHash, parentHash, allTxs, nil
		}
		if len(txs) < pageSize {
			return "", "", nil, fmt.Errorf("unisat block %d pagination stopped at %d of %d transactions", height, len(allTxs), expectedTotal)
		}
	}
}

func (r *RpcListener) unisatGet(ctx context.Context, endpoint bitcoinEndpoint, path string, out any) error {
	body, err := r.getFromEndpoint(ctx, endpoint, path)
	if err != nil {
		return err
	}
	var env unisatEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return err
	}
	if env.Code != 0 {
		err := fmt.Errorf("unisat API error %d: %s", env.Code, env.Msg)
		if env.Code == -2004 || env.Code == -2005 || strings.Contains(strings.ToLower(env.Msg), "rate limit") {
			return rpcutil.NewThrottleError(err, 0)
		}
		return err
	}
	if out == nil {
		return nil
	}
	if raw, ok := out.(*json.RawMessage); ok {
		*raw = append((*raw)[:0], env.Data...)
		return nil
	}
	return json.Unmarshal(env.Data, out)
}

func bitcoinUniSatTxPageToTxs(raw json.RawMessage, height int64, blockHash string) ([]Tx, int, error) {
	txRaws, total, err := rawTxArray(raw)
	if err != nil {
		return nil, 0, err
	}
	txs := make([]Tx, 0, len(txRaws))
	for _, txRaw := range txRaws {
		tx, err := bitcoinUniSatRawTxToTx(txRaw, height, blockHash)
		if err != nil {
			return nil, 0, err
		}
		if tx.TxID != "" {
			txs = append(txs, tx)
		}
	}
	return txs, total, nil
}

func bitcoinUniSatRawTxToTx(raw json.RawMessage, height int64, blockHash string) (Tx, error) {
	obj, err := rawObject(raw)
	if err != nil {
		return Tx{}, err
	}
	tx := Tx{TxID: firstRawString(obj, "txid", "txId", "txID", "hash")}
	tx.Status.BlockHeight = height
	tx.Status.BlockHash = blockHash
	tx.Status.Confirmed = true

	for _, vinRaw := range firstRawArray(obj, "vin", "inputs", "ins") {
		vinObj, err := rawObject(vinRaw)
		if err != nil {
			return Tx{}, err
		}
		input := Vin{IsCoinbase: firstRawString(vinObj, "coinbase") != ""}
		prevObj := vinObj
		nestedPrevout := false
		if nested, ok := firstRawObject(vinObj, "prevout", "prevOut", "prev_output", "prevOutput", "utxo"); ok {
			prevObj = nested
			nestedPrevout = true
		}
		scriptObj, _ := firstRawObject(prevObj, "scriptPubKey", "scriptpubkey")
		input.Prevout.Address = firstNonEmpty(
			firstRawString(prevObj, "scriptpubkey_address", "address", "addr"),
			firstRawString(scriptObj, "address"),
		)
		value, ok, err := firstRawSats(prevObj, scriptObj)
		if err != nil {
			return Tx{}, err
		}
		if ok {
			input.Prevout.Value = value
		}
		input.Prevout.valuePresent = ok
		input.Prevout.ScriptPubKey = firstNonEmpty(
			firstRawString(prevObj, "scriptpubkey", "scriptPk", "script"),
			firstRawString(scriptObj, "hex"),
		)
		input.Prevout.ScriptPubKeyASM = firstNonEmpty(firstRawString(prevObj, "scriptpubkey_asm", "scriptPkAsm"), firstRawString(scriptObj, "asm"))
		input.Prevout.ScriptPubKeyType = firstNonEmpty(firstRawString(prevObj, "scriptpubkey_type", "scriptType"), firstRawString(scriptObj, "type"))
		input.Prevout.scriptPresent = strings.TrimSpace(input.Prevout.ScriptPubKey) != "" || strings.TrimSpace(input.Prevout.ScriptPubKeyASM) != "" || strings.TrimSpace(input.Prevout.ScriptPubKeyType) != ""
		input.prevoutPresent = nestedPrevout || input.Prevout.Address != "" || input.Prevout.valuePresent || input.Prevout.scriptPresent
		tx.Vin = append(tx.Vin, input)
	}

	for _, voutRaw := range firstRawArray(obj, "vout", "outputs", "outs") {
		voutObj, err := rawObject(voutRaw)
		if err != nil {
			return Tx{}, err
		}
		scriptObj, _ := firstRawObject(voutObj, "scriptPubKey", "scriptpubkey")
		value, ok, err := firstRawSats(voutObj, scriptObj)
		if err != nil {
			return Tx{}, err
		}
		output := Vout{
			Address: firstNonEmpty(
				firstRawString(voutObj, "scriptpubkey_address", "address", "addr"),
				firstRawString(scriptObj, "address"),
			),
			ScriptPubKey: firstNonEmpty(
				firstRawString(voutObj, "scriptpubkey", "scriptPk", "script"),
				firstRawString(scriptObj, "hex"),
			),
			ScriptPubKeyASM: firstNonEmpty(
				firstRawString(voutObj, "scriptpubkey_asm", "scriptPkAsm"),
				firstRawString(scriptObj, "asm"),
			),
			ScriptPubKeyType: firstNonEmpty(
				firstRawString(voutObj, "scriptpubkey_type", "scriptType"),
				firstRawString(scriptObj, "type"),
			),
		}
		if ok {
			output.Value = value
		}
		output.valuePresent = ok
		output.scriptPresent = strings.TrimSpace(output.ScriptPubKey) != "" || strings.TrimSpace(output.ScriptPubKeyASM) != "" || strings.TrimSpace(output.ScriptPubKeyType) != ""
		tx.Vout = append(tx.Vout, output)
	}
	return tx, nil
}

func isBitcoinTxPageEOF(err error) bool {
	var apiErr *bitcoinAPIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.statusCode == http.StatusNotFound &&
		strings.Contains(strings.ToLower(apiErr.body), "start index out of range")
}

func (r *RpcListener) handleTx(height int64, blockHash, parentHash string, nativeAsset asset.Asset, tx Tx) error {
	from := "coinbase"
	fromAddresses := make([]string, 0, len(tx.Vin))
	for _, input := range tx.Vin {
		if input.Prevout.Address != "" {
			fromAddresses = append(fromAddresses, input.Prevout.Address)
			if from == "coinbase" {
				from = input.Prevout.Address
			}
		}
	}

	status := "confirmed"
	if !tx.Status.Confirmed {
		status = "pending"
	}

	memo := bitcoinTxMemo(tx)
	for idx, output := range tx.Vout {
		if output.Address == "" || output.Value <= 0 {
			continue
		}

		txParam := &types.TransactionParam{
			Context:       context.Background(),
			ChainID:       r.chain.ChainID(),
			Symbol:        helpers.StrPtr(nativeAsset.GetSymbol()),
			Decimals:      nativeAsset.GetDecimals(),
			Hash:          helpers.StrPtr(tx.TxID),
			Block:         helpers.StrPtr(fmt.Sprintf("%d", height)),
			BlockHash:     helpers.StrPtr(blockHash),
			ParentHash:    helpers.StrPtr(parentHash),
			Token:         nil,
			From:          helpers.StrPtr(from),
			FromAddresses: append([]string(nil), fromAddresses...),
			To:            helpers.StrPtr(output.Address),
			Amount:        helpers.StrPtr(fmt.Sprintf("%d", output.Value)),
			LogIndex:      helpers.StrPtr(fmt.Sprintf("vout:%d", idx)),
			Status:        helpers.StrPtr(status),
			Memo:          optionalMemoPtr(memo),
		}

		if err := r.dispatch("utxo_transfer", txParam); err != nil {
			return err
		}
	}

	return nil
}

func bitcoinCoreTxToTx(coreTx bitcoinCoreTx, height int64, blockHash string) (Tx, error) {
	tx := Tx{TxID: coreTx.TxID}
	tx.Status.BlockHeight = height
	tx.Status.BlockHash = blockHash
	tx.Status.Confirmed = true
	for _, vin := range coreTx.Vin {
		input := Vin{IsCoinbase: vin.Coinbase != ""}
		if !input.IsCoinbase && vin.Prevout == nil {
			return Tx{}, fmt.Errorf("bitcoin core non-coinbase vin is missing verbosity=3 prevout")
		}
		if vin.Prevout != nil {
			input.Prevout.Address = bitcoinCoreAddress(vin.Prevout.ScriptPubKey)
			input.Prevout.ScriptPubKey = vin.Prevout.ScriptPubKey.Hex
			input.Prevout.ScriptPubKeyASM = vin.Prevout.ScriptPubKey.ASM
			input.Prevout.ScriptPubKeyType = vin.Prevout.ScriptPubKey.Type
			value, err := bitcoinValueToSats(vin.Prevout.Value)
			if err != nil {
				return Tx{}, fmt.Errorf("convert bitcoin core vin value: %w", err)
			}
			input.Prevout.Value = value
			input.prevoutPresent = true
			input.Prevout.valuePresent = true
			input.Prevout.scriptPresent = strings.TrimSpace(input.Prevout.ScriptPubKey) != "" || strings.TrimSpace(input.Prevout.ScriptPubKeyASM) != "" || strings.TrimSpace(input.Prevout.ScriptPubKeyType) != ""
		}
		if !input.IsCoinbase && (strings.TrimSpace(input.Prevout.Address) == "" || !input.Prevout.scriptPresent || input.Prevout.Value < 0) {
			return Tx{}, fmt.Errorf("bitcoin core non-coinbase vin has incomplete prevout ownership")
		}
		tx.Vin = append(tx.Vin, input)
	}
	for _, vout := range coreTx.Vout {
		value, err := bitcoinValueToSats(vout.Value)
		if err != nil {
			return Tx{}, fmt.Errorf("convert bitcoin core vout value: %w", err)
		}
		output := Vout{
			Address:          bitcoinCoreAddress(vout.ScriptPubKey),
			Value:            value,
			ScriptPubKey:     vout.ScriptPubKey.Hex,
			ScriptPubKeyASM:  vout.ScriptPubKey.ASM,
			ScriptPubKeyType: vout.ScriptPubKey.Type,
			valuePresent:     true,
			scriptPresent:    strings.TrimSpace(vout.ScriptPubKey.Hex) != "" || strings.TrimSpace(vout.ScriptPubKey.ASM) != "" || strings.TrimSpace(vout.ScriptPubKey.Type) != "",
		}
		if output.Value < 0 || !output.scriptPresent {
			return Tx{}, fmt.Errorf("bitcoin core vout %d has incomplete value/script data", vout.N)
		}
		tx.Vout = append(tx.Vout, output)
	}
	return tx, nil
}

func bitcoinCoreAddress(script bitcoinCoreScriptPubKey) string {
	if address := strings.TrimSpace(script.Address); address != "" {
		return address
	}
	for _, address := range script.Addresses {
		if address = strings.TrimSpace(address); address != "" {
			return address
		}
	}
	return ""
}

func rawObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if len(raw) == 0 || string(raw) == "null" {
		return obj, fmt.Errorf("expected JSON object, got empty")
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	return obj, nil
}

func rawTxArray(raw json.RawMessage) ([]json.RawMessage, int, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, 0, nil
	}
	if raw[0] == '[' {
		var txs []json.RawMessage
		if err := json.Unmarshal(raw, &txs); err != nil {
			return nil, 0, err
		}
		return txs, len(txs), nil
	}

	obj, err := rawObject(raw)
	if err != nil {
		return nil, 0, err
	}
	total, _, err := firstRawInt64(obj, "total", "totalCount", "total_count")
	if err != nil {
		return nil, 0, err
	}
	for _, key := range []string{"list", "txs", "transactions", "items", "detail", "data"} {
		if arr := firstRawArray(obj, key); arr != nil {
			return arr, int(total), nil
		}
	}
	return nil, int(total), nil
}

func rawField(obj map[string]json.RawMessage, keys ...string) (json.RawMessage, bool) {
	if obj == nil {
		return nil, false
	}
	for _, key := range keys {
		if raw, ok := obj[key]; ok {
			return raw, true
		}
	}
	for _, key := range keys {
		for existing, raw := range obj {
			if strings.EqualFold(existing, key) {
				return raw, true
			}
		}
	}
	return nil, false
}

func firstRawString(obj map[string]json.RawMessage, keys ...string) string {
	raw, ok := rawField(obj, keys...)
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return strings.TrimSpace(value)
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		return strings.TrimSpace(number.String())
	}
	return strings.TrimSpace(string(raw))
}

func firstRawObject(obj map[string]json.RawMessage, keys ...string) (map[string]json.RawMessage, bool) {
	raw, ok := rawField(obj, keys...)
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return nil, false
	}
	nested, err := rawObject(raw)
	if err != nil {
		return nil, false
	}
	return nested, true
}

func firstRawArray(obj map[string]json.RawMessage, keys ...string) []json.RawMessage {
	raw, ok := rawField(obj, keys...)
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil
	}
	return arr
}

func firstRawInt64(obj map[string]json.RawMessage, keys ...string) (int64, bool, error) {
	raw, ok := rawField(obj, keys...)
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return 0, false, nil
	}
	value, err := rawInt64(raw)
	if err != nil {
		return 0, false, err
	}
	return value, true, nil
}

func firstRawSats(objects ...map[string]json.RawMessage) (int64, bool, error) {
	for _, obj := range objects {
		if value, ok, err := firstRawInt64(obj, "satoshi", "satoshis", "valueSat", "value_sat", "valueSatoshi", "amountSat"); ok || err != nil {
			return value, ok, err
		}
	}
	for _, obj := range objects {
		raw, ok := rawField(obj, "value", "amount")
		if !ok || len(raw) == 0 || string(raw) == "null" {
			continue
		}
		value, err := rawBitcoinAmount(raw)
		if err != nil {
			return 0, false, err
		}
		return value, true, nil
	}
	return 0, false, nil
}

func rawInt64(raw json.RawMessage) (int64, error) {
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		if value, err := number.Int64(); err == nil {
			return value, nil
		}
		return 0, fmt.Errorf("invalid integer amount %q", number.String())
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, err
	}
	value, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func rawBitcoinAmount(raw json.RawMessage) (int64, error) {
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		text := number.String()
		if strings.Contains(text, ".") {
			return bitcoinValueToSats(number)
		}
		return number.Int64()
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, err
	}
	text = strings.TrimSpace(text)
	if strings.Contains(text, ".") {
		return bitcoinValueToSats(json.Number(text))
	}
	return strconv.ParseInt(text, 10, 64)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func bitcoinValueToSats(value json.Number) (int64, error) {
	raw := strings.TrimSpace(value.String())
	if raw == "" {
		return 0, nil
	}
	rat, ok := new(big.Rat).SetString(raw)
	if !ok {
		return 0, fmt.Errorf("invalid BTC amount %q", raw)
	}
	rat.Mul(rat, big.NewRat(100000000, 1))
	if !rat.IsInt() {
		return 0, fmt.Errorf("BTC amount has more than 8 decimals: %q", raw)
	}
	if !rat.Num().IsInt64() {
		return 0, fmt.Errorf("BTC amount overflows int64: %q", raw)
	}
	return rat.Num().Int64(), nil
}

func bitcoinTxMemo(tx Tx) string {
	for _, output := range tx.Vout {
		if memo := bitcoinOutputMemo(output); memo != "" {
			return memo
		}
	}
	return ""
}

func bitcoinOutputMemo(output Vout) string {
	outputType := strings.ToLower(strings.TrimSpace(output.ScriptPubKeyType))
	asm := strings.TrimSpace(output.ScriptPubKeyASM)
	if outputType != "op_return" && !strings.HasPrefix(strings.ToUpper(asm), "OP_RETURN") {
		return ""
	}

	if memo := bitcoinASMOpReturnMemo(asm); memo != "" {
		return memo
	}
	return bitcoinScriptOpReturnMemo(output.ScriptPubKey)
}

func bitcoinASMOpReturnMemo(asm string) string {
	parts := strings.Fields(asm)
	if len(parts) < 2 || strings.ToUpper(parts[0]) != "OP_RETURN" {
		return ""
	}
	var payload []byte
	for _, part := range parts[1:] {
		if strings.HasPrefix(strings.ToUpper(part), "OP_PUSHBYTES_") {
			continue
		}
		decoded, err := hex.DecodeString(strings.TrimPrefix(part, "0x"))
		if err != nil {
			continue
		}
		payload = append(payload, decoded...)
	}
	return readableMemoBytes(payload)
}

func bitcoinScriptOpReturnMemo(scriptHex string) string {
	scriptHex = strings.TrimPrefix(strings.TrimSpace(scriptHex), "0x")
	script, err := hex.DecodeString(scriptHex)
	if err != nil || len(script) < 2 || script[0] != 0x6a {
		return ""
	}

	offset := 1
	var payload []byte
	for offset < len(script) {
		opcode := script[offset]
		offset++
		if opcode == 0x4c {
			if offset >= len(script) {
				return ""
			}
			opcode = script[offset]
			offset++
		}
		if opcode == 0 || opcode > 0x4b {
			return ""
		}
		size := int(opcode)
		if offset+size > len(script) {
			return ""
		}
		payload = append(payload, script[offset:offset+size]...)
		offset += size
	}
	return readableMemoBytes(payload)
}

func readableMemoBytes(payload []byte) string {
	if len(payload) == 0 || !utf8.Valid(payload) {
		return ""
	}
	memo := strings.TrimSpace(string(payload))
	if memo == "" {
		return ""
	}
	for _, r := range memo {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			return ""
		}
	}
	return memo
}

func optionalMemoPtr(memo string) *string {
	memo = strings.TrimSpace(memo)
	if memo == "" {
		return nil
	}
	return helpers.StrPtr(memo)
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
