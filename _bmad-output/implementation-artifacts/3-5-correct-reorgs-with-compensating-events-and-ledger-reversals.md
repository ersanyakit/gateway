---
story_id: "3.5"
story_key: "3-5-correct-reorgs-with-compensating-events-and-ledger-reversals"
epic: "Epic 3: Trustworthy Deposit Settlement & Ledger Balances"
status: ready-for-dev
created: 2026-06-27
updated: 2026-06-27
baseline_commit: 3e7e8eb
---

# Story 3.5: Correct Reorgs with Compensating Events and Ledger Reversals

Status: ready-for-dev

## Story

Bir operator olarak,
chain reorg'larinin etkiledigi deposit, payment, ledger entry, sweep ve webhook durumlarini destructive history edit yapmadan duzeltmesini istiyorum,
boylece sistem canonical chain degisimlerinden audit trail'i koruyarak recover edebilir.

## Acceptance Criteria

1. Given a block or slot at a processed height changes canonical hash or parent relationship, when the chain indexer detects the conflict, then affected chain facts and transactions are marked reorged or superseded, and a `transaction.reorged.v1` or equivalent correction event is emitted.
2. Given a reorg affects a ledger-posted deposit or payment, when correction processing runs, then the ledger records compensating reversal entries, and previously posted money history is not destructively edited.
3. Given a reorg affects a paid payment session, when correction processing evaluates the payment, then the payment lifecycle is corrected to failed, reverted, or policy-defined state, and a correction webhook/event is queued with reference to the original event id.
4. Given a reorg affects a pending sweep or downstream outbound action, when correction processing runs, then related sweep or outbound jobs are blocked, dead-lettered, or routed to reconciliation before retry, and blind retry does not occur.
5. Given reorg correction is implemented, when automated tests run, then they include deterministic fork simulation, duplicate reorg event handling, stale checkout/payment state, correction webhook, ledger reversal, and reconciliation job creation.

## Tasks / Subtasks

- [ ] Task 1: Harden reorg detection and correction event boundaries (AC: 1, 5)
  - [ ] Preserve the Story 3.1/architecture boundary: chain listeners/indexers produce chain facts and reorg signals; payment, ledger, sweep, webhook, and reconciliation mutations stay in repository/service correction boundaries.
  - [ ] Add deterministic fork simulations for same-height block hash conflicts, parent/child conflict detection where supported by the current model, and transaction block identity changes.
  - [ ] Ensure affected transactions move to `models.TransactionStatusReorged`, set `ReorgedAt`, reset delivery attempt fields, and expose the correction through the webhook boundary.
  - [ ] Replace raw reorg event strings in correction code with `constants.WebhookEventTransactionReorged` or a typed equivalent; preserve the canonical catalog entry `transaction.reorged.v1` and the legacy alias `transaction_reorged`.
  - [ ] Prove duplicate/replayed reorg signals do not create extra transaction correction events or move already-corrected transactions through inconsistent states.

- [ ] Task 2: Make correction processing atomic and idempotent (AC: 1, 2, 3, 4, 5)
  - [ ] Keep each affected transaction correction in one database transaction covering ledger reversal, payment correction, transaction status update, sweep job blocking, and reconciliation job creation.
  - [ ] Reuse existing repository boundaries (`TransactionRepo`, `LedgerRepo`, `PaymentRepo`, `SweepJobRepo`/model updates, `ReconciliationRepo`) instead of creating duplicate correction services unless a small coordinator removes real duplication.
  - [ ] Ensure repeated correction for the same `transaction_unique_hash` is a no-op for money movement: no duplicate reversal entries, no duplicate open reconciliation jobs, no duplicate sweep retries, and no duplicate terminal webhook intent.
  - [ ] Preserve GORM-owned schema discipline. If new fields or indexes are required, update model tags, `AutoMigrate`, `services/database.VerifySchema`, and schema contract tests; do not add raw SQL migration files in this story.
  - [ ] Keep idempotency keys stable and inspectable. Existing ledger reversal keys use `reorg-reversal:<ledger_entry_id>`; change only with matching tests and docs.

- [ ] Task 3: Correct payment lifecycle state after reorg (AC: 3, 5)
  - [ ] Extend `PaymentRepo.MarkReorgedByTransactionWithDB` beyond the current paid-only path so any terminal tx-linked matched outcome from Story 3.4 is corrected consistently: `paid`, `underpaid`, `overpaid`, `partial_paid`, and any expired-after-deposit/matched exception with `tx_unique_hash`.
  - [ ] Define one explicit policy for corrected payment sessions, preferably `failed` with correction metadata/event, unless an existing model already supports a clearer reverted state.
  - [ ] Keep the original tx reference and outcome audit data available; do not erase the original matching outcome or historical payment timestamps unless tests prove the replacement is non-destructive and contract-safe.
  - [ ] Queue the correction webhook/event by setting the payment webhook fields or outbox intent through the existing webhook boundary; include or derive `original_event_id`, `original_resource_id`, and `correction_reason` where catalog/docs require them.
  - [ ] Update checkout and V1 payment reads so a reorged transaction cannot leave stale `paid`, `overpaid`, `underpaid`, or `partial_paid` state visible as an active success/exception outcome.

- [ ] Task 4: Reverse ledger entries and block downstream money movement (AC: 2, 4, 5)
  - [ ] Preserve non-destructive ledger semantics: reversal entries compensate existing entries, and original posted money history remains queryable.
  - [ ] Harden `LedgerRepo.PostTransactionReversalWithDB` tests for deposit-available/payment-linked ledger entries, existing reversal entries, duplicate correction calls, and transactions with no ledger entries.
  - [ ] Ensure reversal logic does not reverse `reorg_reversal` entries, voided entries, or unrelated transaction hashes.
  - [ ] Dead-letter or block pending, processing, and failed sweep jobs tied to the reorged `transaction_unique_hash`; blind retry must not resume after source invalidation.
  - [ ] For succeeded or externally broadcast downstream actions, avoid automatic retry and route the case to reconciliation/operator follow-up with enough scoped context to investigate.

- [ ] Task 5: Complete webhook, catalog, reconciliation, and documentation contracts (AC: 1, 3, 4, 5)
  - [ ] Ensure `transaction.reorged.v1` payload tests cover `transaction_id`, `tx_unique_hash`, `original_event_id`, `original_resource_id`, and `correction_reason`.
  - [ ] Preserve legacy underscore event compatibility while keeping new public documentation focused on dotted versioned names.
  - [ ] Ensure `TransactionRepo.ListPendingWebhooks` or the outbox/webhook delivery path claims reorg corrections exactly once and cannot starve normal confirmed transaction webhooks.
  - [ ] Ensure `ReconciliationRepo.CreateOpenIfMissing` is used with deterministic chain/block/reason scope and remains idempotent for repeated correction runs.
  - [ ] Update `docs/money-event-catalog.md`, `docs/integration-guide.md`, and contract tests so merchants/operators can relate reorg corrections to prior transaction/payment events.

- [ ] Task 6: Tests, validation, and story record (AC: 1, 2, 3, 4, 5)
  - [ ] Add repository/service tests for deterministic fork simulation, duplicate reorg handling, ledger reversal idempotency, payment stale-state correction, correction webhook payload, sweep dead-letter/blocking, and reconciliation job creation.
  - [ ] Include integration/contract coverage for transaction correction event naming, original event references, and checkout/V1 payment state after reorg.
  - [ ] Targeted validation: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./repositories ./services/webhook ./api/handlers ./docs`.
  - [ ] Full validation: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./...`.
  - [ ] Static validation: `GOCACHE=/tmp/gateway-gocache-bmad go vet -p=1 ./...`.
  - [ ] Whitespace validation: `git diff --check && git diff --cached --check`.
  - [ ] Update Dev Agent Record, Completion Notes, File List, Change Log, and story status.

## Dev Notes

### Current Implementation Snapshot

- `repositories/transaction_repo.go` already has first-pass correction behavior in `markTransactionsReorgedWithDB`: it posts ledger reversals, calls `PaymentRepo.MarkReorgedByTransactionWithDB`, marks transactions `reorged`, resets transaction webhook attempt fields, dead-letters related sweep jobs, and creates an open reconciliation job.
- Current risk: `markTransactionsReorgedWithDB` writes raw `"transaction_reorged"` instead of using the constants/catalog vocabulary. Keep alias compatibility, but avoid new raw event strings.
- `repositories/ledger_repo.go` includes `PostTransactionReversalWithDB`. It creates compensating `reorg_reversal` entries, reverses direction, uses idempotency key `reorg-reversal:<ledger_entry_id>`, and skips already-voided or existing reorg reversal entries.
- `repositories/payment_repo.go` currently corrects only sessions with `tx_unique_hash = uniqueHash` and `status = paid`. Story 3.4 added explicit non-paid matched outcomes that may still be tx-linked and ledger-posted; this story must correct those states too.
- `models.SweepJob` supports `pending`, `processing`, `succeeded`, `failed`, and `dead_letter`. Current transaction correction dead-letters `pending`, `processing`, and `failed` jobs tied to the reorged transaction.
- `repositories/reconciliation_repo.go` has `CreateOpenIfMissing`, which dedupes open/processing reconciliation jobs by chain, block range, and reason.
- `services/webhook/event_catalog.go` already defines `transaction.reorged.v1` with correction semantics and legacy alias `transaction_reorged`.
- `docs/money-event-catalog.md` already states that `transaction.reorged.v1` includes `original_event_id`, `original_resource_id`, and `correction_reason`; implementation/tests must match that contract.

### Previous Story Intelligence

- Story 3.4 introduced explicit payment matching outcomes: exact `paid`, `underpaid`, `overpaid`, `partial_paid`, `partial_unsupported`, `expired_after_deposit`, `wrong_asset`, and `wrong_chain` outcome metadata.
- Story 3.4 also established that finalized funds on checkout wallets can remain ledger-eligible even when the payment lifecycle outcome is not `paid`. Reorg correction must reverse ledger effects by transaction hash, not by payment status assumptions.
- Story 3.3 established ledger-only balance authority. Payment sessions are lifecycle state, not balances; corrected balances must derive from ledger reversal entries.
- Recent validation uses an isolated Go cache because the global Go cache has been flaky: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./...` and `GOCACHE=/tmp/gateway-gocache-bmad go vet -p=1 ./...`.

### Architecture And Product Guardrails

- AD-4: Chain Indexer owns block/slot progress, raw extraction, finality, provider health, and reorg detection. Deposit/Payment/Ledger/Webhook components mutate business state after consuming those signals.
- AD-5/AD-9: money-affecting lifecycle changes need stable idempotency and versioned event/outbox semantics.
- AD-8: Webhook boundary owns URL validation, HMAC delivery, retry/backoff, dead-letter state, replay, and diagnostics. Correction events must use this boundary.
- AD-11: uncertain money states and drift recovery route through scoped reconciliation jobs.
- PRD FR15: reorg/correction must trigger payment lifecycle correction and correction webhook behavior.
- PRD FR34: reorg handling must include block hash continuity, parent/child tracking, rollback window behavior, affected ledger reversal, payment correction webhook, sweep dead-letter, and reconciliation job behavior.
- NFR6: corrections must use compensating ledger entries and correction events, not destructive edits to posted money history.
- NFR14: tests must include fork/reorg simulation, webhook retry/correction, ledger invariants, crash/retry recovery, `go test ./...`, and `go vet ./...`.

### Implementation Boundaries

- Build on the existing reorg path instead of replacing it wholesale: `TransactionRepo` already coordinates the affected transaction correction transaction.
- Do not introduce external brokers, new queue infrastructure, or a new chain simulator dependency for this story. Use deterministic repository/service tests unless the existing project already exposes a simulator helper.
- Keep tenant/domain isolation intact when correcting payments and emitting webhooks; do not infer or expose another merchant's payment state from a shared wallet/transaction record.
- Keep raw amount data as integer strings and reuse Story 3.4 payment outcome fields; do not add formatted decimal comparisons for correction decisions.
- Succeeded sweep/outbound cases are not safe to auto-retry from reorg correction. Route them to reconciliation/operator review unless existing withdrawal/sweep policy already defines a stricter safe behavior.

### Likely Files To Touch

- `repositories/transaction_repo.go`
- `repositories/transaction_repo_test.go`
- `repositories/ledger_repo.go`
- `repositories/ledger_repo_test.go`
- `repositories/payment_repo.go`
- `repositories/payment_repo_test.go`
- `repositories/reconciliation_repo.go`
- `repositories/reconciliation_repo_test.go`
- `models/transactions.go`
- `models/sweep_job.go`
- `constants/webhook_events.go`
- `services/webhook/event_catalog.go`
- `services/webhook/event_catalog_test.go`
- `api/handlers/payment.go`
- `api/handlers/payment_test.go`
- `api/handlers/v1api.go`
- `api/handlers/v1api_test.go` or existing V1 handler tests
- `docs/money-event-catalog.md`
- `docs/integration-guide.md`
- `docs/integration_contract_test.go`
- `services/database/database.go` and schema tests only if schema changes are needed

### Project Structure Notes

- This is a Go/Gofiber/GORM/PostgreSQL service. Keep repository tests close to `repositories/*_test.go`, handler contract tests close to `api/handlers`, and documentation contract tests in `docs`.
- The current codebase uses GORM models plus `services/database.VerifySchema` for schema contracts. Follow that pattern for any model change.
- Use existing constants/models for event names, transaction status, payment status, sweep job status, and ledger direction/type before adding new vocabulary.

### References

- Story source: `_bmad-output/planning-artifacts/epics.md` Story 3.5.
- PRD FR15, FR34, NFR6, NFR14: `_bmad-output/planning-artifacts/prds/prd-gateway-2026-06-27/prd.md`.
- Architecture AD-4, AD-5, AD-8, AD-9, AD-11 and reorg sequence: `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md`.
- Project rules: `_bmad-output/project-context.md`.
- Previous story: `_bmad-output/implementation-artifacts/3-4-model-payment-matching-outcomes-explicitly.md`.

## Dev Agent Record

### Agent Model Used

Codex

### Debug Log References

- 2026-06-27: Story created from Epic 3.5 with FR15/FR34, NFR6/NFR14, architecture reorg/correction guardrails, current repository correction path, and Story 3.4 payment outcome learnings.

### Completion Notes List

### File List

- `_bmad-output/implementation-artifacts/3-5-correct-reorgs-with-compensating-events-and-ledger-reversals.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

### Change Log

- 2026-06-27: Created story with reorg correction scope, atomic/idempotent correction requirements, payment lifecycle correction risks, ledger reversal guardrails, sweep/reconciliation behavior, webhook/docs contracts, and validation plan.
