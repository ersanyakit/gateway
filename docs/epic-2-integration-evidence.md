# Epic 2 Integration Evidence

Epic 2 establishes reliable money event delivery for merchant and exchange integrations. This evidence ties the public contract to the implementation and tests.

## Contract Surface

- Event catalog: `docs/money-event-catalog.md` lists canonical dotted `v1` events, current compatibility aliases, required fields, idempotency fields, and migration notes.
- Outbox persistence: `docs/outbox-migration-plan.md` documents the GORM-managed `money_event_outboxes` schema, uniqueness rules, and migration verification.
- Delivery boundary: webhook delivery is handled from `webhook_deliveries` by the webhook boundary, not inline source money flows.
- Replay and dead-letter: failed deliveries keep sanitized diagnostics, failure category, attempt count, next operator action, replay lineage, and stable consumer idempotency metadata.
- Partner guide: `docs/integration-guide.md` documents HMAC verification, at-least-once delivery, replay behavior, and dedupe keys.

## Compatibility Rules

- Current underscore events such as `native_transfer`, `payment_succeeded`, `payment_failed`, `payment_expired`, and `transaction_reorged` remain supported compatibility aliases until a published migration retires them.
- Current `payout.*.v1` names remain supported compatibility aliases for withdrawal lifecycle events until a versioned withdrawal naming migration is published.
- Replay preserves the original event id, event type, event version, merchant/domain scope, and payload idempotency fields.
- Breaking `v1` payload field removals require a new event version or a documented migration note; compatibility snapshot tests fail on silent field removal.

## Validation

- `go test -count=1 ./services/webhook ./docs`
- `go test -count=1 ./docs ./constants ./services/database`
- `go test -count=1 ./...`
- `go vet ./...`
- `git diff --check && git diff --cached --check`

## Known Limits

This is event-delivery and partner-contract evidence. It does not claim full production custody readiness. External signer, reconciliation depth, compliance policy, observability SLOs/alerts, backup/restore drills, and controlled launch gates remain separate production-readiness work.
