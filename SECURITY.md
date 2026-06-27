# Security Guide

This project handles payment and wallet-provider flows. Treat production access as high risk by default.

## Secrets

- Do not commit `.env`, mnemonic phrases, API secrets, webhook secrets, database passwords, private keys, or signer credentials.
- Generate `MASTER_KEY` from a secret manager or KMS-backed secret source. Use at least 32 bytes of entropy.
- `MNEMONIC_PHRASE` is development-only until a real external signer is integrated. In production, private key material must not be exposed to app logs, the app database, HTTP responses, or routine operator shells.
- Rotate merchant API/webhook secrets if any plaintext secret is suspected to have been exposed.

## Production Signing

- `SIGNER_MODE=software` uses process-local mnemonic/private-key derivation and is not production custody ready.
- `APP_ENV=production` blocks software signer use unless `ALLOW_SOFTWARE_SIGNER_IN_PRODUCTION=true` is explicitly set.
- `ALLOW_SOFTWARE_SIGNER_IN_PRODUCTION=true` is a controlled-pilot risk override, not a production-readiness signal. `/api/v1/common/readiness` reports it as not launch-ready.
- `SIGNER_MODE=kms`, `hsm`, or `mpc` currently declares intent only; production readiness requires a real provider adapter, policy metadata, audit logging, and chain-specific integration tests.

## Database Migrations

- Development can use startup `AutoMigrate`.
- `APP_ENV=production` disables startup `AutoMigrate` by default and verifies expected schema only.
- Run production schema changes through versioned migration files, reviewable DDL, backups, migration locks, and rollback plans.
- `ALLOW_AUTOMIGRATE_IN_PRODUCTION=true` should only be used in an explicit maintenance window and is reported as a readiness blocker.

## Webhooks And Network Egress

- Production webhook callback URLs must use public, routable endpoints. Private or loopback targets are rejected when `APP_ENV=production`.
- Validate webhook signatures with `X-Gateway-Signature`, `X-Gateway-Timestamp`, and `X-Gateway-Event`.
- Keep webhook replay, dead-letter, and retry diagnostics redacted; never expose stored secrets or raw sensitive payloads.

## Metrics

- `/metrics` exposes Prometheus-compatible operational gauges for backlog, reconciliation, chain state, migration policy, and signer policy.
- `APP_ENV=production` requires `METRICS_BEARER_TOKEN`; without it the endpoint returns `503`.
- Put `/metrics` behind private networking or reverse-proxy allowlists. Do not expose it as a public internet endpoint.

## HTTP Boundary

- Every request gets an `X-Request-ID` response header. Existing safe request IDs are preserved; malformed or very short IDs are replaced.
- Request logs are structured and deliberately limited to method, path without query string, matched route, status, duration, error type, and request id.
- Do not add request body, query string, `Authorization`, API keys, signatures, webhook secrets, mnemonic, private key, or raw payload fields to operational logs.
- Production should run with bounded `HTTP_READ_TIMEOUT`, `HTTP_WRITE_TIMEOUT`, and `HTTP_IDLE_TIMEOUT`; defaults are 15s, 30s, and 60s.
- Configure a stable `CSRF_JWT_SECRET`, `DEALER_SESSION_SECRET`, `SESSION_SECRET`, or `MASTER_KEY` before production launch so portal CSRF tokens survive process restarts.

## Launch Gate

Before real funds or public production traffic:

- `go test ./...` and `go vet ./...` pass in CI.
- `GET /api/v1/common/readiness` returns `200` in the target environment.
- External signer, reconciliation, backup/restore, alerting, and incident runbooks are validated.
- Initial launch uses small limits, canary merchants, monitored chain/webhook/sweep lag, and manual reconciliation.
