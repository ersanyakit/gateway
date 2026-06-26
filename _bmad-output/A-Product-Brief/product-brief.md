# Project Brief: gateway

> Complete Strategic Foundation

**Created:** 2026-06-27
**Author:** ersan
**Brief Type:** Complete
**Status:** in progress

---

## Vision

Gateway will become a serious crypto currency wallet provider and payment gateway platform: one shared money core that lets merchants accept crypto payments while also giving wallet and exchange-style products reliable multi-chain wallet infrastructure.

In the short term, the platform should support a controlled merchant/dealer beta with hosted checkout, static deposit addresses, payment sessions, webhooks, ledger-backed balances, and admin operations. In the medium term, the wallet-provider surface grows only as custody, external signer, reconciliation, compliance, observability, and operational gates become strong enough for real-funds trust.

**Key Insights from Discussion:**
- The product is not a generic dealer/admin portal; it is crypto currency payment and wallet infrastructure.
- The product has two connected surfaces: merchant/dealer payment gateway and wallet-provider-as-a-service.
- The differentiating idea is the shared money core across checkout, wallet generation, deposit tracking, ledger, withdrawal/refund/sweep, webhooks, reconciliation, and admin recovery.
- Controlled launch matters: wallet-provider/custody claims must not outrun signer, compliance, reconciliation, and observability readiness.
- Design must make operational state visible instead of hiding money-state risk behind simple dashboard summaries.

---

## Positioning Statement

Gateway, kripto ödeme almak ve multi-chain wallet altyapısı sunmak isteyen merchant/dealer ekipleri ile wallet/exchange platformları için bir B2B crypto payment gateway ve wallet-provider altyapısıdır. Checkout-only çözümlerden veya içeride sıfırdan kurulan dağınık wallet operasyonlarından farklı olarak, payment lifecycle, static wallet, ledger, webhook, reconciliation ve admin recovery süreçlerini tek shared money core üzerinde yönetir.

**Breakdown:**

- **Target Customer:** Merchant/dealer ekipleri, ödeme entegrasyonu yapan geliştiriciler ve küçük/orta ölçekli wallet/exchange platformları.
- **Need/Opportunity:** Kripto ödeme almak, kullanıcı bazlı multi-chain deposit wallet üretmek, payment/deposit/withdrawal/refund/sweep lifecycle'ını izlenebilir ve reconcile edilebilir şekilde yönetmek.
- **Category:** B2B crypto payment gateway + wallet-provider infrastructure platform.
- **Key Benefit:** Ekipler checkout, static wallet, ledger, webhook, reconciliation ve admin recovery akışlarını sıfırdan kurmadan kontrollü şekilde canlıya çıkabilir.
- **Alternatives:** Checkout-only crypto payment processor'lar, kendi wallet/indexer/ledger altyapısını içeride kurmak, manuel wallet operasyonu, ya da daha ağır exchange/custody altyapıları.
- **Differentiator:** Gateway sadece checkout değil; ödeme, wallet, deposit tracking, ledger, withdrawal/refund/sweep, webhook, reconciliation ve admin recovery süreçlerini tek shared money core'da birleştirir. Wallet-provider/custody iddiasını signer, compliance, reconciliation ve observability gate'leri kapanmadan abartmaz.

**Strategic Rationale:**
Mevcut PRD ve architecture çıktıları ürünü iki yüzeyli, tek money core'lu bir para altyapısı olarak tanımlar. Bu positioning, kısa vadeli kontrollü merchant/dealer beta ile orta vadeli wallet-provider hedefini aynı hikayede tutar, ama production custody ve exchange-grade iddiasını readiness gate'lerine bağlar.

---

## Business Model

**Model:** B2B infrastructure platform

**Who pays:** Merchant/dealer tenants and wallet/exchange platform tenants that need crypto payment gateway or wallet-provider infrastructure.

**Who uses:**
- Merchant/dealer developers integrate payment sessions, static wallets, auth, idempotency, and webhooks.
- Merchant/dealer operations teams monitor payments, webhook delivery, refunds, payouts, and balances.
- Wallet/exchange operators use user wallet, deposit, balance, withdrawal, sweep, and reconciliation capabilities.
- Internal/admin operators manage readiness, provider health, reconciliation, recovery, approvals, and audit trails.
- Checkout payers interact with the hosted checkout, but they are not the direct Gateway buyer.

**Rationale:**
The product is partner-facing infrastructure with tenant/domain ownership, API keys, webhook subscriptions, ledger scopes, and admin/operator surfaces. Existing `_bmad-output` artifacts consistently describe merchant/dealer and wallet/exchange tenants as the business customers, while end users appear mainly as checkout payers or wallet end users served by those tenants.

**Implications:**
- Product design must separate buyer/admin surfaces from end-payer checkout surfaces.
- Merchant/dealer onboarding, integration docs, API credentials, webhook diagnostics, and operational trust are core to conversion and retention.
- Wallet-provider claims must be gated by external signer, reconciliation, compliance, observability, and custody readiness.
- Pricing and commercial packaging are not defined in `_bmad-output`; they remain a deferred commercial detail.

---

## Business Customer Profile

### Ideal Business Customers

1. **Controlled beta merchant/dealer tenant**
   - Small or mid-size merchant, dealer, or crypto-friendly commerce operation.
   - Needs hosted checkout, static deposit addresses, payment sessions, API credentials, webhook delivery, refund/payout visibility, and ledger-backed balance confidence.
   - Has enough technical capability to integrate API keys, HMAC signing, idempotency, and webhook callbacks.
   - Fits controlled beta limits: small tenant count, limited chain/token set, capped balances/withdrawals, manual oversight, and explicit launch gates.

2. **Small/mid wallet or exchange platform tenant**
   - Needs user wallet, deterministic deposit wallet, deposit detection, ledger-derived balance, withdrawal/sweep lifecycle, webhook events, and reconciliation.
   - Is not a high-volume regulated exchange-grade custody customer until signer, compliance, reconciliation, observability, and scale-readiness gates are complete.
   - Values operational transparency over superficial dashboard simplicity.

### Buying Roles

| Role | Description |
| --- | --- |
| **Buyer** | For external customers: merchant/exchange founder, owner, or business decision-maker. For the first onboarding/sales motion: ersan owns the agreement and commercial decision. |
| **Champion** | Technical founder, CTO, or integration lead who wants faster crypto payment/wallet infrastructure without building the full money core internally. In the current project phase, ersan acts as the internal champion. |
| **User** | Developer integrator, merchant/exchange operator, platform/admin operator, custody/security owner, and downstream checkout payer or wallet end user. |

### Decision Criteria

- Can the tenant integrate safely with API key, HMAC signing, idempotency, and stable response contracts?
- Can money lifecycle state be trusted across checkout, static wallet, deposit, ledger, webhook, refund/payout, sweep, and reconciliation?
- Are operational limits clear enough for controlled beta?
- Does the platform make launch readiness visible instead of implying production custody readiness too early?
- Are tenant isolation, auditability, redaction, and recovery paths credible?

### Current Onboarding Reality

The first customer/onboarding decision path is founder-led and manual: ersan currently owns business selection, technical evaluation, pilot approval, and commercial decision. External buyer roles should still be designed for, but the immediate workflow does not require a multi-person procurement flow.

---

## Additional Context

Existing source artifacts:

- `_bmad-output/planning-artifacts/prd.md`
- `_bmad-output/planning-artifacts/ux.md`
- `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md`
- `docs/payment-gateway-wallet-provider-audit.md`
- `docs/product-readiness-audit.md`
- `docs/integration-guide.md`

---

## Next Steps

- [x] Vision Capture
- [x] Positioning
- [x] Business Model
- [x] Business Customer Profile
- [ ] Target Users
- [ ] Product Concept
- [ ] Success Criteria
- [ ] Competitive Landscape
- [ ] Constraints
- [ ] Platform & Device Strategy
- [ ] Tone of Voice
