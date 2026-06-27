---
story_id: "3.4"
story_key: "3-4-model-payment-matching-outcomes-explicitly"
epic: "Epic 3: Trustworthy Deposit Settlement & Ledger Balances"
status: ready-for-dev
created: 2026-06-27
updated: 2026-06-27
baseline_commit: 7f8c552
---

# Story 3.4: Model Payment Matching Outcomes Explicitly

Status: ready-for-dev

## Story

Bir merchant olarak,
payment matching outcome'larinin paid, expired, underpaid, overpaid ve partial-paid state'lerini ayirmasini istiyorum,
boylece checkout, API ve webhook consumer'lari belirsiz veya mismatch odeme durumlarini successful settlement ile karistirmaz.

## Acceptance Criteria

1. Given a finalized deposit matches a payment session exactly according to asset, chain, address, and amount policy, when payment matching runs, then the session transitions to paid/succeeded and emits the configured payment lifecycle event.
2. Given a deposit amount is below expected amount or below configured tolerance, when payment matching runs, then the session is marked underpaid, failed, or policy-defined non-terminal state, and checkout/API state is distinct from pending and paid.
3. Given a deposit amount exceeds expected amount, when payment matching runs, then the system records overpayment according to policy and operator or refund/reconciliation follow-up is discoverable.
4. Given multiple deposits may partially satisfy one payment session, when partial matching is supported or intentionally unsupported, then the behavior is explicit in status, event, and integration documentation, and tests cover the chosen policy.
5. Given payment matching changes are implemented, when automated tests run, then they cover exact match, wrong asset, wrong chain, underpaid, overpaid, partial payment policy, expiry interaction, and idempotent repeated matching.

## Tasks / Subtasks

- [ ] Task 1: Define explicit payment outcome vocabulary and persistence (AC: 1, 2, 3, 4)
  - [ ] Add explicit model constants for every supported payment matching outcome/state. Current statuses include `pending`, `awaiting_payment`, `paid`, `expired`, `failed`, and `underpaid`; this story must add or otherwise explicitly model `overpaid` and `partial_paid` or document an equivalent policy field that appears in API/webhook/docs.
  - [ ] Add persistent outcome metadata to `models.PaymentSession` if status alone is insufficient: reason/category, matched raw amount, expected raw amount snapshot, excess/shortfall raw amount, and matched transaction/deposit reference. Keep raw integer string storage.
  - [ ] Keep schema changes GORM-owned through model tags, `AutoMigrate`, and `services/database.VerifySchema`; do not add raw SQL migration files.
  - [ ] Preserve backwards compatibility for existing `payment_succeeded`, `payment_failed`, and `payment_expired` webhook consumers.

- [ ] Task 2: Replace implicit paid-only matching with an explicit matching command (AC: 1, 2, 3, 4, 5)
  - [ ] Refactor `repositories.PaymentRepo.MarkPaidByTransaction` into an explicit matching boundary such as `MatchFinalizedTransaction` returning a typed result; keep a compatibility wrapper only if existing callers/tests still need it.
  - [ ] Match only finalized/confirmed transactions. Preserve the Story 3.2 finality rule: pre-finality tx cannot mark a payment paid, underpaid, overpaid, partial, failed, or ledger-available.
  - [ ] Check wallet/address scope, selected chain, selected token/symbol, and raw amount policy before mutating payment status.
  - [ ] Define amount policy unambiguously. Existing code silently accepts up to 0.5% underpayment and rejects over 120% as no-match; this story must turn those branches into explicit outcomes instead of silent skip/success.
  - [ ] Guard idempotency with stable transaction unique hash and row/advisory locks so repeated matching is no-op and cannot produce duplicate lifecycle/webhook side effects.
  - [ ] Expired sessions must not become `paid` automatically. A finalized deposit after expiry must produce the configured exception/outcome and/or scoped reconciliation record.

- [ ] Task 3: Integrate deposit finality settlement without losing ledger authority (AC: 1, 2, 3, 4)
  - [ ] Update `services/deposits.Service.settleFinalizedTransaction` to consume the new matching result instead of assuming changed == paid.
  - [ ] Do not skip ledger availability for finalized money received on a checkout wallet just because the payment outcome is underpaid, overpaid, expired, or partial. Payment outcome is lifecycle state; ledger entries remain the balance authority.
  - [ ] Ensure exact paid outcomes still call `LedgerRepo.PostDepositAvailable` and enqueue the payment lifecycle event exactly once.
  - [ ] For non-success matched outcomes, make the follow-up discoverable through payment outcome fields, webhook/outbox event, or scoped reconciliation job without leaking tenant data.
  - [ ] Keep standalone/static wallet deposit behavior from Story 3.2 intact.

- [ ] Task 4: Update checkout, V1 API, webhook catalog, and integration docs (AC: 1, 2, 3, 4)
  - [ ] Update `api/handlers/payment.go` so checkout state, status JSON, realtime event, and terminal/payable flags distinguish `underpaid`, `overpaid`, and `partial_paid` from `paid`, `pending`, and `confirming`.
  - [ ] Update `api/handlers/v1api.go` payment response/status table/history filters so API consumers can see the explicit outcome and raw shortfall/excess metadata where available.
  - [ ] Update `services/webhook/event_catalog.go`, `docs/money-event-catalog.md`, and notifier payload tests for the selected outcome event policy. New dotted events should remain versioned; legacy underscore aliases must not break existing consumers.
  - [ ] Update `docs/integration-guide.md` and contract tests so merchants know exact, underpaid, overpaid, partial, expired-after-deposit, wrong-asset, and wrong-chain behavior.
  - [ ] Apply UX contract: checkout shows one clear state at a time; paid is terminal and stable; underpaid/failed/overpaid/partial are visually distinct from pending and paid.

- [ ] Task 5: Tests, contracts, validation, and story record (AC: 1, 2, 3, 4, 5)
  - [ ] Add repository tests for exact match, wrong asset, wrong chain, underpaid, overpaid, partial policy, expired interaction, duplicate transaction unique hash, and repeated matching no-op.
  - [ ] Add deposit service tests proving matching runs only after finality and that ledger posting is not skipped for finalized non-paid payment outcomes.
  - [ ] Add checkout/API/webhook/docs contract tests for every public status/event introduced or changed.
  - [ ] Targeted validation: `go test -count=1 ./repositories ./services/deposits ./api/handlers ./services/webhook ./docs`.
  - [ ] Full validation: `go test -count=1 ./...`.
  - [ ] Static validation: `go vet ./...`.
  - [ ] Whitespace validation: `git diff --check && git diff --cached --check`.
  - [ ] Update Dev Agent Record, Completion Notes, File List, Change Log, and story status.

## Dev Notes

### Current Implementation Snapshot

- `models.PaymentSession` currently has statuses `pending`, `awaiting_payment`, `paid`, `canceled`, `expired`, `failed`, and `underpaid`; it has no explicit `overpaid` or `partial_paid` status and no outcome reason/shortfall/excess fields.
- `repositories.PaymentRepo.MarkPaidByTransaction` is the current matching boundary. It only considers finalized confirmed transactions, filters by wallet, `awaiting_payment`, selected chain, selected symbol, and selected token, then silently skips candidates below a 99.5% threshold or above a 120% upper bound.
- The current exact/success path sets `PaymentStatusPaid`, `PaidAt`, `ConfirmedAt`, `TxUniqueHash`, `TxHash`, and `WebhookEvent = "payment_succeeded"` inside a transaction guarded by advisory lock `payment-tx:<unique_hash>` and `tx_unique_hash` uniqueness.
- `services/deposits.Service.settleFinalizedTransaction` calls `PaymentRepo.MarkPaidByTransaction` only after `TransactionRepo.MarkFinality(..., true)`. If `changed == true`, it posts `LedgerRepo.PostDepositAvailable`. If no paid match and the wallet product is `static:` or `wallet:`, it posts `PostStandaloneDepositAvailable`.
- Important risk: finalized money received on a checkout wallet can currently fall through without an available ledger posting when it does not become `paid`. Story 3.4 must close this gap while keeping payment success separate from ledger balance authority.
- Checkout/UI already supports `underpaid` display in `api/handlers/payment.go`; it does not yet expose `overpaid` or `partial_paid`.
- V1 payment status table currently documents `pending`, `awaiting_payment`, `paid`, `expired`, `canceled`, `failed`, and `underpaid`.
- Webhook catalog currently includes canonical `payment.succeeded.v1`, `payment.failed.v1`, and `payment.expired.v1`, with legacy aliases `payment_succeeded`, `payment_failed`, and `payment_expired`.

### Previous Story Intelligence

- Story 3.2 established that chain listeners produce durable chain facts only; payment/ledger mutation happens in the deposit boundary after finality.
- Story 3.3 established ledger-only balance authority, `LedgerRepo.WalletBalancesByWalletIDs`, scoped invariant reconciliation, GORM-owned ledger check constraints, and review-fixed reconciliation reason hashing.
- Preserve Story 3.3 guardrails: payment lifecycle tables are not balance authority; any uncertain money state should open a scoped reconciliation job with correlation id plus merchant/domain context and no secrets.
- Recent validation pattern uses isolated cache because the global Go cache has been flaky: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./...` and `GOCACHE=/tmp/gateway-gocache-bmad go vet -p=1 ./...`.

### Architecture And Product Guardrails

- AD-3: available, pending, hold, transit, and adjustment balances derive from ledger entries only; payment sessions may hold lifecycle state but are not balance stores.
- AD-4: chain indexers/listeners must not mark payments paid or post ledger entries directly.
- AD-5/AD-9: money-affecting transitions need stable idempotency and versioned event/outbox semantics; source modules enqueue events instead of inline delivery.
- AD-8: Webhook boundary owns delivery/retry/replay/HMAC; payment matching should set/enqueue lifecycle events but must not deliver synchronously.
- AD-11: expired-after-deposit, wrong asset/chain, overpayment, or unsupported partial matching are uncertain money states and should be visible to reconciliation/operator flows.
- PRD FR16: amount mismatch must not silently become success; underpaid/overpaid/partial tests must cover checkout and webhook contracts.
- UX: checkout must show one state at a time; paid is terminal/stable; underpaid/failed/overpaid/partial must be distinct from pending and paid.

### Implementation Boundaries

- Prefer extending `PaymentRepo` and `services/deposits` over adding a new service package unless it removes real duplication. Do not introduce external brokers or new dependencies.
- Keep raw amount math with `math/big` and integer strings; do not compare formatted decimals or quote display strings.
- If adding `PaymentSession` columns or constraints, update `services/database.VerifySchema` and schema contract tests.
- If adding new payment outcome events, update both code catalog and docs catalog snapshots; do not remove legacy aliases without a migration story.
- Tenant isolation is mandatory: matching must not reveal or mutate another merchant/domain's payment.

### Likely Files To Touch

- `models/payment_session.go`
- `repositories/payment_repo.go`
- `repositories/payment_repo_test.go`
- `services/deposits/service.go`
- `services/deposits/service_test.go`
- `api/handlers/payment.go`
- `api/handlers/payment_test.go`
- `api/handlers/v1api.go`
- `api/handlers/v1api_test.go` or existing handler tests
- `services/webhook/event_catalog.go`
- `services/webhook/event_catalog_test.go`
- `services/webhook/notifier.go`
- `services/webhook/notifier_test.go`
- `docs/money-event-catalog.md`
- `docs/integration-guide.md`
- `docs/integration_contract_test.go`
- `services/database/database.go`
- `services/database/database_test.go`
- `services/database/outbox_schema_contract_test.go`

### References

- Story source: `_bmad-output/planning-artifacts/epics.md` Story 3.4.
- PRD FR15/FR16: `_bmad-output/planning-artifacts/prds/prd-gateway-2026-06-27/prd.md`.
- UX checkout states: `_bmad-output/planning-artifacts/ux-designs/ux-gateway-2026-06-27/EXPERIENCE.md`.
- Architecture AD-3/AD-4/AD-5/AD-8/AD-9/AD-11: `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md`.
- Previous story: `_bmad-output/implementation-artifacts/3-3-enforce-ledger-derived-balance-authority.md`.
- Project rules: `_bmad-output/project-context.md`.

## Dev Agent Record

### Agent Model Used

Codex

### Debug Log References

- 2026-06-27: Story created from Epic 3.4 with FR15/FR16, UX checkout state requirements, architecture money lifecycle guardrails, current `PaymentRepo.MarkPaidByTransaction` behavior, and Story 3.3 ledger authority learnings.

### Completion Notes List

- Ready for dev-story implementation.

### File List

- `_bmad-output/implementation-artifacts/3-4-model-payment-matching-outcomes-explicitly.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

### Change Log

- 2026-06-27: Created story with explicit payment matching outcome scope, current paid-only matching risks, ledger authority constraints, webhook/API/checkout contract requirements, and validation plan.
