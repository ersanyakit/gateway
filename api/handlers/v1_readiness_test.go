package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"core/blockchain"
	"core/constants"
	"core/models"
	"core/services/dbmigrations"
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

type fakeV1ProviderHealth []models.ProviderHealthSnapshot

func (f fakeV1ProviderHealth) ListByChain(ctx context.Context, chainID constants.ChainID) ([]models.ProviderHealthSnapshot, error) {
	out := make([]models.ProviderHealthSnapshot, 0, len(f))
	for _, snapshot := range f {
		if snapshot.ChainID == chainID {
			out = append(out, snapshot)
		}
	}
	return out, nil
}

type fakeReadinessChain struct {
	id   constants.ChainID
	name string
}

func (f fakeReadinessChain) ChainID() constants.ChainID { return f.id }
func (f fakeReadinessChain) Name() string               { return f.name }
func (f fakeReadinessChain) WSS() []string              { return nil }
func (f fakeReadinessChain) RPCs() []string             { return nil }
func (f fakeReadinessChain) Create(context.Context) (*blockchain.WalletDetails, error) {
	return nil, nil
}
func (f fakeReadinessChain) CreateHDWallet(context.Context, int, int) (*blockchain.WalletDetails, error) {
	return nil, nil
}
func (f fakeReadinessChain) Deposit(context.Context, blockchain.WalletDetails, string, string) (*blockchain.TransactionResult, error) {
	return nil, nil
}
func (f fakeReadinessChain) Withdraw(context.Context, blockchain.WalletDetails, string, string) (*blockchain.TransactionResult, error) {
	return nil, nil
}
func (f fakeReadinessChain) WithdrawToken(context.Context, blockchain.WalletDetails, string, string, string) (*blockchain.TransactionResult, error) {
	return nil, nil
}
func (f fakeReadinessChain) Sweep(context.Context, blockchain.WalletDetails) (*blockchain.TransactionResult, error) {
	return nil, nil
}
func (f fakeReadinessChain) SweepTo(context.Context, blockchain.WalletDetails, string) (*blockchain.TransactionResult, error) {
	return nil, nil
}
func (f fakeReadinessChain) SweepERC20To(context.Context, blockchain.WalletDetails, string, string) (*blockchain.TransactionResult, error) {
	return nil, nil
}
func (f fakeReadinessChain) PrefundGas(context.Context, blockchain.WalletDetails, string) (bool, error) {
	return false, nil
}
func (f fakeReadinessChain) ValidateAddress(string) bool       { return true }
func (f fakeReadinessChain) AddWorker(blockchain.Worker) error { return nil }
func (f fakeReadinessChain) RemoveWorker(blockchain.Worker) error {
	return nil
}
func (f fakeReadinessChain) WorkerCount() int { return 1 }
func (f fakeReadinessChain) BatchBalances(context.Context, []string, int) []models.BalanceResult {
	return nil
}
func (f fakeReadinessChain) StartWorkers(context.Context) error { return nil }
func (f fakeReadinessChain) StopWorkers() error                 { return nil }

func TestV1ProviderHealthReadinessUsesSnapshots(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	added := make([]types.V1ReadinessCheck, 0)
	add := func(name string, ok bool, details string, err error) {
		check := types.V1ReadinessCheck{Name: name, OK: ok, Details: details}
		if err != nil {
			check.Error = err.Error()
		}
		added = append(added, check)
	}
	v1AppendProviderHealthReadiness(context.Background(), add, "chain.ethereum", fakeReadinessChain{id: constants.Ethereum, name: "ethereum"}, fakeV1ProviderHealth{{
		ChainID:       constants.Ethereum,
		ChainName:     "ethereum",
		ProviderLabel: "rpc.example.com",
		Status:        models.ProviderHealthStatusHealthy,
		Selected:      true,
	}})
	if len(added) != 1 || !added[0].OK || !strings.Contains(added[0].Details, "healthy=1") {
		t.Fatalf("readiness checks = %#v, want healthy provider ok", added)
	}
}

func TestV1ProviderHealthReadinessRequiresSnapshotsInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	var check types.V1ReadinessCheck
	add := func(name string, ok bool, details string, err error) {
		check = types.V1ReadinessCheck{Name: name, OK: ok, Details: details}
		if err != nil {
			check.Error = err.Error()
		}
	}
	v1AppendProviderHealthReadiness(context.Background(), add, "chain.ethereum", fakeReadinessChain{id: constants.Ethereum, name: "ethereum"}, fakeV1ProviderHealth{})
	if check.OK || !strings.Contains(check.Error, "provider health snapshot is required") {
		t.Fatalf("check = %#v, want missing snapshot failure", check)
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

func TestV1AppendReadinessCheckRedactsSecrets(t *testing.T) {
	checks := make([]types.V1ReadinessCheck, 0, 1)
	v1AppendReadinessCheck(&checks, "probe", false, "rpc api_secret=plain", errors.New("webhook_secret=plain raw_signature=abc"))

	if len(checks) != 1 {
		t.Fatalf("checks = %d, want one", len(checks))
	}
	serialized := checks[0].Details + " " + checks[0].Error
	for _, forbidden := range []string{"api_secret=plain", "webhook_secret=plain", "raw_signature=abc"} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("readiness check leaked %q: %#v", forbidden, checks[0])
		}
	}
	if !strings.Contains(serialized, "[redacted]") {
		t.Fatalf("readiness check should include redaction marker: %#v", checks[0])
	}
}

func TestV1MigrationStrategyReadiness(t *testing.T) {
	latestMigrationID, err := dbmigrations.LatestID()
	if err != nil {
		t.Fatalf("latest migration id: %v", err)
	}

	t.Setenv("APP_ENV", "development")
	t.Setenv("ALLOW_AUTOMIGRATE_IN_PRODUCTION", "")
	t.Setenv("GATEWAY_DB_MIGRATION_VERSION", "")
	ok, _, err := v1MigrationStrategyReadiness()
	if !ok || err != nil {
		t.Fatalf("development migration readiness ok=%v err=%v, want ok", ok, err)
	}

	t.Setenv("APP_ENV", "production")
	ok, _, err = v1MigrationStrategyReadiness()
	if ok || err == nil {
		t.Fatalf("production without migration evidence ok=%v err=%v, want failure", ok, err)
	}

	t.Setenv("GATEWAY_DB_MIGRATION_VERSION", latestMigrationID)
	ok, _, err = v1MigrationStrategyReadiness()
	if !ok || err != nil {
		t.Fatalf("production with migration evidence ok=%v err=%v, want ok", ok, err)
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

	restore := signer.RegisterCustodyAdapter(fakeV1SignerAdapter{health: signer.AdapterHealth{
		Ready:    true,
		Mode:     "vault",
		Provider: "vault-primary",
	}})
	defer restore()
	ok, details, err = v1ProductionSignerReadiness()
	if !ok || err != nil || !strings.Contains(details, "vault-primary") {
		t.Fatalf("vault signer readiness with adapter ok=%v details=%q err=%v, want ready", ok, details, err)
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

func TestV1PortalJWTReadiness(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("PORTAL_JWT_SECRET", "")
	t.Setenv("DEALER_SESSION_SECRET", "")
	t.Setenv("SESSION_SECRET", "")
	t.Setenv("MASTER_KEY", "")
	ok, _, err := v1PortalJWTReadiness()
	if !ok || err != nil {
		t.Fatalf("development portal jwt readiness ok=%v err=%v, want ok", ok, err)
	}

	t.Setenv("APP_ENV", "production")
	ok, _, err = v1PortalJWTReadiness()
	if ok || err == nil {
		t.Fatalf("production missing portal jwt secret ok=%v err=%v, want failure", ok, err)
	}

	t.Setenv("MASTER_KEY", "stable-production-master-key")
	ok, _, err = v1PortalJWTReadiness()
	if !ok || err != nil {
		t.Fatalf("production portal jwt secret ok=%v err=%v, want ok", ok, err)
	}
}

type fakeV1SignerAdapter struct {
	health signer.AdapterHealth
}

func (f fakeV1SignerAdapter) DeriveAddress(context.Context, signer.DeriveAddressRequest) (signer.DeriveAddressResponse, error) {
	return signer.DeriveAddressResponse{}, nil
}

func (f fakeV1SignerAdapter) SignTransaction(context.Context, signer.SignTransactionRequest) (signer.SignTransactionResponse, error) {
	return signer.SignTransactionResponse{}, nil
}

func (f fakeV1SignerAdapter) SignMessage(context.Context, signer.SignMessageRequest) (signer.SignMessageResponse, error) {
	return signer.SignMessageResponse{}, nil
}

func (f fakeV1SignerAdapter) KeyReference(context.Context, signer.DeriveAddressRequest) (string, error) {
	return "vault:key:ref", nil
}

func (f fakeV1SignerAdapter) Health(context.Context) signer.AdapterHealth {
	return f.health
}
