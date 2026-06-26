# Vision Capture

**Project:** gateway
**Date:** 2026-06-27
**Status:** confirmed

---

## Opening Context

The product owner clarified that the project is a crypto currency wallet provider and payment gateway. Existing PRD, UX, architecture, and readiness-audit artifacts were used as grounding context.

Opening frame:

> Existing documents point to two surfaces on one money core: merchant/dealer payment gateway and wallet-provider-as-a-service. The goal is to clarify what this should become, not to invent a new product from zero.

## Key Exchanges

1. The initial "bayi/admin" framing was corrected by the product owner: the actual domain is crypto currency wallet provider and payment gateway.
2. The product owner confirmed the design work serves multiple pressures: live readiness, merchant/partner acquisition, wallet-provider growth, investor/partner readiness, and productizing the existing platform.
3. Based on repo evidence, the short-term surface was framed as controlled merchant/dealer beta; wallet-provider growth was framed as gated by custody, signer, reconciliation, compliance, observability, and operational readiness.
4. The product owner confirmed the synthesized vision as correct.

## Reflection Checkpoint

Synthesized back to the product owner:

> Gateway, merchant'ların kripto para ile ödeme almasını ve geliştiricilerin/wallet ürünlerinin kullanıcı bazlı multi-chain wallet altyapısı kullanmasını sağlayan ciddi bir para altyapısı olacak. Kısa vadede kontrollü merchant/dealer beta ile ödeme alma, checkout, statik adres, webhook, ledger ve admin operasyonlarını ürünleştirmek; orta vadede signer, reconciliation, compliance ve observability olgunlaştıkça wallet-provider tarafını güvenilir şekilde büyütmek istiyorsun.
>
> Farkı "sadece checkout sayfası" değil: ödeme, wallet, deposit tracking, withdrawal/refund/sweep, webhook, ledger ve admin recovery süreçlerini tek para çekirdeğinde birleştiren altyapı olması.

Product owner confirmation:

> dogru

## Final Vision Statement

Gateway will become a serious crypto currency wallet provider and payment gateway platform: one shared money core that lets merchants accept crypto payments while also giving wallet and exchange-style products reliable multi-chain wallet infrastructure.

In the short term, the platform should support a controlled merchant/dealer beta with hosted checkout, static deposit addresses, payment sessions, webhooks, ledger-backed balances, and admin operations. In the medium term, the wallet-provider surface grows only as custody, external signer, reconciliation, compliance, observability, and operational gates become strong enough for real-funds trust.

## Key Insights Captured

- Two connected product surfaces: merchant/dealer payment gateway and wallet-provider-as-a-service.
- Shared money core is the main product idea.
- Controlled launch is central; UX must not overstate custody or wallet-provider maturity.
- Admin/operator design must expose risky money-state workflows clearly.
- Merchant/dealer design must make integration, payment status, wallet/static address use, and webhook diagnostics understandable.
