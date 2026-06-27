package repositories

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestBuildMoneyEventOutboxFromCatalog(t *testing.T) {
	merchantID := uuid.New()
	domainID := uuid.New()

	event, err := BuildMoneyEventOutboxRecord(MoneyEventOutboxBuildInput{
		EventType:      "payment.succeeded.v1",
		EventID:        " payment_uuid:payment.succeeded.v1 ",
		AggregateType:  " payment ",
		AggregateID:    " payment_uuid ",
		MerchantID:     merchantID,
		DomainID:       domainID,
		IdempotencyKey: " payment_uuid:payment.succeeded.v1 ",
		Payload: map[string]any{
			"event_type": "payment.succeeded.v1",
			"event_id":   "payment_uuid:payment.succeeded.v1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.EventType != "payment.succeeded.v1" || event.EventVersion != constants.WebhookEventVersionV1 {
		t.Fatalf("event identity = %#v", event)
	}
	if event.EventID != "payment_uuid:payment.succeeded.v1" || event.AggregateType != "payment" || event.AggregateID != "payment_uuid" {
		t.Fatalf("event fields were not normalized: %#v", event)
	}
	if event.Status != models.MoneyEventOutboxStatusPending {
		t.Fatalf("status = %q", event.Status)
	}
	if event.PayloadJSON != `{"event_id":"payment_uuid:payment.succeeded.v1","event_type":"payment.succeeded.v1"}` {
		t.Fatalf("payload was not canonicalized: %s", event.PayloadJSON)
	}
}

func TestBuildMoneyEventOutboxRejectsUnknownCatalogEvent(t *testing.T) {
	_, err := BuildMoneyEventOutboxRecord(MoneyEventOutboxBuildInput{
		EventType:      "unknown.event.v1",
		EventID:        "evt",
		AggregateType:  "payment",
		AggregateID:    "payment_uuid",
		MerchantID:     uuid.New(),
		DomainID:       uuid.New(),
		IdempotencyKey: "idem",
		Payload:        map[string]any{"ok": true},
	})
	if err == nil {
		t.Fatal("unknown catalog event should fail")
	}
}

func TestValidateMoneyEventOutboxValidationAndDefaults(t *testing.T) {
	base := testMoneyEventOutbox()
	base.Status = ""
	base.PayloadJSON = `{"b":2,"a":1}`

	if err := validateMoneyEventOutbox(&base); err != nil {
		t.Fatal(err)
	}
	if base.EventVersion != constants.WebhookEventVersionV1 {
		t.Fatalf("event version = %q", base.EventVersion)
	}
	if base.Status != models.MoneyEventOutboxStatusPending {
		t.Fatalf("status = %q", base.Status)
	}
	if base.PayloadJSON != `{"a":1,"b":2}` {
		t.Fatalf("payload = %s", base.PayloadJSON)
	}

	invalid := base
	invalid.EventID = ""
	if err := validateMoneyEventOutbox(&invalid); !errors.Is(err, ErrMoneyEventOutboxInvalid) {
		t.Fatalf("missing event id err = %v", err)
	}

	invalid = base
	invalid.PayloadJSON = `{`
	if err := validateMoneyEventOutbox(&invalid); err == nil {
		t.Fatalf("invalid payload err = %v", err)
	}
}

func TestMoneyEventOutboxCompatibility(t *testing.T) {
	existing := testMoneyEventOutbox()
	if err := validateMoneyEventOutbox(&existing); err != nil {
		t.Fatal(err)
	}
	compatible := existing
	compatible.ID = uuid.New()
	compatible.PayloadJSON = `{ "resource_id": "payment_uuid", "event_type": "payment.succeeded.v1" }`
	if err := validateMoneyEventOutbox(&compatible); err != nil {
		t.Fatal(err)
	}
	if !moneyEventOutboxCompatible(&existing, &compatible) {
		t.Fatal("same identity and canonical payload should be compatible")
	}

	conflicting := compatible
	conflicting.EventID = "other:event"
	if moneyEventOutboxCompatible(&existing, &conflicting) {
		t.Fatal("different event id should be incompatible")
	}

	conflicting = compatible
	conflicting.PayloadJSON = `{"resource_id":"payment_uuid","event_type":"payment.failed.v1"}`
	if err := validateMoneyEventOutbox(&conflicting); err != nil {
		t.Fatal(err)
	}
	if moneyEventOutboxCompatible(&existing, &conflicting) {
		t.Fatal("different payload should be incompatible")
	}
}

func TestMoneyEventOutboxRepoPostgresTransactionSemantics(t *testing.T) {
	ctx := context.Background()
	db := openMoneyEventOutboxPostgresTestDB(t)
	prefix := "money-outbox-test-" + uuid.NewString()

	repo := NewMoneyEventOutboxRepo(db)
	merchantID := uuid.New()
	domainID := uuid.New()

	commitStateID := uuid.New()
	commitEvent := testMoneyEventOutboxWithIDs(prefix+":event:commit", prefix+":idem:commit", merchantID, domainID)
	commitEvent.AggregateID = commitStateID.String()
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&moneyOutboxTestState{ID: commitStateID, Name: prefix + ":commit"}).Error; err != nil {
			return err
		}
		_, created, err := repo.RecordWithDB(ctx, tx, &commitEvent)
		if err != nil {
			return err
		}
		if !created {
			return errors.New("first outbox insert should create row")
		}
		return nil
	}); err != nil {
		t.Fatalf("commit transaction: %v", err)
	}
	requirePostgresCount(t, db, &moneyOutboxTestState{}, "id = ?", commitStateID, 1)
	requirePostgresCount(t, db, &models.MoneyEventOutbox{}, "event_id = ?", commitEvent.EventID, 1)

	rollbackStateID := uuid.New()
	rollbackEvent := testMoneyEventOutboxWithIDs(prefix+":event:rollback", prefix+":idem:rollback", merchantID, domainID)
	rollbackEvent.AggregateID = rollbackStateID.String()
	errRollback := errors.New("force rollback")
	rollbackErr := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&moneyOutboxTestState{ID: rollbackStateID, Name: prefix + ":rollback"}).Error; err != nil {
			return err
		}
		if _, _, err := repo.RecordWithDB(ctx, tx, &rollbackEvent); err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(rollbackErr, errRollback) {
		t.Fatalf("rollback err = %v", rollbackErr)
	}
	requirePostgresCount(t, db, &moneyOutboxTestState{}, "id = ?", rollbackStateID, 0)
	requirePostgresCount(t, db, &models.MoneyEventOutbox{}, "event_id = ?", rollbackEvent.EventID, 0)

	row, created, err := repo.Record(ctx, &commitEvent)
	if err != nil {
		t.Fatalf("duplicate record: %v", err)
	}
	if created || row.EventID != commitEvent.EventID {
		t.Fatalf("duplicate should no-op and return existing row: created=%v row=%#v", created, row)
	}
	requirePostgresCount(t, db, &models.MoneyEventOutbox{}, "idempotency_key = ?", commitEvent.IdempotencyKey, 1)

	conflict := commitEvent
	conflict.EventID = prefix + ":event:conflict"
	conflict.PayloadJSON = `{"event_type":"payment.failed.v1","resource_id":"payment_uuid"}`
	if _, _, err := repo.Record(ctx, &conflict); !errors.Is(err, ErrMoneyEventOutboxConflict) {
		t.Fatalf("conflicting duplicate err = %v", err)
	}
}

type moneyOutboxTestState struct {
	ID   uuid.UUID `gorm:"type:uuid;primaryKey"`
	Name string    `gorm:"size:128;index"`
}

func testMoneyEventOutbox() models.MoneyEventOutbox {
	merchantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	domainID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	return testMoneyEventOutboxWithIDs("payment_uuid:payment.succeeded.v1", "payment_uuid:payment.succeeded.v1", merchantID, domainID)
}

func testMoneyEventOutboxWithIDs(eventID, idempotencyKey string, merchantID, domainID uuid.UUID) models.MoneyEventOutbox {
	return models.MoneyEventOutbox{
		ID:             uuid.New(),
		EventID:        eventID,
		EventType:      "payment.succeeded.v1",
		EventVersion:   constants.WebhookEventVersionV1,
		AggregateType:  "payment",
		AggregateID:    "payment_uuid",
		MerchantID:     merchantID,
		DomainID:       domainID,
		IdempotencyKey: idempotencyKey,
		PayloadJSON:    `{"event_type":"payment.succeeded.v1","resource_id":"payment_uuid"}`,
		Status:         models.MoneyEventOutboxStatusPending,
	}
}

func requirePostgresCount(t *testing.T, db *gorm.DB, model any, query string, args any, want int64) {
	t.Helper()
	var count int64
	if err := db.Model(model).Where(query, args).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("count %s = %d, want %d", query, count, want)
	}
}

func openMoneyEventOutboxPostgresTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("OUTBOX_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("MONEY_OUTBOX_TEST_DATABASE_URL")
	}
	if dsn == "" {
		dsn = os.Getenv("TEST_DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("set OUTBOX_TEST_DATABASE_URL to run Postgres outbox transaction tests")
	}

	adminDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("connect test postgres: %v", err)
	}
	if err := adminDB.Exec(`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`).Error; err != nil {
		t.Fatalf("enable uuid extension: %v", err)
	}
	schemaName := "money_outbox_test_" + strings.ReplaceAll(uuid.NewString(), "-", "_")
	quotedSchema := quotePostgresIdentifier(schemaName)
	if err := adminDB.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		t.Fatalf("create test schema: %v", err)
	}

	db, err := gorm.Open(postgres.Open(postgresDSNWithSearchPath(dsn, schemaName)), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("connect schema-scoped test postgres: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get test sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	if err := db.AutoMigrate(&models.MoneyEventOutbox{}, &moneyOutboxTestState{}); err != nil {
		t.Fatalf("automigrate test schema: %v", err)
	}
	t.Cleanup(func() {
		_ = sqlDB.Close()
		_ = adminDB.Exec("DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE").Error
		if adminSQL, err := adminDB.DB(); err == nil {
			_ = adminSQL.Close()
		}
	})
	return db
}

func postgresDSNWithSearchPath(dsn, schemaName string) string {
	searchPath := schemaName + ",public"
	if strings.Contains(dsn, "://") {
		parsed, err := url.Parse(dsn)
		if err == nil {
			query := parsed.Query()
			query.Set("search_path", searchPath)
			parsed.RawQuery = query.Encode()
			return parsed.String()
		}
	}
	return strings.TrimSpace(dsn) + " search_path=" + searchPath
}

func quotePostgresIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}
