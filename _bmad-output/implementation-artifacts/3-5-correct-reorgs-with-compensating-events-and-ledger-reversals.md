---
story_id: "3.5"
story_key: "3-5-correct-reorgs-with-compensating-events-and-ledger-reversals"
epic: "Epic 3: Trustworthy Deposit Settlement & Ledger Balances"
status: review
created: 2026-06-27
updated: 2026-06-27
baseline_commit: eb422ee
---

# Story 3.5: Correct Reorgs with Compensating Events and Ledger Reversals

Status: review

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

- [x] Task 1: Harden reorg detection and correction event boundaries (AC: 1, 5)
  - [x] Preserve the Story 3.1/architecture boundary: chain listeners/indexers produce chain facts and reorg signals; payment, ledger, sweep, webhook, and reconciliation mutations stay in repository/service correction boundaries.
  - [x] Use or harden the existing `models.Block` parent/hash model instead of inventing a parallel block table. If block tracking becomes active, register it in `autoMigrateModels`, `VerifySchema`, and schema contract tests.
  - [x] Add explicit status/reorg metadata for affected chain facts and deposits if the current models cannot represent `reorged` or `superseded` without destructive edits.
  - [x] Add deterministic fork simulations for same-height block hash conflicts, parent/child conflict detection where supported by the current model, and transaction block identity changes.
  - [x] Ensure affected transactions move to `models.TransactionStatusReorged`, set `ReorgedAt`, reset delivery attempt fields, and expose the correction through the webhook boundary.
  - [x] Replace raw reorg event strings in correction code with `constants.WebhookEventTransactionReorged` or a typed equivalent; preserve the canonical catalog entry `transaction.reorged.v1` and the legacy alias `transaction_reorged`.
  - [x] Prove duplicate/replayed reorg signals do not create extra transaction correction events or move already-corrected transactions through inconsistent states.

- [x] Task 2: Make correction processing atomic and idempotent (AC: 1, 2, 3, 4, 5)
  - [x] Keep each affected transaction correction in one database transaction covering ledger reversal, payment correction, transaction status update, sweep job blocking, and reconciliation job creation.
  - [x] Reuse existing repository boundaries (`TransactionRepo`, `LedgerRepo`, `PaymentRepo`, `SweepJobRepo`/model updates, `ReconciliationRepo`) instead of creating duplicate correction services unless a small coordinator removes real duplication.
  - [x] Ensure repeated correction for the same `transaction_unique_hash` is a no-op for money movement: no duplicate reversal entries, no duplicate open reconciliation jobs, no duplicate sweep retries, and no duplicate terminal webhook intent.
  - [x] Preserve GORM-owned schema discipline. If new fields or indexes are required, update model tags, `AutoMigrate`, `services/database.VerifySchema`, and schema contract tests; do not add raw SQL migration files in this story.
  - [x] Keep idempotency keys stable and inspectable. Existing ledger reversal keys use `reorg-reversal:<ledger_entry_id>`; change only with matching tests and docs.

- [x] Task 3: Correct payment lifecycle state after reorg (AC: 3, 5)
  - [x] Extend `PaymentRepo.MarkReorgedByTransactionWithDB` beyond the current paid-only path so any terminal tx-linked matched outcome from Story 3.4 is corrected consistently: `paid`, `underpaid`, `overpaid`, `partial_paid`, and any expired-after-deposit/matched exception with `tx_unique_hash`.
  - [x] Define one explicit policy for corrected payment sessions, preferably `failed` with correction metadata/event, unless an existing model already supports a clearer reverted state.
  - [x] Keep the original tx reference and outcome audit data available; do not erase the original matching outcome or historical payment timestamps unless tests prove the replacement is non-destructive and contract-safe.
  - [x] Queue the correction webhook/event by setting the payment webhook fields or outbox intent through the existing webhook boundary; include or derive `original_event_id`, `original_resource_id`, and `correction_reason` where catalog/docs require them.
  - [x] Update checkout and V1 payment reads so a reorged transaction cannot leave stale `paid`, `overpaid`, `underpaid`, or `partial_paid` state visible as an active success/exception outcome.

- [x] Task 4: Reverse ledger entries and block downstream money movement (AC: 2, 4, 5)
  - [x] Preserve non-destructive ledger semantics: reversal entries compensate existing entries, and original posted money history remains queryable.
  - [x] Harden `LedgerRepo.PostTransactionReversalWithDB` tests for deposit-available/payment-linked ledger entries, existing reversal entries, duplicate correction calls, and transactions with no ledger entries.
  - [x] Ensure reversal logic does not reverse `reorg_reversal` entries, voided entries, or unrelated transaction hashes.
  - [x] Dead-letter or block pending, processing, and failed sweep jobs tied to the reorged `transaction_unique_hash`; blind retry must not resume after source invalidation.
  - [x] For succeeded or externally broadcast downstream actions, avoid automatic retry and route the case to reconciliation/operator follow-up with enough scoped context to investigate.

- [x] Task 5: Complete webhook, catalog, reconciliation, and documentation contracts (AC: 1, 3, 4, 5)
  - [x] Ensure `transaction.reorged.v1` payload tests cover `transaction_id`, `tx_unique_hash`, `original_event_id`, `original_resource_id`, and `correction_reason`.
  - [x] Preserve legacy underscore event compatibility while keeping new public documentation focused on dotted versioned names.
  - [x] Ensure `TransactionRepo.ListPendingWebhooks` or the outbox/webhook delivery path claims reorg corrections exactly once and cannot starve normal confirmed transaction webhooks.
  - [x] Ensure `ReconciliationRepo.CreateOpenIfMissing` is used with deterministic chain/block/reason scope and remains idempotent for repeated correction runs.
  - [x] Update `docs/money-event-catalog.md`, `docs/integration-guide.md`, and contract tests so merchants/operators can relate reorg corrections to prior transaction/payment events.

- [x] Task 6: Tests, validation, and story record (AC: 1, 2, 3, 4, 5)
  - [x] Add repository/service tests for deterministic fork simulation, duplicate reorg handling, ledger reversal idempotency, payment stale-state correction, correction webhook payload, sweep dead-letter/blocking, and reconciliation job creation.
  - [x] Include integration/contract coverage for transaction correction event naming, original event references, and checkout/V1 payment state after reorg.
  - [x] Targeted validation: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./repositories ./services/webhook ./api/handlers ./docs`.
  - [x] Full validation: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./...`.
  - [x] Static validation: `GOCACHE=/tmp/gateway-gocache-bmad go vet -p=1 ./...`.
  - [x] Whitespace validation: `git diff --check && git diff --cached --check`.
  - [x] Update Dev Agent Record, Completion Notes, File List, Change Log, and story status.

## Dev Notes

### Current Implementation Snapshot

- `repositories/transaction_repo.go` already has first-pass correction behavior in `markTransactionsReorgedWithDB`: it posts ledger reversals, calls `PaymentRepo.MarkReorgedByTransactionWithDB`, marks transactions `reorged`, resets transaction webhook attempt fields, dead-letters related sweep jobs, and creates an open reconciliation job.
- Current risk: `markTransactionsReorgedWithDB` writes raw `"transaction_reorged"` instead of using the constants/catalog vocabulary. Keep alias compatibility, but avoid new raw event strings.
- `models.Block` exists with `ChainID`, `Number`, `Hash`, `ParentHash`, and `Processed`, but it is not currently registered in `services/database.autoMigrateModels` or required by `VerifySchema`; there is no repository/rollback-window processor using it.
- `models.ChainFact` and `models.Deposit` currently have no explicit `reorged` or `superseded` status/metadata. Acceptance criterion 1 requires affected facts and transactions to be marked, so model/schema changes may be needed.
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
- `models/block.go`
- `models/chain_fact.go`
- `models/deposit.go`
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
- 2026-06-27: Story moved to in-progress; implementation reused the existing `TransactionRepo` correction transaction instead of adding a duplicate reorg service.
- 2026-06-27: Implemented canonical `transaction.reorged.v1` correction state with original event/resource references, chain fact/deposit reorg metadata, payment correction for all tx-linked outcomes, webhook payload metadata, and corrected-fact deposit processing guards.
- 2026-06-27: Validation passed: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./repositories ./services/webhook ./api/handlers ./docs ./services/database ./services/deposits`.
- 2026-06-27: Validation passed: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./...`.
- 2026-06-27: Validation passed: `GOCACHE=/tmp/gateway-gocache-bmad go vet -p=1 ./...`.
- 2026-06-27: Validation passed: `git diff --check && git diff --cached --check`.
- 2026-06-27: Revalidated after Story 3.4 review merge point: targeted Story 3.5 packages, full `go test ./...`, `go vet ./...`, and whitespace checks all passed.

### Completion Notes List

- Reorg correction now writes canonical `transaction.reorged.v1` through `constants.WebhookEventTransactionReorged` while preserving legacy underscore aliases in the event catalog.
- Affected transactions now retain `original_event_id`, `original_resource_id`, and `correction_reason`; webhook payloads expose those correction relation fields.
- `ChainFact` and `Deposit` now have reorg/superseded metadata, schema verification coverage, and correction updates from the transaction reorg path.
- Deposit processing skips corrected chain facts so stale facts cannot be reprocessed into settlement.
- Payment reorg correction applies to every tx-linked matched outcome, not only `paid`, and resets webhook retry state while moving sessions to `failed`.
- Ledger reversals remain compensating `reorg_reversal` entries with original ledger history preserved and duplicate correction guarded by idempotency keys.
- Sweep jobs tied to invalidated transactions are blocked/dead-lettered unless already succeeded; reconciliation jobs are opened idempotently for operator follow-up.
- Added deterministic tests for fork/reorg correction, webhook correction metadata, schema guardrails, corrected-fact processing, explicit payment terminal realtime states, and payment match/reorg edge cases.
- Hardened payment reorg idempotency so an already corrected and delivered `payment_failed` webhook is not reopened by a repeated correction call.
- Added deterministic transaction block identity reorg coverage for same transaction hash/log index reappearing at a different block identity.
- Hardened `models.Block` with canonical/reorg state fields, parent hash support, and schema verification so block tracking can represent canonical replacements without a parallel table.
- EVM listener dispatch now carries parent hash into transaction params so parent/child mismatch detection can run from observed chain data.
- Added checkout failed-state and ledger reversal skip/idempotency coverage for reorg-corrected payment and ledger paths.

### File List

- `.gitignore`
- `_bmad-output/implementation-artifacts/3-5-correct-reorgs-with-compensating-events-and-ledger-reversals.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `api/handlers/payment_test.go`
- `constants/webhook_events.go`
- `docs/integration-guide.md`
- `models/block.go`
- `models/chain_fact.go`
- `models/deposit.go`
- `models/transactions.go`
- `repositories/chain_fact_repo.go`
- `repositories/deposit_repo.go`
- `repositories/ledger_repo_test.go`
- `repositories/payment_repo.go`
- `repositories/payment_repo_test.go`
- `repositories/transaction_repo.go`
- `repositories/transaction_repo_test.go`
- `services/database/database.go`
- `services/database/database_test.go`
- `services/deposits/service.go`
- `services/deposits/service_test.go`
- `services/webhook/event_catalog.go`
- `services/webhook/notifier.go`
- `services/webhook/notifier_test.go`
- `types/transaction.go`
- `workers/listeners/evm/listener.go`

### Change Log

- 2026-06-27: Created story with reorg correction scope, atomic/idempotent correction requirements, payment lifecycle correction risks, ledger reversal guardrails, sweep/reconciliation behavior, webhook/docs contracts, and validation plan.
- 2026-06-27: Implemented reorg correction metadata, canonical correction events, chain fact/deposit/payment/sweep correction handling, webhook correction payloads, schema guardrails, tests, and validation; story moved to review.
- 2026-06-27: Tightened repeated payment correction no-op behavior, replaced remaining raw payment failure literals in `PaymentRepo`, and added transaction block identity reorg regression coverage.
- 2026-06-27: Aligned story record with final diff, EVM parent-hash propagation, ledger reversal skip coverage, checkout reorg-failed state coverage, and final validation results.
