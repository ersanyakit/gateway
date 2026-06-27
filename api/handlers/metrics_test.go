package handlers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"core/constants"
	"core/models"

	"github.com/gofiber/fiber/v3"
)

type fakeMetricsCounter map[string]int64

func (f fakeMetricsCounter) CountByStatus(ctx context.Context, statuses ...string) (map[string]int64, error) {
	out := make(map[string]int64, len(statuses))
	for _, status := range statuses {
		out[status] = f[status]
	}
	return out, nil
}

type fakeMetricsChainStates []models.ChainState

func (f fakeMetricsChainStates) ListAll(ctx context.Context) ([]models.ChainState, error) {
	out := make([]models.ChainState, len(f))
	copy(out, f)
	return out, nil
}

func TestOperationalMetricsIncludesBacklogAndChainState(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("METRICS_BEARER_TOKEN", "")
	updatedAt := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	body := buildOperationalMetrics(context.Background(), OperationalMetricsDeps{
		WebhookDeliveryRepo: fakeMetricsCounter{
			models.WebhookDeliveryStatusPending:    2,
			models.WebhookDeliveryStatusFailed:     1,
			models.WebhookDeliveryStatusDeadLetter: 0,
		},
		SweepJobRepo: fakeMetricsCounter{
			models.SweepJobStatusPending:    3,
			models.SweepJobStatusProcessing: 1,
			models.SweepJobStatusFailed:     0,
			models.SweepJobStatusDeadLetter: 0,
		},
		ReconciliationRepo: fakeMetricsCounter{
			models.ReconciliationStatusOpen:                1,
			models.ReconciliationStatusProcessing:          0,
			models.ReconciliationStatusNeedsOperatorAction: 2,
			models.ReconciliationStatusRetryScheduled:      1,
			models.ReconciliationStatusFailed:              0,
		},
		ChainStateRepo: fakeMetricsChainStates{{
			ChainID:            constants.Ethereum,
			LastProcessedBlock: 100,
			LastConfirmedBlock: 88,
			UpdatedAt:          updatedAt,
		}},
	}, func() time.Time { return updatedAt.Add(90 * time.Second) })

	requireMetricsContains(t, body,
		"gateway_migration_strategy_ready 1",
		"gateway_production_signer_ready 1",
		`gateway_webhook_delivery_backlog{status="pending"} 2`,
		`gateway_webhook_delivery_backlog{status="failed"} 1`,
		`gateway_sweep_job_backlog{status="pending"} 3`,
		`gateway_reconciliation_jobs{status="open"} 1`,
		`gateway_reconciliation_jobs{status="needs_operator_action"} 2`,
		`gateway_reconciliation_jobs{status="retry_scheduled"} 1`,
		`gateway_chain_last_processed_block{chain="ethereum",chain_id="1"} 100`,
		`gateway_chain_last_confirmed_block{chain="ethereum",chain_id="1"} 88`,
		`gateway_chain_state_age_seconds{chain="ethereum",chain_id="1"} 90`,
	)
}

func TestOperationalMetricsReportsProductionSignerGate(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("SIGNER_MODE", "software")
	t.Setenv("ALLOW_SOFTWARE_SIGNER_IN_PRODUCTION", "true")
	t.Setenv("METRICS_BEARER_TOKEN", "metrics-token")
	t.Setenv("MNEMONIC_PHRASE", "legacy-secret")

	app := fiber.New()
	app.Get("/metrics", HandleOperationalMetrics(OperationalMetricsDeps{}))

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer metrics-token")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read metrics body: %v", err)
	}
	body := string(bodyBytes)
	requireMetricsContains(t, body, "gateway_production_signer_ready 0")
	if strings.Contains(body, "legacy-secret") || strings.Contains(body, "MNEMONIC_PHRASE") {
		t.Fatalf("metrics body leaked secret-like content: %s", body)
	}
}

func TestOperationalMetricsRequiresTokenInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("METRICS_BEARER_TOKEN", "")
	app := fiber.New()
	app.Get("/metrics", HandleOperationalMetrics(OperationalMetricsDeps{}))

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestOperationalMetricsBearerToken(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("METRICS_BEARER_TOKEN", "metrics-token")
	app := fiber.New()
	app.Get("/metrics", HandleOperationalMetrics(OperationalMetricsDeps{}))

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test missing token: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want 401", resp.StatusCode)
	}

	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	resp, err = app.Test(req)
	if err != nil {
		t.Fatalf("app.Test wrong token: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("wrong token status = %d, want 401", resp.StatusCode)
	}

	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer metrics-token")
	resp, err = app.Test(req)
	if err != nil {
		t.Fatalf("app.Test valid token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("valid token status = %d, want 200", resp.StatusCode)
	}
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read metrics body: %v", err)
	}
	if !strings.Contains(string(bodyBytes), "gateway_build_info") {
		t.Fatalf("metrics body missing gateway_build_info: %s", string(bodyBytes))
	}
}

func requireMetricsContains(t *testing.T, body string, needles ...string) {
	t.Helper()
	for _, needle := range needles {
		if !strings.Contains(body, needle) {
			t.Fatalf("metrics body missing %q:\n%s", needle, body)
		}
	}
}
