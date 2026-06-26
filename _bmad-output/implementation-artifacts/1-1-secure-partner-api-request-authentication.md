---
story_id: "1.1"
story_key: "1-1-secure-partner-api-request-authentication"
epic: "Epic 1: Partner Integration & Payment Intake Hardening"
status: done
created: 2026-06-27
updated: 2026-06-27
baseline_commit: db47e732f1b3a802ac7759132b95c7ca2e868559
---

# Story 1.1: Secure Partner API Request Authentication

Status: done

## Story

As a developer integrator,
I want partner API requests to be authenticated, scoped, and replay-resistant,
so that only authorized merchant or exchange tenants can perform actions against their own resources.

## Requirements Trace

- **FRs:** FR9, FR10, FR39, FR40
- **NFRs:** NFR2, NFR11, NFR13, NFR14, NFR18
- **PRD:** `_bmad-output/planning-artifacts/prds/prd-gateway-2026-06-27/prd.md`
- **UX:** `_bmad-output/planning-artifacts/ux-designs/ux-gateway-2026-06-27/EXPERIENCE.md`
- **Project Context:** `_bmad-output/project-context.md`

## Acceptance Criteria

1. Given a partner sends a request with a valid API key or bearer token, when the request reaches a protected v1 API endpoint, then the system resolves the correct tenant/domain scope and the request cannot access resources outside that scope.
2. Given a partner sends a mutating request, when `X-API-Secret`, timestamp, and `X-Gateway-Signature` are present and valid, then the request is accepted for downstream handling and signature verification uses the exact request method, path, timestamp, and body payload.
3. Given a mutating request has a missing, malformed, expired, or future-skewed timestamp, when auth middleware validates the request, then the system rejects it with the backwards-compatible error envelope and tests cover the allowed clock-skew window.
4. Given a previously accepted signed request is replayed with the same signature and timestamp, when replay protection evaluates the request, then the duplicate request is rejected or safely treated as non-mutating according to endpoint policy and signature reuse behavior is covered by automated tests.
5. Given an API key belongs to one tenant/domain, when it is used to request another tenant/domain's payment, wallet, payout, refund, or webhook resource, then the system returns an authorization failure and does not leak whether the target resource exists.
6. Given an authentication or authorization failure occurs, when the system logs the failure, then logs include tenant/domain context when known, endpoint, failure reason category, and request correlation id, and logs do not include API secrets, raw signatures, private keys, mnemonics, or full sensitive payloads.
7. Given the authentication changes are implemented, when the test suite runs, then it includes positive and negative tests for API key auth, bearer auth, HMAC validation, timestamp skew, replay/signature reuse, tenant scope isolation, and backwards-compatible error responses.

## Tasks / Subtasks

- [x] Task 1: Add canonical v1 request signing helpers (AC: 2, 3, 7)
  - [x] Add method/path/timestamp/body canonical signing helpers without breaking existing webhook signing helpers.
  - [x] Keep `helpers.GenerateSignature` and `helpers.VerifySignature` behavior for webhook delivery unless all call sites are deliberately migrated.
  - [x] Add helper tests for valid signature, modified method, modified path, modified body, malformed timestamp, expired timestamp, and future-skewed timestamp.

- [x] Task 2: Integrate canonical signature verification into v1 signed API auth (AC: 2, 3, 6, 7)
  - [x] Update `v1ResolveSignedDomain` or a small adjacent helper in `api/handlers/v1api.go` to verify method, original request path, timestamp, and body.
  - [x] Preserve API key lookup through `X-API-Key` and `Authorization: Bearer`.
  - [x] Preserve the backwards-compatible JSON error shape: `{"result":"error","message":"..."}`.
  - [x] Ensure auth failures do not echo API secrets, raw signatures, request bodies, private keys, or mnemonics.

- [x] Task 3: Add replay protection for signed mutating v1 requests (AC: 4, 6, 7)
  - [x] Add a no-new-dependency replay guard suitable for the current monolith phase.
  - [x] Key replay decisions by tenant/domain scope plus signature/timestamp/canonical request identity.
  - [x] Apply replay protection only after signature and timestamp validation succeeds.
  - [x] Bound replay memory by timestamp TTL/clock-skew cleanup so it cannot grow unbounded in long-running processes.
  - [x] Add tests proving first signed request succeeds and a replay with the same signature/timestamp/canonical request is rejected.

- [x] Task 4: Strengthen tenant/domain scope tests for v1 auth (AC: 1, 5, 7)
  - [x] Add tests for API key auth and bearer auth resolving the same domain.
  - [x] Add tests for API key/API secret mismatch returning unauthorized without resource-existence leakage.
  - [x] Add at least one handler-level test proving a domain-scoped lookup cannot access another domain's resource.

- [x] Task 5: Run validation commands and update story record (AC: 1-7)
  - [x] Run targeted tests for changed helpers and handlers.
  - [x] Run `go test ./...`.
  - [x] Run `go vet ./...` if it is currently passing in the repo state.
  - [x] Update Dev Agent Record, File List, Change Log, and story status according to `bmad-dev-story`.

### Review Findings

- [x] [Review][Patch] Bind query string in canonical signed request target — fixed by verifying and replay-keying signed v1 requests with `OriginalURL()` fallback.
- [x] [Review][Patch] Redact sensitive auth log values — fixed by replacing sensitive header/token patterns before logging auth errors.
- [x] [Review][Patch] Avoid API key/secret mismatch oracle — fixed by returning a generic invalid credential error while retaining internal log category.
- [x] [Review][Patch] Bound replay guard memory and cleanup cost — fixed by adding a hard entry cap, oldest-entry eviction, and interval-based TTL cleanup.
- [x] [Review][Patch] Document white-label signed-auth headers — fixed by adding signature headers and regenerating OpenAPI docs.
- [x] [Review][Patch] Guarantee auth failure correlation ID — fixed by generating `X-Request-ID` when missing and including it in auth logs.
- [x] [Review][Patch] Add required handler negative tests — fixed with v1 envelope assertions for missing key, missing secret, timestamp failures, and missing signature.
- [x] [Review][Patch] Complete story File List — fixed by listing all story-touched implementation, docs, and BMAD tracking files.

## Dev Notes

### Current Implementation Snapshot

- `api/handlers/v1api.go` contains the v1 auth helpers:
  - `v1ResolveDomain` accepts `X-API-Key` or `Authorization: Bearer <key>`.
  - `v1ResolveSignedDomain` resolves domain, checks `X-API-Secret`, validates `X-Gateway-Timestamp`, strips `sha256=` from `X-Gateway-Signature`, and verifies `helpers.VerifySignature(apiSecret, timestamp, c.Body(), signature)`.
- Current request signature uses `timestamp + body` only. This story requires v1 mutating request signatures to bind `method + path + timestamp + body`.
- `helpers/credentials.go` currently has:
  - `GenerateSignature(secret, timestamp, body)` and `VerifySignature(secret, timestamp, body, received)` used by webhook delivery.
  - `ValidateTimestamp` with `TimeSkewSec = 30`.
  - `HMACSecret` for deterministic API secret lookup using `MASTER_KEY`.
- `services/webhook/notifier.go` uses `helpers.GenerateSignature` for webhook payload signing. Do not break webhook HMAC behavior while changing v1 request signing.
- `repositories/domain_repo.go` stores API secrets as `HMACSecret(apiSecretPlain)` and looks up active domains by API key or API secret.
- `models.Domain` has `MerchantID`, `DomainURL`, `APIKey`, `APISecret`, `WebhookURL`, and `WebhookSecret`. `APISecret` and `WebhookSecret` are `json:"-"`.

### Relevant Files

- Update likely:
  - `helpers/credentials.go`
  - `helpers/credentials_test.go`
  - `api/handlers/v1api.go`
  - `api/handlers/v1api_test.go`
- Read before modifying if touched:
  - `repositories/domain_repo.go`
  - `models/domain.go`
  - `api/routes/routes.go`
  - `services/webhook/notifier.go`
  - `services/webhook/notifier_test.go`

### Architecture Compliance

- Follow AD-10: new code should reason in tenant/domain scope even where current API names use merchant/domain.
- Follow AD-5: mutating money-affecting calls must be idempotent or replay-safe.
- Follow AD-12: auth and signer/webhook diagnostics must avoid leaking secrets in logs or responses.
- Keep the current monolith structure; do not introduce a new service or external dependency for replay protection in this story.

### Implementation Guardrails

- Do not replace `DomainRepo` lookup semantics unless tests prove current active merchant filtering is preserved.
- Do not log raw `X-API-Secret`, raw `X-Gateway-Signature`, request body, private keys, mnemonics, or full payloads.
- Do not change public webhook signature semantics in this story unless corresponding webhook tests and documentation are updated.
- Do not return different JSON envelope shapes for v1 errors.
- Prefer small helper functions over broad middleware refactors; many handlers call `v1ResolveDomain` and `v1ResolveSignedDomain` directly.
- Replay guard must not reject two different canonical requests that happen to use the same timestamp.
- Replay guard must reject the same canonical signed request repeated inside the allowed timestamp/skew window.

### Testing Requirements

- Existing test style uses Go `testing`, `httptest`, and Fiber app handlers.
- Add focused helper tests in `helpers/credentials_test.go` for canonical request signing.
- Add handler tests in `api/handlers/v1api_test.go` for:
  - API key auth success.
  - Bearer auth success.
  - Missing key.
  - Missing secret.
  - Mismatched key/secret.
  - Invalid timestamp.
  - Invalid signature for modified path/method/body.
  - Replay rejection.
  - Domain-scope isolation for an endpoint that loads a domain-owned resource.
- Run targeted tests before full suite. Full regression target: `go test ./...`; vet target: `go vet ./...`.

### References

- `_bmad-output/planning-artifacts/epics.md` - Story 1.1
- `_bmad-output/planning-artifacts/prds/prd-gateway-2026-06-27/prd.md` - FR-9, FR-10, FR-39, FR-40
- `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md` - AD-5, AD-10, AD-12
- `_bmad-output/planning-artifacts/ux-designs/ux-gateway-2026-06-27/EXPERIENCE.md` - diagnostics redaction and scope rules

## Project Structure Notes

- Current v1 API auth lives inside `api/handlers/v1api.go`, not middleware. Keep changes local unless a minimal helper improves reuse.
- `_bmad-output/project-context.md` exists and must be read before implementation.
- Recent git commits are generic `fixes`; do not infer story-specific implementation patterns from commit messages alone.

## Dev Agent Record

### Agent Model Used

Codex

### Debug Log References

- Red phase: `go test ./helpers ./api/handlers` failed before implementation because canonical request signing and replay guard symbols were missing.
- Targeted validation: `go test ./helpers ./api/handlers` passed after implementation.
- Full regression: `go test ./...` passed.
- Static check: `go vet ./...` passed.
- Review validation: `go test -count=1 ./helpers ./api/handlers` passed after code-review fixes.
- Review full regression: `go test -count=1 ./...` passed after code-review fixes.
- Review static check: `go vet ./...` passed after code-review fixes.

### Completion Notes List

- Added method/path/query/timestamp/body request signing helpers while preserving existing webhook `GenerateSignature` and `VerifySignature` behavior.
- Added v1 auth lookup seam for focused handler tests without changing repository lookup semantics.
- Added bounded in-memory signed request replay guard scoped by domain, method, path/query, timestamp, and signature with TTL cleanup.
- Integrated canonical request signature verification, generated correlation IDs, generic credential failures, and sanitized auth failure logging for v1 signed auth.
- Added tests for API key auth, bearer auth, canonical signature binding, replay rejection, modified path/query/method/body rejection, negative auth envelopes, redaction, and domain-scope hiding.
- Regenerated OpenAPI docs for the updated partner request signature contract.
- Dismissed the multi-instance replay-store note as out of scope for this story because the story explicitly requires a no-new-dependency guard suitable for the current monolith phase.

### File List

- `helpers/credentials.go`
- `helpers/credentials_test.go`
- `api/handlers/payment.go`
- `api/handlers/txrescan.go`
- `api/handlers/v1api.go`
- `api/handlers/v1_auth.go`
- `api/handlers/v1_auth_test.go`
- `docs/docs.go`
- `docs/swagger.json`
- `docs/swagger.yaml`
- `_bmad-output/implementation-artifacts/1-1-secure-partner-api-request-authentication.md`
- `_bmad-output/implementation-artifacts/1-1-secure-partner-api-request-authentication-validation.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

### Change Log

- 2026-06-27: Story created with canonical PRD, UX, architecture, epics, and current-code context.
- 2026-06-27: Story validation updated references to include generated project context.
- 2026-06-27: Implemented Story 1.1 auth hardening; status ready for review.
- 2026-06-27: Resolved fresh-context code-review findings and moved Story 1.1 to done.
