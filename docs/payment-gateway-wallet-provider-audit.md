# Payment Gateway ve Wallet Provider Uygunluk Raporu

Tarih: 2026-06-26  
Kapsam: mevcut `gateway` codebase'i; payment gateway, wallet provider, custody, chain indexing, ledger, webhook, payout/refund ve Binance ayarında borsa wallet tracking beklentileri.

## Nihai karar

Kısa cevap: Hayır, codebase bugün "kusursuz" payment gateway ve wallet provider ihtiyacını karşılamıyor.

Daha doğru sınıflandırma:

| Alan | Bugünkü durum | Karar |
| --- | --- | --- |
| Merchant/dealer payment gateway MVP | Gerçek ve çalışabilir temel var | Kontrollü beta için uygun olabilir |
| Production-grade payment gateway | Birçok temel parça var, ama kritik operasyonel açıklar duruyor | Eksik |
| Wallet provider / custodial wallet ürünü | Deposit wallet ve ledger temeli var, custody güvenliği eksik | Üretime hazır değil |
| Binance ayarında exchange wallet tracking | Tek prosesli listener ve sınırlı backfill modeli var | Hazır değil |
| Kurumsal custody | KMS/HSM/MPC signer yok | Hazır değil |

Bu repo boş veya sahte bir proje değil. Ödeme oturumu, statik adres, multi-chain HD wallet, ledger, webhook, withdrawal ve refund akışları gerçek kodla kurulmuş. Fakat "kusursuz" veya "borsa seviyesinde" demek için gereken güvenlik, dayanıklılık, reconciliation, reorg muhasebesi, signer mimarisi, durable job sistemi ve operasyonel kontrol katmanları henüz tamam değil.

## İncelenen ana yüzeyler

- Payment session ve checkout: `api/handlers/payment.go`, `repositories/payment_repo.go`, `models/payment_session.go`
- V1 API auth, payout, refund, wallet endpointleri: `api/handlers/v1api.go`
- Wallet üretimi ve adres sahipliği: `repositories/wallet_repo.go`, `models/wallet.go`
- Chain signer ve Trust Wallet Core entegrasyonu: `blockchain/basechain.go`, `blockchain/walletcore/*`, `blockchain/chains/*_transfer.go`
- Deposit listener ve finality: `workers/listeners/*`, `main.go`, `repositories/transaction_repo.go`
- Ledger: `models/ledger_entry.go`, `repositories/ledger_repo.go`
- Webhook: `services/webhook/notifier.go`, `repositories/webhook_delivery_repo.go`
- Withdrawal/refund: `repositories/withdrawal_request_repo.go`, `repositories/refund_repo.go`, `api/handlers/dealer.go`
- Operasyonel dokümanlar: `docs/product-readiness-audit.md`, `docs/microservices-architecture.md`, `ROADMAP.md`

## Güçlü taraflar

1. Gerçek payment session modeli var.
   - `models/payment_session.go` ödeme durumlarını, seçilen chain/token bilgisini, beklenen raw tutarı, deposit adresini, tx hash bilgisini, confirmation alanlarını ve webhook attempt alanlarını tutuyor.
   - `api/handlers/payment.go` payment create, checkout URL, expiry ve idempotency akışını kuruyor.

2. API auth ve request imzalama temeli var.
   - `api/handlers/v1api.go` `X-API-Key` veya `Authorization: Bearer` ile domain buluyor.
   - Mutating endpointlerde `X-API-Secret`, timestamp ve `X-Gateway-Signature` HMAC kontrolü var.
   - `repositories/idempotency_repo.go` aynı idempotency key farklı payload ile kullanılırsa conflict üretiyor.

3. Multi-chain deterministic wallet üretimi gerçek.
   - `repositories/wallet_repo.go` merchant/domain/product/user bazında tekil wallet döndürüyor.
   - HD index için transaction içinde advisory lock kullanılıyor.
   - Bitcoin, Ethereum, Avalanche, BNB Chain, Base, Arbitrum, Unichain, TRON, Solana, Chiliz adresleri modelde tutuluyor.

4. Trust Wallet Core entegrasyonu mevcut.
   - `blockchain/walletcore/provider_trustwalletcore.go` `TWHDWallet`, `TWAnyAddress` ve `TWAnySignerSign` kullanıyor.
   - Fallback provider wallet address üretemiyor ve üretim için uygun değil; bu iyi, çünkü sessizce yanlış adres üretmiyor.

5. Deposit detection ve finality katmanı var.
   - Chain listenerlar block/slot tarıyor.
   - `main.go` transaction'ı kaydedip wallet ile eşleştiriyor, finality hesaplıyor, payment eşleşirse ledger ve webhook akışını çalıştırıyor.
   - `models/transactions.go` confirmation, finalized ve reorg alanlarını içeriyor.

6. Ledger temeli var.
   - Deposit pending, deposit available, withdrawal hold, withdrawal debit ve refund debit entry tipleri var.
   - `repositories/ledger_repo.go` birçok hareketi iki satırlı muhasebe yaklaşımıyla yazıyor.
   - Withdrawal hold sırasında available balance kontrolü yapılıyor.

7. Webhook temeli var.
   - Payload HMAC ile imzalanıyor.
   - Event id, timestamp ve event type headerları gönderiliyor.
   - Delivery attempt ve dead-letter benzeri status alanları var.

8. Payout/refund workflow var.
   - Payout request API tarafında oluşturuluyor, ledger hold yazılıyor, admin onayı sonrası transfer ve ledger finalize akışı var.
   - Refund request paid payment ile sınırlanıyor, refundable tutar kontrol ediliyor.

## Payment gateway ihtiyaç matrisi

| İhtiyaç | Bugünkü durum | Yeterlilik | Eksik |
| --- | --- | --- | --- |
| Merchant onboarding | Admin/dealer domain, API key, webhook secret var | Orta | Role separation, key rotation policy, audit hardening |
| API authentication | API key + HMAC imza var | İyi temel | Standart hata kontratı, key scope, replay telemetry, IP allowlist |
| Payment create | Session, checkout URL, expiry, idempotency var | İyi | Contract testleri ve API versioning güçlenmeli |
| Hosted checkout | Asset seçimi, QR, websocket status var | Orta-iyi | UX/security audit, tam mobil/regression testleri |
| Quote ve fiyatlama | Quote snapshot var | Orta | Multi-source oracle, staleness/circuit breaker, extreme volatility policy |
| Deposit address | Session wallet ve static wallet var | İyi temel | Reuse riskleri için invoice-address policy açık yazılmalı |
| Payment matching | Chain/symbol/token/amount eşleşiyor, underpay toleransı var | Orta | Underpaid/overpaid/partial-paid lifecycle eksik |
| Confirmation gate | Transaction finality alanları ve worker var | Orta | Canonical rollback window ve block hash continuity eksik |
| Ledger posting | Pending/available ve reorg reversal entryleri var | Orta | On-chain reconciliation ve tam rollback workflow eksik |
| Webhook | HMAC, retry, delivery log var | Orta | Event versioning, exponential backoff, merchant diagnostics, replay idempotency SLA |
| Refund | Request ve admin onayı var | Orta | Otomatik policy, refund webhook ve chain-finality sonrası net lifecycle güçlenmeli |
| Payout | Hold + admin approval + transfer var | Orta | Address whitelist, velocity limits, nonce/UTXO reservation, maker-checker policy |
| Rescan | Transaction replay endpointi var | İyi destek aracı | Full block/range replay ve reconciliation ile bağlanmalı |
| Observability | Health/readiness ve loglar var | Zayıf-orta | Metrics, tracing, lag, failed job, balance drift alertleri eksik |

Sonuç: Merchant payment gateway olarak MVP seviyesi güçlü. Üretim seviyesi için en büyük riskler finality/reorg, ledger reconciliation, webhook reliability ve payout güvenliği.

## Wallet provider ihtiyaç matrisi

| İhtiyaç | Bugünkü durum | Yeterlilik | Eksik |
| --- | --- | --- | --- |
| Kullanıcı bazlı multi-chain wallet | Merchant/domain/product/user bazında deterministic wallet var | İyi temel | Milyonlarca kullanıcı için partition/index stratejisi eksik |
| Address ownership | Wallet modelinde unique address alanları var | İyi temel | Address table normalizasyonu ve chain bağımsız lookup ölçeklenmeli |
| HD derivation | Trust Wallet Core ile yapılıyor | İyi | External signer ile uyumlu key derivation tasarımı eksik |
| Private key handling | Process içinde mnemonic/private key derivation var | Kritik zayıf | KMS/HSM/MPC yok |
| Deposit balance | Ledger wallet/domain bazlı bakiye üretiyor | Orta | Liability proof, asset reconciliation, snapshot/close period eksik |
| Sweep | Durable `sweep_jobs`, retry/dead-letter, tx-hash guard ve reconciliation temeli var | Orta | Multi-replica resource ownership, replacement evidence ve operator recovery UI eksik |
| Withdrawal | Admin onaylı transfer var | Orta | Nonce/UTXO lock, address whitelist, risk engine, approval matrix eksik |
| Token desteği | ERC-20, TRC-20, SPL gibi transfer pathleri var | Orta | Token registry ve chain-specific edge case testleri genişlemeli |
| Custody policy | Reserve wallet kavramı var | Zayıf | Hot/warm/cold tier, daily limit, freeze, signer audit, segregation eksik |
| Exchange user wallet | Statik deposit wallet endpointi var | Orta | Borsa ölçeğinde address lifecycle ve full audit trail eksik |
| Compliance | Temel ürün akışında görünmüyor | Zayıf | AML, sanctions, KYT, travel rule, case management yok |
| DR/backup | Kodda belirgin değil | Zayıf | Seed/key backup, restore drill, signer quorum, runbook yok |

Sonuç: Codebase "custodial wallet provider" için temel atmış, ama müşteri fonlarını ciddi hacimde tutacak bir wallet provider olmak için kritik güvenlik ve operasyon parçaları eksik.

## Trust Wallet Core ve transfer doğrulaması

"Bütün blockchainlerde wallet ve transfer Trust Wallet üzerinden mi?" sorusunun cevabı: Wallet üretimi büyük ölçüde Trust Wallet Core üzerinden; transfer imzalama ise her chain için Trust Wallet Core üzerinden değil.

| Chain grubu | Wallet derivation | Transfer signing | Karar |
| --- | --- | --- | --- |
| Bitcoin | Trust Wallet Core | P2WPKH Trust Wallet Core, mevcut Taproot sweep için manual fallback | Karışık |
| Ethereum | Trust Wallet Core | Trust Wallet Core native/ERC-20 signing | Evet |
| Avalanche | Trust Wallet Core | EVM Trust Wallet Core path | Evet |
| BNB Chain | Trust Wallet Core | EVM Trust Wallet Core path | Evet |
| Base | Trust Wallet Core | EVM Trust Wallet Core path | Evet |
| Arbitrum | Trust Wallet Core | EVM Trust Wallet Core path | Evet |
| Unichain | Trust Wallet Core | EVM Trust Wallet Core path | Evet |
| Chiliz | Trust Wallet Core | EVM Trust Wallet Core path | Evet |
| TRON | Trust Wallet Core | TRON SDK path | Hayır, sadece derivation Trust Wallet Core |
| Solana | Trust Wallet Core | Solana SDK path | Hayır, sadece derivation Trust Wallet Core |

Bu tek başına fatal bir bug değildir. Chain SDK ile imzalama yapılabilir. Fatal olan nokta, üretim custody için imzalamanın process içindeki mnemonic/private key ile yapılmasıdır. `SIGNER_MODE=kms/hsm/mpc` seçenekleri bugün gerçek external signer entegrasyonu değil, "integration required" hatası döndüren placeholder durumundadır.

## Binance ayarında wallet tracking değerlendirmesi

Binance ayarında bir borsa wallet tracking sistemi için beklenen minimumlar:

- Her chain için durable, shard edilebilir, yatay ölçeklenebilir indexer.
- İlk kurulumda genesis/configured start block'tan deterministik backfill.
- Her block için block hash parent hash takibi.
- Reorg tespiti, transaction reversal ve ledger reversal.
- RPC node quorum veya provider failover kalitesi.
- Çok büyük address setleri için partitioned lookup.
- At-least-once event delivery ve idempotent consumer modeli.
- Block lag, queue lag, failed tx, stale node, missed block alertleri.
- UTXO chainlerde UTXO reservation ve coin selection.
- EVM chainlerde nonce manager, replacement transaction, stuck tx recovery.
- Token metadata ve contract event decoding için registry ve migration policy.
- Gün sonu on-chain balance ile internal ledger reconciliation.

Mevcut kodda bunların yalnızca bir kısmı var:

- Listener var ve artık explicit start block/slot env ayarıyla geçmişten başlatılabiliyor. Env ayarı verilmezse ilk start davranışı hâlâ safe/latest'e yakınsar; bu nedenle otomatik full-history backfill sayılmaz.
- Poll başına limitler düşük: EVM 5 block, Bitcoin 2 block, Solana 8 slot, TRON 10 block. Uzun downtime sonrası yetişme süresi zayıf.
- Reorg correction için same-height block hash conflict tespiti, transaction `reorged` status, ledger reversal ve correction webhook temeli var.
- `chain_state` last processed/confirmed block tutuyor, fakat block hash continuity ve rollback muhasebesi üretim seviyesinde değil.
- Auto-sweep artık durable job tablosuna taşındı ve webhook retry backoff güçlendi. Kalan açık chain event bus, range replay ve bazı worker akışlarının hâlâ monolith/process içi olmasıdır.
- Address matching pratikte çalışabilir, ama milyonlarca adres için normalleşmiş address index/partition stratejisi şart.

Sonuç: Bu haliyle Binance ölçeğinde exchange wallet tracking yapamaz. Küçük/orta hacimli merchant gateway veya kontrollü wallet-provider pilotu için geliştirilebilir bir taban var.

## Kritik açıklar

### 2026-06-26 Kod Düzeltme Güncellemesi

Bu rapordan sonra codebase'e şu düzeltmeler işlendi:

- Auto-sweep artık doğrudan goroutine ile kaybolan bir iş değil. Finalized deposit sonrası `sweep_jobs` tablosuna idempotent job yazılıyor; worker `FOR UPDATE SKIP LOCKED` ile claim ediyor, başarısızlıkta exponential backoff uyguluyor, maksimum deneme sonrası `dead_letter` durumuna alıyor ve başarılı sweep tx hash'ini saklıyor.
- Listener ilk başlangıç davranışı kısmen düzeltildi. `CHAIN_<id>_START_BLOCK`, `<CHAIN_NAME>_START_BLOCK`, `START_BLOCK_<CHAIN_NAME>` veya `CHAIN_START_BLOCK_DEFAULT` verilirse listener geçmiş taramaya o blok/slot değerinden başlayabiliyor.
- Webhook payload'ları ve header'ları event version bilgisi taşıyor: `event_version: "v1"` ve `X-Gateway-Event-Version: v1`.
- Transaction/payment webhook retry akışı artık başarısız denemede hemen tekrar dönmüyor; exponential backoff ve `WEBHOOK_MAX_ATTEMPTS` gating kullanıyor.
- Ledger invariant taraması eklendi. Aynı idempotency key altındaki debit/credit toplamı sıfır değilse reconciliation job açılıyor.
- Reorg için ilk koruma eklendi. Aynı chain ve aynı block height üzerinde farklı block hash görülürse eski transactionlar `reorged` olur, `transaction_reorged` webhook'una hazırlanır, bağlı ledger entryleri idempotent `reorg_reversal` entryleriyle terslenir, bağlı paid payment `payment_failed` correction webhook'una çekilir, bekleyen sweep job dead-letter yapılır ve reconciliation job açılır.

Bu düzeltmeler durable sweep, webhook hardening, configurable backfill, reorg correction ve reconciliation temelini iyileştirdi. Aşağıdaki P0 maddelerden bazıları artık "tamamen yok" değil, "kısmi tamamlandı" durumundadır. Ancak gerçek production signer, canonical parent/child block tracking, horizontal durable event bus ve custody policy hâlâ açık kalır.

### P0 - Canlı müşteri fonu almadan önce çözülmeli

1. Gerçek production signer yok.
   - Kanıt: `services/signer` external custody adapter kontratını ve production watch-only boundary'yi tanımlar; fakat `SIGNER_MODE=kms/hsm/mpc` için gerçek provider implementation ve chain-specific external signing yoktur.
   - Risk: Mnemonic/process memory ile custody fonu tutmak kabul edilemez.
   - Kabul kriteri: KMS/HSM/MPC signer interface, chain başına signing adapter, audit log, key export olmadan imza, testnet/mainnet shadow test.

2. Reorg-safe indexer kısmen tamamlandı.
   - Güncel durum: Aynı chain/block height üzerinde yeni bir block hash görülürse eski transactionlar `reorged` olur, linked ledger entryleri terslenir, linked paid payment `failed` correction webhook'una hazırlanır ve reconciliation job açılır.
   - Kalan risk: Canonical parent/child block hash zinciri, proactive rollback-window scan ve fork simulation testleri henüz yok.
   - Kalan kabul kriteri: Block parent tracking, rollback window processor, deterministic fork simulation, operator reconciliation workflow.

3. Durable backfill ve job queue kısmen tamamlandı.
   - Güncel durum: Listenerlar artık explicit start block/slot env ayarı alabiliyor. Sweep işleri `sweep_jobs` tablosunda persistent job olarak tutuluyor. Webhook retry backoff güçlendi.
   - Kalan risk: Chain event bus hâlâ monolith/process içi; full historical range replay, poison queue ve operator replay UI tam ürünleşmedi.
   - Kalan kabul kriteri: Durable chain event queue, range replay worker, poison queue, operator replay UI.

4. Ledger reconciliation kısmen tamamlandı.
   - Güncel durum: Ledger invariant checker eklendi; debit/credit toplamı bozuk idempotency key için reconciliation job açılıyor.
   - Kalan kanıt: On-chain balance karşılaştırması, reserve proof ve drift alertleri henüz yok.
   - Risk: Bakiye doğru görünürken on-chain fon farklı olabilir.
   - Kalan kabul kriteri: Daily on-chain reconciliation job, per asset liability report, on-chain reserve proof, negative balance guard, dashboard/alert.

5. Withdrawal güvenliği güçlendi, ancak borsa seviyesinde tamam değil.
   - Güncel durum: Hold ve admin approval yanında outbound policy setting/whitelist tabloları, emergency freeze, raw amount limit, rolling velocity limit, high-risk admin role guard ve append-only activity log eklendi.
   - Kalan eksik: Risk score, nonce/UTXO reservation ve gelişmiş approval matrix hâlâ yok.
   - Risk: Yanlış/adversarial payout, double spend benzeri concurrency, stuck transaction.
   - Kalan kabul kriteri: Risk scoring, nonce/UTXO lock, stuck tx replacement.

6. Auto-sweep durable job'a taşındı.
   - Güncel durum: `sweep_jobs` status machine, retry, lock, idempotency, dead-letter, bounded failure category, sweep tx hash kaydı ve parent-job prefund attempt/error/category state eklendi.
   - Kalan risk: Gas prefund ayrı idempotent alt-job değil; admin recovery ekranı ve multi-replica durable resource ownership hâlâ güçlendirilmeli.
   - Kalan kabul kriteri: Admin recovery ekranı, durable multi-replica nonce/UTXO/resource locking, operator replay/reconcile workflow.

### P1 - Production readiness için gerekli

1. Webhook event contract versioning.
   - Güncel durum: Payload ve header seviyesinde `v1` event version eklendi.
   - Kalan: Versiyon katalog dokümanı, backwards compatibility policy ve consumer contract testleri.

2. Webhook retry kalitesi.
   - Güncel durum: Exponential backoff ve max-attempt gating eklendi.
   - Kalan: Per-merchant rate limit, replay audit ve merchant diagnostics.

3. Underpaid/overpaid/partial-paid lifecycle.
   - Bugün amount matching toleranslı ama underpayment ayrı event/status olarak güçlü modellenmiyor.

4. Price oracle hardening.
   - Quote snapshot var, fakat multi-source pricing, staleness guard, volatility freeze ve oracle outage policy üretim seviyesine çıkmalı.

5. API contract stability.
   - V1 endpointler var, ama OpenAPI contract tests, backwards-compatible error envelope ve deprecation policy şart.

6. Observability.
   - Chain lag, listener health, block processing latency, webhook failure rate, sweep backlog, ledger drift, signer latency metrikleri eksik.

7. Database migration ve constraints.
   - GORM model indexleri var, ama production migration dosyaları, check constraints, partial indexes ve online migration stratejisi güçlenmeli.

8. Test kapsamı.
   - Unit testler mevcut, ancak chain integration, fork/reorg simulation, webhook retry, withdrawal concurrency, ledger invariant ve crash recovery testleri artırılmalı.

### P2 - Ölçek ve kurumsal ürünleşme

1. Chain indexer mikroservis ayrımı.
   - `docs/microservices-architecture.md` doğru hedefi tarif ediyor. Uygulamada chain indexer, wallet, ledger, webhook ve payment session servisleri kademeli ayrılmalı.

2. Address index normalizasyonu.
   - Wallet modelindeki chain-specific kolonlar MVP için hızlı, ama çok chain/çok token/çok adres ölçeğinde `wallet_addresses` benzeri normalize tablo daha doğru.

3. Compliance ve risk entegrasyonları.
   - AML/KYT provider, sanctions screening, travel rule, suspicious activity workflow, case management.

4. Tenant isolation.
   - Domain/merchant scope var, ama kurumsal kullanım için per-tenant rate limit, quotas, data export, audit isolation, encryption policy gerekir.

5. Reporting.
   - Merchant settlement report, tax/accounting export, liability/reserve report, failed payment/refund/payout report.

## Ürünün bugünkü gerçek konumu

### Payment gateway olarak

Gerçekçi konum: Controlled beta / limited production candidate.

Kullanım şartları:

- Küçük hacim.
- Sınırlı merchant.
- Limitli chain/token seti.
- Operatör gözetimi.
- Manuel reconciliation.
- Düşük custody bakiyesi.
- Testnet ve düşük tutarlı mainnet pilotu.

Canlı büyük hacimli ödeme almak için P0 maddeleri tamamlanmalı.

### Wallet provider olarak

Gerçekçi konum: Developer preview / internal pilot.

Wallet provider olarak en kritik eksik custody güvenliği. Wallet üretimi ve deposit detection tek başına wallet provider olmak için yetmez. Wallet provider fon tutar, transfer imzalar, charge/dispute/refund/payout yönetir, ledger ile on-chain rezervi günlük doğrular, anahtarları operatörün process memory'sine çıkarmadan imzalar.

Bu codebase o hedefe doğru ilerliyor, ama bugün production custody ürünü değildir.

### Binance ayarında borsa wallet tracking olarak

Gerçekçi konum: Hazır değil.

Bir borsa, milyonlarca adres ve yüksek hacimli deposit/withdrawal akışında "kaçırmama" garantisi ister. Mevcut listener mimarisi tek prosesli, düşük batch limitli ve geçmiş tarama/reorg bakımından yetersiz. Borsa ölçeği için ayrı indexer servisi, durable queue, block hash ledger, reorg rollback ve partitioned address matching şart.

## Kabul kriterleri

Payment gateway production-ready diyebilmek için:

- P0 signer, reorg, durable queue, ledger reconciliation, withdrawal policy maddeleri tamam.
- Tüm supported chainlerde testnet E2E: payment create -> deposit -> finality -> ledger -> webhook.
- Mainnet canary: küçük limitli, otomatik alertli, rollback runbooklu.
- Webhook replay ve dead-letter operator ekranı.
- Price oracle staleness ve fallback policy.
- API contract tests CI'da zorunlu.
- `go test ./...`, `go vet ./...`, race/concurrency kritik testleri CI'da zorunlu.

Wallet provider production-ready diyebilmek için:

- KMS/HSM/MPC signer aktif.
- Private key/mnemonic application process içinde tutulmuyor.
- Deposit, sweep, withdrawal durable job state machine ile çalışıyor.
- Per-wallet/per-chain nonce ve UTXO reservation var.
- Hot/warm/cold wallet policy ve emergency freeze var.
- Daily asset liability/reconciliation raporu var.
- Admin actions immutable audit log ile izleniyor.
- KYT/AML/sanctions entegrasyonu veya ürün kapsamı dışında olduğuna dair yazılı policy var.

Binance ayarında tracking diyebilmek için:

- Chain indexer yatay ölçekleniyor.
- Configured historical start block'tan deterministic backfill yapıyor.
- Reorg simulation testleri geçiyor.
- Address lookup partitioned ve milyonlarca adresle benchmark edilmiş.
- Node failover/quorum var.
- Event bus durable.
- Block lag ve missed block alertleri var.
- Ledger drift zero-tolerance alertleri var.

## Önerilen roadmap

### İlk 2 hafta

1. External signer interface tasarla: `SignEVM`, `SignBitcoin`, `SignTRON`, `SignSolana` gibi chain-specific request/response modelleri.
2. Auto-sweep'i durable job table'a taşı. Durum: tamamlandı.
3. Listenerlara configurable start block ekle. Durum: tamamlandı; range replay worker hâlâ açık.
4. Ledger invariant checker ekle. Durum: temel checker tamamlandı; kapsamlı invariant testleri hâlâ açık.
5. Webhook event schema `v1` olarak dondur. Durum: payload/header tamamlandı; contract test ve katalog hâlâ açık.

### 30 gün

1. Reorg tracking: transaction reorg status, ledger reversal ve correction webhook temeli tamamlandı; block hash chain ve rollback window açık.
2. Nonce/UTXO reservation: EVM nonce manager, Bitcoin UTXO lock, stuck tx replacement.
3. Sweep state machine: pending, processing, succeeded, failed, dead-letter. Durum: temel tamamlandı; prefunding parent-job attempt/error/category state eklendi, ayrı child-job ve operator UI açık.
4. Reconciliation job: ledger invariant job temel tamamlandı; on-chain reserve/user wallet balance karşılaştırması açık.
5. Observability: Prometheus metrics ve alert rules.

### 60 gün

1. KMS/HSM/MPC signer entegrasyonu ilk chainlerde canlı.
2. Payout policy engine: limits, whitelist, dual approval, freeze.
3. Chain indexer servis ayrımı için event contract ve DB boundary.
4. Large wallet set benchmark: address matching, listener lag, DB indexes.
5. API versioning/deprecation ve SDK/integration guide hardening.

### 90 gün

1. Multi-chain production signer tüm supported chainlere yayılmış.
2. Reorg/backfill/failover chaos testleri CI ve staging'de.
3. Merchant settlement ve liability reporting.
4. AML/KYT/compliance entegrasyonları veya ürün kapsam dokümanı.
5. Controlled production launch için operational runbook ve incident drills.

## Sonuç

Bu codebase payment gateway için sağlam bir MVP tabanı oluşturmuş. Özellikle payment session, wallet üretimi, idempotency, HMAC auth, ledger entryleri, webhook ve payout/refund akışları doğru yönde.

Son düzeltmelerden sonra durable sweep, webhook retry, event versioning, configurable listener start block, reorg ledger reversal ve ledger invariant reconciliation temeli güçlendi. Buna rağmen ürün hâlâ "kusursuz" veya "Binance seviyesinde wallet tracking/custody" seviyesinde değildir. En kritik açıklar production signer, canonical rollback-window indexer, durable chain event bus, withdrawal risk policy, advanced fee/nonce/UTXO yönetimi ve on-chain reserve reconciliation tarafında kalır. Bu maddeler tamamlanmadan yüksek hacimli canlı müşteri fonu alınmamalı ve ürün "wallet provider custody" olarak pazarlanmamalı.
