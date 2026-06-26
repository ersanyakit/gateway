---
name: Gateway Ops
status: final
sources:
  - ../../prd.md
  - ../../epics.md
  - ../../implementation-readiness-report-2026-06-27.md
  - DESIGN.md
updated: 2026-06-27
---

# Gateway Ops - Experience Spine

## Foundation

Gateway uses server-rendered HTML templates under `views/`, static assets under `views/assets/` and `static/`, Tailwind CSS, and Gofiber template rendering. This UX contract assumes the existing stack continues; it does not require a new SPA framework. `DESIGN.md` owns visual tokens and component appearance. This file owns information architecture, behavior, states, interactions, accessibility, and key flows.

Primary surfaces:

- Hosted checkout for payers.
- Dealer/merchant portal for onboarding, API/webhook configuration, dashboard, static wallets, and payment visibility.
- Admin portal for refunds, rescans, security controls, withdrawals, and operational review.
- Operator diagnostics for webhook replay/dead-letter, reconciliation, provider health, migration/runbook evidence, and launch readiness.

## Information Architecture

| Surface | Reached From | Purpose |
| --- | --- | --- |
| Hosted checkout asset selection | Checkout URL | Select supported asset/network and inspect invoice contract |
| Hosted checkout payment | Asset selection | Show amount, address, QR, expiry, copy action, status refresh |
| Checkout terminal result | Payment page redirect/status | Show paid, expired, failed, canceled, or underpaid outcome |
| Dealer dashboard | Dealer login | Merchant/dealer payment overview, API docs, wallet/payment shortcuts |
| API credentials and webhook settings | Dealer/admin settings | Manage API key, webhook URL, webhook secret lifecycle, HMAC guidance |
| Static wallets | Dealer dashboard/API docs | Create/list deterministic wallets by tenant/domain/product/user |
| Payment sessions | Dealer dashboard | Inspect sessions, status, amount, tx/finality, webhook state |
| Webhook diagnostics | Dealer/admin operations | Search events, attempts, dead letters, replay state, redacted payloads |
| Reconciliation dashboard | Admin operations | Inspect drift/reorg/stuck lifecycle jobs and recovery outcomes |
| Withdrawal/refund/sweep review | Admin operations | Approve/reject, inspect holds, signer state, policy controls, audit |
| Provider health | Admin operations | Chain lag, RPC health, stale-head/inconsistent-head signals |
| Launch readiness | Admin/owner operations | Controlled beta gate evidence, SLOs, runbooks, migration readiness |

Navigation rules:

- Dealer users see dealer-scoped surfaces only.
- Admin users see operational, security, and recovery surfaces.
- Payer checkout is isolated from authenticated app chrome.
- Modal stacks are one level deep. Risky actions use confirmation dialogs with explicit scope and consequence.

## Voice And Tone

Microcopy must be factual and short. Brand voice lives in `DESIGN.md`; this section governs UI text behavior.

| Do | Don't |
| --- | --- |
| "Payment confirming" | "Almost there!" |
| "Webhook replay queued" | "Success! Your webhook is flying" |
| "Insufficient available balance" | "Something went wrong" |
| "Signer integration required" | "Signer unavailable" when the real issue is missing integration |
| "Reconciliation opened for ledger drift" | "Balance issue" |
| "Expired" / "Underpaid" / "Paid" | Ambiguous states like "Complete?" |

Error copy must name the category, not leak sensitive internals. Diagnostic copy may show event ids, correlation ids, tenant/domain scope, and redacted payload excerpts.

## Component Patterns

| Component | Use | Behavioral Rules |
| --- | --- | --- |
| Checkout status block | Hosted checkout | One status at a time: active, pending, confirming, paid, expired, failed, underpaid. Paid is terminal and stable. |
| QR/address block | Hosted checkout | Copy button must be adjacent to address. QR and text address both visible on desktop; on mobile QR may stack above address. |
| Expiry timer | Checkout | When expired, disable payment affordance and show terminal expired state. Do not silently refresh to a new invoice. |
| Money table | Dealer/admin lists | Sort/filter without losing current scope. Amounts include raw/asset metadata where relevant. |
| Status pill | All operational views | Text + color. Never color-only. |
| Replay action | Webhook diagnostics | Requires permission scope, shows original event id, creates replay attempt tied to original event. |
| Reconciliation detail | Ops | Shows evidence tabs: chain facts, ledger entries, lifecycle state, webhook state, broadcast state. |
| Approval panel | Withdrawals/refunds/sweeps | Shows actor, tenant/domain, destination, amount, hold, policy checks, signer mode, chain-resource reservation. |
| Redacted payload preview | Webhook and logs | Secrets, private keys, mnemonics, raw signatures, and internal diagnostic blobs are omitted or masked. |
| Launch gate checklist | Launch readiness | Every item has status, owner, evidence link/notes, and blocking/non-blocking classification. |

## State Patterns

| State | Surface | Treatment |
| --- | --- | --- |
| Checkout loading | Checkout | Skeleton or compact loading copy; no missing amount/address placeholder that looks payable. |
| Active checkout | Checkout | Show asset/network, expected amount, address, QR, expiry, and copy action. |
| Pending detection | Checkout | Show that payment is not yet detected; keep invoice details visible. |
| Confirming finality | Checkout | Show confirmation/finality progress when available; do not show paid early. |
| Paid | Checkout | Terminal success. Keep amount, asset, tx reference where safe, and merchant return action. |
| Expired | Checkout | Terminal expired. Disable payment instruction affordance. |
| Underpaid/failed | Checkout | Distinct from pending and paid; explain merchant will resolve or payer should contact merchant. |
| Webhook transient failure | Webhook diagnostics | Attempt remains retryable with next attempt time and failure category. |
| Webhook dead-letter | Webhook diagnostics | Mark terminal failed; replay action visible for authorized operator. |
| Reorg detected | Reconciliation | Show affected tx/payment/ledger/webhook/sweep scope and correction status. |
| Ledger invariant drift | Reconciliation | Open job, severity, affected idempotency key, and next recovery action. |
| Signer missing in production | Withdrawal/release | Block signing and show integration-required state. |
| Provider degraded | Provider health | Mark chain/provider degraded and show fallback/degraded-mode policy. |
| Launch gate blocked | Launch readiness | Gate item blocks real-funds launch until evidence exists. |

## Interaction Primitives

- Copy actions for address, event id, request id, tx hash, and correlation id use icon+tooltip or compact button text depending on current template capability.
- Risky commands require explicit confirmation: replay, retry replacement, approve withdrawal, reject withdrawal, emergency freeze, migration approval, and launch gate override.
- Filters persist within a session for webhook diagnostics, reconciliation, and money tables.
- Operator surfaces must support keyboard tab traversal and visible focus states.
- No hover-only controls on mobile or touch-width layouts.
- No infinite scroll for audit, money, webhook, or reconciliation logs; use pagination or cursor navigation.

## Accessibility Floor

- WCAG 2.2 AA contrast for text, controls, focus indicators, and status tokens.
- All icon-only controls need accessible labels and visible tooltip/help text on hover/focus.
- Status must be conveyed through text and ARIA labels, not color alone.
- Checkout copy/address controls must be usable at mobile width without overlap.
- Forms must associate labels with inputs and preserve validation text near the field.
- Dialogs trap focus and return focus to the invoking control on close.
- Tables need headers and logical reading order; stacked mobile rows need label/value pairs.

## Responsive And Platform

| Viewport | Behavior |
| --- | --- |
| Mobile | Checkout is single-column. Dealer/admin tables become stacked rows or horizontal-scroll data grids only where hashes/IDs require it. |
| Tablet | Main shell uses one content column with optional right-side details below primary list. |
| Desktop | Operational dashboards may use list + detail or two-column evidence layouts. Keep dense data inside aligned panels/tables. |

Existing server-rendered pages must render without text overlap at common mobile widths. Fixed-format elements such as QR blocks, status rows, and table action cells need stable dimensions.

## Product-Specific UX Rules

- Checkout must never display an address if the payment session is expired, disabled, or unsupported.
- Checkout must never display internal-only diagnostics to payers.
- Dealer/admin diagnostics must redact secrets by default.
- Operator replay must never leak whether another tenant's resource exists.
- Withdrawal approval must show ledger hold/reservation and signer state before approval actions.
- Reconciliation jobs must always show reason, scope, affected resources, status, and audit trail.
- Launch readiness must distinguish controlled beta, production-grade payment gateway, wallet-provider custody, and exchange-grade tracking.

## Key Flows

### Flow 1 - Hosted checkout payment (Ayla, payer on mobile)

1. Ayla opens a checkout URL from a merchant.
2. The checkout shows selected or selectable asset/network, expected amount, deposit address, QR, and expiry.
3. Ayla copies the address or scans the QR and sends payment.
4. The status changes to pending or confirming as chain detection/finality progresses.
5. **Climax:** the page shows terminal `Paid` only when settlement/finality requirements are met.
6. Failure: if the session expires or is underpaid, the terminal state is distinct and does not look payable.

### Flow 2 - Developer integration proof (Deniz, merchant developer)

1. Deniz logs into the dealer portal and opens API/integration docs.
2. He creates a payment session with an idempotency key.
3. He retries the same request and confirms the same session response.
4. He opens webhook settings and sees signing guidance and event contract examples.
5. **Climax:** contract evidence shows request authentication, idempotency, response shape, and webhook versioning are stable.

### Flow 3 - Webhook replay recovery (Elif, operator)

1. Elif opens webhook diagnostics filtered to dead-letter events.
2. She selects a failed event and reviews redacted attempt metadata.
3. She confirms a replay within tenant/domain scope.
4. The system creates a replay attempt tied to the original event id.
5. **Climax:** the event leaves dead-letter investigation with duplicate-safe metadata and audit log.

### Flow 4 - Reconciliation recovery (Zeynep, platform operator)

1. Zeynep opens the reconciliation dashboard after a ledger drift alert.
2. She opens a job and reviews chain facts, ledger entries, payment/deposit lifecycle, webhook state, and broadcast state.
3. She records the recovery outcome or schedules a retry.
4. **Climax:** uncertain money state is resolved or explicitly escalated without destructive history edits.

### Flow 5 - Withdrawal approval safety (Murat, custody operator)

1. Murat opens a pending withdrawal/refund/sweep request.
2. He reviews tenant/domain, wallet, amount, destination, ledger hold, policy checks, signer mode, and chain-resource reservation.
3. If a production signer is missing or policy fails, signing remains blocked.
4. If all gates pass, he approves within his role scope.
5. **Climax:** no outbound money moves without hold, approval, signer boundary, chain-resource reservation, and audit evidence.

### Flow 6 - Launch readiness review (Ersan, platform owner)

1. Ersan opens launch readiness before enabling real customer funds.
2. The checklist separates controlled beta, production gateway, wallet-provider custody, and exchange-grade tracking.
3. He reviews signer readiness, ledger/reconciliation evidence, durable eventing, webhook recovery, migrations, SLOs, alerts, runbooks, and compliance scope.
4. **Climax:** launch scope is approved only for the readiness level with complete evidence.
