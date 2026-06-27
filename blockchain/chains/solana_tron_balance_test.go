package chains

import (
	"net/http"
	"net/http/httptest"
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
	got, err := chain.getBalance(second.Client(), "11111111111111111111111111111111")
	if err != nil {
		t.Fatal(err)
	}
	if got != "12345" {
		t.Fatalf("balance = %q, want 12345", got)
	}
}

func TestTronGetBalanceFallsBackToNextRPC(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"error":{"message":"down"}}`))
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":"0x2a"}`))
	}))
	defer second.Close()

	chain := NewTronChain()
	chain.RPCHttp = []string{first.URL, second.URL}
	got, err := chain.getBalance(second.Client(), "T0000000000000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if got != "0x2a" {
		t.Fatalf("balance = %q, want 0x2a", got)
	}
}
