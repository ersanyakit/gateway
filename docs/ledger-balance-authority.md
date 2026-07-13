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
- `pending` and `posted` entries participate in balance queries. Legacy `voided` entries are excluded.
- `reorg_reversal` entries compensate previously posted rows without deleting history.
- `withdrawal_release`, `refund_release`, and failed/cancelled `sweep_release` entries release active holds by referencing the original hold ledger entry id. The original hold rows stay unchanged.
- `adjustment` entries are manual or system correction rows and must use the same account, direction, status and idempotency rules as other money movement.

## Idempotency And Invariants

Every ledger movement uses a stable idempotency key. The GORM schema owns the `ux_ledger_idempotent_account` index so duplicate posting attempts either no-op before insert or fail safely at the database boundary. It also owns check constraints for ledger entry type, account, direction, and status values.

Balanced movements are checked by `LedgerRepo.FindInvariantIssues`, grouped by idempotency key, tenant scope, chain, token, and symbol. `LedgerRepo.OpenInvariantReconciliationJobs` opens a scoped reconciliation job for each non-zero group using redacted evidence. Logs include `correlation_id`, merchant id, domain id when present, chain id, token/symbol, and net amount; they must not include API secrets, webhook secrets, private keys, mnemonics, or raw signatures.

## Projection Rebuild

`ledger_balance_projections` is rebuildable from `ledger_entries` only. `LedgerRepo.RebuildBalanceProjections` reads active `pending` and `posted` numeric ledger rows, excludes legacy `voided` and non-numeric rows, and writes merchant, domain, wallet, and platform projection scopes. Projection rows are cacheable derived state, not a second authority.

## GORM Migration Plan

Ledger schema changes remain GORM-owned through model tags, `AutoMigrate`, and `services/database.VerifySchema`. Current GORM checks require valid entry types, accounts, directions, statuses, the idempotency/account uniqueness index, the projection indexes, and the `trg_ledger_entries_immutable` trigger.

Normal application code must not update or delete `ledger_entries`. `models.LedgerEntry` rejects GORM update/delete hooks unless `models.LedgerEntryMutationContextKey` is explicitly set, and the PostgreSQL trigger rejects direct `UPDATE`/`DELETE` unless migration tooling sets `app.allow_ledger_entry_mutation` for the session. Legacy `voided` rows remain readable for historical compatibility; new normal lifecycle corrections must append release/reversal rows instead.
