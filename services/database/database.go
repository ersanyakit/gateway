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
	err := godotenv.Load()

	if err != nil {
		log.Fatal("Error loading .env file", err)
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		panic("DATABASE_URL is required")
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
		panic("failed to connect database")
	}

	sqlDB, err := db.DB()
	if err != nil {
		// Hata işleme
	}

	sqlDB.SetMaxIdleConns(10)           // Boşta bekleyen bağlantıların maksimum sayısı
	sqlDB.SetMaxOpenConns(0)            // Aynı anda açık olabilecek maksimum bağlantı sayısı
	sqlDB.SetConnMaxLifetime(time.Hour) // Bağlantının yeniden kullanılabilir olacağı maksimum süre

	DB = db
	return nil
}

func CreateSquence(db *gorm.DB) error {

	if err := db.Exec(`
		CREATE SEQUENCE IF NOT EXISTS domain_hd_account_seq
		START 1
		MAXVALUE 2147483647;
	`).Error; err != nil {
		return err
	}

	if err := db.Exec(`
		CREATE SEQUENCE IF NOT EXISTS wallet_hd_address_seq
		START 1
		MAXVALUE 2147483647;
	`).Error; err != nil {
		return err
	}
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

	fmt.Println("CreateSquence:Begin")

	if err := CreateSquence(app.DB); err != nil {
		return err
	}
	fmt.Println("CreateSquence:End")

	fmt.Println("Migration:Begin")

	err := app.DB.AutoMigrate(

		&models.Domain{},
		&models.Merchant{},
		&models.Transaction{},
		&models.Wallet{},
	)

	return err
}

func Seed(app *application.App) error {
	fmt.Println("Seed:Begin")

	return nil
}
