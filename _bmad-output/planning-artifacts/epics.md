---
stepsCompleted:
  - step-01-validate-prerequisites
  - step-02-design-epics
  - step-03-create-stories
  - step-04-final-validation
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

## Epic 2: Reliable Money Event Delivery

Merchant ve exchange partnerleri payment, deposit, withdrawal, refund, sweep ve correction lifecycle event'lerini versioned, replay-safe ve geriye uyumlu webhook/outbox kontratlarıyla alabilir.

### Story 2.1: Define Versioned Money Event Catalog

As a developer integrator,
I want all money lifecycle events to have a documented versioned event catalog,
So that merchant and exchange consumers can handle payment, deposit, withdrawal, refund, sweep, and correction events consistently.

**Acceptance Criteria:**

**Given** the system emits money lifecycle events
**When** the event catalog is defined
**Then** it includes documented event names, event versions, payload fields, required identifiers, timestamps, tenant/domain scope, and lifecycle semantics
**And** it covers payment, deposit, withdrawal, refund, sweep, webhook delivery, and correction/reorg event families.

**Given** new money event contracts are defined
**When** event names are introduced
**Then** they use dotted, versioned names including `deposit.detected.v1`, `deposit.finalized.v1`, `payment.succeeded.v1`, `payment.failed.v1`, `payment.expired.v1`, `withdrawal.requested.v1`, `withdrawal.broadcast.v1`, `withdrawal.finalized.v1`, `withdrawal.failed.v1`, `refund.succeeded.v1`, `sweep.succeeded.v1`, and `transaction.reorged.v1`
**And** each event name has a clear producer, consumer, and terminal/non-terminal lifecycle meaning.

**Given** existing underscore-style webhook events are still used by current integrations
**When** the event catalog is published
**Then** each supported legacy event is mapped to its dotted/versioned equivalent or explicitly marked as legacy-only
**And** no existing event name is removed without a deprecation note.

**Given** event payload schemas are defined
**When** a consumer receives an event
**Then** every payload includes a stable event id, event type, event version, tenant/domain id where applicable, occurred-at timestamp, resource id, resource status, and idempotency/correlation metadata
**And** sensitive values such as API secrets, private keys, mnemonics, raw signatures, and internal-only diagnostics are excluded.

**Given** correction or reorg events are defined
**When** a previously emitted lifecycle state must be corrected
**Then** the catalog explains how correction events relate to the original event id and resource state
**And** correction semantics do not require destructive edits to prior event history.

**Given** the event catalog is implemented or documented
**When** automated checks run
**Then** they verify every configured event constant or emitted event type is present in the catalog
**And** catalog examples are valid against the declared payload schemas.

### Story 2.2: Persist Money Events Through Postgres Outbox

As a platform operator,
I want money lifecycle events to be persisted through a Postgres outbox in the same transaction as state changes,
So that event delivery is durable, replayable, and not lost when the process crashes.

**Acceptance Criteria:**

**Given** a money-affecting state change occurs
**When** the owning boundary commits the state change
**Then** the corresponding versioned event is inserted into the Postgres outbox in the same database transaction
**And** no in-memory dispatcher is the only record of the event.

**Given** an outbox event is inserted
**When** the event record is persisted
**Then** it stores stable event id, event type, event version, aggregate/resource id, tenant/domain scope, idempotency key, payload, status, attempt count, and created timestamp
**And** database uniqueness prevents duplicate records for the same idempotent lifecycle transition.

**Given** a transaction fails after state validation but before commit
**When** the operation is rolled back
**Then** neither the state change nor the outbox event is visible
**And** rollback behavior is covered by tests.

**Given** an event has already been recorded for a lifecycle transition
**When** the same transition is retried
**Then** the system reuses or safely no-ops the existing event record
**And** does not create duplicate downstream delivery obligations.

**Given** outbox schema or indexes are introduced
**When** the story is implemented
**Then** it includes versioned migration artifacts or an explicit migration plan
**And** startup `AutoMigrate` is not the only production database change mechanism.

**Given** outbox persistence is implemented
**When** automated tests run
**Then** they cover same-transaction persistence, rollback, duplicate lifecycle retries, uniqueness constraints, and payload schema validation.

### Story 2.3: Deliver Webhooks from the Webhook Boundary

As a merchant or exchange integrator,
I want webhook delivery to be handled by a dedicated boundary with signing and retries,
So that money flows do not block on external callbacks and consumers receive verifiable notifications.

**Acceptance Criteria:**

**Given** an outbox event requires partner notification
**When** the webhook boundary claims the event for delivery
**Then** source payment, deposit, withdrawal, refund, and sweep flows are not responsible for inline HTTP delivery
**And** the event remains replayable if delivery fails.

**Given** a webhook attempt is sent
**When** the outbound request is built
**Then** it includes HMAC signature, event id, event type, event version, timestamp, and tenant/domain-scoped delivery metadata
**And** callback URL validation runs at delivery time.

**Given** a callback endpoint returns a transient failure or timeout
**When** the webhook boundary records the attempt
**Then** it increments attempt count, stores failure category, schedules exponential backoff, and keeps the event eligible for retry until max attempts
**And** retry scheduling is covered by tests.

**Given** a callback endpoint returns a terminal success response
**When** the webhook boundary records the attempt
**Then** the delivery is marked delivered with final attempt metadata
**And** the event is not retried again unless an explicit operator replay is requested.

**Given** a webhook payload is generated
**When** it is logged or displayed to operators
**Then** secrets, private keys, mnemonics, raw signatures, and internal-only diagnostic payloads are redacted or excluded
**And** logs include correlation id, tenant/domain, event id, and failure category.

**Given** webhook delivery is implemented
**When** automated tests run
**Then** they cover successful delivery, transient failure, timeout, backoff scheduling, callback URL validation, HMAC headers, and redaction.

### Story 2.4: Support Replay, Dead-Letter, and Duplicate Delivery Safety

As an operator,
I want failed or uncertain webhook deliveries to be replayable and duplicate-safe,
So that partner notifications can be recovered without causing duplicate fulfillment or silent data loss.

**Acceptance Criteria:**

**Given** a webhook delivery reaches maximum attempts without success
**When** retry policy is exhausted
**Then** the delivery is marked dead-letter or equivalent terminal failure
**And** the reason, last error, attempt count, and next operator action are visible.

**Given** an operator replays a webhook delivery
**When** replay is requested for an event
**Then** the system creates a replay attempt tied to the original event id
**And** the payload remains idempotent for consumers by preserving stable event id and event type metadata.

**Given** a consumer receives the same event more than once
**When** duplicate delivery occurs through retry or replay
**Then** the event contract provides enough idempotency metadata for consumers to deduplicate
**And** duplicate delivery behavior is documented and tested.

**Given** a replay is requested for a resource outside the operator's tenant or permission scope
**When** authorization is evaluated
**Then** the replay is rejected without leaking resource existence
**And** the denial is audit logged.

**Given** replay and dead-letter features are implemented
**When** automated tests run
**Then** they cover retry exhaustion, replay success, duplicate replay attempts, authorization failure, and operator-visible delivery state.

### Story 2.5: Verify Event Contract Compatibility

As a developer integrator,
I want event schemas and compatibility aliases to be test-backed,
So that consumers can upgrade from current webhook names to versioned money events without breakage.

**Acceptance Criteria:**

**Given** current underscore-style events exist
**When** the versioned event contract is introduced
**Then** compatibility aliases continue to emit or translate supported legacy names
**And** each alias has a documented migration path to its dotted/versioned event.

**Given** event payload schemas are documented
**When** schema examples are generated or checked
**Then** examples validate against the declared schema
**And** required fields are present for each event family.

**Given** a breaking event payload change is proposed
**When** compatibility tests run
**Then** tests fail unless a new event version or documented migration note is provided
**And** existing `v1` consumers are not silently broken.

**Given** event contract tests run
**When** handlers, outbox producers, or webhook payload builders change
**Then** tests verify event type, event version, payload shape, legacy aliases, and sensitive field exclusion
**And** failures point to the mismatched event contract.

**Given** Epic 2 is complete
**When** a developer reviews integration evidence
**Then** they can see the event catalog, outbox persistence rules, delivery/replay semantics, dead-letter behavior, and compatibility test coverage.

## Epic 3: Trustworthy Deposit Settlement & Ledger Balances

Merchant, exchange ve operatörler deposit finality, payment settlement, ledger-derived balances, reorg correction ve reconciliation sayesinde bakiye ve ödeme durumuna güvenebilir.

### Story 3.1: Emit Chain Facts Without Mutating Business State

As a platform operator,
I want chain indexers to produce durable chain facts instead of directly mutating payment or ledger state,
So that deposit processing, settlement, and reconciliation are deterministic and replayable.

**Acceptance Criteria:**

**Given** an EVM, Bitcoin, Solana, or TRON listener detects a transaction or log
**When** the chain indexer processes it
**Then** it emits a stable chain fact event with chain id, block/slot height, block hash where available, tx hash, log index or equivalent, observed address, asset metadata, amount, and finality metadata
**And** it does not directly mark payments paid or post ledger entries.

**Given** the listener starts from a configured block or slot
**When** explicit start configuration is present
**Then** it begins from that configured point and records progress safely
**And** default safe/latest behavior is documented as not equivalent to full historical backfill.

**Given** a chain fact is seen more than once
**When** the same chain id, tx hash, and log index or equivalent identifier is processed
**Then** the fact is deduplicated by stable event id
**And** downstream consumers receive idempotent input.

**Given** indexer processing fails after a fact is observed
**When** the process restarts
**Then** progress and fact persistence prevent silent loss or duplicate business mutation
**And** crash/retry behavior is covered by tests.

**Given** chain fact emission is implemented
**When** automated tests run
**Then** they cover supported chain families, duplicate tx/log detection, configured start block, progress persistence, and no direct business state mutation.

### Story 3.2: Match Deposits and Gate Settlement on Finality

As a merchant or exchange operator,
I want detected deposits to be matched to owned wallets and gated by finality,
So that payment and wallet balances are not credited before the chain event is sufficiently reliable.

**Acceptance Criteria:**

**Given** a chain fact references an address owned by a wallet
**When** the deposit boundary consumes the fact
**Then** it creates or updates a deposit lifecycle record scoped to wallet, tenant/domain, chain, asset, amount, and tx metadata
**And** the deposit event is idempotent for repeated chain facts.

**Given** a chain fact does not match an owned address
**When** the deposit boundary processes it
**Then** no payment session or ledger mutation occurs
**And** the unmatched fact can be observed or reconciled without leaking tenant data.

**Given** a matched deposit has not reached required confirmations/finality
**When** payment or balance state is evaluated
**Then** the system reports pending/confirming state
**And** does not mark payment succeeded or available balance credited as finalized.

**Given** a matched deposit reaches chain-specific finality
**When** the deposit boundary finalizes it
**Then** it emits `deposit.finalized.v1` or equivalent configured event
**And** it provides deterministic input for ledger posting and payment matching.

**Given** finality behavior is implemented
**When** automated tests run
**Then** they cover matched deposit, unmatched fact, duplicate fact, pending finality, finalized deposit, and chain-specific confirmation thresholds.

### Story 3.3: Enforce Ledger-Derived Balance Authority

As a merchant or exchange tenant,
I want balances to be derived from ledger entries only,
So that available, pending, hold, transit, and adjustment balances are consistent across deposits, withdrawals, refunds, sweeps, and reconciliation.

**Acceptance Criteria:**

**Given** a balance API or dealer/admin balance view is requested
**When** the system computes balances
**Then** it reads from ledger entries or ledger projections
**And** it does not sum transaction rows or poll chain balances inline as authoritative balance.

**Given** a finalized deposit is posted to the ledger
**When** the ledger boundary records the movement
**Then** it creates balanced debit/credit entries with stable idempotency key, asset metadata, wallet/tenant scope, and lifecycle reference
**And** duplicate posting attempts are rejected or no-op safely.

**Given** ledger schema or projection changes are introduced
**When** the story is implemented
**Then** it includes versioned migration artifacts or an explicit migration plan
**And** DB-level constraints protect status values, unique idempotency keys, and balanced ledger movement where feasible.

**Given** ledger-derived balances are implemented
**When** automated tests run
**Then** they cover pending, available, hold, transit, reversal, adjustment, duplicate idempotency key, negative balance guard, and dealer/admin view compatibility.

**Given** a ledger invariant violation is detected
**When** balance checks or invariant jobs run
**Then** the system opens a scoped reconciliation job
**And** logs include correlation id and tenant/domain context without exposing sensitive values.

### Story 3.4: Model Payment Matching Outcomes Explicitly

As a merchant,
I want payment matching outcomes to distinguish paid, expired, underpaid, overpaid, and partial-paid states,
So that checkout, API, and webhook consumers do not confuse uncertain payment states with successful settlement.

**Acceptance Criteria:**

**Given** a finalized deposit matches a payment session exactly according to asset, chain, address, and amount policy
**When** payment matching runs
**Then** the session transitions to paid/succeeded
**And** the transition emits the configured payment lifecycle event.

**Given** a deposit amount is below expected amount or below configured tolerance
**When** payment matching runs
**Then** the session is marked underpaid, failed, or policy-defined non-terminal state
**And** the checkout/API state is distinct from pending and paid.

**Given** a deposit amount exceeds expected amount
**When** payment matching runs
**Then** the system records overpayment according to policy
**And** operator or refund/reconciliation follow-up is discoverable.

**Given** multiple deposits may partially satisfy one payment session
**When** partial matching is supported or intentionally unsupported
**Then** the behavior is explicit in status, event, and integration documentation
**And** tests cover the chosen policy.

**Given** payment matching changes are implemented
**When** automated tests run
**Then** they cover exact match, wrong asset, wrong chain, underpaid, overpaid, partial payment policy, expiry interaction, and idempotent repeated matching.

### Story 3.5: Correct Reorgs with Compensating Events and Ledger Reversals

As an operator,
I want chain reorgs to correct affected deposits, payments, ledger entries, sweeps, and webhooks without destructive history edits,
So that the system can recover from canonical chain changes while preserving auditability.

**Acceptance Criteria:**

**Given** a block or slot at a processed height changes canonical hash or parent relationship
**When** the chain indexer detects the conflict
**Then** affected chain facts and transactions are marked reorged or superseded
**And** a `transaction.reorged.v1` or equivalent correction event is emitted.

**Given** a reorg affects a ledger-posted deposit or payment
**When** correction processing runs
**Then** the ledger records compensating reversal entries
**And** previously posted money history is not destructively edited.

**Given** a reorg affects a paid payment session
**When** correction processing evaluates the payment
**Then** the payment lifecycle is corrected to failed, reverted, or policy-defined state
**And** a correction webhook/event is queued with reference to the original event id.

**Given** a reorg affects a pending sweep or downstream outbound action
**When** correction processing runs
**Then** related sweep or outbound jobs are blocked, dead-lettered, or routed to reconciliation before retry
**And** blind retry does not occur.

**Given** reorg correction is implemented
**When** automated tests run
**Then** they include deterministic fork simulation, duplicate reorg event handling, stale checkout/payment state, correction webhook, ledger reversal, and reconciliation job creation.

### Story 3.6: Open Scoped Reconciliation Jobs for Drift and Uncertainty

As an operator,
I want uncertain or drifting money states to open scoped reconciliation jobs,
So that chain facts, ledger entries, lifecycle state, webhook delivery, and broadcast state can be compared before recovery action.

**Acceptance Criteria:**

**Given** the system detects ledger invariant failure, chain/payment mismatch, stale finality, webhook correction failure, or stuck lifecycle state
**When** uncertainty is classified
**Then** it opens a reconciliation job with bounded scope, reason, affected resource ids, tenant/domain context, and current status
**And** duplicate jobs for the same active issue are deduplicated.

**Given** a reconciliation job runs
**When** it gathers evidence
**Then** it compares chain facts, ledger entries, payment/deposit lifecycle state, webhook delivery state, and outbound broadcast state where applicable
**And** it records the evidence used for resolution.

**Given** a reconciliation outcome is determined
**When** the job completes
**Then** it records resolved, needs-operator-action, retry-scheduled, or failed state
**And** any money-affecting correction is emitted as a compensating event.

**Given** reconciliation features are implemented
**When** automated tests run
**Then** they cover invariant drift, duplicate job dedupe, reorg-created reconciliation, webhook drift, stuck lifecycle state, and operator-visible status.

## Epic 4: Safe Outbound Funds & Custody Controls

Operatörler withdrawal, payout, refund ve sweep işlemlerini reservation, approval, signer boundary, nonce/UTXO/resource policy ve audit kontrolleriyle güvenli şekilde yürütebilir.

### Story 4.1: Reserve Ledger Holds Before Outbound Money Movement

As an operator,
I want withdrawal, payout, refund, and sweep requests to reserve funds before signing,
So that outbound money movement cannot overdraw balances or double-spend funds.

**Acceptance Criteria:**

**Given** an outbound withdrawal, payout, refund, or sweep is requested
**When** the request is validated
**Then** the system checks ledger-derived available balance and creates a hold/reservation before signing or broadcast
**And** requests without successful hold cannot proceed.

**Given** two outbound requests compete for the same available balance
**When** reservation runs concurrently
**Then** only reservations backed by available ledger balance succeed
**And** negative balances are prevented by transaction, lock, or DB constraint behavior covered by tests.

**Given** an outbound request is rejected or fails before broadcast
**When** failure is terminal
**Then** the hold is released or reversed through ledger entries
**And** the release is idempotent for repeated failure handling.

**Given** hold/reservation schema changes are required
**When** the story is implemented
**Then** it includes versioned migration artifacts or an explicit migration plan
**And** ledger invariant tests include hold/release paths.

**Given** reservation logic is implemented
**When** automated tests run
**Then** they cover successful hold, insufficient funds, concurrent requests, duplicate request idempotency, failure release, and audit logging.

### Story 4.2: Enforce External Signer Boundary and Production Software-Signer Guard

As a custody operator,
I want outbound signing to go through a signer boundary with production software signing blocked,
So that private keys and mnemonics do not leave the approved custody layer.

**Acceptance Criteria:**

**Given** an outbound transaction intent is ready for signing
**When** the application requests a signature
**Then** it sends key reference, chain, derivation/account context, transaction intent, amount, destination, and policy metadata to the signer boundary
**And** it does not expose private keys, mnemonics, or raw seed material to application callers.

**Given** the environment is production
**When** `SIGNER_MODE=software` or equivalent development signer is selected
**Then** signing hard-fails before transaction construction or broadcast
**And** the failure is audit logged as a production custody gate.

**Given** KMS, HSM, MPC, Vault, or external custody signing is not yet configured
**When** production outbound signing is requested
**Then** the system returns an explicit integration-required failure
**And** does not fall back to process-memory mnemonic signing.

**Given** a signing request is completed or rejected
**When** audit logs are written
**Then** logs include signer mode, key reference, actor or job id, policy decision, request correlation id, and outcome
**And** logs exclude secret material and raw signatures unless explicitly safe for audit storage.

**Given** signer boundary enforcement is implemented
**When** automated tests run
**Then** they cover development software signing allowance, production hard-fail, missing external signer, audit logging, and secret redaction.

### Story 4.3: Reserve Chain Resources and Apply Fee/Gas Policy Before Broadcast

As an operator,
I want nonce, UTXO, resource, and fee/gas policy to be reserved before outbound broadcast,
So that concurrent payouts, refunds, and sweeps do not conflict or get stuck through blind retries.

**Acceptance Criteria:**

**Given** an account-based chain outbound transaction is prepared
**When** signing is requested
**Then** the system reserves nonce or equivalent chain sequence ownership before signing
**And** concurrent outbound jobs for the same wallet cannot reuse the same nonce.

**Given** a Bitcoin-like outbound transaction is prepared
**When** coin selection runs
**Then** selected UTXOs are reserved before signing
**And** concurrent jobs cannot spend the same UTXO set.

**Given** Solana, TRON, EVM, or token-specific fees/resources are required
**When** the outbound transaction is built
**Then** the system applies chain-specific fee/gas/resource policy instead of fixed unsafe defaults where possible
**And** missing resources route to policy failure, prefund, or operator action instead of blind broadcast.

**Given** a broadcast is stuck or replacement is needed
**When** retry logic runs
**Then** it checks existing broadcast state and reconciliation evidence before replacement
**And** it does not create a second unrelated spend for the same reserved funds.

**Given** chain resource reservation is implemented
**When** automated tests run
**Then** they cover nonce contention, UTXO contention, resource/gas policy failure, stuck tx replacement guard, and reservation release on terminal failure.

### Story 4.4: Run Auto-Sweeps as Durable Recoverable Jobs

As an operator,
I want auto-sweeps to run as durable jobs with retry and recovery state,
So that finalized deposits can be consolidated without losing work or duplicating broadcasts.

**Acceptance Criteria:**

**Given** a deposit becomes finalized and sweep-eligible
**When** sweep scheduling runs
**Then** the system creates an idempotent persistent sweep job
**And** repeated scheduling for the same deposit does not create duplicate sweep obligations.

**Given** a sweep worker claims work
**When** multiple workers or retries are active
**Then** a job is claimed with lock semantics such as `FOR UPDATE SKIP LOCKED` or equivalent
**And** the same job is not processed concurrently.

**Given** a sweep attempt fails transiently
**When** the worker records failure
**Then** retry count, next attempt time, failure category, and exponential backoff are stored
**And** the job remains recoverable.

**Given** a sweep reaches maximum attempts or an unrecoverable policy failure
**When** failure is terminal
**Then** the job is marked dead-letter or needs-operator-action
**And** related holds/resources are released or reconciled safely.

**Given** gas prefund or resource funding is needed
**When** the sweep job evaluates prerequisites
**Then** prefund work is idempotent and linked to the parent sweep job
**And** per-wallet concurrency policy prevents conflicting prefund/sweep actions.

**Given** durable sweep jobs are implemented
**When** automated tests run
**Then** they cover idempotent scheduling, worker claim locking, retry/backoff, dead-letter, tx hash persistence, prefund idempotency, and recovery after process restart.

### Story 4.5: Complete Withdrawal, Payout, and Refund Lifecycle Events

As a merchant or exchange operator,
I want outbound withdrawal, payout, and refund lifecycles to have clear approval, broadcast, finalization, failure, and notification states,
So that users and operators can understand where funds are and recover safely when something fails.

**Acceptance Criteria:**

**Given** a withdrawal, payout, or refund request is created
**When** validation passes
**Then** the request records requester, tenant/domain, source wallet, destination, asset, amount, status, and idempotency/correlation metadata
**And** it emits or schedules the appropriate requested lifecycle event.

**Given** the request requires approval
**When** an admin or authorized operator approves or rejects it
**Then** the action is scoped, audit logged, and reflected in request status
**And** maker-checker separation is enforced where configured.

**Given** an approved outbound request is broadcast
**When** broadcast succeeds
**Then** the request records tx hash, chain, broadcast timestamp, and broadcast event
**And** downstream finality tracking can update terminal state.

**Given** finality confirms the outbound transaction
**When** terminal processing runs
**Then** the ledger finalizes debit or releases hold according to outcome
**And** `withdrawal.finalized.v1`, `withdrawal.failed.v1`, `refund.succeeded.v1`, or equivalent event is queued.

**Given** lifecycle implementation is complete
**When** automated tests run
**Then** they cover request validation, approval, rejection, broadcast success, broadcast failure, finalization, hold release, webhook/event enqueue, and idempotent repeated processing.

### Story 4.6: Harden Admin Security, Limits, Whitelists, and Audit Trails

As a platform owner,
I want outbound money operations protected by admin security controls and immutable audit trails,
So that high-risk actions can be reviewed, limited, and stopped before funds leave custody.

**Acceptance Criteria:**

**Given** an admin or dealer portal action can affect outbound money movement
**When** the action is submitted
**Then** CSRF/session protections, actor authorization, and tenant/domain scope checks run before state changes
**And** failures do not leak sensitive resource existence.

**Given** a withdrawal or payout destination is submitted
**When** policy validation runs
**Then** address whitelist, velocity limit, per-tenant limit, and emergency freeze policy are enforced where configured
**And** policy failures are audit logged.

**Given** an approval decision is made
**When** audit logging occurs
**Then** it records actor, role, tenant/domain, subject id, decision, reason, timestamp, correlation id, and before/after status
**And** audit logs are immutable or append-only by design.

**Given** security hardening is implemented
**When** automated tests run
**Then** they cover CSRF enforcement, role separation, scope isolation, whitelist rejection, velocity limit rejection, emergency freeze, audit log creation, and sensitive data redaction.

## Epic 5: Production Operations & Scale Readiness

Operatörler sistemi production'da izleyebilir, provider sorunlarını yakalayabilir, migration/observability/runbook disiplinini işletebilir ve exchange-grade ölçeğe hazırlanabilir.

### Story 5.1: Track Provider Health and RPC Failover Signals

As an operator,
I want RPC/provider health, lag, and consistency to be measured,
So that stale nodes, missed blocks, and provider outages are visible before they cause missed deposits or stuck withdrawals.

**Acceptance Criteria:**

**Given** multiple providers or RPC URLs are configured for a chain
**When** health checks run
**Then** the system records provider reachability, latest height/slot, response latency, error rate, and stale-head indicators
**And** results are tagged by chain and provider.

**Given** providers disagree on canonical head or lag beyond threshold
**When** provider comparison runs
**Then** the system marks the provider unhealthy or degraded
**And** emits metrics/logs suitable for alerting.

**Given** failover strategy is configured
**When** a provider is unhealthy
**Then** chain operations use the configured fallback or degraded-mode policy
**And** fallback decisions are observable in logs/metrics.

**Given** provider health features are implemented
**When** automated tests run
**Then** they cover healthy provider, timeout, stale head, inconsistent head, failover selection, and metric emission.

### Story 5.2: Prepare Address Lookup for Large Wallet Sets

As an exchange or platform operator,
I want address lookup to be partitioned or normalized for large wallet sets,
So that deposit matching can scale beyond small merchant MVP volumes.

**Acceptance Criteria:**

**Given** wallet addresses exist across multiple chain families
**When** address lookup design is introduced
**Then** it supports normalized or partitioned lookup by chain, address, tenant/domain, wallet id, and asset where applicable
**And** it does not require loading every address into one unbounded in-memory index for production scale.

**Given** existing wallet data uses chain-specific address fields
**When** the new lookup structure is introduced
**Then** migration/backfill strategy preserves existing address ownership
**And** duplicate or conflicting address ownership is rejected.

**Given** deposit matching uses the lookup
**When** a chain fact is processed
**Then** lookup performance and indexes are suitable for large address sets
**And** lookup misses do not mutate business state.

**Given** address lookup readiness is implemented
**When** automated tests or benchmarks run
**Then** they cover migration/backfill, duplicate address guard, chain-specific lookup, miss handling, and a documented scale benchmark target.

### Story 5.3: Replace Production AutoMigrate Dependence with Versioned Migration Discipline

As a platform operator,
I want production database changes to use versioned migrations and release gates,
So that money tables, indexes, and constraints change predictably and can be reviewed or rolled back.

**Acceptance Criteria:**

**Given** a story changes production schema for money movement, outbox, ledger, wallet, reconciliation, or audit tables
**When** the change is implemented
**Then** it includes versioned migration artifacts or an explicit migration plan
**And** startup `AutoMigrate` is not the only production schema mechanism.

**Given** a migration is added
**When** migration verification runs
**Then** it checks forward application, rollback or recovery notes, lock impact, data backfill needs, and online safety
**And** risky migrations document operator steps.

**Given** DB constraints are added
**When** tests run
**Then** they verify status enums/checks, uniqueness, partial indexes, idempotency keys, and ledger/outbox invariants where applicable.

**Given** migration discipline is implemented
**When** release evidence is reviewed
**Then** operators can identify which migrations are required for each story and how to verify them before production traffic.

### Story 5.4: Provide Money-Path Observability, SLOs, Alerts, and Runbooks

As an operator,
I want money-path metrics, dashboards, alerts, and runbooks,
So that production incidents can be detected, triaged, and recovered before customer funds are at risk.

**Acceptance Criteria:**

**Given** money lifecycle jobs and APIs are running
**When** metrics are emitted
**Then** the system reports chain lag, provider health, outbox lag, webhook lag/failure rate, sweep backlog, withdrawal backlog, signer latency/failure, ledger drift, reconciliation backlog, and stuck lifecycle counts
**And** metrics are tagged by chain, asset, tenant/domain where safe, and outcome.

**Given** structured logs are emitted
**When** money-path actions occur
**Then** logs include correlation id, tenant/domain where known, resource id, lifecycle state, and failure category
**And** logs redact secrets, private keys, mnemonics, raw signatures, and sensitive payloads.

**Given** SLOs and alerts are configured
**When** lag, failure rate, stuck jobs, signer errors, or reconciliation drift crosses threshold
**Then** operators receive actionable alerts
**And** alert descriptions point to a runbook or diagnostic checklist.

**Given** runbooks are written
**When** an operator follows them
**Then** they cover webhook replay/dead-letter, stuck withdrawal, reorg correction, provider outage, ledger drift, signer outage, backup/restore drill, and emergency freeze.

**Given** observability features are implemented
**When** verification runs
**Then** it confirms metrics/logs exist for Epic 2-4 money paths and that at least one dashboard or documented query set can surface each critical SLO.

### Story 5.5: Produce Controlled Launch and Exchange-Grade Readiness Evidence

As a platform owner,
I want launch gates and scale-readiness evidence before real customer funds or exchange-grade usage,
So that the product is not marketed or operated beyond its proven safety envelope.

**Acceptance Criteria:**

**Given** the platform is considered for real customer funds
**When** launch readiness is reviewed
**Then** production gates require signer readiness, ledger/reconciliation evidence, durable eventing, webhook recovery, withdrawal policy, observability, migrations, and incident runbooks
**And** any missing gate is documented as a launch blocker or pilot limitation.

**Given** a controlled pilot or canary is planned
**When** launch limits are configured
**Then** the plan defines tenant count, chain/token set, maximum balances, withdrawal limits, monitoring owner, rollback criteria, and manual reconciliation cadence
**And** operators can verify those limits before onboarding.

**Given** exchange-grade scale is requested
**When** readiness is assessed
**Then** the evidence covers sharded indexer plan, address lookup partitioning, durable event bus path, provider/archive/quorum strategy, reorg simulation, and large wallet benchmark target
**And** unsupported scale claims are explicitly rejected.

**Given** compliance and custody scope are reviewed
**When** production scope is finalized
**Then** AML/KYT, sanctions screening, travel rule, case management, hot/warm/cold custody, backup/restore, and signer quorum are either implemented or documented as out-of-scope with risk owner
**And** the integration guide reflects the actual support level.

**Given** readiness evidence is complete
**When** stakeholders review the release package
**Then** they can distinguish controlled beta, production-grade payment gateway, wallet provider custody, and exchange-grade tracking readiness levels
**And** no level is marked ready without passing its documented gates.
