---
project: gateway
epic: 3
date: 2026-06-27
status: complete
---

# Epic 3 Documentation Verification

Bu belge, Epic 3 retrospektifi sonrasinda implementation learning'lerine gore guncellenmesi gerekebilecek dokumanlari listeler ve her biri icin kod ile karsilastirma sonucunu kaydeder.

## Verification Method

- Dokumanlar okundu.
- Story File List ve completion notes uzerinden ilgili kod dosyalari belirlendi.
- Kodda model, repository, handler, worker, event catalog ve schema guard davranislari `rg` ve dogrudan dosya okumayla kontrol edildi.
- Sadece dogrulanmis discrepancy icin dokuman guncellendi.

## Results

| Dokuman | Kod karsilastirmasi | Karar |
| --- | --- | --- |
| `docs/chain-fact-boundary.md` | `main.go` `handleChainIndexerEvent` sadece `recordChainFactObservation` cagiriyor; `repositories/chain_fact_repo.go`, `models/chain_fact.go`, `workers/listeners/config.go` event id/start block davranisini destekliyor. | Guncelleme yok. |
| `docs/deposit-finality-boundary.md` | `services/deposits/service.go` chain fact -> deposit -> finality -> ledger/payment akisini ayri worker path'inden yurutuyor; `repositories/deposit_repo.go` `deposit.finalized.v1` outbox kaydini uretiyor. | Guncelleme yok. |
| `docs/ledger-balance-authority.md` | `api/handlers/dealer.go`, `api/handlers/v1api.go`, `repositories/ledger_repo.go` ve handler/source contract testleri ledger-only balance rule ile uyumlu. | Guncelleme yok. |
| `docs/money-event-catalog.md` | `constants/webhook_events.go` ve `services/webhook/event_catalog.go` canonical/alias event listesini, payment mismatch events'i, payout alias'larini ve reorg correction fields'i destekliyor. | Guncelleme yok. |
| `docs/integration-guide.md` | V1 request signing, payment statuses, mismatch outcomes, payout/refund/sweep events ve reorg correction aciklamalari `helpers.GenerateRequestSignature`, `models.PaymentSession`, `constants/webhook_events.go` ve webhook catalog ile uyumlu. | Guncelleme yok. |
| `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md` | Epic 3 kodu AD-3, AD-4, AD-5, AD-8, AD-9 ve AD-11 ile uyumlu; Epic 4 icin AD-7 hala planli is olarak duruyor. | Guncelleme yok. |
| `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/SOLUTION-DESIGN.md` | Deposit, ledger, webhook, reconciliation ve production blockers bolumleri mevcut kod ve kalan Epic 4/5 kapsamiyla uyumlu. | Guncelleme yok. |
| `_bmad-output/planning-artifacts/prds/prd-gateway-2026-06-27/prd.md` | FR11-FR18 ve FR32-FR34 Epic 3 implementation'iyle uyumlu; FR19+ outbound gate'ler halen Epic 4 scope'unda. | Guncelleme yok. |
| `readme.md` | README V1 HMAC'i `timestamp + raw_body` gibi gosteriyordu, fakat kod `helpers.GenerateRequestSignature` ile `METHOD\npath?query\ntimestamp\nraw_body` kullaniyor. README worker flow'u da dispatcher'in dogrudan transaction/payment/webhook mutate ettigini soyluyordu, fakat `main.go` chain fact-first ve deposit worker path'ini kullaniyor. Database table listesi de `blocks`, `chain_facts`, `deposits`, `money_event_outboxes`, `sweep_jobs` tablolarini kaciriyordu. | Guncellendi. |

## Applied Updates

- `readme.md` V1 signed request header aciklamasi canonical method/path/timestamp/body semantigine cekildi.
- `readme.md` database model listesine `blocks`, `chain_facts`, `deposits`, `money_event_outboxes`, `sweep_jobs` eklendi.
- `readme.md` worker flow'u chain fact-first listener path, deposit fact worker, ledger/payment/outbox progression ve reconciliation worker ile uyumlu hale getirildi.

## Discarded Proposed Updates

- Architecture spine guncellemesi: kod mevcut AD kararlarini bozmadigi ve outbound/signing kararlarini zaten Epic 4/5'e biraktigi icin uygulanmadi.
- PRD guncellemesi: Epic 3 implementation'i PRD FR/NFR maddelerini degistirmedi; kalan outbound/production gate'ler PRD'de zaten acik.
- Money event catalog guncellemesi: `payment.underpaid.v1`, `payment.overpaid.v1`, `payment.partial_paid.v1`, `transaction.reorged.v1`, payout aliases ve correction relation fields kodla uyumlu.
- Integration guide guncellemesi: HMAC, payment outcome, payout/refund/sweep event ve reorg correction metinleri kodla uyumlu.
- Boundary docs guncellemesi: chain fact, deposit finality ve ledger authority dokumanlari implementation ile uyumlu.
