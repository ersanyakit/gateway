---
name: phase-roadmap
description: Produce or update the Gateway remaining-stage roadmap from the architecture spine.
---

# Phase Roadmap

The outcome is a sequenced delivery roadmap that a technical team can execute without re-reading the whole architecture conversation.

Ground it in:

- `ARCHITECTURE-SPINE.md`
- `SOLUTION-DESIGN.md`
- `ROADMAP.md`
- current code, when a phase claims something is already done

Preserve this order unless the user explicitly changes risk tolerance:

1. Monolith hardening: ledger authority, webhook boundary, confirmation/finality, reorg/reconciliation, payout/refund/sweep lifecycle events.
2. Modular monolith: internal boundaries, Postgres outbox, idempotent consumers, contract tests, Ledger-derived balance APIs.
3. Worker split: webhook, chain indexer, reconciliation, pricing.
4. Custody split: external signer, hard Ledger boundary, withdrawal service contract, custody policy.
5. Exchange-grade scale: sharded indexers, provider quorum/archive strategy, distributed queues/rate limits, dashboards, SLOs.

For each phase, produce:

- Objective
- Architecture ADs it closes
- Required code areas
- Exit criteria
- Blocking dependencies
- Tests and operational evidence

If a requested phase violates the spine, say exactly which AD it violates and propose the nearest compliant slice.
