# Product Brief Decisions - gateway

## 2026-06-27

- Product framing corrected from generic dealer/admin portal to crypto currency wallet provider and payment gateway.
- Organisation profile: one-person product / technical team.
- Decision ownership: ersan owns product and technical decisions.
- Approval model: no external approval chain confirmed.
- Decision culture: fast individual decisions, suitable for iterative WDS work.
- Internal driver combines live readiness, merchant/partner acquisition, wallet-provider growth, investor/partner readiness, and productizing the existing platform.
- Vision confirmed: one shared money core for crypto payment gateway and wallet-provider surfaces, with merchant/dealer beta first and wallet-provider growth gated by custody, signer, reconciliation, compliance, observability, and operational readiness.
- Positioning confirmed: B2B crypto payment gateway + wallet-provider infrastructure for merchant/dealer teams, developers, and small/mid wallet or exchange platforms; differentiated by one shared money core rather than checkout-only processing or fragmented in-house wallet operations.
- Business model determined from `_bmad-output`: B2B infrastructure platform. Merchant/dealer tenants and wallet/exchange platform tenants are the paying business customers; checkout payers and wallet end users are downstream users, not direct Gateway buyers. Pricing/commercial packaging is not defined in `_bmad-output` and remains deferred.

## Business Model Decision Detail

- **Opening basis:** Per product-owner instruction, business model was derived from `_bmad-output` rather than re-asked.
- **Evidence:** PRD, positioning, architecture, UX, and epics consistently describe partner-facing tenant/domain infrastructure for merchant/dealer and wallet/exchange customers.
- **Final decision:** B2B infrastructure platform.
- **Rationale:** The system sells operational crypto payment/wallet infrastructure to tenants that integrate APIs, configure webhooks, manage payments/wallets, and operate money-state workflows.
- **Implications:** Design must prioritize tenant onboarding, developer integration, credentials, webhook diagnostics, operational dashboards, audit trails, launch readiness, and clear separation between direct business customers and checkout/end-wallet users.

## Business Customer Definition

- **Business customer profile:** small/mid merchant/dealer tenants and small/mid wallet or exchange platform tenants that need crypto payment gateway or wallet-provider infrastructure.
- **Buyer vs end-user distinction:** external buyer is usually founder/owner/business decision-maker; technical champion is founder/CTO/integration lead; users include developer integrators, merchant/exchange operators, platform/admin operators, custody/security owners, checkout payers, and wallet end users.
- **Current onboarding decision:** ersan owns all first-phase sales/onboarding roles: business selection, technical evaluation, pilot approval, and commercial decision.
- **Decision criteria:** safe integration, money lifecycle trust, controlled beta limits, launch readiness visibility, tenant isolation, auditability, redaction, and recovery paths.

## Target User Definition

- **Primary users:** developer integrator, merchant/exchange operator, and platform/admin operator.
- **Secondary users:** checkout payer, custody/security owner, platform owner, and downstream wallet end user.
- **Non-users for v1:** high-volume regulated exchanges needing production custody before readiness gates, self-custody wallet consumers, fiat/card/bank settlement users, and general-purpose blockchain indexer users.
- **Basis:** derived from finalized PRD and UX flows in `_bmad-output`, without asking additional questions because the source folder contained enough detail.
- WDS development lane will wrap implementation work: code changes should trace back to PRD, UX handoff, architecture spine, epics, sprint status, story files, and `_bmad-output/project-context.md`.
