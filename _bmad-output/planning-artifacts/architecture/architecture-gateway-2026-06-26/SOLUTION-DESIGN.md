# Gateway Payment & Wallet Platform - Solution Design

Tarih: 2026-06-26  
Kitle: teknik ekip, entegrasyon ortağı, ürün sahibi  
Kaynak spine: `ARCHITECTURE-SPINE.md`

## Kısa Karar

Gateway iki ürün yüzeyi sunar:

- E-ticaret siteleri için kripto ödeme geçidi: checkout, static address, payment lifecycle, webhook.
- Borsalar için wallet altyapısı: user wallet, deposit, balance, withdrawal, sweep, reconciliation.

Bu iki ürün ayrı ledger veya ayrı chain takip sistemi kurmayacak. Ortak money core kullanılacak: Wallet, Chain Indexer, Deposit, Ledger, Signer, Webhook ve Reconciliation.

## Neden Hemen Mikroservis Değil

Para hareketi olan sistemlerde servis ayrımı ancak sahiplik ve idempotency netse güvenlidir. Bu nedenle ilk hedef modüler monolith:

1. Mevcut Go monolith korunur.
2. İç sınırlar servis gibi davranır.
3. Her sınır kendi verisine sahip olur.
4. Para eventleri Postgres outbox ile durable hale gelir.
5. Worker ve servis ayrımı daha sonra yapılır.

## Ana Sınırlar

| Boundary | Sorumluluk |
| --- | --- |
| Tenant / Domain | Merchant, exchange tenant, API key, webhook subscription |
| Wallet | HD index, address üretimi, address ownership |
| Chain Indexer | Block/slot tarama, tx/log tespiti, finality, reorg |
| Deposit | Chain event -> wallet match -> deposit lifecycle |
| Ledger | Tek balance kaynağı, pending/available/hold/transit/reversal |
| Payment Session | Checkout, invoice, quote snapshot, payment status |
| Withdrawal / Sweep | Payout, refund, sweep, hold, approval, broadcast state |
| Signer | KMS/HSM/MPC/Vault gibi dış signer arayüzü |
| Webhook | Delivery, retry, replay, dead-letter, HMAC signing |
| Reconciliation | Chain-ledger drift, reorg, stuck tx, replay recovery |

## Para Akışı

Deposit:

1. Chain Indexer tx/log tespit eder.
2. Event outbox'a yazılır.
3. Deposit boundary wallet ile eşleştirir.
4. Finality tamamlanınca Ledger credit yazar.
5. Payment varsa session `paid/failed/expired` lifecycle'a girer.
6. Webhook boundary merchant veya exchange callback'ini gönderir.

Withdrawal / sweep:

1. API/Admin withdrawal, refund veya sweep talebi oluşturur.
2. Ledger hold/reservation olmadan imza atılmaz.
3. Nonce/UTXO/resource reservation alınır.
4. Signer boundary transaction imzalar.
5. Broadcast sonrası Chain Indexer finality izler.
6. Ledger debit/finalize veya release yazar.
7. Webhook terminal sonucu bildirir.

## Event Kontratı

Yeni hedef event adları dotted ve versiyonlu olacak:

- `deposit.detected.v1`
- `deposit.finalized.v1`
- `payment.succeeded.v1`
- `payment.failed.v1`
- `payment.expired.v1`
- `withdrawal.requested.v1`
- `withdrawal.broadcast.v1`
- `withdrawal.finalized.v1`
- `withdrawal.failed.v1`
- `refund.succeeded.v1`
- `sweep.succeeded.v1`
- `transaction.reorged.v1`

Mevcut `payment_succeeded`, `payment_failed`, `payment_expired` gibi underscore webhook adları compatibility alias olarak kalır. Bunlar event catalog migration ile resmi şekilde deprecate edilmeden kırılmaz.

## Production Bloklayıcıları

Gerçek müşteri fonu ve borsa ölçeği için şu başlıklar tamamlanmadan production açılmamalı:

- Gerçek external signer: KMS/HSM/MPC/Vault veya seçilecek custody signer.
- Ledger-only balance ve DB-level ledger invariants.
- Postgres outbox ve idempotent event consumers.
- Full reorg accounting: parent/child block tracking, rollback window, correction events.
- Nonce/UTXO reservation ve stuck transaction recovery.
- Webhook event catalog, retry diagnostics, replay UI/API.
- Continuous reconciliation: chain balance, ledger balance, sweep, withdrawal, webhook drift.
- Production operations: versioned migrations, metrics, tracing, alerts, runbooks, backup/restore drill.

## Faz Planı

### Faz 0 - Monolith Hardening

- Ledger authority kurallarını enforce et.
- Webhook delivery'yi source flow'lardan ayır.
- Confirmation/finality gate'i ödeme lifecycle'a bağla.
- Reorg reversal ve reconciliation job kapsamını tamamla.
- Payout/refund/sweep lifecycle webhook'larını ekle.

### Faz 1 - Modular Monolith

- `internal/wallet`, `internal/deposit`, `internal/ledger`, `internal/payment`, `internal/withdrawal`, `internal/webhook`, `internal/reconciliation`, `internal/signer` sınırlarını oluştur.
- Postgres outbox ekle.
- Money event contract testleri yaz.
- Balance API'lerini Ledger kaynaklı hale getir.

### Faz 2 - Worker Ayrımı

- Webhook worker.
- Chain indexer worker.
- Reconciliation worker.
- Pricing worker.

### Faz 3 - Custody Ayrımı

- Signer external hale gelir.
- Ledger service boundary sertleşir.
- Withdrawal service signer ve ledger ile kontrat üzerinden çalışır.
- Hot/warm/cold custody policy eklenir.

### Faz 4 - Exchange-Grade Ölçek

- Per-chain sharded indexer.
- Provider health scoring ve archive/quorum stratejisi.
- Distributed rate limit ve queue.
- Reconciliation dashboard.
- SLO ve on-call operasyon modeli.

## Net Sonuç

Mevcut repo e-ticaret payment gateway MVP için iyi bir taban. Borsa wallet provider hedefi de doğru yönde başlamış, fakat production custody ürünü olması için signer, ledger invariants, durable eventing, reconciliation, operational gates ve withdrawal güvenliği tamamlanmalı.
