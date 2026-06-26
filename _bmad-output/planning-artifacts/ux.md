---
title: Gateway UX Handoff Index
status: final
created: 2026-06-27
updated: 2026-06-27
canonical_workspace: ux-designs/ux-gateway-2026-06-27
---

# UX Handoff Index: Gateway

Canonical UX workspace:

- [DESIGN.md](ux-designs/ux-gateway-2026-06-27/DESIGN.md)
- [EXPERIENCE.md](ux-designs/ux-gateway-2026-06-27/EXPERIENCE.md)

This root file exists so downstream BMad workflows that scan `_bmad-output/planning-artifacts/*ux*.md` can discover the UX contract.

## Covered Surfaces

- Hosted checkout asset selection, payment, terminal result.
- Dealer/merchant dashboard, API credentials, webhook settings, static wallets, payment sessions.
- Webhook diagnostics, replay, dead-letter recovery.
- Reconciliation dashboard and reconciliation job detail.
- Withdrawal/refund/sweep review and approval.
- Provider health and launch readiness.

## Key Rules

- Existing server-rendered HTML + Tailwind stack remains the UI foundation.
- Checkout must be mobile-safe and must not show paid before finality/settlement gates.
- Operator screens must show scope, actor, status, timestamps, IDs, and audit trail near risky actions.
- Secrets, private keys, mnemonics, raw signatures, and internal-only diagnostic payloads are never shown.
- UI-heavy implementation should use the canonical UX workspace as the acceptance baseline.
