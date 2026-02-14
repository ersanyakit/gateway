package ethereum

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/gorilla/websocket"
)

type RpcListener struct {
	wsURL   string
	conn    *websocket.Conn
	events  chan interface{}
	quit    chan struct{}
	running bool
}

func NewRpcListener(wsURL string) *RpcListener {
	return &RpcListener{
		wsURL:  wsURL,
		events: make(chan interface{}, 100),
		quit:   make(chan struct{}),
	}
}

func (r *RpcListener) Start() error {
	if r.running {
		return fmt.Errorf("listener already running")
	}

	c, _, err := websocket.DefaultDialer.Dial(r.wsURL, nil)
	if err != nil {
		return err
	}

	r.conn = c
	r.running = true

	subscribeMsg := `{
		"id": 1,
		"method": "eth_subscribe",
		"params": ["newHeads"]
	}`

	if err := r.conn.WriteMessage(websocket.TextMessage, []byte(subscribeMsg)); err != nil {
		return err
	}

	go r.readLoop()

	return nil
}

type SubscriptionResponse struct {
	Method string `json:"method"`
	Params struct {
		Result struct {
			Hash   string `json:"hash"`
			Number string `json:"number"`
		} `json:"result"`
	} `json:"params"`
}

type BlockResponse struct {
	ID     int `json:"id"`
	Result struct {
		Number       string        `json:"number"`
		Hash         string        `json:"hash"`
		Transactions []Transaction `json:"transactions"`
	} `json:"result"`
}

type Transaction struct {
	Hash  string `json:"hash"`
	From  string `json:"from"`
	To    string `json:"to"`
	Value string `json:"value"`
	Input string `json:"input"`
}

func (r *RpcListener) readLoop() {
	for {
		select {
		case <-r.quit:
			return
		default:
			_, msg, err := r.conn.ReadMessage()
			if err != nil {
				log.Println("read error:", err)
				r.Stop()
				return
			}

			var subResp SubscriptionResponse
			if err := json.Unmarshal(msg, &subResp); err == nil && subResp.Method == "eth_subscription" {
				blockHash := subResp.Params.Result.Hash

				getBlockMsg := fmt.Sprintf(`{
					"id": 2,
					"method": "eth_getBlockByHash",
					"params": ["%s", true]
				}`, blockHash)

				r.conn.WriteMessage(websocket.TextMessage, []byte(getBlockMsg))
				continue
			}

			var blockResp BlockResponse
			if err := json.Unmarshal(msg, &blockResp); err == nil && blockResp.Result.Hash != "" {

				fmt.Println("New Block:", blockResp.Result.Number)

				for _, tx := range blockResp.Result.Transactions {

					// ETH transfer kontrol
					if tx.Value != "0x0" && tx.To != "" {
						fmt.Println("ETH Transfer")
						fmt.Println("From:", tx.From)
						fmt.Println("To:", tx.To)
						fmt.Println("Value:", tx.Value)
						fmt.Println("Hash:", tx.Hash)
					}

					// ERC20 transfer kontrol (input method id)
					if len(tx.Input) >= 10 && tx.Input[:10] == "0xa9059cbb" {
						fmt.Println("ERC20 Transfer detected")
						fmt.Println("Contract:", tx.To)
						fmt.Println("TxHash:", tx.Hash)

					}
				}

				continue
			}

			// Diğer mesajlar
			r.events <- string(msg)
		}
	}
}

func (r *RpcListener) readLoopWe() {
	for {
		select {
		case <-r.quit:
			return
		default:
			_, msg, err := r.conn.ReadMessage()
			if err != nil {
				log.Println("read error:", err)
				r.Stop()
				return
			}

			var subResp SubscriptionResponse
			if err := json.Unmarshal(msg, &subResp); err == nil && subResp.Method == "eth_subscription" {
				blockHash := subResp.Params.Result.Hash

				getBlockMsg := fmt.Sprintf(`{
					"id": 2,
					"method": "eth_getBlockByHash",
					"params": ["%s", true]
				}`, blockHash)

				if err := r.conn.WriteMessage(websocket.TextMessage, []byte(getBlockMsg)); err != nil {
					log.Println("block request error:", err)
					continue
				}
				continue
			}

			r.events <- string(msg)
		}
	}
}

func (r *RpcListener) readLoopEx() {
	for {
		select {
		case <-r.quit:
			return
		default:
			_, msg, err := r.conn.ReadMessage()
			if err != nil {
				log.Println("read error:", err)
				r.Stop()
				return
			}
			r.events <- string(msg)
		}
	}
}

func (r *RpcListener) Stop() error {
	if !r.running {
		return fmt.Errorf("listener not running")
	}
	close(r.quit)
	r.running = false
	return r.conn.Close()
}

func (r *RpcListener) Events() <-chan interface{} {
	return r.events
}
