# WDS Design Log - Gateway

## Current

- WDS Project Brief flow started for a crypto wallet provider and payment gateway.
- Vision Capture completed and confirmed.
- Positioning completed and confirmed from `_bmad-output` sources.
- Business Model completed from `_bmad-output` sources.
- Existing BMad artifacts are available and should be treated as source context:
  - `_bmad-output/planning-artifacts/prd.md`
  - `_bmad-output/planning-artifacts/ux.md`
  - `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md`
  - `docs/payment-gateway-wallet-provider-audit.md`
  - `docs/product-readiness-audit.md`
  - `docs/integration-guide.md`
- Initial role framing: "bayi" maps to merchant/dealer/partner unless renamed later by the product owner.

## Decisions

- Use complete Product Brief level because the product handles money movement, custody-adjacent wallet flows, webhooks, reconciliation, and admin approvals.
- Treat the product as two connected surfaces sharing one money core:
  - Merchant/dealer payment gateway.
  - Wallet-provider-as-a-service / exchange-user wallet infrastructure.
- Design work must expose operational truth for risky money workflows: finality, ledger authority, signer boundary, webhook delivery, reconciliation, and approval audit trails.
- Business pressure is broad: live readiness, merchant/partner acquisition, wallet-provider growth, investor/partner readiness, and productization all matter.
- Confirmed vision: one shared money core for merchant/dealer payment gateway and wallet-provider-as-a-service; controlled merchant/dealer beta first, wallet-provider expansion only when operational gates are strong enough.
- Confirmed positioning: B2B crypto payment gateway + wallet-provider infrastructure, differentiated by shared money core across payment lifecycle, static wallet, ledger, webhook, reconciliation, and admin recovery.
- Business model: B2B infrastructure platform. Merchant/dealer and wallet/exchange tenants are paying customers; checkout payers and wallet end users are downstream users.

## Backlog

- Confirm whether "bayi" means merchant, reseller, exchange client, or internal dealer.
- Define target users and roles for merchant/dealer and admin/operator portals.
- Define pricing/commercial packaging later; `_bmad-output` does not specify it.
- Create Product Brief, then Trigger Map, then Outline Scenarios.

## WDS Development Integration - 2026-06-27

- WDS `D` development lane is active alongside the existing BMad implementation flow.
- Development baseline: PRD, UX handoff, architecture spine, epics, sprint status, Story 1.1, and `_bmad-output/project-context.md`.
- Story 1.1 is done with canonical request auth, replay protection, tenant-scope tests, query-bound signing, redacted auth diagnostics, fresh-context review fixes, and validation commands.
- Next implementation candidate is Story 1.2, but it should get a dedicated story/spec file and validation before code changes.
- Story 1.2 story/spec is created and validated for idempotent selected-asset payment session creation; next step is development environment baseline before implementation.
