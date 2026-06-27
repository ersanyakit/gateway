---
story_id: "4.1"
story_key: "4-1-reserve-ledger-holds-before-outbound-money-movement"
epic: "Epic 4: Safe Outbound Funds & Custody Controls"
status: ready-for-dev
created: 2026-06-27
updated: 2026-06-27
baseline_commit: c772d77
---

# Story 4.1: Reserve Ledger Holds Before Outbound Money Movement

Status: ready-for-dev

## Story

Bir operator olarak,
withdrawal, payout, refund ve sweep taleplerinin signing veya broadcast oncesinde ledger hold/reservation almasini istiyorum,
boylece outbound para hareketleri bakiyeyi asamaz, ayni fon iki kez harcanamaz ve basarisiz islerde hold idempotent sekilde serbest kalir.

## Acceptance Criteria

1. Given an outbound withdrawal, payout, refund, or sweep is requested, when the request is validated, then the system checks ledger-derived available balance and creates a hold/reservation before signing or broadcast, and requests without successful hold cannot proceed.
2. Given two outbound requests compete for the same available balance, when reservation runs concurrently, then only reservations backed by available ledger balance succeed, and negative balances are prevented by transaction, lock, or DB constraint behavior covered by tests.
3. Given an outbound request is rejected or fails before broadcast, when failure is terminal, then the hold is released or reversed through ledger entries, and the release is idempotent for repeated failure handling.
4. Given hold/reservation schema changes are required, when the story is implemented, then it includes versioned migration artifacts or an explicit migration plan, and ledger invariant tests include hold/release paths.
5. Given reservation logic is implemented, when automated tests run, then they cover successful hold, insufficient funds, concurrent requests, duplicate request idempotency, failure release, and audit logging.

## Tasks / Subtasks

- [ ] Task 1: Make ledger hold a mandatory outbound gate (AC: 1, 2)
  - [ ] Preserve and extend `LedgerRepo`; do not create a second balance authority or a parallel reservation table unless the ledger model cannot represent a required hold.
  - [ ] Ensure `WithdrawalRequestRepo.CreateWithHold`, `RefundRepo.CreateWithHold`, `RefundRepo.ClaimPendingWithHold`, and approval/broadcast paths hard-fail when `LedgerRepo` is nil or hold creation/check fails.
  - [ ] Add a repo-level hold existence/assertion before any withdrawal/refund transfer callback can run, so legacy rows created without a hold cannot sign or broadcast.
  - [ ] Map insufficient ledger balance to operator/API-visible validation errors instead of generic 500 responses on V1 payout/refund creation.

- [ ] Task 2: Harden withdrawal/payout/refund hold lifecycle (AC: 1, 2, 3, 5)
  - [ ] Keep current double-entry hold model: merchant_available debit plus withdrawal/refund transit credit with stable idempotency keys.
  - [ ] Preserve `pg_advisory_xact_lock` balance locking and `ensureAvailableBalance` semantics for tenant/domain/asset scope.
  - [ ] Verify duplicate hold/debit calls remain idempotent and do not create extra ledger rows.
  - [ ] Release pending holds only for rejected or pre-broadcast terminal failures; do not void holds after a tx hash exists unless reconciliation/correction proves it is safe.
  - [ ] Add ActivityLog/audit evidence for create, hold failure, approve, reject, failure release, and broadcast/finalize outcomes where the action is operator-facing.

- [ ] Task 3: Add or explicitly gate sweep reservation (AC: 1, 3, 4, 5)
  - [ ] Remove direct unreserved sweep/broadcast paths from admin and legacy handlers; `ExecuteWalletTransfer(..., sweep=true)` must not be callable from a money path without a recorded reservation decision.
  - [ ] For auto-sweeps, reserve against the known finalized deposit amount and asset from `SweepJob.TransactionUniqueHash` before `executeSweepJob` signs/broadcasts.
  - [ ] If a manual "sweep all" request has no deterministic amount, reject it with a policy/validation error until Story 4.3 fee/resource policy can determine a safe amount.
  - [ ] If adding real sweep ledger rows, extend `LedgerEntry` with `SweepJobID` and sweep hold/release/debit entry types plus schema verification; otherwise document the explicit migration plan and keep unheld sweeps blocked.
  - [ ] Release sweep holds idempotently on pre-broadcast terminal failure and preserve the hold for broadcasted but unresolved sweep jobs.

- [ ] Task 4: Schema, migration, and invariant coverage (AC: 2, 3, 4)
  - [ ] Update `models.LedgerEntry` constraints and `services/database.VerifySchema` for any new fields, entry types, accounts, indexes, or constraints.
  - [ ] Add `_bmad-output/implementation-artifacts/4-1-migration-plan.md` if production schema changes are not expressed as versioned migrations in this repo.
  - [ ] Extend ledger invariant tests so pending holds reduce available balance and voided releases are excluded from available/transit totals.
  - [ ] Add a Postgres concurrency test where competing requests over the same merchant/domain/asset cannot both reserve more than available.

- [ ] Task 5: Handler/API integration and UX-safe operator feedback (AC: 1, 3, 5)
  - [ ] V1 payout/refund, dealer withdrawal, admin withdrawal, admin refund, and admin sweep routes must all surface hold failures without signing/broadcasting.
  - [ ] Preserve V1 error envelope shape: `{"result":"error","message":"..."}`.
  - [ ] Keep dealer/admin UI copy concise and action-focused; show hold/policy failure without exposing secrets or raw diagnostic blobs.
  - [ ] Preserve tenant/domain isolation: one domain must not learn whether another domain has funds, requests, or wallets.

- [ ] Task 6: Documentation, story record, and validation (AC: 1, 2, 3, 4, 5)
  - [ ] Update Dev Agent Record, Completion Notes, File List, Change Log, and story status.
  - [ ] Targeted validation: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./repositories ./api/handlers ./services/database`.
  - [ ] Sweep/worker validation if touched: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 . ./services/reconciliation`.
  - [ ] Full validation: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./...`.
  - [ ] Static validation: `GOCACHE=/tmp/gateway-gocache-bmad go vet -p=1 ./...`.
  - [ ] Whitespace validation: `git diff --check && git diff --cached --check`.

## Dev Notes

### Current Implementation Snapshot

- `models.LedgerEntry` already has `withdrawal_hold`, `withdrawal_release`, `withdrawal_debit`, `refund_hold`, and `refund_debit` entry types, plus merchant available, withdrawal transit, and refund transit accounts.
- `repositories.LedgerRepo.CreateWithdrawalHold` and `CreateRefundHold` write pending double-entry holds after `ensureAvailableBalance` and `lockLedgerAsset`.
- `WithdrawalRequestRepo.CreateWithHold`, `RefundRepo.CreateWithHold`, and `RefundRepo.ClaimPendingWithHold` already create holds when a ledger repo is supplied, but nil ledger currently allows a request to continue without a hold. This story must close that bypass.
- `WithdrawalRequestRepo.ApproveWithTransfer` runs the transfer callback outside the DB transaction after marking a row processing. It must prove the hold exists before calling the transfer callback.
- `RefundRepo.MarkFailed` skips hold release when `TxHash` is already present. Preserve that safety for broadcasted refunds.
- `HandleAdminSweep` and legacy sweep/withdraw transfer helpers can call `ExecuteWalletTransfer` directly. These are the largest current bypass risks for AC1.
- `SweepJobRepo` is durable and idempotent by `transaction_unique_hash`, but it has no ledger hold/reference fields today.
- `api/handlers/transfer.go` already supports token sweep via `SweepERC20To`; that capability still needs the hold gate before any production money path uses it.

### Architecture And Product Guardrails

- AD-3: ledger is the only balance authority. Do not use wallet rows, transaction sums, or live chain reads as spendable balance.
- AD-5: every money-affecting transition needs stable idempotency and duplicate safety.
- AD-7: outbound transactions must not be signed before ledger hold/reservation succeeds. Chain-resource reservation is Story 4.3, but this story must block unheld signing now.
- AD-8: lifecycle events must be enqueued through the webhook boundary, not inline merchant callbacks.
- AD-11: uncertain broadcast/ledger states should open scoped reconciliation jobs instead of blind retries.
- FR19/FR21: withdrawal, payout, refund, and sweep lifecycle includes request, policy validation, hold, approval, signing/broadcast, finalization/release, and webhook lifecycle.
- NFR2/NFR3: duplicate withdrawal and negative ledger balance prevention must be proven with idempotency and invariant tests.
- NFR13: admin, signer, withdrawal approval, replay, and recovery actions need auditability.

### Implementation Boundaries

- Reuse `LedgerRepo`, `WithdrawalRequestRepo`, `RefundRepo`, `SweepJobRepo`, `ActivityLogRepo`, and existing handler surfaces.
- Do not introduce external brokers, a new service boundary, or a SPA.
- Do not implement nonce/UTXO/resource reservation, production signer integration, address whitelist, velocity limits, dual approval, or fee/gas policy beyond blocking unreserved paths; those belong to later Epic 4 stories.
- Do not silently mutate posted money history. Releases should void pending hold rows or use compensating entries if a posted correction is needed.
- Do not expose API secrets, webhook secrets, signatures, mnemonics, private keys, raw signed payloads, or unredacted diagnostics in logs, API errors, or UI.

### UX Notes

- UX contract exists in `_bmad-output/planning-artifacts/ux.md` and `ux-designs/ux-gateway-2026-06-27`.
- Operator withdrawal/refund/sweep views should show actor, tenant/domain, destination, amount, hold, policy checks, signer state, and chain-resource reservation when available.
- Risky commands require explicit confirmation, but this story should not add decorative dashboards or broad UI redesign.

### Previous Story Intelligence

- Story 3.6 added scoped reconciliation jobs, bounded evidence JSON, active dedupe statuses, and evidence redaction. If this story finds a broadcast/hold mismatch, open a scoped reconciliation job rather than hiding it in a log.
- Story 3.5 established compensating ledger reversal and correction event patterns for reorgs. Follow that non-destructive correction style.
- Recent reconciliation review tightened empty-scope validation and generic signature redaction. Apply the same discipline to any new hold/release evidence or audit metadata.

### Likely Files To Touch

- `models/ledger_entry.go`
- `models/sweep_job.go`
- `repositories/ledger_repo.go`
- `repositories/ledger_repo_test.go`
- `repositories/withdrawal_request_repo.go`
- `repositories/withdrawal_request_repo_test.go`
- `repositories/refund_repo.go`
- `repositories/sweep_job_repo.go`
- `repositories/sweep_job_repo_test.go`
- `api/handlers/dealer.go`
- `api/handlers/transfer.go`
- `api/handlers/v1api.go`
- `api/handlers/*_test.go`
- `main.go`
- `services/database/database.go`
- `services/database/database_test.go`
- `_bmad-output/implementation-artifacts/4-1-reserve-ledger-holds-before-outbound-money-movement.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

### Project Structure Notes

- The repo is still a Go modular monolith under module path `core`.
- Server-rendered admin/dealer templates live under `views/`; keep UI changes minimal and compatible with existing Tailwind-style templates.
- Production migration discipline is not fully implemented; any schema change must be reflected in `VerifySchema` and accompanied by an explicit migration plan artifact.
- Before implementation, re-check the working tree and do not overwrite unrelated UI/admin changes.

### References

- Story source: `_bmad-output/planning-artifacts/epics.md` Story 4.1.
- PRD FR19/FR21 and NFR2/NFR3/NFR13/NFR14: `_bmad-output/planning-artifacts/prds/prd-gateway-2026-06-27/prd.md`.
- Architecture AD-3/AD-5/AD-7/AD-8/AD-11: `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md`.
- UX contract: `_bmad-output/planning-artifacts/ux.md`, `_bmad-output/planning-artifacts/ux-designs/ux-gateway-2026-06-27/EXPERIENCE.md`.
- Audit context: `docs/payment-gateway-wallet-provider-audit.md`, `docs/product-readiness-audit.md`.
- Project context: `_bmad-output/project-context.md`.
- Previous story: `_bmad-output/implementation-artifacts/3-6-open-scoped-reconciliation-jobs-for-drift-and-uncertainty.md`.

## Dev Agent Record

### Agent Model Used

Codex

### Debug Log References

- 2026-06-27: Story created from Epic 4.1 with PRD FR19/FR21, architecture AD-3/AD-7, existing ledger hold implementation, and current direct sweep bypass analysis.

### Completion Notes List

- Story context created and marked ready-for-dev.

### File List

- `_bmad-output/implementation-artifacts/4-1-reserve-ledger-holds-before-outbound-money-movement.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

### Change Log

- 2026-06-27: Created story with outbound ledger hold/reservation scope, current implementation snapshot, bypass risks, tests, and validation plan.
