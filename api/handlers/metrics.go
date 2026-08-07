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
	"core/services/signer"

	"github.com/gofiber/fiber/v3"
)

type metricsStatusCounter interface {
	CountByStatus(ctx context.Context, statuses ...string) (map[string]int64, error)
}

type metricsStatusAger interface {
	OldestAgeSecondsByStatus(ctx context.Context, statuses ...string) (map[string]float64, error)
}

type metricsStatusAttemptCounter interface {
	AttemptCountByStatus(ctx context.Context, statuses ...string) (map[string]int64, error)
}

type metricsChainStateLister interface {
	ListAll(ctx context.Context) ([]models.ChainState, error)
}

type metricsProviderHealthLister interface {
	ListLatest(ctx context.Context) ([]models.ProviderHealthSnapshot, error)
}

type metricsWalletAddressLookupCounter interface {
	CountByChain(ctx context.Context) (map[constants.ChainID]int64, error)
}

type OperationalMetricsDeps struct {
	WebhookDeliveryRepo  metricsStatusCounter
	MoneyEventOutboxRepo metricsStatusCounter
	MoneyEventInboxRepo  interface {
		metricsStatusCounter
		metricsStatusAger
		metricsStatusAttemptCounter
	}
	WorkerLeaseRepo         metricsStatusCounter
	SweepJobRepo            metricsStatusCounter
	OutboundTransactionRepo metricsStatusCounter
	WithdrawalRepo          metricsStatusCounter
	RefundRepo              metricsStatusCounter
	ReconciliationRepo      metricsStatusCounter
	ChainStateRepo          metricsChainStateLister
	ProviderHealthRepo      metricsProviderHealthLister
	WalletAddressLookupRepo metricsWalletAddressLookupCounter
	Blockchains             *blockchain.ChainFactory
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
	appendSignerAdapterMetrics(ctx, &b)

	writeMetricHeader(&b, "gateway_metrics_collection_error", "Metrics collection errors by collector.", "gauge")
	appendStatusMetrics(ctx, &b, "gateway_webhook_delivery_backlog", "Webhook delivery rows by retry-relevant status.", deps.WebhookDeliveryRepo, []string{
		models.WebhookDeliveryStatusPending,
		models.WebhookDeliveryStatusProcessing,
		models.WebhookDeliveryStatusFailed,
		models.WebhookDeliveryStatusDeadLetter,
	})
	appendStatusMetrics(ctx, &b, "gateway_money_event_outbox_backlog", "Money event outbox rows by retry-relevant status.", deps.MoneyEventOutboxRepo, []string{
		models.MoneyEventOutboxStatusPending,
		models.MoneyEventOutboxStatusProcessing,
		models.MoneyEventOutboxStatusFailed,
		models.MoneyEventOutboxStatusDeadLetter,
	})
	inboxStatuses := []string{
		models.MoneyEventInboxStatusReceived,
		models.MoneyEventInboxStatusProcessing,
		models.MoneyEventInboxStatusFailed,
		models.MoneyEventInboxStatusDeadLetter,
	}
	appendStatusMetrics(ctx, &b, "gateway_money_event_inbox_backlog", "Money event inbox rows by retry-relevant status.", deps.MoneyEventInboxRepo, inboxStatuses)
	appendStatusAgeMetrics(ctx, &b, "gateway_money_event_inbox_oldest_age_seconds", "Oldest money event inbox row age by retry-relevant status.", deps.MoneyEventInboxRepo, inboxStatuses)
	appendStatusAttemptMetrics(ctx, &b, "gateway_money_event_inbox_attempts", "Money event inbox processing attempts by retry-relevant status.", deps.MoneyEventInboxRepo, inboxStatuses)
	appendStatusMetrics(ctx, &b, "gateway_worker_leases", "Worker lease rows by status.", deps.WorkerLeaseRepo, []string{
		models.WorkerLeaseStatusActive,
		models.WorkerLeaseStatusReleased,
	})
	appendStatusMetrics(ctx, &b, "gateway_sweep_job_backlog", "Sweep jobs by operational status.", deps.SweepJobRepo, []string{
		models.SweepJobStatusPending,
		models.SweepJobStatusProcessing,
		models.SweepJobStatusFailed,
		models.SweepJobStatusDeadLetter,
	})
	appendStatusMetrics(ctx, &b, "gateway_withdrawal_backlog", "Withdrawal requests by active or failed status.", deps.WithdrawalRepo, []string{
		models.WithdrawalStatusPending,
		models.WithdrawalStatusApproved,
		models.WithdrawalStatusProcessing,
		models.WithdrawalStatusFailed,
	})
	appendStatusMetrics(ctx, &b, "gateway_refund_backlog", "Refund requests by active or failed status.", deps.RefundRepo, []string{
		models.RefundStatusPending,
		models.RefundStatusApproved,
		models.RefundStatusProcessing,
		models.RefundStatusFailed,
	})
	appendStatusMetrics(ctx, &b, "gateway_reconciliation_jobs", "Reconciliation jobs by unresolved status.", deps.ReconciliationRepo, []string{
		models.ReconciliationStatusOpen,
		models.ReconciliationStatusProcessing,
		models.ReconciliationStatusNeedsOperatorAction,
		models.ReconciliationStatusRetryScheduled,
		models.ReconciliationStatusFailed,
	})
	appendChainMetrics(ctx, &b, deps, now)
	appendProviderHealthMetrics(ctx, &b, deps.ProviderHealthRepo)
	appendWalletAddressLookupMetrics(ctx, &b, deps.WalletAddressLookupRepo)
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

func appendStatusAgeMetrics(ctx context.Context, b *strings.Builder, name string, help string, ager metricsStatusAger, statuses []string) {
	writeMetricHeader(b, name, help, "gauge")
	if ager == nil {
		writeCollectionError(b, name, "repository_not_configured")
		return
	}
	ages, err := ager.OldestAgeSecondsByStatus(ctx, statuses...)
	if err != nil {
		writeCollectionError(b, name, "query_failed")
		return
	}
	for _, status := range statuses {
		writeGauge(b, name, map[string]string{"status": status}, ages[status])
	}
}

func appendStatusAttemptMetrics(ctx context.Context, b *strings.Builder, name string, help string, counter metricsStatusAttemptCounter, statuses []string) {
	writeMetricHeader(b, name, help, "gauge")
	if counter == nil {
		writeCollectionError(b, name, "repository_not_configured")
		return
	}
	attempts, err := counter.AttemptCountByStatus(ctx, statuses...)
	if err != nil {
		writeCollectionError(b, name, "query_failed")
		return
	}
	for _, status := range statuses {
		writeGauge(b, name, map[string]string{"status": status}, float64(attempts[status]))
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

func appendProviderHealthMetrics(ctx context.Context, b *strings.Builder, lister metricsProviderHealthLister) {
	writeMetricHeader(b, "gateway_provider_health", "Provider health status gauge: healthy=1 degraded=0.5 unhealthy_or_unknown=0.", "gauge")
	writeMetricHeader(b, "gateway_provider_latest_height", "Latest observed provider block height or slot.", "gauge")
	writeMetricHeader(b, "gateway_provider_lag_blocks", "Provider lag from chain reference head.", "gauge")
	writeMetricHeader(b, "gateway_provider_response_latency_ms", "Provider probe response latency in milliseconds.", "gauge")
	writeMetricHeader(b, "gateway_provider_consecutive_failures", "Consecutive provider probe failures.", "gauge")
	writeMetricHeader(b, "gateway_provider_failover_decision", "1 for the selected provider after health-aware ranking.", "gauge")
	if lister == nil {
		writeCollectionError(b, "gateway_provider_health", "repository_not_configured")
		return
	}
	snapshots, err := lister.ListLatest(ctx)
	if err != nil {
		writeCollectionError(b, "gateway_provider_health", "query_failed")
		return
	}
	for _, snapshot := range snapshots {
		labels := providerHealthLabels(snapshot)
		writeGauge(b, "gateway_provider_health", labels, providerHealthStatusValue(snapshot.Status))
		writeGauge(b, "gateway_provider_latest_height", labels, float64(snapshot.LatestHeight))
		writeGauge(b, "gateway_provider_lag_blocks", labels, float64(snapshot.LagFromReference))
		writeGauge(b, "gateway_provider_response_latency_ms", labels, float64(snapshot.ResponseLatencyMS))
		writeGauge(b, "gateway_provider_consecutive_failures", labels, float64(snapshot.ConsecutiveFailures))
		writeGauge(b, "gateway_provider_failover_decision", labels, boolFloat(snapshot.Selected))
	}
}

func appendWalletAddressLookupMetrics(ctx context.Context, b *strings.Builder, counter metricsWalletAddressLookupCounter) {
	writeMetricHeader(b, "gateway_wallet_address_lookup_rows", "Normalized wallet address lookup rows by chain.", "gauge")
	if counter == nil {
		writeCollectionError(b, "gateway_wallet_address_lookup_rows", "repository_not_configured")
		return
	}
	counts, err := counter.CountByChain(ctx)
	if err != nil {
		writeCollectionError(b, "gateway_wallet_address_lookup_rows", "query_failed")
		return
	}
	for _, chainID := range constants.AllChainIDs() {
		writeGauge(b, "gateway_wallet_address_lookup_rows", map[string]string{
			"chain":    constants.ChainName(chainID),
			"chain_id": strconv.FormatInt(int64(chainID), 10),
		}, float64(counts[chainID]))
	}
}

func appendSignerAdapterMetrics(ctx context.Context, b *strings.Builder) {
	status := signer.Status(ctx)
	writeMetricHeader(b, "gateway_signer_adapter_ready", "1 when the configured external custody signer adapter is active and ready.", "gauge")
	writeGauge(b, "gateway_signer_adapter_ready", map[string]string{
		"mode":           status.Mode,
		"external_mode":  strconv.FormatBool(status.ExternalMode),
		"adapter_active": strconv.FormatBool(status.AdapterActive),
		"provider":       safeSignerProviderLabel(status.Provider),
	}, boolFloat(status.AdapterReady))
}

func safeSignerProviderLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "none"
	}
	lower := strings.ToLower(value)
	if strings.Contains(value, "://") || strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, "key") {
		return "redacted"
	}
	if len(value) > 48 {
		return value[:48]
	}
	return value
}

func providerHealthLabels(snapshot models.ProviderHealthSnapshot) map[string]string {
	reason := strings.TrimSpace(snapshot.ErrorCategory)
	if reason == "" {
		reason = strings.TrimSpace(snapshot.FailoverReason)
	}
	if reason == "" {
		reason = "none"
	}
	return map[string]string{
		"chain":         snapshot.ChainName,
		"chain_id":      strconv.FormatInt(int64(snapshot.ChainID), 10),
		"provider":      snapshot.ProviderLabel,
		"provider_hash": shortMetricHash(snapshot.ProviderURLHash),
		"status":        snapshot.Status,
		"reason":        reason,
	}
}

func providerHealthStatusValue(status string) float64 {
	switch status {
	case models.ProviderHealthStatusHealthy:
		return 1
	case models.ProviderHealthStatusDegraded:
		return 0.5
	default:
		return 0
	}
}

func shortMetricHash(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 12 {
		return value[:12]
	}
	return value
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
