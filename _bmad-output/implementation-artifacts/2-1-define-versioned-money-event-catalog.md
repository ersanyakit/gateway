---
story_id: "2.1"
story_key: "2-1-define-versioned-money-event-catalog"
epic: "Epic 2: Reliable Money Event Delivery"
status: review
created: 2026-06-27
updated: 2026-06-27
baseline_commit: 8449389195a3d4b185bb6b30c730ddb524a8b1f5
---

# Story 2.1: Define Versioned Money Event Catalog

Status: review

## Story

Bir developer integrator olarak,
tum money lifecycle event'lerinin dokumante edilmis versioned event catalog'a sahip olmasini istiyorum,
boylece merchant ve exchange consumer'lari payment, deposit, withdrawal, refund, sweep ve correction event'lerini tutarli sekilde isleyebilir.

## Requirements Trace

- **FRs:** FR27, FR28, FR29
- **NFRs:** NFR11, NFR14
- **PRD:** `_bmad-output/planning-artifacts/prds/prd-gateway-2026-06-27/prd.md`
- **Architecture:** `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md`
- **Solution Design:** `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/SOLUTION-DESIGN.md`
- **Project Context:** `_bmad-output/project-context.md`

## Acceptance Criteria

1. Given the system emits money lifecycle events, when the event catalog is defined, then it includes documented event names, event versions, payload fields, required identifiers, timestamps, tenant/domain scope, and lifecycle semantics, and covers payment, deposit, withdrawal, refund, sweep, webhook delivery, and correction/reorg event families.
2. Given new money event contracts are defined, when event names are introduced, then they use dotted, versioned names including `deposit.detected.v1`, `deposit.finalized.v1`, `payment.succeeded.v1`, `payment.failed.v1`, `payment.expired.v1`, `withdrawal.requested.v1`, `withdrawal.broadcast.v1`, `withdrawal.finalized.v1`, `withdrawal.failed.v1`, `refund.succeeded.v1`, `sweep.succeeded.v1`, and `transaction.reorged.v1`, and each event name has a clear producer, consumer, and terminal/non-terminal lifecycle meaning.
3. Given existing underscore-style webhook events are still used by current integrations, when the event catalog is published, then each supported legacy event is mapped to its dotted/versioned equivalent or explicitly marked as legacy-only, and no existing event name is removed without a deprecation note.
4. Given event payload schemas are defined, when a consumer receives an event, then every payload includes a stable event id, event type, event version, tenant/domain id where applicable, occurred-at timestamp, resource id, resource status, and idempotency/correlation metadata, and sensitive values such as API secrets, private keys, mnemonics, raw signatures, and internal-only diagnostics are excluded.
5. Given correction or reorg events are defined, when a previously emitted lifecycle state must be corrected, then the catalog explains how correction events relate to the original event id and resource state, and correction semantics do not require destructive edits to prior event history.
6. Given the event catalog is implemented or documented, when automated checks run, then they verify every configured event constant or emitted event type is present in the catalog, and catalog examples are valid against the declared payload schemas.

## Tasks / Subtasks

- [x] Task 1: Inventory current event surface and compatibility aliases (AC: 1, 2, 3, 6)
  - [x] Inventory constants in `constants/webhook_events.go`, raw event literals in repositories/handlers/workers, and payload builders in `services/webhook/*`.
  - [x] Identify current legacy underscore events: `native_transfer`, `transaction_reorged`, `payment_succeeded`, `payment_failed`, `payment_expired`.
  - [x] Identify current dotted events already emitted: `payout.*.v1`, `refund.*.v1`, `sweep.*.v1`.
  - [x] Decide catalog treatment for `payout.*.v1`: map it to canonical `withdrawal.*.v1` as a current compatibility alias, or explicitly document it as current implementation name with migration path to withdrawal naming. Do not remove or silently rename emitted `payout.*.v1` events in this story.
  - [x] Record event producer boundary and consumer type for each family: chain indexer/deposit, payment, withdrawal/payout, refund, sweep, webhook delivery, correction/reorg.

- [x] Task 2: Add a structured money event catalog source (AC: 1, 2, 3, 4, 5, 6)
  - [x] Add a structured catalog in a reusable package, preferably `services/webhook/event_catalog.go` or another local pattern near webhook contract code.
  - [x] Include event name, version, family, producer, consumer, terminal/non-terminal flag, resource type, lifecycle meaning, required common fields, family-specific fields, legacy aliases, and deprecation/migration notes.
  - [x] Include all canonical target names required by the epic: `deposit.detected.v1`, `deposit.finalized.v1`, `payment.succeeded.v1`, `payment.failed.v1`, `payment.expired.v1`, `withdrawal.requested.v1`, `withdrawal.broadcast.v1`, `withdrawal.finalized.v1`, `withdrawal.failed.v1`, `refund.succeeded.v1`, `sweep.succeeded.v1`, `transaction.reorged.v1`.
  - [x] Include currently emitted extra events if present, such as `refund.requested.v1`, `refund.broadcast.v1`, `refund.rejected.v1`, `refund.failed.v1`, `sweep.requested.v1`, `sweep.failed.v1`, `sweep.dead_lettered.v1`, and `payout.*.v1`, without claiming all are canonical target names.
  - [x] Define correction/reorg relation fields, including original event/resource reference and non-destructive correction semantics.

- [x] Task 3: Publish developer-facing catalog documentation (AC: 1, 2, 3, 4, 5)
  - [x] Add or update a docs artifact such as `docs/money-event-catalog.md`.
  - [x] Link the catalog from `docs/integration-guide.md` without replacing the current integration guide examples.
  - [x] Document common payload envelope fields: `event_id`, `event_type`, `event_version`, `occurred_at`, `merchant_id`, `domain_id`, `resource_type`, `resource_id`, `resource_status`, `idempotency_key`, and `correlation_id`.
  - [x] Document sensitive-field exclusions: API secrets, webhook secrets, raw signatures, private keys, mnemonics, full internal diagnostics, and unredacted stack traces.
  - [x] Document legacy alias compatibility and deprecation rules for underscore names and current `payout.*.v1` names.
  - [x] Document that webhook HMAC signing remains `timestamp + raw_body`; do not merge it with V1 request signing.

- [x] Task 4: Add catalog contract tests (AC: 2, 3, 4, 5, 6)
  - [x] Add tests that every constant in `constants/webhook_events.go` is represented in the catalog either as canonical or alias.
  - [x] Add tests that raw emitted event literals still present in payment/transaction/rescan paths are represented in the catalog.
  - [x] Add tests that required canonical event names exist with version `v1`, producer, consumer, lifecycle semantics, resource type, and terminal flag.
  - [x] Add tests that catalog examples validate against the declared required fields and exclude sensitive fields.
  - [x] Add tests that correction/reorg events include original-resource/event relation metadata and state that prior event history is not destructively edited.

- [x] Task 5: Validate and update story record (AC: 1, 2, 3, 4, 5, 6)
  - [x] Targeted validation: `go test -count=1 ./services/webhook ./constants`.
  - [x] Docs/catalog validation: run any new docs test package added by this story.
  - [x] Full validation: `go test -count=1 ./...`.
  - [x] Static validation: `go vet ./...`.
  - [x] Whitespace validation: `git diff --check`.
  - [x] Update Dev Agent Record, Completion Notes, File List, Change Log, and story status according to `bmad-dev-story`.

## Dev Notes

### Current Implementation Snapshot

- Webhook event constants live in `constants/webhook_events.go`. Current constants include legacy underscore events and dotted `payout`, `refund`, and `sweep` lifecycle names.
- Transaction webhook payloads are built in `services/webhook/notifier.go` as `Payload`; event ids come from `services/webhook/event_id.go`.
- Payment webhook payloads are built in `services/webhook/notifier.go` as `PaymentPayload`; payment event ids include the current `session.WebhookEvent`.
- Payout/refund/sweep lifecycle payloads are built in `services/webhook/lifecycle.go` as `LifecyclePayload`.
- Webhook delivery rows are created by `repositories/webhook_delivery_repo.go`; `EventVersion` defaults to `v1` and delivery uniqueness is currently based on `event_id`.
- Current payment code still writes legacy names such as `payment_succeeded`, `payment_failed`, and `payment_expired`. Current transaction code can emit `native_transfer` and `transaction_reorged`.
- Current public docs list webhook examples in `docs/integration-guide.md`; Story 1.5 added `docs/epic-1-integration-evidence.md` and docs contract tests.

### Architecture Compliance

- AD-5: every money-affecting transition needs a stable idempotency key and versioned event name; this story defines the names/contracts and tests catalog coverage.
- AD-8: source modules enqueue versioned webhook events, while Webhook owns delivery, signing, retry, replay, dead-letter, and diagnostics. This story must not move delivery into payment/deposit/withdrawal code.
- AD-9: Postgres outbox is the durable substrate, but actual outbox persistence is Story 2.2. Do not add the outbox table or transaction insertion in Story 2.1 unless it is a narrow non-invasive type placeholder required by tests.
- AD-10: architecture uses tenant/domain language; current code exposes merchant/domain. Catalog docs should explain scope with current `merchant_id` and `domain_id` fields without inventing a new tenant API.
- Compatibility convention: existing underscore webhook names remain compatibility aliases until an event catalog migration retires them. This story must not remove or silently rename current emitted event names.

### Implementation Guardrails

- Prefer a catalog metadata abstraction plus tests over behavior changes.
- Do not break existing webhook event ids. Existing event id functions include event type in the id; changing event type emission changes downstream idempotency.
- Do not change webhook HMAC signing, headers, or retry behavior in this story.
- Do not introduce Kafka/NATS/SQS/RabbitMQ or an external broker. External broker selection is deferred until Postgres outbox limits are measured.
- Do not claim all live payload producers already include newly required fields such as `occurred_at`, `idempotency_key`, or `correlation_id` unless implementation actually adds them and tests cover it.
- If adding fields to live payloads, keep them additive and backwards-compatible.
- Use structured JSON parsing for catalog example tests. Avoid brittle full-document matching except for required event names and alias notes.
- Keep docs examples free of API secrets, webhook secrets, private keys, mnemonics, raw signatures, and stack traces.

### Previous Story Intelligence

- Story 1.5 established partner contract drift tests across `docs/integration-guide.md`, `docs/swagger.json`, and `docs/epic-1-integration-evidence.md`.
- Story 1.5 review separated payment API statuses from checkout polling statuses. Preserve that clarity when adding event lifecycle docs.
- Story 1.5 reaffirmed that current code exposes `merchant/domain` while architecture names the ownership boundary `tenant/domain`; use both terms carefully.
- Story 1.5 kept webhook signing separate from V1 request signing. Continue that separation.

### Testing Requirements

- Use standard Go tests in the package owning the catalog, likely `services/webhook`.
- Add a docs-level test if `docs/money-event-catalog.md` is created.
- Tests should be deterministic and network-free.
- Minimum checks:
  - all event constants in `constants/webhook_events.go` are cataloged;
  - required canonical v1 names exist;
  - legacy aliases map to canonical or legacy-only status;
  - examples satisfy required common fields;
  - sensitive field names are absent from examples;
  - correction/reorg entries document original relation and non-destructive semantics.
- Validation commands:
  - `go test -count=1 ./services/webhook ./constants`
  - `go test -count=1 ./docs` if docs tests are added
  - `go test -count=1 ./...`
  - `go vet ./...`
  - `git diff --check`

### Relevant Files

- Likely update/create:
  - `services/webhook/event_catalog.go`
  - `services/webhook/event_catalog_test.go`
  - `docs/money-event-catalog.md`
  - `docs/integration-guide.md`
  - `docs/integration_contract_test.go` or a new docs catalog test
- Read before modifying:
  - `constants/webhook_events.go`
  - `services/webhook/event_id.go`
  - `services/webhook/notifier.go`
  - `services/webhook/lifecycle.go`
  - `repositories/webhook_delivery_repo.go`
  - `repositories/payment_repo.go`
  - `repositories/transaction_repo.go`
  - `services/txrescan/service.go`

## Project Structure Notes

- Partner-facing catalog docs belong under `docs/`; do not create a separate docs site.
- Catalog metadata belongs near webhook contract code unless implementation reveals a stronger existing local pattern.
- This story is a contract/catalog story, not an outbox/delivery worker story.

## Dev Agent Record

### Agent Model Used

Codex

### Debug Log References

- Baseline commit: `8449389195a3d4b185bb6b30c730ddb524a8b1f5`
- RED: `go test -count=1 ./services/webhook -run 'TestMoneyEventCatalog'` failed before the catalog API existed.
- RED: `go test -count=1 ./docs -run 'TestMoneyEventCatalogDocumentsVersionedEvents'` failed before `docs/money-event-catalog.md` existed.
- GREEN: `go test -count=1 ./services/webhook -run 'TestMoneyEventCatalog'` passed.
- GREEN: `go test -count=1 ./docs -run 'TestMoneyEventCatalogDocumentsVersionedEvents'` passed.
- Validation: `go test -count=1 ./services/webhook ./constants` passed.
- Validation: `go test -count=1 ./docs` passed.
- Validation: `go test -count=1 ./...` passed.
- Validation: `go vet ./...` passed.
- Validation: `git diff --check` passed.

### Implementation Plan

- Keep Story 2.1 as a contract/catalog change: add reusable metadata, docs, and tests without changing live event emission, event ids, webhook HMAC behavior, retry behavior, or outbox persistence.
- Treat current `payout.*.v1` events as compatibility aliases for canonical `withdrawal.*.v1` names, and keep legacy underscore events mapped until an explicit catalog migration retires them.
- Validate catalog drift with Go contract tests over constants, known raw literals, required canonical entries, example payload fields, alias migration notes, and correction semantics.

### Completion Notes List

- Added a structured money event catalog under `services/webhook` with canonical dotted `v1` event names, common fields, family fields, producer/consumer metadata, terminal flags, alias relations, deprecation notes, and correction semantics.
- Mapped current legacy underscore webhook events to canonical names and mapped current `payout.*.v1` events as compatibility aliases for canonical withdrawal events without changing emitted event names.
- Added developer-facing `docs/money-event-catalog.md` and linked it from `docs/integration-guide.md` while preserving existing webhook examples and signing semantics.
- Added catalog contract tests for required canonical events, current webhook constants, raw emitted event literals, required example fields, sensitive field exclusion, alias migration notes, and non-destructive correction semantics.
- Verified targeted catalog/docs tests, full repo tests, vet, and whitespace checks.

### File List

- `_bmad-output/implementation-artifacts/2-1-define-versioned-money-event-catalog.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `docs/integration-guide.md`
- `docs/integration_contract_test.go`
- `docs/money-event-catalog.md`
- `services/webhook/event_catalog.go`
- `services/webhook/event_catalog_test.go`

### Change Log

- 2026-06-27: Story created with Epic 2.1 acceptance criteria, event-surface inventory, architecture guardrails, Story 1.5 learnings, and catalog/testing guidance.
- 2026-06-27: Implemented versioned money event catalog metadata, developer documentation, compatibility alias mapping, and catalog contract tests.
