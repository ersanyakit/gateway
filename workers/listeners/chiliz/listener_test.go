package chiliz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"core/blockchain"
	"core/constants"
	"core/models"

	"github.com/gorilla/websocket"
)

func TestConnectFallsBackToNextWebsocket(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer bad.Close()

	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		_ = conn.Close()
	}))
	defer good.Close()

	listener := &RpcListener{
		chain: testWSChain{ws: []string{"", strings.Replace(bad.URL, "http://", "ws://", 1), strings.Replace(good.URL, "http://", "ws://", 1)}},
	}

	if err := listener.connect(); err != nil {
		t.Fatalf("connect returned error: %v", err)
	}
	if listener.conn == nil {
		t.Fatal("connect returned nil conn")
	}
	_ = listener.conn.Close()
}

type testWSChain struct {
	ws []string
}

func (t testWSChain) ChainID() constants.ChainID { return constants.Chiliz }
func (t testWSChain) Name() string               { return "chiliz" }
func (t testWSChain) WSS() []string              { return t.ws }
func (t testWSChain) RPCs() []string             { return nil }
func (t testWSChain) Create(context.Context) (*blockchain.WalletDetails, error) {
	return nil, nil
}
func (t testWSChain) CreateHDWallet(context.Context, int, int) (*blockchain.WalletDetails, error) {
	return nil, nil
}
func (t testWSChain) Deposit(context.Context, blockchain.WalletDetails, string, string) (*blockchain.TransactionResult, error) {
	return nil, nil
}
func (t testWSChain) Withdraw(context.Context, blockchain.WalletDetails, string, string) (*blockchain.TransactionResult, error) {
	return nil, nil
}
func (t testWSChain) WithdrawToken(context.Context, blockchain.WalletDetails, string, string, string) (*blockchain.TransactionResult, error) {
	return nil, nil
}
func (t testWSChain) Sweep(context.Context, blockchain.WalletDetails) (*blockchain.TransactionResult, error) {
	return nil, nil
}
func (t testWSChain) SweepTo(context.Context, blockchain.WalletDetails, string) (*blockchain.TransactionResult, error) {
	return nil, nil
}
func (t testWSChain) SweepERC20To(context.Context, blockchain.WalletDetails, string, string) (*blockchain.TransactionResult, error) {
	return nil, nil
}
func (t testWSChain) PrefundGas(context.Context, blockchain.WalletDetails, string) (bool, error) {
	return false, nil
}
func (t testWSChain) ValidateAddress(string) bool { return false }
func (t testWSChain) AddWorker(blockchain.Worker) error {
	return nil
}
func (t testWSChain) RemoveWorker(blockchain.Worker) error {
	return nil
}
func (t testWSChain) WorkerCount() int { return 0 }
func (t testWSChain) BatchBalances(context.Context, []string, int) []models.BalanceResult {
	return nil
}
func (t testWSChain) StartWorkers(context.Context) error { return nil }
func (t testWSChain) StopWorkers() error                 { return nil }
