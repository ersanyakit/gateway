---
title: gateway Payment & Wallet Platform
status: final
created: 2026-06-27
updated: 2026-06-27
project: gateway
document_language: Turkish
sources:
  - _bmad-output/planning-artifacts/epics.md
  - _bmad-output/planning-artifacts/implementation-readiness-report-2026-06-27.md
  - _bmad-output/planning-artifacts/delivery/gateway-remaining-stages-2026-06-26.md
  - _bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md
  - _bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/SOLUTION-DESIGN.md
  - docs/product-readiness-audit.md
  - docs/payment-gateway-wallet-provider-audit.md
---

# PRD: gateway Payment & Wallet Platform

## 0. Belge Amacı

Bu PRD, `gateway` reposu için eksik olan canonical ürün gereksinim dokümanını tamamlar. Kaynak olarak mevcut architecture spine, solution design, readiness audit, delivery stages ve epic/story envanteri kullanılmıştır. PRD; ürün sahibi, teknik ekip, UX, architecture, epic/story, QA ve launch governance çalışmalarının ortak baseline'ıdır. `[ASSUMPTION: Existing epics remain valid.]`

Bu belge mevcut teknik kararları tekrar tasarlamaz; capability, davranış, sınır, launch gate ve başarı ölçütlerini ürün diliyle sabitler. Teknik mekanizma detayları ve ertelenmiş mimari kararların gerekçeleri `addendum.md` içinde tutulur.

FR ID'leri mevcut `epics.md` ve readiness raporuyla uyumlu kalması için `FR1...FR40` formatında korunmuştur. NFR ID'leri de `NFR1...NFR20` formatındadır.

## 1. Vision

`gateway`, iki ürün yüzeyini tek bir güvenilir para çekirdeği üzerinde birleştiren kripto ödeme ve wallet altyapısıdır: e-ticaret merchant'ları için payment gateway ve exchange/wallet platformları için user wallet provider. Ürün, partnerlerin ödeme almasını, statik deposit wallet üretmesini, deposit/withdrawal/refund/sweep lifecycle'larını izlemesini ve money event'lerini güvenli webhook kontratlarıyla almasını sağlar.

Ürünün ana tezi şudur: küçük ve orta ölçekli merchant gateway pilotu, aynı money core disiplinleri doğru kurulursa daha sonra wallet-provider ve exchange-grade altyapıya evrilebilir. Bu nedenle ilk değer "ödeme alabiliyorum" değil, "paranın lifecycle'ı idempotent, izlenebilir, reconcile edilebilir ve geri alınabilir şekilde yönetiliyor" olmalıdır.

Canlı büyük hacimli müşteri fonu ve exchange-grade operation, bu PRD'nin hedef mimarisine dahildir fakat ilk release iddiası değildir. İlk release controlled merchant/dealer beta olmalıdır; production custody ve borsa ölçeği signer, reconciliation, observability, compliance ve scale gate'leri kapanmadan açılmamalıdır.

## 2. Target User

### 2.1 Jobs To Be Done

- Merchant developer, ödeme oluşturma ve callback entegrasyonunu düşük sürtünmeyle tamamlamak ister.
- Merchant operasyon ekibi, payment status, webhook delivery, refund ve payout durumlarını güvenle izlemek ister.
- Exchange/wallet platformu, kullanıcı bazlı deposit wallet, balance, withdrawal ve sweep akışlarını tek money core ile yönetmek ister.
- Platform operator, chain listener, provider, webhook, ledger, reconciliation ve signer durumlarını üretimde denetlenebilir hale getirmek ister.
- Custody/security owner, private key/mnemonic'in application process veya app database içinde görünmemesini ve outbound para hareketlerinin policy ile yönetilmesini ister.

### 2.2 Non-Users (v1)

- Regulated exchange-grade custody isteyen yüksek hacimli borsalar, production signer ve exchange-grade indexer tamamlanmadan v1 kullanıcısı değildir.
- Self-custody wallet son kullanıcıları v1 kapsamı değildir; ürün partner-facing altyapı sağlar.
- Fiat on/off-ramp, card acquiring, dispute/chargeback ve bank settlement ürünleri v1 kapsamı değildir.
- Genel amaçlı blockchain indexer ürünü v1 kapsamı değildir; indexing para hareketleri için owned wallet/address scope'una bağlıdır.

### 2.3 Key User Journeys

- **UJ1. Aylin bir merchant ödeme entegrasyonunu canlı beta için bağlar.** Aylin, orta ölçekli bir e-ticaret ekibinde backend geliştiricisidir. API key ve HMAC imza bilgileriyle authenticate olur, payment session oluşturur, checkout URL alır, webhook endpoint'ini tanımlar ve test deposit sonrası `payment.succeeded.v1` event'inin doğrulanabilir şekilde geldiğini görür. Edge case: aynı idempotency key farklı payload ile tekrar kullanılırsa conflict alır ve duplicate order yaratmaz.

- **UJ2. Deniz hosted checkout'ta kripto ödeme yapar.** Deniz, merchant sitesinden checkout'a yönlenir. Asset/chain seçer, QR ve raw amount bilgisini görür, ödeme yapar ve UI ödeme durumunu active, confirming, paid, expired, failed veya underpaid şeklinde açıkça gösterir. Edge case: ödeme expiry sonrası gelirse sistem bunu terminal success'e çevirmeden policy'ye uygun exception/reconciliation akışına taşır.

- **UJ3. Mert exchange kullanıcısı için deposit ve balance güvenilirliğini izler.** Mert, küçük bir exchange operasyon ekibindedir. Kullanıcıya deterministic deposit wallet üretilir; chain event wallet ownership ile eşleşir; finality tamamlanınca ledger credit oluşur; balance API sadece ledger-derived projection'dan cevap verir. Edge case: reorg tespit edilirse ledger destructive edit yerine reversal/correction event'i üretir.

- **UJ4. Selin webhook ve reconciliation sorununu operatör olarak çözer.** Selin, platform operatörüdür. Merchant webhook'u başarısız olduğunda delivery attempt, retry/backoff, dead-letter ve replay durumunu görür. Reconciliation job chain fact, ledger entry, lifecycle state, webhook delivery ve broadcast state'i scoped şekilde karşılaştırır. Edge case: duplicate replay olduğunda consumer-facing event idempotency bozulmaz.

- **UJ5. Can outbound para hareketini custody policy ile onaylar.** Can, custody/security owner'dır. Withdrawal, payout, refund veya sweep talebi ledger hold olmadan signing'e gidemez. Chain-specific nonce/UTXO/resource reservation alınır, signer sadece key reference ve policy metadata ile imza üretir, finalization sonrası ledger debit veya hold release yapılır. Edge case: signer veya broadcast failure olursa retry blind transaction üretmeden reconciliation-first çalışır.

## 3. Glossary

- **Tenant / Domain** - Üründeki ownership scope'u. Mevcut API'de merchant/domain olarak görünebilir; yeni boundary ve event tasarımları tenant kavramını kullanır.
- **Merchant Payment Gateway** - Merchant'ın payment session, hosted checkout, static deposit address, payment lifecycle ve webhook akışlarıyla kripto ödeme almasını sağlayan ürün yüzeyi.
- **Wallet Provider** - Partnerin user wallet, deposit, balance, withdrawal, sweep ve reconciliation akışlarını yönetmesini sağlayan ürün yüzeyi.
- **Money Core** - Wallet, Chain Indexer, Deposit, Ledger, Signer, Webhook ve Reconciliation boundary'lerinin ortak para hareketi çekirdeği.
- **Payment Session** - Checkout URL, selected chain/token, quote snapshot, expected raw amount, expiry, deposit address, transaction hash/finality ve payment lifecycle bilgisini tutan ödeme niyeti.
- **Static Deposit Wallet** - Tenant/domain/product/user scope'una deterministik bağlı deposit wallet.
- **Chain Indexer** - Block/slot progress, raw transaction/log extraction, provider health, finality signal ve reorg detection sorumlusu boundary.
- **Deposit** - Chain event'ini wallet ownership ile eşleştiren ve settlement input'u üreten boundary.
- **Ledger** - Pending, available, hold, transit, debit, credit, reversal ve adjustment kayıtlarından authoritative balance üreten boundary.
- **Signer Boundary** - KMS/HSM/MPC/Vault veya seçilecek external custody signer üzerinden key reference ile imza üreten production boundary.
- **Webhook Boundary** - Versioned money event delivery, retry, replay, dead-letter, HMAC signing ve diagnostics sorumlusu boundary.
- **Reconciliation** - Chain facts, ledger entries, lifecycle state, webhook delivery ve broadcast state arasındaki drift'i scoped job'larla çözme mekanizması.
- **Outbox** - State change ile aynı transaction içinde yazılan durable business event substrate.
- **Reorg Correction** - Chain reorg durumunda destructive edit yerine reversal ledger entries ve correction webhook/event üretme davranışı.
- **Launch Gate** - Gerçek müşteri fonu, production custody veya exchange-grade operation öncesi kapanması gereken zorunlu readiness kararı.

## 4. Product Thesis and Release Posture

V1 release posture controlled merchant/dealer beta'dır. `[ASSUMPTION: Controlled beta first posture.]` Bu posture küçük hacim, sınırlı tenant, limitli chain/token seti, düşük custody bakiyesi, manual operator oversight ve canary-style rollout ile sınırlandırılır.

Wallet-provider custody ve exchange-grade wallet tracking, aynı PRD kapsamındaki hedef yeteneklerdir fakat v1 production iddiası değildir. Bu hedefler için external signer, full reconciliation, exchange-grade indexer scale, compliance scope, launch SLO ve observability gate'leri kapanmalıdır.

## 5. Features and Functional Requirements

### 5.1 Partner Integration and Payment Intake

**Description:** Merchant ve exchange partnerleri güvenli API kontratlarıyla payment session oluşturur, static wallet alır, hosted checkout kullanır ve integration contract'ına güvenebilir. Realizes UJ1, UJ2.

#### FR1: İki ürün yüzeyi
Sistem e-ticaret merchant payment gateway ve exchange/user wallet provider yüzeylerini desteklemelidir.

**Consequences:**
- Merchant payment ve wallet-provider API akışları ayrı ürün surface'i olarak dokümante edilir.
- Ortak para hareketi davranışları Money Core üzerinden çalışır.

#### FR2: Merchant payment gateway akışı
Merchant payment gateway; payment session, hosted checkout, static deposit address, payment lifecycle ve merchant webhook akışlarını sağlamalıdır.

**Consequences:**
- Payment create response checkout URL ve payment lifecycle izleme bilgisini döndürür.
- Merchant webhook'ları payment terminal state ve correction state'lerini alabilir.

#### FR3: Wallet provider akışı
Wallet provider yüzeyi; user wallet, deposit, balance, withdrawal, sweep ve reconciliation akışlarını sağlamalıdır.

**Consequences:**
- Tenant/domain/product/user scope'unda wallet üretilebilir.
- Deposit, balance, withdrawal ve sweep lifecycle'ları ledger ve reconciliation ile izlenebilir.

#### FR5: Payment session kapsamı
Sistem payment session oluştururken checkout URL, seçilen chain/token, quote snapshot, expected raw amount, expiry, deposit address, transaction hash/finality alanları ve idempotency davranışını yönetmelidir.

**Consequences:**
- Aynı idempotency key ve aynı payload tekrarında aynı semantic result döner.
- Aynı idempotency key farklı payload ile kullanıldığında conflict döner.

#### FR6: Hosted checkout state experience
Hosted checkout; asset seçimi, QR gösterimi, ödeme durumunun gerçek zamanlı veya websocket tabanlı izlenmesi ve mobil/regression güvenilirliği için testlenebilir davranışlar sağlamalıdır.

**Consequences:**
- Checkout active, confirming, paid, expired, failed ve underpaid/partial durumlarını ayırt eder.
- Mobile viewport, refresh ve websocket disconnect senaryoları acceptance coverage'a girer.

#### FR7: Deterministic static deposit wallet
Sistem tenant/domain/product/user kapsamına göre deterministic static deposit wallet üretmeli ve aynı scope için tekil wallet döndürmelidir.

**Consequences:**
- Aynı scope tekrar çağrıldığında duplicate wallet yerine mevcut wallet döner.
- Scope uniqueness DB-level veya equivalent constraint ile korunur.

#### FR8: Wallet/address generation güvenliği
Desteklenen chain'lerde wallet/address generation Trust Wallet Core üzerinden yapılmalı; fallback provider production'da adres üretmemeli veya sessizce geçerliymiş gibi davranmamalıdır.

**Consequences:**
- Production fallback wallet/address generation kapalı veya fail-fast olmalıdır.
- Supported chain coverage testleri address format doğrulaması içerir.

#### FR9: Partner API authentication
Sistem v1 API erişiminde API key veya bearer authentication sağlamalı; mutating endpoint'lerde timestamp ve HMAC request signature doğrulamalıdır.

**Consequences:**
- Timestamp replay window dışındaki mutating request'ler reddedilir.
- Hatalı signature veya key scope yetkisizliği standard error envelope ile döner.

#### FR10: Write API idempotency
Merchant/exchange write API'leri idempotency key kabul etmeli; aynı key farklı payload ile kullanıldığında conflict dönmeli; iç ledger/outbox kayıtlarında DB-level uniqueness uygulanmalıdır.

**Consequences:**
- Duplicate payment, duplicate credit ve duplicate withdrawal testleri regression suite'e girer.
- Internal idempotency keys ledger/outbox seviyesinde unique constraint veya equivalent guard taşır.

#### FR40: API contract stability
API contract stability; OpenAPI contract tests, backwards-compatible error envelope, API versioning/deprecation policy ve integration guide hardening ile korunmalıdır.

**Consequences:**
- Public endpoint contract changes CI'da contract test ile yakalanır.
- Breaking change deprecation policy olmadan yayınlanmaz.

### 5.2 Shared Money Core and Deposit Settlement

**Description:** Merchant checkout/static-address akışları ve exchange wallet akışları aynı Wallet, Chain Indexer, Deposit, Ledger, Webhook ve Reconciliation boundary'lerini kullanır. Realizes UJ3.

#### FR4: Ortak Money Core
Merchant checkout/static-address akışları ve exchange wallet akışları; Wallet, Chain Indexer, Deposit, Ledger, Signer, Webhook ve Reconciliation boundary'lerini ortak money core olarak kullanmalıdır.

**Consequences:**
- Ürün yüzeyleri ayrı ledger veya ayrı deposit tracking sistemi kurmaz.
- Product-specific behavior Payment Session veya Wallet Provider API layer'da kalır.

#### FR11: Chain Indexer fact ownership
Chain Indexer boundary, block/slot progress, raw transaction/log extraction, provider health, finality signal ve reorg detection işlerini sahiplenmeli; business state'i doğrudan mutate etmemelidir.

**Consequences:**
- Listener code payment paid veya ledger credit'i doğrudan yazmaz.
- Business state mutation Deposit/Payment/Ledger consumer boundary'lerinden geçer.

#### FR12: Deposit detection and backfill
Chain listener'lar EVM family, Bitcoin, Solana ve TRON için deposit detection yapmalı; explicit start block/slot ve range replay/backfill yapılandırmasını desteklemelidir.

**Consequences:**
- First-start behavior config'siz geçmiş deposit kaçırma riskiyle açılmaz; explicit start veya documented pilot guard gerekir.
- Range replay/backfill operator action olarak testlenebilir.

#### FR13: Deposit lifecycle input
Deposit boundary chain event'lerini wallet ownership ile eşleştirmeli, deposit lifecycle'ı yönetmeli ve payment session settlement input'u üretmelidir.

**Consequences:**
- Unknown address event'leri business payment state'i mutate etmez.
- Matched deposit finality tamamlanmadan available balance veya succeeded payment üretmez.

#### FR14: Finality gate
Payment/deposit settlement, chain-specific confirmation/finality gate tamamlanmadan terminal paid/succeeded davranışına geçmemelidir.

**Consequences:**
- Chain-specific confirmation threshold testleri supported chain family bazında bulunur.
- Pre-finality deposit pending/confirming olarak görünür.

#### FR15: Payment lifecycle correction
Payment lifecycle succeeded, failed ve expired durumlarını desteklemeli; reorg veya correction durumunda payment correction webhook'u ve lifecycle düzeltmesini tetikleyebilmelidir.

**Consequences:**
- Reorg sonrası paid payment destructive edit yerine correction event ile düzeltilir.
- Expired payment sonradan gelen deposit için policy-driven exception/reconciliation üretir.

#### FR16: Payment matching outcomes
Payment matching, underpaid, overpaid ve partial-paid durumlarını ayrı lifecycle/status/event olarak modellemeli veya açık business policy ile ele almalıdır.

**Consequences:**
- Amount mismatch sessiz success'e çevrilmez.
- Underpaid/overpaid/partial testleri checkout ve webhook contract coverage'a girer.

#### FR17: Ledger balance authority
Ledger boundary pending, available, hold, transit, debit, credit, reversal ve adjustment kayıtlarını yönetmeli; balance endpoint'leri sadece ledger-derived projection veya query layer üzerinden çalışmalıdır.

**Consequences:**
- Balance API transaction row toplamı veya live chain read üzerinden cevap vermez.
- Ledger invariant testleri debit/credit consistency'yi kontrol eder.

#### FR18: Lifecycle tables are not balance authority
`transactions`, `payment_sessions`, `withdrawal_requests`, `refunds` ve `sweep_jobs` lifecycle state tutabilir, ancak authoritative balance kaynağı olarak kullanılmamalıdır.

**Consequences:**
- Lifecycle state düzeltmeleri ledger balance'ı doğrudan override edemez.
- Reconciliation drift ledger ve lifecycle state'i ayrı ayrı raporlar.

#### FR32: Scoped reconciliation
Reconciliation boundary chain facts, ledger entries, lifecycle state, webhook delivery ve broadcast state'i karşılaştıran scoped recovery job'ları açmalı ve çözmelidir.

**Consequences:**
- Belirsiz money state durumunda manual-only çözüm yerine reason ve scope içeren reconciliation job açılır.
- Job sonucu resolved, deferred veya escalated olarak izlenir.

#### FR33: Ledger and reserve readiness
Ledger invariant checker, on-chain balance comparison, reserve/liability reporting ve balance drift alerting production readiness kapsamına alınmalıdır.

**Consequences:**
- Real-funds pilot öncesi ledger invariant checker çalışır.
- Production custody öncesi on-chain reserve/liability comparison ve drift alert gerekir.

#### FR34: Reorg handling
Reorg handling block hash continuity, parent/child tracking, rollback window processor, affected ledger reversal, payment correction webhook, sweep dead-letter ve reconciliation job davranışlarını sağlamalıdır.

**Consequences:**
- Same-height block hash conflict temel koruma olarak yeterli sayılmaz; rollback window processor gate'tir.
- Reorg simulation tests launch gate kapsamındadır.

### 5.3 Reliable Money Event Delivery

**Description:** Para lifecycle event'leri source module içinde inline callback olarak kalmaz; versioned event catalog, outbox, idempotent delivery, replay ve diagnostics üzerinden yönetilir. Realizes UJ1, UJ4.

#### FR26: Webhook boundary separation
Webhook boundary source money flow'lardan ayrılmalı; source modules sadece versioned webhook event enqueue etmeli, delivery/retry/replay/dead-letter/HMAC/diagnostics davranışları Webhook boundary tarafından yönetilmelidir.

**Consequences:**
- Payment/deposit/withdrawal code merchant HTTP latency'sine bağlı kalmaz.
- Delivery retry ve dead-letter source module lifecycle'ını bozmaz.

#### FR27: Versioned event catalog
Sistem payment, deposit, withdrawal, refund, sweep ve correction event'leri için versioned event catalog sağlamalıdır.

**Consequences:**
- Her public money event type için schema, version, idempotency key ve compatibility policy dokümante edilir.
- Contract tests event payload/header compatibility'sini kontrol eder.

#### FR28: Dotted event names
Yeni money event contract'ları dotted ve versioned adlar kullanmalıdır: `deposit.detected.v1`, `deposit.finalized.v1`, `payment.succeeded.v1`, `payment.failed.v1`, `payment.expired.v1`, `withdrawal.requested.v1`, `withdrawal.broadcast.v1`, `withdrawal.finalized.v1`, `withdrawal.failed.v1`, `refund.succeeded.v1`, `sweep.succeeded.v1`, `transaction.reorged.v1`.

**Consequences:**
- Yeni event catalog bu isimleri canonical kabul eder.
- Legacy alias'lar canonical event'lere map edilir.

#### FR29: Legacy webhook compatibility
Mevcut underscore webhook event adları compatibility alias olarak desteklenmeli ve resmi deprecation/event catalog migration olmadan kırılmamalıdır.

**Consequences:**
- `payment_succeeded` gibi mevcut consumers migration policy olmadan kırılmaz.
- Deprecation notice, migration guide ve dual-delivery/test policy tanımlanır.

#### FR30: Postgres outbox substrate
Postgres outbox, monolith içindeki ilk durable event substrate olmalı; state change ile event insert aynı transaction içinde yapılmalıdır.

**Consequences:**
- State commit başarılı olup event kaybı yaşanması regression test ile yakalanır.
- External broker selection throughput/partitioning kanıtına kadar ertelenir.

#### FR31: Idempotent outbox consumers
Outbox consumers at-least-once delivery varsayımıyla idempotent çalışmalı; replay ve duplicate delivery testleriyle doğrulanmalıdır.

**Consequences:**
- Duplicate outbox delivery duplicate webhook veya duplicate ledger side effect üretmez.
- Replay operator action'ı audit trail üretir.

### 5.4 Safe Outbound Funds and Custody Controls

**Description:** Withdrawal, payout, refund ve sweep akışları ledger hold, policy validation, chain reservation, external signer ve audit kontrolleri tamamlanmadan imzalanmaz veya broadcast edilmez. Realizes UJ5.

#### FR19: Hold before outbound signing
Withdrawal, payout, refund ve sweep talepleri ledger hold/reservation olmadan imzalanmamalı veya broadcast edilmemelidir.

**Consequences:**
- Insufficient available balance durumunda signing request oluşmaz.
- Hold failure outbound lifecycle'ı terminal veya retryable hata olarak açıkça işaretler.

#### FR20: Chain resource reservation
Outbound money akışları chain-specific nonce, UTXO, resource/gas veya equivalent reservation almadan signing aşamasına geçmemelidir.

**Consequences:**
- EVM nonce conflict ve Bitcoin UTXO double-spend race testleri bulunur.
- Stuck transaction recovery blind replacement yerine policy ve reconciliation ile çalışır.

#### FR21: Withdrawal/payout/refund lifecycle
Withdrawal/payout/refund akışları request, policy validation, hold, admin approval veya maker-checker approval, signing, broadcast, finalization/release ve webhook lifecycle'ını kapsamalıdır.

**Consequences:**
- Lifecycle state machine terminal ve retryable durumları ayırır.
- Approval, broadcast ve finalization immutable audit log'a girer.

#### FR22: Durable auto-sweep jobs
Auto-sweep finalized deposit sonrası durable `sweep_jobs` benzeri persistent job olarak yazılmalı; claim, retry, exponential backoff, dead-letter, tx hash kaydı ve recovery davranışları sağlamalıdır.

**Consequences:**
- Process crash sweep intent'ini kaybettirmez.
- Max retry sonrası operator-visible dead-letter durumu oluşur.

#### FR23: Gas prefund and concurrency policy
Gas prefund veya chain-specific funding alt işleri idempotent olmalı ve sweep/withdrawal concurrency policy ile yönetilmelidir.

**Consequences:**
- Gas prefund duplicate transfer üretmez.
- Per-wallet concurrency policy sweep ve withdrawal çakışmalarını engeller.

#### FR24: Production signer boundary
Signer boundary production'da KMS/HSM/MPC/Vault veya seçilecek external custody signer ile çalışmalı; application code private key veya mnemonic alamamalı, saklamamalı ve loglamamalıdır.

**Consequences:**
- `SIGNER_MODE=software` development-only kalır ve production'da fail-fast eder.
- Production signing request private key veya mnemonic döndürmez.

#### FR25: Signing request metadata
Signing request'leri key reference, chain, derivation/account context, transaction intent ve policy metadata ile yapılmalıdır.

**Consequences:**
- Signer audit log hangi key reference ve policy context ile imza üretildiğini gösterir.
- Request/response chain-specific transaction intent'i doğrulanabilir şekilde taşır.

#### FR35: Fee and gas policy
Fee/gas policy; EVM EIP-1559, ERC-20 gas estimation, Bitcoin fee estimation/RBF/CPFP, Solana priority fee/blockhash retry ve TRON resource/energy accounting gibi chain-specific stratejileri desteklemelidir.

**Consequences:**
- Fixed gas/fee assumptions production readiness için yeterli sayılmaz.
- Supported chain family bazında fee simulation ve failure mode testleri bulunur.

#### FR39: Admin/security hardening
Admin/security hardening; CSRF audit, role separation, key rotation policy, IP/device/session controls, immutable audit trail, address whitelist, velocity limits, dual approval ve emergency freeze davranışlarını kapsamalıdır.

**Consequences:**
- High-risk outbound action'lar single uncontrolled admin action ile tamamlanmaz.
- Emergency freeze para çıkışlarını durdurur ve audit event üretir.

### 5.5 Production Operations and Scale Readiness

**Description:** Platform, controlled beta'dan production gateway ve exchange-grade scale'e evrilebilmek için provider health, address lookup, migration discipline, observability, launch gate ve scale evidence üretir. Realizes UJ4.

#### FR36: RPC/provider reliability
RPC/provider layer provider health scoring, fallback consistency checks, archive/quorum strategy ve per-provider metrics sağlamalıdır.

**Consequences:**
- Stale node veya inconsistent head durumunda operator-visible alert oluşur.
- Provider failover consistency check olmadan silent success sayılmaz.

#### FR37: Address lookup scale
Address lookup milyonlarca adres ölçeğine hazırlanmalı; chain-specific wallet kolonları yerine normalize/partitioned lookup veya equivalent indexed strategy planlanmalıdır.

**Consequences:**
- Large wallet set benchmark ve query plan evidence exchange-grade readiness'e girer.
- In-memory all-address index tek başına scale strategy sayılmaz.

#### FR38: Production operations baseline
Production operations; versioned migrations, environment separation, structured logs, metrics, traces, SLOs, alerts, incident runbooks, backup/restore drills, signer audit logs, withdrawal approval audit logs ve reconciliation dashboards sağlamalıdır.

**Consequences:**
- Real-funds launch öncesi migration, observability, backup/restore ve incident runbook evidence gerekir.
- Chain lag, webhook lag, sweep backlog, signer latency ve reconciliation drift dashboard/alert kapsamına girer.

## 6. Cross-Cutting NFRs

- **NFR1:** Gerçek müşteri fonu veya yüksek hacimli production kullanım, signer, reorg, durable eventing, reconciliation, withdrawal policy ve observability P0 gate'leri tamamlanmadan açılmamalıdır.
- **NFR2:** Para hareketleri idempotent, replay-safe ve duplicate-credit/duplicate-withdrawal dirençli olmalıdır.
- **NFR3:** Ledger kayıtları double-entry prensibine uygun olmalı; debit/credit toplamları, status enumları, unique keys, partial indexes ve ledger invariants DB-level constraints/testlerle korunmalıdır.
- **NFR4:** Production custody, private key/mnemonic'in application process memory, app database veya loglara çıkmamasını garanti etmelidir.
- **NFR5:** Durable job/event processing crash recovery, retry, lock, poison/dead-letter ve operator replay/recovery davranışları sağlamalıdır.
- **NFR6:** Reorg/correction işlemleri destructive edit yapmamalı; compensating ledger entries ve correction events ile izlenebilir olmalıdır.
- **NFR7:** Sistem küçük/orta merchant gateway pilotundan exchange-grade ölçeğe chain indexer sharding, partitioned address lookup ve durable event bus ile evrilebilir olmalıdır.
- **NFR8:** Chain catch-up throughput, block lag, webhook lag, sweep backlog, signer latency ve reconciliation drift için ölçülebilir SLO/alert eşikleri tanımlanmalıdır.
- **NFR9:** Observability structured JSON logs, Prometheus-compatible metrics, traces, dashboards ve alert rules ile desteklenmelidir.
- **NFR10:** Provider/RPC erişimi stale node, missed block, provider outage ve inconsistent head durumlarında failover/quorum davranışı göstermelidir.
- **NFR11:** Public webhook/API contract'ları backwards-compatible korunmalı; breaking changes deprecation policy olmadan yapılmamalıdır.
- **NFR12:** Production database değişiklikleri startup `AutoMigrate` yerine versioned, reviewable, rollback-aware ve lock-aware migration stratejisiyle yapılmalıdır.
- **NFR13:** Admin, signer, withdrawal approval, replay ve recovery aksiyonları immutable audit log ile izlenebilmelidir.
- **NFR14:** Test kapsamı unit, integration, chain simulator, fork/reorg simulation, webhook retry, withdrawal concurrency, ledger invariant, crash recovery, `go test ./...`, `go vet ./...` ve kritik race/concurrency testlerini içermelidir.
- **NFR15:** Merchant/operator deneyimi webhook diagnostics, replay status, dead-letter visibility ve actionable error reporting ile desteklenmelidir.
- **NFR16:** Compliance kapsamı açıkça belirlenmeli; AML/KYT, sanctions screening, travel rule, case management veya bunların ürün kapsamı dışında olduğuna dair policy netleştirilmelidir.
- **NFR17:** Backup/restore drills, seed/key recovery policy, signer quorum ve incident runbooks gerçek fon kullanımından önce doğrulanmalıdır.
- **NFR18:** Tenant isolation; per-tenant rate limit, quotas, data export, audit isolation ve encryption policy ile güçlendirilmelidir.
- **NFR19:** Pricing/quote güvenilirliği multi-source oracle, staleness guard, circuit breaker, volatility freeze ve oracle outage policy ile korunmalıdır.
- **NFR20:** Canlıya çıkış kontrollü pilot/canary yaklaşımıyla, küçük limitler, otomatik alertler, rollback runbook ve manuel reconciliation desteğiyle yapılmalıdır.

## 7. UX and Operator Experience Requirements

UX design contract bu PRD sırasında bulunmamıştır. `[ASSUMPTION: First UX artifact is not required to create this PRD.]` Bu nedenle aşağıdaki requirements UI-heavy implementation başlamadan UX handoff veya story-level AC olarak korunmalıdır:

- Hosted checkout active, confirming, paid, expired, failed, underpaid, overpaid ve partial-paid state'lerini kullanıcıya açıkça göstermelidir.
- Checkout mobile viewport, QR readability, copy address/amount, refresh, websocket disconnect ve expiry davranışları için acceptance coverage taşımalıdır.
- Merchant/developer diagnostics webhook delivery status, retry schedule, dead-letter reason, replay action ve signature verification bilgisini göstermelidir.
- Operator dashboards chain lag, provider health, webhook lag, sweep backlog, signer latency, reconciliation drift ve stuck withdrawal durumlarını ayrıştırmalıdır.
- Admin/security screens secret, mnemonic veya private key göstermemeli; high-risk actions için role separation, audit trail ve freeze control sunmalıdır.

## 8. Non-Goals

- V1, yüksek hacimli exchange-grade custody launch değildir.
- V1, production private key custody'yi software signer ile çözmez.
- V1, generic blockchain indexer ürünü değildir.
- V1, fiat payment, card acquiring, bank settlement veya chargeback ürünü değildir.
- V1, AML/KYT/travel-rule entegrasyonunu otomatik var saymaz; compliance scope ayrıca kararlaştırılmalıdır.
- V1, fiziksel mikroservis ayrımını ilk hedef yapmaz; modular monolith hardening önce gelir.
- V1, legacy webhook event adlarını migration policy olmadan kırmaz.

## 9. MVP Scope

### 9.1 In Scope

- Controlled merchant/dealer beta posture.
- Payment session creation, hosted checkout, static deposit wallet ve merchant webhook lifecycle.
- API key/bearer auth, timestamp/HMAC signature ve idempotency protection.
- Shared Money Core boundary discipline.
- Versioned event catalog, webhook boundary ve Postgres outbox baseline.
- Ledger-derived balance authority, finality gate, reorg correction ve reconciliation foundation.
- Durable sweep jobs, withdrawal/refund/payout lifecycle hardening ve signer boundary definition.
- Production migration, observability, SLO, alert ve runbook baseline.

### 9.2 Out of Scope for MVP

- Full exchange-grade scale with sharded indexers and archive/quorum infrastructure.
- Real-funds production custody without external signer.
- Full AML/KYT/travel-rule productization until jurisdiction and provider are selected.
- External broker migration before Postgres outbox limits are measured.
- Full physical service extraction before modular boundaries and contract tests are stable.

## 10. Launch Gates

### 10.0 Gate Governance

Launch gate evidence, planning artifact klasörü altında owner ve kanıt linkiyle tutulmalıdır. Varsayılan owner dağılımı:

- Product owner: first customer profile, supported chain/token seti, pricing/quote policy ve partner-facing launch claim.
- Engineering owner: signer, ledger, outbox, reorg, reconciliation, migration ve API contract evidence.
- Operations owner: observability, alert, runbook, backup/restore, incident drill ve on-call/SLO evidence.
- Security/compliance owner: custody policy, admin controls, audit trail, AML/KYT/sanctions/travel-rule scope ve emergency freeze evidence.

Numeric SLO hedefleri ilk production customer profile, chain/token seti ve custody posture kararlaştırıldıktan sonra bu PRD'nin §12 açık sorularından kapatılmalıdır.

### 10.1 Controlled Beta Gate

- Limitli tenant, chain/token seti, transaction amount ve custody balance belirlenir.
- Manual reconciliation ve operator monitoring çalışır.
- `go test ./...`, `go vet ./...`, API contract smoke ve critical money-path tests geçer.
- Hosted checkout critical states ve webhook delivery diagnostics acceptance coverage'a sahiptir.

### 10.2 Real-Funds Production Gate

- External signer provider seçilmiş ve en az target first chain için production-grade entegrasyon kanıtlanmıştır.
- Ledger-only balance, DB-level invariants ve on-chain reserve/liability comparison çalışır.
- Reorg rollback window, fork/reorg simulation ve correction webhook tests geçer.
- Webhook event catalog, retry/backoff, dead-letter, replay ve diagnostics tamamdır.
- Versioned migrations, structured logs, metrics, traces, alert rules, incident runbooks ve backup/restore drill tamamdır.
- Compliance scope, sanctions/KYT/travel-rule kararı ve owner belirlenmiştir.

### 10.3 Exchange-Grade Gate

- Per-chain sharded indexer, partitioned address lookup ve large wallet set benchmark kanıtlanmıştır.
- Archive/quorum provider strategy ve provider consistency checks production'da çalışır.
- Chain lag, missed block, reconciliation drift, stuck withdrawal ve webhook lag alertleri vardır.
- Hot/warm/cold custody policy, velocity limits, whitelist, dual approval ve emergency freeze tamamdır.
- On-call/SLO operating model ve incident drills tamamdır.

## 11. Success Metrics

**Primary**

- **SM1:** Controlled beta payment success integrity - finalized deposit sonrası duplicate credit olmadan doğru payment lifecycle ve webhook üretilmesi. Target: critical path testlerinde 0 duplicate credit/duplicate payment. Validates FR2, FR5, FR10, FR14, FR17, FR26.
- **SM2:** Webhook reliability baseline - event delivery retry/backoff/dead-letter/replay akışlarının at-least-once ve idempotent çalışması. Target: duplicate replay testlerinde consumer-facing duplicate side effect 0. Validates FR26-FR31.
- **SM3:** Ledger authority - balance endpoint'lerinin ledger-derived çalışması ve invariant checker'ın drift'i yakalaması. Target: ledger invariant regression suite pass. Validates FR17, FR18, FR32, FR33.
- **SM4:** Production launch readiness evidence - signer, reorg, reconciliation, observability ve migration gate'leri için kanıt dosyalarının tamamlanması. Target: Real-Funds Production Gate checklist pass. Validates FR24, FR34, FR36, FR38.

**Secondary**

- **SM5:** Partner integration friction - test merchant'ın API auth, payment create, checkout, webhook verify ve replay smoke path'ini tamamlayabilmesi. Target: documented integration guide path pass. Validates FR1, FR2, FR9, FR40.
- **SM6:** Operator visibility - chain lag, webhook lag, sweep backlog, signer latency ve reconciliation drift dashboard/alert coverage. Target: all named signals visible in staging. Validates FR32, FR36, FR38.
- **SM7:** Outbound safety - withdrawal/refund/sweep hold, approval, signing, broadcast, finalization/release path'lerinde unauthorized or unreserved signing olmaması. Target: policy and concurrency tests pass. Validates FR19-FR25, FR35, FR39.

**Counter-metrics**

- **SM-C1:** Faster payment success must not optimize away finality. A lower time-to-paid is harmful if FR14 finality gate is bypassed.
- **SM-C2:** Higher webhook delivery attempts must not hide idempotency failures. More retries are harmful if duplicate consumer side effects increase.
- **SM-C3:** Faster launch must not reduce custody safety. A release is harmful if software signer is used for production custody.

## 12. Open Questions

1. İlk production customer profile hangisi olacak: e-commerce merchant, small exchange, internal pilot veya dealer-only beta?
2. İlk real-funds signer class/provider hangisi kabul edilecek: cloud KMS, HSM, MPC vendor, Vault-backed signer veya external custody provider?
3. Compliance scope hangi jurisdiction/customer type için geçerli olacak; AML/KYT, sanctions, travel-rule ve case management kapsam içinde mi, dışında mı?
4. Launch SLO hedefleri nedir: deposit finality latency, webhook delivery latency, withdrawal broadcast latency, reconciliation resolution time?
5. Supported first-release chain/token seti hangi varlıklarla sınırlandırılacak?
6. Hosted checkout ve operator dashboard için ayrı UX handoff ne zaman üretilecek?
7. Story 3.5, Story 4.3 ve Story 5.4 sprint planning sırasında bölünecek mi?

## 13. Assumptions Index

- §0 - `[ASSUMPTION: Existing epics remain valid.]` 40 FR ve 20 NFR inventory mevcut `epics.md` coverage map ile korunmuştur.
- §4 - `[ASSUMPTION: Controlled beta first posture.]` Mevcut readiness raporları gerçek production/custody için gate'leri açık tuttuğundan ilk release hedefi kontrollü merchant/dealer beta olarak kabul edildi.
- §7 - `[ASSUMPTION: First UX artifact is not required to create this PRD.]` UX design contract eksikliği PRD blocker değil, UI-heavy implementation gate'i olarak ele alındı.
