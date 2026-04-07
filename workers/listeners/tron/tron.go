package tron

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"strings"
	"sync"
	"time"

	"core/asset"
	"core/blockchain"
	"core/helpers"
	"core/models"
	"core/types"
	"core/workers/dispatcher"

	"github.com/btcsuite/btcd/btcutil/base58"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gorilla/websocket"
)

// TransferEventHash TRC20 ve ERC20 için tamamen aynıdır
var TransferEventHash = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)")).Hex()

type RpcListener struct {
	chain       blockchain.Chain
	registry    *asset.Registry
	chainState  *models.ChainState
	stateWriter func(*models.ChainState) error
	bus         *dispatcher.Dispatcher

	conn *websocket.Conn

	mu        sync.Mutex
	writeMu   sync.Mutex
	callbacks map[int]func(json.RawMessage)
	nextID    int
	quit      chan struct{}
	running   bool
	events    chan interface{}
}

func NewRpcListener(
	chain blockchain.Chain,
	registry *asset.Registry,
	state *models.ChainState,
	bus *dispatcher.Dispatcher,
	stateWriter func(*models.ChainState) error,
) *RpcListener {
	return &RpcListener{
		chain:       chain,
		registry:    registry,
		chainState:  state,
		bus:         bus,
		stateWriter: stateWriter,
		callbacks:   make(map[int]func(json.RawMessage)),
		quit:        make(chan struct{}),
		events:      make(chan interface{}, 100),
	}
}

func (r *RpcListener) Start() error {
	if r.running {
		return fmt.Errorf("listener already running")
	}

	if err := r.connect(); err != nil {
		return err
	}

	r.running = true

	go r.readLoop()
	go r.subscribeTransfers()
	go r.subscribeNewHeads()

	return nil
}

func (r *RpcListener) Stop() error {
	if !r.running {
		return fmt.Errorf("listener not running")
	}

	close(r.quit)
	r.running = false

	r.writeMu.Lock()
	defer r.writeMu.Unlock()

	if r.conn != nil {
		return r.conn.Close()
	}
	return nil
}

func (r *RpcListener) connect() error {
	c, _, err := websocket.DefaultDialer.Dial(r.chain.WSS()[0], nil)
	if err != nil {
		return err
	}
	r.conn = c
	return nil
}

func (r *RpcListener) writeJSON(v interface{}) error {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()

	if r.conn == nil {
		return fmt.Errorf("ws not connected")
	}

	b, err := json.Marshal(v)
	if err != nil {
		return err
	}

	return r.conn.WriteMessage(websocket.TextMessage, b)
}

type JsonRpcMessage struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Params struct {
		Result json.RawMessage `json:"result"`
	} `json:"params"`
}

// TRC20 Log yapısı (ERC20 ile birebir aynı EVM katmanında)
type TRC20Log struct {
	Address         string   `json:"address"`
	Topics          []string `json:"topics"`
	Data            string   `json:"data"`
	TransactionHash string   `json:"transactionHash"`
	BlockNumber     string   `json:"blockNumber"`
	LogIndex        string   `json:"logIndex"`
}

type BlockHeader struct {
	Number string `json:"number"`
}

type Block struct {
	Number       string  `json:"number"`
	Hash         string  `json:"hash"`
	Transactions []RawTx `json:"transactions"`
}

type RawTx struct {
	Hash  string `json:"hash"`
	From  string `json:"from"`
	To    string `json:"to"`
	Value string `json:"value"`
}

func (r *RpcListener) subscribeTransfers() {
	r.mu.Lock()
	id := r.nextID
	r.nextID++
	r.mu.Unlock()

	req := map[string]interface{}{
		"id":      id,
		"jsonrpc": "2.0",
		"method":  "eth_subscribe",
		"params":  []interface{}{"logs", map[string]interface{}{"topics": []string{TransferEventHash}}},
	}

	_ = r.writeJSON(req)
}

func (r *RpcListener) subscribeNewHeads() {
	r.mu.Lock()
	id := r.nextID
	r.nextID++
	r.mu.Unlock()

	req := map[string]interface{}{
		"id":      id,
		"jsonrpc": "2.0",
		"method":  "eth_subscribe",
		"params":  []interface{}{"newHeads"},
	}

	_ = r.writeJSON(req)
}

func (r *RpcListener) rpcCall(method string, params []interface{}) int {
	r.mu.Lock()
	id := r.nextID
	r.nextID++
	r.mu.Unlock()

	req := map[string]interface{}{
		"id":      id,
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}

	_ = r.writeJSON(req)
	return id
}

func (r *RpcListener) readLoop() {
	for {
		select {
		case <-r.quit:
			return
		default:
			_, msg, err := r.conn.ReadMessage()
			if err != nil {
				log.Println("read error, reconnecting:", err)
				r.reconnect()
				continue
			}

			var rpcMsg JsonRpcMessage
			if err := json.Unmarshal(msg, &rpcMsg); err != nil {
				continue
			}

			if rpcMsg.Method == "eth_subscription" {
				if len(rpcMsg.Params.Result) == 0 {
					continue
				}

				var logEntry TRC20Log
				if err := json.Unmarshal(rpcMsg.Params.Result, &logEntry); err == nil && logEntry.Address != "" {
					r.handleTRC20Log(logEntry)
					continue
				}

				var header BlockHeader
				if err := json.Unmarshal(rpcMsg.Params.Result, &header); err == nil && header.Number != "" {
					go r.handleNewBlock(header.Number)
					continue
				}
			}

			r.mu.Lock()
			if cb, ok := r.callbacks[rpcMsg.ID]; ok {
				cb(rpcMsg.Result)
				delete(r.callbacks, rpcMsg.ID)
			}
			r.mu.Unlock()
		}
	}
}

func (r *RpcListener) handleNewBlock(blockHex string) {
	reqID := r.rpcCall("eth_getBlockByNumber", []interface{}{blockHex, true})

	r.mu.Lock()
	r.callbacks[reqID] = func(result json.RawMessage) {
		var block Block
		if err := json.Unmarshal(result, &block); err != nil {
			return
		}

		blockNumber := hexToDec(block.Number)

		nativeAsset, ok := r.registry.GetNative(r.chain.ChainID())
		if !ok {
			return
		}

		for idx, tx := range block.Transactions {
			if tx.Value == "" || tx.Value == "0x0" || tx.To == "" {
				continue
			}

			value := big.NewInt(0)
			if b, err := hexutil.Decode(tx.Value); err == nil {
				value.SetBytes(b)
			}
			if value.Sign() == 0 {
				continue
			}

			// Tron'daki 0x EVM adreslerini Base58 (T...) formatına çeviriyoruz
			fromBase58 := evmToTronAddress(tx.From)
			toBase58 := evmToTronAddress(tx.To)

			txParam := &types.TransactionParam{
				Context:  context.Background(),
				ChainID:  r.chain.ChainID(),
				Symbol:   helpers.StrPtr(nativeAsset.GetSymbol()), // Örn: TRX
				Decimals: nativeAsset.GetDecimals(),               // Tron için genelde 6
				Hash:     helpers.StrPtr(tx.Hash),
				Block:    helpers.StrPtr(blockNumber),
				Token:    nil,
				From:     helpers.StrPtr(fromBase58),
				To:       helpers.StrPtr(toBase58),
				Amount:   helpers.StrPtr(value.String()),
				LogIndex: helpers.StrPtr(fmt.Sprintf("%d", idx)),
				Status:   helpers.StrPtr("pending"),
			}

			r.bus.Dispatch(dispatcher.Event{
				Chain:       r.chain.ChainID(),
				Type:        "transfer",
				Transaction: txParam,
			})
		}
	}
	r.mu.Unlock()
}

func (r *RpcListener) handleTRC20Log(l TRC20Log) {
	if len(l.Topics) < 3 {
		return
	}

	tokenHex := common.HexToAddress(l.Address).Hex()
	fromHex := common.BytesToAddress(common.HexToHash(l.Topics[1]).Bytes()[12:]).Hex()
	toHex := common.BytesToAddress(common.HexToHash(l.Topics[2]).Bytes()[12:]).Hex()

	// 0x... EVM adreslerini T... Base58 Tron adreslerine çevir
	tokenBase58 := evmToTronAddress(tokenHex)
	fromBase58 := evmToTronAddress(fromHex)
	toBase58 := evmToTronAddress(toHex)

	value := big.NewInt(0)
	if l.Data != "" && l.Data != "0x" {
		if b, err := hexutil.Decode(l.Data); err == nil {
			value.SetBytes(b)
		}
	}

	blockNumber := hexToDec(l.BlockNumber)
	logIndex := hexToDec(l.LogIndex)

	isRegistered := false
	var assetInfo asset.Asset
	if r.registry != nil {
		// Registry'de Tron adresi ile arama yapılıyor (örn: TXYZ...)
		assetInfo, isRegistered = r.registry.Get(r.chain.ChainID(), tokenBase58)
	}

	if !isRegistered {
		return
	}

	txParam := &types.TransactionParam{
		Context:  context.Background(),
		ChainID:  r.chain.ChainID(),
		Symbol:   helpers.StrPtr(assetInfo.GetSymbol()),
		Decimals: assetInfo.GetDecimals(),
		Hash:     helpers.StrPtr(l.TransactionHash),
		Block:    helpers.StrPtr(blockNumber),
		Token:    helpers.StrPtr(tokenBase58),
		From:     helpers.StrPtr(fromBase58),
		To:       helpers.StrPtr(toBase58),
		Amount:   helpers.StrPtr(value.String()),
		LogIndex: helpers.StrPtr(logIndex),
		Status:   helpers.StrPtr("pending"),
	}

	r.bus.Dispatch(dispatcher.Event{
		Chain:       r.chain.ChainID(),
		Type:        "transfer",
		Transaction: txParam,
	})
}

func (r *RpcListener) reconnect() {
	r.writeMu.Lock()
	if r.conn != nil {
		_ = r.conn.Close()
	}
	r.writeMu.Unlock()

	for {
		select {
		case <-r.quit:
			return
		default:
			if err := r.connect(); err == nil {
				log.Println("reconnected successfully to Tron RPC")
				go r.subscribeTransfers()
				go r.subscribeNewHeads()
				return
			}
			log.Println("reconnect failed, retrying in 3s")
			time.Sleep(3 * time.Second)
		}
	}
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

// evmToTronAddress: "0x..." EVM formatındaki adresi Base58Check "T..." formatına çevirir
func evmToTronAddress(evmHex string) string {
	evmHex = strings.TrimPrefix(evmHex, "0x")
	if len(evmHex) != 40 {
		return evmHex
	}

	// Tron adresleri EVM'de tutulurken "41" öneki silinir, gerçek Tron adresi oluşturmak için "41" ekliyoruz.
	tronHex := "41" + evmHex
	b, err := hex.DecodeString(tronHex)
	if err != nil {
		return evmHex
	}

	// Base58Check algoritması (Double SHA-256)
	h256 := sha256.New()
	h256.Write(b)
	hash1 := h256.Sum(nil)

	h256.Reset()
	h256.Write(hash1)
	hash2 := h256.Sum(nil)

	// Veriye Checksum (ilk 4 byte) eklenir
	b = append(b, hash2[:4]...)
	return base58.Encode(b)
}

func (r *RpcListener) Events() <-chan interface{} {
	return r.events
}
