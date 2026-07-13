# Product Readiness Audit

Audit date: 2026-06-26
Scope: local code review, transfer-signing tests, `go test ./...`, `go vet ./...`
Repo state reviewed: `main` branch working tree after Trust Wallet Core transfer fixes

## Executive Verdict

This codebase can become a merchant/dealer payment gateway and static-wallet payment provider, but it is not yet a production-grade wallet provider and it is not ready for Binance-level exchange wallet tracking.

Current fit:

- Controlled beta / internal pilot: yes, if operated with small volume, monitored manually, and with production signer risks accepted.
- Merchant payment gateway: partially yes. Payment sessions, static deposit addresses, webhooks, idempotency, ledger rows, finality fields, and admin/dealer screens exist.
- Wallet-provider-as-a-service: partial. HD address generation exists across supported chains, but key custody, operational controls, reconciliation, queueing, and signer isolation are not production-grade.
- Binance-scale exchange wallet tracking: no. The listener architecture is single-process, low-throughput, not backfill-safe on first start, and lacks durable streaming, node quorum, reorg accounting, and operational telemetry.

## Trust Wallet Core Coverage

Short answer: wallets are generated through Trust Wallet Core for all supported chains, but transfers are not all signed through Trust Wallet Core.

| Chain | Wallet/address generation | Native transfer signing | Token transfer signing | Current truth |
|---|---|---|---|---|
| Bitcoin | Trust Wallet Core, Taproot address derivation | Mixed | Not applicable | P2WPKH signing uses Trust Wallet Core. Current generated Taproot wallets use manual `btcd/txscript` signing fallback because the vendored Trust Wallet Core Bitcoin signer path is not reliable for this Taproot flow. |
| Ethereum | Trust Wallet Core | Trust Wallet Core | Trust Wallet Core ERC-20 | Uses Trust Wallet Core `TWAnySignerSign`, but currently legacy gas mode, not EIP-1559. |
| Avalanche | Trust Wallet Core | Trust Wallet Core | Trust Wallet Core ERC-20 | Same EVM signing path. |
| BNB Chain | Trust Wallet Core | Trust Wallet Core | Trust Wallet Core ERC-20 | Same EVM signing path. |
| Base | Trust Wallet Core | Trust Wallet Core | Trust Wallet Core ERC-20 | Same EVM signing path. |
| Arbitrum | Trust Wallet Core | Trust Wallet Core | Trust Wallet Core ERC-20 | Same EVM signing path. |
| Unichain | Trust Wallet Core | Trust Wallet Core | Trust Wallet Core ERC-20 | Same EVM signing path. |
| Chiliz | Trust Wallet Core | Trust Wallet Core | Trust Wallet Core ERC-20 | Same EVM signing path. |
| Chiliz Spicy | Trust Wallet Core | Trust Wallet Core | Trust Wallet Core ERC-20 | Same EVM signing path on testnet. |
| Solana | Trust Wallet Core | `gagliardetto/solana-go` | `gagliardetto/solana-go` SPL | Trust Wallet Core is used for address/private-key derivation, not transfer signing. |
| TRON | Trust Wallet Core | `okx/go-wallet-sdk/coins/tron` | `okx/go-wallet-sdk/coins/tron` TRC-20 | Trust Wallet Core is used for address/private-key derivation, not transfer signing. |

Relevant code:

- `blockchain/basechain.go`: all `Create` / `CreateHDWallet` paths call `BaseChain.GetDerivedWallet`.
- `blockchain/walletcore/provider_trustwalletcore.go`: Trust Wallet Core provider uses `TWHDWallet`, `TWAnyAddress`, and `TWAnySignerSign`.
- `blockchain/walletcore/provider_fallback.go`: fallback build cannot derive wallet addresses or sign transactions; it is not production-capable.
- `blockchain/chains/evm_transfer.go`: EVM native and ERC-20 signing goes through Trust Wallet Core.
- `blockchain/chains/bitcoin_transfer.go`: Bitcoin P2WPKH goes through Trust Wallet Core; Taproot uses manual fallback.
- `blockchain/chains/solana_transfer.go`: Solana/SPL transactions are built and signed with `gagliardetto/solana-go`.
- `blockchain/chains/tron_transfer.go`: TRON/TRC-20 transactions are built and signed with OKX TRON SDK helpers.

## What Already Works

The project is not empty or fake. It has a real payment-gateway skeleton:

- Multi-chain registry for Bitcoin, Ethereum, Avalanche, BNB Chain, Base, Arbitrum, Unichain, Solana, TRON, Chiliz, and Chiliz Spicy.
- HD wallet/address generation per merchant/domain/user.
- Payment sessions, checkout flow, static wallet responses, and API v1 endpoints.
- API key / bearer authentication plus request HMAC checks for sensitive v1 API calls.
- Idempotency handling for payment creation.
- Webhook delivery records, retry worker, replay surfaces, and HMAC-signed webhook payloads.
- Ledger models and repository methods for deposit, withdrawal, refund, and manual deposit flows.
- Listener workers for EVM, Bitcoin, Solana, and TRON.
- Finality fields and pending-finality worker logic.
- Transfer paths for EVM native/ERC-20, Bitcoin native, Solana native/SPL, and TRON TRX/TRC-20.
- Readiness-style runtime checks and tests are present.

## Critical Gaps

### P0 - Do Not Run Large Real Money Volume Before These

1. Key custody is still not backed by a real production provider.
   The code now has an external custody adapter contract and production watch-only derivation boundary. `SIGNER_MODE=kms/hsm/mpc/vault` still needs a real active adapter for production readiness, and current chain transaction builders fail closed before local private-key signing in production. A wallet provider cannot safely custody exchange or merchant funds until chain-specific external signing is implemented.

2. Listener first start skips history.
   EVM, Bitcoin, and TRON listeners start from `safeLatest` when `LastProcessedBlock <= 1`; Solana starts at latest. This prevents old backlog scanning on first boot and can miss deposits that happened before the service came online.

3. Catch-up throughput is far below exchange grade.
   Current limits are EVM 5 blocks per 6s, Bitcoin 2 blocks per 30s, Solana 8 slots per 10s, TRON 10 blocks per 3s. This is not enough for long downtime, high-volume block ranges, or large wallet sets.

4. Reorg accounting is incomplete.
   The code has finality fields and transaction statuses, but there is no robust reorg detector that stores canonical block hashes, compares parent/child history, reverses affected ledger entries, and emits corrected webhooks.

5. Single-process architecture is a scaling ceiling.
   Dispatcher, address index, rate limiter, retry workers, finality worker, and listener workers all run in one app process. There is no durable event queue, partitioning, leader election, or multi-consumer model.

6. Sweep execution is durable but not yet exchange-grade.
   Deposits enqueue persistent sweep jobs with retry count, bounded failure category, dead-letter state, ledger holds, parent-job prefund attempt state, tx-hash-required success handling, and reconciliation on uncertain broadcast outcomes. Chain resource reservation is still process-local, so multi-replica nonce/UTXO/resource ownership needs durable storage before high-volume production.

7. Fee and gas strategy is too simple.
   EVM signing still uses legacy gas mode and named caps, ERC-20 gas limit remains a bounded fallback, Bitcoin fee policy is env-bounded rather than estimator-driven, Solana compute/rent handling is minimal, and TRON energy/bandwidth handling is bounded but still approximate.

8. RPC strategy is not exchange-grade.
   The code records per-provider health snapshots, lag, latency, stale/inconsistent heads, and failover decisions. This improves controlled-beta visibility, but it is still not archive/quorum exchange infrastructure.

9. Database migration strategy is not production-controlled.
   Startup `AutoMigrate` is disabled by default in production and readiness now requires versioned GORM migration evidence. The process still needs operator drills and production run history before real-funds launch.

10. Observability is not sufficient.
    The code now exposes baseline Prometheus gauges for provider health, chain lag, outbox/webhook/sweep/withdrawal/refund/reconciliation backlog, migration readiness, signer readiness, and wallet address lookup rows. Structured traces, dashboards, alert manager wiring, and on-call drills remain required.

### P1 - Needed Before Public Production

1. Durable ingestion pipeline.
   Put chain events into Kafka/NATS/SQS/Postgres outbox, process idempotently, and persist offset progress only after downstream commits.

2. Partitioned address lookup.
   Normalized DB-backed wallet address lookup now exists and the in-memory index is bounded as a cache. Large wallet-set benchmark evidence and sharded indexer planning are still required for exchange-grade scale.

3. Durable nonce and UTXO concurrency controls.
   Current in-process nonce, UTXO, and sequence/resource reservation blocks same-process duplicate signing and routes uncertain sweep broadcasts to reconciliation. Add durable reservation rows and replacement evidence before running multiple gateway replicas or high-volume outbound flows.

4. Full reconciliation jobs.
   Scheduled chain-vs-ledger reconciliation must compare address balances, tx history, webhook state, sweep state, and ledger balances.

5. Webhook delivery hardening.
   The notifier validates webhook URLs during delivery, which is good. Still needed: event version catalog, exponential backoff, dead-letter queues, replay idempotency, and merchant-visible delivery diagnostics.

6. Stronger schema constraints.
   Enforce status enums/check constraints, partial unique indexes for pending withdrawals/jobs, unique merchant email, unique idempotency keys, and ledger invariants at the DB level.

7. Admin/security hardening.
   Complete portal JWT audit on all portal actions, add audit trails for signer/withdrawal decisions, separate duties for approval and execution, and add IP/device/session controls.

### P2 - Needed For Binance-Level Tracking

1. Dedicated node infrastructure.
   Run full/archive nodes or paid providers with archive guarantees per chain. Use independent providers and compare canonical heads.

2. Sharded listeners.
   Split listeners by chain, block range, and wallet/address partitions. Track lag and process blocks in parallel while preserving ordering where needed.

3. Reorg-safe ledger.
   Ledger postings must be reversible with explicit compensating entries. Webhooks must support correction events.

4. High-volume token indexing.
   EVM token logs need scalable filtering by contract and address, not only per-block processing through one process. Solana/TRON token transfer extraction needs equivalent scale testing.

5. Custody platform.
   Implement hot/warm/cold wallet tiers, policy engine, HSM/MPC signing, withdrawal approvals, travel-rule/compliance hooks if required, and emergency freeze/runbook tooling.

## Capability Matrix

| Capability | Current status | Verdict |
|---|---|---|
| Merchant/dealer onboarding | Exists via dealer/admin portal and APIs | Usable after security audit |
| Payment session creation | Exists with idempotency support | Good MVP base |
| Static deposit wallets | Exists across supported chains | Good MVP base |
| Deposit detection | Exists, but first-start backfill and reorg safety are weak | Needs P0 work |
| Payment finality | Fields and worker exist | Needs chain-specific verification and reorg handling |
| Webhooks | Delivery, retry, replay, HMAC signing exist | Needs SLO/dead-letter/versioning |
| Ledger | Models and flows exist | Needs invariants, reconciliation, and reorg reversals |
| Withdrawals/transfers | Implemented for supported families with signer guard and process-local resource reservation | Needs external signer and durable nonce/UTXO locking |
| Wallet provider custody | Partial software signer | Not production-ready |
| Binance-level tracking | Single-process listeners | Not suitable |

## Chain-Specific Risks

### EVM Family

Includes Ethereum, Avalanche, BNB Chain, Base, Arbitrum, Unichain, Chiliz, and Chiliz Spicy.

- Uses Trust Wallet Core signing for native and ERC-20 transfers.
- Uses legacy gas mode only; EIP-1559 fields are not used.
- ERC-20 gas limit is fixed at 65,000.
- Process-local nonce reservation prevents duplicate same-process signing, but there is no durable multi-replica nonce table.
- Listener catch-up is slow and starts from safe latest on first boot.
- Internal transfer tracing is best-effort and can be unavailable depending on RPC provider.

### Bitcoin

- Address generation is Taproot through Trust Wallet Core.
- P2WPKH signing uses Trust Wallet Core.
- Taproot signing uses manual `txscript` fallback.
- Fee rate is env-bounded but not estimator-driven; no mempool fee estimator, RBF, CPFP, batching policy, or UTXO consolidation.
- Process-local UTXO reservation blocks same-process duplicate spends; no durable UTXO reservation table exists for multi-replica operation.
- Listener starts from safe latest on first boot and only processes two blocks per poll.

### Solana

- Address generation is Trust Wallet Core.
- Transfers are signed with `gagliardetto/solana-go`, not Trust Wallet Core.
- SPL handling creates ATA when missing, but production needs stronger rent, compute budget, priority fee, blockhash retry, and confirmation handling.
- Listener starts from latest on first boot and processes limited slots per poll.

### TRON

- Address generation is Trust Wallet Core.
- Transfers are signed with OKX TRON SDK helpers, not Trust Wallet Core.
- TRC-20 fee limit and gas prefund assumptions are fixed.
- Listener relies on gRPC fullnode connection and limited catch-up.
- Production needs resource/energy accounting, provider redundancy, and stronger confirmation/reorg handling.

## Recommended Roadmap

### Phase 0 - Stabilize Money Safety

- Implement actual KMS/HSM/MPC signer adapter implementations for every supported signing path and keep production env mnemonic/private-key access disabled.
- Harden sweep/withdrawal durable jobs with multi-replica resource ownership, replacement evidence, and operator recovery workflows.
- Add listener backfill configuration: explicit start block/slot, full historical backfill mode, and safe manual rescan per chain.
- Add reorg detector and reversible ledger postings.
- Add durable per-wallet nonce/UTXO/resource locking.
- Replace fixed fee/gas logic with policy-driven estimators.
- Add chain/provider health checks with failover and metrics.

### Phase 1 - Production Gateway

- Move event ingestion and webhook delivery to durable queues.
- Replace in-process rate limits with Redis or another shared limiter.
- Add structured JSON logs, Prometheus metrics, traces, and alerting.
- Replace `AutoMigrate` in production with versioned migrations.
- Add DB constraints for statuses, uniqueness, pending jobs, idempotency, and ledger invariants.
- Expand integration tests with local chain simulators and mocked RPC failure modes.

### Phase 2 - Exchange-Grade Wallet Tracking

- Shard listeners by chain/block/address range.
- Add archive-node and quorum-provider support.
- Build reconciliation dashboards for chain balance, ledger balance, pending sweeps, stuck withdrawals, and webhook lag.
- Add hot/warm/cold wallet policy engine.
- Add withdrawal risk rules, multi-approval, velocity limits, and emergency freeze.

## Verification Run

The following checks were run after the Story 4.4 durable sweep recovery changes:

- `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./repositories ./services/database`
- `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 .`
- `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./...`
- `GOCACHE=/tmp/gateway-gocache-bmad go vet -p=1 ./...`
- `git diff --check && git diff --cached --check`

All passed in this local environment.
