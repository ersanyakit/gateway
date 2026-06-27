---
story_id: "2.3"
story_key: "2-3-deliver-webhooks-from-the-webhook-boundary"
epic: "Epic 2: Reliable Money Event Delivery"
status: done
created: 2026-06-27
updated: 2026-06-27
baseline_commit: 92e6df6b5e0a1e1f079f1c6d6c773a934432c6ed
---

# Story 2.3: Deliver Webhooks from the Webhook Boundary

Status: done

## Story

Bir merchant veya exchange integrator olarak,
webhook delivery'nin dedicated boundary tarafindan signing ve retry ile yonetilmesini istiyorum,
boylece money flow'lar external callback'lerde bloklanmaz ve consumer'lar dogrulanabilir notification alir.

## Requirements Trace

- **FRs:** FR26, FR31
- **NFRs:** NFR5, NFR11, NFR13, NFR15
- **PRD:** `_bmad-output/planning-artifacts/prds/prd-gateway-2026-06-27/prd.md`
- **Architecture:** `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md`
- **Solution Design:** `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/SOLUTION-DESIGN.md`
- **Project Context:** `_bmad-output/project-context.md`

## Acceptance Criteria

1. Given an outbox or webhook delivery event requires partner notification, when the webhook boundary claims the event for delivery, then source payment, deposit, withdrawal, refund, and sweep flows are not responsible for inline HTTP delivery, and the event remains replayable if delivery fails.
2. Given a webhook attempt is sent, when the outbound request is built, then it includes HMAC signature, event id, event type, event version, timestamp, and tenant/domain-scoped delivery metadata, and callback URL validation runs at delivery time.
3. Given a callback endpoint returns a transient failure or timeout, when the webhook boundary records the attempt, then it increments attempt count, stores failure category, schedules exponential backoff, and keeps the event eligible for retry until max attempts, and retry scheduling is covered by tests.
4. Given a callback endpoint returns a terminal success response, when the webhook boundary records the attempt, then the delivery is marked delivered with final attempt metadata, and the event is not retried again unless an explicit operator replay is requested.
5. Given a webhook payload is generated, when it is logged or displayed to operators, then secrets, private keys, mnemonics, raw signatures, and internal-only diagnostic payloads are redacted or excluded, and logs include correlation id, tenant/domain, event id, and failure category.
6. Given webhook delivery is implemented, when automated tests run, then they cover successful delivery, transient failure, timeout, backoff scheduling, callback URL validation, HMAC headers, and redaction.

## Tasks / Subtasks

- [x] Task 1: Define the webhook boundary claim/delivery service (AC: 1, 3, 4, 6)
  - [x] Reuse the existing `WebhookDeliveryRepo`/`models.WebhookDelivery` delivery queue for per-attempt diagnostics; do not create a parallel delivery table.
  - [x] Add a boundary service or worker under `services/webhook` or a clearly named worker package that claims due delivery rows, sends HTTP via the notifier, and records success/failure without source-flow inline delivery.
  - [x] Add repository claim semantics for due `pending`/`failed` delivery rows using transaction-safe locking (`FOR UPDATE SKIP LOCKED`, advisory lock, or equivalent) so multiple workers cannot deliver the same row concurrently.
  - [x] Include all delivery families that have existing queue rows: transaction, payment, and lifecycle `PayloadJSON` rows.
  - [x] If `MoneyEventOutbox` rows are bridged in this story, convert them idempotently into `WebhookDelivery` rows before HTTP delivery; do not bypass delivery diagnostics.

- [x] Task 2: Remove inline HTTP delivery from source money flows (AC: 1, 4)
  - [x] Replace direct `Notifier.Deliver*` calls in source state-change helpers with enqueue-only behavior where the source flow currently sends inline.
  - [x] Known inline call sites to inspect: `main.go` `handleDepositWebhook`, `handlePaymentDeposit`, `retryPendingWebhooks` transaction/payment loops; `api/handlers/payment.go` `deliverPaymentWebhook`; `api/handlers/dealer.go` `deliverAdminTransactionWebhook` and `deliverAdminPaymentWebhookIfMatched`.
  - [x] Keep `WebhookDeliveryRepo.EnqueueTransaction`, `EnqueuePayment`, and `EnqueueLifecycle` idempotent and backward-compatible.
  - [x] Do not remove explicit admin replay behavior in this story; Story 2.4 owns replay/dead-letter operator actions.

- [x] Task 3: Preserve HMAC signing and delivery-time callback validation (AC: 2, 5, 6)
  - [x] Reuse `services/webhook.Notifier` and `helpers.GenerateSignature(secret, timestamp, raw_body)`; do not merge this with V1 request signing.
  - [x] Ensure outbound headers include `X-Gateway-Event`, `X-Gateway-Event-Version`, `X-Gateway-Event-Id`, `X-Gateway-Timestamp`, and `X-Gateway-Signature`.
  - [x] Validate callback URL at delivery time with existing webhook URL validation before sending HTTP.
  - [x] Ensure missing webhook URL/secret and invalid callback URL are classified as permanent delivery failures, not infinite transient retries.

- [x] Task 4: Implement retry/backoff and final attempt state (AC: 3, 4, 6)
  - [x] Use existing `WebhookDeliveryRepo.MarkAttempt` semantics or extend them narrowly to store failure category, next retry time/backoff, terminal delivered status, and dead-letter status after max attempts.
  - [x] Preserve `WEBHOOK_MAX_ATTEMPTS` and backoff helpers already present in `repositories/webhook_delivery_repo.go`.
  - [x] Ensure succeeded deliveries are excluded from future due-claim queries.
  - [x] Ensure failed deliveries are due only when `next_retry_at` is nil or elapsed.
  - [x] Keep Story 2.4 replay/dead-letter controls out of scope except for terminal status needed by retry exhaustion.

- [x] Task 5: Add redaction and operator-safe diagnostics (AC: 5, 6)
  - [x] Logs may include event id, event type, event version, merchant/domain id, delivery id, failure category, and correlation/request id.
  - [x] Logs and operator-facing delivery views must not include webhook secret, API secret, private key, mnemonic, raw signature, unbounded response body, or full sensitive payload.
  - [x] Limit persisted/visible callback response excerpts to a bounded sanitized string if response bodies are stored or logged.
  - [x] Keep existing admin/dealer webhook delivery list useful; do not hide failure reason behind hover-only UI if touched.

- [x] Task 6: Validate and update story record (AC: 1, 2, 3, 4, 5, 6)
  - [x] Targeted validation: `go test -count=1 ./services/webhook ./repositories`.
  - [x] Handler/worker validation if touched: `go test -count=1 ./api/handlers ./workers/...`.
  - [x] Contract validation: `go test -count=1 ./docs ./constants`.
  - [x] Full validation: `go test -count=1 ./...`.
  - [x] Static validation: `go vet ./...`.
  - [x] Whitespace validation: `git diff --check`.
  - [x] Update Dev Agent Record, Completion Notes, File List, Change Log, and story status according to `bmad-dev-story`.

## Dev Notes

### Current Implementation Snapshot

- `models.WebhookDelivery` already stores merchant/domain scope, event id/type/version, entity ids, payload JSON, target URL, status, attempts, last error, next retry time, delivered time, and timestamps.
- `repositories.WebhookDeliveryRepo` already has enqueue no-op semantics by event id, `MarkAttempt`, `ListDueLifecycle`, retry backoff helpers, and max-attempt configuration.
- `services/webhook.Notifier` already signs transaction, payment, and raw lifecycle payloads with webhook HMAC headers and validates callback URL at delivery time.
- `main.go` has `startWebhookRetryWorker` and `retryPendingWebhooks`, but parts of the worker still scan source payment/transaction pending flags and directly call `Notifier.Deliver*`.
- Several source state-change helpers still send inline HTTP after enqueueing delivery rows. This story should move those sends behind the boundary worker.
- `api/handlers/v1api.go` payout/refund creation currently enqueues lifecycle delivery rows without inline HTTP; preserve that pattern.

### Architecture Compliance

- AD-8: Webhook delivery is a boundary, not an inline side effect. Source modules enqueue versioned events; webhook owns URL validation, HMAC signing, retry/backoff, dead-letter state, replay, attempt logs, and diagnostics.
- AD-9: Postgres outbox/durable queue is the first event substrate. Do not introduce Kafka, NATS, SQS, RabbitMQ, or another broker.
- AD-5: public event ids and versioned event names are part of the partner contract. Do not rename existing legacy aliases without an event catalog migration.
- Webhook HMAC uses `timestamp + raw_body`; V1 API request signing uses a different canonical method/path/timestamp/body scheme. Keep them separate.

### Implementation Guardrails

- Do not create a second webhook sender path. Consolidate on one boundary service/worker and keep notifier as the HTTP/signing primitive.
- Do not mark source payment/transaction rows delivered before the boundary records a successful HTTP attempt.
- Do not turn source money state transitions into fire-and-forget goroutines for delivery; durable queue rows must be the record.
- Do not log decrypted webhook secrets, generated signatures, API credentials, private keys, mnemonics, or full payload blobs.
- If a new due-claim method is added, make it deterministic and concurrency-safe; tests should prove two claims cannot receive the same row.
- If schema changes become necessary, update migration docs/tests; otherwise prefer existing `webhook_deliveries` fields.

### Previous Story Intelligence

- Story 2.2 added `models.MoneyEventOutbox`, `repositories.MoneyEventOutboxRepo`, outbox schema verification, and `docs/outbox-migration-plan.md`.
- Story 2.2 review tightened payload JSON validation to require object payloads.
- Story 2.2 deliberately preserved existing live webhook delivery behavior; this story owns the boundary delivery behavior change.
- Story 2.1 defined event catalog names and legacy/current alias mapping. Use existing event ids and event types; do not invent a parallel naming scheme.
- Local Story 2.2 validation did not have a Postgres DSN, so any new DB-lock/claim behavior should have deterministic unit tests and optional Postgres integration tests when `OUTBOX_TEST_DATABASE_URL`, `MONEY_OUTBOX_TEST_DATABASE_URL`, or `TEST_DATABASE_URL` is set.

### Testing Requirements

- Unit-test notifier/request building for headers, HMAC shape, event id/type/version, raw body signing, and callback URL validation.
- Unit-test boundary worker/service with fake due rows and fake notifier/client for success, transient failure, timeout, permanent failure, max attempts, and backoff scheduling.
- Repository tests should cover due-claim filtering and concurrency-safe claim semantics. If real Postgres locking is required, guard integration tests behind a test DSN and keep non-DB unit coverage.
- Add regression tests that source-flow helpers enqueue but do not inline-call `Notifier.Deliver*` where direct delivery is removed.
- Add redaction tests for logs/diagnostics if new diagnostic formatting is introduced.

### Relevant Files

- Likely update/create:
  - `repositories/webhook_delivery_repo.go`
  - `repositories/webhook_retry_test.go`
  - `services/webhook/notifier.go`
  - `services/webhook/notifier_test.go`
  - `services/webhook/*delivery*.go` or `workers/*webhook*.go`
  - `main.go`
  - `api/handlers/payment.go`
  - `api/handlers/payment_test.go`
  - `api/handlers/dealer.go`
  - `api/handlers/dealer_test.go`
- Read before modifying:
  - `models/webhook_delivery.go`
  - `repositories/money_event_outbox_repo.go`
  - `services/webhook/lifecycle.go`
  - `services/webhook/event_id.go`
  - `helpers/credentials.go`
  - `helpers/webhook_url.go` or the file containing `ValidateWebhookURL`

## Project Structure Notes

- Webhook delivery boundary code belongs in `services/webhook` or a focused worker package; repository ownership remains in `repositories`.
- Do not introduce an external broker or a new service process for this story.
- Keep admin replay UI/API changes minimal; Story 2.4 owns replay and duplicate delivery safety.
- Keep partner-facing docs stable unless public headers, retry semantics, or delivery diagnostics change.

## Dev Agent Record

### Agent Model Used

Codex

### Debug Log References

- `go test -count=1 ./services/webhook ./repositories ./api/handlers`
- `go test -count=1 ./api/handlers ./workers/...`
- `go test -count=1 ./docs ./constants ./services/database`
- `rg -n "notifier\\.Deliver\\(|notifier\\.DeliverPayment\\(" main.go api/handlers/payment.go api/handlers/dealer.go`
- `go test -count=1 ./...`
- `go vet ./...`
- `git diff --check`
- Code review patch: fixed `DeliveryProcessor` to support the production `WebhookDeliveryRepo.ClaimDue(ctx, limit, lockFor)` signature and added nil guards around retry worker repository dependencies.
- Post-review validation: `go test -count=1 ./services/webhook ./repositories ./api/handlers` passed.
- Post-review validation: `go test -count=1 ./workers/... ./docs ./constants` passed.
- Post-review validation: `go test -count=1 ./...` passed.
- Post-review validation: `go vet ./...` passed.
- Post-review validation: `git diff --check` passed.

### Completion Notes List

- Implemented a webhook delivery boundary that claims due `webhook_deliveries` rows and sends transaction, payment, or lifecycle payloads through the existing signed notifier.
- Added transaction-safe due-claim leasing with `FOR UPDATE SKIP LOCKED`, retry eligibility filtering, failure category persistence, sanitized last-error storage, and max-attempt terminal handling.
- Converted source money flows and admin test-deposit helpers to enqueue-only delivery behavior; explicit admin replay remains supported through the boundary path.
- Added diagnostics redaction helpers and regression coverage for success, transient failure, timeout/permanent classification, backoff/claim behavior, callback validation/signing preservation, and no-inline source delivery.
- Resolved code review finding by adapting the boundary processor to three-argument claim repositories and guarding nil retry-worker dependencies before constructing lookup callbacks.
- Review fallback found and fixed a worker adapter mismatch so `DeliveryProcessor` supports the repository's claim-lock `ClaimDue(ctx, limit, lockFor)` signature.

### File List

- `_bmad-output/implementation-artifacts/2-3-deliver-webhooks-from-the-webhook-boundary.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `api/handlers/dealer.go`
- `api/handlers/payment.go`
- `api/handlers/payment_test.go`
- `main.go`
- `models/webhook_delivery.go`
- `repositories/payment_repo.go`
- `repositories/transaction_repo.go`
- `repositories/webhook_delivery_repo.go`
- `repositories/webhook_delivery_repo_test.go`
- `services/database/database.go`
- `services/database/outbox_schema_contract_test.go`
- `services/webhook/delivery_processor.go`
- `services/webhook/delivery_processor_test.go`
- `services/webhook/diagnostics.go`
- `services/webhook/diagnostics_test.go`
- `services/webhook/notifier.go`
- `services/webhook/notifier_test.go`
- `services/webhook/source_flow_contract_test.go`
- `services/webhook/webhook_delivery_boundary.go`

### Change Log

- 2026-06-27: Story created with Epic 2.3 acceptance criteria, webhook boundary guardrails, Story 2.1/2.2 learnings, and delivery/retry/redaction testing guidance.
- 2026-06-27: Implemented webhook boundary delivery, due-claim leasing, enqueue-only source flows, sanitized diagnostics, validation tests, and moved story to review.
- 2026-06-27: Addressed code review finding for production claim adapter compatibility and marked story done.
- 2026-06-27: Fixed review-found `DeliveryProcessor` claim adapter mismatch and reran targeted/full validation.
