---
stepsCompleted:
  - step-01-document-discovery
  - step-02-prd-analysis
  - step-03-epic-coverage-validation
  - step-04-ux-alignment
  - step-05-epic-quality-review
  - step-06-final-assessment
includedDocuments:
  - _bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md
  - _bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/SOLUTION-DESIGN.md
  - _bmad-output/planning-artifacts/epics.md
  - _bmad-output/planning-artifacts/delivery/gateway-remaining-stages-2026-06-26.md
  - docs/product-readiness-audit.md
  - docs/payment-gateway-wallet-provider-audit.md
---

# Implementation Readiness Assessment Report

**Date:** 2026-06-27
**Project:** gateway

## Step 1: Document Discovery

### PRD Files Found

**Whole Documents:** None

**Sharded Documents:** None

**Warning:** PRD bulunamadı. Assessment, önceki workflow'da onaylandığı gibi PRD-equivalent delivery/audit kaynaklarıyla yapılacaktır.

### Architecture Files Found

**Whole Documents:**

- `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md` — 16,538 bytes, modified `2026-06-26 23:05:39 +03`
- `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/SOLUTION-DESIGN.md` — 5,254 bytes, modified `2026-06-26 23:05:31 +03`

**Related Architecture Review Files:**

- `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/reviews/review-data-integrity-security.md`
- `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/reviews/review-tech-currentness.md`
- `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/reviews/review-adversarial-boundaries.md`
- `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/reviews/review-rubric.md`

**Sharded Documents:** No `architecture*/index.md` found.

### Epics & Stories Files Found

**Whole Documents:**

- `_bmad-output/planning-artifacts/epics.md` — 67,245 bytes, modified `2026-06-27 00:13:48 +03`

**Sharded Documents:** None

### UX Design Files Found

**Whole Documents:** None

**Sharded Documents:** None

**Warning:** UX design contract bulunamadı. Hosted checkout UX state'leri epics/stories içinde ele alınmıştır, fakat ayrı UX spec yoktur.

### Additional Planning Files

- `_bmad-output/planning-artifacts/delivery/gateway-remaining-stages-2026-06-26.md` — 3,515 bytes, modified `2026-06-26 23:12:07 +03`
- `docs/product-readiness-audit.md`
- `docs/payment-gateway-wallet-provider-audit.md`

### Issues Found

- Critical duplicate issue yok.
- Missing PRD var; assessment PRD-equivalent kaynaklarla yapılacak.
- Missing UX var; UI kapsamı sınırlı doğrulanabilir.

## Step 2: PRD Analysis

Canonical PRD bulunmadığı için bu bölüm, Step 1'de onaylanan PRD-equivalent kaynaklardan çıkarılmış normalize requirements inventory üzerinden yapılmıştır:

- `_bmad-output/planning-artifacts/delivery/gateway-remaining-stages-2026-06-26.md`
- `docs/product-readiness-audit.md`
- `docs/payment-gateway-wallet-provider-audit.md`
- `_bmad-output/planning-artifacts/epics.md`

### Functional Requirements

FR1: Sistem iki ürün yüzeyini desteklemelidir: e-ticaret merchant payment gateway ve exchange/user wallet provider.

FR2: Merchant payment gateway; payment session, hosted checkout, static deposit address, payment lifecycle ve merchant webhook akışlarını sağlamalıdır.

FR3: Wallet provider yüzeyi; user wallet, deposit, balance, withdrawal, sweep ve reconciliation akışlarını sağlamalıdır.

FR4: Merchant checkout/static-address akışları ve exchange wallet akışları; Wallet, Chain Indexer, Deposit, Ledger, Signer, Webhook ve Reconciliation boundary'lerini ortak money core olarak kullanmalıdır.

FR5: Sistem payment session oluştururken checkout URL, seçilen chain/token, quote snapshot, expected raw amount, expiry, deposit address, transaction hash/finality alanları ve idempotency davranışını yönetmelidir.

FR6: Hosted checkout; asset seçimi, QR gösterimi, ödeme durumunun gerçek zamanlı veya websocket tabanlı izlenmesi ve mobil/regression güvenilirliği için testlenebilir davranışlar sağlamalıdır.

FR7: Sistem tenant/domain/product/user kapsamına göre deterministic static deposit wallet üretmeli ve aynı scope için tekil wallet döndürmelidir.

FR8: Desteklenen chain'lerde wallet/address generation Trust Wallet Core üzerinden yapılmalı; fallback provider production'da adres üretmemeli veya sessizce geçerliymiş gibi davranmamalıdır.

FR9: Sistem v1 API erişiminde API key veya bearer authentication sağlamalı; mutating endpoint'lerde timestamp ve HMAC request signature doğrulamalıdır.

FR10: Merchant/exchange write API'leri idempotency key kabul etmeli; aynı key farklı payload ile kullanıldığında conflict dönmeli; iç ledger/outbox kayıtlarında DB-level uniqueness uygulanmalıdır.

FR11: Chain Indexer boundary, block/slot progress, raw transaction/log extraction, provider health, finality signal ve reorg detection işlerini sahiplenmeli; business state'i doğrudan mutate etmemelidir.

FR12: Chain listener'lar EVM family, Bitcoin, Solana ve TRON için deposit detection yapmalı; explicit start block/slot ve range replay/backfill yapılandırmasını desteklemelidir.

FR13: Deposit boundary chain event'lerini wallet ownership ile eşleştirmeli, deposit lifecycle'ı yönetmeli ve payment session settlement input'u üretmelidir.

FR14: Payment/deposit settlement, chain-specific confirmation/finality gate tamamlanmadan terminal paid/succeeded davranışına geçmemelidir.

FR15: Payment lifecycle succeeded, failed ve expired durumlarını desteklemeli; reorg veya correction durumunda payment correction webhook'u ve lifecycle düzeltmesini tetikleyebilmelidir.

FR16: Payment matching, underpaid, overpaid ve partial-paid durumlarını ayrı lifecycle/status/event olarak modellemeli veya açık business policy ile ele almalıdır.

FR17: Ledger boundary pending, available, hold, transit, debit, credit, reversal ve adjustment kayıtlarını yönetmeli; balance endpoint'leri sadece ledger-derived projection veya query layer üzerinden çalışmalıdır.

FR18: `transactions`, `payment_sessions`, `withdrawal_requests`, `refunds` ve `sweep_jobs` lifecycle state tutabilir, ancak authoritative balance kaynağı olarak kullanılmamalıdır.

FR19: Withdrawal, payout, refund ve sweep talepleri ledger hold/reservation olmadan imzalanmamalı veya broadcast edilmemelidir.

FR20: Outbound money akışları chain-specific nonce, UTXO, resource/gas veya equivalent reservation almadan signing aşamasına geçmemelidir.

FR21: Withdrawal/payout/refund akışları request, policy validation, hold, admin approval veya maker-checker approval, signing, broadcast, finalization/release ve webhook lifecycle'ını kapsamalıdır.

FR22: Auto-sweep finalized deposit sonrası durable `sweep_jobs` benzeri persistent job olarak yazılmalı; claim, retry, exponential backoff, dead-letter, tx hash kaydı ve recovery davranışları sağlamalıdır.

FR23: Gas prefund veya chain-specific funding alt işleri idempotent olmalı ve sweep/withdrawal concurrency policy ile yönetilmelidir.

FR24: Signer boundary production'da KMS/HSM/MPC/Vault veya seçilecek external custody signer ile çalışmalı; application code private key veya mnemonic alamamalı, saklamamalı ve loglamamalıdır.

FR25: Signing request'leri key reference, chain, derivation/account context, transaction intent ve policy metadata ile yapılmalıdır.

FR26: Webhook boundary source money flow'lardan ayrılmalı; source modules sadece versioned webhook event enqueue etmeli, delivery/retry/replay/dead-letter/HMAC/diagnostics davranışları Webhook boundary tarafından yönetilmelidir.

FR27: Sistem payment, deposit, withdrawal, refund, sweep ve correction event'leri için versioned event catalog sağlamalıdır.

FR28: Yeni money event contract'ları dotted ve versioned adlar kullanmalıdır: `deposit.detected.v1`, `deposit.finalized.v1`, `payment.succeeded.v1`, `payment.failed.v1`, `payment.expired.v1`, `withdrawal.requested.v1`, `withdrawal.broadcast.v1`, `withdrawal.finalized.v1`, `withdrawal.failed.v1`, `refund.succeeded.v1`, `sweep.succeeded.v1`, `transaction.reorged.v1`.

FR29: Mevcut underscore webhook event adları compatibility alias olarak desteklenmeli ve resmi deprecation/event catalog migration olmadan kırılmamalıdır.

FR30: Postgres outbox, monolith içindeki ilk durable event substrate olmalı; state change ile event insert aynı transaction içinde yapılmalıdır.

FR31: Outbox consumers at-least-once delivery varsayımıyla idempotent çalışmalı; replay ve duplicate delivery testleriyle doğrulanmalıdır.

FR32: Reconciliation boundary chain facts, ledger entries, lifecycle state, webhook delivery ve broadcast state'i karşılaştıran scoped recovery job'ları açmalı ve çözmelidir.

FR33: Ledger invariant checker, on-chain balance comparison, reserve/liability reporting ve balance drift alerting production readiness kapsamına alınmalıdır.

FR34: Reorg handling block hash continuity, parent/child tracking, rollback window processor, affected ledger reversal, payment correction webhook, sweep dead-letter ve reconciliation job davranışlarını sağlamalıdır.

FR35: Fee/gas policy; EVM EIP-1559, ERC-20 gas estimation, Bitcoin fee estimation/RBF/CPFP, Solana priority fee/blockhash retry ve TRON resource/energy accounting gibi chain-specific stratejileri desteklemelidir.

FR36: RPC/provider layer provider health scoring, fallback consistency checks, archive/quorum strategy ve per-provider metrics sağlamalıdır.

FR37: Address lookup milyonlarca adres ölçeğine hazırlanmalı; chain-specific wallet kolonları yerine normalize/partitioned lookup veya equivalent indexed strategy planlanmalıdır.

FR38: Production operations; versioned migrations, environment separation, structured logs, metrics, traces, SLOs, alerts, incident runbooks, backup/restore drills, signer audit logs, withdrawal approval audit logs ve reconciliation dashboards sağlamalıdır.

FR39: Admin/security hardening; CSRF audit, role separation, key rotation policy, IP/device/session controls, immutable audit trail, address whitelist, velocity limits, dual approval ve emergency freeze davranışlarını kapsamalıdır.

FR40: API contract stability; OpenAPI contract tests, backwards-compatible error envelope, API versioning/deprecation policy ve integration guide hardening ile korunmalıdır.

Total FRs: 40

### Non-Functional Requirements

NFR1: Gerçek müşteri fonu veya yüksek hacimli production kullanım, signer, reorg, durable eventing, reconciliation, withdrawal policy ve observability P0 gate'leri tamamlanmadan açılmamalıdır.

NFR2: Para hareketleri data integrity açısından idempotent, replay-safe ve duplicate-credit/duplicate-withdrawal dirençli olmalıdır.

NFR3: Ledger kayıtları double-entry prensibine uygun olmalı; debit/credit toplamları, status enumları, unique keys, partial indexes ve ledger invariants DB-level constraints/testlerle korunmalıdır.

NFR4: Production custody güvenliği private key/mnemonic'in application process memory, app database veya loglara çıkmamasını garanti etmelidir.

NFR5: Durable job/event processing crash recovery, retry, lock, poison/dead-letter ve operator replay/recovery davranışları sağlamalıdır.

NFR6: Reorg/correction işlemleri destructive edit yapmamalı; compensating ledger entries ve correction events ile izlenebilir olmalıdır.

NFR7: Sistem küçük/orta merchant gateway pilotundan exchange-grade ölçeğe geçebilmek için chain indexer sharding, partitioned address lookup ve durable event bus'a evrilebilir olmalıdır.

NFR8: Chain catch-up throughput, block lag, webhook lag, sweep backlog, signer latency ve reconciliation drift için ölçülebilir SLO/alert eşikleri tanımlanmalıdır.

NFR9: Observability structured JSON logs, Prometheus-compatible metrics, traces, dashboards ve alert rules ile desteklenmelidir.

NFR10: Provider/RPC erişimi stale node, missed block, provider outage ve inconsistent head durumlarında failover/quorum davranışı göstermelidir.

NFR11: Backwards compatibility public webhook/API contract'ları için korunmalı; breaking changes deprecation policy olmadan yapılmamalıdır.

NFR12: Production database değişiklikleri startup `AutoMigrate` yerine versioned, reviewable, rollback-aware ve lock-aware migration stratejisiyle yapılmalıdır.

NFR13: Admin, signer, withdrawal approval, replay ve recovery aksiyonları immutable audit log ile izlenebilmelidir.

NFR14: Test kapsamı unit, integration, chain simulator, fork/reorg simulation, webhook retry, withdrawal concurrency, ledger invariant, crash recovery, `go test ./...`, `go vet ./...` ve kritik race/concurrency testlerini içermelidir.

NFR15: Merchant/operator deneyimi webhook diagnostics, replay status, dead-letter visibility ve actionable error reporting ile desteklenmelidir.

NFR16: Compliance kapsamı açıkça belirlenmeli; AML/KYT, sanctions screening, travel rule, case management veya bunların ürün kapsamı dışında olduğuna dair policy netleştirilmelidir.

NFR17: Backup/restore drills, seed/key recovery policy, signer quorum ve incident runbooks gerçek fon kullanımından önce doğrulanmalıdır.

NFR18: Tenant isolation; per-tenant rate limit, quotas, data export, audit isolation ve encryption policy ile güçlendirilmelidir.

NFR19: Pricing/quote güvenilirliği multi-source oracle, staleness guard, circuit breaker, volatility freeze ve oracle outage policy ile korunmalıdır.

NFR20: Canlıya çıkış kontrollü pilot/canary yaklaşımıyla, küçük limitler, otomatik alertler, rollback runbook ve manuel reconciliation desteğiyle yapılmalıdır.

Total NFRs: 20

### Additional Requirements

- Architecture yaklaşımı modular-monolith-first olmalıdır; fiziksel servis ayrımı ownership, idempotency, durable event contracts ve integration tests olgunlaştıktan sonra yapılmalıdır.
- Yeni kod hedef boundary'leri dikkate almalıdır: `internal/tenant`, `internal/wallet`, `internal/chainindexer`, `internal/deposit`, `internal/ledger`, `internal/payment`, `internal/withdrawal`, `internal/sweep`, `internal/signer`, `internal/webhook`, `internal/reconciliation`.
- Her boundary yalnızca kendi owned tablolarına yazmalı; cross-boundary hareketler command veya event kontratından geçmelidir.
- Architecture-level ownership adı `tenant/domain` olmalıdır; mevcut API `merchant/domain` şeklini koruyabilir, ancak yeni boundary/event tasarımları tenant'ı yalnızca e-commerce merchant varsaymamalıdır.
- Amount alanları raw integer string, decimals, symbol ve token metadata ile taşınmalı; fiat display amount'lar snapshot olmalı ve güncel oracle fiyatından yeniden hesaplanmamalıdır.
- Status değişimleri repository/service metotları üzerinden yapılmalı ve mümkün olduğunda ledger/outbox/reconciliation side effect'leriyle aynı transaction içinde gerçekleşmelidir.
- Webhook security HMAC signature, event id, event type, event version ve timestamp içermeli; callback URL delivery anında tekrar doğrulanmalıdır.
- Balance API'leri external chain reads veya transaction row toplamları yerine ledger projection/query layer üzerinden cevap vermelidir.
- Production signing için `SIGNER_MODE=software` kabul edilmemeli; software mode development-only olmalıdır.
- Worker split önceliği Webhook worker, Reconciliation worker, Pricing worker, Chain Indexer worker sırasını takip etmelidir.
- Custody split için external signer interface, signer audit log, withdrawal hold/reservation hardening, nonce/UTXO/resource reservation ve hot/warm/cold custody policy gereklidir.
- Exchange-grade scale için per-chain sharded indexer, archive/quorum provider strategy, address lookup partitioning, distributed rate limit/queue, reconciliation dashboard ve SLO/on-call model gereklidir.
- Deferred decisions açık tutulmalıdır: production signer provider, external broker, physical service extraction order, exact indexer sharding scheme, AML/KYT/travel-rule integration, hot/warm/cold policy ve merchant-facing webhook diagnostics UI.
- Stack kararları mevcut Go/Gofiber/GORM/PostgreSQL ve Trust Wallet Core entegrasyonuyla uyumlu ilerlemelidir.

### PRD Completeness Assessment

- Canonical PRD yok; bu nedenle PRD completeness doğrudan "tam" kabul edilemez.
- Buna rağmen PRD-equivalent kaynaklardan çıkarılan FR/NFR inventory kapsamlıdır ve epics/stories dosyasında normalize edilmiştir.
- En belirgin eksikler ayrı PRD kararları olarak değil, readiness riskleri olarak izlenmelidir: first production customer profile, signer provider seçimi, launch SLO'ları, compliance scope ve UX design contract.

## Step 3: Epic Coverage Validation

### Epic FR Coverage Extracted

FR1: Covered in Epic 1 - Partner Integration & Payment Intake Hardening
FR2: Covered in Epic 1 - Partner Integration & Payment Intake Hardening
FR3: Covered in Epic 1 - Partner Integration & Payment Intake Hardening
FR4: Covered in Epic 3 - Trustworthy Deposit Settlement & Ledger Balances
FR5: Covered in Epic 1 - Partner Integration & Payment Intake Hardening
FR6: Covered in Epic 1 - Partner Integration & Payment Intake Hardening
FR7: Covered in Epic 1 - Partner Integration & Payment Intake Hardening
FR8: Covered in Epic 1 - Partner Integration & Payment Intake Hardening
FR9: Covered in Epic 1 - Partner Integration & Payment Intake Hardening
FR10: Covered in Epic 1 - Partner Integration & Payment Intake Hardening
FR11: Covered in Epic 3 - Trustworthy Deposit Settlement & Ledger Balances
FR12: Covered in Epic 3 - Trustworthy Deposit Settlement & Ledger Balances
FR13: Covered in Epic 3 - Trustworthy Deposit Settlement & Ledger Balances
FR14: Covered in Epic 3 - Trustworthy Deposit Settlement & Ledger Balances
FR15: Covered in Epic 3 - Trustworthy Deposit Settlement & Ledger Balances
FR16: Covered in Epic 3 - Trustworthy Deposit Settlement & Ledger Balances
FR17: Covered in Epic 3 - Trustworthy Deposit Settlement & Ledger Balances
FR18: Covered in Epic 3 - Trustworthy Deposit Settlement & Ledger Balances
FR19: Covered in Epic 4 - Safe Outbound Funds & Custody Controls
FR20: Covered in Epic 4 - Safe Outbound Funds & Custody Controls
FR21: Covered in Epic 4 - Safe Outbound Funds & Custody Controls
FR22: Covered in Epic 4 - Safe Outbound Funds & Custody Controls
FR23: Covered in Epic 4 - Safe Outbound Funds & Custody Controls
FR24: Covered in Epic 4 - Safe Outbound Funds & Custody Controls
FR25: Covered in Epic 4 - Safe Outbound Funds & Custody Controls
FR26: Covered in Epic 2 - Reliable Money Event Delivery
FR27: Covered in Epic 2 - Reliable Money Event Delivery
FR28: Covered in Epic 2 - Reliable Money Event Delivery
FR29: Covered in Epic 2 - Reliable Money Event Delivery
FR30: Covered in Epic 2 - Reliable Money Event Delivery
FR31: Covered in Epic 2 - Reliable Money Event Delivery
FR32: Covered in Epic 3 - Trustworthy Deposit Settlement & Ledger Balances
FR33: Covered in Epic 3 - Trustworthy Deposit Settlement & Ledger Balances
FR34: Covered in Epic 3 - Trustworthy Deposit Settlement & Ledger Balances
FR35: Covered in Epic 4 - Safe Outbound Funds & Custody Controls
FR36: Covered in Epic 5 - Production Operations & Scale Readiness
FR37: Covered in Epic 5 - Production Operations & Scale Readiness
FR38: Covered in Epic 5 - Production Operations & Scale Readiness
FR39: Covered in Epic 4 - Safe Outbound Funds & Custody Controls
FR40: Covered in Epic 1 - Partner Integration & Payment Intake Hardening

Total FRs in epics: 40

### Coverage Matrix

| FR Number | Epic Coverage | Representative Story Coverage | Status |
| --- | --- | --- | --- |
| FR1 | Epic 1 | Stories 1.1-1.5 establish partner payment intake and integration surface | Covered |
| FR2 | Epic 1 | Stories 1.2, 1.4, 1.5 cover payment sessions, checkout, and partner contract | Covered |
| FR3 | Epic 1 | Story 1.3 covers wallet issuance; Stories 3.2, 3.3, 4.1-4.5 further cover deposit, balance, withdrawal, sweep, and reconciliation | Covered with cross-epic traceability note |
| FR4 | Epic 3 | Stories 3.1-3.6 enforce shared money-core behavior around chain facts, deposit, ledger, and reconciliation | Covered |
| FR5 | Epic 1 | Story 1.2 | Covered |
| FR6 | Epic 1 | Story 1.4 | Covered |
| FR7 | Epic 1 | Story 1.3 | Covered |
| FR8 | Epic 1 | Story 1.3 | Covered |
| FR9 | Epic 1 | Story 1.1 | Covered |
| FR10 | Epic 1 | Stories 1.1, 1.2 | Covered |
| FR11 | Epic 3 | Story 3.1 | Covered |
| FR12 | Epic 3 | Story 3.1 | Covered |
| FR13 | Epic 3 | Story 3.2 | Covered |
| FR14 | Epic 3 | Story 3.2 | Covered |
| FR15 | Epic 3 | Stories 3.4, 3.5 | Covered |
| FR16 | Epic 3 | Story 3.4 | Covered |
| FR17 | Epic 3 | Story 3.3 | Covered |
| FR18 | Epic 3 | Story 3.3 | Covered |
| FR19 | Epic 4 | Story 4.1 | Covered |
| FR20 | Epic 4 | Story 4.3 | Covered |
| FR21 | Epic 4 | Stories 4.1, 4.5, 4.6 | Covered |
| FR22 | Epic 4 | Story 4.4 | Covered |
| FR23 | Epic 4 | Stories 4.3, 4.4 | Covered |
| FR24 | Epic 4 | Story 4.2 | Covered |
| FR25 | Epic 4 | Story 4.2 | Covered |
| FR26 | Epic 2 | Story 2.3 | Covered |
| FR27 | Epic 2 | Story 2.1 | Covered |
| FR28 | Epic 2 | Story 2.1 | Covered |
| FR29 | Epic 2 | Stories 2.1, 2.5 | Covered |
| FR30 | Epic 2 | Story 2.2 | Covered |
| FR31 | Epic 2 | Stories 2.2, 2.4, 2.5 | Covered |
| FR32 | Epic 3 | Story 3.6 | Covered |
| FR33 | Epic 3 | Stories 3.3, 3.6 | Covered |
| FR34 | Epic 3 | Story 3.5 | Covered |
| FR35 | Epic 4 | Story 4.3 | Covered |
| FR36 | Epic 5 | Story 5.1 | Covered |
| FR37 | Epic 5 | Story 5.2 | Covered |
| FR38 | Epic 5 | Stories 5.3, 5.4, 5.5 | Covered |
| FR39 | Epic 4 | Story 4.6 | Covered |
| FR40 | Epic 1 | Story 1.5 | Covered |

### Missing Requirements

No missing FR coverage found.

### Traceability Notes

- FR3 is broad and is listed under Epic 1 in the FR Coverage Map, but its actual implementation path spans Epic 1, Epic 3, and Epic 4. This is acceptable for implementation readiness, but sprint planning should preserve cross-epic traceability for wallet provider scope.
- FR2 similarly has event/webhook implications covered by Epic 2, though its primary user-facing payment gateway surface is Epic 1.

### Coverage Statistics

- Total PRD-equivalent FRs: 40
- FRs covered in epics: 40
- Coverage percentage: 100%

## Step 4: UX Alignment Assessment

### UX Document Status

Not found.

Searches found no whole UX document and no sharded UX `index.md` under `_bmad-output/planning-artifacts`.

### UX/UI Implied By Requirements

UX is implied by multiple requirements and planning artifacts:

- Hosted checkout is explicitly required for merchant payment gateway flows.
- Checkout requires asset selection, QR display, amount/address display, expiry, and status refresh.
- Dealer/admin portal surfaces exist and are referenced by the audits.
- Operator surfaces are implied for webhook replay/dead-letter, reconciliation dashboards, provider/lag dashboards, and admin recovery.

### Alignment Findings

- Epic 1 Story 1.4 covers the highest-risk payer-facing checkout states: active, pending/confirming, paid, expired, failed, and underpaid.
- Epic 1 Story 1.5 covers partner-facing API contract and integration evidence, which partially offsets missing UX documentation for developer integrators.
- Epic 5 Story 5.4 covers operational dashboards/runbooks at a requirements level.
- Architecture supports checkout through Payment Session, Wallet, and Webhook boundaries; it also defers merchant-facing webhook diagnostics UI shape until event catalog and delivery model are finalized.

### Alignment Issues

- There is no dedicated UX design contract for hosted checkout, dealer/admin portal, webhook replay diagnostics, reconciliation dashboard, or recovery/admin screens.
- No separate UX artifact defines information architecture, interaction flows, visual states, accessibility expectations, responsive behavior, or operator task flows.
- Architecture is sufficient for backend/supporting boundaries, but UX acceptance must be protected in story-level ACs because no UX spec exists.

### Warnings

- Missing UX documentation is a readiness warning because this is a user-facing and operator-facing product.
- Before implementation of checkout or operator dashboards, create at least a lightweight UX handoff or ensure story acceptance criteria include state coverage, mobile behavior, accessibility, error handling, and no-secret exposure.
- Current stories include partial UX coverage, so this is not a blocker for all backend work, but it should block UI-heavy implementation from starting without additional detail.

## Step 5: Epic Quality Review

### Validation Scope

Reviewed `_bmad-output/planning-artifacts/epics.md` against create-epics-and-stories standards:

- 5 epics found.
- 27 stories found.
- 27 user story statements found.
- 27 acceptance criteria sections found.
- No `TBD`, `TODO`, placeholder, stub, or explicit forward-dependency marker found.

### Epic Structure Validation

| Epic | User Value | Independence | Result |
| --- | --- | --- | --- |
| Epic 1: Partner Integration & Payment Intake Hardening | Developer integrators and payers can authenticate, create sessions, receive wallets, and use checkout. | Can stand alone as the first merchant/dealer integration slice. | Pass |
| Epic 2: Reliable Money Event Delivery | Integrators and operators get durable, replay-safe money events and webhook behavior. | Can build on Epic 1 outputs, but outbound event semantics must be treated as contracts until Epic 4 lifecycle implementation lands. | Pass with sequencing caution |
| Epic 3: Trustworthy Deposit Settlement & Ledger Balances | Merchants, exchange operators, and operators can trust deposits, balances, reorg correction, and reconciliation. | Builds naturally on intake/events and does not require later Epic 4 work for deposit-side value. | Pass |
| Epic 4: Safe Outbound Funds & Custody Controls | Operators and custody owners can move funds with holds, signer boundary, approvals, and controls. | Depends on ledger concepts from Epic 3, but does not require Epic 5 to function. | Pass |
| Epic 5: Production Operations & Scale Readiness | Operators and platform owners get provider health, migration discipline, observability, launch gates, and scale evidence. | Provides operational hardening after core flows; acceptable as production-readiness value, not just infrastructure setup. | Pass with scope caution |

### Story Quality Assessment

- All stories use a recognizable `As a / I want / So that` structure and carry user/operator/integrator value.
- Acceptance criteria are BDD-style and generally testable.
- Error and negative-path coverage is present for auth failure, unsupported assets, idempotency conflict, provider failure, signer failure, replay authorization, reorg correction, insufficient funds, and security controls.
- Database/entity timing is mostly correct: migrations and schema changes are introduced where the story needs them, such as outbox, ledger, holds, address lookup, and migration discipline. There is no upfront "create all tables" story.
- Brownfield alignment is good: epics preserve current Go monolith/API compatibility while moving toward modular boundaries.
- No starter template requirement was found in Architecture, so the absence of an initial starter-template story is not a defect.

### Critical Violations

None found.

### Major Issues

None found that block implementation planning.

### Minor Concerns

1. **Sequencing caution for Epic 2 event contracts:** Epic 2 defines events for withdrawal, refund, and sweep before Epic 4 fully hardens those source lifecycles. This is acceptable if Epic 2 treats them as catalog/schema contracts and existing/current event translation, but sprint planning must not claim full outbound lifecycle readiness until Epic 4 stories are implemented.

2. **Large story candidates:** Story 3.5, Story 4.3, and Story 5.4 are high-risk and may be too broad for a single dev-agent execution depending on implementation depth. Split during sprint planning if one story would require multiple chain families, dashboard/runbook work, or reorg plus ledger plus webhook changes in one pass.

3. **Per-story FR tags are not explicit:** FR coverage is complete at the epic/report level, but individual stories do not carry `Requirements: FRx` tags. This is not a blocker, but adding them would improve later story creation and QA traceability.

4. **Epic 5 can become a catch-all if unmanaged:** It is valid because it delivers production/operator value. Keep acceptance tight so deferred implementation gaps do not get parked there without measurable release evidence.

### Recommendations

- During sprint planning, preserve the sequence Epic 1 -> Epic 2 contracts/outbox -> Epic 3 settlement/ledger -> Epic 4 outbound/custody -> Epic 5 production gates.
- Split Story 3.5, Story 4.3, and Story 5.4 if implementation estimates exceed one coherent delivery slice.
- Add per-story FR/NFR references before generating individual implementation story files.
- For Epic 2, document which events are fully emitted now versus contract-defined for later Epic 4 source lifecycles.

## Summary and Recommendations

### Overall Readiness Status

NEEDS WORK

The artifacts are strong enough to proceed with backend-focused story creation and sprint planning, but not strong enough for unrestricted implementation of UI-heavy or production-money flows. The blockers are not epic coverage; they are missing canonical PRD/UX artifacts and unresolved production launch decisions.

### Critical Issues Requiring Immediate Action

1. **No canonical PRD exists.** PRD-equivalent sources are comprehensive and cover 40 FRs plus 20 NFRs, but implementation governance should either create a canonical PRD or formally accept the current epics/report as the PRD baseline.

2. **No UX design contract exists.** Hosted checkout, admin/dealer portal, webhook replay diagnostics, reconciliation dashboard, and recovery screens need at least lightweight UX handoff before UI-heavy stories begin.

3. **Production-money launch gates remain unresolved.** Signer provider, compliance scope, launch SLOs, custody policy, backup/restore evidence, and first controlled pilot profile must be decided before real customer funds or exchange-grade operation.

### Recommended Next Steps

1. Run `bmad-create-story` for the first backend-safe story: `Story 1.1: Secure Partner API Request Authentication`.
2. Before UI implementation, create lightweight UX specs for hosted checkout and operator recovery/dashboard surfaces.
3. Add per-story FR/NFR references to `epics.md` or enforce them during `bmad-create-story`.
4. Define production readiness decisions: signer provider, AML/KYT/travel-rule scope, launch SLOs, pilot limits, and custody policy.
5. Run sprint planning only after deciding whether Story 3.5, Story 4.3, and Story 5.4 should be split.

### Final Note

This assessment identified 6 readiness issues across 4 categories: missing canonical PRD, missing UX contract, production launch decision gaps, and minor epic/story sequencing or sizing concerns. FR coverage is complete and epic quality is acceptable; address the critical readiness gaps before production-money implementation, while backend foundation stories can proceed.

**Assessor:** Codex using `bmad-check-implementation-readiness`
**Assessment completed:** 2026-06-27
