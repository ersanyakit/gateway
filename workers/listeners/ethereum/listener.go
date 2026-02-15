package ethereum

import (
	"core/asset"
	"core/blockchain"
	"encoding/json"
	"fmt"
	"log"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gorilla/websocket"
)

// ERC20 Transfer Event Topic (keccak256("Transfer(address,address,uint256)"))

var TransferEventHash = crypto.Keccak256Hash(
	[]byte("Transfer(address,address,uint256)"),
).Hex()

type RpcListener struct {
	chain    blockchain.Chain
	registry *asset.Registry

	conn    *websocket.Conn
	events  chan interface{}
	quit    chan struct{}
	running bool
}

func NewRpcListener(chain blockchain.Chain, registry *asset.Registry) *RpcListener {
	return &RpcListener{
		chain:    chain,
		registry: registry,
		events:   make(chan interface{}, 100),
		quit:     make(chan struct{}),
	}
}

func (r *RpcListener) Start() error {
	if r.running {
		return fmt.Errorf("listener already running")
	}

	c, _, err := websocket.DefaultDialer.Dial(r.chain.WSS()[0], nil)
	if err != nil {
		return err
	}

	r.conn = c
	r.running = true

	subscribeHeads := `{
		"id": 1,
		"method": "eth_subscribe",
		"params": ["newHeads"]
	}`
	if err := r.conn.WriteMessage(websocket.TextMessage, []byte(subscribeHeads)); err != nil {
		return err
	}

	subscribeLogs := fmt.Sprintf(`{
	"id": 2,
	"method": "eth_subscribe",
	"params": ["logs", {"topics": ["%s"]}]
}`, TransferEventHash)

	if err := r.conn.WriteMessage(websocket.TextMessage, []byte(subscribeLogs)); err != nil {
		return err
	}

	go r.readLoop()

	return nil
}

type JsonRpcMessage struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"` // Doğrudan çağrı cevabı (örn: eth_getBlockByHash)
	Params struct {
		Result json.RawMessage `json:"result"` // Abonelik bildirimi (örn: eth_subscription)
	} `json:"params"`
}

// eth_subscribe("newHeads") Gelen Veri
type NewHeadResult struct {
	Hash   string `json:"hash"`
	Number string `json:"number"`
}

type LogResult struct {
	Address         string   `json:"address"`
	Topics          []string `json:"topics"`
	Data            string   `json:"data"`
	TransactionHash string   `json:"transactionHash"`
	BlockNumber     string   `json:"blockNumber"`
	Removed         bool     `json:"removed"`
}

type BlockResult struct {
	Number       string        `json:"number"`
	Hash         string        `json:"hash"`
	Transactions []Transaction `json:"transactions"`
}

type Transaction struct {
	Hash  string `json:"hash"`
	From  string `json:"from"`
	To    string `json:"to"`
	Value string `json:"value"`
	Input string `json:"input"`
}

// --- Logic ---

func (r *RpcListener) readLoop() {
	defer r.Stop()

	for {
		select {
		case <-r.quit:
			return
		default:
			_, msg, err := r.conn.ReadMessage()
			if err != nil {
				log.Println("read error:", err)
				return
			}

			var rpcMsg JsonRpcMessage
			if err := json.Unmarshal(msg, &rpcMsg); err != nil {
				continue
			}

			// A) Abonelik Bildirimleri (Notification)
			if rpcMsg.Method == "eth_subscription" {

				// İçeriğin ne olduğunu anlamak için önce Log mu diye bakıyoruz (Topic var mı?)
				var logCheck LogResult
				if err := json.Unmarshal(rpcMsg.Params.Result, &logCheck); err == nil && len(logCheck.Topics) > 0 {
					// >>> ERC20 TRANSFER LOG <<<
					r.handleERC20Log(logCheck)
					continue
				}

				// Log değilse Blok Başlığı mı diye bakıyoruz
				var headCheck NewHeadResult
				if err := json.Unmarshal(rpcMsg.Params.Result, &headCheck); err == nil && headCheck.Number != "" {
					// >>> NEW BLOCK HEADER <<<
					// ETH transactionları için bloğun tamamını istiyoruz
					r.fetchBlock(headCheck.Hash)
					continue
				}
			}

			// B) İsteğe Bağlı Cevaplar (Response)
			// fetchBlock fonksiyonunda ID olarak 100 veriyoruz
			if rpcMsg.ID == 100 {
				var blockResp BlockResult
				if err := json.Unmarshal(rpcMsg.Result, &blockResp); err == nil {
					r.processBlockTransactions(blockResp)
				}
			}

			// Ham mesajı da kanala iletiyoruz (isteğe bağlı)
			// r.events <- string(msg)
		}
	}
}

// ETH Transferlerini analiz etmek için bloğu çeker
func (r *RpcListener) fetchBlock(blockHash string) {
	// full tx objelerini almak için ikinci parametre true olmalı
	req := fmt.Sprintf(`{
		"id": 100,
		"method": "eth_getBlockByHash",
		"params": ["%s", true]
	}`, blockHash)

	r.conn.WriteMessage(websocket.TextMessage, []byte(req))
}

// ERC20 Transferlerini İşler (Internal Dahil)
func (r *RpcListener) handleERC20Log(l LogResult) {
	// Transfer eventi imzası kontrolü

	fmt.Println("CODER,ERC20:handleERC20Log", l.TransactionHash)
	if len(l.Topics) < 3 || l.Topics[0] != TransferEventHash {
		fmt.Println("RETURNED RETURNED RETURN")
		return
	}

	tokenContract := common.HexToAddress(l.Address)
	fromAddress := common.HexToAddress(l.Topics[1])
	toAddress := common.HexToAddress(l.Topics[2])

	data := common.FromHex(l.Data)
	if len(data) != 32 {
		fmt.Println("INVALID DATA LENGTH:", len(data))
		return
	}

	//testUSDT, usdtFound := assetRegistry.Get(ethChain.ChainID(), "0xdAC17F958D2ee523a2206206994597C13D831ec7")
	//if usdtFound {
	//		fmt.Println("ERSAN", testUSDT.GetName(), testUSDT.GetSymbol())
	//}

	value := new(big.Int).SetBytes(data)

	fmt.Printf("🔵 [ERC20] %s -> %s | Amount: %s | Token: %s | Tx: %s\n",
		fromAddress.Hex(),
		toAddress.Hex(),
		value.String(),
		tokenContract.Hex(),
		l.TransactionHash,
	)
}

// Native ETH Transferlerini İşler
func (r *RpcListener) processBlockTransactions(block BlockResult) {
	// Block Number Decode
	blockNum, _ := hexutil.DecodeBig(block.Number)

	fmt.Printf("Processing Block #%s (%d txs)\n", blockNum.String(), len(block.Transactions))

	for _, tx := range block.Transactions {
		// Value Decode
		valBig, err := hexutil.DecodeBig(tx.Value)
		if err != nil {
			continue
		}

		// 0 ETH üzerindeki işlemleri kontrol et
		if valBig.Sign() > 0 {

			// From ve To adreslerini temizle (Lower case çevir veya checksum yap)
			from := common.HexToAddress(tx.From)
			to := common.HexToAddress(tx.To)

			// Basit bir loglama
			fmt.Printf("🟢 [ETH]   %s -> %s | Amount: %s Wei | Tx: %s\n",
				from.Hex(),
				to.Hex(),
				valBig.String(),
				tx.Hash,
			)

			// Eğer input data doluysa (örneğin bir smart contract fonksiyonuna ETH gönderildiyse)
			if len(tx.Input) > 2 {
				// Burada method ID kontrolü yapılabilir.
				// fmt.Println("   -> (Contract Call with ETH Value)")
			}
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
