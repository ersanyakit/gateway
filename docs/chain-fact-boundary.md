# Chain Fact Boundary

Chain indexers produce durable chain facts only for confirmed, positive inbound transfers whose recipient is a registered platform wallet address and whose asset is registered. Unowned addresses, outbound transfers, pending/failed/unknown-status observations, zero-value events, unknown tokens, same-merchant custody movements, and unrelated chain activity are discarded before database persistence. A complete in-memory wallet index is required; listeners fail closed when it is unavailable. Indexers do not mark payments paid, post ledger entries, enqueue webhooks, or create sweep jobs directly from listener observation.

Bitcoin candidates retain every input address so a platform input cannot hide behind another first input. Solana SPL candidates resolve token-account owner, mint, program, and decimals from strict pre/post token-balance metadata; incomplete or conflicting metadata is skipped instead of being treated as native SOL.

## Fact Identity

Each eligible owned inbound transfer/log is recorded with a stable event id:

- `chain_id`
- normalized transaction hash
- log index or chain-equivalent identifier

The event id deduplicates repeated listener observations and gives downstream deposit, payment, ledger, webhook, and reconciliation consumers idempotent input.

## Required Metadata

Chain facts include chain id, block or slot height, block hash where available, transaction hash, log index or equivalent, observed address, direction, asset token/symbol/decimals, raw amount, event type, and finality metadata.

## Progress And Start Behavior

Listeners use explicit start block or slot configuration when provided. Current configuration keys are:

- `CHAIN_<id>_START_BLOCK`
- `<CHAIN_NAME>_START_BLOCK`
- `START_BLOCK_<CHAIN_NAME>`
- `CHAIN_START_BLOCK_DEFAULT`

Default safe/latest startup is not historical backfill. It is a pilot guard against unsafe broad scanning, not evidence that old deposits were scanned. Range replay/backfill remains an operator recovery capability to implement and test separately.

## Recovery Rule

If processing fails after a fact is observed, retrying the same observation must no-op by event id or report a conflict without causing duplicate payment, ledger, webhook, or sweep mutation.
