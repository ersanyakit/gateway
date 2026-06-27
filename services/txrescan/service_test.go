package txrescan

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"core/asset"
	chainpkg "core/blockchain/chains"
	"core/constants"
)

func TestBitcoinGetAnnotatesEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("bad gateway"))
	}))
	defer server.Close()

	svc := &Service{client: server.Client()}
	chain := chainpkg.NewBitcoinChain()
	chain.RPCHttp = []string{server.URL}

	_, err := svc.bitcoinGet(context.Background(), chain, "/tx/abc")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), server.URL) {
		t.Fatalf("error %q does not include endpoint %q", err.Error(), server.URL)
	}
}

func TestSolanaRPCAnnotatesEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("bad gateway"))
	}))
	defer server.Close()

	svc := &Service{client: server.Client()}
	chain := chainpkg.NewSolanaChain()
	chain.RPCHttp = []string{server.URL}

	var out any
	err := svc.solanaRPC(context.Background(), chain, "getTransaction", []any{"abc"}, &out)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), server.URL) {
		t.Fatalf("error %q does not include endpoint %q", err.Error(), server.URL)
	}
}

func TestTronPostAnnotatesEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("bad gateway"))
	}))
	defer server.Close()

	t.Setenv("TRON_HTTP_ENDPOINTS", server.URL)

	svc := &Service{client: server.Client()}
	_, err := svc.tronPost(context.Background(), "/wallet/gettransactionbyid", map[string]string{"value": "abc"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), server.URL) {
		t.Fatalf("error %q does not include endpoint %q", err.Error(), server.URL)
	}
}

func TestScanTronProducesNativeTRXTransfer(t *testing.T) {
	const txHash = "abc123"
	ownerHex := "410000000000000000000000000000000000000001"
	toHex := "410000000000000000000000000000000000000002"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/wallet/gettransactionbyid":
			_, _ = fmt.Fprintf(w, `{
				"txID": %q,
				"raw_data": {
					"contract": [{
						"type": "TransferContract",
						"parameter": {
							"value": {
								"amount": 1000000,
								"owner_address": %q,
								"to_address": %q
							}
						}
					}]
				}
			}`, txHash, ownerHex, toHex)
		case "/wallet/gettransactioninfobyid":
			_, _ = w.Write([]byte(`{"blockNumber": 123456, "receipt": {"result": "SUCCESS"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("TRON_HTTP_ENDPOINTS", server.URL)

	registry := asset.NewRegistry()
	registry.Register(asset.NewTRX(constants.TRON))
	svc := &Service{Registry: registry, client: server.Client()}

	events, err := svc.scanTron(context.Background(), txHash)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	event := events[0]
	if event.Type != "native_transfer" {
		t.Fatalf("type = %q, want native_transfer", event.Type)
	}
	if event.Tx == nil {
		t.Fatal("event tx is nil")
	}
	if got := *event.Tx.Symbol; got != "TRX" {
		t.Fatalf("symbol = %q, want TRX", got)
	}
	if got := *event.Tx.Amount; got != "1000000" {
		t.Fatalf("amount = %q, want 1000000", got)
	}
	if got := *event.Tx.LogIndex; got != "tx:0" {
		t.Fatalf("log index = %q, want tx:0", got)
	}
	if got := *event.Tx.To; got != tronHexToBase58(toHex) {
		t.Fatalf("to = %q, want %q", got, tronHexToBase58(toHex))
	}
	if got := *event.Tx.From; got != tronHexToBase58(ownerHex) {
		t.Fatalf("from = %q, want %q", got, tronHexToBase58(ownerHex))
	}
	if event.Tx.Decimals != 6 {
		t.Fatalf("decimals = %d, want 6", event.Tx.Decimals)
	}
}
