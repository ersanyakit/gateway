---
story_id: "3.2"
story_key: "3-2-match-deposits-and-gate-settlement-on-finality"
epic: "Epic 3: Trustworthy Deposit Settlement & Ledger Balances"
status: done
created: 2026-06-27
updated: 2026-06-27
baseline_commit: 93aace0
---

# Story 3.2: Match Deposits and Gate Settlement on Finality

Status: done

## Story

Bir merchant veya exchange operatoru olarak,
tespit edilen deposit'lerin sahip olunan wallet'larla eslesmesini ve finality gate arkasinda settlement'a girmesini istiyorum,
böylece payment ve wallet bakiyeleri zincir eventi yeterince guvenilir olmadan credit edilmez.

## Acceptance Criteria

1. Given a chain fact references an address owned by a wallet, when the deposit boundary consumes the fact, then it creates or updates a deposit lifecycle record scoped to wallet, tenant/domain, chain, asset, amount, and tx metadata, and repeated chain facts are idempotent.
2. Given a chain fact does not match an owned address, when the deposit boundary processes it, then no payment session, ledger, webhook, or sweep mutation occurs, and the unmatched fact remains observable/reconcilable without tenant data leakage.
3. Given a matched deposit has not reached required confirmations/finality, when payment or balance state is evaluated, then the system reports pending/confirming state and does not mark payment succeeded or available balance credited as finalized.
4. Given a matched deposit reaches chain-specific finality, when the deposit boundary finalizes it, then it emits `deposit.finalized.v1` or equivalent configured event and provides deterministic input for ledger posting and payment matching.
5. Given finality behavior is implemented, when automated tests run, then they cover matched deposit, unmatched fact, duplicate fact, pending finality, finalized deposit, and chain-specific confirmation thresholds.

## Tasks / Subtasks

- [x] Task 1: Add deposit lifecycle ownership boundary (AC: 1, 2, 3)
  - [x] Add a narrow GORM `models.Deposit` or equivalent lifecycle model keyed by `chain_fact_event_id`.
  - [x] Store status, chain fact identity, wallet/merchant/domain when matched, observed address, chain/asset/amount/tx metadata, confirmations, required confirmations, finalized timestamp, and transaction unique hash when an adapter row is created.
  - [x] Allow unmatched facts to be recorded or counted without merchant/domain/wallet ids; do not leak tenant data for unknown addresses.
  - [x] Register the model and required columns/indexes in `services/database` GORM migration/schema verification.

- [x] Task 2: Consume chain facts outside the chain indexer path (AC: 1, 2, 3)
  - [x] Add repository/service helpers that process persisted `models.ChainFact` records idempotently.
  - [x] Use `WalletRepo.FindByChainAddress` and/or the existing address index pattern for ownership matching; do not duplicate address normalization rules.
  - [x] Keep `handleChainIndexerEvent` and the dispatcher subscriber fact-only; do not call payment, ledger, webhook, or sweep functions from the listener subscriber.
  - [x] Add a separate deposit worker/service entry point from `main.go` that can be retried safely after fact persistence.

- [x] Task 3: Gate pending vs finalized settlement (AC: 3, 4)
  - [x] Convert matched facts to deterministic transaction lifecycle input only after wallet ownership is known.
  - [x] Before required chain confirmations/finality, keep payment unpaid and available ledger uncredited; pending/confirming state may be represented by deposit and/or transaction status.
  - [x] At finality, update deposit lifecycle to finalized and emit a `deposit.finalized.v1` money event through the existing Postgres outbox.
  - [x] If a minimal compatibility adapter calls existing transaction/payment/ledger helpers, it must run from the deposit boundary after finality gates and be idempotent.

- [x] Task 4: Add tests for matching, duplicates, finality gates, and source boundaries (AC: 1, 2, 3, 4, 5)
  - [x] Test matched chain fact creates/updates deposit lifecycle with tenant/wallet scope.
  - [x] Test unmatched chain fact creates no payment/ledger/webhook/sweep mutation and carries no tenant identifiers.
  - [x] Test duplicate chain fact/deposit processing no-ops by `chain_fact_event_id`.
  - [x] Test pending finality does not mark payment paid or post available ledger entries.
  - [x] Test finalized deposit emits `deposit.finalized.v1` outbox record and respects chain-specific confirmation thresholds.
  - [x] Add source contract tests proving chain indexer handler remains fact-only and deposit worker owns business mutation.

- [x] Task 5: Update evidence and story record (AC: 5)
  - [x] Update docs/evidence to describe chain fact -> deposit lifecycle -> finality gate -> outbox settlement flow.
  - [x] Targeted validation: `go test -count=1 ./repositories ./services/database ./services/deposits`.
  - [x] Boundary validation: `go test -count=1 . ./docs ./services/webhook`.
  - [x] Full validation: `go test -count=1 ./...`.
  - [x] Static validation: `go vet ./...`.
  - [x] Whitespace validation: `git diff --check && git diff --cached --check`.
  - [x] Update Dev Agent Record, Completion Notes, File List, Change Log, and story status.

## Dev Notes

### Current Implementation Snapshot

- Story 3.1 added `models.ChainFact`, `repositories.ChainFactRepo`, GORM schema verification, and `main.go` fact-only listener handling. Do not undo that boundary.
- `main.go` still contains legacy transaction/payment helpers (`handleDepositWebhook`, `applyTransactionFinality`, `finalizePendingTransactions`, `handlePaymentDeposit`, `enqueueSweepJob`). After 3.1 the listener subscriber no longer calls them; 3.2 may reuse them only from a deposit boundary/worker after fact persistence and finality gating.
- `TransactionRepo.Create` is idempotent by `unique_hash`, initializes `confirmed` chain observations as `pending_confirmation`, and `MarkFinality` moves rows to `confirmed` only after required confirmations.
- `PaymentRepo.MarkPaidByTransaction` already refuses payment settlement unless `txModel.Status == confirmed` and `FinalizedAt != nil`.
- `LedgerRepo.CreateDepositPending` and `PostDepositAvailable` are idempotent by ledger keys. Available ledger credit must remain finality-gated.
- `MoneyEventOutboxRepo.BuildMoneyEventOutbox` and `Record` already enforce cataloged events and idempotency. Use `deposit.finalized.v1` for finalized deposit lifecycle output.
- `WalletRepo.FindByChainAddress` already owns chain-specific address lookup and case sensitivity rules. Reuse it instead of duplicating SQL.

### Guardrails

- The Chain Indexer remains fact-only. Deposit matching must run in a separate service/worker path so a listener ack never implies business settlement.
- No terminal `PaymentStatusPaid`, available ledger credit, payment webhook, transaction webhook, or sweep job before chain-specific finality is met.
- Unmatched facts must not expose merchant/domain/wallet ids. They can be recorded with `status=unmatched`, `chain_fact_event_id`, chain/tx metadata, and observed address for operator reconciliation.
- Keep all money transitions idempotent and replayable. The stable id for this story is `chain_fact_event_id`.
- Use GORM model registration and schema verification, not raw SQL migrations.
- Do not implement reorg reversal in this story; Story 3.5 owns correction/reversal behavior.
- Do not replace existing listener parsers or add live-network tests.

### Project Structure Notes

- Likely new files: `models/deposit.go`, `repositories/deposit_repo.go`, `repositories/deposit_repo_test.go`, `services/deposits/service.go`, `services/deposits/service_test.go`, `docs/deposit-finality-boundary.md`.
- Likely updates: `api/routes/routes.go`, `services/database/database.go`, `services/database/outbox_schema_contract_test.go`, `main.go`, `main_chain_fact_contract_test.go`, `docs/integration_contract_test.go`, this story file, and `sprint-status.yaml`.
- Keep package imports under module path `core/...`.
- If a service package is added, keep it deterministic and repository-driven; avoid direct HTTP/framework dependencies.

### References

- Story source: `_bmad-output/planning-artifacts/epics.md` Story 3.2.
- PRD FR13/FR14: `_bmad-output/planning-artifacts/prds/prd-gateway-2026-06-27/prd.md`.
- Architecture flow: `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md`.
- Previous story: `_bmad-output/implementation-artifacts/3-1-emit-chain-facts-without-mutating-business-state.md`.
- Event catalog: `services/webhook/event_catalog.go`.
- Project rules: `_bmad-output/project-context.md`.

## Dev Agent Record

### Agent Model Used

Codex

### Debug Log References

- 2026-06-27: Started dev-story implementation.
- 2026-06-27: Added GORM deposit lifecycle model, repository, schema verification, and idempotent chain fact consumption.
- 2026-06-27: Added deposit service/worker boundary using persisted chain facts, wallet ownership lookup, chain-specific finality, and finalized outbox event emission.
- 2026-06-27: Added repository/source-contract/docs tests for matched, unmatched, duplicate, pending, finalized, and listener/deposit boundary behavior.
- 2026-06-27: Validation passed: `go test -count=1 ./repositories ./services/database ./services/deposits`.
- 2026-06-27: Validation passed: `go test -count=1 . ./docs ./services/webhook`.
- 2026-06-27: Validation passed: `go test -count=1 ./...`.
- 2026-06-27: Validation passed: `go vet ./...`.
- 2026-06-27: Validation passed: `git diff --check`.
- 2026-06-27: Code review validation passed with isolated cache: `go test -p=1 -count=1 ./...`, `go vet -p=1 ./...`, `git diff --check && git diff --cached --check`.

### Completion Notes List

- Implemented `models.Deposit` lifecycle records keyed by `chain_fact_event_id`, including matched tenant/wallet scope, unmatched reconciliation records without tenant leakage, finality fields, and transaction correlation.
- Added `DepositRepo` and deposit service processing for durable `ChainFact` rows; the chain indexer remains fact-only while a separate worker owns deposit matching and settlement adapter execution.
- Added finality gating so pending/confirming deposits can create pending transaction/ledger state but cannot mark payments paid or post available balance until required confirmations are met.
- Finalized matched deposits emit `deposit.finalized.v1` through the existing Postgres money event outbox and then use idempotent transaction/payment/ledger compatibility helpers.
- Updated documentation and contract tests for the chain fact -> deposit lifecycle -> finality gate -> outbox settlement flow.

### File List

- `_bmad-output/implementation-artifacts/3-2-match-deposits-and-gate-settlement-on-finality.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `api/routes/routes.go`
- `docs/deposit-finality-boundary.md`
- `docs/integration_contract_test.go`
- `main.go`
- `main_chain_fact_contract_test.go`
- `models/deposit.go`
- `repositories/chain_fact_repo.go`
- `repositories/deposit_repo.go`
- `repositories/deposit_repo_test.go`
- `repositories/payment_repo_test.go`
- `services/database/database.go`
- `services/database/outbox_schema_contract_test.go`
- `services/deposits/service.go`
- `services/deposits/service_test.go`

### Change Log

- 2026-06-27: Story created from Epic 3.2 with FR13/FR14, architecture finality flow, 3.1 chain fact boundary, and current transaction/payment/ledger compatibility notes.
- 2026-06-27: Implemented deposit lifecycle matching, finality-gated settlement boundary, GORM schema registration, docs, and validation tests.
- 2026-06-27: Code review approved; story marked done.

### Senior Developer Review (AI)

- Outcome: Approve
- Findings: No blocking findings.
- Validation: `go test -p=1 -count=1 ./...`, `go vet -p=1 ./...`, and `git diff --check && git diff --cached --check` passed.
