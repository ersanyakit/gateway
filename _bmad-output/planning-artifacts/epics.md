---
stepsCompleted:
  - step-01-validate-prerequisites
inputDocuments:
  - _bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md
  - _bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/SOLUTION-DESIGN.md
  - _bmad-output/planning-artifacts/delivery/gateway-remaining-stages-2026-06-26.md
  - docs/product-readiness-audit.md
  - docs/payment-gateway-wallet-provider-audit.md
---

# gateway - Epic Breakdown

## Overview

This document provides the complete epic and story breakdown for gateway, decomposing the requirements from PRD-equivalent delivery/audit sources, Architecture requirements, and UX Design if it exists into implementable stories.

## Requirements Inventory

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

### NonFunctional Requirements

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

### UX Design Requirements

UX design contract bulunamadı; bu step'te ayrı UX-DR çıkarılmadı.

### FR Coverage Map

{{requirements_coverage_map}}

## Epic List

{{epics_list}}

<!-- Repeat for each epic in epics_list (N = 1, 2, 3...) -->

## Epic {{N}}: {{epic_title_N}}

{{epic_goal_N}}

<!-- Repeat for each story (M = 1, 2, 3...) within epic N -->

### Story {{N}}.{{M}}: {{story_title_N_M}}

As a {{user_type}},
I want {{capability}},
So that {{value_benefit}}.

**Acceptance Criteria:**

<!-- for each AC on this story -->

**Given** {{precondition}}
**When** {{action}}
**Then** {{expected_outcome}}
**And** {{additional_criteria}}

<!-- End story repeat -->
