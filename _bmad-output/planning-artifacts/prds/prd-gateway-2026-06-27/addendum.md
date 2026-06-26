---
title: gateway Payment & Wallet Platform PRD Addendum
created: 2026-06-27
updated: 2026-06-27
project: gateway
---

# Addendum: gateway Payment & Wallet Platform PRD

Bu addendum, PRD ana anlatısını şişirmemesi gereken teknik gerekçe, deferred decision ve kaynak uzlaştırma notlarını tutar. Ana PRD capability ve gereksinim baseline'ıdır; bu dosya downstream architecture, UX, delivery ve launch governance için ek bağlamdır.

## Kaynak Uzlaştırma

Kullanılan kaynaklar:

- `_bmad-output/planning-artifacts/implementation-readiness-report-2026-06-27.md`
- `_bmad-output/planning-artifacts/epics.md`
- `_bmad-output/planning-artifacts/delivery/gateway-remaining-stages-2026-06-26.md`
- `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md`
- `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/SOLUTION-DESIGN.md`
- `docs/product-readiness-audit.md`
- `docs/payment-gateway-wallet-provider-audit.md`

Implementation readiness raporu canonical PRD bulunmadığını kritik issue olarak işaretledi. Bu PRD, aynı rapordaki PRD-equivalent FR/NFR inventory'yi canonical hale getirir ve existing `epics.md` coverage map'i bozmadan ürün baseline'ına taşır.

## Mevcut Güçlü Temel

- Payment session, checkout URL, static wallet, API auth, HMAC request signature ve idempotency temeli mevcut.
- Multi-chain wallet/address generation Trust Wallet Core üzerinden kurulmuş; fallback provider production-capable kabul edilmiyor.
- Ledger, webhook delivery records, retry worker, finality fields, listener workers, withdrawal/refund ve admin/dealer surfaces mevcut.
- Son düzeltmeler durable sweep job, webhook retry backoff, event versioning, configurable start block, reorg ledger reversal ve ledger invariant reconciliation temelini güçlendirmiş.

## Bilerek PRD Ana Metnine Alınmayan Teknik Detaylar

- Go/Gofiber/GORM/PostgreSQL stack kararı architecture artifact'te tutulur.
- Target package layout (`internal/wallet`, `internal/deposit`, `internal/ledger`, vb.) architecture artifact'te source-tree guidance olarak kalır.
- Specific external broker seçimi PRD requirement değildir; Postgres outbox ilk durable substrate olarak PRD'de sabitlenir.
- Specific signer provider seçimi PRD'de requirement olarak değil launch gate/open question olarak kalır.
- Chain-specific signing implementation detayları (`gagliardetto/solana-go`, OKX TRON SDK, Trust Wallet Core binding) PRD ana metnine alınmamıştır.

## Deferred Decisions

| Decision | Deferred Until |
| --- | --- |
| Specific production signer provider | Provider/compliance/chain coverage/latency/ownership kararı verildiğinde |
| External broker selection | Postgres outbox throughput, partitioning veya independent service deployment gereksinimi kanıtlandığında |
| Physical service extraction order | Modular monolith boundaries, outbox consumers ve contract tests staging'de kanıtlandığında |
| Exact exchange-grade indexer sharding scheme | Target chain list, address count, block lag SLO ve archive-node erişimi netleştiğinde |
| AML/KYT/travel-rule integration | Target jurisdiction, customer type ve compliance provider seçildiğinde |
| Hot/warm/cold custody policy | Production signer ve approval model seçildiğinde |
| Merchant-facing webhook diagnostics UI shape | Event catalog ve delivery model finalized olduğunda |

## PRD Concern Scan

Bu ürün aşağıdaki cross-cutting concern'leri taşır:

- Custody security: private key/mnemonic visibility, signer audit, external signer.
- Data integrity: idempotency, double-entry ledger, duplicate credit/withdrawal prevention.
- Chain reliability: finality, reorg, block hash continuity, replay/backfill.
- Public API contract: versioning, deprecation, error envelope, OpenAPI tests.
- Webhook reliability: delivery, retry, dead-letter, replay, diagnostics.
- Operational readiness: migrations, logs, metrics, traces, alerts, runbooks, backup/restore.
- Compliance: AML/KYT/sanctions/travel-rule scope decision.
- Scale: sharded indexer, partitioned address lookup, provider quorum/archive strategy.
- UX: hosted checkout states, merchant diagnostics, operator recovery dashboards.

## Reviewer Notes for Downstream Workflows

- `bmad-ux` should start from PRD §7 and UJ1-UJ5, then produce lightweight specs for hosted checkout, webhook diagnostics, reconciliation dashboard and admin recovery surfaces.
- `bmad-create-epics-and-stories` has already produced `epics.md`; per-story FR/NFR tags have been added for downstream traceability.
- `bmad-architecture` already produced spine/design; future update should reconcile any PRD launch gate changes back into architecture decisions.
- Sprint planning should preserve the sequence: Epic 1 -> Epic 2 contracts/outbox -> Epic 3 settlement/ledger -> Epic 4 outbound/custody -> Epic 5 production gates.
