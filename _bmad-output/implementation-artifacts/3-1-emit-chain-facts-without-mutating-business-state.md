---
story_id: "3.1"
story_key: "3-1-emit-chain-facts-without-mutating-business-state"
epic: "Epic 3: Trustworthy Deposit Settlement & Ledger Balances"
status: done
created: 2026-06-27
updated: 2026-06-27
baseline_commit: 90e5c93
---

# Story 3.1: Emit Chain Facts Without Mutating Business State

Status: done

## Story

As a platform operator,
I want chain indexers to produce durable chain facts instead of directly mutating payment or ledger state,
so that deposit processing, settlement, and reconciliation are deterministic and replayable.

## Acceptance Criteria

1. Given an EVM, Bitcoin, Solana, or TRON listener detects a transaction or log, when the chain indexer processes it, then it emits a stable chain fact event with chain id, block/slot height, block hash where available, tx hash, log index or equivalent, observed address, asset metadata, amount, and finality metadata, and it does not directly mark payments paid or post ledger entries.
2. Given the listener starts from a configured block or slot, when explicit start configuration is present, then it begins from that configured point and records progress safely, and default safe/latest behavior is documented as not equivalent to full historical backfill.
3. Given a chain fact is seen more than once, when the same chain id, tx hash, and log index or equivalent identifier is processed, then the fact is deduplicated by stable event id, and downstream consumers receive idempotent input.
4. Given indexer processing fails after a fact is observed, when the process restarts, then progress and fact persistence prevent silent loss or duplicate business mutation, and crash/retry behavior is covered by tests.
5. Given chain fact emission is implemented, when automated tests run, then they cover supported chain families, duplicate tx/log detection, configured start block, progress persistence, and no direct business state mutation.

## Tasks / Subtasks

- [x] Task 1: Add durable chain fact ownership boundary (AC: 1, 3, 4)
  - [x] Add a narrow `models.ChainFact` or equivalent durable fact model owned by the Chain Indexer boundary.
  - [x] Store stable event id, chain id, block/slot height, block hash, tx hash, log index or equivalent, observed address, direction, asset metadata, amount raw, confirmations/finality metadata, source event type, and safe raw metadata needed for replay/reconciliation.
  - [x] Add repository helper(s) that upsert by stable event id and return created/no-op state without mutating payment, ledger, webhook, or sweep state.
  - [x] Register required schema columns in `services/database` verification if a new table/model is added.

- [x] Task 2: Route listener output through chain fact persistence (AC: 1, 3, 4)
  - [x] Preserve existing listener extraction logic in `workers/listeners/*`; do not replace supported chain parsers or add live-network test dependencies.
  - [x] Refactor the `main.go` dispatcher subscriber so observed transactions/logs persist chain facts first and acknowledge only after durable fact/progress handling is safe.
  - [x] Remove or guard direct business mutations from the indexer path: no payment paid transition, no ledger posting, no webhook enqueue, and no sweep job creation from listener observation.
  - [x] Leave deposit/payment/ledger consumption for Story 3.2 unless a minimal compatibility adapter is required and covered as non-authoritative.

- [x] Task 3: Keep configured start/progress behavior explicit (AC: 2, 4, 5)
  - [x] Reuse `workers/listeners.ConfiguredStartBlock` and existing `ChainStateRepo` progress semantics where appropriate.
  - [x] Ensure EVM, Bitcoin, Solana, and TRON listeners respect configured start block/slot behavior or have tests documenting the current gap.
  - [x] Document that safe/latest first-start behavior is not historical backfill and does not prove missed deposits were scanned.
  - [x] Do not implement full range replay/backfill unless it is already present; capture the remaining operator backfill gap explicitly.

- [x] Task 4: Add idempotency, crash-safety, and no-mutation tests (AC: 1, 3, 4, 5)
  - [x] Test stable event id generation for EVM log index, Bitcoin tx output or equivalent, Solana instruction/inner instruction, and TRON log/contract event identifiers.
  - [x] Test duplicate fact persistence no-ops by event id and does not create duplicate downstream input.
  - [x] Test failure after fact persistence can be retried without duplicate business mutation.
  - [x] Add source contract or unit tests proving chain listener/indexer paths do not call `PaymentRepo.MarkPaidByTransaction`, ledger posting methods, direct webhook enqueue/delivery, or sweep creation.
  - [x] Keep existing webhook boundary source-flow tests passing.

- [x] Task 5: Update evidence and story record (AC: 2, 5)
  - [x] Update integration or architecture evidence to describe chain facts, stable ids, configured start behavior, and non-backfill default behavior.
  - [x] Targeted validation: `go test -count=1 ./workers/listeners/... ./repositories ./services/database`.
  - [x] Boundary validation: `go test -count=1 ./services/webhook ./docs ./constants`.
  - [x] Full validation: `go test -count=1 ./...`.
  - [x] Static validation: `go vet ./...`.
  - [x] Whitespace validation: `git diff --check && git diff --cached --check`.
  - [x] Update Dev Agent Record, Completion Notes, File List, Change Log, and story status.

## Dev Notes

### Current Implementation Snapshot

- `main.go` currently subscribes to dispatcher events, matches wallets, writes `TransactionRepo.Create`, applies finality, calls `handleDepositWebhook`, calls `handlePaymentDeposit`, writes ledger pending/available entries, enqueues webhooks, and creates sweep jobs from observed chain transactions. Story 3.1 must break that direct listener-to-business-mutation path.
- `models.ChainState` and `repositories.ChainStateRepo` already track `LastProcessedBlock` and `LastConfirmedBlock` with monotonic updates. Reuse this progress boundary instead of creating a second progress table unless a chain fact table requires separate metadata.
- `models.Transaction` and `repositories.TransactionRepo` currently act as lifecycle/tx storage with unique hash `chain_id-hash-log_index`; they also contain wallet, merchant/domain, finality, webhook, and reorg fields. Do not turn `transactions` into authoritative balance or final settlement state.
- `workers/dispatcher.Event` currently carries `*types.TransactionParam`. The fact boundary can be built from that shape first, but the durable fact record must not imply payment settlement.
- `workers/listeners.ConfiguredStartBlock` already supports `CHAIN_<id>_START_BLOCK`, `<CHAIN_NAME>_START_BLOCK`, `START_BLOCK_<CHAIN_NAME>`, and `CHAIN_START_BLOCK_DEFAULT`.
- There are existing listener implementations for EVM, Bitcoin, Solana, and TRON. Keep tests deterministic with fake RPC/listener inputs; do not add live RPC dependency.

### Guardrails

- Chain Indexer owns block/slot progress, raw transaction/log extraction, provider health, finality signals, and reorg detection. Deposit, Payment, Ledger, Webhook, and Sweep boundaries mutate business state later.
- Do not mark checkout/payment paid before a finality gate. Story 3.1 should produce facts only; Story 3.2 owns deposit matching/finality settlement.
- Do not post ledger entries from listener observation. Ledger authority remains a separate boundary and Story 3.3 expands that work.
- Do not introduce Kafka/NATS/SQS. Epic 2 established Postgres as the first durable event substrate.
- Do not use transaction row sums or live chain reads as balance authority.
- Do not revert the current uncommitted `blockchain/chains/solana.go` and `blockchain/chains/tron.go` RPC fallback edits unless the user explicitly asks; they are outside this story unless deliberately adopted with tests.

### Project Structure Notes

- Likely new/updated files: `models/*chain*fact*.go`, `repositories/*chain*fact*_repo.go`, `services/database/database.go`, `main.go`, `workers/dispatcher/dispatcher.go`, `workers/listeners/config.go`, listener tests under `workers/listeners/...`, and contract tests near the boundary being protected.
- Prefer repository-owned GORM writes. If a new model is added, include schema verification tokens and tests as done for `MoneyEventOutbox` and `WebhookDelivery`.
- Keep all new event ids stable and string-based; avoid generated UUIDs as consumer/replay identity for chain facts.

### References

- Epic story and ACs: `_bmad-output/planning-artifacts/epics.md` Story 3.1.
- PRD FR11/FR12: `_bmad-output/planning-artifacts/prds/prd-gateway-2026-06-27/prd.md`.
- Architecture AD-4 and AD-9: `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md`.
- UX reconciliation evidence: `_bmad-output/planning-artifacts/ux-designs/ux-gateway-2026-06-27/EXPERIENCE.md`.
- Project rules: `_bmad-output/project-context.md`.
- Previous Epic 2 evidence: `docs/epic-2-integration-evidence.md`.
- Current source-flow guardrail example: `services/webhook/source_flow_contract_test.go`.

## Dev Agent Record

### Agent Model Used

Codex

### Debug Log References

- `go test -count=1 ./repositories ./services/database ./docs ./workers/listeners/... ./blockchain/chains ./services/webhook`
- `go test -count=1 ./workers/listeners/... ./repositories ./services/database`
- `go test -count=1 ./services/webhook ./docs ./constants`
- `go test -count=1 ./workers/listeners`
- `go test -count=1 .`
- `go test -count=1 ./api/routes`
- `go test -count=1 ./docs`
- `go test -count=1 ./...`
- `go vet ./...`
- `git diff --check && git diff --cached --check`

### Completion Notes List

- Added durable `ChainFact` persistence with stable chain/hash/log event ids and idempotent record semantics.
- Routed dispatcher-observed transactions through `handleChainIndexerEvent` so listener observation records chain facts without directly mutating payment, ledger, webhook, or sweep state.
- Registered chain fact schema verification and added contract tests for schema tokens and no direct business mutation in the indexer path.
- Documented chain fact identity, configured start behavior, non-backfill default behavior, and replay-safe recovery rules.

### File List

- `_bmad-output/implementation-artifacts/3-1-emit-chain-facts-without-mutating-business-state.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `api/routes/routes.go`
- `docs/chain-fact-boundary.md`
- `docs/integration_contract_test.go`
- `main.go`
- `main_chain_fact_contract_test.go`
- `models/chain_fact.go`
- `repositories/chain_fact_repo.go`
- `repositories/chain_fact_repo_test.go`
- `services/database/database.go`
- `services/database/outbox_schema_contract_test.go`
- `workers/listeners/config_test.go`

### Change Log

- 2026-06-27: Story created from Epic 3.1 with FR11/FR12, architecture AD-4/AD-9, current listener flow, and Epic 2 continuity notes.
- 2026-06-27: Implemented durable chain facts, indexer no-mutation boundary, docs, tests, and validation; story marked done.
- 2026-06-27: Added listener source contract coverage for configured start block behavior across EVM, Bitcoin, Solana, and TRON.
