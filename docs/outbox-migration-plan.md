# Money Event Outbox Migration Plan

Bu plan `money_event_outboxes` tablosunu durable money lifecycle event kaydi icin tanimlar. Startup `AutoMigrate` sadece development kolayligidir; production migration mekanizmasi degildir.

## Ileri Migration

Money event outbox satiri yazan kod production'a alinmadan once bu DDL production migration sureciyle uygulanmalidir.

```sql
CREATE TABLE IF NOT EXISTS money_event_outboxes (
    id uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
    event_id varchar(256) NOT NULL,
    event_type varchar(120) NOT NULL,
    event_version varchar(16) NOT NULL DEFAULT 'v1',
    aggregate_type varchar(64) NOT NULL,
    aggregate_id varchar(256) NOT NULL,
    merchant_id uuid NOT NULL,
    domain_id uuid NOT NULL,
    idempotency_key varchar(256) NOT NULL,
    payload_json jsonb NOT NULL,
    status varchar(32) NOT NULL DEFAULT 'pending',
    attempts bigint NOT NULL DEFAULT 0,
    locked_until timestamptz NULL,
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS ux_money_event_outboxes_event_id
    ON money_event_outboxes (event_id);

CREATE UNIQUE INDEX IF NOT EXISTS ux_money_event_outboxes_idempotency_scope
    ON money_event_outboxes (merchant_id, domain_id, idempotency_key);

CREATE INDEX IF NOT EXISTS idx_money_event_outboxes_event_type
    ON money_event_outboxes (event_type);

CREATE INDEX IF NOT EXISTS idx_money_event_outboxes_status
    ON money_event_outboxes (status);

CREATE INDEX IF NOT EXISTS idx_money_event_outboxes_locked_until
    ON money_event_outboxes (locked_until);

CREATE INDEX IF NOT EXISTS idx_money_event_outbox_aggregate
    ON money_event_outboxes (aggregate_type, aggregate_id);
```

## Deployment Notlari

- Live webhook delivery halen `webhook_deliveries` kullanirken bu migration uygulanabilir; Story 2.2 sadece durable event substrate olusturur.
- `APP_ENV=production` ortaminda `ALLOW_AUTOMIGRATE_IN_PRODUCTION` set edilmemelidir; `VerifySchema` disaridan yonetilen schemayi dogrular.
- Table DDL uygulanmadan once `uuid-ossp` extension'inin mevcut oldugu dogrulanmalidir.
- Bu story icin historical backfill gerekmez.

## Rollback

Rollback sadece production write baslamadan once gecerlidir:

```sql
DROP TABLE IF EXISTS money_event_outboxes;
```

Write basladiktan sonra tablo drop edilmemeli; event history korunmali ve gerekiyorsa writer code path'i kapatilmalidir.
