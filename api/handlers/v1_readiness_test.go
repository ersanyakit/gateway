package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"core/services/signer"
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

func TestV1ReadinessCountDetails(t *testing.T) {
	counts := map[string]int64{
		"pending":     2,
		"failed":      1,
		"dead_letter": 0,
	}
	got := v1ReadinessCountDetails(counts, "pending", "failed", "dead_letter")
	want := "pending=2, failed=1, dead_letter=0"
	if got != want {
		t.Fatalf("details = %q, want %q", got, want)
	}
}

func TestV1ReadinessCountTotal(t *testing.T) {
	counts := map[string]int64{
		"open":                  2,
		"processing":            3,
		"needs_operator_action": 4,
		"retry_scheduled":       1,
		"failed":                5,
	}
	if got := v1ReadinessCountTotal(counts, "open", "processing", "needs_operator_action", "retry_scheduled", "failed"); got != 15 {
		t.Fatalf("total = %d, want 15", got)
	}
}

func TestV1MigrationStrategyReadiness(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("ALLOW_AUTOMIGRATE_IN_PRODUCTION", "")
	ok, _, err := v1MigrationStrategyReadiness()
	if !ok || err != nil {
		t.Fatalf("development migration readiness ok=%v err=%v, want ok", ok, err)
	}

	t.Setenv("APP_ENV", "production")
	ok, _, err = v1MigrationStrategyReadiness()
	if !ok || err != nil {
		t.Fatalf("production with AutoMigrate disabled ok=%v err=%v, want ok", ok, err)
	}

	t.Setenv("ALLOW_AUTOMIGRATE_IN_PRODUCTION", "true")
	ok, _, err = v1MigrationStrategyReadiness()
	if ok || err == nil {
		t.Fatalf("production AutoMigrate override ok=%v err=%v, want failure", ok, err)
	}
}

func TestV1ProductionSignerReadiness(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("SIGNER_MODE", "")
	t.Setenv("ALLOW_SOFTWARE_SIGNER_IN_PRODUCTION", "")
	ok, _, err := v1ProductionSignerReadiness()
	if !ok || err != nil {
		t.Fatalf("development signer readiness ok=%v err=%v, want ok", ok, err)
	}

	t.Setenv("APP_ENV", "production")
	ok, _, err = v1ProductionSignerReadiness()
	if ok || !errors.Is(err, signer.ErrProductionSoftwareSignerDisabled) {
		t.Fatalf("production default software signer ok=%v err=%v, want software signer failure", ok, err)
	}

	t.Setenv("ALLOW_SOFTWARE_SIGNER_IN_PRODUCTION", "true")
	ok, _, err = v1ProductionSignerReadiness()
	if ok || !errors.Is(err, signer.ErrProductionSoftwareSignerDisabled) {
		t.Fatalf("production software signer override ok=%v err=%v, want software signer failure", ok, err)
	}

	t.Setenv("ALLOW_SOFTWARE_SIGNER_IN_PRODUCTION", "")
	t.Setenv("SIGNER_MODE", "kms")
	ok, _, err = v1ProductionSignerReadiness()
	if ok || !errors.Is(err, signer.ErrExternalSignerIntegrationRequired) {
		t.Fatalf("unimplemented external signer ok=%v err=%v, want external integration failure", ok, err)
	}

	t.Setenv("SIGNER_MODE", "vault")
	ok, details, err := v1ProductionSignerReadiness()
	if ok || !errors.Is(err, signer.ErrExternalSignerIntegrationRequired) || !strings.Contains(details, "vault") {
		t.Fatalf("vault signer readiness ok=%v details=%q err=%v, want external integration failure", ok, details, err)
	}
}

func TestV1MetricsAccessReadiness(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("METRICS_BEARER_TOKEN", "")
	ok, _, err := v1MetricsAccessReadiness()
	if !ok || err != nil {
		t.Fatalf("development metrics readiness ok=%v err=%v, want ok", ok, err)
	}

	t.Setenv("APP_ENV", "production")
	ok, _, err = v1MetricsAccessReadiness()
	if ok || err == nil {
		t.Fatalf("production missing metrics token ok=%v err=%v, want failure", ok, err)
	}

	t.Setenv("METRICS_BEARER_TOKEN", "metrics-token")
	ok, _, err = v1MetricsAccessReadiness()
	if !ok || err != nil {
		t.Fatalf("production metrics token ok=%v err=%v, want ok", ok, err)
	}
}

func TestV1PortalCSRFReadiness(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("CSRF_JWT_SECRET", "")
	t.Setenv("DEALER_SESSION_SECRET", "")
	t.Setenv("SESSION_SECRET", "")
	t.Setenv("MASTER_KEY", "")
	ok, _, err := v1PortalCSRFReadiness()
	if !ok || err != nil {
		t.Fatalf("development csrf readiness ok=%v err=%v, want ok", ok, err)
	}

	t.Setenv("APP_ENV", "production")
	ok, _, err = v1PortalCSRFReadiness()
	if ok || err == nil {
		t.Fatalf("production missing csrf secret ok=%v err=%v, want failure", ok, err)
	}

	t.Setenv("MASTER_KEY", "stable-production-master-key")
	ok, _, err = v1PortalCSRFReadiness()
	if !ok || err != nil {
		t.Fatalf("production csrf secret ok=%v err=%v, want ok", ok, err)
	}
}
