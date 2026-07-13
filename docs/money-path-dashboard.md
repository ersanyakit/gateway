# Money Path Dashboard

This dashboard spec keeps production operations focused on low-cardinality money-path signals. Panels should link to `docs/money-path-observability-runbook.md`.

## Panels

| Panel | PromQL | Purpose |
| --- | --- | --- |
| Chain state age | `max by (chain, chain_id) (gateway_chain_state_age_seconds)` | Detect stale scanners. |
| Provider lag | `max by (chain, chain_id) (gateway_provider_lag_blocks)` | Detect lagging or stale RPC providers. |
| Provider health | `min by (chain, chain_id, status) (gateway_provider_health)` | Show healthy/degraded/unhealthy providers. |
| Webhook backlog | `sum by (status) (gateway_webhook_delivery_backlog)` | Track partner callback queue health. |
| Signer readiness | `gateway_production_signer_ready` and `gateway_signer_adapter_ready` | Confirm production signer boundary. |
| Sweep recovery | `sum by (status) (gateway_sweep_job_backlog)` | Surface sweep dead-letters and failed jobs. |
| Reconciliation drift | `sum by (status) (gateway_reconciliation_jobs)` | Surface unresolved money drift. |
| Migration readiness | `gateway_migration_strategy_ready` | Confirm production schema evidence. |

## Trace Drilldown

Dashboard links should preserve `request_id`, `trace_id`, chain, status and resource identifiers. Do not link with credentials, callback URLs containing credentials, request bodies or signature values.

## Review Cadence

- Review thresholds after every controlled-beta volume increase.
- Attach alert screenshots and incident notes to reconciliation or activity-log evidence when money state is affected.
