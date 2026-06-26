# Gateway Remaining Delivery Stages

Tarih: 2026-06-26  
Kaynak: `ARCHITECTURE-SPINE.md` ve `SOLUTION-DESIGN.md`

## Karar

Kalan iş, mikroservise bölme projesi olarak değil, önce modüler monolith hardening olarak yürütülecek. Amaç mevcut kodun merchant payment gateway MVP tabanını production-grade wallet provider çekirdeğine yaklaştırmak.

## Faz 0 - Monolith Hardening

Amaç: gerçek para hareketlerinde veri kaybı, double credit, duplicate withdrawal ve callback drift riskini düşürmek.

Epikler:

- Ledger authority hardening
- Webhook boundary and event catalog
- Confirmation/finality and reorg completion
- Payout/refund/sweep lifecycle events
- Production migration and observability baseline

Exit criteria:

- Balance endpointleri Ledger-derived çalışır.
- Payment/deposit/withdrawal/refund/sweep eventleri idempotent ve versioned olur.
- Inline webhook delivery para akışlarından ayrılır.
- Reorg correction rollback-window ve reconciliation ile tamamlanır.
- Para akışları için temel metrics/logging/alerts eklenir.

## Faz 1 - Modular Monolith

Amaç: servisleri fiziksel olarak ayırmadan ownership boundaries kurmak.

Epikler:

- `internal/wallet`, `internal/deposit`, `internal/ledger`, `internal/payment`, `internal/withdrawal`, `internal/webhook`, `internal/reconciliation`, `internal/signer` sınırları
- Postgres outbox
- Idempotent outbox consumers
- Money event contract tests
- Ledger projection / balance query layer

Exit criteria:

- Her money boundary kendi owned tablolarına yazar.
- Cross-boundary hareketler command/event kontratından geçer.
- Outbox replay ve duplicate delivery testleri geçer.

## Faz 2 - Worker Split

Amaç: düşük riskli workerları ana app sürecinden ayırmak.

Öncelik:

1. Webhook worker
2. Reconciliation worker
3. Pricing worker
4. Chain indexer worker

Exit criteria:

- Workerlar aynı outbox/event kontratlarını kullanır.
- Retry, lock, dead-letter ve lag metrikleri vardır.
- Main app içinde fallback path para kaybı üretmez.

## Faz 3 - Custody Split

Amaç: production wallet provider için private key/mnemonic görünürlüğünü uygulamadan çıkarmak.

Epikler:

- External signer interface
- Signer audit log
- Withdrawal hold/reservation hardening
- Nonce/UTXO/resource reservation
- Hot/warm/cold custody policy

Exit criteria:

- Production signer app process'e private key döndürmez.
- Withdrawal/sweep/refund imzaları policy context ve key reference ile atılır.
- Stuck tx recovery blind retry yapmaz; reconciliation-first çalışır.

## Faz 4 - Exchange-Grade Scale

Amaç: küçük/orta merchant gateway’den borsa wallet infrastructure seviyesine geçmek.

Epikler:

- Per-chain sharded indexer
- Archive/quorum provider strategy
- Address lookup partitioning
- Distributed rate limit and queue
- Reconciliation dashboard
- SLO/on-call operating model

Exit criteria:

- Chain lag, webhook lag, reconciliation drift ve stuck withdrawal görünürdür.
- Backfill ve reorg simulation testleri vardır.
- Büyük address setleri için partitioned lookup ve replay stratejisi kanıtlanır.

## İlk Uygulama Paketi

Başlangıç paketi Faz 0'dan seçilmeli:

1. Webhook event catalog ve payout/refund/sweep lifecycle webhookları.
2. Ledger-derived balance hardening ve DB-level invariant testleri.
3. Confirmation/finality gate + reorg rollback-window tamamlaması.
4. Production observability baseline.

Bu paket tamamlanmadan worker split veya custody split başlatılmamalı.
