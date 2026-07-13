package providerhealth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"core/constants"
	"core/models"
)

func TestRedactedEndpointLabelAndHashDoNotLeakSecrets(t *testing.T) {
	raw := "https://user:secret@rpc.example.com/path?api_key=top-secret"
	label := RedactedEndpointLabel(raw, "fallback")
	if label != "rpc.example.com" {
		t.Fatalf("label = %q, want host only", label)
	}
	if strings.Contains(label, "secret") || strings.Contains(label, "api_key") {
		t.Fatalf("label leaked secret: %q", label)
	}
	if URLHash(raw) == URLHash("https://rpc.example.com/path") {
		t.Fatal("hash should be stable for exact endpoint, not host-only")
	}
}

func TestFinalizeSnapshotsMarksStaleAndSelectsHealthyFallback(t *testing.T) {
	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	snapshots := []models.ProviderHealthSnapshot{
		{ChainID: constants.Ethereum, ChainName: "ethereum", ProviderLabel: "primary", ProviderURLHash: URLHash("https://primary"), Reachable: true, Status: models.ProviderHealthStatusHealthy, LatestHeight: 100, ResponseLatencyMS: 10, CheckedAt: now},
		{ChainID: constants.Ethereum, ChainName: "ethereum", ProviderLabel: "fallback", ProviderURLHash: URLHash("https://fallback"), Reachable: true, Status: models.ProviderHealthStatusHealthy, LatestHeight: 110, ResponseLatencyMS: 20, CheckedAt: now},
	}
	got := FinalizeSnapshots(snapshots, Config{StaleLag: 3, Strategy: StrategyPreferLive})
	if got[0].Status != models.ProviderHealthStatusDegraded || got[0].ErrorCategory != ErrorStaleHead {
		t.Fatalf("primary status=%q category=%q, want degraded stale", got[0].Status, got[0].ErrorCategory)
	}
	if !got[1].Selected || got[1].FailoverReason != "primary_not_selected" {
		t.Fatalf("fallback selected=%v reason=%q, want selected failover", got[1].Selected, got[1].FailoverReason)
	}
}

func TestFinalizeSnapshotsMarksInconsistentSameHeightHeads(t *testing.T) {
	snapshots := []models.ProviderHealthSnapshot{
		{ChainID: constants.Ethereum, ChainName: "ethereum", ProviderLabel: "a", ProviderURLHash: URLHash("a"), Reachable: true, LatestHeight: 100, HeadHash: "0xa"},
		{ChainID: constants.Ethereum, ChainName: "ethereum", ProviderLabel: "b", ProviderURLHash: URLHash("b"), Reachable: true, LatestHeight: 100, HeadHash: "0xb"},
	}
	got := FinalizeSnapshots(snapshots, Config{StaleLag: 3})
	for _, snapshot := range got {
		if snapshot.Status != models.ProviderHealthStatusUnhealthy || snapshot.ErrorCategory != ErrorInconsistent {
			t.Fatalf("snapshot status=%q category=%q, want inconsistent unhealthy", snapshot.Status, snapshot.ErrorCategory)
		}
	}
}

func TestProbeEVMRPCRecordsHeightAndHeadHash(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req["method"] != "eth_getBlockByNumber" {
			t.Fatalf("method = %v, want eth_getBlockByNumber", req["method"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": map[string]any{"number": "0x10", "hash": "0xabc"}})
	}))
	defer server.Close()

	svc := New(nil, nil, Config{Timeout: time.Second, StaleLag: 3})
	height, hash, err := svc.probeEVM(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if height != 16 || hash != "0xabc" {
		t.Fatalf("height/hash = %d/%q, want 16/0xabc", height, hash)
	}
}

func TestProbeSolanaAndBitcoin(t *testing.T) {
	solana := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": 12345})
	}))
	defer solana.Close()
	bitcoin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/blocks/tip/height" {
			t.Fatalf("path = %q, want /blocks/tip/height", r.URL.Path)
		}
		_, _ = w.Write([]byte("850000"))
	}))
	defer bitcoin.Close()

	svc := New(nil, nil, Config{Timeout: time.Second, StaleLag: 3})
	slot, err := svc.probeSolana(context.Background(), solana.URL)
	if err != nil || slot != 12345 {
		t.Fatalf("solana slot=%d err=%v, want 12345 nil", slot, err)
	}
	height, err := svc.probeBitcoin(context.Background(), bitcoin.URL)
	if err != nil || height != 850000 {
		t.Fatalf("bitcoin height=%d err=%v, want 850000 nil", height, err)
	}
}

func TestRankURLsUsesHealthySnapshotWhenStrategyConfigured(t *testing.T) {
	urls := []string{"https://primary", "https://fallback"}
	snapshots := []models.ProviderHealthSnapshot{
		{ChainID: constants.Ethereum, ProviderURLHash: URLHash("https://primary"), Status: models.ProviderHealthStatusUnhealthy},
		{ChainID: constants.Ethereum, ProviderURLHash: URLHash("https://fallback"), Status: models.ProviderHealthStatusHealthy, LatestHeight: 10},
	}
	got := RankURLs(constants.Ethereum, "ethereum", urls, snapshots, "")
	if got[0] != "https://fallback" {
		t.Fatalf("ranked urls = %#v, want fallback first", got)
	}
	if observe := RankURLs(constants.Ethereum, "ethereum", urls, snapshots, StrategyObserve); observe[0] != "https://primary" {
		t.Fatalf("observe urls = %#v, want original order", observe)
	}
}

func TestConsistencyReportsSurfaceProviderDriftEvidence(t *testing.T) {
	snapshots := FinalizeSnapshots([]models.ProviderHealthSnapshot{
		{ChainID: constants.Ethereum, ChainName: "ethereum", ProviderLabel: "a", ProviderURLHash: URLHash("a"), Reachable: true, LatestHeight: 100, HeadHash: "0xa"},
		{ChainID: constants.Ethereum, ChainName: "ethereum", ProviderLabel: "b", ProviderURLHash: URLHash("b"), Reachable: true, LatestHeight: 100, HeadHash: "0xb"},
		{ChainID: constants.Base, ChainName: "base", ProviderLabel: "base-a", ProviderURLHash: URLHash("base-a"), Reachable: true, LatestHeight: 200, HeadHash: "0xc"},
		{ChainID: constants.Base, ChainName: "base", ProviderLabel: "base-b", ProviderURLHash: URLHash("base-b"), Reachable: true, LatestHeight: 180, HeadHash: "0xd"},
	}, Config{StaleLag: 3, Strategy: StrategyPreferLive})

	reports := ConsistencyReports(snapshots, Config{StaleLag: 3})
	if len(reports) != 2 {
		t.Fatalf("reports len = %d, want 2", len(reports))
	}
	byChain := map[constants.ChainID]ConsistencyReport{}
	for _, report := range reports {
		byChain[report.ChainID] = report
	}
	if !byChain[constants.Ethereum].InconsistentHead || byChain[constants.Ethereum].Evidence["error_category"] != ErrorInconsistent {
		t.Fatalf("ethereum report = %+v, want inconsistent head evidence", byChain[constants.Ethereum])
	}
	if !byChain[constants.Base].DriftExceeded || byChain[constants.Base].MaxLagBlocks != 20 {
		t.Fatalf("base report = %+v, want drift exceeded with max lag 20", byChain[constants.Base])
	}
}
