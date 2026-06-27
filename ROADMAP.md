# Gateway — Implementation Roadmap

Latest audit date: 2026-06-26
Latest detailed report: [`docs/payment-gateway-wallet-provider-audit.md`](docs/payment-gateway-wallet-provider-audit.md)

---

## 2026-06-26 — Platform Readiness Delta

Verdict: the project is a credible merchant/dealer payment-gateway MVP, but it is not yet a production-grade wallet provider and it is not ready for Binance-level exchange wallet tracking.

Trust Wallet Core truth:

- Wallet/address generation uses Trust Wallet Core for all supported chains through `BaseChain.GetDerivedWallet`.
- EVM-family native/ERC-20 transfers use Trust Wallet Core signing.
- Bitcoin P2WPKH transfers use Trust Wallet Core signing; current generated Taproot Bitcoin wallets use a manual `btcd/txscript` signing fallback.
- Solana and TRON wallets are generated through Trust Wallet Core, but transfers are signed with Solana/TRON SDKs, not Trust Wallet Core.

### New P0 Blockers

| ID | Gap | Evidence | Acceptance criteria |
|---|---|---|---|
| P0-20260626-1 | Real production signer is missing | `blockchain/basechain.go`, `blockchain/walletcore/provider_trustwalletcore.go` | Implement KMS/HSM/MPC signer interface; production no longer depends on env mnemonic or in-process private-key signing. |
| P0-20260626-2 | Listener first start can miss history | `workers/listeners/evm/listener.go`, `workers/listeners/bitcoin/bitcoin.go`, `workers/listeners/solana/listener.go`, `workers/listeners/tron/tron.go` | Add explicit start block/slot, historical backfill mode, and safe resume semantics per chain. |
| P0-20260626-3 | Catch-up throughput is too low for exchange workload | same listener files | Make listeners horizontally scalable and configurable; expose per-chain lag metrics; process large downtime windows safely. |
| P0-20260626-4 | Reorg accounting is incomplete | listener/repository/finality flow | Store canonical block hashes, detect reorgs, reverse ledger entries, and emit correction webhooks. |
| P0-20260626-5 | Sweeps are not durable jobs | `main.go:autoSweepDeposit` | Persist sweep jobs with status, retries, locks, idempotency, dead-letter state, and reconciliation. |
| P0-20260626-6 | Fee/gas policy is too simple | `blockchain/chains/evm_transfer.go`, `blockchain/chains/bitcoin_transfer.go`, `blockchain/chains/solana_transfer.go`, `blockchain/chains/tron_transfer.go` | Add EIP-1559, BTC fee estimator/RBF/CPFP, Solana priority fee/rent handling, and TRON resource/energy policy. |
| P0-20260626-7 | Single-process architecture is a scaling ceiling | `main.go`, `workers/dispatcher`, `workers/indexer/address_index.go`, `api/middleware/ratelimit.go` | Move chain events, webhook delivery, and sweep/finality processing to durable queues; replace per-process rate limiting and in-memory-only indexing. |
| P0-20260626-8 | Observability is insufficient | broad use of `log.Printf` / `fmt.Println` | Add structured logs, Prometheus metrics, traces, chain lag alerts, webhook SLOs, signer alerts, and reconciliation dashboards. |

### New P1/P2 Work

| ID | Gap | Acceptance criteria |
|---|---|---|
| P1-20260626-1 | Production migrations | Replace production startup `AutoMigrate` with versioned migrations, migration locks, rollback docs, and schema drift checks. |
| P1-20260626-2 | DB-level invariants | Add status checks/enums, unique idempotency constraints, partial unique indexes for pending jobs/withdrawals, and ledger balance invariants. |
| P1-20260626-3 | RPC provider strategy | Add provider health scoring, failover, archive-node requirements, and quorum/canonical-head checks. |
| P1-20260626-4 | Nonce/UTXO concurrency | Add per-wallet nonce manager, UTXO reservation, stuck transaction replacement, and concurrent withdrawal tests. |
| P1-20260626-5 | Webhook hardening | Add event version catalog, exponential backoff, dead-letter state, merchant delivery diagnostics, and replay idempotency. |
| P2-20260626-1 | Exchange-grade sharding | Partition listeners by chain/block/address range; support multi-worker deployment with leader/lease ownership. |
| P2-20260626-2 | Custody policy platform | Add hot/warm/cold wallet tiers, approval policy engine, velocity limits, emergency freeze, and signer audit logs. |
| P2-20260626-3 | Continuous reconciliation | Compare chain balances, ledger balances, sweep jobs, webhook state, and withdrawals continuously. |

### 2026-06-26 Implementation Update

Completed in this pass:

- P0-20260626-5: auto-sweep is now persisted through `sweep_jobs` with idempotent enqueue, claim locks, retry attempts, exponential backoff, dead-letter state, and sweep tx hash recording.
- P1-20260626-5: webhook deliveries now include event versioning (`event_version`, `X-Gateway-Event-Version`) and failed transaction/payment webhook retries use exponential backoff with max-attempt gating.
- P0-20260626-2 partial: listeners now support explicit chain start block env vars (`CHAIN_<id>_START_BLOCK`, `<CHAIN_NAME>_START_BLOCK`, `START_BLOCK_<CHAIN_NAME>`, `CHAIN_START_BLOCK_DEFAULT`) instead of always jumping to safe/latest on first start.
- P0-20260626-4 partial: same-chain/same-height block hash conflicts now mark old transactions as `reorged`, enqueue `transaction_reorged` webhooks, reverse linked ledger entries with idempotent `reorg_reversal` entries, fail linked paid sessions with correction webhooks, dead-letter pending sweep jobs, and open reconciliation jobs.
- P2-20260626-3 partial: ledger invariant scanning now opens reconciliation jobs when a non-zero debit/credit imbalance is detected by idempotency key.
- P1-20260626-1 partial: `APP_ENV=production` now disables startup `AutoMigrate` by default, runs schema verification instead, reports migration policy in readiness, and documents the versioned-migration launch requirement.
- P0-20260626-8 partial: `/metrics` now exposes Prometheus-compatible gauges for webhook backlog, sweep backlog, reconciliation drift, chain worker/state lag, migration policy, and signer policy; production access requires `METRICS_BEARER_TOKEN`. HTTP request correlation, panic recovery, bounded server timeouts, and structured request logs are now installed at the Fiber boundary without logging bodies, query strings, or secret headers.
- T0-5: transaction `unique_hash` generation now trims hash/logIndex input, canonicalizes `0x` hashes and hex log indexes, rejects blank hashes, and keeps nil/empty logIndex values on the same backward-compatible unique-key suffix.
- T1-4: `SECURITY.md` now documents secret handling, mnemonic/software-signer limits, production migration discipline, webhook egress, and launch gates.

Still open:

- P0-20260626-1: real KMS/HSM/MPC signer integration requires a selected signer provider and credentials.
- P0-20260626-3/P0-20260626-7: horizontal chain indexing and durable event bus are architectural work beyond the monolith worker patch.
- P0-20260626-4: full reorg accounting still needs canonical parent/child block hash storage, proactive rollback-window scanning, and fork simulation tests.
- P0-20260626-6/P1-20260626-4: advanced fee, nonce, UTXO, stuck transaction, RBF/CPFP, priority fee, and TRON resource policies are still open.
- P1-20260626-1: production still needs actual versioned migration files/runner and rollback workflow; startup `AutoMigrate` is now guarded but not replaced by a full migration system.
- P0-20260626-8: distributed traces, dashboards, alert rules, and SLO thresholds are still open beyond the `/metrics` and HTTP request-log baseline.
- P2-20260626-2: custody policy platform remains open.

Older audit sections below remain for historical continuity; some statuses may have changed after the latest implementation work.

---

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
| T0-5 | Fix `UniqueHash` nil/empty logIndex normalization — prevent duplicate transaction processing on EVM logs without logIndex | `repositories/transaction_repo.go` | ✅ Done |

---

## Tier 1 — Security
> Fix before any production or public exposure.

| # | Task | File(s) | Status |
|---|---|---|---|
| T1-1 | Encrypt TOTP secret in DB — wrap `SaveTOTPSecret` with `helpers.EncryptSecret`, decrypt on verify | `repositories/admin_repo.go:75` | ✅ Done |
| T1-2 | Add CSRF tokens to all admin/merchant portal forms — use Fiber's built-in `csrf` middleware | `api/routes/routes.go`, all HTML templates | ❌ Open |
| T1-3 | Remove hardcoded default admin credentials (`admin123`) — generate random password on first run | `main.go` | ✅ Done |
| T1-4 | Document minimum mnemonic security — add `SECURITY.md` with env hardening guide; path toward KMS/Vault integration | `main.go`, `SECURITY.md` | ✅ Done |
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
| T3-5 | Block reorg handling — detect reorged txs, mark transactions `reorged`, reverse affected sessions | `workers/listeners/`, `repositories/transaction_repo.go` | 🟡 Partial |

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
