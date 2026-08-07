# Production Migration Discipline

Gateway production schema changes use versioned GORM migration artifacts, not startup-only `AutoMigrate` and not ad hoc raw SQL files in this repo.

## Current Artifact Registry

Versioned migration metadata lives in `services/dbmigrations`.

Every artifact must include an ID, model list, summary, forward-only plan, lock-impact note, backfill plan, rollback note, verification query description, and test command. `dbmigrations.Validate()` rejects incomplete metadata.

Current artifacts:

- `202606280001_provider_health_wallet_address_lookup`: `models.ProviderHealthSnapshot`, `models.WalletAddressLookup`; backfill owner `repositories.WalletAddressLookupRepo.BackfillWallets`.
- `202606280002_wallet_address_lifecycle_pool`: `models.WalletAddressReservation`, `models.WalletAddress`, `models.WalletAddressGapScanCursor`, `models.WalletAddressGapScanAnomaly`; backfill owner `repositories.WalletAddressRepo.BackfillWallets`.
- `202606280003_immutable_ledger_journal_projection`: `models.LedgerBalanceProjection`; backfill owner `repositories.LedgerRepo.RebuildBalanceProjections`.
- `202606280004_deposit_settlement_allocations`: `models.PaymentDepositAllocation`, `models.ChainFact`, `models.Deposit`, `models.PaymentSession`.
- `202606280005_durable_outbound_transaction_manager`: `models.OutboundTransaction`, `models.OutboundChainResourceReservation`.
- `202606290006_reliability_substrate_inbox_worker_leases`: `models.MoneyEventInbox`, `models.WorkerLease`.
- `202606290007_chain_scanner_continuity_governance`: `models.ChainState`, `models.Block`.
- `202606290008_webhook_ordering_resource_sequences`: `models.WebhookDelivery`, `models.WebhookResourceSequence`.
- `202606290009_merchant_api_security_controls`: `models.Domain`, `models.APIRateLimitCounter`.
- `202606290010_sweep_batching_gas_funding_recovery`: `models.SweepJob`.
- `202607130011_payment_asset_metadata_product_snapshot_notification_targets`: `models.Domain`, `models.Product`, `models.PaymentSession`.
- `202607130012_wallet_chiliz_spicy_partial_unique`: `models.Wallet`.
- `202607130013_network_operational_states`: `models.NetworkOperationalState`.
- `202607180014_canonical_block_money_event_sequence_invariants`: `models.Block`, `models.MoneyEventOutbox`, `models.WebhookDelivery`; preflight owner `services/dbmigrations.Prepare`.

Verification owner for all artifacts is `services/database.VerifySchema`.

## Production Gate

Application startup keeps `AutoMigrate` disabled when `APP_ENV=production` unless an operator explicitly sets `ALLOW_AUTOMIGRATE_IN_PRODUCTION=true`. That override is a launch blocker and readiness reports it as unhealthy.

Production readiness also requires:

- `GATEWAY_DB_MIGRATION_VERSION` equals `dbmigrations.LatestID()`.
- `services/database.VerifySchema` passes.
- The migration artifact registry validates through `dbmigrations.Validate()`.
- Repository tests for the changed tables pass.

## GORM Apply Path

A controlled migration job should open a production GORM connection, validate the versioned artifact registry, and run the GORM migration entrypoint that also reconciles schema guards:

```go
ctx := context.Background()
db := openProductionGORMConnection()

if err := dbmigrations.Validate(); err != nil {
    return err
}
if err := database.ApplyGORMMigrations(ctx, db); err != nil {
    return err
}
if err := database.VerifySchema(ctx, db); err != nil {
    return err
}
```

Data backfills must stay in Go/GORM code where the ownership rules are tested. For wallet lookup, `WalletAddressLookupRepo.BackfillWallets` loads wallets in bounded batches, normalizes addresses by chain family, and rejects conflicting ownership. For ledger projections, `LedgerRepo.RebuildBalanceProjections` derives rows only from active numeric `ledger_entries`.

## Lock And Index Strategy

Large or hot tables must be handled as an operator-controlled job, not as normal web startup:

- Pause or drain the workers that write to the affected table before applying indexes to `webhook_deliveries`, `blocks`, `chain_facts`, `ledger_entries`, `outbound_transactions`, `sweep_jobs`, and `api_rate_limit_counters`.
- Prefer low-write maintenance windows for GORM-managed index reconciliation. If a table is already large enough that blocking index creation is unacceptable, use an operator-approved online index plan outside web startup and keep the GORM tag plus `VerifySchema` guard as the source of drift detection.
- Run `services/database.VerifySchema` immediately after applying the migration job. Missing table, column, index, constraint, or trigger details must fail readiness before traffic is restored.
- Backfills must be bounded, resumable, and conflict-aware; ownership conflicts stop the job and require operator review.
- `202607180014_canonical_block_money_event_sequence_invariants` creates or verifies the small `network_operational_states` safety table before reconciling duplicate canonical history. Every affected chain is set to `maintenance` in the same transaction, which blocks deposits, withdrawals, refunds, and sweeps while authoritative replay repairs money state.
- A migration-created maintenance gate is intentionally sticky. Its reason names the migration and requires authoritative scanner replay, money-state reconciliation, and operator acknowledgement. Migration or replay code must never switch it back to `active`; a privileged administrator reviews the evidence and explicitly reactivates the network from `/admin/networks`.

## Operator Checklist

- Confirm `APP_ENV=production` and `ALLOW_AUTOMIGRATE_IN_PRODUCTION` is unset or false.
- Run the GORM migration job during a maintenance window.
- Run `go test -tags walletcorefallback -count=1 ./services/dbmigrations ./services/database ./repositories`.
- Run `LedgerRepo.RebuildBalanceProjections` after applying `202606280003_immutable_ledger_journal_projection`.
- Run wallet address lookup and lifecycle backfills after their artifacts if the deployment is upgrading an existing wallet table.
- For every chain whose `chain_states.continuity_status` is `rollback_required`, confirm `/admin/networks` shows `maintenance`, `updated_by=migration`, and the full migration reason before starting replay. Keep the gate closed until scanner replay and downstream transaction, deposit, payment, ledger, inbox, and reconciliation evidence agree; then require a privileged operator to acknowledge the evidence and explicitly select `active`.
- Confirm `dbmigrations.LatestID()` returns the artifact being recorded in production evidence.
- Set `GATEWAY_DB_MIGRATION_VERSION` to the latest artifact id after the migration and backfill pass.
- Start the app and verify `GET /api/v1/common/readiness` returns a healthy `migration.strategy` check.

## Rollback

These changes are additive. Rollback means deploying prior code and leaving additive columns/tables in place unless an operator-approved GORM migration removes them after confirming no code path reads them.
