---
stepsCompleted:
  - step-01-validate-prerequisites
  - step-02-design-epics
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

FR1: Epic 1 - Partner Integration & Payment Intake Hardening
FR2: Epic 1 - Partner Integration & Payment Intake Hardening
FR3: Epic 1 - Partner Integration & Payment Intake Hardening
FR4: Epic 3 - Trustworthy Deposit Settlement & Ledger Balances
FR5: Epic 1 - Partner Integration & Payment Intake Hardening
FR6: Epic 1 - Partner Integration & Payment Intake Hardening
FR7: Epic 1 - Partner Integration & Payment Intake Hardening
FR8: Epic 1 - Partner Integration & Payment Intake Hardening
FR9: Epic 1 - Partner Integration & Payment Intake Hardening
FR10: Epic 1 - Partner Integration & Payment Intake Hardening
FR11: Epic 3 - Trustworthy Deposit Settlement & Ledger Balances
FR12: Epic 3 - Trustworthy Deposit Settlement & Ledger Balances
FR13: Epic 3 - Trustworthy Deposit Settlement & Ledger Balances
FR14: Epic 3 - Trustworthy Deposit Settlement & Ledger Balances
FR15: Epic 3 - Trustworthy Deposit Settlement & Ledger Balances
FR16: Epic 3 - Trustworthy Deposit Settlement & Ledger Balances
FR17: Epic 3 - Trustworthy Deposit Settlement & Ledger Balances
FR18: Epic 3 - Trustworthy Deposit Settlement & Ledger Balances
FR19: Epic 4 - Safe Outbound Funds & Custody Controls
FR20: Epic 4 - Safe Outbound Funds & Custody Controls
FR21: Epic 4 - Safe Outbound Funds & Custody Controls
FR22: Epic 4 - Safe Outbound Funds & Custody Controls
FR23: Epic 4 - Safe Outbound Funds & Custody Controls
FR24: Epic 4 - Safe Outbound Funds & Custody Controls
FR25: Epic 4 - Safe Outbound Funds & Custody Controls
FR26: Epic 2 - Reliable Money Event Delivery
FR27: Epic 2 - Reliable Money Event Delivery
FR28: Epic 2 - Reliable Money Event Delivery
FR29: Epic 2 - Reliable Money Event Delivery
FR30: Epic 2 - Reliable Money Event Delivery
FR31: Epic 2 - Reliable Money Event Delivery
FR32: Epic 3 - Trustworthy Deposit Settlement & Ledger Balances
FR33: Epic 3 - Trustworthy Deposit Settlement & Ledger Balances
FR34: Epic 3 - Trustworthy Deposit Settlement & Ledger Balances
FR35: Epic 4 - Safe Outbound Funds & Custody Controls
FR36: Epic 5 - Production Operations & Scale Readiness
FR37: Epic 5 - Production Operations & Scale Readiness
FR38: Epic 5 - Production Operations & Scale Readiness
FR39: Epic 4 - Safe Outbound Funds & Custody Controls
FR40: Epic 1 - Partner Integration & Payment Intake Hardening

## Epic List

### Epic 1: Partner Integration & Payment Intake Hardening

Merchant ve exchange partnerleri güvenli şekilde API kullanabilir, payment session veya static wallet oluşturabilir, hosted checkout ile ödeme alabilir ve integration contract'ına güvenebilir.

**FRs covered:** FR1, FR2, FR3, FR5, FR6, FR7, FR8, FR9, FR10, FR40

### Epic 2: Reliable Money Event Delivery

Merchant ve exchange partnerleri payment, deposit, withdrawal, refund, sweep ve correction lifecycle event'lerini versioned, replay-safe ve geriye uyumlu webhook/outbox kontratlarıyla alabilir.

**FRs covered:** FR26, FR27, FR28, FR29, FR30, FR31

### Epic 3: Trustworthy Deposit Settlement & Ledger Balances

Merchant, exchange ve operatörler deposit finality, payment settlement, ledger-derived balances, reorg correction ve reconciliation sayesinde bakiye ve ödeme durumuna güvenebilir.

**FRs covered:** FR4, FR11, FR12, FR13, FR14, FR15, FR16, FR17, FR18, FR32, FR33, FR34

### Epic 4: Safe Outbound Funds & Custody Controls

Operatörler withdrawal, payout, refund ve sweep işlemlerini reservation, approval, signer boundary, nonce/UTXO/resource policy ve audit kontrolleriyle güvenli şekilde yürütebilir.

**FRs covered:** FR19, FR20, FR21, FR22, FR23, FR24, FR25, FR35, FR39

### Epic 5: Production Operations & Scale Readiness

Operatörler sistemi production'da izleyebilir, provider sorunlarını yakalayabilir, migration/observability/runbook disiplinini işletebilir ve exchange-grade ölçeğe hazırlanabilir.

**FRs covered:** FR36, FR37, FR38

## Epic 1: Partner Integration & Payment Intake Hardening

Merchant ve exchange partnerleri güvenli şekilde API kullanabilir, payment session veya static wallet oluşturabilir, hosted checkout ile ödeme alabilir ve integration contract'ına güvenebilir.

### Story 1.1: Secure Partner API Request Authentication

As a developer integrator,
I want partner API requests to be authenticated, scoped, and replay-resistant,
So that only authorized merchant or exchange tenants can perform actions against their own resources.

**Acceptance Criteria:**

**Given** a partner sends a request with a valid API key or bearer token
**When** the request reaches a protected v1 API endpoint
**Then** the system resolves the correct tenant/domain scope
**And** the request cannot access resources outside that scope.

**Given** a partner sends a mutating request
**When** `X-API-Secret`, timestamp, and `X-Gateway-Signature` are present and valid
**Then** the request is accepted for downstream handling
**And** the signature verification uses the exact request method, path, timestamp, and body payload.

**Given** a mutating request has a missing, malformed, expired, or future-skewed timestamp
**When** the authentication middleware validates the request
**Then** the system rejects it with a backwards-compatible error envelope
**And** the allowed clock-skew window is covered by tests.

**Given** a previously accepted signed request is replayed with the same signature and timestamp
**When** replay protection evaluates the request
**Then** the duplicate request is rejected or safely treated as non-mutating according to endpoint policy
**And** signature reuse behavior is covered by automated tests.

**Given** an API key belongs to one tenant/domain
**When** it is used to request another tenant/domain's payment, wallet, payout, refund, or webhook resource
**Then** the system returns an authorization failure
**And** the failure does not leak whether the target resource exists.

**Given** an authentication or authorization failure occurs
**When** the system logs the failure
**Then** logs include tenant/domain context when known, endpoint, failure reason category, and request correlation id
**And** logs do not include API secrets, raw signatures, private keys, mnemonics, or full sensitive payloads.

**Given** the authentication changes are implemented
**When** the test suite runs
**Then** it includes positive and negative tests for API key auth, bearer auth, HMAC validation, timestamp skew, replay/signature reuse, tenant scope isolation, and backwards-compatible error responses.

### Story 1.2: Idempotent Payment Session Creation

As a developer integrator,
I want to create payment sessions idempotently with a stable checkout URL and quote snapshot,
So that checkout creation is safe to retry and produces a predictable payment contract for the merchant.

**Acceptance Criteria:**

**Given** a merchant or exchange tenant sends a valid authenticated payment session creation request
**When** the request includes supported chain/token, amount, currency, callback metadata, and an idempotency key
**Then** the system creates a payment session scoped to the tenant/domain
**And** returns a stable session id, checkout URL, expiry, selected asset information, expected raw amount, and deposit address reference.

**Given** the payment session requires pricing conversion
**When** the quote is calculated
**Then** the system stores a quote snapshot with price, currency, decimals, symbol/token metadata, and timestamp
**And** later display or settlement logic does not recalculate the original fiat amount from current oracle prices.

**Given** the same idempotency key and same request payload are submitted again
**When** the create endpoint handles the retry
**Then** the system returns the original payment session response
**And** does not create a duplicate session, duplicate wallet assignment, or duplicate downstream lifecycle state.

**Given** the same idempotency key is reused with a different request payload
**When** the create endpoint validates idempotency
**Then** the system rejects the request with a conflict response
**And** the response uses the backwards-compatible error envelope.

**Given** the selected chain/token is unsupported, disabled, or missing required token metadata
**When** the create endpoint validates the request
**Then** the system rejects the request before creating a payment session
**And** records no partial payment session or wallet assignment.

**Given** a session expiry is reached before settlement
**When** the payment session status is queried through the partner API or checkout status surface
**Then** the session reports an expired state consistently
**And** expiry behavior is covered by automated tests.

**Given** the payment session creation path is implemented
**When** contract and integration tests run
**Then** they cover successful creation, idempotent retry, idempotency conflict, unsupported asset rejection, quote snapshot persistence, expiry behavior, and response schema compatibility.

### Story 1.3: Deterministic Static Wallet Issuance for Partner Scopes

As a developer integrator,
I want static deposit wallets to be issued deterministically for a dealer/merchant tenant, domain, product, and user scope,
So that merchant and exchange integrations can safely request the same deposit wallet without duplicate address ownership.

**Acceptance Criteria:**

**Given** an authenticated partner or dealer portal action requests a static wallet for a dealer/merchant tenant, domain, product, and user scope
**When** no wallet exists for that exact scope
**Then** the system derives and stores a new wallet/address set for the supported chain family
**And** the wallet is owned by the requesting tenant/domain scope.

**Given** the same dealer/merchant tenant, domain, product, and user scope requests a wallet again
**When** a wallet already exists
**Then** the system returns the existing wallet/address set
**And** does not increment HD index, create duplicate ownership, or create conflicting address records.

**Given** two concurrent requests target the same wallet scope
**When** wallet issuance runs
**Then** only one wallet/address set is created
**And** concurrency behavior is protected by transaction, lock, or uniqueness guarantees covered by tests.

**Given** wallet derivation is performed for a supported chain
**When** the address is generated
**Then** the system uses the Trust Wallet Core derivation provider where supported by the current architecture
**And** records enough chain/address metadata for later deposit matching.

**Given** Trust Wallet Core derivation is unavailable or fallback provider would be used in production
**When** the wallet issuance path executes
**Then** the system fails safely before returning an address
**And** fallback behavior cannot silently generate invalid or placeholder production wallets.

**Given** a partner requests a wallet for a chain/token that is disabled or unsupported
**When** validation runs
**Then** the system rejects the request with a backwards-compatible error envelope
**And** no partial wallet scope, HD index mutation, or address ownership record is committed.

**Given** the wallet issuance story is implemented
**When** automated tests run
**Then** they cover first issuance, idempotent repeat issuance, concurrent issuance, unsupported chain rejection, Trust Wallet Core provider usage, fallback production guard, dealer/merchant portal scope, and tenant scope isolation.

### Story 1.4: Hosted Checkout Payment State Experience

As a payer,
I want the hosted checkout page to clearly show payment instructions and state changes,
So that I can complete a crypto payment without confusion and understand whether it is pending, paid, expired, failed, or underpaid.

**Acceptance Criteria:**

**Given** a payer opens a valid checkout URL
**When** the payment session is active
**Then** the checkout displays selected asset, chain/network, expected amount, deposit address, QR code, and expiry information
**And** the displayed amount/address matches the payment session contract.

**Given** a payer views checkout on desktop or mobile
**When** the page renders
**Then** the payment instructions, QR code, address copy action, amount, and status are usable without layout overlap
**And** mobile rendering is covered by regression or view tests where feasible.

**Given** a payment session is still waiting for chain detection or finality
**When** the checkout status refreshes through polling, websocket, or existing status mechanism
**Then** the checkout shows a pending or confirming state
**And** does not show paid before required settlement/finality state is reached.

**Given** the payment session succeeds
**When** checkout status is refreshed
**Then** the checkout shows a paid/succeeded state
**And** the final state remains stable across page refresh.

**Given** the payment session expires before settlement
**When** the payer opens or refreshes checkout
**Then** the checkout shows an expired state
**And** prevents the payer from interpreting the invoice as still payable.

**Given** the detected payment amount is below the expected amount or otherwise fails matching policy
**When** checkout status is refreshed
**Then** the checkout shows an underpaid or failed state according to product policy
**And** the state is distinct from pending and paid.

**Given** checkout state rendering is implemented
**When** automated tests run
**Then** they cover active, pending/confirming, paid, expired, failed, and underpaid states
**And** tests verify the checkout does not expose secrets or internal-only diagnostic data.

### Story 1.5: Stable Partner API Contract and Integration Evidence

As a developer integrator,
I want the partner API contract and integration examples to stay stable and test-backed,
So that merchant, dealer, and exchange integrations can upgrade safely without guessing response formats or error behavior.

**Acceptance Criteria:**

**Given** partner-facing payment session, static wallet, checkout status, and authentication endpoints exist
**When** OpenAPI or equivalent API documentation is generated or updated
**Then** the documented request/response schemas match the implemented handlers
**And** required auth, idempotency, error, and tenant/domain scope fields are documented.

**Given** a partner receives an error response from authentication, validation, idempotency conflict, unsupported asset, expired session, or authorization failure
**When** the response is serialized
**Then** it follows a backwards-compatible error envelope
**And** tests verify no sensitive implementation details or resource-existence leaks are exposed.

**Given** existing merchant/dealer integrations use current API fields
**When** the contract is updated for Epic 1 changes
**Then** existing compatible fields remain available or are explicitly marked as deprecated
**And** no breaking change is introduced without a documented migration note.

**Given** a developer follows the integration guide
**When** they create a payment session, retry with the same idempotency key, request a static wallet, and open checkout status
**Then** the documented examples produce behavior consistent with automated contract tests
**And** examples include at least one success path and one failure/conflict path.

**Given** partner API contract tests run in CI or local verification
**When** handlers, schemas, or response envelopes change
**Then** tests fail if implemented responses drift from the documented contract
**And** the verification includes `go test ./...` or the repo's equivalent targeted test command for the affected packages.

**Given** Epic 1 is complete
**When** a developer reviews the integration evidence
**Then** they can identify covered endpoints, supported authentication modes, idempotency behavior, static wallet scope rules, checkout state semantics, and known production limitations.
