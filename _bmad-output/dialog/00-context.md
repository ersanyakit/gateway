# Context & Working Relationship

**Step:** Phase 1 - Product Brief Initialization
**Completed:** 2026-06-27
**Session:** 1

---

## Project Metadata

**Project Name:** gateway
**Project Slug:** gateway
**Product Type:** web app / platform
**Industry:** crypto payment infrastructure, wallet provider, payment gateway

---

## Working Relationship Context

### Stakes

**Level:** enterprise

**What this means:**
The product handles money movement, merchant integrations, wallet generation, deposits, withdrawals, refunds, sweeps, webhooks, ledger state, and admin/operator actions. UX decisions must reduce operational risk, expose system state clearly, and avoid hiding compliance or custody readiness gaps.

**Stakeholders:**
- Product owner
- Merchant/dealer users
- Admin/operator users
- Developers integrating payment and wallet APIs
- Risk, operations, and support users

**Political Sensitivities:**
Do not present wallet-provider or custody surfaces as production-safe unless signer, reconciliation, compliance, observability, and operational gates are satisfied.

---

### Collaboration Style

**Involvement Level:** collaborative
**User Role:** product owner / technical stakeholder
**Recommendation Style:** direct

**What this means for our work:**
Use the existing repo artifacts as evidence, make explicit assumptions, and ask only for decisions that materially affect the design.

---

## Project Configuration

**Brief Level:** complete
**Strategic Analysis:** full
**Skip Design System:** yes
**Skip Trigger Map:** no

**Product Complexity:** complex
**Tech Stack:** Go/Gofiber, PostgreSQL, server-rendered HTML/Tailwind, blockchain integrations, Trust Wallet Core, chain SDKs
**Component Library:** none / existing server-rendered UI conventions

---

## Initial Context From User

The design work is for a crypto wallet provider and payment gateway. The two initial portal audiences are:

- **Bayi:** assumed to mean merchant/dealer/partner portal user.
- **Admin:** assumed to mean internal operator/admin portal user.

This framing replaces a generic dealer-commerce interpretation.
