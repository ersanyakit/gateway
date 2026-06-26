---
name: implementation-guardrails
description: Apply architecture constraints while implementing or reviewing Gateway code changes.
---

# Implementation Guardrails

The outcome is code work that advances the current phase without breaking the architecture spine.

Before code changes:

- Identify the active phase and owner boundary.
- Read the relevant existing files.
- Name the ADs the change must preserve.
- Keep edits scoped to the boundary unless a cross-boundary contract is the actual work.

Guardrails:

- AD-3: Balance reads come from Ledger-derived data only.
- AD-4: Chain Indexer emits facts; it does not post ledger entries or mark payments paid directly.
- AD-5: Money lifecycle transitions require stable idempotency keys and versioned events.
- AD-6: Production custody cannot rely on app-visible mnemonic/private key material.
- AD-7: Withdrawal/sweep/refund broadcast requires ledger hold plus nonce/UTXO/resource reservation.
- AD-8: Source modules enqueue webhook events; Webhook owns delivery.
- AD-9: Durable events start with Postgres outbox inside the monolith.
- AD-11: Uncertain money state opens reconciliation instead of blind retry.
- AD-12: Production-impacting work needs migration, observability, and support evidence.

Verification should scale with risk. For money movement, prefer unit tests for invariants plus integration-style tests for transaction boundaries and retries. If tests cannot run, report that explicitly and state residual risk.
