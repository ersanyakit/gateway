package database

import (
	"context"
	"core/application"
	"core/constants"
	"core/models"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

type requiredSchemaColumn struct {
	table string
	model any
	field string
}

type requiredSchemaIndex struct {
	table string
	model any
	name  string
}

func normalizedAppEnv() string {
	return strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV")))
}

func envBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func IsProductionEnv() bool {
	return normalizedAppEnv() == "production"
}

func AllowAutoMigrateInProduction() bool {
	return envBool("ALLOW_AUTOMIGRATE_IN_PRODUCTION")
}

func AutoMigrateEnabled() bool {
	return !IsProductionEnv() || AllowAutoMigrateInProduction()
}

func EnableExtensions(ctx context.Context, db *gorm.DB, extensions map[string]string) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {

		for name, schema := range extensions {

			query := fmt.Sprintf(`CREATE EXTENSION IF NOT EXISTS "%s"`, name)

			if schema != "" {
				query += fmt.Sprintf(` WITH SCHEMA "%s"`, schema)
			}

			query += ";"

			if err := tx.Exec(query).Error; err != nil {
				return fmt.Errorf("failed to enable extension %s: %w", name, err)
			}
		}

		return nil
	})
}

func InitDB() error {
	_ = godotenv.Load()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}

	errorOnlyLogger := logger.New(
		log.New(os.Stderr, "\r\n", log.LstdFlags),
		logger.Config{
			LogLevel:                  logger.Error, // sadece Error
			IgnoreRecordNotFoundError: true,         // record not found'u loglama
			Colorful:                  false,
		},
	)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: errorOnlyLogger})
	if err != nil {
		return fmt.Errorf("failed to connect database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to get sql db: %w", err)
	}

	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetConnMaxLifetime(time.Hour)

	DB = db
	return nil
}

func Migrate(app *application.App) error {
	ctx := context.Background()
	if !AutoMigrateEnabled() {
		log.Println("Migration: APP_ENV=production; AutoMigrate disabled. Schema must be managed by an external migration process.")
		return VerifySchema(ctx, app.DB)
	}
	if IsProductionEnv() {
		log.Println("Migration: WARNING AutoMigrate is enabled in production by ALLOW_AUTOMIGRATE_IN_PRODUCTION=true")
	}

	extensions := map[string]string{
		"uuid-ossp": "public",
	}

	if err := EnableExtensions(ctx, app.DB, extensions); err != nil {
		return err
	}

	if err := ApplyGORMMigrations(ctx, app.DB); err != nil {
		return err
	}

	return ReconcileChainStates(ctx, app.DB)
}

func ApplyGORMMigrations(ctx context.Context, db *gorm.DB) error {
	if err := db.WithContext(ctx).AutoMigrate(autoMigrateModels()...); err != nil {
		return err
	}
	return VerifySchema(ctx, db)
}

func autoMigrateModels() []any {
	return []any{
		&models.ChainState{},
		&models.Domain{},
		&models.Merchant{},
		&models.Transaction{},
		&models.LedgerEntry{},
		&models.Wallet{},
		&models.Product{},
		&models.PaymentSession{},
		&models.IdempotencyKey{},
		&models.MoneyEventOutbox{},
		&models.WebhookDelivery{},
		&models.SweepJob{},
		&models.WithdrawalRequest{},
		&models.Refund{},
		&models.PriceQuote{},
		&models.ReconciliationJob{},
		&models.ActivityLog{},
		&models.Admin{},
	}
}

func requiredSchemaColumns() []requiredSchemaColumn {
	return []requiredSchemaColumn{
		{table: "chain_states", model: &models.ChainState{}, field: "ChainID"},
		{table: "domains", model: &models.Domain{}, field: "ID"},
		{table: "merchants", model: &models.Merchant{}, field: "ID"},
		{table: "transactions", model: &models.Transaction{}, field: "UniqueHash"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, field: "ID"},
		{table: "wallets", model: &models.Wallet{}, field: "ID"},
		{table: "products", model: &models.Product{}, field: "ID"},
		{table: "payment_sessions", model: &models.PaymentSession{}, field: "ID"},
		{table: "idempotency_keys", model: &models.IdempotencyKey{}, field: "Key"},
		{table: "money_event_outboxes", model: &models.MoneyEventOutbox{}, field: "EventID"},
		{table: "money_event_outboxes", model: &models.MoneyEventOutbox{}, field: "IdempotencyKey"},
		{table: "money_event_outboxes", model: &models.MoneyEventOutbox{}, field: "PayloadJSON"},
		{table: "money_event_outboxes", model: &models.MoneyEventOutbox{}, field: "Status"},
		{table: "webhook_deliveries", model: &models.WebhookDelivery{}, field: "ID"},
		{table: "webhook_deliveries", model: &models.WebhookDelivery{}, field: "FailureCategory"},
		{table: "webhook_deliveries", model: &models.WebhookDelivery{}, field: "OriginalDeliveryID"},
		{table: "webhook_deliveries", model: &models.WebhookDelivery{}, field: "ReplayCount"},
		{table: "webhook_deliveries", model: &models.WebhookDelivery{}, field: "OperatorAction"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "ID"},
		{table: "withdrawal_requests", model: &models.WithdrawalRequest{}, field: "ID"},
		{table: "refunds", model: &models.Refund{}, field: "ID"},
		{table: "price_quotes", model: &models.PriceQuote{}, field: "ID"},
		{table: "reconciliation_jobs", model: &models.ReconciliationJob{}, field: "ID"},
		{table: "activity_logs", model: &models.ActivityLog{}, field: "ID"},
		{table: "admins", model: &models.Admin{}, field: "ID"},
		{table: "transactions", model: &models.Transaction{}, field: "WebhookLockedUntil"},
		{table: "payment_sessions", model: &models.PaymentSession{}, field: "WebhookLockedUntil"},
		{table: "admins", model: &models.Admin{}, field: "TOTPSecret"},
	}
}

func requiredSchemaIndexes() []requiredSchemaIndex {
	return []requiredSchemaIndex{
		{table: "money_event_outboxes", model: &models.MoneyEventOutbox{}, name: "ux_money_event_outboxes_event_id"},
		{table: "money_event_outboxes", model: &models.MoneyEventOutbox{}, name: "ux_money_event_outboxes_idempotency_scope"},
	}
}

func VerifySchema(ctx context.Context, db *gorm.DB) error {
	requiredColumns := requiredSchemaColumns()

	migrator := db.WithContext(ctx).Migrator()
	for _, column := range requiredColumns {
		if !migrator.HasColumn(column.model, column.field) {
			return fmt.Errorf("schema check failed: %s.%s is missing", column.table, column.field)
		}
	}
	for _, index := range requiredSchemaIndexes() {
		if !migrator.HasIndex(index.model, index.name) {
			return fmt.Errorf("schema check failed: %s index %s is missing", index.table, index.name)
		}
	}
	return nil
}

func ReconcileChainStates(ctx context.Context, db *gorm.DB) error {
	ids := constants.AllChainIDs()
	values := make([]int64, 0, len(ids))
	checkValues := make([]string, 0, len(ids))
	for _, id := range ids {
		values = append(values, int64(id))
		checkValues = append(checkValues, fmt.Sprintf("%d", id))
	}

	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("chain_id NOT IN ?", values).Delete(&models.ChainState{}).Error; err != nil {
			return err
		}
		if err := tx.Exec(`ALTER TABLE chain_states ALTER COLUMN chain_id DROP DEFAULT`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`ALTER TABLE chain_states DROP CONSTRAINT IF EXISTS chain_states_supported_chain_id_check`).Error; err != nil {
			return err
		}
		checkSQL := fmt.Sprintf(
			`ALTER TABLE chain_states ADD CONSTRAINT chain_states_supported_chain_id_check CHECK (chain_id IN (%s))`,
			strings.Join(checkValues, ","),
		)
		return tx.Exec(checkSQL).Error
	})
}

func Seed(app *application.App) error {
	return nil
}
