---
story_id: "3.6"
story_key: "3-6-open-scoped-reconciliation-jobs-for-drift-and-uncertainty"
epic: "Epic 3: Trustworthy Deposit Settlement & Ledger Balances"
status: review
created: 2026-06-27
updated: 2026-06-27
baseline_commit: e748c00
---

# Story 3.6: Open Scoped Reconciliation Jobs for Drift and Uncertainty

Status: review

## Story

Bir operator olarak,
belirsiz veya drift yapan money state durumlarinin scoped reconciliation job acmasini istiyorum,
boylece chain facts, ledger entries, lifecycle state, webhook delivery ve outbound broadcast state recovery aksiyonundan once karsilastirilabilir.

## Acceptance Criteria

1. Given the system detects ledger invariant failure, chain/payment mismatch, stale finality, webhook correction failure, or stuck lifecycle state, when uncertainty is classified, then it opens a reconciliation job with bounded scope, reason, affected resource ids, tenant/domain context, and current status, and duplicate active jobs for the same issue are deduplicated.
2. Given a reconciliation job runs, when it gathers evidence, then it compares chain facts, ledger entries, payment/deposit lifecycle state, webhook delivery state, and outbound broadcast state where applicable, and records the evidence used for resolution.
3. Given a reconciliation outcome is determined, when the job completes, then it records resolved, needs-operator-action, retry-scheduled, or failed state, and any money-affecting correction is emitted as a compensating event.
4. Given reconciliation features are implemented, when automated tests run, then they cover invariant drift, duplicate job dedupe, reorg-created reconciliation, webhook drift, stuck lifecycle state, and operator-visible status.

## Tasks / Subtasks

- [x] Task 1: Reconciliation scope model and schema contract (AC: 1, 2, 3)
  - [x] Extend `models.ReconciliationJob` rather than creating a parallel table.
  - [x] Add bounded scope fields for tenant/domain context, resource type/id, stable scope key, affected resource ids, evidence JSON, outcome, and retry schedule metadata.
  - [x] Add statuses for `needs_operator_action` and `retry_scheduled` while preserving existing `open`, `processing`, `resolved`, and `failed`.
  - [x] Register all new columns and indexes in `services/database.VerifySchema` and schema tests.

- [x] Task 2: Scoped dedupe and evidence repository API (AC: 1, 2, 3)
  - [x] Keep `CreateOpenIfMissing` backward compatible for existing callers.
  - [x] Add a scoped create/open API that dedupes active jobs by a stable scope key, not only chain/from/to/reason.
  - [x] Validate and bound reason/scope/evidence payload sizes; never store secrets or raw signed payloads.
  - [x] Add repository methods for recording evidence and resolving to `resolved`, `needs_operator_action`, `retry_scheduled`, or `failed`.

- [x] Task 3: Open scoped jobs from existing uncertainty detectors (AC: 1, 4)
  - [x] Update ledger invariant reconciliation in `main.go` to include merchant/domain context and affected ledger idempotency key/resource scope.
  - [x] Update reserve reconciliation in `services/reconciliation/reserve.go` to include merchant/chain/symbol scope.
  - [x] Preserve Story 3.5 reorg-created reconciliation behavior and include affected transaction/block scope where available.
  - [x] Add focused coverage for webhook drift and stuck lifecycle state through repository/service detection helpers if no existing worker owns them.

- [x] Task 4: Evidence and operator-visible status tests (AC: 2, 3, 4)
  - [x] Test duplicate scoped job creation returns the existing active job.
  - [x] Test evidence JSON can be recorded and status transitions to resolved/operator action/retry scheduled/failed.
  - [x] Test active dedupe does not hide a new issue after the prior one is resolved.
  - [x] Test metrics/readiness status counting includes new active statuses.

- [x] Task 5: Documentation, story record, and validation (AC: 1, 2, 3, 4)
  - [x] Update Dev Agent Record, Completion Notes, File List, Change Log, and story status.
  - [x] Targeted validation: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./repositories ./services/reconciliation ./services/database ./api/handlers`.
  - [x] Postgres integration validation: `OUTBOX_TEST_DATABASE_URL='host=127.0.0.1 port=5432 user=postgres password=postgres dbname=gateway sslmode=disable' GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./repositories ./services/database`.
  - [x] Full validation: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./...`.
  - [x] Static validation: `GOCACHE=/tmp/gateway-gocache-bmad go vet -p=1 ./...`.
  - [x] Whitespace validation: `git diff --check && git diff --cached --check`.

## Dev Notes

### Current Implementation Snapshot

- `models.ReconciliationJob` currently stores `chain_id`, `from_block`, `to_block`, `reason`, `status`, `error`, `attempts`, `started_at`, and `resolved_at`.
- `repositories.ReconciliationRepo.CreateOpenIfMissing` dedupes only `open`/`processing` jobs by chain/from/to/reason. It must remain backward compatible because Story 3.3 ledger invariant checks, Story 3.5 reorg correction, and reserve reconciliation already call it.
- `main.go` runs `runLedgerInvariantReconciliation`, which currently opens reason-only reconciliation jobs after `LedgerRepo.FindInvariantIssues`.
- `services/reconciliation/reserve.go` opens reserve/liability reconciliation jobs using reason-only scope.
- `api/handlers/metrics.go` and `api/handlers/v1_readiness.go` count reconciliation jobs by unresolved status. New active statuses must be visible there.
- Story 3.5 now creates reconciliation jobs for reorgs, including parent mismatch block ranges. Do not break that path while broadening the model.

### Architecture And Product Guardrails

- AD-11: any uncertain money state must open a reconciliation job with bounded scope and reason; reconciliation compares chain facts, ledger entries, lifecycle state, webhook delivery, and broadcast state before resolution or retry.
- FR32: reconciliation boundary owns scoped recovery jobs and outcomes; manual-only recovery is not acceptable for uncertain money states.
- FR33: ledger invariant and reserve/liability drift must be visible before real-funds pilot.
- FR34: reorg-created reconciliation from Story 3.5 remains a required source of scoped reconciliation jobs.
- NFR6: any money-affecting correction must be compensating, not destructive. This story may record evidence/outcome; it must not silently mutate ledger money history.

### Implementation Boundaries

- Reuse `models.ReconciliationJob` and `repositories.ReconciliationRepo`; do not add a new queue, broker, or reconciliation table.
- Keep schema changes GORM-owned and add `VerifySchema` coverage.
- Store evidence as bounded JSON containing ids/status summaries only. Do not store webhook secrets, signatures, mnemonics, private keys, or raw sensitive payload bodies.
- Keep tenant/domain isolation intact. A scoped job may carry merchant/domain ids for operators, but public APIs must not expose cross-tenant existence.
- Treat `open`, `processing`, `needs_operator_action`, and `retry_scheduled` as active for dedupe. A resolved or failed job must not suppress a new later issue with the same scope.

### Likely Files To Touch

- `models/reconciliation_job.go`
- `repositories/reconciliation_repo.go`
- `repositories/reconciliation_repo_test.go`
- `main.go`
- `services/reconciliation/reserve.go`
- `services/reconciliation/reserve_test.go`
- `services/database/database.go`
- `services/database/database_test.go`
- `api/handlers/metrics.go`
- `api/handlers/v1_readiness.go`
- `api/handlers/*_test.go`
- `_bmad-output/implementation-artifacts/3-6-open-scoped-reconciliation-jobs-for-drift-and-uncertainty.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

### Previous Story Intelligence

- Story 3.5 established `transaction.reorged.v1`, compensating ledger reversals, payment failed correction, sweep dead-lettering, and reorg-created reconciliation jobs in one transaction.
- Story 3.5 added block canonicality and parent mismatch range scope. 3.6 should preserve the affected from/to block range and add richer job scope/evidence, not replace the reorg correction coordinator.
- Story 3.3 established ledger invariant reconciliation reason hashing to keep reasons within 120 characters. Reuse or preserve bounded reason behavior.

### References

- Story source: `_bmad-output/planning-artifacts/epics.md` Story 3.6.
- PRD FR32/FR33/FR34: `_bmad-output/planning-artifacts/prds/prd-gateway-2026-06-27/prd.md`.
- Architecture AD-11: `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md`.
- Project context: `_bmad-output/project-context.md`.
- Previous story: `_bmad-output/implementation-artifacts/3-5-correct-reorgs-with-compensating-events-and-ledger-reversals.md`.

## Dev Agent Record

### Agent Model Used

Codex

### Debug Log References

- 2026-06-27: Story created from Epic 3.6 with FR32/FR33/FR34, AD-11, existing reconciliation repo/model constraints, and Story 3.5 reorg scope learnings.
- 2026-06-27: Story moved to in-progress and implemented on the existing `ReconciliationJob`/`ReconciliationRepo` boundary.
- 2026-06-27: Added scoped reconciliation fields, active dedupe by `scope_key`, legacy `CreateOpenIfMissing` fallback, evidence recording, outcome statuses, retry scheduling, and sensitive evidence redaction.
- 2026-06-27: Updated ledger invariant, reserve, and reorg-created reconciliation producers to include scoped context and evidence; added webhook drift and stuck lifecycle helper coverage.
- 2026-06-27: Validation passed: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./repositories ./services/reconciliation ./services/database ./api/handlers`.
- 2026-06-27: Validation passed: `OUTBOX_TEST_DATABASE_URL='host=127.0.0.1 port=5432 user=postgres password=postgres dbname=gateway sslmode=disable' GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./repositories -run TestReconciliationRepo -v`.
- 2026-06-27: Validation passed: `OUTBOX_TEST_DATABASE_URL='host=127.0.0.1 port=5432 user=postgres password=postgres dbname=gateway sslmode=disable' GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./repositories ./services/database`.
- 2026-06-27: Validation passed: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./...`.
- 2026-06-27: Validation passed: `GOCACHE=/tmp/gateway-gocache-bmad go vet -p=1 ./...`.
- 2026-06-27: Validation passed: `git diff --check && git diff --cached --check`.
- 2026-06-27: Final validation rerun after JSONB default and evidence redaction assertion fixes passed: full `go test ./...`, full `go vet ./...`, and whitespace checks.

### Completion Notes List

- `ReconciliationJob` now carries merchant/domain, scope key, resource type/id, affected ids JSON, evidence JSON, outcome, and retry scheduling fields.
- Active reconciliation dedupe now uses stable `scope_key` for scoped jobs while `CreateOpenIfMissing` remains backward compatible with legacy chain/from/to/reason callers and pre-scope active rows.
- Evidence payloads are bounded and redact known sensitive keys before persistence; JSONB fields default to valid empty JSON values.
- Reconciliation outcomes now support `needs_operator_action` and `retry_scheduled`; due retry jobs are claimable while future retries wait.
- Ledger invariant, reserve drift, and Story 3.5 reorg reconciliation producers now open scoped jobs with evidence and affected resource ids.
- Repository helpers cover webhook drift and stuck lifecycle uncertainty scopes.
- Metrics and V1 readiness include `needs_operator_action` and `retry_scheduled` reconciliation statuses.

### File List

- `_bmad-output/implementation-artifacts/3-6-open-scoped-reconciliation-jobs-for-drift-and-uncertainty.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `api/handlers/metrics.go`
- `api/handlers/metrics_test.go`
- `api/handlers/v1_readiness.go`
- `api/handlers/v1_readiness_test.go`
- `main.go`
- `main_chain_fact_contract_test.go`
- `models/reconciliation_job.go`
- `repositories/reconciliation_repo.go`
- `repositories/reconciliation_repo_test.go`
- `repositories/transaction_repo.go`
- `services/database/database.go`
- `services/database/database_test.go`
- `services/reconciliation/reserve.go`

### Change Log

- 2026-06-27: Created story with scoped reconciliation schema, dedupe, evidence, outcome, detector integration, and validation scope.
- 2026-06-27: Implemented scoped reconciliation model, repository API, detector integrations, operator-visible statuses, tests, and validation; story moved to review.
- 2026-06-27: Tightened JSONB defaults and sensitive evidence redaction tests, then reran Postgres, full regression, static, and whitespace validation.
