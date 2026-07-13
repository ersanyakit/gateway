# Controlled Launch Readiness Evidence

Gateway readiness levels are intentionally separated. Passing one level does not imply the next.

## Level 1: Controlled Merchant/Dealer Beta

Required evidence:

- API auth, HMAC signing, idempotent payment creation, hosted checkout, and static address contracts are tested.
- Durable money event outbox and webhook delivery retry/replay are enabled.
- Ledger-derived balances are the only authoritative balance source.
- Deposit finality gates settlement.
- Admin recover/sweep requires security-level authorization.
- `/metrics` is protected in production.
- `/api/v1/common/readiness` is healthy for database, migration, signer policy, operational backlogs, chain registry, worker registry, wallet derivation, and provider health.

Pilot limits:

- Small merchant count.
- Small balance and withdrawal limits.
- Explicit chain/token allowlist.
- Daily manual reconciliation.
- Named monitoring owner and rollback owner.

## Level 2: Production Payment Gateway

Additional required evidence:

- External signer mode is implemented and tested; process-memory software signing is not used.
- Versioned GORM migration evidence is current through `GATEWAY_DB_MIGRATION_VERSION`.
- Money-path SLOs, alerts, and runbooks are reviewed.
- Backup/restore drill is completed.
- Webhook dead-letter/replay process is operator-tested.
- Withdrawal/refund/sweep policy, hold, broadcast, finality, and reconciliation evidence is reviewed.

## Level 3: Wallet-Provider Custody

Additional required evidence:

- Hot/warm/cold custody model is defined.
- Signer quorum, key recovery, seed/key backup, and incident access process are documented.
- AML/KYT, sanctions screening, travel-rule obligations, and case management are implemented or formally out of scope with risk owner.
- Customer balance, chain balance, and ledger reconciliation can be run on schedule and after incidents.

## Level 4: Exchange-Grade Tracking

Additional required evidence:

- Sharded or partitioned chain indexer design exists.
- Normalized wallet address lookup is backfilled and benchmarked for target address count.
- Durable event bus path is defined for high-volume ingestion.
- Archive/quorum provider strategy is defined.
- Reorg simulation and large wallet-set benchmark target are documented.
- Unsupported scale claims are explicitly rejected until the above evidence passes.

## Current Status

As of this artifact, the platform can be evaluated for controlled beta evidence only. Production payment gateway, wallet-provider custody, and exchange-grade tracking remain blocked by external signer implementation, operational drills, compliance scope, archive/quorum provider strategy, and scale benchmarks.
