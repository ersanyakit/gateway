---
title: Gateway Payment & Wallet Platform PRD
status: final
created: 2026-06-27
updated: 2026-06-27
canonical_workspace: prds/prd-gateway-2026-06-27/prd.md
---

# PRD Index: Gateway Payment & Wallet Platform

Canonical PRD: [prds/prd-gateway-2026-06-27/prd.md](prds/prd-gateway-2026-06-27/prd.md)

This root file exists so downstream BMad workflows that scan `_bmad-output/planning-artifacts/*prd*.md` can discover the canonical PRD. Use the canonical workspace file as the source of truth.

## Scope

Gateway has two product surfaces: merchant/dealer payment gateway and exchange/user wallet-provider infrastructure. The MVP launch posture is controlled merchant/dealer beta. Wallet-provider custody and exchange-grade tracking are gated behind external signer, compliance, reconciliation, observability, and scale-readiness evidence.

## Key Decisions

- Current Go monolith remains the runtime shell while money boundaries harden.
- One shared money core serves checkout, static wallets, deposit, ledger, withdrawal, sweep, webhook, signer, and reconciliation flows.
- Real customer funds require external signer/custody integration; software signing is development-only.
- Compliance for AML/KYT/sanctions/travel-rule is gated before custody/exchange-grade launch.
- Postgres outbox is the first durable event substrate; external broker is deferred until measured need.

## Sources

- `_bmad-output/planning-artifacts/prds/prd-gateway-2026-06-27/prd.md`
- `_bmad-output/planning-artifacts/epics.md`
- `_bmad-output/planning-artifacts/implementation-readiness-report-2026-06-27.md`
- `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md`
- `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/SOLUTION-DESIGN.md`
