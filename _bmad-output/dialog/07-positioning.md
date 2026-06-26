# Positioning

**Project:** gateway  
**Date:** 2026-06-27  
**Status:** confirmed

---

## Source Context

Positioning was synthesized from `_bmad-output` artifacts, per product-owner instruction to ask only for information that cannot be derived from that folder.

Primary sources:

- `_bmad-output/planning-artifacts/prds/prd-gateway-2026-06-27/prd.md`
- `_bmad-output/planning-artifacts/ux-designs/ux-gateway-2026-06-27/DESIGN.md`
- `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/SOLUTION-DESIGN.md`
- `_bmad-output/A-Product-Brief/project-brief.md`

## Opening Frame

The product was introduced as a B2B crypto payment gateway and wallet-provider infrastructure platform, not as a generic dealer/admin portal.

## Positioning Components

- **Target Customer:** Merchant/dealer ekipleri, ödeme entegrasyonu yapan geliştiriciler ve küçük/orta ölçekli wallet/exchange platformları.
- **Their Need:** Kripto ödeme almak, kullanıcı bazlı multi-chain deposit wallet üretmek, payment/deposit/withdrawal/refund/sweep lifecycle'ını izlenebilir ve reconcile edilebilir şekilde yönetmek.
- **Product Category:** B2B crypto payment gateway + wallet-provider infrastructure platform.
- **Key Benefit:** Ekipler checkout, static wallet, ledger, webhook, reconciliation ve admin recovery akışlarını sıfırdan kurmadan kontrollü şekilde canlıya çıkabilir.
- **Alternatives:** Checkout-only crypto payment processor'lar, kendi wallet/indexer/ledger altyapısını içeride kurmak, manuel wallet operasyonu, ya da daha ağır exchange/custody altyapıları.
- **Differentiator:** Gateway sadece checkout değil; ödeme, wallet, deposit tracking, ledger, withdrawal/refund/sweep, webhook, reconciliation ve admin recovery süreçlerini tek shared money core'da birleştirir. Wallet-provider/custody iddiasını signer, compliance, reconciliation ve observability gate'leri kapanmadan abartmaz.

## Reflection Checkpoint

The synthesized positioning was presented to the product owner for confirmation.

Product owner response:

> evett

## Final Positioning Statement

Gateway, kripto ödeme almak ve multi-chain wallet altyapısı sunmak isteyen merchant/dealer ekipleri ile wallet/exchange platformları için bir B2B crypto payment gateway ve wallet-provider altyapısıdır. Checkout-only çözümlerden veya içeride sıfırdan kurulan dağınık wallet operasyonlarından farklı olarak, payment lifecycle, static wallet, ledger, webhook, reconciliation ve admin recovery süreçlerini tek shared money core üzerinde yönetir.

## Strategic Rationale

Mevcut PRD ve architecture çıktıları ürünü iki yüzeyli, tek money core'lu bir para altyapısı olarak tanımlar. Bu positioning, kısa vadeli kontrollü merchant/dealer beta ile orta vadeli wallet-provider hedefini aynı hikayede tutar, ama production custody ve exchange-grade iddiasını readiness gate'lerine bağlar.
