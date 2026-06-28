---
story_id: "4.5"
story_key: "4-5-complete-withdrawal-payout-and-refund-lifecycle-events"
epic: "Epic 4: Safe Outbound Funds & Custody Controls"
status: done
created: 2026-06-28
updated: 2026-06-28
baseline_commit: fa0ecb1f2c2fafaa108da71b3eac8af096f17555
---

# Story 4.5: Complete Withdrawal, Payout, and Refund Lifecycle Events

Status: done

## Story

As a merchant or exchange operator,
I want outbound withdrawal, payout, and refund lifecycles to have clear approval, broadcast, finalization, failure, and notification states,
so that users and operators can understand where funds are and recover safely when something fails.

## Acceptance Criteria

1. Given a withdrawal, payout, or refund request is created, when validation passes, then the request records requester, tenant/domain, source wallet, destination, asset, amount, status, and idempotency/correlation metadata, and it emits or schedules the appropriate requested lifecycle event.
2. Given the request requires approval, when an admin or authorized operator approves or rejects it, then the action is scoped, audit logged, and reflected in request status, and maker-checker separation is enforced where configured.
3. Given an approved outbound request is broadcast, when broadcast succeeds, then the request records tx hash, chain, broadcast timestamp, and broadcast event, and downstream finality tracking can update terminal state.
4. Given finality confirms the outbound transaction, when terminal processing runs, then the ledger finalizes debit or releases hold according to outcome, and `withdrawal.finalized.v1`, `withdrawal.failed.v1`, `refund.succeeded.v1`, or equivalent event is queued.
5. Given lifecycle implementation is complete, when automated tests run, then they cover request validation, approval, rejection, broadcast success, broadcast failure, finalization, hold release, webhook/event enqueue, and idempotent repeated processing.

## Tasks / Subtasks

- [x] Task 1: Complete outbound lifecycle persistence and schema contracts (AC: 1, 3, 4, 5)
  - [x] Keep `models.WithdrawalRequest` as the current payout/withdrawal lifecycle table; do not introduce a parallel payout model.
  - [x] Add or verify persisted lifecycle metadata on withdrawal and refund records: requester, merchant/domain scope, source wallet, destination, asset, amount, current status, tx hash, broadcast timestamp, terminal timestamp, idempotency key, and correlation id.
  - [x] Record a source wallet for refunds as well as withdrawals. If refund source wallet is selected at approval time, persist it before broadcast and include it in response/payload contracts.
  - [x] Resolve the current withdrawal status ambiguity. Either introduce an explicit terminal status such as `finalized`, or document and test a deliberate compatibility alias if `approved` remains the public terminal name.
  - [x] Update `services/database.VerifySchema` and schema tests for any new lifecycle columns, indexes, or unique constraints.

- [x] Task 2: Make request creation idempotent and event-safe (AC: 1, 5)
  - [x] Add `Idempotency-Key` handling for V1 payout and refund create endpoints using the existing idempotency table/repository pattern. If the existing repo remains payment-specific, extend it narrowly with resource type/id support instead of adding a second mechanism.
  - [x] Repeated create requests with the same key and identical payload must return the same semantic response; the same key with a different payload must return conflict without creating another hold or request.
  - [x] Emit or enqueue `payout.requested.v1` / `withdrawal.requested.v1` compatibility output and `refund.requested.v1` only after the request and ledger hold commit successfully.
  - [x] Keep requested lifecycle event ids stable as `<resource_id>:<event_type>` and ensure duplicate create retries do not duplicate consumer-visible events.

- [x] Task 3: Complete approval, rejection, scope, and audit behavior (AC: 2, 5)
  - [x] Preserve merchant/domain scoping in V1 and admin/dealer paths before mutating any request.
  - [x] Ensure approval and rejection transitions are status-guarded and idempotent under repeat submissions.
  - [x] Rejection must release or void pre-broadcast ledger holds through `LedgerRepo`, then enqueue the rejected/failed lifecycle event through the webhook boundary.
  - [x] Use existing `ActivityLogRepo`/`logDealerActivity` for admin and operator actions; audit records must include actor, scope, action, outcome, subject id, reason/error, timestamp, request path, and correlation id where available.
  - [x] Enforce maker-checker separation where configuration exists. If no configuration exists yet, add a small explicit default-off guard with tests for the configured self-approval rejection path.

- [x] Task 4: Separate broadcast success/failure from terminal finalization (AC: 3, 4, 5)
  - [x] `RecordBroadcast` paths for withdrawals and refunds must require a non-empty tx hash, store broadcast timestamp, reviewed actor, chain/source wallet metadata, and leave the request in a non-terminal broadcast/processing state.
  - [x] Enqueue broadcast lifecycle events after the broadcast state commit succeeds: `payout.broadcast.v1` as the current compatibility name for withdrawal, and `refund.broadcast.v1` for refunds.
  - [x] Pre-broadcast validation or transfer failures must mark the request failed and release/void the hold.
  - [x] Post-broadcast uncertainty, missing tx hash persistence, or ledger-finalization failure must not blindly void funds or retry a second spend; keep the request recoverable and open scoped reconciliation/operator evidence where the existing reconciliation boundary can support it.
  - [x] Remove or gate any handler path that enqueues finalized/succeeded immediately after broadcast without finality evidence.

- [x] Task 5: Finalize terminal outcomes from finality evidence and ledger authority (AC: 4, 5)
  - [x] Terminal processing must be idempotent and status-guarded: repeated finalizer runs for the same tx hash/request must not double-post ledger entries or duplicate terminal events.
  - [x] Withdrawal finalization must post the ledger debit from the held funds only after finality evidence exists, then transition to the terminal status and enqueue `payout.finalized.v1` / canonical `withdrawal.finalized.v1` compatibility output.
  - [x] Refund finalization must post the refund debit from the held funds only after finality evidence exists, then transition to `succeeded` and enqueue `refund.succeeded.v1`.
  - [x] Confirmed terminal failure must release or reconcile the hold according to whether broadcast evidence exists, then enqueue `payout.failed.v1` / canonical `withdrawal.failed.v1` or `refund.failed.v1`.
  - [x] If outbound finality evidence is not yet available from existing chain fact/transaction repositories, keep broadcasted requests non-terminal and document the required reconciliation path instead of pretending broadcast equals finality.

- [x] Task 6: Align webhook lifecycle payloads and event catalog contracts (AC: 1, 3, 4, 5)
  - [x] Update `services/webhook.NewPayoutPayload` and `NewRefundPayload` so emitted payloads include the common money-event metadata required by the catalog: stable event id, event type/version, merchant/domain, resource/entity id, resource/entity status, idempotency key, correlation id, occurred timestamp, and lifecycle timestamps where present.
  - [x] Preserve existing `entity_*`, `payout.*`, and `refund.*` compatibility fields unless a documented versioned migration changes them.
  - [x] Verify `services/webhook/event_catalog.go` continues to map canonical `withdrawal.*` names to current `payout.*` aliases and includes refund requested/broadcast/succeeded/rejected/failed events.
  - [x] Keep delivery/retry/dead-letter concerns inside `WebhookDeliveryRepo` and the webhook boundary; do not call merchant HTTP endpoints inline from source flows.

- [x] Task 7: Update public API/admin surfaces and docs only where behavior changes (AC: 1, 2, 3, 4)
  - [x] Update V1 payout/refund responses and status tables to expose new lifecycle metadata without leaking internal diagnostics or cross-tenant information.
  - [x] Update admin/dealer views only if new statuses or timestamps need to be visible for operator recovery.
  - [x] Regenerate or update Swagger/integration docs for new idempotency headers, response fields, status names, and lifecycle event behavior.
  - [x] Keep UX consistent with the existing work-focused dashboard patterns; audit metadata must not be hidden behind hover-only UI.

- [x] Task 8: Add targeted and regression validation evidence (AC: 1, 2, 3, 4, 5)
  - [x] Add or expand repository tests for create idempotency, duplicate-key conflict, approval/rejection status guards, hold release, broadcast metadata persistence, non-empty tx hash guards, terminal finalization idempotency, and post-broadcast failure preservation.
  - [x] Add handler/source-contract tests for V1 payout/refund requested events, admin approval/rejection audit logs, broadcast events, failure events, and terminal events.
  - [x] Add webhook payload/catalog tests for common money-event metadata, payout alias compatibility, refund event coverage, and duplicate enqueue safety.
  - [x] Add finalizer tests for withdrawal/refund terminal processing and repeated processing.
  - [x] Run targeted validation: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./repositories ./services/webhook ./services/database`.
  - [x] Run API/main targeted validation: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 . ./api/handlers`.
  - [x] Run full validation: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./...`.
  - [x] Run static validation: `GOCACHE=/tmp/gateway-gocache-bmad go vet -p=1 ./...`.
  - [x] Run whitespace validation: `git diff --check && git diff --cached --check`.

- [x] Task 9: Update story record and readiness evidence (AC: 1, 2, 3, 4, 5)
  - [x] Update Dev Agent Record, Debug Log References, Completion Notes, File List, and Change Log during implementation.
  - [x] If lifecycle/schema/API/docs behavior changes, update readiness or audit docs with precise claims and remaining caveats.
  - [x] Do not set story or sprint status to `review` until all tasks and validation pass.

### Review Findings

- [x] [Review][Patch] Idempotency completion failure leaves committed payout/refund holds unreplayable [api/handlers/v1api.go:1240] - fixed by surfacing completion failures and repairing duplicate-safe requested lifecycle enqueue on idempotent replay.
- [x] [Review][Patch] Requested lifecycle enqueue failures are logged after successful create/idempotency completion with no durable repair record [api/handlers/v1api.go:1243] - fixed by returning retryable create failures and opening reconciliation for dealer requested-event enqueue failures.
- [x] [Review][Patch] Refund ledger hold/debit uses payment session wallet while approval persists a potentially different source wallet [repositories/ledger_repo.go:596] - fixed by using the persisted refund source wallet for refund hold/debit attribution, including alignment of pre-existing pending refund holds.
- [x] [Review][Patch] Broadcasted terminal failed transactions are never processed into failed/reconciled withdrawal or refund outcomes [main.go:1407] - fixed by detecting failed transaction evidence, preserving post-broadcast holds, opening reconciliation, and queueing failed lifecycle events.
- [x] [Review][Patch] Terminal lifecycle events can be lost after ledger finalization commits [main.go:1424] - fixed by opening scoped reconciliation when terminal lifecycle enqueue fails after ledger finalization.
- [x] [Review][Patch] Approval and rejection retries return failures instead of idempotent no-op outcomes [api/handlers/dealer.go:2725] - fixed with handler-level status guards for repeated approve/reject submissions.
- [x] [Review][Patch] Refund approval audit logs drop merchant scope on several failure and success branches [api/handlers/dealer.go:2861] - fixed by preserving refund merchant scope in failure and success audit branches.
- [x] [Review][Patch] Admin recover fund transfers bypass configured maker-checker lifecycle guard [api/handlers/dealer.go:2279] - fixed by applying the configured maker-checker guard before admin recover fund approval.
- [x] [Review][Patch] Admin recover fund success does not enqueue the payout broadcast lifecycle event [api/handlers/dealer.go:2320] - fixed by enqueueing the payout broadcast lifecycle event after successful recover-fund broadcast persistence.

## Dev Notes

### Current Implementation Snapshot

- `models.WithdrawalRequest` currently has statuses `pending`, `processing`, `approved`, `rejected`, and `failed`. It stores merchant/domain, wallet, chain/token/symbol/decimals, destination, amount, requester/reviewer, reviewed timestamp, tx hash, and error. It does not currently store broadcast timestamp, terminal timestamp, idempotency key, or correlation id.
- `models.Refund` currently has statuses `pending`, `processing`, `approved`, `rejected`, `succeeded`, and `failed`. It stores merchant/domain/payment, amount, reason, status, tx hash, error, requester/reviewer, and reviewed timestamp. It does not currently store source wallet, destination address, asset metadata, broadcast timestamp, terminal timestamp, idempotency key, or correlation id.
- `repositories.WithdrawalRequestRepo.CreateWithHold` creates the request and withdrawal hold in one transaction. `ApproveWithTransfer` checks an existing hold, performs the transfer callback, records broadcast, posts ledger debit, and currently returns an approved request in one flow.
- `repositories.WithdrawalRequestRepo.RecordBroadcast` and `repositories.RefundRepo.RecordBroadcast` currently store `tx_hash` and set an interim error, but do not store a broadcast timestamp.
- `repositories.RefundRepo.ClaimPendingWithHold`, `RecordBroadcast`, and `MarkSucceededWithLedger` provide the basic refund approval/broadcast/finalization path; the handler currently broadcasts and succeeds a refund in the same request when ledger posting succeeds.
- `main.go` has `finalizeProcessingTransfers`, which scans processing withdrawals/refunds with tx hashes and posts terminal ledger state. The current implementation should be reviewed carefully because Story 4.5 requires finality evidence, not just tx hash presence.
- `api/handlers/v1api.go` already enqueues requested lifecycle webhooks for V1 payout and refund creates after `CreateWithHold`, but the create endpoints do not currently expose idempotency handling.
- `api/handlers/dealer.go` enqueues payout/refund broadcast, finalized/succeeded, rejected, and failed lifecycle events from admin flows and writes `ActivityLog` records for high-risk actions.
- `services/webhook/lifecycle.go` creates stable event ids as `<entity_id>:<event_type>` for payout, refund, and sweep payloads. The current payload shape lacks some catalog common fields such as `occurred_at`, `resource_type`, `resource_id`, `resource_status`, `idempotency_key`, and `correlation_id`.
- `services/webhook/event_catalog.go` already defines canonical `withdrawal.*` events and maps current `payout.*` event names as compatibility aliases. Refund requested, broadcast, succeeded, rejected, and failed events are already present in the catalog.
- `repositories.IdempotencyRepo` and `models.IdempotencyKey` exist for payment create behavior. They may need narrow generalization because the current stored resource reference is payment-session specific.
- `models.ActivityLog` and `repositories.ActivityLogRepo` exist and are already used by dealer/admin handlers. Story 4.6 will harden broader admin security controls later; Story 4.5 should only add what is needed for lifecycle approval/rejection auditability.

### Architecture And Product Guardrails

- FR21 requires withdrawal, payout, and refund lifecycle states for requested, approved, broadcast, finalized/succeeded, failed, and notified outcomes.
- FR26 requires source modules to enqueue versioned lifecycle events and leave delivery/retry/dead-letter work to the webhook boundary.
- FR10 and AD-5 require stable idempotency for money-affecting transitions and duplicate-delivery-safe event ids.
- AD-3 says lifecycle tables are not balance authority. Ledger holds/debits/releases remain the authoritative money movement evidence.
- AD-7 says outbound withdrawal/refund/sweep flows require ledger hold and chain resource reservation before signing, and retry/recovery must reconcile existing broadcast state before replacement.
- AD-8 says merchant webhook HTTP delivery must not happen inline in source money flows.
- NFR13 requires high-risk admin/operator actions to be auditable without exposing secrets, raw signatures, private keys, mnemonics, or cross-tenant details.

### Previous Story Intelligence

- Story 4.1 made ledger holds mandatory before outbound withdrawal/refund/payout/sweep signing and established pre-broadcast hold release versus post-broadcast preservation/reconciliation.
- Story 4.2 added signer audit context and production software-signer guardrails. Admin withdrawal/refund paths already pass signer correlation ids; preserve and persist safe correlation metadata where useful.
- Story 4.3 added chain resource reservation and broadcast-uncertain handling. Do not convert post-broadcast uncertainty into a normal retry that can create a second spend.
- Story 4.4 added durable sweep jobs, idempotent requested events, broadcast-uncertain dead-letter/reconciliation behavior, and validation discipline. Reuse those patterns for outbound lifecycle recovery where they fit.
- Epic 3 retrospective action items remain open: event/API docs must stay synchronized, outbound reconciliation reuse must be tested, and validation records must include targeted tests, full regression, vet, and whitespace validation.

### Implementation Boundaries

- Keep changes story-scoped. The current worktree already has unrelated payment, docs, UI, and Story 4.4 changes. Do not revert or claim unrelated changes.
- Do not add Kafka/NATS/SQS or split a new service. This repo is still a Go modular monolith.
- Do not introduce a new public event family that breaks current `payout.*` consumers. Canonical `withdrawal.*` names may be represented through catalog aliases until a versioned migration changes emission.
- Do not mark a request terminal merely because a broadcast returned a tx hash. If finality evidence is missing, keep the request recoverable and make the limitation explicit in tests/docs.
- Do not store private keys, mnemonics, raw signed transactions, signatures, webhook secrets, API secrets, or unbounded diagnostics in lifecycle records, events, or audit logs.
- Prefer narrow updates in the existing models, repositories, handlers, lifecycle payloads, and tests before adding new abstractions.

### Testing Requirements

- Repository tests should exercise DB-backed behavior where possible:
  - V1-style idempotent create keys and conflict behavior.
  - Withdrawal/refund hold creation, duplicate create retry, and no duplicate ledger hold.
  - Approval/rejection status guards and hold release.
  - Broadcast metadata persistence, non-empty tx hash guard, and duplicate broadcast handling.
  - Terminal finalization idempotency and duplicate ledger-post prevention.
  - Failed paths before broadcast release holds; failed or uncertain paths after broadcast preserve funds for reconciliation.
- Handler/source-contract tests should cover:
  - Requested event enqueue after create commit.
  - Admin/dealer approval and rejection audit logs.
  - Broadcast event enqueue only after tx hash persistence.
  - Terminal event enqueue only after terminal state commit.
  - Maker-checker configured self-approval rejection.
- Webhook tests should cover:
  - Stable event id and idempotency key semantics.
  - Common money-event metadata in payout/refund lifecycle payloads.
  - Payout alias compatibility for canonical withdrawal catalog entries.
  - Refund requested/broadcast/succeeded/rejected/failed catalog coverage.

### Likely Files To Touch

- `models/withdrawal_request.go`
- `models/refund.go`
- `models/idempotency_key.go` (only if idempotency resource references need generalization)
- `repositories/withdrawal_request_repo.go`
- `repositories/refund_repo.go`
- `repositories/idempotency_repo.go` (only if create idempotency is generalized)
- `repositories/withdrawal_request_repo_test.go`
- `repositories/refund_repo_test.go`
- `api/handlers/v1api.go`
- `api/handlers/v1api_test.go`
- `api/handlers/dealer.go`
- `api/handlers/dealer_test.go`
- `main.go`
- `main_sweep_reservation_test.go` or a new focused finalizer test file
- `services/webhook/lifecycle.go`
- `services/webhook/lifecycle_test.go`
- `services/webhook/event_catalog.go`
- `services/webhook/event_catalog_test.go`
- `services/database/database.go`
- `services/database/database_test.go`
- `types/v1api.go`
- `docs/integration-guide.md`
- `docs/swagger.yaml`
- `docs/swagger.json`
- `docs/docs.go`
- `docs/product-readiness-audit.md` (only if readiness claims change)
- `docs/payment-gateway-wallet-provider-audit.md` (only if audit caveats change)
- `_bmad-output/implementation-artifacts/4-5-complete-withdrawal-payout-and-refund-lifecycle-events.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

### References

- Story source: `_bmad-output/planning-artifacts/epics.md` Story 4.5.
- PRD FR10, FR21, FR26, FR28, FR30, FR31, FR39: `_bmad-output/planning-artifacts/prds/prd-gateway-2026-06-27/prd.md`.
- Architecture AD-3, AD-5, AD-7, AD-8, AD-11: `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md`.
- UX audit-row and status-display guidance: `_bmad-output/planning-artifacts/ux-designs/ux-gateway-2026-06-27/DESIGN.md`.
- Current project rules: `_bmad-output/project-context.md`.
- Previous story: `_bmad-output/implementation-artifacts/4-4-run-auto-sweeps-as-durable-recoverable-jobs.md`.
- Current event catalog implementation: `services/webhook/event_catalog.go`.
- Current lifecycle payload implementation: `services/webhook/lifecycle.go`.

## Dev Agent Record

### Agent Model Used

Codex

### Debug Log References

- 2026-06-28: Story created from Epic 4.5 after Story 4.4 was completed. Existing withdrawal/refund models, repositories, V1/admin handlers, lifecycle payloads, event catalog, idempotency repo, activity logs, and finalization worker inspected. Core gaps identified: payout/refund create idempotency, refund source wallet metadata, broadcast/terminal timestamps, withdrawal terminal status ambiguity, terminal finalization without explicit finality evidence, and payload common metadata alignment.
- 2026-06-28: Implemented Story 4.5 lifecycle separation: outbound persistence metadata, idempotent V1 payout/refund create, maker-checker default-off guard, broadcast-only admin approval, finality-gated terminal worker, common lifecycle payload metadata, schema/index contracts, Swagger/integration docs.
- 2026-06-28: Validation passed: targeted repositories/webhook/database/api/main/docs, full go test ./..., go vet ./..., git diff --check && git diff --cached --check.
- 2026-06-28: Code review follow-up applied: idempotency completion/enqueue failures are retry-safe, requested lifecycle repair runs on replay, refund ledger attribution follows the persisted source wallet, broadcast-uncertain admin paths preserve holds and open reconciliation, terminal failed transaction evidence is processed, terminal event enqueue failures open reconciliation, refund audit scope includes merchant id, admin recover funds uses maker-checker and emits broadcast events, and approve/reject retries are idempotent no-ops.
- 2026-06-28: Post-review validation passed: targeted repositories/database/webhook, main/api handlers, full package-set `go test` coverage via clean-cache package-group reruns, go vet ./..., and git diff whitespace checks.
- 2026-06-28: Donation refund follow-up applied: V1 refund limit calculation now uses paid donation `matched_amount_raw` when fixed expected amount is zero or absent.

### Completion Notes List

- Withdrawal/payout now uses explicit `finalized` terminal status after transaction finality; broadcast leaves the request `processing`.
- Refund approval persists source wallet, destination, and asset metadata; refund hold/debit ledger attribution follows that source wallet; success is produced only by the finality-gated terminal worker.
- V1 payout/refund create endpoints use `Idempotency-Key` through generic idempotency resource references and enqueue requested events after commit.
- Lifecycle webhook payloads include common money-event metadata, and Swagger/integration docs were regenerated for the behavior changes.
- Review fixes preserve post-broadcast funds on uncertain outcomes, create repair evidence for lost lifecycle enqueue, and record audit correlation ids for dealer/admin activity logs.
- Donation payment refunds now cap against the matched paid amount when donation links do not carry a fixed expected raw amount.

### File List

- `_bmad-output/implementation-artifacts/4-5-complete-withdrawal-payout-and-refund-lifecycle-events.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `api/handlers/dealer.go`
- `api/handlers/dealer_test.go`
- `api/handlers/v1api.go`
- `api/handlers/v1api_test.go`
- `api/routes/routes.go`
- `docs/docs.go`
- `docs/integration-guide.md`
- `docs/swagger.json`
- `docs/swagger.yaml`
- `go.sum`
- `main.go`
- `main_sweep_reservation_test.go`
- `models/activity_log.go`
- `models/idempotency_key.go`
- `models/refund.go`
- `models/withdrawal_request.go`
- `repositories/idempotency_repo.go`
- `repositories/idempotency_repo_test.go`
- `repositories/refund_repo.go`
- `repositories/refund_repo_test.go`
- `repositories/transaction_repo.go`
- `repositories/transaction_repo_test.go`
- `repositories/withdrawal_request_repo.go`
- `repositories/withdrawal_request_repo_test.go`
- `services/database/database.go`
- `services/database/database_test.go`
- `services/webhook/event_catalog.go`
- `services/webhook/event_catalog_test.go`
- `services/webhook/lifecycle.go`
- `services/webhook/lifecycle_test.go`
- `types/v1api.go`

### Change Log

- 2026-06-28: Created ready-for-dev Story 4.5 with lifecycle persistence, idempotent create, approval/audit, broadcast/finality separation, webhook payload, API/docs, and validation requirements.
- 2026-06-28: Implemented outbound withdrawal/payout/refund lifecycle persistence, idempotent create, broadcast/finality separation, webhook payload metadata, API/docs updates, and validation; moved story to review.
- 2026-06-28: Applied code-review fixes for retry-safe idempotency completion, requested-event repair/reconciliation, broadcast uncertainty preservation, failed transaction terminal handling, terminal event reconciliation, maker-checker recover funds, scoped refund audit logs, idempotent admin retries, and moved story to done.
- 2026-06-28: Fixed donation refund limit fallback so donation refunds use matched paid amount instead of a zero expected amount.
