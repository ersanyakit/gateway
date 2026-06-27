# Money Event Outbox GORM Migration Plan

Bu plan `money_event_outboxes` tablosunu durable money lifecycle event kaydi icin GORM ile yonetir. Migration kaynagi raw SQL DDL degil, `models.MoneyEventOutbox` GORM tag'leri, `services/database.autoMigrateModels`, `services/database.ApplyGORMMigrations`, ve `services/database.VerifySchema` kontrolleridir.

## GORM Migration Entry Point

Production migration job'u uygulama startup path'inden ayri calismalidir ve GORM entrypoint'ini cagirmalidir:

```go
ctx := context.Background()
db := openProductionGORMConnection()

if err := database.EnableExtensions(ctx, db, map[string]string{"uuid-ossp": "public"}); err != nil {
    return err
}
if err := database.ApplyGORMMigrations(ctx, db); err != nil {
    return err
}
```

`ApplyGORMMigrations` sirasiyla:

- `db.WithContext(ctx).AutoMigrate(autoMigrateModels()...)` ile GORM model migration'larini uygular.
- `VerifySchema(ctx, db)` ile zorunlu kolon ve index'leri GORM `Migrator` uzerinden dogrular.

## GORM Model Source Of Truth

`models.MoneyEventOutbox` su sozlesmenin kaynak modelidir:

| Field | GORM-managed contract |
| --- | --- |
| `EventID` | `money_event_outboxes.event_id`, unique index `ux_money_event_outboxes_event_id`. |
| `EventType` | `money_event_outboxes.event_type`, indexed event name. |
| `EventVersion` | `money_event_outboxes.event_version`, version string such as `v1`. |
| `AggregateType` | `money_event_outboxes.aggregate_type`, indexed aggregate family. |
| `AggregateID` | `money_event_outboxes.aggregate_id`, indexed aggregate/resource id. |
| `MerchantID` | `money_event_outboxes.merchant_id`, scoped idempotency index component. |
| `DomainID` | `money_event_outboxes.domain_id`, scoped idempotency index component. |
| `IdempotencyKey` | `money_event_outboxes.idempotency_key`, scoped idempotency index component. |
| `PayloadJSON` | `money_event_outboxes.payload_json`, JSONB payload field. |
| `Status` | `money_event_outboxes.status`, indexed outbox state. |
| `Attempts` | `money_event_outboxes.attempts`, attempt counter. |
| `LockedUntil` | `money_event_outboxes.locked_until`, future worker lock index. |
| `LastError` | `money_event_outboxes.last_error`, bounded failure summary. |
| `CreatedAt` | `money_event_outboxes.created_at`, GORM-managed creation timestamp. |
| `UpdatedAt` | `money_event_outboxes.updated_at`, GORM-managed update timestamp. |

Required GORM-managed indexes:

- `ux_money_event_outboxes_event_id`
- `ux_money_event_outboxes_idempotency_scope`

## Deployment Notes

- `APP_ENV=production` startup must keep automatic app startup migration disabled unless an operator intentionally sets `ALLOW_AUTOMIGRATE_IN_PRODUCTION=true`.
- The production migration pipeline should call `ApplyGORMMigrations` explicitly, then start the application with schema verification only.
- `VerifySchema` must fail startup if required outbox columns or unique indexes are missing.
- Existing `webhook_deliveries` behavior remains live while Story 2.3 moves delivery behind the webhook boundary.
- GORM does not automatically drop fields on rollback. Rollback means disabling the writer code path or deploying the prior application version while preserving durable event history.

## Verification

Run:

```bash
go test -count=1 ./services/database ./docs
```

The docs contract intentionally rejects raw SQL migration instructions here. Outbox schema drift should be expressed through GORM model tags and `VerifySchema` checks.
