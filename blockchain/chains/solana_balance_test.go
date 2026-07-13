package chains

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSolanaGetBalanceFallsBackToNextRPC(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"error":{"message":"down"}}`))
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":{"value":12345}}`))
	}))
	defer second.Close()

	chain := NewSolanaChain()
	chain.RPCHttp = []string{first.URL, second.URL}
	got, err := chain.getBalance(context.Background(), second.Client(), "11111111111111111111111111111111")
	if err != nil {
		t.Fatal(err)
	}
	if got != "12345" {
		t.Fatalf("balance = %q, want 12345", got)
	}
}

func TestSolanaBatchBalancesNormalizesZeroWorkers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":{"value":42}}`))
	}))
	defer server.Close()

	chain := NewSolanaChain()
	chain.RPCHttp = []string{server.URL}
	results := chain.BatchBalances(context.Background(), []string{"11111111111111111111111111111111"}, 0)
	if len(results) != 1 || results[0].Error != nil || results[0].Balance != "42" {
		t.Fatalf("results = %#v, want one successful balance", results)
	}
}

func TestSolanaGetBalanceAnnotatesFailingRPC(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`bad gateway`))
	}))
	defer first.Close()

	chain := NewSolanaChain()
	chain.RPCHttp = []string{first.URL}

	_, err := chain.getBalance(context.Background(), first.Client(), "11111111111111111111111111111111")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), first.URL) {
		t.Fatalf("error %q does not include endpoint %q", err.Error(), first.URL)
	}
}
