package txrescan

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	chainpkg "core/blockchain/chains"
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
