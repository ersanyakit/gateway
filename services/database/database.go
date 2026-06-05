package database

import (
	"context"
	"core/application"
	"core/models"
	"fmt"
	"log"
	"os"
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

	fmt.Println("EnableExtensions:Begin")

	extensions := map[string]string{
		"uuid-ossp": "public",
	}

	if err := EnableExtensions(context.Background(), app.DB, extensions); err != nil {
		return err
	}
	fmt.Println("EnableExtensions:End")

	fmt.Println("Migration:Begin")

	err := app.DB.AutoMigrate(

		&models.ChainState{},
		&models.Domain{},
		&models.Merchant{},
		&models.Transaction{},
		&models.Wallet{},
		&models.Product{},
		&models.PaymentSession{},
		&models.WithdrawalRequest{},
		&models.ActivityLog{},
	)

	return err
}

func Seed(app *application.App) error {
	fmt.Println("Seed:Begin")

	return nil
}
