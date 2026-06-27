---
story_id: "3.3"
story_key: "3-3-enforce-ledger-derived-balance-authority"
epic: "Epic 3: Trustworthy Deposit Settlement & Ledger Balances"
status: ready-for-dev
created: 2026-06-27
updated: 2026-06-27
baseline_commit: 5adc975
---

# Story 3.3: Enforce Ledger-Derived Balance Authority

Status: ready-for-dev

## Story

Bir merchant veya exchange tenant olarak,
bakiyelerin sadece ledger entry'lerinden turetilmesini istiyorum,
boylece available, pending, hold, transit, reversal ve adjustment bakiyeleri deposit, withdrawal, refund, sweep ve reconciliation boyunca tutarli kalir.

## Acceptance Criteria

1. Given a balance API or dealer/admin balance view is requested, when the system computes balances, then it reads from ledger entries or ledger projections only, and it does not sum transaction rows or poll chain balances inline as authoritative balance.
2. Given a finalized deposit is posted to the ledger, when the ledger boundary records the movement, then it creates balanced debit/credit entries with stable idempotency key, asset metadata, wallet/tenant scope, and lifecycle reference, and duplicate posting attempts are rejected or no-op safely.
3. Given ledger schema or projection changes are introduced, when the story is implemented, then they use GORM model/schema verification or an explicit GORM migration plan, and DB-level constraints/indexes protect status values, unique idempotency keys, and balanced ledger movement where feasible.
4. Given ledger-derived balances are implemented, when automated tests run, then they cover pending, available, hold, transit, reversal, adjustment, duplicate idempotency key, negative balance guard, and dealer/admin view compatibility.
5. Given a ledger invariant violation is detected, when balance checks or invariant jobs run, then the system opens a scoped reconciliation job, and logs include correlation id plus merchant/domain context without exposing secrets.

## Tasks / Subtasks

- [ ] Task 1: Remove transaction-derived balance authority from read paths (AC: 1, 4)
  - [ ] Keep `api/handlers/v1api.go` common and wallet balance endpoints ledger-only; add source/contract tests proving they do not call `TransactionRepo` or live chain balance APIs.
  - [ ] Replace dealer dashboard fallback from `TransactionRepo.MerchantDepositSummary` to ledger-only empty-state behavior or ledger projection rows.
  - [ ] Replace admin wallet balance map from `TransactionRepo.AllWalletDeposits` with `LedgerRepo.WalletBalances`/batch ledger query support.
  - [ ] Leave transaction summary helpers only for non-authoritative historical reporting if still needed; document that they must not feed balance views.

- [ ] Task 2: Harden ledger-derived balance query/projection behavior (AC: 1, 2, 4)
  - [ ] Add or refine ledger repository helpers for merchant/domain/wallet balance rows so pending, available, withdrawal transit, refund transit, reversal and adjustment accounts are represented explicitly.
  - [ ] Ensure balance queries aggregate only numeric `amount_raw`, apply credit/debit signs consistently, exclude `voided` entries, and preserve asset metadata (`chain_id`, `token`, `symbol`, `decimals`).
  - [ ] Add batch wallet balance lookup if needed for dealer/admin wallet list performance; do not reintroduce transaction row sums.
  - [ ] Keep available balance checks backed by ledger rows and row/advisory locks before withdrawal/refund holds.

- [ ] Task 3: Strengthen ledger posting invariants and GORM schema checks (AC: 2, 3, 4)
  - [ ] Verify deposit pending/available postings remain balanced by `idempotency_key`, account, direction, amount, tenant/wallet scope, and lifecycle reference.
  - [ ] Add tests for duplicate idempotency no-op/reject behavior across deposit, withdrawal hold/debit, refund hold/debit, reversal and adjustment paths where implemented.
  - [ ] Use GORM model tags, `services/database.VerifySchema`, and/or documented GORM migration plan for any new columns/indexes/check constraints; do not add raw SQL migration files.
  - [ ] If DB-level balanced-movement enforcement cannot be expressed safely with current GORM/Postgres shape, document the explicit plan in `docs/ledger-balance-authority.md` and cover it with repository invariant tests.

- [ ] Task 4: Open scoped reconciliation for ledger invariant violations (AC: 5)
  - [ ] Extend `LedgerRepo.FindInvariantIssues` output with enough scope for logs and reconciliation (`merchant_id`, optional `domain_id`, chain, token/symbol, idempotency/correlation key).
  - [ ] Update `runLedgerInvariantReconciliation` to create/open a scoped reconciliation job or reason string carrying the ledger invariant scope without exposing secrets.
  - [ ] Ensure logs include correlation id, merchant id, domain id when available, chain id, and net amount; avoid API secrets, webhook secrets, private keys, mnemonics, or raw signatures.
  - [ ] Keep broader reconciliation lifecycle expansion for Story 3.6; this story only needs ledger invariant scope.

- [ ] Task 5: Documentation, contract tests, and validation (AC: 1, 2, 3, 4, 5)
  - [ ] Add `docs/ledger-balance-authority.md` describing ledger-only balance reads, accounts, status semantics, idempotency, invariant detection, and GORM migration/schema plan.
  - [ ] Add docs contract coverage in `docs/integration_contract_test.go`.
  - [ ] Add source contract tests proving dealer/admin balance views no longer use `TransactionRepo.MerchantDepositSummary` or `TransactionRepo.AllWalletDeposits` as balance authority.
  - [ ] Targeted validation: `go test -count=1 ./repositories ./api/handlers ./services/database ./docs`.
  - [ ] Full validation: `go test -count=1 ./...`.
  - [ ] Static validation: `go vet ./...`.
  - [ ] Whitespace validation: `git diff --check && git diff --cached --check`.
  - [ ] Update Dev Agent Record, Completion Notes, File List, Change Log, and story status.

## Dev Notes

### Current Implementation Snapshot

- Story 3.2 committed `models.Deposit`, `DepositRepo`, and `services/deposits` so finalized deposit settlement now posts ledger entries through the deposit boundary after finality.
- `models.LedgerEntry` already has explicit entry types for deposit pending/available, withdrawal hold/debit, refund hold/debit, reorg reversal, and adjustment; accounts include `merchant_pending`, `merchant_available`, `platform_clearing`, `withdrawal_transit`, and `refund_transit`.
- `repositories.LedgerRepo` already owns double-entry posting helpers, idempotency checks by `idempotency_key`, `ensureAvailableBalance`, `MerchantBalances`, `DomainBalances`, `WalletBalances`, and `FindInvariantIssues`.
- V1 public balance endpoints in `api/handlers/v1api.go` already use `LedgerRepo.DomainBalances` and `LedgerRepo.WalletBalances`. Preserve this and add contract tests so future changes cannot regress to transaction sums or live chain reads.
- Dealer dashboard in `api/handlers/dealer.go` still falls back to `TransactionRepo.MerchantDepositSummary` when ledger balances are empty. That fallback violates AD-3/FR17 and must be removed or converted to a clearly non-balance historical report.
- Admin wallet list in `api/handlers/dealer.go` uses `buildWalletBalanceMap` -> `TransactionRepo.AllWalletDeposits`, which sums confirmed transactions. Replace this with ledger-derived wallet balances.
- `repositories.TransactionRepo.AllWalletDeposits`, `MerchantDepositSummary`, and `DomainDepositSummary` sum `transactions`. Do not use them for authoritative balance views; if retained, name/document them as historical transaction/deposit reporting only.
- `main.go` already runs `runLedgerInvariantReconciliation`, which calls `LedgerRepo.FindInvariantIssues` and opens reconciliation jobs. Current issue rows do not include merchant/domain scope; add scoped context for AC5 without expanding the full Story 3.6 reconciliation lifecycle.
- `services/database.Migrate` uses GORM `AutoMigrate` and `VerifySchema`; the user explicitly requires migrations to remain GORM-based.

### Guardrails

- Ledger is the only authoritative balance source. Do not calculate available/pending/hold/transit balances from `transactions`, `payment_sessions`, `withdrawal_requests`, `refunds`, `sweep_jobs`, wallet rows, or live chain RPC responses.
- Balance reads may aggregate `ledger_entries` directly or through a ledger projection owned by the ledger boundary. If a projection is introduced, keep it populated from ledger entries and covered by idempotency/invariant tests.
- Do not broaden scope into reorg correction implementation; Story 3.5 owns compensating reversal behavior beyond existing `PostTransactionReversal`.
- Do not broaden scope into full reconciliation workflow; Story 3.6 owns generic scoped reconciliation jobs. This story only needs ledger invariant violations to carry enough scope.
- Do not add external brokers or new services. Keep changes inside current Go/GORM repository/service/handler structure.
- Preserve tenant/domain isolation in API and dealer/admin views. A tenant must not infer another tenant's balances.
- Preserve raw integer amount storage; display formatting may use existing helpers only after ledger-derived raw values are selected.

### Project Structure Notes

- Likely updates: `repositories/ledger_repo.go`, `repositories/ledger_repo_test.go`, `api/handlers/dealer.go`, `api/handlers/dealer_test.go`, `api/handlers/v1api.go` tests, `main.go`, `main_chain_fact_contract_test.go`, `services/database/database.go`, `services/database/outbox_schema_contract_test.go`, `docs/integration_contract_test.go`, `docs/ledger-balance-authority.md`.
- Possible new helper package only if it removes duplication across API/dealer views; prefer extending `LedgerRepo` first.
- Keep module imports as `core/...`.
- Keep all database schema changes GORM-owned via model tags, `AutoMigrate`, and schema verification.

### References

- Story source: `_bmad-output/planning-artifacts/epics.md` Story 3.3.
- PRD FR17/FR18/FR33: `_bmad-output/planning-artifacts/prds/prd-gateway-2026-06-27/prd.md`.
- Architecture AD-3 and balance read rule: `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md`.
- Previous story: `_bmad-output/implementation-artifacts/3-2-match-deposits-and-gate-settlement-on-finality.md`.
- Project rules: `_bmad-output/project-context.md`.
- Current ledger implementation: `models/ledger_entry.go`, `repositories/ledger_repo.go`.
- Current balance surfaces: `api/handlers/v1api.go`, `api/handlers/dealer.go`.

## Dev Agent Record

### Agent Model Used

Codex

### Debug Log References

- 2026-06-27: Story created from Epic 3.3 with FR17/FR18/FR33, AD-3 ledger authority, Story 3.2 deposit finality implementation, and current dealer/admin transaction-sum fallback analysis.

### Completion Notes List

- Ready for dev-story implementation.

### File List

- `_bmad-output/implementation-artifacts/3-3-enforce-ledger-derived-balance-authority.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

### Change Log

- 2026-06-27: Created story with ledger-only balance authority scope, GORM migration guardrails, dealer/admin fallback risks, invariant reconciliation scope, and validation requirements.
