package ethereum

import (
	"core/asset"
	"core/blockchain"
	"core/models"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gorilla/websocket"
)

var TransferEventHash = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)")).Hex()

type RpcListener struct {
	chain       blockchain.Chain
	registry    *asset.Registry
	chainState  *models.ChainState
	stateWriter func(*models.ChainState) error

	conn      *websocket.Conn
	mu        sync.Mutex
	callbacks map[int]func(json.RawMessage)
	nextID    int
	quit      chan struct{}
	running   bool
	events    chan interface{}
}

func NewRpcListener(chain blockchain.Chain, registry *asset.Registry, state *models.ChainState, stateWriter func(*models.ChainState) error) *RpcListener {
	return &RpcListener{
		chain:       chain,
		registry:    registry,
		chainState:  state,
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

type JsonRpcMessage struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Params struct {
		Result json.RawMessage `json:"result"`
	} `json:"params"`
}

type ERC20Log struct {
	Address          string   `json:"address"`
	Topics           []string `json:"topics"`
	Data             string   `json:"data"`
	TransactionHash  string   `json:"transactionHash"`
	BlockNumber      string   `json:"blockNumber"`
	LogIndex         string   `json:"logIndex"`         // yeni
	TransactionIndex string   `json:"transactionIndex"` // isteğe bağlı
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
	b, _ := json.Marshal(req)
	_ = r.conn.WriteMessage(websocket.TextMessage, b)
}

func (r *RpcListener) readLoop() {
	for {
		select {
		case <-r.quit:
			return
		default:
			_, msg, err := r.conn.ReadMessage()
			if err != nil {
				log.Println("Read error, reconnecting:", err)
				r.reconnect()
				continue
			}

			var rpcMsg JsonRpcMessage
			if err := json.Unmarshal(msg, &rpcMsg); err != nil {
				continue
			}

			if rpcMsg.Method == "eth_subscription" {
				var logEntry ERC20Log
				if err := json.Unmarshal(rpcMsg.Params.Result, &logEntry); err == nil {
					r.handleERC20Log(logEntry)
				}
				fmt.Println("logEntry", logEntry.TransactionIndex, logEntry.LogIndex)
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

func (r *RpcListener) handleERC20Log(l ERC20Log) {
	if len(l.Topics) < 3 {
		return
	}

	token := common.HexToAddress(l.Address)
	from := common.BytesToAddress(common.HexToHash(l.Topics[1]).Bytes()[12:])
	to := common.BytesToAddress(common.HexToHash(l.Topics[2]).Bytes()[12:])

	value := big.NewInt(0)
	if l.Data != "" && l.Data != "0x" {
		if b, err := hexutil.Decode(l.Data); err == nil {
			value.SetBytes(b)
		}
	}

	isRegistered := false
	if r.registry != nil {
		_, isRegistered = r.registry.Get(r.chain.ChainID(), strings.ToLower(token.Hex()))
	}

	r.events <- map[string]interface{}{
		"chain": r.chain.ChainID(),
		"type":  "erc20",
		"from":  from.Hex(),
		"to":    to.Hex(),
		"token": token.Hex(),
		"value": value.String(),
		"tx":    l.TransactionHash,
		"block": l.BlockNumber,
	}

	fmt.Printf("🔵 [ERC20] %s -> %s | Amount: %s | Token: %s | Registered: %v | Tx: %s | Block: %s\n",
		from.Hex(), to.Hex(), value.String(), token.Hex(), isRegistered, l.TransactionHash, l.BlockNumber)
}

func (r *RpcListener) Events() <-chan interface{} {
	return r.events
}

func (r *RpcListener) Stop() error {
	if !r.running {
		return fmt.Errorf("listener not running")
	}
	close(r.quit)
	r.running = false
	if r.conn != nil {
		return r.conn.Close()
	}
	return nil
}

func (r *RpcListener) reconnect() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conn != nil {
		_ = r.conn.Close()
	}
	for {
		select {
		case <-r.quit:
			return
		default:
			if err := r.connect(); err == nil {
				log.Println("Reconnected successfully")
				go r.subscribeTransfers()
				return
			}
			log.Println("Reconnect failed, retrying in 3s")
			time.Sleep(3 * time.Second)
		}
	}
}
