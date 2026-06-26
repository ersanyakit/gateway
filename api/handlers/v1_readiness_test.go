package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"core/types"
)

func TestV1ProbeEVMRPC(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req["method"] != "eth_blockNumber" {
			t.Fatalf("method = %v, want eth_blockNumber", req["method"])
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": req["id"], "result": "0x10"})
	}))
	defer server.Close()

	details, err := v1ProbeEVMRPC(context.Background(), []string{server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(details, "16") {
		t.Fatalf("details = %q, want latest block 16", details)
	}
}

func TestV1ProbeSolanaRPC(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req["method"] != "getSlot" {
			t.Fatalf("method = %v, want getSlot", req["method"])
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": req["id"], "result": 12345})
	}))
	defer server.Close()

	details, err := v1ProbeSolanaRPC(context.Background(), []string{server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(details, "12345") {
		t.Fatalf("details = %q, want latest slot 12345", details)
	}
}

func TestV1ProbeBitcoinREST(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/blocks/tip/height" {
			t.Fatalf("path = %q, want /blocks/tip/height", r.URL.Path)
		}
		_, _ = w.Write([]byte("850000"))
	}))
	defer server.Close()

	details, err := v1ProbeBitcoinREST(context.Background(), []string{server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(details, "850000") {
		t.Fatalf("details = %q, want latest height 850000", details)
	}
}

func TestV1ReadinessOK(t *testing.T) {
	if v1ReadinessOK(nil) {
		t.Fatal("empty checks should not be ready")
	}
	if !v1ReadinessOK([]types.V1ReadinessCheck{{Name: "database", OK: true}}) {
		t.Fatal("all passing checks should be ready")
	}
	if v1ReadinessOK([]types.V1ReadinessCheck{{Name: "database", OK: true}, {Name: "rpc", OK: false}}) {
		t.Fatal("failing check should not be ready")
	}
}
