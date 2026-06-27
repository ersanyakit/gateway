package chains

import (
	"context"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	tronTestAddress      = "TLa2f6VPqDgRE67v1736s7bJ8Ray5wYjU7"
	tronTestUSDTContract = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
)

func TestTronGetTRXBalanceFromRPCsFallsBack(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":{"code":-32000,"message":"down"}}`))
	}))
	defer bad.Close()

	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":"0x2a"}`))
	}))
	defer good.Close()

	got, err := tronGetTRXBalanceFromRPCs(context.Background(), []string{" ", bad.URL, good.URL}, tronTestAddress)
	if err != nil {
		t.Fatal(err)
	}
	if got != 42 {
		t.Fatalf("balance = %d, want 42", got)
	}
}

func TestTronGetTRXBalanceFromRPCsAnnotatesEndpoint(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`bad gateway`))
	}))
	defer bad.Close()

	_, err := tronGetTRXBalanceFromRPCs(context.Background(), []string{bad.URL}, tronTestAddress)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), bad.URL) {
		t.Fatalf("error %q does not include endpoint %q", err.Error(), bad.URL)
	}
}

func TestTronGetTRC20BalanceFromRPCsFallsBack(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":{"code":-32000,"message":"down"}}`))
	}))
	defer bad.Close()

	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":"0x2a"}`))
	}))
	defer good.Close()

	got, err := tronGetTRC20BalanceFromRPCs(context.Background(), []string{bad.URL, good.URL}, tronTestUSDTContract, tronTestAddress)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cmp(big.NewInt(42)) != 0 {
		t.Fatalf("balance = %s, want 42", got)
	}
}

func TestTronGetTRC20BalanceFromRPCsAnnotatesEndpoint(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`bad gateway`))
	}))
	defer bad.Close()

	_, err := tronGetTRC20BalanceFromRPCs(context.Background(), []string{bad.URL}, tronTestUSDTContract, tronTestAddress)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), bad.URL) {
		t.Fatalf("error %q does not include endpoint %q", err.Error(), bad.URL)
	}
}
