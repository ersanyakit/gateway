package realtime

import (
	"log"
	"strings"
	"sync"
	"time"

	"github.com/fasthttp/websocket"
)

type PaymentEvent struct {
	Event        string `json:"event"`
	Status       string `json:"status"`
	Paid         bool   `json:"paid"`
	SessionToken string `json:"session_token"`
	PaymentID    string `json:"payment_id"`
	TxHash       string `json:"tx_hash,omitempty"`
	SuccessPath  string `json:"success_path,omitempty"`
	ResultPath   string `json:"result_path,omitempty"`
	CancelPath   string `json:"cancel_path,omitempty"`
	Payable      bool   `json:"payable"`
	Terminal     bool   `json:"terminal"`
	UpdatedAt    int64  `json:"updated_at"`
}

type paymentClient struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

type PaymentHub struct {
	mu    sync.RWMutex
	rooms map[string]map[*paymentClient]struct{}
}

func NewPaymentHub() *PaymentHub {
	return &PaymentHub{
		rooms: make(map[string]map[*paymentClient]struct{}),
	}
}

func (h *PaymentHub) Subscribe(token string, conn *websocket.Conn, initial PaymentEvent) {
	token = cleanToken(token)
	if conn == nil {
		log.Printf("payment realtime subscribe error=nil websocket connection")
		return
	}
	if token == "" {
		if err := conn.Close(); err != nil {
			log.Printf("payment realtime invalid-token connection close error=%v", err)
		}
		return
	}

	client := &paymentClient{conn: conn}
	h.add(token, client)
	defer h.remove(token, client)
	defer func() {
		if err := conn.Close(); err != nil {
			log.Printf("payment realtime connection close error=%v", err)
		}
	}()

	initial.Event = "payment.connected"
	initial.SessionToken = token
	if initial.Status == "" {
		initial.Status = "awaiting_payment"
	}
	if initial.SuccessPath == "" {
		initial.SuccessPath = "/checkout/" + token + "/return/success"
	}
	if initial.CancelPath == "" {
		initial.CancelPath = "/checkout/" + token + "/cancel"
	}
	if initial.UpdatedAt == 0 {
		initial.UpdatedAt = time.Now().UnixMilli()
	}
	if err := client.write(initial); err != nil {
		log.Printf("payment realtime initial write error=%v", err)
		return
	}

	conn.SetReadLimit(1024)
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (h *PaymentHub) Broadcast(token string, event PaymentEvent) {
	token = cleanToken(token)
	if token == "" {
		return
	}
	event.SessionToken = token
	if event.UpdatedAt == 0 {
		event.UpdatedAt = time.Now().UnixMilli()
	}

	clients := h.clients(token)
	for _, client := range clients {
		if err := client.write(event); err != nil {
			log.Printf("payment realtime broadcast write error=%v", err)
			h.remove(token, client)
			if closeErr := client.conn.Close(); closeErr != nil {
				log.Printf("payment realtime broadcast connection close error=%v", closeErr)
			}
		}
	}
}

func (h *PaymentHub) add(token string, client *paymentClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.rooms[token] == nil {
		h.rooms[token] = make(map[*paymentClient]struct{})
	}
	h.rooms[token][client] = struct{}{}
}

func (h *PaymentHub) remove(token string, client *paymentClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[token]
	if room == nil {
		return
	}
	delete(room, client)
	if len(room) == 0 {
		delete(h.rooms, token)
	}
}

func (h *PaymentHub) clients(token string) []*paymentClient {
	h.mu.RLock()
	defer h.mu.RUnlock()
	room := h.rooms[token]
	if len(room) == 0 {
		return nil
	}
	clients := make([]*paymentClient, 0, len(room))
	for client := range room {
		clients = append(clients, client)
	}
	return clients
}

func (c *paymentClient) write(event PaymentEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	return c.conn.WriteJSON(event)
}

func cleanToken(token string) string {
	return strings.TrimSpace(token)
}
