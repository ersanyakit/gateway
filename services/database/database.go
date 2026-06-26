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
	extensions := map[string]string{
		"uuid-ossp": "public",
	}

	if err := EnableExtensions(context.Background(), app.DB, extensions); err != nil {
		return err
	}

	err := app.DB.AutoMigrate(

		&models.ChainState{},
		&models.Domain{},
		&models.Merchant{},
		&models.Transaction{},
		&models.LedgerEntry{},
		&models.Wallet{},
		&models.Product{},
		&models.PaymentSession{},
		&models.IdempotencyKey{},
		&models.WebhookDelivery{},
		&models.SweepJob{},
		&models.WithdrawalRequest{},
		&models.Refund{},
		&models.PriceQuote{},
		&models.ReconciliationJob{},
		&models.ActivityLog{},
		&models.Admin{},
	)

	if err != nil {
		return err
	}

	if err := VerifySchema(context.Background(), app.DB); err != nil {
		return err
	}

	return ReconcileChainStates(context.Background(), app.DB)
}

func VerifySchema(ctx context.Context, db *gorm.DB) error {
	requiredColumns := []struct {
		table string
		model any
		field string
	}{
		{table: "transactions", model: &models.Transaction{}, field: "WebhookLockedUntil"},
		{table: "payment_sessions", model: &models.PaymentSession{}, field: "WebhookLockedUntil"},
		{table: "admins", model: &models.Admin{}, field: "TOTPSecret"},
	}

	migrator := db.WithContext(ctx).Migrator()
	for _, column := range requiredColumns {
		if !migrator.HasColumn(column.model, column.field) {
			return fmt.Errorf("schema check failed: %s.%s is missing after AutoMigrate", column.table, column.field)
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
