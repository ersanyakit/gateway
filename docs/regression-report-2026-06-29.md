# Regression Report - 2026-06-29

Story: 8.2 Chain Simulation, Fuzz, Load and Regression Test Harness

## Commands

- Native CI mode: `GATEWAY_REGRESSION_MODE=native scripts/regression.sh`
- Fallback isolation mode: `GATEWAY_REGRESSION_MODE=fallback scripts/regression.sh`
- Targeted local checks: `go test -p=1 -count=1 ./test/chainsim ./test/load ./repositories ./helpers .`
- Full local checks: `go test -p=1 -count=1 ./... && go vet ./...`

## Coverage Added

- Trust Wallet Core native regression path builds `third_party/trustwallet/wallet-core` through `scripts/build_wallet_core.sh`.
- `walletcorefallback` remains an explicit test isolation mode for environments without native C/C++ libraries.
- Deterministic chain simulator emits EVM, Bitcoin, Solana and TRON representative transfer events and reorg replacement blocks without live RPC.
- Fuzz corpora cover chain fact amount/address/asset handling, webhook request signatures, idempotency request hashes and money event payload canonicalization.
- Queue load harness records throughput, p50/p95 latency, backlog and error counts for webhook delivery, deposit facts and sweep/outbound scenarios.

## Deterministic Load Harness Baseline

| Scenario | Items | Workers | Required metrics |
| --- | ---: | ---: | --- |
| webhook-delivery | 256 | 8 | throughput, p50 latency, p95 latency, backlog, errors |
| deposit-facts | 256 | 4 | throughput, p50 latency, p95 latency, backlog, errors |
| sweep-outbound | 128 | 4 | throughput, p50 latency, p95 latency, backlog, errors |

All scenarios are intentionally in-process and deterministic so failures indicate code or harness regressions, not third-party network instability.

## Dependency Failure Semantics

`scripts/regression.sh` exits with code `70` and a `regression dependency failure` message when Trust Wallet Core source or native dependencies are missing in native mode. This separates environment setup failures from Go test failures.
