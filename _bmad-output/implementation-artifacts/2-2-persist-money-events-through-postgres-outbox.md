---
story_id: "2.2"
story_key: "2-2-persist-money-events-through-postgres-outbox"
epic: "Epic 2: Reliable Money Event Delivery"
status: review
created: 2026-06-27
updated: 2026-06-27
baseline_commit: 3db844050e180146b55e4074815fd8e4b1dffc99
---

# Story 2.2: Persist Money Events Through Postgres Outbox

Status: review

## Story

Bir platform operator olarak,
money lifecycle event'lerinin state change ile ayni Postgres transaction icinde outbox'a yazilmasini istiyorum,
boylece process crash olsa bile event delivery kaybolmaz, replay edilebilir ve duplicate lifecycle retry'lari duplicate downstream obligation uretmez.

## Requirements Trace

- **FRs:** FR30, FR31
- **NFRs:** NFR2, NFR5, NFR12, NFR14
- **PRD:** `_bmad-output/planning-artifacts/prds/prd-gateway-2026-06-27/prd.md`
- **Architecture:** `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md`
- **Solution Design:** `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/SOLUTION-DESIGN.md`
- **Project Context:** `_bmad-output/project-context.md`

## Acceptance Criteria

1. Given a money-affecting state change occurs, when the owning boundary commits the state change, the corresponding versioned event is inserted into the Postgres outbox in the same database transaction and no in-memory dispatcher is the only record of the event.
2. Given an outbox event is inserted, when the event record is persisted, it stores stable event id, event type, event version, aggregate/resource id, tenant/domain scope, idempotency key, payload, status, attempt count, and created timestamp, and database uniqueness prevents duplicate records for the same idempotent lifecycle transition.
3. Given a transaction fails after state validation but before commit, when the operation is rolled back, neither the state change nor the outbox event is visible and rollback behavior is covered by tests.
4. Given an event has already been recorded for a lifecycle transition, when the same transition is retried, the system reuses or safely no-ops the existing event record and does not create duplicate downstream delivery obligations.
5. Given outbox schema or indexes are introduced, when the story is implemented, it includes versioned migration artifacts or an explicit migration plan and startup `AutoMigrate` is not the only production database change mechanism.
6. Given outbox persistence is implemented, when automated tests run, they cover same-transaction persistence, rollback, duplicate lifecycle retries, uniqueness constraints, and payload schema validation.

## Tasks / Subtasks

- [x] Task 1: Define durable outbox model and schema contract (AC: 2, 5, 6)
  - [x] Add a `MoneyEventOutbox` model/table for durable money lifecycle events; do not overload `webhook_deliveries` because delivery attempts are Story 2.3 boundary work.
  - [x] Persist fields required by AC2: `event_id`, `event_type`, `event_version`, `aggregate_type`, `aggregate_id`, `merchant_id`, `domain_id`, `idempotency_key`, `payload_json`, `status`, `attempts`, `created_at`, `updated_at`, and optional `locked_until`/`last_error` if needed for future workers.
  - [x] Add DB-level uniqueness for `event_id` and/or `idempotency_key` so duplicate lifecycle retries cannot create duplicate outbox obligations.
  - [x] Include the model in development `AutoMigrate` and `VerifySchema`, but also add an explicit production migration plan/artifact under `docs/` or another existing repo docs location.
  - [x] Keep payloads JSON text or `json.RawMessage` compatible with GORM/Postgres without adding new dependencies.

- [x] Task 2: Add repository boundary with transaction-aware insert/no-op semantics (AC: 1, 2, 3, 4, 6)
  - [x] Add `repositories.MoneyEventOutboxRepo` or equivalent near repository ownership patterns.
  - [x] Provide a transaction-aware method that can operate on caller-owned `*gorm.DB` transactions, plus a regular wrapper for standalone inserts.
  - [x] Validate required fields before insert: event id/type/version, aggregate type/id, idempotency key, tenant/domain scope where applicable, and valid JSON payload.
  - [x] On duplicate event/idempotency key, return the existing row and `created=false` instead of erroring when payload/event identity is compatible.
  - [x] If the duplicate idempotency key points to a different event id/type/payload, fail explicitly instead of silently merging incompatible events.

- [x] Task 3: Prove same-transaction commit/rollback behavior (AC: 1, 3, 4, 6)
  - [x] Add repository tests using the existing Postgres test patterns or a deterministic GORM test DB helper already present in the repo.
  - [x] Test committing a state row and outbox row in the same transaction makes both visible.
  - [x] Test returning an error from the transaction rolls back both the state row and the outbox row.
  - [x] Test retrying the same lifecycle idempotency key does not create duplicate outbox rows.
  - [x] Test incompatible duplicate idempotency key is rejected.

- [x] Task 4: Add first integration seam without changing delivery semantics (AC: 1, 4, 5, 6)
  - [x] Wire the outbox repository into the application route/dependency graph only as needed for tested persistence seams.
  - [x] Add a narrow helper for building outbox records from Story 2.1 catalog metadata or existing webhook payloads; keep live webhook delivery behavior unchanged until Story 2.3.
  - [x] Do not remove existing `WebhookDeliveryRepo.Enqueue*` calls yet; this story creates the durable substrate and tests same-transaction persistence.
  - [x] If touching payment/transaction state methods, keep repository-owned transactions intact and avoid direct handler-side table mutation.

- [x] Task 5: Validate and update story record (AC: 1, 2, 3, 4, 5, 6)
  - [x] Targeted validation: `go test -count=1 ./repositories ./models ./services/database`.
  - [x] Outbox/catalog validation: `go test -count=1 ./services/webhook ./constants` if catalog helpers are touched.
  - [x] Full validation: `go test -count=1 ./...`.
  - [x] Static validation: `go vet ./...`.
  - [x] Whitespace validation: `git diff --check`.
  - [x] Update Dev Agent Record, Completion Notes, File List, Change Log, and story status according to `bmad-dev-story`.

## Dev Notes

### Current Implementation Snapshot

- Existing durable delivery tracking is `models.WebhookDelivery` plus `repositories.WebhookDeliveryRepo`, but that table represents webhook delivery state and attempts. It should not be treated as the generic money event outbox for this story.
- `WebhookDeliveryRepo.enqueueByEventID` already uses `pg_advisory_xact_lock(hashtext("webhook-delivery:"+eventID))` and no-op semantics by `event_id`; this is useful precedent but not a replacement for outbox persistence in the source transaction.
- Existing payment and transaction webhook paths still enqueue delivery rows after state transitions. This story must not remove or rename those emitted event names.
- `services/database.Migrate` currently runs development `AutoMigrate`, but production `APP_ENV=production` disables it by default and calls `VerifySchema`. Any new outbox schema must be represented there and have explicit migration guidance.
- Story 2.1 added `services/webhook/event_catalog.go` and `docs/money-event-catalog.md`. Reuse catalog event names/version semantics instead of inventing another event registry.
- Existing DB uniqueness/idempotency patterns:
  - `models.IdempotencyKey` plus `repositories.IdempotencyRepo` for partner write request idempotency.
  - `models.LedgerEntry.IdempotencyKey` with DB uniqueness for account-scoped ledger idempotency.
  - `WebhookDeliveryRepo` event-id based enqueue no-op for delivery rows.

### Architecture Compliance

- AD-5: every money-affecting transition needs stable idempotency key and versioned event name. Outbox rows must enforce that invariant at DB level.
- AD-8: Webhook delivery remains a boundary. This story persists events; Story 2.3 owns delivery/retry/dead-letter worker behavior.
- AD-9: Postgres outbox is the first durable event substrate. Do not introduce Kafka, NATS, SQS, RabbitMQ, or another broker here.
- AD-10: architecture says tenant/domain; current code exposes merchant/domain. Model fields should use current `merchant_id` and `domain_id` while docs can mention tenant/domain scope.
- NFR12: production schema changes need a versioned migration artifact or explicit migration plan. Startup `AutoMigrate` alone is not enough.

### Implementation Guardrails

- Keep changes narrowly scoped to model/repository/database docs/tests unless a single integration seam is needed for AC1.
- Do not move webhook HTTP delivery into source modules.
- Do not change existing webhook HMAC signing, headers, retry behavior, event ids, or emitted legacy alias names.
- Do not claim outbox consumers are complete; Story 2.3 handles consuming and delivery from the outbox.
- Use `context.Context` and repository-owned transaction helpers; do not open background contexts inside request handlers.
- Store payloads as validated JSON and keep sensitive values out of examples/tests.
- If adding JSON payload comparison for duplicate conflict, compare canonical JSON or raw bytes consistently to avoid whitespace-only conflicts.

### Previous Story Intelligence

- Story 2.1 defined the canonical money event catalog and compatibility aliases. Reuse its event names and `v1` version instead of adding a parallel naming scheme.
- Story 2.1 explicitly did not change live event emission, webhook HMAC behavior, retry behavior, or outbox persistence.
- Story 1.5 contract tests guard docs and Swagger drift; update docs tests if integration guide or migration docs gain new contract claims.
- Current worktree also contains unrelated production-readiness and chain RPC changes. Do not revert them; keep Story 2.2 file list scoped.

### Testing Requirements

- Use standard Go tests and the repo's existing GORM/Postgres test patterns.
- Required tests:
  - insert validates required fields and JSON payload;
  - same transaction commit persists both a state row and outbox row;
  - same transaction rollback hides both;
  - duplicate event/idempotency key returns existing row/no-op;
  - incompatible duplicate idempotency key fails;
  - schema verification includes the outbox table;
  - migration plan/docs mention the outbox table and required unique indexes.
- Validation commands:
  - `go test -count=1 ./repositories ./models ./services/database`
  - `go test -count=1 ./services/webhook ./constants` if catalog helpers are touched
  - `go test -count=1 ./...`
  - `go vet ./...`
  - `git diff --check`

### Relevant Files

- Likely update/create:
  - `models/money_event_outbox.go`
  - `repositories/money_event_outbox_repo.go`
  - `repositories/money_event_outbox_repo_test.go`
  - `services/database/database.go`
  - `services/database/database_test.go`
  - `docs/outbox-migration-plan.md` or equivalent migration docs artifact
  - `_bmad-output/implementation-artifacts/sprint-status.yaml`
- Read before modifying:
  - `repositories/webhook_delivery_repo.go`
  - `models/webhook_delivery.go`
  - `services/webhook/event_catalog.go`
  - `services/webhook/notifier.go`
  - `services/webhook/lifecycle.go`
  - `repositories/payment_repo.go`
  - `repositories/transaction_repo.go`

## Project Structure Notes

- Outbox model belongs in `models/`; repository ownership belongs in `repositories/`.
- Database schema registration and schema verification live in `services/database/database.go`.
- Production migration guidance belongs under `docs/` unless a versioned migration directory already exists.
- No new service boundary or external broker should be introduced in this story.

## Dev Agent Record

### Agent Model Used

Codex

### Debug Log References

- Baseline commit: `3db844050e180146b55e4074815fd8e4b1dffc99`
- Validation: `go test -count=1 ./repositories ./models ./services/database ./docs ./services/webhook ./constants` passed.
- Validation: `go test -count=1 ./...` passed.
- Validation: `go vet ./...` passed.
- Validation: `git diff --check` passed.
- Note: Postgres transaction semantics test is present and runs when `OUTBOX_TEST_DATABASE_URL`, `MONEY_OUTBOX_TEST_DATABASE_URL`, or `TEST_DATABASE_URL` is set. Local validation environment did not provide a test DSN, so the integration test was skipped by Go's normal test skip path.

### Implementation Plan

- Add `models.MoneyEventOutbox` as the durable event substrate, separate from `webhook_deliveries`.
- Add `repositories.MoneyEventOutboxRepo` with caller-owned `*gorm.DB` support, canonical JSON payload validation, duplicate no-op semantics, and explicit conflict detection.
- Register outbox schema in development migration/schema verification and publish production DDL guidance under `docs/outbox-migration-plan.md`.
- Preserve existing live webhook delivery behavior for Story 2.3; this story creates persistence substrate and tests.

### Completion Notes List

- Added durable `money_event_outboxes` model with stable event id, scoped idempotency uniqueness, aggregate/resource metadata, tenant/domain scope, canonical payload JSON, status, attempts, timestamps, and future worker lock/error fields.
- Added outbox repository helpers that build records from Story 2.1 catalog event names, validate/canonicalize payload JSON, insert through caller-owned transactions, no-op compatible duplicates, and reject incompatible duplicate idempotency records.
- Added schema registration/verification evidence and `docs/outbox-migration-plan.md` production migration guidance so startup `AutoMigrate` is not the only production schema mechanism.
- Added unit/docs/schema tests plus optional Postgres integration coverage for commit, rollback, duplicate, and conflict semantics when a test DSN is available.

### File List

- `_bmad-output/implementation-artifacts/2-2-persist-money-events-through-postgres-outbox.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `docs/integration_contract_test.go`
- `docs/outbox-migration-plan.md`
- `models/money_event_outbox.go`
- `repositories/money_event_outbox_repo.go`
- `repositories/money_event_outbox_repo_test.go`
- `services/database/database.go`
- `services/database/database_test.go`
- `services/database/outbox_schema_contract_test.go`

### Change Log

- 2026-06-27: Story created with Epic 2.2 acceptance criteria, outbox architecture guardrails, Story 2.1 learnings, and migration/test guidance.
- 2026-06-27: Implemented durable money event outbox schema, repository boundary, migration plan, schema/docs tests, and validation evidence.
