package ethereum

import (
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

	// Örnek subscribe request (Ethereum eth_subscribe "newPendingTransactions")
	subscribeMsg := `{
        "id": 1,
        "method": "eth_subscribe",
        "params": ["newPendingTransactions"]
    }`

	if err := r.conn.WriteMessage(websocket.TextMessage, []byte(subscribeMsg)); err != nil {
		return err
	}

	go r.readLoop()

	return nil
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
			r.events <- string(msg) // Basitçe JSON mesajını ilettik, işlenebilir
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
