---
story_id: "2.4"
story_key: "2-4-support-replay-dead-letter-and-duplicate-delivery-safety"
epic: "Epic 2: Reliable Money Event Delivery"
status: review
created: 2026-06-27
updated: 2026-06-27
baseline_commit: 92e6df6b5e0a1e1f079f1c6d6c773a934432c6ed
---

# Story 2.4: Support Replay, Dead-Letter, and Duplicate Delivery Safety

Status: review

## Story

Bir operator olarak,
basarisiz veya belirsiz webhook delivery'leri replay edilebilir ve duplicate-safe olsun istiyorum,
boylece partner notification'lari duplicate fulfillment veya sessiz veri kaybi olmadan kurtarilabilir.

## Requirements Trace

- **FRs:** FR26, FR31
- **NFRs:** NFR2, NFR5, NFR13, NFR15
- **PRD:** `_bmad-output/planning-artifacts/prds/prd-gateway-2026-06-27/prd.md`
- **Architecture:** `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md`
- **UX:** `_bmad-output/planning-artifacts/ux-designs/ux-gateway-2026-06-27/EXPERIENCE.md`
- **Previous Story:** `_bmad-output/implementation-artifacts/2-3-deliver-webhooks-from-the-webhook-boundary.md`

## Acceptance Criteria

1. Given a webhook delivery reaches maximum attempts without success, when retry policy is exhausted, then the delivery is marked dead-letter or equivalent terminal failure and the reason, last error, attempt count, and next operator action are visible.
2. Given an operator replays a webhook delivery, when replay is requested for an event, then the system creates a replay attempt tied to the original event id and the payload remains idempotent for consumers by preserving stable event id and event type metadata.
3. Given a consumer receives the same event more than once, when duplicate delivery occurs through retry or replay, then the event contract provides enough idempotency metadata for consumers to deduplicate and duplicate delivery behavior is documented and tested.
4. Given a replay is requested for a resource outside the operator's tenant or permission scope, when authorization is evaluated, then the replay is rejected without leaking resource existence and the denial is audit logged.
5. Given replay and dead-letter features are implemented, when automated tests run, then they cover retry exhaustion, replay success, duplicate replay attempts, authorization failure, and operator-visible delivery state.

## Tasks / Subtasks

- [x] Task 1: Add duplicate-safe replay attempt semantics (AC: 2, 3, 5)
  - [x] Extend `models.WebhookDelivery` narrowly to track replay lineage, replay count, and operator action metadata without creating a second delivery system.
  - [x] Add repository helper(s) that create a replay delivery row tied to the original delivery while preserving original event id, event type, event version, entity references, and payload.
  - [x] Ensure duplicate replay requests for the same original event while a replay is already pending/processing no-op to the existing replay row instead of creating unbounded duplicates.
  - [x] Keep consumer-facing idempotency metadata stable; do not generate a new event id for replay.

- [x] Task 2: Harden dead-letter visibility and next operator action (AC: 1, 5)
  - [x] Ensure max-attempt exhaustion sets `dead_letter`, `failure_category`, sanitized `last_error`, final attempt count, and no future retry time.
  - [x] Add operator-facing view fields for failure category, next retry/dead-letter action, original event id, and replay lineage.
  - [x] Keep diagnostics redacted and bounded; no webhook secret, API secret, private key, mnemonic, raw signature, or full payload display.

- [x] Task 3: Scope and audit replay requests (AC: 4, 5)
  - [x] Reject invalid, missing, cross-scope, or unauthorized replay requests with the same generic not-found style message where tenant scope is unknown.
  - [x] Audit both replay success and replay denial with actor, action, delivery id, outcome, and safe reason.
  - [x] Preserve existing admin replay path but route it through the duplicate-safe replay helper and webhook boundary.

- [x] Task 4: Document duplicate delivery contract (AC: 3)
  - [x] Update partner/integration evidence or webhook docs to state that webhook delivery is at-least-once.
  - [x] Document consumer dedupe keys: `X-Gateway-Event-Id`, event id in payload, event type, event version, and domain/merchant scope.
  - [x] Document replay behavior: replay preserves event id/type/version and may deliver the same event again.

- [x] Task 5: Validate and update story record (AC: 1, 2, 3, 4, 5)
  - [x] Targeted validation: `go test -count=1 ./services/webhook ./repositories ./api/handlers`.
  - [x] Contract/docs validation: `go test -count=1 ./docs ./constants`.
  - [x] Full validation: `go test -count=1 ./...`.
  - [x] Static validation: `go vet ./...`.
  - [x] Whitespace validation: `git diff --check && git diff --cached --check`.
  - [x] Update Dev Agent Record, Completion Notes, File List, Change Log, and story status.

## Dev Notes

### Current Implementation Snapshot

- Story 2.3 added `DeliveryBoundary`, `DeliveryProcessor`, `WebhookDeliveryRepo.ClaimDue`, failure category storage, source-flow enqueue-only behavior, and admin replay through the boundary.
- `models.WebhookDelivery` already has stable `EventID`, `EventType`, `EventVersion`, merchant/domain scope, source entity refs, `PayloadJSON`, `TargetURL`, `Status`, `Attempts`, `LastError`, `FailureCategory`, `NextRetryAt`, and `DeliveredAt`.
- `WebhookDeliveryRepo.MarkAttempt` already marks retry exhaustion as `dead_letter`.
- Admin webhooks page already lists delivery rows, filters `dead_letter`, and has a replay action for non-succeeded rows.

### Guardrails

- Do not introduce a new broker or parallel webhook delivery table.
- Do not mutate the original event id for replay; consumer dedupe depends on stability.
- Do not turn replay into source-flow inline delivery; it remains a webhook boundary action.
- Do not leak cross-tenant existence in replay denial.
- Do not display unredacted payloads, callback response bodies, generated signatures, or decrypted secrets.

## Dev Agent Record

### Agent Model Used

Codex

### Debug Log References

- `go test -count=1 ./services/webhook ./repositories ./api/handlers`
- `go test -count=1 ./docs ./constants ./services/database`
- `go test -count=1 ./api/handlers ./workers/...`
- `go test -count=1 ./...`
- `go vet ./...`
- `git diff --check && git diff --cached --check`

### Completion Notes List

- Added replay lineage and operator-action metadata to webhook deliveries without adding a second delivery table.
- Added duplicate-safe replay enqueue semantics that preserve original event id/type/version/payload metadata and no-op active replay duplicates.
- Hardened dead-letter/retry operator visibility in the admin webhooks panel with bounded diagnostics.
- Routed admin replay through the replay repository helper and webhook boundary, with generic denial messaging and audit logging.
- Documented at-least-once delivery, replay behavior, and consumer dedupe keys in partner integration docs.

### File List

- `_bmad-output/implementation-artifacts/2-4-support-replay-dead-letter-and-duplicate-delivery-safety.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `api/handlers/dealer.go`
- `api/handlers/dealer_test.go`
- `docs/integration-guide.md`
- `docs/integration_contract_test.go`
- `docs/outbox-migration-plan.md`
- `models/webhook_delivery.go`
- `repositories/webhook_delivery_repo.go`
- `repositories/webhook_delivery_repo_test.go`
- `services/database/database.go`
- `services/database/outbox_schema_contract_test.go`
- `views/dealer/admin_dashboard.html`

### Change Log

- 2026-06-27: Story created from Epic 2.4 with replay/dead-letter/duplicate-safety scope and Story 2.3 continuity notes.
- 2026-06-27: Implemented duplicate-safe replay, dead-letter diagnostics, admin replay audit path, docs, and validation coverage; moved to review.
