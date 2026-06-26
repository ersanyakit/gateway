---
name: epic-story-slicing
description: Slice Gateway phases into epics and stories with architecture-safe acceptance criteria.
---

# Epic & Story Slicing

The outcome is implementation-ready epics and stories for the next Gateway stage. A story is ready only when a developer can implement it without choosing a new architecture.

Every story must include:

- User/system outcome
- Owner boundary: Wallet, Chain Indexer, Deposit, Ledger, Payment, Withdrawal/Sweep, Signer, Webhook, Reconciliation, Tenant/Admin
- Architecture ADs enforced
- Files or modules likely touched
- Acceptance criteria
- Required tests
- Migration/operational evidence when money state changes

Money-safety defaults:

- Ledger changes need idempotency and invariant tests.
- Outbound money changes need hold/reservation and retry/reconciliation acceptance criteria.
- Webhook changes need event id, version, HMAC, retry, dead-letter, and replay behavior.
- Chain indexer changes need finality, reorg, backfill, and provider failure coverage.
- Signer changes need key-reference-only signing and audit evidence.

Do not create a story that lets handlers mutate ledger, deliver webhooks inline, or sign transactions before reservation. Split those into boundary-correct stories.
