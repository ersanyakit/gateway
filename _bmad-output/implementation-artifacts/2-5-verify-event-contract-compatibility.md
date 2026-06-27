---
story_id: "2.5"
story_key: "2-5-verify-event-contract-compatibility"
epic: "Epic 2: Reliable Money Event Delivery"
status: in-progress
created: 2026-06-27
updated: 2026-06-27
baseline_commit: 92e6df6b5e0a1e1f079f1c6d6c773a934432c6ed
---

# Story 2.5: Verify Event Contract Compatibility

Status: in-progress

## Story

Bir developer integrator olarak,
event schema ve compatibility alias'lar test-backed olsun istiyorum,
boylece consumer'lar mevcut webhook isimlerinden versioned money event'lere kirilmadan gecis yapabilir.

## Requirements Trace

- **FRs:** FR27, FR28, FR29, FR40
- **NFRs:** NFR11, NFR14
- **PRD:** `_bmad-output/planning-artifacts/prds/prd-gateway-2026-06-27/prd.md`
- **Architecture:** `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md`
- **Previous Stories:** `_bmad-output/implementation-artifacts/2-1-define-versioned-money-event-catalog.md`, `_bmad-output/implementation-artifacts/2-2-persist-money-events-through-postgres-outbox.md`, `_bmad-output/implementation-artifacts/2-3-deliver-webhooks-from-the-webhook-boundary.md`, `_bmad-output/implementation-artifacts/2-4-support-replay-dead-letter-and-duplicate-delivery-safety.md`

## Acceptance Criteria

1. Given current underscore-style events exist, when the versioned event contract is introduced, then compatibility aliases continue to emit or translate supported legacy names and each alias has a documented migration path to its dotted/versioned event.
2. Given event payload schemas are documented, when schema examples are generated or checked, then examples validate against the declared schema and required fields are present for each event family.
3. Given a breaking event payload change is proposed, when compatibility tests run, then tests fail unless a new event version or documented migration note is provided and existing `v1` consumers are not silently broken.
4. Given event contract tests run, when handlers, outbox producers, or webhook payload builders change, then tests verify event type, event version, payload shape, legacy aliases, and sensitive field exclusion, and failures point to the mismatched event contract.
5. Given Epic 2 is complete, when a developer reviews integration evidence, then they can see the event catalog, outbox persistence rules, delivery/replay semantics, dead-letter behavior, and compatibility test coverage.

## Tasks / Subtasks

- [ ] Task 1: Strengthen catalog alias and schema contract tests (AC: 1, 2, 3, 4)
  - [ ] Add tests that every catalog entry example includes every declared required field.
  - [ ] Add tests that alias entries include relation, note, deprecation/migration path, and map to a canonical dotted/versioned event.
  - [ ] Add a v1 required-field snapshot test for public canonical events so accidental field removal fails CI.
  - [ ] Keep sensitive field exclusion tests for examples and docs.

- [ ] Task 2: Verify emitted events and payload builders stay compatible (AC: 1, 2, 4)
  - [ ] Ensure current constants and raw literals still resolve through `MoneyEventCatalogEntryForEmittedEvent`.
  - [ ] Ensure notifier/request tests assert event id/type/version headers and payload event metadata.
  - [ ] Ensure outbox/docs tests keep event id/type/version/idempotency semantics visible.

- [ ] Task 3: Produce Epic 2 integration evidence (AC: 5)
  - [ ] Add a concise docs artifact linking event catalog, outbox persistence, boundary delivery, replay/dead-letter, and duplicate-safety behavior.
  - [ ] Include validation commands and known limitations without claiming full production custody readiness.
  - [ ] Update docs contract tests so missing evidence breaks CI.

- [ ] Task 4: Validate and update story record (AC: 1, 2, 3, 4, 5)
  - [ ] Targeted validation: `go test -count=1 ./services/webhook ./docs`.
  - [ ] Full validation: `go test -count=1 ./...`.
  - [ ] Static validation: `go vet ./...`.
  - [ ] Whitespace validation: `git diff --check && git diff --cached --check`.
  - [ ] Update Dev Agent Record, Completion Notes, File List, Change Log, and story status.

## Dev Notes

- Story 2.1 introduced `services/webhook/event_catalog.go` and catalog tests.
- Story 2.2 introduced `MoneyEventOutbox` and outbox migration evidence.
- Story 2.3 introduced delivery boundary tests and signed webhook delivery validation.
- Story 2.4 introduced replay/dead-letter duplicate-safety docs and diagnostics.
- This story should avoid runtime behavior changes unless a compatibility gap is found.

## Dev Agent Record

### Agent Model Used

Codex

### Debug Log References

- Not started.

### Completion Notes List

- Not started.

### File List

- `_bmad-output/implementation-artifacts/2-5-verify-event-contract-compatibility.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

### Change Log

- 2026-06-27: Story created from Epic 2.5 acceptance criteria and Epic 2 continuity notes.
