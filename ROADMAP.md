# Gateway — Implementation Roadmap

Audit date: 2026-06-05  
Auditor: Senior payment systems architect / Go backend engineer

---

## Tier 0 — Money Loss / Data Corruption
> Fix these before taking any real payments.

| # | Task | File(s) | Status |
|---|---|---|---|
| T0-1 | Fix `FindByAPISecret` — replace non-deterministic AES-GCM with `HMAC-SHA256(MASTER_KEY, secret)` for API key lookup | `repositories/domain_repo.go:65` | ✅ Done |
| T0-2 | Fix webhook secret decryption fallback — remove silent fallback to ciphertext bytes as HMAC key | `services/webhook/notifier.go:126` | ✅ Done |
| T0-3 | Fix payment amount matching — accept ±0.5% tolerance, reject extreme overpayment, emit `payment.underpaid` event instead of silently dropping | `repositories/payment_repo.go:225` | ✅ Done |
| T0-4 | Add N-confirmation gate — do not mark session `paid` until N block confirmations per chain (1 EVM, 3 BTC, 1 SOL) | `workers/listeners/`, `models/payment_session.go` | ❌ Open |
| T0-5 | Fix `UniqueHash` nil/empty logIndex normalization — prevent duplicate transaction processing on EVM logs without logIndex | `repositories/transaction_repo.go` | ❌ Open |

---

## Tier 1 — Security
> Fix before any production or public exposure.

| # | Task | File(s) | Status |
|---|---|---|---|
| T1-1 | Encrypt TOTP secret in DB — wrap `SaveTOTPSecret` with `helpers.EncryptSecret`, decrypt on verify | `repositories/admin_repo.go:75` | ✅ Done |
| T1-2 | Add CSRF tokens to all admin/dealer forms — use Fiber's built-in `csrf` middleware | `api/routes/routes.go`, all HTML templates | ❌ Open |
| T1-3 | Remove hardcoded default admin credentials (`admin123`) — generate random password on first run | `main.go` | ✅ Done |
| T1-4 | Document minimum mnemonic security — add `SECURITY.md` with env hardening guide; path toward KMS/Vault integration | `main.go`, `SECURITY.md` | ❌ Open |
| T1-5 | Fix bootstrap admin race condition — use transaction with count check instead of count-then-insert | `repositories/admin_repo.go` | ✅ Done |

---

## Tier 2 — Reliability
> Fix before scaling beyond a single instance or handling real merchant volume.

| # | Task | File(s) | Status |
|---|---|---|---|
| T2-1 | Add price oracle staleness guard — reject prices older than configurable TTL (5 min default); record quote at session creation | `services/pricing/coingecko.go`, `models/payment_session.go` | ❌ Open |
| T2-2 | Replace in-process rate limiting with Redis — current `sync.Map` limiter is per-pod, bypassed with multiple instances | `api/middleware/ratelimit.go` | ❌ Open |
| T2-3 | Add `idempotency_key` to payment create API — merchant-supplied key; `ON CONFLICT DO NOTHING` prevents double-billing | `models/payment_session.go`, `api/handlers/payment.go` | ❌ Open |
| T2-4 | Fix SSRF TOCTOU — re-validate webhook URL on every delivery attempt, not just at registration | `services/webhook/notifier.go`, `helpers/credentials.go:214` | ❌ Open |
| T2-5 | Add per-API-key rate limits — track request counts by domain/API key, not just by IP | `api/middleware/`, `repositories/domain_repo.go` | ❌ Open |

---

## Tier 3 — Stripe-Parity Completeness

| # | Task | File(s) | Status |
|---|---|---|---|
| T3-1 | Add double-entry ledger — `ledger_entries` table; every deposit/withdrawal creates matching debit/credit rows | `models/ledger_entry.go` (new), `repositories/` | ❌ Open |
| T3-2 | Merchant balance API endpoint — serve real-time balance from ledger, not full-table scan | `api/handlers/`, `repositories/ledger_repo.go` (new) | ❌ Open |
| T3-3 | Webhook delivery log — `webhook_deliveries` table with per-attempt request/response/latency + replay endpoint | `models/webhook_delivery.go` (new), `services/webhook/notifier.go` | ❌ Open |
| T3-4 | Refund workflow — reverse ledger entry + track on-chain return tx + `payment.refunded` webhook event | `models/`, `api/handlers/`, `services/` | ❌ Open |
| T3-5 | Block reorg handling — detect reorged txs, mark transactions `reorged`, reverse affected sessions | `workers/listeners/`, `repositories/transaction_repo.go` | ❌ Open |

---

## Tier 4 — Operational Maturity

| # | Task | File(s) | Status |
|---|---|---|---|
| T4-1 | Per-chain block finality configuration — configurable `ConfirmationsRequired` per chain | `application/configuration/chains.go`, `blockchain/` | ❌ Open |
| T4-2 | Settlement / payout scheduler — periodic sweep of settled balances to merchant hot wallets | `workers/`, `services/system/` | ❌ Open |
| T4-3 | Webhook event versioning + typed catalog — replace hardcoded strings with versioned event types | `services/webhook/`, constants | ❌ Open |
| T4-4 | Structured JSON logging + distributed tracing — replace `fmt.Println` with `slog` or `zap` | All packages | ❌ Open |
| T4-5 | HSM or cloud KMS for mnemonic — integrate AWS KMS / HashiCorp Vault for key derivation root | `blockchain/`, `main.go` | ❌ Open |

---

## Schema / Database Gaps

| Table | Gap | Priority |
|---|---|---|
| `merchants` | No DB-level `UNIQUE` on `email`; `is_active` not filtered in all queries | Tier 1 |
| `transactions` | `status` free-form string (no CHECK/enum); no `confirmed_at`; no `block_confirmations` counter | Tier 0 |
| `payment_sessions` | No `idempotency_key`, `price_quote_at`, `confirmations_required`; status has no CHECK constraint | Tier 0–2 |
| `withdrawal_requests` | No unique partial index on `(wallet_id, chain)` WHERE `status='pending'` | Tier 1 |
| `admins` | `totp_secret` plaintext | Tier 1 |
| `activity_logs` | No compound index on `(merchant_id, created_at)` — will be slow at scale | Tier 2 |
| Missing | `ledger_entries` — no authoritative balance store | Tier 3 |
| Missing | `webhook_deliveries` — no per-attempt delivery log | Tier 3 |
| Missing | `price_quotes` — no record of price shown at session creation | Tier 2 |

---

## First 5 Tasks (ordered by impact)

1. **T0-1** — Fix `FindByAPISecret` → payment intake may be completely broken for API callers
2. **T0-2** — Fix webhook fallback → all delivered webhooks are unverifiable
3. **T0-3** — Fix amount matching → underpayment causes customer fund loss
4. **T0-4** — Add confirmation gate → payments marked paid before on-chain finality
5. **T1-1** — Encrypt TOTP secret → 2FA bypass on DB read

**Start with T0-1.** If `FindByAPISecret` is broken, every merchant integration that creates payments via API is silently failing. It is also the simplest fix (4 lines changed, one migration) and it unblocks T0-2 and T0-3 from being reachable in production.
