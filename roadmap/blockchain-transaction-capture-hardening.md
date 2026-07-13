# Blockchain Transaction Capture Hardening Roadmap

Date: 2026-06-30
Scope: EVM-family chains, Solana, Bitcoin, TRON and supported payment/deposit assets.

## Current Answer

The gateway does not currently guarantee "all transactions on every blockchain" capture in the block-explorer sense.

The current design is a payment/deposit observer:

- It scans supported chains and records durable `chain_facts`.
- It is intended to capture owned-address payment/deposit activity for registered assets.
- It now holds checkpoints on retryable RPC errors instead of skipping blocks.
- It now uses provider health ranking, endpoint timeout, endpoint circuit breaking and active failover.
- It now fails closed on TRON transaction-info gaps so TRC20 logs are not silently missed.
- It now detects Bitcoin endpoint type from chain RPC URLs and supports UniSat Open API, Bitcoin Core JSON-RPC, and Esplora-compatible APIs.
- It now records Bitcoin block hash/parent-hash continuity and rewinds checkpoint on mismatch.
- It now has explicit TRON per-call endpoint failover tests and Solana Token-2022 transfer coverage.

This is a safer operational baseline, but the remaining work below is required before claiming complete, exchange-grade capture for all relevant chain activity.

## Current Coverage

| Family | Current capture path | Strong points | Remaining limitation |
|---|---|---|---|
| EVM | `eth_getBlockByNumber`, receipts, ERC-20 `Transfer`, native tx value, internal transfers when trace RPC is available | Multi-provider retry/failover; parent continuity rollback; checkpoint held on retryable RPC failure | Internal native transfers depend on provider trace support; no quorum validation; no full canonical rollback window; not an archive/indexer-grade all-contract parser |
| Solana | finalized slots via `getBlocks`/`getBlock`, parsed system/SPL token transfers, memos, program calls | finalized slot scanning; endpoint failover; checkpoint held on retryable RPC failure | Token-2022/CPI coverage needs explicit contract tests; skipped/missing slots and versioned tx behavior need stronger recovery; no enhanced account-owner attribution layer |
| Bitcoin | Chain RPC URL adapter for UniSat Open API, Bitcoin Core JSON-RPC, and Esplora-compatible APIs | confirmed safe-height scanning; endpoint failover; checkpoint held on 429/timeout; parent-hash checkpoint rollback | no mempool/unconfirmed tracking; no persisted per-page REST resume cursor; no full block-record reorg correction workflow |
| TRON | gRPC blocks + transaction info for TRX/TRC20, HTTP transaction-info fallback | gRPC reconnect; per-call endpoint failover; fail-closed when transaction info is unavailable; HTTP fallback for retryable gRPC info failures | Provider 429 still holds progress when every configured gRPC and HTTP endpoint is throttled |

## Implemented On 2026-06-30

- Bitcoin listener now uses the existing chain RPC source (`BITCOIN_RPC_URLS`, `CHAIN_0_RPC_URLS`, and `BitcoinChain.RPCHttp`) instead of a separate Bitcoin Core env family.
- Bitcoin endpoint adapter supports:
  - UniSat Open API (`open-api.unisat.io`)
  - Bitcoin Core JSON-RPC (`getblockcount`, `getblockhash`, verbose `getblock`)
  - Esplora-compatible APIs such as Blockstream and mempool.space
- Credentials embedded in Core RPC URLs are stripped from the endpoint key and used as Basic Auth; UniSat URL user-info is used as a Bearer token.
- Bitcoin confirmed block processing now validates parent-hash continuity and persists hash evidence through `chain_states`.
- Bitcoin public REST mode now fetches block metadata before tx pages, so REST mode also validates parent hash.
- TRON gRPC calls now retry through configured ranked endpoints within the same operation before giving up.
- TRON transaction-info failure remains fail-closed, so TRC20 logs are not silently skipped.
- TRON transaction-info retryable gRPC failures now fall back to `/wallet/gettransactioninfobyid` over configured HTTP endpoints.
- Solana Token-2022 `transferChecked` parsing is covered by a fixture test.
- Solana null `getBlock` result is covered as a checkpoint-holding error.
- Regression coverage added for BTC Core conversion, BTC Core event dispatch, BTC parent mismatch rollback, TRON failover, Solana Token-2022, and Solana null block.

## Non-Negotiable Definition Of Done

The system can only claim complete capture for supported payment/deposit activity when all of these are true:

- Every scanned block/slot is either fully processed or checkpoint is not advanced.
- Every relevant native/token transfer has a deterministic event id and idempotent chain fact.
- Listener restart, RPC timeout, provider 429, process crash and DB retry cannot create a missing block interval.
- Reorgs are detected across a configurable rollback window and affected facts/deposits/ledger entries are corrected.
- Backfill can replay explicit historical ranges without duplicate money effects.
- Provider health is not just observed; it is enforced by tests and operator runbooks.
- Coverage tests exist per chain family using simulated blocks/slots with edge-case transactions.

## Phase 0 - Lock Current Safety Baseline

Goal: make the current no-skip behavior contractual.

Tasks:

- Add a regression test for every listener proving retryable RPC failure does not advance `chain_states.last_processed_block`.
- Add a DB-level invariant or test helper that detects checkpoint jumps over unprocessed ranges.
- Add a metric: `gateway_chain_checkpoint_held_total{chain,reason}`.
- Add a metric: `gateway_chain_rpc_endpoint_failover_total{chain,provider}`.
- Add an operator dashboard panel for selected provider, provider lag and checkpoint lag.

Acceptance:

- Forced RPC timeout/429 tests pass for EVM, Solana, Bitcoin and TRON.
- Checkpoint remains unchanged when block/slot processing fails.
- Dashboard shows held checkpoints within one provider-health interval.

## Phase 1 - Deterministic Historical Backfill

Goal: make old deposits recoverable without relying on live listener startup behavior.

Tasks:

- Build an explicit backfill command/service:
  - chain id
  - start block/slot
  - end block/slot
  - batch size
  - dry-run mode
  - resume cursor
- Persist backfill jobs with status, lease owner, attempts and last processed range.
- Reuse the same `chain_fact` idempotency path as live listeners.
- Add range replay tests proving duplicate backfill runs do not duplicate deposits, ledger entries or webhooks.

Acceptance:

- Operator can replay any supported chain range.
- Backfill can stop/restart safely.
- Backfill reports scanned, matched, unmatched, skipped-empty and failed counts.

## Phase 2 - EVM Completeness

Goal: close EVM payment/deposit gaps beyond simple block/receipt scanning.

Tasks:

- Store canonical block records with number, hash, parent hash, observed provider and scan status.
- Implement configurable reorg rollback window per EVM chain.
- Add fallback strategy for internal native transfers:
  - use `trace_block` when available
  - use provider-specific trace alternatives where available
  - mark internal-transfer coverage unavailable per provider when unsupported
- Add tests for:
  - native transfer
  - ERC-20 transfer
  - contract-created address receiving funds
  - internal transfer from contract execution
  - same-height reorg
  - provider trace unsupported
- Add per-chain finality config enforcement in listener facts, not just downstream deposit handling.

Acceptance:

- EVM listener cannot claim internal-transfer coverage unless trace or equivalent is actually working.
- Reorg simulation reverses/corrects affected chain facts and downstream money state.
- All EVM supported chains pass the same fixture suite.

## Phase 3 - Solana Completeness

Goal: make Solana slot and token parsing explicit and test-covered.

Tasks:

- Add fixture tests for:
  - SOL system transfer
  - SPL token transfer
  - inner instruction transfer
  - Token-2022 transfer
  - memo-only transaction
  - failed transaction with balance movement absent
  - versioned transaction with address lookup tables
- Track skipped slots separately from processed empty slots.
- If `getBlock` returns unavailable/null for a finalized slot, classify it as retryable until a policy threshold is reached.
- Add associated token account owner resolution for deposit attribution.
- Add slot backfill and gap repair job.

Acceptance:

- Solana cannot advance past a finalized slot whose block data is unavailable unless the slot is proven skipped.
- Token-2022 and inner-token transfers have explicit pass/fail fixture coverage.
- Deposit attribution works for direct wallet addresses and associated token accounts.

## Phase 4 - Bitcoin Completeness

Goal: remove public REST API rate-limit dependency from confirmed BTC capture.

Tasks:

- Add optional Bitcoin Core `getrawtransaction`/`txindex` enrichment when verbose block prevout data is unavailable.
- Keep REST APIs only as fallback.
- Add Bitcoin block page retry with resume offset when REST is used.
- Persist per-block scan status including tx page offset.
- Expand reorg handling from checkpoint rollback to full affected-fact correction workflow.
- Add tests for:
  - multi-page block transactions
  - address in `vin.prevout`
  - address in `vout`
  - coinbase ignored for deposits unless explicitly configured
  - reorg at safe depth boundary

Acceptance:

- BTC confirmed capture can run against a self-hosted Bitcoin Core node without public REST APIs.
- A REST 429 cannot lose a block page; it only holds checkpoint.
- Reorg correction is tested.

## Phase 5 - TRON Completeness

Goal: make TRX/TRC20 capture resilient to provider transaction-info throttling.

Tasks:

- Persist per-block scan status when transaction info is unavailable.
- Add tests for:
  - TRX transfer
  - TRC20 transfer logs
  - transaction info unavailable
  - provider 429
  - endpoint failover within same block

Acceptance:

- TRC20 logs are never silently skipped.
- Provider 429 holds checkpoint and rotates endpoint.
- Same block succeeds when any configured endpoint can return transaction info.

## Phase 6 - Production Claim Gate

Goal: prevent accidental marketing or operational claims beyond proven coverage.

Tasks:

- Add a `/health/chain-coverage` admin/API endpoint with per-chain coverage flags:
  - live scanning
  - backfill
  - trace/internal transfers
  - reorg rollback
  - provider quorum
  - last successful fixture suite
- Add readiness check that marks chains degraded when coverage prerequisites are missing.
- Document supported asset and transaction classes per chain in public integration docs.

Acceptance:

- The system reports exactly what it can capture per chain.
- Unsupported classes are visible to operators and merchants.
- "Complete capture" can only be claimed for chain/asset classes with passing coverage gates.

## Immediate Recommendation

Do not describe the current system as "captures all blockchain transactions".

Use this wording until the roadmap is complete:

"The gateway captures supported payment/deposit activity for configured chains and assets. On retryable RPC/provider failures it holds the checkpoint and retries instead of skipping blocks. Full exchange-grade capture requires the remaining backfill, reorg, provider quorum and chain-specific fixture work in this roadmap."
