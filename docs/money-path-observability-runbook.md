# Money Path Observability Runbook

This runbook defines the baseline signals for controlled production operation. It is not a full exchange-grade NOC process.

## Metrics

`GET /metrics` exposes low-cardinality Prometheus gauges:

- `gateway_money_event_outbox_backlog` by status.
- `gateway_webhook_delivery_backlog` by status.
- `gateway_sweep_job_backlog` by status.
- `gateway_withdrawal_backlog` by status.
- `gateway_refund_backlog` by status.
- `gateway_reconciliation_jobs` by unresolved status.
- `gateway_chain_last_processed_block`, `gateway_chain_last_confirmed_block`, and `gateway_chain_state_age_seconds`.
- `gateway_provider_health`, `gateway_provider_latest_height`, `gateway_provider_lag_blocks`, `gateway_provider_response_latency_ms`, `gateway_provider_consecutive_failures`, and `gateway_provider_failover_decision`.
- `gateway_wallet_address_lookup_rows` by chain.
- `gateway_migration_strategy_ready` and `gateway_production_signer_ready`.

Metric labels must remain low-cardinality and safe. Provider labels are redacted; credentials, key material, raw signatures, and payload bodies must not appear in metrics or logs.

## Baseline Alerts

Prometheus alert rules live in `deploy/prometheus/gateway-alerts.yaml`.

Controlled-beta alert rules:

- Provider health: alert when any production chain has no healthy selected provider for 5 minutes.
- Chain lag: alert when chain state age exceeds 10 minutes or provider lag exceeds configured stale lag for 5 minutes.
- Webhooks: alert on any `dead_letter` delivery or failed deliveries increasing for 10 minutes.
- Money outbox: alert when `pending` or `processing` backlog grows for 10 minutes.
- Withdrawals/refunds: alert when `processing` rows remain for more than one finality window.
- Sweep jobs: alert on any `dead_letter` job or repeated `broadcast_uncertain` failure category.
- Reconciliation: alert on any `needs_operator_action` or unresolved drift older than 30 minutes.
- Signer: alert when `gateway_production_signer_ready` is `0` in production.
- Migration: alert when `gateway_migration_strategy_ready` is `0` in production.

Thresholds must be tightened before public real-funds production after first pilot volume is known.

## Logs And Traces

Production logs are structured JSON through the operational logger. Every HTTP request receives `X-Request-ID`; if a W3C `traceparent` header is present it is propagated into the request context, response headers and request logs as `trace_id`.

Operators should search by:

- `request_id` for API request, activity log and immediate handler diagnostics.
- `trace_id` for cross-service traces when an OpenTelemetry provider/exporter is installed.
- Money resource IDs such as payment ID, webhook delivery ID, sweep job ID, outbound transaction ID and reconciliation job ID.

Logs must not include credentials, key material, raw signatures, raw transaction payloads or request bodies. Sensitive log attributes are redacted before writing.

## Dashboard Panels

Baseline dashboard panels are documented in `docs/money-path-dashboard.md`. The minimum production dashboard should include:

- Provider health and chain-state age by chain.
- Webhook backlog and dead-letter count.
- Signer readiness and adapter health.
- Sweep job dead-letters and operator actions.
- Reconciliation jobs by unresolved status.
- Migration and metrics access readiness.

## Diagnostic Flow

### Chain Lag Or Provider Outage

- Check `gateway_provider_health` and selected provider labels.
- Confirm `PROVIDER_FAILOVER_STRATEGY`.
- Review chain listener logs by request id and chain id.
- If provider heads disagree, keep withdrawals paused for that chain until the canonical head is clear.

### Webhook Backlog Or Dead-Letter

- Inspect `webhook_deliveries` status and failure category.
- Verify merchant endpoint URL, TLS, and signature verification.
- Replay only after the merchant confirms idempotent event handling.

### Signer Readiness Failure

- Check `gateway_production_signer_ready` and `gateway_signer_adapter_ready`.
- In production, software signer mode is not acceptable for launch readiness.
- Confirm external signer adapter health before resuming outbound broadcasts.

### Sweep Dead-Letter Or Broadcast-Uncertain

- Inspect the `sweep_jobs` row, `operator_action`, `failure_category`, and linked reconciliation job.
- For broadcast-uncertain rows, inspect chain outcome before retry or replacement.
- Preserve ledger holds until chain evidence proves no spend happened.

### Stuck Withdrawal Or Refund

- Confirm a ledger hold exists before broadcast.
- Check signer readiness and broadcast/finality timestamps.
- If broadcast outcome is uncertain, open or update reconciliation before retrying.

### Reconciliation Drift

- Review unresolved `reconciliation_jobs`.
- Do not edit ledger history destructively.
- Use compensating entries or scoped reconciliation resolution notes.

### Emergency Freeze

- Enable outbound policy freeze for the affected merchant/domain/chain.
- Keep intake/webhooks observable, but stop new outbound broadcasts.
- Record operator, reason, affected scope, and correlation id in activity logs.

### Backup And Restore Drill

- Restore a recent backup into an isolated environment.
- Run `database.VerifySchema`.
- Run read-only balance, webhook, outbox, and reconciliation queries.
- Do not resume production traffic from a restored environment without explicit operator signoff.
