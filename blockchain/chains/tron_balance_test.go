package chains

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTronGetBalanceFallsBackToNextRPC(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"Error":"down"}`))
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"balance":42}`))
	}))
	defer second.Close()

	chain := NewTronChain()
	chain.RPCHttp = []string{first.URL, second.URL}
	got, err := chain.getBalance(context.Background(), second.Client(), tronTestAddress)
	if err != nil {
		t.Fatal(err)
	}
	if got != "TRX:42" {
		t.Fatalf("balance = %q, want TRX:42", got)
	}
}

func TestTronGetBalanceAnnotatesFailingRPC(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`bad gateway`))
	}))
	defer first.Close()

	chain := NewTronChain()
	chain.RPCHttp = []string{first.URL}

	_, err := chain.getBalance(context.Background(), first.Client(), tronTestAddress)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), first.URL) {
		t.Fatalf("error %q does not include endpoint %q", err.Error(), first.URL)
	}
}

func TestTronBatchBalancesUsesFullNodeGetAccount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wallet/getaccount" {
			t.Fatalf("path = %q, want /wallet/getaccount", r.URL.Path)
		}
		var payload struct {
			Address string `json:"address"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(payload.Address, "41") {
			t.Fatalf("address = %q, want tron hex address with 41 prefix", payload.Address)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"balance":18500000}`))
	}))
	defer server.Close()
	t.Setenv("TRON_HTTP_ENDPOINTS", server.URL)

	chain := NewTronChain()
	results := chain.BatchBalances(context.Background(), []string{tronTestAddress}, 1)
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].Error != nil {
		t.Fatal(results[0].Error)
	}
	if results[0].Balance != "TRX:18500000" {
		t.Fatalf("balance = %q, want TRX:18500000", results[0].Balance)
	}
}

func TestTronTestnetHTTPAPIEndpointsPreferTestnetEnv(t *testing.T) {
	t.Setenv("TRON_HTTP_ENDPOINTS", "https://mainnet.invalid")
	t.Setenv("TRON_TESTNET_HTTP_ENDPOINTS", "https://shasta.example")

	chain := NewTronTestnetChain()
	got := chain.httpAPIEndpoints()
	if len(got) == 0 {
		t.Fatal("expected testnet endpoints")
	}
	if got[0] != "https://shasta.example" {
		t.Fatalf("first endpoint = %q, want testnet endpoint", got[0])
	}
}

func TestTronTestnetTRXBalanceDoesNotUseMainnetHTTPEnv(t *testing.T) {
	var mainnetHits int32
	mainnet := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&mainnetHits, 1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer mainnet.Close()

	shasta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wallet/getaccount" {
			t.Fatalf("path = %q, want /wallet/getaccount", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"balance":42}`))
	}))
	defer shasta.Close()

	t.Setenv("TRON_HTTP_ENDPOINTS", mainnet.URL)
	t.Setenv("TRON_TESTNET_HTTP_ENDPOINTS", "")
	t.Setenv("TRON_TESTNET_HTTP_ENDPOINT", "")

	chain := NewTronTestnetChain()
	chain.RPCHttp = []string{shasta.URL + "/jsonrpc"}
	got, err := tronGetTRXBalanceFromRPCs(context.Background(), chain.httpAPIEndpoints(), tronTestAddress)
	if err != nil {
		t.Fatal(err)
	}
	if got != 42 {
		t.Fatalf("balance = %d, want 42", got)
	}
	if hits := atomic.LoadInt32(&mainnetHits); hits != 0 {
		t.Fatalf("mainnet endpoint was called %d times", hits)
	}
}
