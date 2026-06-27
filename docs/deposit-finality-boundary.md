# Deposit Finality Boundary

Deposit processing consumes durable chain facts after the Chain Indexer has recorded them. The listener path remains fact-only; deposit matching runs from a separate retryable worker.

## Lifecycle

1. A chain fact is matched to an owned wallet address through the wallet lookup boundary.
2. A deposit lifecycle record is written with the chain fact event id as the stable idempotency key.
3. Unknown addresses are recorded as unmatched without merchant, domain, or wallet identifiers.
4. Matched deposits remain `pending` or `confirming` until chain-specific finality is met.
5. Finalized deposits emit `deposit.finalized.v1` through the Postgres money event outbox.

## Finality Rule

The deposit boundary uses the same chain-specific confirmation requirements as the rest of the money core:

- Bitcoin: 3 confirmations by default
- Solana: 1 confirmation by default
- TRON: 20 confirmations by default
- EVM/default chains: 12 confirmations by default

Environment-specific overrides can still use `CHAIN_<id>_CONFIRMATIONS`, `<CHAIN_NAME>_CONFIRMATIONS`, or `FINALITY_CONFIRMATIONS_DEFAULT`.

## Safety Rule

Pre-finality deposits must not mark payments paid, post available ledger entries, enqueue merchant webhooks, or schedule sweep jobs. Those state changes require finalized deposit input and are handled by later payment, ledger, webhook, and sweep boundaries.
