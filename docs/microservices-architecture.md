# Gateway Microservice Architecture

Bu doküman, mevcut Gateway kod tabanının borsa entegrasyonu için güvenli şekilde mikroservis mimarisine taşınacak hedef yapısını tanımlar. Amaç, her kullanıcı için wallet üretmek, depositleri izlemek, withdrawal yapmak, ledger bakiyesini doğru tutmak ve borsaya güvenilir API/event kontratları sağlamaktır.

## Temel Hedef

Gateway, borsa çekirdeği için custody/payment altyapısı olarak çalışır:

- Her borsa kullanıcısı için zincir/asset bazlı deposit adresi üretir veya mevcut adresi döner.
- On-chain depositleri izler, finality sonrası ledger bakiyesine işler.
- Withdrawal isteklerini idempotent şekilde alır, risk/approval/hold akışından geçirir, imzalar ve broadcast eder.
- Merchant/payment session kullanımını destekler, ancak ledger ve wallet sahipliği tek merkezde kalır.
- Borsaya API ve event/webhook ile durum bildirir.

## Servis Sınırları

| Servis | Sorumluluk | Sahip Olduğu Veri |
|---|---|---|
| API Gateway / BFF | Public REST API, auth, API key validation, request schema, idempotency envelope, rate limit | API request log, idempotency keys |
| Identity & Admin | Admin kullanıcıları, TOTP, operator rolleri, merchant/exchange tenant erişimi | admins, roles, sessions, audit logs |
| Merchant / Tenant Service | Merchant/domain/API key/webhook ayarları | merchants, domains, api_keys |
| Wallet Service | Kullanıcı ve ödeme oturumu wallet üretimi, address ownership, HD index yönetimi | wallets, hd_accounts, address_indexes |
| Signer Service | KMS/HSM/Vault üzerinden transaction imzalama; mnemonic veya private key app DB'de durmaz | key references, signing policies |
| Chain Indexer Service | Zincir başına blok tarama, tx/log tespiti, chain state, reorg detection | chain_states, raw_chain_events |
| Deposit Service | Indexer eventlerini wallet ile eşleştirme, confirmation gate, payment/deposit state | transactions, deposit_attempts |
| Ledger Service | Double-entry ledger, available/pending/hold bakiyeleri, balance API | ledger_entries, ledger_accounts |
| Payment Session Service | Invoice/checkout/payment link oturumları, quote snapshot, payment status | payment_sessions, price_quotes |
| Withdrawal Service | Withdrawal request, hold, approval, nonce, broadcast, finalization | withdrawal_requests, withdrawal_txs |
| Webhook Service | Webhook delivery, retry, replay, signing, per-attempt log | webhook_deliveries |
| Pricing Service | Fiat/crypto quote, staleness guard, quote snapshot | price_quotes, price_sources |
| Reconciliation Service | Rescan, missed tx detection, ledger-chain reconciliation reports | reconciliation_jobs, mismatch_reports |

## Kritik Veri Sahipliği Kuralları

- Wallet adreslerini sadece Wallet Service üretir ve sahiplenir.
- Ledger bakiyesini sadece Ledger Service değiştirir.
- Chain Indexer business status yazmaz; sadece ham zincir eventleri üretir.
- Deposit Service, confirmed deposit eventinden sonra Ledger Service'e idempotent credit komutu gönderir.
- Withdrawal Service, Ledger Service'ten hold almadan imza/broadcast yapamaz.
- Signer Service private key veya mnemonic'i HTTP response, log veya app DB'ye vermez.
- Webhook Service, gönderimden hemen önce webhook URL'ini tekrar doğrular.
- Payment Session Service fiyatı session anında snapshot olarak kaydeder; sonradan oracle fiyatıyla geçmiş session yeniden hesaplanmaz.

## Event Kontratları

Fiziksel ayrıştırmada Kafka, NATS JetStream, RabbitMQ veya Postgres outbox kullanılabilir. İlk aşamada Postgres outbox yeterlidir.

| Event | Producer | Consumer | Idempotency Key |
|---|---|---|---|
| `wallet.created.v1` | Wallet Service | Exchange, Deposit Service | `wallet_id` |
| `chain.tx.detected.v1` | Chain Indexer | Deposit Service | `chain_id:tx_hash:log_index` |
| `chain.tx.confirmed.v1` | Chain Indexer | Deposit Service | `chain_id:tx_hash:log_index:confirmed_block` |
| `deposit.detected.v1` | Deposit Service | Webhook, Exchange | `transaction_id` |
| `deposit.finalized.v1` | Deposit Service | Ledger, Webhook, Exchange | `transaction_id:finalized` |
| `ledger.entry.posted.v1` | Ledger Service | Exchange, Reporting | `ledger_entry_id` |
| `payment.paid.v1` | Payment Session Service | Webhook, Exchange | `payment_session_id:paid` |
| `withdrawal.requested.v1` | Withdrawal Service | Risk/Admin | `withdrawal_id` |
| `withdrawal.approved.v1` | Withdrawal Service | Signer Service | `withdrawal_id:approved` |
| `withdrawal.broadcast.v1` | Withdrawal Service | Webhook, Exchange | `withdrawal_id:tx_hash` |
| `withdrawal.finalized.v1` | Withdrawal Service | Ledger, Webhook, Exchange | `withdrawal_id:finalized` |
| `webhook.delivery.failed.v1` | Webhook Service | Ops/Replay | `delivery_id:attempt` |

## Borsa Entegrasyon API'leri

Gateway borsaya tek bir stable API sunmalı; iç servislerin sayısı borsayı etkilememeli.

### Wallet

- `POST /api/v1/users/{user_id}/wallets`
  - Kullanıcı için asset/chain bazlı deposit wallet üretir veya mevcut wallet'ı döner.
  - Idempotency key zorunlu olmalı.
- `GET /api/v1/users/{user_id}/wallets`
  - Kullanıcının aktif deposit adreslerini listeler.

### Deposit

- `GET /api/v1/users/{user_id}/deposits`
  - Pending, confirmed, finalized deposit geçmişini döner.
- `GET /api/v1/deposits/{deposit_id}`
  - Tek deposit durumunu döner.

### Balance

- `GET /api/v1/users/{user_id}/balances`
  - Sadece Ledger Service kaynaklı available/pending/hold bakiyeleri döner.
  - Zincir tarama tablosu veya raw transaction tablosundan balance hesaplanmaz.

### Withdrawal

- `POST /api/v1/withdrawals`
  - Idempotent withdrawal talebi açar.
  - Ledger hold başarılı değilse request kabul edilmez.
- `GET /api/v1/withdrawals/{withdrawal_id}`
  - Request, approval, broadcast ve finality durumlarını döner.

### Webhook

- `deposit.detected`
- `deposit.finalized`
- `payment.paid`
- `withdrawal.broadcast`
- `withdrawal.finalized`
- `withdrawal.failed`

Her webhook şu headerları taşımalı:

- `X-Gateway-Event`
- `X-Gateway-Event-Id`
- `X-Gateway-Timestamp`
- `X-Gateway-Signature`

## Deposit Akışı

1. Exchange, kullanıcı için wallet ister.
2. Wallet Service deterministic HD index ile adres üretir ve `wallet.created.v1` eventini yayınlar.
3. Chain Indexer zinciri tarar, wallet adresine gelen tx/log için `chain.tx.detected.v1` üretir.
4. Deposit Service tx'yi wallet ile eşleştirir, duplicate kontrolü yapar ve depositi `detected` yazar.
5. Gerekli confirmation sayısı tamamlanınca `deposit.finalized.v1` üretilir.
6. Ledger Service pending/available entrylerini double-entry olarak işler.
7. Webhook Service borsaya deposit finalized bildirir.

Deposit invariantları:

- Aynı `chain_id + tx_hash + log_index` ikinci kez ledger credit oluşturamaz.
- Finality tamamlanmadan available balance artmaz.
- Reorg tespit edilirse ilgili deposit `reorged` olur ve ledger reversal entry yazılır.
- Payment link session için wallet scope session bazında tekil olmalıdır; aynı statik adres farklı açık invoice'ları kapatmamalıdır.

## Withdrawal Akışı

1. Exchange `POST /withdrawals` ile talep açar.
2. Withdrawal Service idempotency key ile duplicate request'i engeller.
3. Ledger Service available bakiyeden hold alır.
4. Risk/admin policy uygunsa withdrawal `approved` olur.
5. Signer Service transaction imzalar; private key dışarı çıkmaz.
6. Withdrawal Service nonce ve broadcast işlemini yönetir.
7. Chain Indexer broadcast tx'yi izler.
8. Finality tamamlanınca Ledger Service hold'u kapatır ve withdrawal finalized entry yazar.
9. Webhook Service sonucu borsaya bildirir.

Withdrawal invariantları:

- Hold yoksa imza yok.
- Available balance negatif olamaz.
- Aynı idempotency key ikinci withdrawal oluşturamaz.
- Broadcast edilmiş tx kaybolursa reconciliation job tekrar kontrol eder; kör retry yeni tx üretmez.
- Nonce yönetimi chain/account bazında tek sahipli olmalıdır.

## Veritabanı Ayrıştırma Stratejisi

İlk adım fiziksel DB ayırmak değil, tablo sahipliğini netleştirmektir.

| Şema | Tablolar |
|---|---|
| `identity` | admins, roles, admin_sessions, activity_logs |
| `tenant` | merchants, domains, api_keys |
| `wallet` | wallets, hd_accounts, address_indexes |
| `chain` | chain_states, raw_chain_events, reorg_events |
| `deposit` | transactions, deposit_attempts |
| `ledger` | ledger_entries, ledger_accounts |
| `payment` | payment_sessions, price_quotes |
| `withdrawal` | withdrawal_requests, withdrawal_transactions |
| `webhook` | webhook_deliveries, webhook_subscriptions |

Kural: Bir servis başka servisin tablosuna doğrudan yazmaz. Monolit içinde bile bu kural repository boundary ile uygulanmalıdır.

## Geçiş Planı

### Faz 0: Monolit Sertleştirme

- Env fallback admin login kapatılır.
- Pasif merchant login/API erişimi engellenir.
- Payment link wallet reuse kaldırılır.
- Balance endpoint sadece ledger available hesaplarını döner.
- Webhook URL delivery anında yeniden doğrulanır.
- CSRF, rate limit, confirmation gate, quote snapshot ve reorg reversal tamamlanır.

### Faz 1: Modüler Monolit

- `internal/wallet`, `internal/deposit`, `internal/ledger`, `internal/withdrawal`, `internal/webhook` paket sınırları oluşturulur.
- Repository erişimi servis sahipliğine göre ayrılır.
- Postgres outbox tablosu eklenir.
- Tüm para hareketleri outbox + idempotency ile çalışır.

### Faz 2: İlk Fiziksel Servisler

Önce stateless ve düşük riskli parçalar ayrılır:

1. Webhook Worker
2. Chain Indexer Worker
3. Pricing Worker
4. Reconciliation Worker

Bu servisler ayrıldıktan sonra ledger ve wallet sahipliği hâlâ monolitte kalabilir.

### Faz 3: Para Sahipliği Ayrımı

1. Ledger Service ayrılır.
2. Wallet Service ayrılır.
3. Signer Service KMS/HSM arkasına alınır.
4. Withdrawal Service signer ve ledger ile kontrat üzerinden çalışır.

Bu fazdan önce integration test ve staging replay yapılmadan production geçiş yapılmamalıdır.

### Faz 4: Borsa Ölçeği

- Per-chain indexer autoscaling.
- Redis/NATS/Kafka tabanlı distributed rate limit ve queue.
- Multi-region read replica.
- Ledger reconciliation dashboard.
- On-call alerting: indexer lag, webhook failure rate, withdrawal stuck, ledger mismatch.

## Production Öncesi Bloklayıcılar

- Admin/merchant formlarına CSRF token eklenmeli.
- Chain confirmation gate ödeme status akışına bağlanmalı.
- Reorg reversal ledger entryleri yazılmalı.
- Withdrawal nonce ve hold akışı integration test ile kapatılmalı.
- Mnemonic/private key env yerine KMS/HSM/Vault referansı ile yönetilmeli.
- API key rate limit per-domain/per-key olmalı.
- Price quote session anında saklanmalı ve TTL ile korunmalı.
- Ledger DB constraintleri negative available balance ve duplicate posting'i engellemeli.

## Kısa Karar

Bu codebase doğrudan "servislere böl" şeklinde parçalanmamalı. Önce para hareketi invariantları monolit içinde netleşmeli, sonra outbox/event kontratları eklenmeli, en son servisler fiziksel olarak ayrılmalıdır. Aksi halde mikroservis mimarisi hata sayısını azaltmaz; dağıtık para kaybı riskini artırır.
