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

type requiredSchemaConstraint struct {
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

	// AutoMigrate can change SELECT * result shapes during startup; avoid stale pgx cached plans.
	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN:                  dsn,
		PreferSimpleProtocol: true,
	}), &gorm.Config{Logger: errorOnlyLogger})
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
		&models.Block{},
		&models.ChainFact{},
		&models.Deposit{},
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
		{table: "blocks", model: &models.Block{}, field: "ChainID"},
		{table: "blocks", model: &models.Block{}, field: "Number"},
		{table: "blocks", model: &models.Block{}, field: "Hash"},
		{table: "blocks", model: &models.Block{}, field: "ParentHash"},
		{table: "blocks", model: &models.Block{}, field: "Processed"},
		{table: "blocks", model: &models.Block{}, field: "Canonical"},
		{table: "blocks", model: &models.Block{}, field: "Status"},
		{table: "blocks", model: &models.Block{}, field: "ReorgedAt"},
		{table: "blocks", model: &models.Block{}, field: "SupersededByHash"},
		{table: "blocks", model: &models.Block{}, field: "CorrectionReason"},
		{table: "chain_facts", model: &models.ChainFact{}, field: "EventID"},
		{table: "chain_facts", model: &models.ChainFact{}, field: "ChainID"},
		{table: "chain_facts", model: &models.ChainFact{}, field: "BlockNumber"},
		{table: "chain_facts", model: &models.ChainFact{}, field: "TxHash"},
		{table: "chain_facts", model: &models.ChainFact{}, field: "LogIndex"},
		{table: "chain_facts", model: &models.ChainFact{}, field: "ObservedAddress"},
		{table: "chain_facts", model: &models.ChainFact{}, field: "AmountRaw"},
		{table: "chain_facts", model: &models.ChainFact{}, field: "SourceEventType"},
		{table: "chain_facts", model: &models.ChainFact{}, field: "RawMetadataJSON"},
		{table: "chain_facts", model: &models.ChainFact{}, field: "ConfirmationsRequired"},
		{table: "chain_facts", model: &models.ChainFact{}, field: "Status"},
		{table: "chain_facts", model: &models.ChainFact{}, field: "ReorgedAt"},
		{table: "chain_facts", model: &models.ChainFact{}, field: "SupersededByEventID"},
		{table: "chain_facts", model: &models.ChainFact{}, field: "CorrectionReason"},
		{table: "deposits", model: &models.Deposit{}, field: "ID"},
		{table: "deposits", model: &models.Deposit{}, field: "ChainFactID"},
		{table: "deposits", model: &models.Deposit{}, field: "ChainFactEventID"},
		{table: "deposits", model: &models.Deposit{}, field: "Status"},
		{table: "deposits", model: &models.Deposit{}, field: "WalletID"},
		{table: "deposits", model: &models.Deposit{}, field: "MerchantID"},
		{table: "deposits", model: &models.Deposit{}, field: "DomainID"},
		{table: "deposits", model: &models.Deposit{}, field: "ProductID"},
		{table: "deposits", model: &models.Deposit{}, field: "UserID"},
		{table: "deposits", model: &models.Deposit{}, field: "ChainID"},
		{table: "deposits", model: &models.Deposit{}, field: "BlockNumber"},
		{table: "deposits", model: &models.Deposit{}, field: "TxHash"},
		{table: "deposits", model: &models.Deposit{}, field: "LogIndex"},
		{table: "deposits", model: &models.Deposit{}, field: "ObservedAddress"},
		{table: "deposits", model: &models.Deposit{}, field: "Direction"},
		{table: "deposits", model: &models.Deposit{}, field: "Token"},
		{table: "deposits", model: &models.Deposit{}, field: "Symbol"},
		{table: "deposits", model: &models.Deposit{}, field: "Decimals"},
		{table: "deposits", model: &models.Deposit{}, field: "AmountRaw"},
		{table: "deposits", model: &models.Deposit{}, field: "Confirmations"},
		{table: "deposits", model: &models.Deposit{}, field: "ConfirmationsRequired"},
		{table: "deposits", model: &models.Deposit{}, field: "TransactionUniqueHash"},
		{table: "deposits", model: &models.Deposit{}, field: "SourceEventType"},
		{table: "deposits", model: &models.Deposit{}, field: "UnmatchedReason"},
		{table: "deposits", model: &models.Deposit{}, field: "ReorgedAt"},
		{table: "deposits", model: &models.Deposit{}, field: "SupersededByEventID"},
		{table: "deposits", model: &models.Deposit{}, field: "CorrectionReason"},
		{table: "deposits", model: &models.Deposit{}, field: "DetectedAt"},
		{table: "deposits", model: &models.Deposit{}, field: "FinalizedAt"},
		{table: "domains", model: &models.Domain{}, field: "ID"},
		{table: "merchants", model: &models.Merchant{}, field: "ID"},
		{table: "transactions", model: &models.Transaction{}, field: "UniqueHash"},
		{table: "transactions", model: &models.Transaction{}, field: "OriginalEventID"},
		{table: "transactions", model: &models.Transaction{}, field: "OriginalResourceID"},
		{table: "transactions", model: &models.Transaction{}, field: "CorrectionReason"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, field: "ID"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, field: "MerchantID"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, field: "DomainID"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, field: "WalletID"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, field: "PaymentID"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, field: "TransactionUniqueHash"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, field: "WithdrawalID"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, field: "RefundID"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, field: "SweepJobID"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, field: "ChainID"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, field: "Token"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, field: "Symbol"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, field: "Decimals"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, field: "EntryType"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, field: "Account"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, field: "Direction"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, field: "Status"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, field: "AmountRaw"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, field: "IdempotencyKey"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, field: "Reference"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, field: "PostedAt"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, field: "VoidedAt"},
		{table: "wallets", model: &models.Wallet{}, field: "ID"},
		{table: "products", model: &models.Product{}, field: "ID"},
		{table: "products", model: &models.Product{}, field: "LinkType"},
		{table: "payment_sessions", model: &models.PaymentSession{}, field: "ID"},
		{table: "payment_sessions", model: &models.PaymentSession{}, field: "LinkType"},
		{table: "payment_sessions", model: &models.PaymentSession{}, field: "PaymentOutcome"},
		{table: "payment_sessions", model: &models.PaymentSession{}, field: "PaymentOutcomeReason"},
		{table: "payment_sessions", model: &models.PaymentSession{}, field: "MatchedAmountRaw"},
		{table: "payment_sessions", model: &models.PaymentSession{}, field: "ShortfallAmountRaw"},
		{table: "payment_sessions", model: &models.PaymentSession{}, field: "ExcessAmountRaw"},
		{table: "idempotency_keys", model: &models.IdempotencyKey{}, field: "Key"},
		{table: "idempotency_keys", model: &models.IdempotencyKey{}, field: "ResourceType"},
		{table: "idempotency_keys", model: &models.IdempotencyKey{}, field: "ResourceID"},
		{table: "money_event_outboxes", model: &models.MoneyEventOutbox{}, field: "EventID"},
		{table: "money_event_outboxes", model: &models.MoneyEventOutbox{}, field: "IdempotencyKey"},
		{table: "money_event_outboxes", model: &models.MoneyEventOutbox{}, field: "PayloadJSON"},
		{table: "money_event_outboxes", model: &models.MoneyEventOutbox{}, field: "Status"},
		{table: "webhook_deliveries", model: &models.WebhookDelivery{}, field: "ID"},
		{table: "webhook_deliveries", model: &models.WebhookDelivery{}, field: "FailureCategory"},
		{table: "webhook_deliveries", model: &models.WebhookDelivery{}, field: "OriginalDeliveryID"},
		{table: "webhook_deliveries", model: &models.WebhookDelivery{}, field: "ReplayCount"},
		{table: "webhook_deliveries", model: &models.WebhookDelivery{}, field: "ReplayRequestedBy"},
		{table: "webhook_deliveries", model: &models.WebhookDelivery{}, field: "ReplayRequestedAt"},
		{table: "webhook_deliveries", model: &models.WebhookDelivery{}, field: "OperatorAction"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "ID"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "TransactionUniqueHash"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "TransactionHash"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "WalletID"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "MerchantID"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "ChainID"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "Token"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "Status"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "Attempts"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "MaxAttempts"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "LastError"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "FailureCategory"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "NextRunAt"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "LockedUntil"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "SweepTxHash"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "PrefundAttempts"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "PrefundLastError"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "PrefundFailureCategory"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "PrefundLastAttemptAt"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "PrefundedAt"},
		{table: "withdrawal_requests", model: &models.WithdrawalRequest{}, field: "ID"},
		{table: "withdrawal_requests", model: &models.WithdrawalRequest{}, field: "BroadcastedAt"},
		{table: "withdrawal_requests", model: &models.WithdrawalRequest{}, field: "FinalizedAt"},
		{table: "withdrawal_requests", model: &models.WithdrawalRequest{}, field: "IdempotencyKey"},
		{table: "withdrawal_requests", model: &models.WithdrawalRequest{}, field: "CorrelationID"},
		{table: "refunds", model: &models.Refund{}, field: "ID"},
		{table: "refunds", model: &models.Refund{}, field: "WalletID"},
		{table: "refunds", model: &models.Refund{}, field: "Chain"},
		{table: "refunds", model: &models.Refund{}, field: "Token"},
		{table: "refunds", model: &models.Refund{}, field: "Symbol"},
		{table: "refunds", model: &models.Refund{}, field: "Decimals"},
		{table: "refunds", model: &models.Refund{}, field: "ToAddress"},
		{table: "refunds", model: &models.Refund{}, field: "BroadcastedAt"},
		{table: "refunds", model: &models.Refund{}, field: "FinalizedAt"},
		{table: "refunds", model: &models.Refund{}, field: "IdempotencyKey"},
		{table: "refunds", model: &models.Refund{}, field: "CorrelationID"},
		{table: "price_quotes", model: &models.PriceQuote{}, field: "ID"},
		{table: "reconciliation_jobs", model: &models.ReconciliationJob{}, field: "ID"},
		{table: "reconciliation_jobs", model: &models.ReconciliationJob{}, field: "MerchantID"},
		{table: "reconciliation_jobs", model: &models.ReconciliationJob{}, field: "DomainID"},
		{table: "reconciliation_jobs", model: &models.ReconciliationJob{}, field: "ScopeKey"},
		{table: "reconciliation_jobs", model: &models.ReconciliationJob{}, field: "ResourceType"},
		{table: "reconciliation_jobs", model: &models.ReconciliationJob{}, field: "ResourceID"},
		{table: "reconciliation_jobs", model: &models.ReconciliationJob{}, field: "AffectedResourceIDsJSON"},
		{table: "reconciliation_jobs", model: &models.ReconciliationJob{}, field: "EvidenceJSON"},
		{table: "reconciliation_jobs", model: &models.ReconciliationJob{}, field: "Outcome"},
		{table: "reconciliation_jobs", model: &models.ReconciliationJob{}, field: "NextRunAt"},
		{table: "activity_logs", model: &models.ActivityLog{}, field: "ID"},
		{table: "activity_logs", model: &models.ActivityLog{}, field: "CorrelationID"},
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
		{table: "blocks", model: &models.Block{}, name: "ux_blocks_chain_hash"},
		{table: "blocks", model: &models.Block{}, name: "ux_blocks_chain_number_hash"},
		{table: "chain_facts", model: &models.ChainFact{}, name: "ux_chain_facts_event_id"},
		{table: "deposits", model: &models.Deposit{}, name: "ux_deposits_chain_fact_event_id"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, name: "ux_ledger_idempotent_account"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, name: "idx_ledger_entries_sweep_job_id"},
		{table: "sweep_jobs", model: &models.SweepJob{}, name: "idx_sweep_jobs_transaction_unique_hash"},
		{table: "sweep_jobs", model: &models.SweepJob{}, name: "idx_sweep_jobs_failure_category"},
		{table: "sweep_jobs", model: &models.SweepJob{}, name: "idx_sweep_jobs_prefund_failure_category"},
		{table: "sweep_jobs", model: &models.SweepJob{}, name: "idx_sweep_jobs_prefund_last_attempt_at"},
		{table: "withdrawal_requests", model: &models.WithdrawalRequest{}, name: "idx_withdrawal_requests_idempotency_key"},
		{table: "withdrawal_requests", model: &models.WithdrawalRequest{}, name: "idx_withdrawal_requests_correlation_id"},
		{table: "withdrawal_requests", model: &models.WithdrawalRequest{}, name: "idx_withdrawal_requests_broadcasted_at"},
		{table: "withdrawal_requests", model: &models.WithdrawalRequest{}, name: "idx_withdrawal_requests_finalized_at"},
		{table: "refunds", model: &models.Refund{}, name: "idx_refunds_wallet_id"},
		{table: "refunds", model: &models.Refund{}, name: "idx_refunds_idempotency_key"},
		{table: "refunds", model: &models.Refund{}, name: "idx_refunds_correlation_id"},
		{table: "refunds", model: &models.Refund{}, name: "idx_refunds_broadcasted_at"},
		{table: "refunds", model: &models.Refund{}, name: "idx_refunds_finalized_at"},
		{table: "idempotency_keys", model: &models.IdempotencyKey{}, name: "idx_idempotency_keys_resource_id"},
		{table: "reconciliation_jobs", model: &models.ReconciliationJob{}, name: "idx_reconciliation_jobs_merchant_id"},
		{table: "reconciliation_jobs", model: &models.ReconciliationJob{}, name: "idx_reconciliation_jobs_domain_id"},
		{table: "reconciliation_jobs", model: &models.ReconciliationJob{}, name: "idx_reconciliation_jobs_scope_key"},
		{table: "reconciliation_jobs", model: &models.ReconciliationJob{}, name: "idx_reconciliation_jobs_resource_type"},
		{table: "reconciliation_jobs", model: &models.ReconciliationJob{}, name: "idx_reconciliation_jobs_resource_id"},
		{table: "reconciliation_jobs", model: &models.ReconciliationJob{}, name: "idx_reconciliation_jobs_outcome"},
		{table: "reconciliation_jobs", model: &models.ReconciliationJob{}, name: "idx_reconciliation_jobs_next_run_at"},
		{table: "activity_logs", model: &models.ActivityLog{}, name: "idx_activity_logs_correlation_id"},
	}
}

func requiredSchemaConstraints() []requiredSchemaConstraint {
	return []requiredSchemaConstraint{
		{table: "ledger_entries", model: &models.LedgerEntry{}, name: "ledger_entries_entry_type_check"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, name: "ledger_entries_account_check"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, name: "ledger_entries_direction_check"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, name: "ledger_entries_status_check"},
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
	for _, constraint := range requiredSchemaConstraints() {
		if !migrator.HasConstraint(constraint.model, constraint.name) {
			return fmt.Errorf("schema check failed: %s constraint %s is missing", constraint.table, constraint.name)
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
