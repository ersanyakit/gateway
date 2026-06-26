---
story_id: "1.2"
story_key: "1-2-idempotent-payment-session-creation"
epic: "Epic 1: Partner Integration & Payment Intake Hardening"
status: done
created: 2026-06-27
updated: 2026-06-27
baseline_commit: ca49fb274a9b1581c935363fc0cb0a4c18647459
---

# Story 1.2: Idempotent Payment Session Creation

Status: done

## Story

As a developer integrator,
I want to create payment sessions idempotently with a stable checkout URL and quote snapshot,
so that checkout creation is safe to retry and produces a predictable payment contract for the merchant.

## Requirements Trace

- **FRs:** FR5, FR10, FR40
- **NFRs:** NFR2, NFR11, NFR14, NFR19
- **PRD:** `_bmad-output/planning-artifacts/prds/prd-gateway-2026-06-27/prd.md`
- **UX:** `_bmad-output/planning-artifacts/ux-designs/ux-gateway-2026-06-27/EXPERIENCE.md`
- **Architecture:** `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md`
- **Project Context:** `_bmad-output/project-context.md`

## Acceptance Criteria

1. Given a merchant or exchange tenant sends a valid authenticated payment session creation request, when the request includes supported chain/token, amount, currency, callback metadata, and an idempotency key, then the system creates a payment session scoped to the tenant/domain and returns a stable session id, checkout URL, expiry, selected asset information, expected raw amount, and deposit address reference.
2. Given the payment session requires pricing conversion, when the quote is calculated, then the system stores a quote snapshot with price, currency, decimals, symbol/token metadata, and timestamp, and later display or settlement logic does not recalculate the original fiat amount from current oracle prices.
3. Given the same idempotency key and same request payload are submitted again, when the create endpoint handles the retry, then the system returns the original payment session response and does not create a duplicate session, duplicate wallet assignment, or duplicate downstream lifecycle state.
4. Given the same idempotency key is reused with a different request payload, when the create endpoint validates idempotency, then the system rejects the request with a conflict response and the response uses the backwards-compatible error envelope.
5. Given the selected chain/token is unsupported, disabled, or missing required token metadata, when the create endpoint validates the request, then the system rejects the request before creating a payment session and records no partial payment session or wallet assignment.
6. Given a session expiry is reached before settlement, when the payment session status is queried through the partner API or checkout status surface, then the session reports an expired state consistently and expiry behavior is covered by automated tests.
7. Given the payment session creation path is implemented, when contract and integration tests run, then they cover successful creation, idempotent retry, idempotency conflict, unsupported asset rejection, quote snapshot persistence, expiry behavior, and response schema compatibility.

## Tasks / Subtasks

- [x] Task 1: Normalize payment create request and response contracts (AC: 1, 4, 7)
  - [x] Extend or map `types.PaymentCreateParams` and `types.V1InvoiceRequest` so create requests can include optional selected asset fields: `chain_id`, `symbol`, and `token`/token identifier without breaking the existing checkout-later flow.
  - [x] Keep existing public `/payments/create` behavior backwards-compatible for clients that omit asset selection; do not force checkout asset selection on legacy clients unless tests and docs are updated.
  - [x] Make `/api/v1/payment/create` and `/api/v1/payment/white-label` return the documented V1 envelope shape `{"result":"ok","data":...}` / `{"result":"error","message":"..."}` instead of leaking the legacy `{"success":...}` shape.
  - [x] Include stable create response fields for `payment_id`, `track_id`/`session_token`, `checkout_url`, `status`, `expires_at`, `order_id`, `amount`, `currency`, and selected asset fields when an asset is selected.

- [x] Task 2: Apply idempotency before payment/wallet mutation (AC: 3, 4, 5, 7)
  - [x] Preserve current idempotency key source: `Idempotency-Key` header first, then `order:<order_id>` fallback.
  - [x] Ensure request hashing uses normalized request data and includes newly supported selected asset fields.
  - [x] On same key and same payload, return the cached original create response exactly enough that client-visible ids, checkout URL, expiry, selected asset fields, and status remain stable.
  - [x] On same key and different payload, return conflict without creating a new `PaymentSession`, wallet assignment, price quote, or other lifecycle state.
  - [x] Keep idempotency scope tenant/domain-based (`domain_id + key`) and do not broaden it to merchant-global or process-local state.

- [x] Task 3: Validate selected asset and quote before partial creation (AC: 1, 2, 5, 7)
  - [x] If `chain_id`/`symbol`/`token` are supplied, validate the asset via `asset.Registry` before creating payment session or wallet records.
  - [x] Reject unsupported chain/token, disabled/missing metadata, invalid decimals, or unavailable price provider before mutating payment/wallet state.
  - [x] For fiat-to-crypto conversion, calculate expected raw amount once and persist `models.PriceQuote` with `PaymentID`, chain, token, symbol, decimals, fiat currency, fiat amount, expected raw amount, price source, price, quoted_at, and expires_at.
  - [x] For crypto-denominated amount where currency matches the selected asset/canonical symbol, keep fixed price source behavior and raw amount conversion.
  - [x] Do not recalculate quote values in create retry responses; use persisted session/quote data or cached idempotency response.

- [x] Task 4: Preserve checkout expiry and partner status consistency (AC: 6, 7)
  - [x] Keep `paymentSessionTTL()` behavior unless tests deliberately change it.
  - [x] Ensure partner API `payment/info` and checkout status surfaces report expired consistently when `ExpiresAt` is in the past and status is not terminal paid/canceled/failed.
  - [x] Avoid marking a payment paid or posting ledger entries in this story; finality and settlement belong to later deposit/ledger stories.

- [x] Task 5: Update contract docs and tests (AC: 1-7)
  - [x] Update Swagger comments and regenerate `docs/docs.go`, `docs/swagger.json`, and `docs/swagger.yaml` if public request/response structs change.
  - [x] Add focused tests for create success with selected asset, idempotent retry, idempotency conflict, unsupported asset rejection before mutation, quote snapshot persistence, expiry status behavior, and V1 response envelope compatibility.
  - [x] Add repository or helper tests around `IdempotencyRepo.RequestHash`/conflict behavior where DB-backed testing is not required.
  - [x] Run targeted package tests for changed handlers/repositories/types.
  - [x] Run `go test ./...`.
  - [x] Run `go vet ./...` if the repo state supports it.
  - [x] Update Dev Agent Record, File List, Change Log, and story status according to `bmad-dev-story`.

### Review Findings

- [x] [Review][Patch] Require token identifier for non-native selected assets [api/handlers/payment.go:993] — Removed the create-time `GetBySymbol` fallback so ERC20/TRC20/SPL selections cannot bind an arbitrary registry entry when token is omitted; added a regression test.
- [x] [Review][Patch] Document actual v1 payment detail `deposit_address` field [types/v1api.go:238] — Added `deposit_address` to `V1PaymentDetail` and regenerated Swagger so partner info/history docs include the field returned by `v1PaymentResponse`.
- [x] [Review][Patch] Document idempotency conflict responses [api/handlers/payment.go:160, api/handlers/v1api.go:684] — Added 409 Swagger annotations for legacy and v1 create endpoints and regenerated Swagger.

## Dev Notes

### Current Implementation Snapshot

- `api/handlers/payment.go` owns the shared payment create flow used by both public `/payments/create` and v1 payment create wrappers.
- `HandlePaymentCreate` currently:
  - verifies v1 signed auth only when `PaymentHandlerDeps.RequireSignature` is true;
  - binds into `types.PaymentCreateParams`;
  - resolves domain from `X-API-Key` or bearer token;
  - uses `paymentIdempotencyKey(c, params)` to select `Idempotency-Key` or `order:<order_id>`;
  - calls `IdempotencyRepo.Begin` before wallet/session creation;
  - creates/reuses a wallet via `WalletRepo.Create`;
  - creates a `PaymentSession`;
  - caches the raw JSON response in `IdempotencyRepo.Complete`.
- Current create response is legacy `{"success":true,...}` even when called through `/api/v1/payment/create`. This contradicts the documented `types.V1PaymentCreateResponse` envelope.
- Asset selection and quote persistence currently happen later in `HandleCheckoutSelectAsset`, not at create time:
  - `findCheckoutAsset` validates selected asset.
  - `checkoutExpectedQuote` calculates `amountRaw`, price, and source.
  - `PaymentRepo.SelectAsset` writes selected asset fields and creates `models.PriceQuote` in the same transaction.
- `types.V1InvoiceRequest` documents `description`, but `HandlePaymentCreate` binds `types.PaymentCreateParams`, which does not currently include `description`.
- `models.PaymentSession` already stores `SelectedChainID`, `SelectedToken`, `SelectedSymbol`, `SelectedDecimals`, `ExpectedAmountRaw`, `DepositAddress`, `ExpiresAt`, and `IdempotencyKey`.
- `models.PriceQuote` already stores the required quote snapshot fields and is AutoMigrated.
- `models.IdempotencyKey` has unique scope on `DomainID + Key`; `IdempotencyRepo.Begin` returns `ErrIdempotencyConflict` on hash mismatch and returns cached response or in-progress state on repeat.
- `WalletRepo.Create` is idempotent by `merchant_id + domain_id + product_id + user_id`, uses a Postgres advisory lock for HD index allocation, and backfills addresses when an existing wallet is reused.
- Partner status APIs currently use `v1PaymentResponse`; verify whether expired sessions are lazily marked expired or just reported from persisted status.

### Relevant Files

- Update likely:
  - `api/handlers/payment.go`
  - `api/handlers/v1api.go`
  - `types/payment.go`
  - `types/v1api.go`
  - `docs/docs.go`
  - `docs/swagger.json`
  - `docs/swagger.yaml`
- Update if needed:
  - `repositories/payment_repo.go`
  - `repositories/idempotency_repo.go`
  - `models/payment_session.go`
  - `models/price_quote.go`
- Tests likely:
  - `api/handlers/payment_test.go`
  - `api/handlers/v1api_test.go`
  - `repositories/*_test.go` or package-level helper tests if DB-backed repository tests are not practical.
- Read before modifying:
  - `repositories/wallet_repo.go`
  - `repositories/payment_repo.go`
  - `repositories/idempotency_repo.go`
  - `types/payment.go`
  - `types/v1api.go`
  - `services/pricing/*`
  - `api/routes/routes.go`

### Architecture Compliance

- Follow AD-5: every money-affecting create path must be idempotent and duplicate-safe.
- Follow AD-10: reason in tenant/domain scope even where current code says merchant/domain.
- Follow AD-12: do not claim production funds readiness from this story; this is partner create-path hardening only.
- Follow the project context rule: public webhook signing remains `timestamp + body`; v1 request signing remains method/path/query/timestamp/body from Story 1.1.
- Do not introduce an external broker, external cache, new service boundary, or SPA architecture.

### Implementation Guardrails

- Validate selected chain/token and quote availability before creating wallet/session when selection fields are supplied, so unsupported asset requests leave no partial records.
- Preserve existing checkout-later behavior for clients that create a pending session without selected asset fields.
- Do not mutate ledger, mark paid, emit money lifecycle events, or send webhooks in this story.
- Keep `IdempotencyRepo` DB-backed; do not replace it with in-memory state.
- Do not broaden request hash with volatile fields such as `Context`, timestamps, or generated ids.
- Do not recalculate a completed idempotency response from current price oracle values.
- Preserve backwards-compatible public `/payments/create` contract while making `/api/v1/payment/create` match the documented v1 envelope.
- If a conflict response is produced through v1 endpoints, use `v1Err` envelope and HTTP 409.
- If a conflict response is produced through the legacy endpoint, preserve the existing `{"success":false,"error":"..."}` envelope unless deliberately changing and updating docs/tests.
- Keep auth behavior from Story 1.1 intact; signed v1 request tests should continue to pass.

### Previous Story Intelligence

- Story 1.1 established v1 signed request verification with method/path/query/timestamp/body canonicalization.
- Auth failures now produce generated request correlation IDs and redact sensitive values.
- Fresh-context code review caught generated Swagger drift; any Story 1.2 public contract change must regenerate `docs/docs.go`, `docs/swagger.json`, and `docs/swagger.yaml`.
- Tests that exercise v1 auth live in `api/handlers/v1_auth_test.go`; avoid duplicating auth internals in payment-create tests unless needed for integration coverage.
- The multi-instance replay-store limitation was explicitly out of scope for the current monolith phase; do not expand Story 1.2 into distributed idempotency infrastructure beyond the existing Postgres idempotency table.

### Testing Requirements

- Use standard Go `testing`, `httptest`, and Fiber test style already present in `api/handlers/*_test.go`.
- Add tests that prove a selected-asset create request:
  - validates asset before mutation;
  - stores session selected asset fields;
  - persists quote snapshot data;
  - returns selected asset and expected raw amount in the response.
- Add idempotency tests:
  - same key + same payload returns the original response with stable ids and no duplicate session/wallet;
  - same key + different payload returns conflict;
  - conflict does not create partial payment/wallet/quote state.
- Add v1 contract tests:
  - success envelope is `{"result":"ok","data":...}`;
  - conflict/error envelope is `{"result":"error","message":"..."}`;
  - `/api/v1/payment/white-label` behaves consistently with `/api/v1/payment/create`.
- Add expiry behavior tests for partner payment info or checkout status helper/path.
- Targeted tests before full suite: `go test ./api/handlers ./repositories ./types` if those packages are touched.
- Full validation: `go test ./...` and `go vet ./...`.

### References

- `_bmad-output/planning-artifacts/epics.md` - Story 1.2
- `_bmad-output/planning-artifacts/prds/prd-gateway-2026-06-27/prd.md` - FR5, FR10, FR40, NFR2, NFR11, NFR14, NFR19
- `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md` - AD-5, AD-10, AD-12, idempotency invariant table
- `_bmad-output/planning-artifacts/ux-designs/ux-gateway-2026-06-27/EXPERIENCE.md` - checkout and partner diagnostics consistency
- `_bmad-output/project-context.md` - project rules for auth, idempotency, money movement, testing, docs regeneration
- `_bmad-output/implementation-artifacts/1-1-secure-partner-api-request-authentication.md` - previous story learnings

## Project Structure Notes

- Current payment create is still in `api/handlers/payment.go`; do not move it into a new module just for this story.
- V1 wrappers are in `api/handlers/v1api.go`; they currently delegate to the shared payment handler.
- Repository ownership stays under `repositories/`; do not bypass `PaymentRepo`, `WalletRepo`, or `IdempotencyRepo` for durable create-path state.
- Recent commits include `Harden v1 partner request authentication` and `Complete WDS auth story hardening`; preserve those auth and Swagger contract changes.

## Dev Agent Record

### Agent Model Used

Codex

### Debug Log References

- `go test ./api/handlers ./repositories ./types`
- `swag init -g main.go -o docs`
- `go test ./...`
- `go vet ./...`

### Implementation Plan

- Keep `/payments/create` backwards-compatible while extracting a shared create core that can render either legacy or v1 response envelopes.
- Validate and quote selected create-time assets before wallet/session mutation, then reuse `PaymentRepo.SelectAsset` so selected session fields and `PriceQuote` persist transactionally.
- Preserve DB-backed idempotency through `IdempotencyRepo.Begin`/`Complete`; extend normalized request hashing by adding selected asset fields to `PaymentCreateParams`.
- Report expired status consistently through a shared response-status helper without marking paid, posting ledger entries, or emitting lifecycle webhooks.

### Completion Notes List

- Extended `types.PaymentCreateParams`, `types.V1InvoiceRequest`, `types.PaymentCreateResponse`, `types.V1PaymentCreatedData`, and `types.V1PaymentDetail` with selected asset contract fields and normalized validation.
- Refactored payment creation into a shared core with legacy and v1 modes; `/api/v1/payment/create` and `/api/v1/payment/white-label` now return v1 success/error envelopes.
- Added create-time selected asset validation and quote preparation before wallet/session creation, then persisted selected session fields and `models.PriceQuote` through `PaymentRepo.SelectAsset`.
- Preserved idempotency key sourcing and DB-backed domain/key scope; cached create responses remain raw JSON and request hashes now include normalized selected asset fields.
- Added response status handling so pending/awaiting sessions with past expiry report `expired` through create/info response helpers while terminal statuses remain unchanged.
- Regenerated Swagger docs for the public contract changes, including selected asset fields and idempotency conflict responses.
- Added helper-level tests for selected asset quote preparation, non-native token requirement, unsupported asset rejection before mutation, v1 success/error envelopes, expiry response status, and idempotency request hashing.
- Added handler-level integration tests with in-memory repo boundaries covering selected-asset create success, quote snapshot persistence, cached idempotent retry without quote recalculation, idempotency conflict without duplicate wallet/session/quote mutation, unsupported asset rejection before mutation, and v1 success/conflict envelopes.
- Validation passed: `go test ./api/handlers ./repositories ./types`, `go test ./...`, and `go vet ./...`.

### File List

- `api/handlers/payment.go`
- `api/handlers/payment_test.go`
- `api/handlers/v1api.go`
- `repositories/idempotency_repo_test.go`
- `types/payment.go`
- `types/v1api.go`
- `types/validation_test.go`
- `docs/docs.go`
- `docs/swagger.json`
- `docs/swagger.yaml`
- `_bmad-output/implementation-artifacts/1-2-idempotent-payment-session-creation.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

### Change Log

- 2026-06-27: Story created with PRD, UX, architecture, project-context, previous-story, and current-code context.
- 2026-06-27: Implemented idempotent selected-asset payment session creation, v1 response contract, expiry response consistency, contract docs, and tests.
- 2026-06-27: Addressed code review findings - 3 items resolved.
