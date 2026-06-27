# Story 4.1 Migration Plan: Outbound Ledger Holds

Date: 2026-06-27

## Scope

Story 4.1 changes the ledger schema contract:

- Add nullable `ledger_entries.sweep_job_id` UUID column with index.
- Extend `ledger_entries.entry_type` allowed values with `sweep_hold`, `sweep_release`, and `sweep_debit`.
- Extend `ledger_entries.account` allowed values with `sweep_transit`.

Development and test environments use `AutoMigrate` plus `VerifySchema` coverage in `services/database/database.go`. Production must not rely on implicit AutoMigrate.

## Production Steps

1. Add `ledger_entries.sweep_job_id uuid NULL`.
2. Backfill is not required because existing rows have no sweep hold linkage.
3. Create an index on `ledger_entries.sweep_job_id`.
4. Replace the `ledger_entries_entry_type_check` constraint so it allows:
   `deposit_pending`, `deposit_available`, `withdrawal_hold`, `withdrawal_release`, `withdrawal_debit`, `refund_hold`, `refund_debit`, `sweep_hold`, `sweep_release`, `sweep_debit`, `reorg_reversal`, `adjustment`.
5. Replace the `ledger_entries_account_check` constraint so it allows:
   `merchant_pending`, `merchant_available`, `platform_clearing`, `withdrawal_transit`, `refund_transit`, `sweep_transit`.
6. Deploy application code after the schema change is present on every writer database.
7. Verify `GET /api/v1/common/readiness` and `VerifySchema` pass before enabling sweep workers.

## Rollback

If rollback is required before new sweep ledger rows are written, restore the previous constraints and drop `sweep_job_id`.

If new `sweep_hold`, `sweep_release`, or `sweep_debit` rows exist, do not drop the column or constraints. Disable sweep workers, keep the widened schema, and roll application code forward after investigation.
