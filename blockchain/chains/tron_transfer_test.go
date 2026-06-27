package chains

import (
	"context"
	"encoding/json"
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
		_, _ = w.Write([]byte(`{"balance":42}`))
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

func TestTronGetTRXBalanceUsesFullNodeGetAccount(t *testing.T) {
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

	got, err := tronGetTRXBalance(context.Background(), server.URL+"/jsonrpc", tronTestAddress)
	if err != nil {
		t.Fatal(err)
	}
	if got != 18_500_000 {
		t.Fatalf("balance = %d, want 18500000", got)
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

func TestTronEstimateBandwidthFeeSUNUsesAvailableResources(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wallet/getaccountresource" {
			t.Fatalf("path = %q, want /wallet/getaccountresource", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"freeNetLimit":80,"freeNetUsed":30,"NetLimit":100,"NetUsed":50}`))
	}))
	defer server.Close()

	fee, err := tronEstimateBandwidthFeeSUN(context.Background(), []string{server.URL}, tronTestAddress, strings.Repeat("ab", 180))
	if err != nil {
		t.Fatal(err)
	}
	if fee != 80_000 {
		t.Fatalf("fee = %d, want 80000", fee)
	}
}

func TestTronEstimateBandwidthFeeSUNReturnsZeroWhenBandwidthCoversTx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"freeNetLimit":500,"freeNetUsed":0}`))
	}))
	defer server.Close()

	fee, err := tronEstimateBandwidthFeeSUN(context.Background(), []string{server.URL}, tronTestAddress, strings.Repeat("ab", 180))
	if err != nil {
		t.Fatal(err)
	}
	if fee != 0 {
		t.Fatalf("fee = %d, want 0", fee)
	}
}

func TestTronGasPrefundDefaultsToTRC20FeeLimit(t *testing.T) {
	t.Setenv("TRON_TRC20_FEE_LIMIT_SUN", "42000000")
	t.Setenv("TRON_GAS_THRESHOLD_SUN", "")
	t.Setenv("TRON_GAS_PREFUND_SUN", "")

	if got := tronGasThresholdSUN(); got != 42_000_000 {
		t.Fatalf("threshold = %d, want 42000000", got)
	}
	if got := tronGasPrefundSUN(); got != 42_000_000 {
		t.Fatalf("prefund = %d, want 42000000", got)
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
