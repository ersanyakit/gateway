package bitcoin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
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
		return fmt.Errorf("%s has no REST API configured", r.chain.Name())
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
	latest, err := r.latestHeight()
	if err != nil {
		return err
	}
	confirmedHead := latest
	safeLatest := latest - safeBlockConfirmations
	if safeLatest <= 0 {
		return nil
	}

	from := r.chainState.LastProcessedBlock + 1
	configuredStart := false
	if r.chainState.LastProcessedBlock <= 0 {
		if configured, ok := listenerconfig.ConfiguredStartBlock(r.chain); ok {
			from = configured
			configuredStart = true
		}
	}
	if from <= 1 && !configuredStart {
		from = safeLatest
	}
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
		r.chainState.LastProcessedBlock = height
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
	body, err := r.get("/blocks/tip/height")
	if err != nil {
		return 0, err
	}

	height, err := strconv.ParseInt(strings.TrimSpace(string(body)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse latest bitcoin height: %w", err)
	}
	return height, nil
}

func (r *RpcListener) get(path string) ([]byte, error) {
	var lastErr error
	for _, baseURL := range r.chain.RPCs() {
		req, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+path, nil)
		if err != nil {
			lastErr = err
			continue
		}

		resp, err := r.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("bitcoin API returned HTTP %d: %s", resp.StatusCode, string(body))
			continue
		}

		return body, nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("no bitcoin API endpoint configured")
	}
	return nil, lastErr
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
	Address string `json:"scriptpubkey_address"`
	Value   int64  `json:"value"`
}

func (r *RpcListener) processBlock(height int64) error {
	hashBody, err := r.get(fmt.Sprintf("/block-height/%d", height))
	if err != nil {
		return err
	}
	blockHash := strings.TrimSpace(string(hashBody))
	if blockHash == "" {
		return fmt.Errorf("empty block hash for bitcoin height %d", height)
	}

	nativeAsset, ok := r.registry.GetNative(r.chain.ChainID())
	if !ok {
		return fmt.Errorf("native asset is not registered")
	}

	for offset := 0; ; offset += 25 {
		body, err := r.get(fmt.Sprintf("/block/%s/txs/%d", blockHash, offset))
		if err != nil {
			return err
		}

		var txs []Tx
		if err := json.Unmarshal(body, &txs); err != nil {
			return err
		}
		if len(txs) == 0 {
			return nil
		}

		for _, tx := range txs {
			if err := r.handleTx(height, blockHash, nativeAsset, tx); err != nil {
				return err
			}
		}

		if len(txs) < 25 {
			return nil
		}
	}
}

func (r *RpcListener) handleTx(height int64, blockHash string, nativeAsset asset.Asset, tx Tx) error {
	from := "coinbase"
	for _, input := range tx.Vin {
		if input.Prevout.Address != "" {
			from = input.Prevout.Address
			break
		}
	}

	status := "confirmed"
	if !tx.Status.Confirmed {
		status = "pending"
	}

	for idx, output := range tx.Vout {
		if output.Address == "" || output.Value <= 0 {
			continue
		}

		txParam := &types.TransactionParam{
			Context:   context.Background(),
			ChainID:   r.chain.ChainID(),
			Symbol:    helpers.StrPtr(nativeAsset.GetSymbol()),
			Decimals:  nativeAsset.GetDecimals(),
			Hash:      helpers.StrPtr(tx.TxID),
			Block:     helpers.StrPtr(fmt.Sprintf("%d", height)),
			BlockHash: helpers.StrPtr(blockHash),
			Token:     nil,
			From:      helpers.StrPtr(from),
			To:        helpers.StrPtr(output.Address),
			Amount:    helpers.StrPtr(fmt.Sprintf("%d", output.Value)),
			LogIndex:  helpers.StrPtr(fmt.Sprintf("vout:%d", idx)),
			Status:    helpers.StrPtr(status),
		}

		if err := r.dispatch("utxo_transfer", txParam); err != nil {
			return err
		}
	}

	return nil
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
