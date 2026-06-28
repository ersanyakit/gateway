---
story_id: "4.4"
story_key: "4-4-run-auto-sweeps-as-durable-recoverable-jobs"
epic: "Epic 4: Safe Outbound Funds & Custody Controls"
status: done
created: 2026-06-28
updated: 2026-06-28
baseline_commit: 7cbbd38156b72fd03a1e9febb471185f4045808e
---

# Story 4.4: Run Auto-Sweeps as Durable Recoverable Jobs

Status: done

## Story

As an operator,
I want auto-sweeps to run as durable jobs with retry and recovery state,
so that finalized deposits can be consolidated without losing work or duplicating broadcasts.

## Acceptance Criteria

1. Given a deposit becomes finalized and sweep-eligible, when sweep scheduling runs, then the system creates an idempotent persistent sweep job and repeated scheduling for the same deposit does not create duplicate sweep obligations.
2. Given a sweep worker claims work, when multiple workers or retries are active, then a job is claimed with lock semantics such as `FOR UPDATE SKIP LOCKED` or equivalent and the same job is not processed concurrently.
3. Given a sweep attempt fails transiently, when the worker records failure, then retry count, next attempt time, failure category, and exponential backoff are stored and the job remains recoverable.
4. Given a sweep reaches maximum attempts or an unrecoverable policy failure, when failure is terminal, then the job is marked dead-letter or needs-operator-action and related holds/resources are released or reconciled safely.
5. Given gas prefund or resource funding is needed, when the sweep job evaluates prerequisites, then prefund work is idempotent and linked to the parent sweep job and per-wallet concurrency policy prevents conflicting prefund/sweep actions.
6. Given durable sweep jobs are implemented, when automated tests run, then they cover idempotent scheduling, worker claim locking, retry/backoff, dead-letter, tx hash persistence, prefund idempotency, and recovery after process restart.

## Tasks / Subtasks

- [x] Task 1: Verify and complete durable sweep job persistence (AC: 1, 3, 4, 6)
  - [x] Keep using `models.SweepJob` and `repositories.SweepJobRepo`; do not create a second sweep queue abstraction.
  - [x] Add any missing recoverability fields required by AC3, especially failure category, without storing private keys, raw tx payloads, signatures, or unbounded diagnostic blobs.
  - [x] Ensure `services/database.VerifySchema` requires new sweep job columns/indexes when schema changes are made.
  - [x] Preserve `transaction_unique_hash` as the idempotency key for one sweep obligation per finalized deposit.

- [x] Task 2: Make finalized-deposit scheduling explicitly idempotent and restart-safe (AC: 1, 6)
  - [x] Confirm both finalized paths still call `enqueueSweepJob`: immediate confirmed handling and `finalizePendingTransactions`.
  - [x] Add DB-level tests proving repeated `EnqueueForTransaction` calls for the same `Transaction.UniqueHash` return the existing job and do not create duplicates.
  - [x] Verify non-user/reserve wallets (`HDAddressId == 0`) are not scheduled for sweeps.
  - [x] Keep `sweep.requested.v1` lifecycle webhook enqueue idempotent; do not emit duplicate requested events for an existing job.

- [x] Task 3: Fix worker claim semantics for stale processing recovery (AC: 2, 3, 6)
  - [x] `ClaimDue` must claim `pending`, retryable `failed`, and stale `processing` jobs whose `locked_until` has expired. Current code only selects `pending` and `failed`, which can strand a job after process crash.
  - [x] Preserve per-wallet/per-chain serialization. A wallet+chain with an active non-expired `processing` job must suppress other pending/failed/stale jobs for that wallet+chain.
  - [x] Preserve `FOR UPDATE OF sj SKIP LOCKED` for Postgres concurrency; if test helpers use SQLite, keep source-contract tests for this SQL plus behavior tests where practical.
  - [x] Add tests for two due jobs on the same wallet+chain: only the oldest due job is claimed; another wallet or chain can still be claimed.

- [x] Task 4: Persist retry/backoff and terminal failure classification (AC: 3, 4, 6)
  - [x] `MarkFailed` must increment attempts, store bounded error text, store failure category, clear locks, and set `next_run_at` using exponential backoff while attempts remain below max.
  - [x] At or above max attempts, status must become `dead_letter` or an explicit operator-action status if introduced; `next_run_at` and `locked_until` must be cleared.
  - [x] Broadcast-uncertain failures must remain non-retryable and route through `MarkBroadcastUncertain` plus scoped reconciliation.
  - [x] Pre-broadcast terminal failures may release/void the sweep ledger hold; post-broadcast uncertainty must preserve funds until reconciliation proves outcome.

- [x] Task 5: Harden gas prefund idempotency and parent linkage (AC: 5, 6)
  - [x] Keep prefund state on the parent sweep job unless a small child-job table is truly needed; do not add a broker or external queue.
  - [x] Prevent duplicate prefund broadcasts for the same parent job while `prefunded_at` is still within `SWEEP_PREFUND_RETRY_AFTER`.
  - [x] Persist prefund attempts and prefund last error through `MarkPrefunded` / `MarkPrefundFailed`; if failure category is added, align prefund failures with the same category vocabulary.
  - [x] Ensure prefund and sweep execution remain under the same per-wallet/per-chain claim policy so two jobs cannot concurrently fund/sweep the same wallet resources.

- [x] Task 6: Preserve Story 4.1/4.3 money safety in worker execution (AC: 2, 4, 5)
  - [x] `executeAutoSweepDepositWithJob` must still require a real `SweepJob` and `LedgerRepo`; do not re-enable legacy direct goroutine sweeps.
  - [x] Keep `LedgerRepo.CreateSweepHold` before signing/broadcast and `PostSweepRelease` only after the job is marked succeeded with a non-empty tx hash.
  - [x] Keep Story 4.3 broadcast-uncertain behavior: dead-letter plus scoped reconciliation for broadcast errors or missing tx hash, no blind retry.
  - [x] Preserve chain resource reservation paths in EVM, Bitcoin, Solana and TRON; do not bypass `services/chainresource`.

- [x] Task 7: Add targeted and regression validation evidence (AC: 1, 2, 3, 4, 5, 6)
  - [x] Add or expand repository tests for idempotent scheduling, claim locking/source contract, stale processing recovery, retry/backoff, max-attempt dead-letter, tx hash persistence, and prefund persistence.
  - [x] Add worker-level tests or source-contract guards for missing tx hash, broadcast-uncertain dead-letter, hold release/reconciliation split, and prefund duplicate suppression.
  - [x] Run targeted validation: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./repositories ./services/database`.
  - [x] Run sweep/main targeted validation: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 .`.
  - [x] Run full validation: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./...`.
  - [x] Run static validation: `GOCACHE=/tmp/gateway-gocache-bmad go vet -p=1 ./...`.
  - [x] Run whitespace validation: `git diff --check && git diff --cached --check`.

- [x] Task 8: Update story record and operational docs (AC: 3, 4, 5, 6)
  - [x] Update Dev Agent Record, Debug Log References, Completion Notes, File List and Change Log.
  - [x] If schema or public operational evidence changes, update `docs/product-readiness-audit.md`, `docs/payment-gateway-wallet-provider-audit.md`, readiness docs, or swagger only when behavior/API changed.
  - [x] Do not set story or sprint status to `review` until all tasks and validation pass.

### Review Findings

- [x] [Review][Patch] Finalized deposits can be missed by sweep scheduling [services/deposits/service.go:195] — fact-backed finalized deposits settle without creating a sweep job, and `finalizePendingTransactions` can mark finality before enqueueing; a crash in between can leave a finalized transaction outside pending-finality retry scans.
- [x] [Review][Patch] Reclaimed stale processing jobs can re-broadcast blindly [main.go:1038] — `ClaimDue` can return expired `processing` jobs for recovery, but the worker executes them like fresh jobs instead of first forcing reconciliation/operator action.
- [x] [Review][Patch] Sweep terminal updates lack fencing/status guards [repositories/sweep_job_repo.go:160] — `MarkSucceeded`, `MarkFailed`, and `MarkBroadcastUncertain` update by `id` only, so a late stale worker can overwrite newer terminal state.
- [x] [Review][Patch] Broadcast-uncertain mark failures are ignored [main.go:1058] — the worker opens reconciliation and emits dead-letter lifecycle events even when the DB state transition to dead-letter fails.
- [x] [Review][Patch] Broadcast reconciliation can be skipped after reload failure [main.go:1059] — if `Find` fails after marking broadcast uncertainty, no fallback reconciliation is opened from the claimed job.
- [x] [Review][Patch] Lock timeout may be shorter than sweep execution timeout [main.go:116] — a configured short `SWEEP_JOB_LOCK_TIMEOUT` can expire while the 90-second sweep attempt is still live.
- [x] [Review][Patch] MarkSucceeded accepts blank tx hash directly [repositories/sweep_job_repo.go:160] — repository-level success has no non-empty transaction hash guard.
- [x] [Review][Patch] Unrecoverable policy failures are classified but still retried [repositories/sweep_job_repo.go:176] — `MarkFailed` stores `failure_category` but only dead-letters at max attempts, contrary to the terminal policy-failure requirement.
- [x] [Review][Patch] Prefund retry gate ignores recent failed attempts [main.go:615] — `shouldAttemptSweepPrefund` only checks `prefunded_at`, so repeated failed/uncertain prefund attempts can happen inside the retry window.
- [x] [Review][Patch] Sweep job uniqueness/index evidence is incomplete [services/database/database.go:286] — schema verification requires columns but not the unique `transaction_unique_hash` index that enforces one sweep obligation per deposit.
- [x] [Review][Patch] Error truncation can split UTF-8 runes [repositories/sweep_job_repo.go:267] — byte-length slicing of diagnostic text can produce invalid UTF-8 before persistence.
- [x] [Review][Patch] Audit docs conflict with new sweep claims [docs/payment-gateway-wallet-provider-audit.md:103] — docs still contain older “sweep is not durable” language while later sections and product readiness now claim durable sweep jobs.

## Dev Notes

### Current Implementation Snapshot

- `models.SweepJob` already exists with `pending`, `processing`, `succeeded`, `failed`, and `dead_letter` statuses plus attempts, max attempts, last error, next run, lock, sweep tx hash, and prefund fields.
- `repositories.SweepJobRepo.EnqueueForTransaction` writes one job per `Transaction.UniqueHash` using `ON CONFLICT DO NOTHING` and returns an existing job for duplicate scheduling.
- `repositories.SweepJobRepo.ClaimDue` uses a Postgres `FOR UPDATE OF sj SKIP LOCKED` query and serializes claims by `wallet_id, chain_id`, but the current status filter only includes `pending` and `failed`. This is the main AC6 recovery gap: stale `processing` jobs with expired locks are not reclaimable after a process crash.
- `repositories.SweepJobRepo.MarkFailed` increments attempts, sets retry backoff, and dead-letters at max attempts, but it does not currently persist a failure category.
- `repositories.SweepJobRepo.MarkBroadcastUncertain` marks the job `dead_letter`, clears lock/retry fields, and stores error text.
- `main.go` schedules sweeps from finalized deposit paths, starts `startSweepJobWorker`, executes jobs with a signer audit context, requires `LedgerRepo.CreateSweepHold`, requires non-empty tx hash before success, posts ledger release after success, and opens scoped reconciliation for uncertain outcomes.
- `main.go` has `shouldAttemptSweepPrefund`, `markSweepPrefunded`, and `markSweepPrefundFailed`, but prefund idempotency is only represented by parent job fields and time gating. There is no separate child prefund job.
- Readiness and metrics already expose sweep backlog/dead-letter counts through `api/handlers/v1_readiness.go` and `api/handlers/metrics.go`.

### Architecture And Product Guardrails

- FR22 requires durable `sweep_jobs`-style persistence: claim, retry, exponential backoff, dead-letter, tx hash persistence, and recovery.
- FR23 requires gas prefund or chain funding sub-work to be idempotent and managed by sweep/withdrawal concurrency policy.
- AD-3 says `sweep_jobs` may hold lifecycle state but must not become an authoritative balance source.
- AD-7 says no outbound sweep may sign before ledger hold/reservation and chain-specific resource ownership exist; retry workers must reconcile existing broadcast state before replacement.
- AD-8 says sweep lifecycle events must use the webhook boundary, not inline callback delivery.
- Do not add Kafka/NATS/SQS or split a new service. This repo is still a Go modular monolith.
- Do not re-enable `autoSweepDeposit` as fire-and-forget behavior. It currently returns `repositories.ErrLedgerReservationRequired` and should stay non-operational unless the durable job repo is unavailable by explicit design.

### Previous Story Intelligence

- Story 4.1 made ledger holds mandatory for outbound withdrawal/refund/payout/sweep paths and established the `CreateSweepHold` -> broadcast -> `PostSweepRelease` ordering. Preserve that ordering.
- Story 4.2 added signer audit context and production signer boundary. `executeSweepJob` already sets `signer.AuditContext{JobID, CorrelationID}`; preserve this so signer audit logs tie back to the sweep job.
- Story 4.3 added chain resource reservation and broadcast-uncertain guard behavior. Do not turn broadcast errors back into normal retryable failures; that reopens blind retry risk.
- Epic 3 retrospective action items are still open: validation records must include targeted tests, full regression, vet, whitespace validation, and documented sandbox limits if any.

### Implementation Boundaries

- Keep changes story-scoped. Current worktree already has unrelated donation/payment-link, swagger/docs, webhook, and UI changes. Do not revert them and do not claim them as Story 4.4 unless you intentionally edit them for this story.
- Prefer narrow updates in `models/sweep_job.go`, `repositories/sweep_job_repo.go`, `main.go`, `main_sweep_reservation_test.go`, `services/database/database.go`, and focused tests.
- If adding status values such as `needs_operator_action`, update readiness/metrics/tests and keep terminal status semantics explicit.
- Bounded error/failure fields must avoid secrets and raw signed transactions.
- If schema fields are added, include schema verification tests. Production still needs versioned migrations later; do not claim migration discipline is solved by AutoMigrate.

### Testing Requirements

- Repository tests should exercise real persistence semantics where possible:
  - Duplicate enqueue by `transaction_unique_hash`.
  - Claim due respects lock timeout and per-wallet/per-chain rank.
  - Expired `processing` jobs are reclaimed.
  - Non-expired `processing` jobs suppress other jobs for the same wallet+chain.
  - Retry increments attempts and schedules exponential backoff.
  - Max attempts clears locks and dead-letters.
  - `MarkSucceeded` persists tx hash and clears retry fields.
  - Prefund success/failure persists attempt counters and errors.
- Worker tests may use source-contract guards if building full app fakes would be too invasive, but at least the pure classifiers (`sweepFailureBroadcastUncertain`, `sweepFailureLikelyBeforeBroadcast`, `shouldAttemptSweepPrefund`) should have behavior tests.
- Avoid live chain/RPC tests. Use deterministic DB/unit tests and source-contract tests for Postgres locking SQL where needed.

### Likely Files To Touch

- `models/sweep_job.go`
- `repositories/sweep_job_repo.go`
- `repositories/sweep_job_repo_test.go` (new or expanded)
- `main.go`
- `main_sweep_reservation_test.go`
- `services/database/database.go`
- `services/database/database_test.go`
- `api/handlers/v1_readiness.go` (only if statuses/categories change)
- `api/handlers/metrics.go` (only if statuses/categories change)
- `docs/product-readiness-audit.md` (only if readiness caveats change)
- `docs/payment-gateway-wallet-provider-audit.md` (only if durable sweep caveats change)
- `_bmad-output/implementation-artifacts/4-4-run-auto-sweeps-as-durable-recoverable-jobs.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

### References

- Story source: `_bmad-output/planning-artifacts/epics.md` Story 4.4.
- PRD FR22/FR23: `_bmad-output/planning-artifacts/prds/prd-gateway-2026-06-27/prd.md`.
- Architecture AD-3/AD-7/AD-8 and sweep module seed: `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md`.
- Current project rules: `_bmad-output/project-context.md`.
- Previous story: `_bmad-output/implementation-artifacts/4-3-reserve-chain-resources-and-apply-fee-gas-policy-before-broadcast.md`.
- Current audit caveat: `docs/payment-gateway-wallet-provider-audit.md` says durable sweep basics exist but gas prefund idempotency, per-wallet policy, and admin recovery remain open.
- Current product readiness caveat: `docs/product-readiness-audit.md` says sweep execution is durable but not exchange-grade and multi-replica reservation remains future work.

## Dev Agent Record

### Agent Model Used

Codex

### Debug Log References

- 2026-06-28: Story created from Epic 4.4 after Story 4.3 review completed. Existing sweep job model/repo/worker inspected; stale `processing` recovery, failure category persistence, prefund idempotency evidence, and DB-level tests identified as the core remaining scope.
- 2026-06-28: Confirmed red phase with `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./repositories`; build failed on missing `SweepJob` failure-category fields before implementation.
- 2026-06-28: Implemented durable sweep job recovery and validation; targeted repository/database, main package, full regression, vet, and whitespace checks passed.
- 2026-06-28: Code review findings applied; finalized deposit scheduling/backfill, stale processing reconciliation, terminal update fencing, prefund retry gating, schema index verification, and UTF-8-safe diagnostic redaction validated.

### Completion Notes List

- Added bounded sweep failure category fields for job and prefund failures without storing raw payloads, signatures, keys, or unbounded diagnostic text.
- Updated `SweepJobRepo.ClaimDue` to recover expired `processing` jobs while preserving per-wallet/per-chain serialization and active lock suppression.
- Classified retryable failures, terminal broadcast-uncertain failures, prefund failures, and success cleanup through the existing durable sweep job repository.
- Preserved finalized-deposit scheduling through `enqueueSweepJob`, reserve/non-user wallet skip behavior, idempotent requested lifecycle events, ledger hold before broadcast, success before ledger release, and broadcast-uncertain reconciliation.
- Added DB/source-contract coverage for idempotent enqueue, stale processing recovery, claim locking, retry/backoff, dead-letter, tx hash persistence, prefund persistence, scheduling paths, and worker safety.
- Updated operational audit notes to reflect bounded failure category persistence and parent-job prefund attempt/error/category state while keeping multi-replica resource ownership and operator recovery caveats open.
- Applied code review fixes: fact-backed finalized deposits enqueue sweep jobs, missing finalized sweeps are backfilled before claims, reclaimed stale processing jobs go to reconciliation instead of blind rebroadcast, terminal updates are fenced, policy failures dead-letter immediately, prefund failed attempts gate duplicate prefunds, and sweep schema indexes are verified.
- Validation passed:
  - `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./repositories ./services/database`
  - `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 .`
  - `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./...`
  - `GOCACHE=/tmp/gateway-gocache-bmad go vet -p=1 ./...`
  - `git diff --check && git diff --cached --check`

### File List

- `_bmad-output/implementation-artifacts/4-4-run-auto-sweeps-as-durable-recoverable-jobs.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `docs/payment-gateway-wallet-provider-audit.md`
- `docs/product-readiness-audit.md`
- `main.go`
- `main_sweep_reservation_test.go`
- `models/sweep_job.go`
- `repositories/sweep_job_repo.go`
- `repositories/sweep_job_repo_test.go`
- `services/database/database.go`
- `services/database/database_test.go`
- `services/deposits/service.go`
- `services/deposits/service_test.go`

### Change Log

- 2026-06-28: Created ready-for-dev Story 4.4 with durable sweep job recovery, retry classification, prefund idempotency, concurrency policy, and validation requirements.
- 2026-06-28: Implemented durable auto-sweep job recovery, classified retries/dead letters, prefund persistence, scheduling/worker guard tests, schema verification, and moved story to review.
- 2026-06-28: Resolved code review patch findings and moved story to done.
