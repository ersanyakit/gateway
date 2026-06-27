package handlers

import (
	"context"
	"crypto/hmac"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"core/blockchain"
	"core/constants"
	"core/models"

	"github.com/gofiber/fiber/v3"
)

type metricsStatusCounter interface {
	CountByStatus(ctx context.Context, statuses ...string) (map[string]int64, error)
}

type metricsChainStateLister interface {
	ListAll(ctx context.Context) ([]models.ChainState, error)
}

type OperationalMetricsDeps struct {
	WebhookDeliveryRepo metricsStatusCounter
	SweepJobRepo        metricsStatusCounter
	ReconciliationRepo  metricsStatusCounter
	ChainStateRepo      metricsChainStateLister
	Blockchains         *blockchain.ChainFactory
}

// HandleOperationalMetrics exposes low-cardinality Prometheus text metrics for production operations.
func HandleOperationalMetrics(deps OperationalMetricsDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		if err := authorizeMetricsRequest(c); err != nil {
			return err
		}

		body := buildOperationalMetrics(c.Context(), deps, time.Now)
		c.Set(fiber.HeaderContentType, "text/plain; version=0.0.4; charset=utf-8")
		return c.SendString(body)
	}
}

func authorizeMetricsRequest(c fiber.Ctx) error {
	token := strings.TrimSpace(os.Getenv("METRICS_BEARER_TOKEN"))
	if token == "" {
		if strings.EqualFold(strings.TrimSpace(os.Getenv("APP_ENV")), "production") {
			return c.Status(fiber.StatusServiceUnavailable).SendString("metrics disabled: METRICS_BEARER_TOKEN is required in production\n")
		}
		return nil
	}

	auth := strings.TrimSpace(c.Get("Authorization"))
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		c.Set("WWW-Authenticate", `Bearer realm="gateway-metrics"`)
		return c.Status(fiber.StatusUnauthorized).SendString("missing metrics bearer token\n")
	}
	provided := strings.TrimSpace(strings.TrimPrefix(auth, prefix))
	if !hmac.Equal([]byte(provided), []byte(token)) {
		c.Set("WWW-Authenticate", `Bearer realm="gateway-metrics"`)
		return c.Status(fiber.StatusUnauthorized).SendString("invalid metrics bearer token\n")
	}
	return nil
}

func buildOperationalMetrics(ctx context.Context, deps OperationalMetricsDeps, now func() time.Time) string {
	var b strings.Builder
	writeMetricHeader(&b, "gateway_build_info", "Gateway process info.", "gauge")
	writeGauge(&b, "gateway_build_info", map[string]string{
		"version": constants.PRODUCT_VERSION,
	}, 1)

	migrationOK, _, _ := v1MigrationStrategyReadiness()
	writeMetricHeader(&b, "gateway_migration_strategy_ready", "1 when startup migration policy is acceptable for this environment.", "gauge")
	writeGauge(&b, "gateway_migration_strategy_ready", nil, boolFloat(migrationOK))

	signerOK, _, _ := v1ProductionSignerReadiness()
	writeMetricHeader(&b, "gateway_production_signer_ready", "1 when signer policy is production launch-ready.", "gauge")
	writeGauge(&b, "gateway_production_signer_ready", nil, boolFloat(signerOK))

	writeMetricHeader(&b, "gateway_metrics_collection_error", "Metrics collection errors by collector.", "gauge")
	appendStatusMetrics(ctx, &b, "gateway_webhook_delivery_backlog", "Webhook delivery rows by retry-relevant status.", deps.WebhookDeliveryRepo, []string{
		models.WebhookDeliveryStatusPending,
		models.WebhookDeliveryStatusFailed,
		models.WebhookDeliveryStatusDeadLetter,
	})
	appendStatusMetrics(ctx, &b, "gateway_sweep_job_backlog", "Sweep jobs by operational status.", deps.SweepJobRepo, []string{
		models.SweepJobStatusPending,
		models.SweepJobStatusProcessing,
		models.SweepJobStatusFailed,
		models.SweepJobStatusDeadLetter,
	})
	appendStatusMetrics(ctx, &b, "gateway_reconciliation_jobs", "Reconciliation jobs by unresolved status.", deps.ReconciliationRepo, []string{
		models.ReconciliationStatusOpen,
		models.ReconciliationStatusProcessing,
		models.ReconciliationStatusNeedsOperatorAction,
		models.ReconciliationStatusRetryScheduled,
		models.ReconciliationStatusFailed,
	})
	appendChainMetrics(ctx, &b, deps, now)
	return b.String()
}

func appendStatusMetrics(ctx context.Context, b *strings.Builder, name string, help string, counter metricsStatusCounter, statuses []string) {
	writeMetricHeader(b, name, help, "gauge")
	if counter == nil {
		writeCollectionError(b, name, "repository_not_configured")
		return
	}
	counts, err := counter.CountByStatus(ctx, statuses...)
	if err != nil {
		writeCollectionError(b, name, "query_failed")
		return
	}
	for _, status := range statuses {
		writeGauge(b, name, map[string]string{"status": status}, float64(counts[status]))
	}
}

func appendChainMetrics(ctx context.Context, b *strings.Builder, deps OperationalMetricsDeps, now func() time.Time) {
	writeMetricHeader(b, "gateway_chain_worker_count", "Registered listener worker count per chain.", "gauge")
	if deps.Blockchains == nil {
		writeCollectionError(b, "gateway_chain_worker_count", "factory_not_configured")
	} else {
		for _, chainName := range deps.Blockchains.ListChains() {
			chain, err := deps.Blockchains.GetChain(chainName)
			if err != nil {
				writeCollectionError(b, "gateway_chain_worker_count", "chain_lookup_failed")
				continue
			}
			writeGauge(b, "gateway_chain_worker_count", chainLabels(chain), float64(chain.WorkerCount()))
		}
	}

	writeMetricHeader(b, "gateway_chain_last_processed_block", "Last processed block or slot by chain state.", "gauge")
	writeMetricHeader(b, "gateway_chain_last_confirmed_block", "Last confirmed block or slot by chain state.", "gauge")
	writeMetricHeader(b, "gateway_chain_state_age_seconds", "Age of the chain state update timestamp.", "gauge")
	if deps.ChainStateRepo == nil {
		writeCollectionError(b, "gateway_chain_state", "repository_not_configured")
		return
	}
	states, err := deps.ChainStateRepo.ListAll(ctx)
	if err != nil {
		writeCollectionError(b, "gateway_chain_state", "query_failed")
		return
	}
	sort.Slice(states, func(i, j int) bool { return states[i].ChainID < states[j].ChainID })
	nowTime := now()
	for _, state := range states {
		labels := map[string]string{
			"chain_id": strconv.FormatInt(int64(state.ChainID), 10),
			"chain":    constants.ChainName(state.ChainID),
		}
		writeGauge(b, "gateway_chain_last_processed_block", labels, float64(state.LastProcessedBlock))
		writeGauge(b, "gateway_chain_last_confirmed_block", labels, float64(state.LastConfirmedBlock))
		age := 0.0
		if !state.UpdatedAt.IsZero() {
			age = nowTime.Sub(state.UpdatedAt).Seconds()
			if age < 0 {
				age = 0
			}
		}
		writeGauge(b, "gateway_chain_state_age_seconds", labels, age)
	}
}

func chainLabels(chain blockchain.Chain) map[string]string {
	return map[string]string{
		"chain":    chain.Name(),
		"chain_id": strconv.FormatInt(int64(chain.ChainID()), 10),
	}
}

func writeCollectionError(b *strings.Builder, collector string, reason string) {
	writeGauge(b, "gateway_metrics_collection_error", map[string]string{
		"collector": collector,
		"reason":    reason,
	}, 1)
}

func writeMetricHeader(b *strings.Builder, name string, help string, typ string) {
	fmt.Fprintf(b, "# HELP %s %s\n", name, help)
	fmt.Fprintf(b, "# TYPE %s %s\n", name, typ)
}

func writeGauge(b *strings.Builder, name string, labels map[string]string, value float64) {
	fmt.Fprintf(b, "%s%s %s\n", name, prometheusLabels(labels), strconv.FormatFloat(value, 'f', -1, 64))
}

func prometheusLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+`="`+escapePrometheusLabel(labels[key])+`"`)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func escapePrometheusLabel(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return value
}

func boolFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}
