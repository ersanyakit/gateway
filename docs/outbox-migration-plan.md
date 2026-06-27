# Money Event Outbox Migration Plan

Bu plan `money_event_outboxes` tablosunun production ortaminda startup `AutoMigrate` disinda yonetilmesi icindir. Development ortaminda model `services/database.Migrate` icindeki AutoMigrate listesine dahildir; production ortaminda explicit migration uygulanmalidir.

## Table

Create table `money_event_outboxes`:

| Column | Type | Notes |
| --- | --- | --- |
| `id` | uuid | Primary key. |
| `event_id` | varchar(256) | Stable event id, required. |
| `event_type` | varchar(120) | Cataloged money event name, required. |
| `event_version` | varchar(16) | Contract version, normally `v1`, required. |
| `aggregate_type` | varchar(64) | Resource family such as `payment`, `deposit`, `withdrawal`, `refund`, `sweep`, `transaction`, or `webhook_delivery`. |
| `aggregate_id` | varchar(256) | Stable aggregate/resource id. |
| `merchant_id` | uuid | Merchant/tenant scope, required. |
| `domain_id` | uuid | Domain scope, required. |
| `idempotency_key` | varchar(256) | Stable lifecycle transition idempotency key, required. |
| `payload_json` | jsonb | Canonical event payload, required. |
| `status` | varchar(32) | Outbox processing state, initially `pending`. |
| `attempts` | integer | Processing attempt count, initially `0`. |
| `locked_until` | timestamptz | Optional future worker lock. |
| `last_error` | text | Optional processing error summary. |
| `created_at` | timestamptz | Insert timestamp. |
| `updated_at` | timestamptz | Last update timestamp. |

## Required Indexes

```sql
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS ux_money_event_outboxes_event_id
  ON money_event_outboxes (event_id);

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS ux_money_event_outboxes_idempotency_scope
  ON money_event_outboxes (merchant_id, domain_id, idempotency_key);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_money_event_outbox_aggregate
  ON money_event_outboxes (aggregate_type, aggregate_id);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_money_event_outbox_status
  ON money_event_outboxes (status);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_money_event_outbox_locked_until
  ON money_event_outboxes (locked_until);
```

## Deployment Steps

1. Apply the table migration in a maintenance window or with an online schema tool.
2. Apply unique indexes before enabling code paths that write outbox rows.
3. Run schema verification in production startup; `services/database.VerifySchema` must find `money_event_outboxes.event_id`, `money_event_outboxes.idempotency_key`, and `money_event_outboxes.payload_json`.
4. Deploy Story 2.2 code with outbox writes enabled only for tested seams.
5. Story 2.3 will introduce the delivery worker that consumes this table; until then existing webhook delivery behavior remains unchanged.

## Rollback Notes

If the code deploy is rolled back before Story 2.3, leave the table and indexes in place. Existing rows are durable event records and should not be deleted unless an operator has confirmed the corresponding state transition can be replayed safely.
