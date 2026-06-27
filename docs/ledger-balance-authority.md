# Ledger Balance Authority

Ledger entries are the only authoritative source for merchant and wallet balances. Balance APIs and dealer/admin balance views must read `ledger_entries` through `LedgerRepo` queries or a ledger-owned projection.

## Balance Read Rule

- Public common balance reads `LedgerRepo.DomainBalances`.
- Public wallet balance reads `LedgerRepo.WalletBalances`.
- Dealer merchant balances read `LedgerRepo.MerchantBalances`.
- Admin wallet balances read `LedgerRepo.WalletBalancesByWalletIDs`.
- Transaction rows, payment sessions, withdrawal requests, refund rows, sweep jobs, wallet rows, and live chain RPC balance calls are lifecycle or observation data only; they must not be summed as authoritative balance.

## Accounts And Status

- `merchant_pending` tracks pending deposit credit before it becomes available.
- `merchant_available` is spendable balance after finality and settlement.
- `withdrawal_transit` tracks reserved outbound funds while withdrawal lifecycle completes.
- `refund_transit` tracks reserved refund funds while refund lifecycle completes.
- `platform_clearing` is the balancing account for money movement.
- `pending` and `posted` entries participate in balance queries. `voided` entries are excluded.
- `reorg_reversal` entries compensate previously posted rows without deleting history.
- `adjustment` entries are manual or system correction rows and must use the same account, direction, status and idempotency rules as other money movement.

## Idempotency And Invariants

Every ledger movement uses a stable idempotency key. The GORM schema owns the `ux_ledger_idempotent_account` index so duplicate posting attempts either no-op before insert or fail safely at the database boundary.

Balanced movements are checked by `LedgerRepo.FindInvariantIssues`, grouped by idempotency key, tenant scope, chain, token, and symbol. Non-zero net movement opens a scoped reconciliation job with a ledger invariant correlation id. Logs include `correlation_id`, merchant id, domain id when present, chain id, token/symbol, and net amount; they must not include API secrets, webhook secrets, private keys, mnemonics, or raw signatures.

## GORM Migration Plan

Ledger schema changes remain GORM-owned through model tags, `AutoMigrate`, and `services/database.VerifySchema`. If a future balanced-movement constraint cannot be represented safely as a GORM model/index/check tag, it must be documented here before implementation and covered by repository invariant tests.
