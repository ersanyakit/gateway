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
	helpers.GoSafely("listener.bitcoin."+r.chain.Name(), r.pollLoop)
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
		listenerconfig.RecordProcessedBlockCheckpoint(r.chainState, height, r.lastBlockHash, r.lastBlockParentHash)
		r.chainState.LastConfirmedBlock = confirmedHead
		if r.stateWriter != nil {
			if err := r.stateWriter(r.chainState); err != nil {
				return fmt.Errorf("write chain state: %w", err)
			}
		}
	}

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
	err := r.coreCall(ctx, endpoint, "getblock", []any{blockHash, 3}, &block)
	if err != nil && !rpcutil.IsRetryable(err) {
		err = r.coreCall(ctx, endpoint, "getblock", []any{blockHash, 2}, &block)
	}
	if err != nil {
		return "", "", nil, err
	}
	if strings.TrimSpace(block.Hash) != "" {
		blockHash = strings.TrimSpace(block.Hash)
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
	IsCoinbase bool    `json:"is_coinbase"`
	Prevout    Prevout `json:"prevout"`
}

type Prevout struct {
	Address string `json:"scriptpubkey_address"`
	Value   int64  `json:"value"`
}

type Vout struct {
	Address          string `json:"scriptpubkey_address"`
	Value            int64  `json:"value"`
	ScriptPubKey     string `json:"scriptpubkey"`
	ScriptPubKeyASM  string `json:"scriptpubkey_asm"`
	ScriptPubKeyType string `json:"scriptpubkey_type"`
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
}

func (r *RpcListener) processBlock(height int64) error {
	nativeAsset, ok := r.registry.GetNative(r.chain.ChainID())
	if !ok {
		return fmt.Errorf("native asset is not registered")
	}

	var lastErr error
	for _, endpoint := range r.bitcoinEndpoints() {
		blockHash, parentHash, txs, err := r.blockFromEndpoint(context.Background(), endpoint, height)
		if err != nil {
			if errors.Is(err, listenerconfig.ErrParentContinuity) {
				return err
			}
			lastErr = err
			r.recordRPCFailure(endpoint.Key, err)
			continue
		}
		for _, tx := range txs {
			if err := r.handleTx(height, blockHash, nativeAsset, tx); err != nil {
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
	if err := r.validateBlockContinuity(height, blockHash, parentHash); err != nil {
		return "", "", nil, err
	}
	return blockHash, parentHash, txs, nil
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
	parentHash, err := r.esploraBlockParentHash(ctx, endpoint, blockHash)
	if err != nil {
		return "", "", nil, err
	}
	var allTxs []Tx
	for offset := 0; ; offset += 25 {
		body, err := r.getFromEndpoint(ctx, endpoint, fmt.Sprintf("/block/%s/txs/%d", blockHash, offset))
		if err != nil {
			if isBitcoinTxPageEOF(err) {
				return blockHash, parentHash, allTxs, nil
			}
			return "", "", nil, err
		}

		var txs []Tx
		if err := json.Unmarshal(body, &txs); err != nil {
			return "", "", nil, err
		}
		if len(txs) == 0 {
			return blockHash, parentHash, allTxs, nil
		}
		allTxs = append(allTxs, txs...)
		if len(txs) < 25 {
			return blockHash, parentHash, allTxs, nil
		}
	}
}

func (r *RpcListener) esploraBlockParentHash(ctx context.Context, endpoint bitcoinEndpoint, blockHash string) (string, error) {
	body, err := r.getFromEndpoint(ctx, endpoint, fmt.Sprintf("/block/%s", blockHash))
	if err != nil {
		return "", err
	}
	var info BlockInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return "", fmt.Errorf("decode bitcoin block metadata %s: %w", blockHash, err)
	}
	if id := strings.TrimSpace(info.ID); id != "" && !strings.EqualFold(id, blockHash) {
		return "", fmt.Errorf("bitcoin block metadata hash mismatch: height hash %s metadata %s", blockHash, id)
	}
	return strings.TrimSpace(info.PreviousBlockHash), nil
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
		allTxs = append(allTxs, txs...)
		if len(txs) == 0 || len(txs) < pageSize {
			return blockHash, parentHash, allTxs, nil
		}
		if total > 0 && cursor+len(txs) >= total {
			return blockHash, parentHash, allTxs, nil
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
		if nested, ok := firstRawObject(vinObj, "prevout", "prevOut", "prev_output", "prevOutput", "utxo"); ok {
			prevObj = nested
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
		tx.Vout = append(tx.Vout, output)
	}
	return tx, nil
}

func (r *RpcListener) validateBlockContinuity(height int64, blockHash, parentHash string) error {
	if err := listenerconfig.ValidateParentContinuity(r.chainState, height, parentHash); err != nil {
		listenerconfig.RewindParentContinuityCheckpoint(r.chainState, height)
		if r.stateWriter != nil {
			if writeErr := r.stateWriter(r.chainState); writeErr != nil {
				return fmt.Errorf("write bitcoin chain rollback state: %w", writeErr)
			}
		}
		return err
	}
	if strings.TrimSpace(blockHash) == "" {
		return fmt.Errorf("empty bitcoin block hash for height %d", height)
	}
	return nil
}

func isBitcoinTxPageEOF(err error) bool {
	var apiErr *bitcoinAPIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.statusCode == http.StatusNotFound &&
		strings.Contains(strings.ToLower(apiErr.body), "start index out of range")
}

func (r *RpcListener) handleTx(height int64, blockHash string, nativeAsset asset.Asset, tx Tx) error {
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
		if vin.Prevout != nil {
			input.Prevout.Address = bitcoinCoreAddress(vin.Prevout.ScriptPubKey)
			value, err := bitcoinValueToSats(vin.Prevout.Value)
			if err != nil {
				return Tx{}, fmt.Errorf("convert bitcoin core vin value: %w", err)
			}
			input.Prevout.Value = value
		}
		tx.Vin = append(tx.Vin, input)
	}
	for _, vout := range coreTx.Vout {
		value, err := bitcoinValueToSats(vout.Value)
		if err != nil {
			return Tx{}, fmt.Errorf("convert bitcoin core vout value: %w", err)
		}
		tx.Vout = append(tx.Vout, Vout{
			Address:          bitcoinCoreAddress(vout.ScriptPubKey),
			Value:            value,
			ScriptPubKey:     vout.ScriptPubKey.Hex,
			ScriptPubKeyASM:  vout.ScriptPubKey.ASM,
			ScriptPubKeyType: vout.ScriptPubKey.Type,
		})
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
