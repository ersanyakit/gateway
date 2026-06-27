---
story_id: "1.5"
story_key: "1-5-stable-partner-api-contract-and-integration-evidence"
epic: "Epic 1: Partner Integration & Payment Intake Hardening"
status: review
created: 2026-06-27
updated: 2026-06-27
baseline_commit: 93b2f5f7f66701be6a0d9ba697ca3c4416adefe6
---

# Story 1.5: Stable Partner API Contract and Integration Evidence

Status: review

## Story

Bir developer integrator olarak,
partner API contract ve integration examples'in stabil ve test-backed kalmasini istiyorum,
boylece merchant, dealer ve exchange entegrasyonlari response formatlarini veya error davranisini tahmin etmeden guvenle upgrade edebilir.

## Requirements Trace

- **FRs:** FR1, FR2, FR3, FR9, FR10, FR40
- **NFRs:** NFR11, NFR14, NFR15
- **PRD:** `_bmad-output/planning-artifacts/prds/prd-gateway-2026-06-27/prd.md`
- **UX:** `_bmad-output/planning-artifacts/ux-designs/ux-gateway-2026-06-27/EXPERIENCE.md`
- **Architecture:** `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md`
- **Project Context:** `_bmad-output/project-context.md`

## Acceptance Criteria

1. Given partner-facing payment session, static wallet, checkout status, and authentication endpoints exist, when OpenAPI or equivalent API documentation is generated or updated, documented request/response schemas match implemented handlers and required auth, idempotency, error, and tenant/domain scope fields are documented.
2. Given partner receives an error response from authentication, validation, idempotency conflict, unsupported asset, expired session, or authorization failure, when response is serialized, it follows a backwards-compatible error envelope and tests verify no sensitive implementation details or resource-existence leaks are exposed.
3. Given existing merchant/dealer integrations use current API fields, when contract is updated for Epic 1 changes, existing compatible fields remain available or are explicitly marked as deprecated and no breaking change is introduced without a documented migration note.
4. Given developer follows integration guide, when they create a payment session, retry with the same idempotency key, request a static wallet, and open checkout status, documented examples produce behavior consistent with automated contract tests and examples include at least one success path and one failure/conflict path.
5. Given partner API contract tests run in CI or local verification, when handlers, schemas, or response envelopes change, tests fail if implemented responses drift from the documented contract and verification includes `go test ./...` or the repo's equivalent targeted test command for affected packages.
6. Given Epic 1 is complete, when developer reviews integration evidence, they can identify covered endpoints, supported auth modes, idempotency behavior, static wallet scope rules, checkout state semantics, and known production limitations.

## Tasks / Subtasks

- [x] Task 1: Inventory partner contract surfaces and current drift (AC: 1, 3, 6)
  - [x] Inventory partner-facing endpoints covered by Epic 1: `/api/v1/payment/create`, `/api/v1/payment/white-label`, `/api/v1/payment/static-address`, `/api/v1/payment/static-addresses`, `/api/v1/payment/info`, `/api/v1/payment/history`, `/api/v1/payment/status-table`, `/checkout/{token}/status.json`, `/api/v1/common/readiness`, and signed V1 auth behavior.
  - [x] Compare implemented handlers/types, Swagger artifacts, and `docs/integration-guide.md` for request fields, response envelope, auth headers, idempotency headers, status enums, tenant/domain scope, and error envelope.
  - [x] Record any deliberately unsupported or deferred production behavior in integration evidence instead of implying production custody or exchange-grade readiness.
  - [x] Preserve legacy public fields where they still exist; if any field is removed or renamed, add a migration/deprecation note before changing behavior.

- [x] Task 2: Harden integration guide and OpenAPI contract docs (AC: 1, 3, 4, 6)
  - [x] Update V1 request signing docs to match the implemented canonical request signature: method, original path/query target, timestamp, and raw body. Keep webhook signing separately documented as `timestamp + raw_body`.
  - [x] Update hosted payment create docs for V1 envelope `{"result":"ok","data":...}`, idempotency key behavior, selected asset fields, `track_id`/`session_token`, checkout URL, expiry, and stable error envelope.
  - [x] Update static address docs for `product_id`, `chain_id`, `symbol`, `token`, `address`, `label`, `created_at`, deterministic domain/product/user/asset scope, non-native token requirement, and unsupported asset failure.
  - [x] Update checkout status docs for payer-facing states `active`, `pending`, `confirming`, `paid`, `expired`, `canceled`, `failed`, `underpaid`, plus `paid`, `payable`, `terminal`, `payment_id`, `tx_hash`, `success_path`, and `cancel_path`.
  - [x] Include one success path and one failure/conflict path that can be verified by automated tests: create payment, repeat same idempotency key, conflict same key with different payload, create static address, query checkout status.
  - [x] Regenerate Swagger artifacts with `swag init -g main.go -o docs` when route comments, public structs, or examples change.

- [x] Task 3: Add contract and error-envelope tests (AC: 1, 2, 5)
  - [x] Add tests that verify V1 auth, validation, idempotency conflict, unsupported asset, expired/not-found payment, and authorization failure responses use `{"result":"error","message":"..."}` and do not return API secrets, webhook secrets, raw signatures, mnemonics, private keys, stack traces, or unredacted diagnostic blobs.
  - [x] Add tests that tenant/domain isolation does not reveal whether another domain's payment or wallet resource exists.
  - [x] Add tests that payment create and static address examples match implemented response keys and envelope shape.
  - [x] Add tests that checkout status JSON exposes only payer-safe contract fields and all documented status values remain accepted/documented.
  - [x] Prefer structured JSON parsing and existing handler fake patterns over brittle string matching; keep tests close to `api/handlers/*_test.go` unless a docs-level test is cleaner.

- [x] Task 4: Build integration evidence for Epic 1 completion (AC: 4, 5, 6)
  - [x] Add or update a focused contract evidence section in `docs/integration-guide.md` or a linked docs artifact that lists covered endpoints, auth modes, idempotency semantics, static wallet scope rules, checkout state semantics, and known production limitations.
  - [x] Document that current code exposes `merchant/domain` while architecture names the ownership boundary `tenant/domain`; do not invent a new tenant API in this story.
  - [x] Document that real-funds production custody remains gated by signer, reconciliation, webhook recovery, observability, migration, backup/restore, and compliance readiness stories.
  - [x] Keep webhook event naming compatibility clear: existing underscore events remain supported aliases until an explicit event catalog migration; new dotted versioned events belong to Epic 2.

- [x] Task 5: Validate contract stability and update story record (AC: 1, 2, 3, 4, 5, 6)
  - [x] Targeted validation: `go test -count=1 ./api/handlers ./types`.
  - [x] Docs/contract validation: run any new docs or contract test package added by this story.
  - [x] Full validation: `go test -count=1 ./...`.
  - [x] Static validation: `go vet ./...`.
  - [x] Whitespace validation: `git diff --check`.
  - [x] Update Dev Agent Record, Completion Notes, File List, Change Log, and set story status according to `bmad-dev-story`.

## Dev Notes

### Current Implementation Snapshot

- `docs/integration-guide.md` is served by `api/handlers/docs.go` and is part of the partner-facing documentation surface.
- V1 REST handlers live mainly in `api/handlers/v1api.go`; V1 auth helpers live in `api/handlers/v1_auth.go`.
- Public V1 request/response structs live in `types/v1api.go`; hosted checkout status response lives in `types/payment.go`.
- Swagger artifacts live under `docs/` and are regenerated with `swag init -g main.go -o docs`.
- V1 write auth uses `helpers.GenerateRequestSignature` / `helpers.VerifyRequestSignature`, binding method, path/query target, timestamp, and body. Webhook signing uses `helpers.GenerateSignature`, binding timestamp and raw body only.
- V1 error responses use `v1Err(c, status, msg)` and must preserve `{"result":"error","message":"..."}`.
- Hosted checkout status JSON uses `types.PaymentStatusResponse` and payer-facing state mapping from Story 1.4.
- Static address request/response fields were expanded in Story 1.3 with `product_id`, `chain_id`, `token`, and `created_at`.
- Payment create V1 responses are wrapped in `types.V1PaymentCreateResponse`, not the legacy public `/payments/create` `success` envelope.

### Known Contract Drift To Address

- `docs/integration-guide.md` currently documents V1 request signing as `timestamp + body`, but implemented V1 request signing binds method and path/query as well. Webhook docs should keep `timestamp + raw_body`.
- The hosted payment create guide currently shows a legacy `{"success":true,...}` response for `/api/v1/payment/create`; it should show the V1 `result/data` envelope and current `V1PaymentCreatedData` fields.
- Static address docs currently omit `product_id`, `chain_id`, `token`, and `created_at`, and do not explain deterministic domain/product/user/asset scope from Story 1.3.
- Payment status docs currently omit `underpaid` and the Story 1.4 checkout status fields `payment_id`, `tx_hash`, `payable`, and `terminal`.
- The guide does not yet include an idempotency conflict example or a contract evidence summary for Epic 1.

### Architecture Compliance

- AD-2: Merchant checkout/static-address flows and exchange wallet-provider flows share one money core. This story must not duplicate money movement code while documenting both surfaces.
- AD-5 / FR10: Partner write APIs must document idempotency keys and conflict behavior; tests should fail on response drift.
- AD-8: Webhook details in the guide must keep webhook signing and event compatibility clear without moving webhook delivery behavior into payment/static wallet handlers.
- AD-10: Architecture-level ownership is `tenant/domain`; current code exposes `merchant/domain`. Docs should explain scope without claiming a new tenant API exists.
- AD-12: Integration evidence must state real-funds production limitations honestly and not claim signer/custody/reconciliation/ops gates are complete.

### Implementation Guardrails

- Prefer documentation and contract tests over behavior changes. If implementation drift is found, make the smallest compatible code fix with tests.
- Do not change V1 request signing semantics to match stale docs; fix the docs and examples to match code.
- Do not merge webhook and V1 request HMAC semantics.
- Do not expose or log secrets, raw signatures, private keys, mnemonics, or unredacted diagnostic blobs in tests, docs examples, or error responses.
- Do not introduce a new API version, SPA, service boundary, event broker, or production custody claim in this story.
- Do not break legacy `/payments/create` response shape while documenting `/api/v1/payment/create`; they are separate surfaces.
- Use structured JSON parsing for contract tests where possible; avoid brittle full-document string assertions unless checking a small required docs phrase.

### Previous Story Intelligence

- Story 1.1 established signed V1 request validation, replay resistance, error envelope behavior, and sensitive value redaction. This story should keep those semantics stable and documented.
- Story 1.2 established idempotent payment session creation, selected asset request/response fields, conflict behavior, and V1 response envelope. The guide must align with those fields.
- Story 1.3 established deterministic static wallet issuance by domain/product/user/asset scope, non-native token requirements, chain validation before mutation, and static address response metadata. The guide and tests must reflect that.
- Story 1.4 established payer-facing checkout states, terminal/payable fields, `underpaid` status handling, and safe checkout status/websocket payloads. Contract docs must include those states and safe fields.

### Testing Requirements

- Use standard Go `testing`, Fiber/httptest, and existing handler fake patterns in `api/handlers/*_test.go`.
- Add docs/contract tests that cover at least:
  - V1 payment create success envelope and idempotent repeat behavior.
  - V1 idempotency conflict error envelope.
  - V1 auth failure error envelope and redaction.
  - Static address unsupported asset error envelope and no mutation behavior where current fakes support it.
  - Tenant/domain isolation for payment or wallet lookup without resource-existence leak.
  - Checkout status safe JSON fields and documented state semantics.
- Keep network-free and deterministic. Do not use live RPCs, live wallet derivation dependencies, or production secrets in tests.
- Validation commands:
  - `go test -count=1 ./api/handlers ./types`
  - `go test -count=1 ./...`
  - `go vet ./...`
  - `git diff --check`

### Relevant Files

- Update likely:
  - `docs/integration-guide.md`
  - `api/handlers/payment_test.go`
  - `api/handlers/v1api_test.go`
  - `api/handlers/v1_auth_test.go`
  - `api/handlers/docs.go` or related tests if docs route behavior is verified
- Update if public comments/examples change:
  - `api/handlers/v1api.go`
  - `api/handlers/payment.go`
  - `types/v1api.go`
  - `types/payment.go`
  - `docs/docs.go`
  - `docs/swagger.json`
  - `docs/swagger.yaml`
- Read before modifying:
  - `helpers/credentials.go`
  - `docs/integration-guide.md`
  - `_bmad-output/implementation-artifacts/1-1-secure-partner-api-request-authentication.md`
  - `_bmad-output/implementation-artifacts/1-2-idempotent-payment-session-creation.md`
  - `_bmad-output/implementation-artifacts/1-3-deterministic-static-wallet-issuance-for-partner-scopes.md`
  - `_bmad-output/implementation-artifacts/1-4-hosted-checkout-payment-state-experience.md`

## Project Structure Notes

- Contract tests should stay near existing handler tests unless a docs-specific test package provides a clearer boundary.
- Partner documentation remains under `docs/`; do not create a separate docs site or frontend stack.
- Swagger artifacts remain generated files under `docs/`; update them only through the repo's generator.
- Story workflow state remains in `_bmad-output/implementation-artifacts/sprint-status.yaml`.

## Dev Agent Record

### Agent Model Used

Codex

### Debug Log References

- Red phase: `go test -count=1 ./docs` failed on stale V1 signing docs and missing Epic 1 evidence.
- Contract drift validation: `go test -count=1 ./docs ./api/handlers ./types` failed on integration guide snippets and stale Swagger idempotency docs; fixed docs/comments and regenerated Swagger.
- Swagger regeneration: `swag init -g main.go -o docs`
- Targeted validation: `go test -count=1 ./docs ./api/handlers ./types`
- Full validation: `go test -count=1 ./...`
- Static validation: `go vet ./...`
- Whitespace validation: `git diff --check`

### Completion Notes List

- Added docs-level contract tests that parse `docs/integration-guide.md`, `docs/swagger.json`, and Epic 1 evidence to catch partner API contract drift.
- Updated the integration guide for canonical V1 request signing, V1 `result/data` success envelope, V1 `result/message` error envelope, idempotency retry/conflict behavior, static address scope metadata, checkout status `terminal`/`payable`, webhook signing separation, and production limitations.
- Added Epic 1 integration evidence covering endpoints, auth modes, idempotency, static wallet domain/product/user/asset scope, checkout state semantics, error envelopes, and launch limitations.
- Updated V1 payment create Swagger comments to document `Idempotency-Key`, regenerated Swagger artifacts, and moved payment status-table rows to a typed helper that includes `is_final`.
- Verified targeted docs/handler/type tests, full repo tests, vet, and whitespace checks.

### File List

- `_bmad-output/implementation-artifacts/1-5-stable-partner-api-contract-and-integration-evidence.md`
- `_bmad-output/implementation-artifacts/1-5-stable-partner-api-contract-and-integration-evidence-validation.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `api/handlers/integration_guide_contract_test.go`
- `api/handlers/partner_contract_test.go`
- `api/handlers/v1api.go`
- `docs/docs.go`
- `docs/epic-1-integration-evidence.md`
- `docs/integration-guide.md`
- `docs/integration_contract_test.go`
- `docs/swagger.json`
- `docs/swagger.yaml`
- `types/v1api.go`

### Change Log

- 2026-06-27: Story created with PRD, UX, architecture, project-context, previous-story, and current-code contract drift context.
- 2026-06-27: Implemented Epic 1 partner contract docs, evidence, contract tests, Swagger idempotency documentation, and status-table `is_final` contract support.
