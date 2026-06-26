# Target Users

**Project:** gateway  
**Date:** 2026-06-27  
**Status:** captured from `_bmad-output`

---

## Source Context

Target users were derived from existing finalized `_bmad-output` artifacts per product-owner instruction to ask only for information that cannot be obtained from that folder.

Primary sources:

- `_bmad-output/planning-artifacts/prds/prd-gateway-2026-06-27/prd.md`
- `_bmad-output/planning-artifacts/ux-designs/ux-gateway-2026-06-27/EXPERIENCE.md`
- `_bmad-output/planning-artifacts/epics.md`
- `_bmad-output/A-Product-Brief/project-brief.md`

## Opening Basis

Positioning and business customer work already established that Gateway serves merchant/dealer tenants and wallet/exchange platform tenants. This step focused on the actual users who interact with Gateway surfaces.

## Primary User Definitions

### 1. Developer Integrator

- **Role:** Technical user inside a merchant/dealer or wallet/exchange tenant.
- **Goals:** Authenticate safely, create payment sessions, issue static wallets, retry idempotently, understand response contracts, and validate webhook delivery.
- **Frustrations:** Unclear auth rules, unstable response shapes, duplicate payment/session risk, poor webhook examples, and insufficient production-readiness evidence.
- **Current solutions:** Checkout-only processors, internal wallet/indexer/ledger code, manual wallet operations, or heavy custody/exchange infrastructure.
- **Scenarios:** Developer integration proof; payment session creation; static wallet issuance; webhook contract validation.

### 2. Merchant / Exchange Operator

- **Role:** Tenant-side operations user monitoring payment and wallet lifecycle states.
- **Goals:** Understand where funds are, whether deposits are final, whether webhooks delivered, whether balances are ledger-backed, and which failures need action.
- **Frustrations:** Ambiguous payment state, hidden webhook failures, unclear underpaid/expired handling, balance drift, and no clear audit trail.
- **Current solutions:** Chain explorers, spreadsheets, internal scripts, support escalation, or fragmented admin tools.
- **Scenarios:** Payment visibility, webhook diagnostics, balance view, refund/payout review, deposit finality, static wallet operations.

### 3. Platform / Admin Operator

- **Role:** Internal/admin operator responsible for Gateway operations and recovery.
- **Goals:** Resolve webhook dead letters, reconciliation jobs, reorg corrections, provider degradation, launch-readiness gaps, and risky outbound money operations.
- **Frustrations:** Missing correlation IDs, hidden tenant scope, destructive history edits, unsafe replay, signer ambiguity, and incomplete launch evidence.
- **Current solutions:** Database inspection, logs, manual scripts, ad hoc runbooks, or direct code intervention.
- **Scenarios:** Webhook replay recovery, reconciliation recovery, withdrawal approval safety, provider health, launch readiness review.

## Secondary Users

- **Checkout Payer:** Uses hosted checkout and needs clear amount, asset/network, address, QR, expiry, and terminal status.
- **Custody / Security Owner:** Reviews signer readiness, policy checks, private key isolation, withdrawal approvals, and custody gates.
- **Platform Owner:** Reviews controlled beta, production gateway, wallet-provider custody, and exchange-grade tracking readiness. Currently this role is held by ersan.
- **Wallet End User:** Downstream user of a tenant wallet product; not a direct v1 Gateway interface user.

## Non-Users for v1

- High-volume regulated exchanges requiring production custody before readiness gates close.
- Self-custody wallet consumers.
- Fiat on/off-ramp, card acquiring, chargeback/dispute, or bank settlement users.
- General-purpose blockchain indexer users outside owned wallet/address money movement.

## Reflection Checkpoint

No additional user question was asked because `_bmad-output` already contained finalized PRD user journeys, UX flows, jobs-to-be-done, and non-user boundaries. This respects the product-owner instruction to ask only for missing information.

## Design Implications

- Dealer/merchant, admin/operator, and checkout payer surfaces must stay separated.
- Checkout must be simple, mobile-safe, and payer-facing only.
- Dealer/operator surfaces must expose state, scope, IDs, timestamps, actor, audit trail, and next action.
- Diagnostics must redact secrets and avoid cross-tenant resource leakage.
- The next WDS phases should preserve the six UX flows already present in the UX workspace.
