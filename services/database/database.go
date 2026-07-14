package database

import (
	"context"
	"core/application"
	"core/constants"
	"core/models"
	"database/sql"
	"fmt"
	"log"
	"os"
	"regexp"
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

type requiredSchemaTrigger struct {
	table string
	name  string
}

type checkConstraintSpec struct {
	table  string
	name   string
	column string
	values []string
}

const (
	ledgerEntryImmutabilityFunctionName = "fn_reject_ledger_entries_mutation"
	ledgerEntryImmutabilityTriggerName  = "trg_ledger_entries_immutable"
	walletChilizSpicyIndexName          = "idx_wallets_chiliz_spicy_address"
)

var postgresStringLiteralPattern = compilePostgresStringLiteralPattern()

func compilePostgresStringLiteralPattern() *regexp.Regexp {
	pattern, err := regexp.Compile(`'((?:''|[^'])*)'`)
	if err != nil {
		log.Printf("postgres constraint string-literal validator error=%v", err)
		return nil
	}
	return pattern
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
	if err := ReconcileWalletChilizSpicyAddressIndex(ctx, db); err != nil {
		return err
	}
	if err := ReconcileLedgerEntryCheckConstraints(ctx, db); err != nil {
		return err
	}
	if err := ReconcileLedgerEntryImmutabilityGuard(ctx, db); err != nil {
		return err
	}
	return VerifySchema(ctx, db)
}

func WithLedgerEntryMutationAllowed(ctx context.Context, db *gorm.DB, fn func(*gorm.DB) error) error {
	if db == nil {
		return gorm.ErrInvalidDB
	}
	if fn == nil {
		return gorm.ErrInvalidData
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tx = tx.Set(models.LedgerEntryMutationContextKey, true)
		if tx.Dialector != nil && tx.Dialector.Name() == "postgres" {
			if err := tx.Exec(`SET LOCAL app.allow_ledger_entry_mutation = 'true'`).Error; err != nil {
				return err
			}
		}
		return fn(tx)
	})
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
		&models.LedgerBalanceProjection{},
		&models.Wallet{},
		&models.Product{},
		&models.PaymentSession{},
		&models.PaymentDepositAllocation{},
		&models.IdempotencyKey{},
		&models.APIRateLimitCounter{},
		&models.APISignedRequestReplay{},
		&models.MoneyEventOutbox{},
		&models.MoneyEventInbox{},
		&models.WorkerLease{},
		&models.WebhookDelivery{},
		&models.WebhookResourceSequence{},
		&models.SweepJob{},
		&models.OutboundTransaction{},
		&models.OutboundChainResourceReservation{},
		&models.WithdrawalRequest{},
		&models.Refund{},
		&models.PriceQuote{},
		&models.ReconciliationJob{},
		&models.ActivityLog{},
		&models.OutboundPolicySetting{},
		&models.OutboundAddressWhitelist{},
		&models.NetworkOperationalState{},
		&models.ProviderHealthSnapshot{},
		&models.WalletAddressLookup{},
		&models.WalletAddressReservation{},
		&models.WalletAddress{},
		&models.WalletAddressGapScanCursor{},
		&models.WalletAddressGapScanAnomaly{},
		&models.Admin{},
	}
}

func requiredSchemaColumns() []requiredSchemaColumn {
	return []requiredSchemaColumn{
		{table: "chain_states", model: &models.ChainState{}, field: "ChainID"},
		{table: "chain_states", model: &models.ChainState{}, field: "LastProcessedBlock"},
		{table: "chain_states", model: &models.ChainState{}, field: "LastProcessedHash"},
		{table: "chain_states", model: &models.ChainState{}, field: "LastProcessedParentHash"},
		{table: "chain_states", model: &models.ChainState{}, field: "LastConfirmedBlock"},
		{table: "chain_states", model: &models.ChainState{}, field: "ScannerStartBlock"},
		{table: "chain_states", model: &models.ChainState{}, field: "ScannerStartPolicy"},
		{table: "chain_states", model: &models.ChainState{}, field: "ContinuityStatus"},
		{table: "chain_states", model: &models.ChainState{}, field: "ContinuityReason"},
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
		{table: "chain_facts", model: &models.ChainFact{}, field: "ObservationStatus"},
		{table: "chain_facts", model: &models.ChainFact{}, field: "Memo"},
		{table: "chain_facts", model: &models.ChainFact{}, field: "MemoNormalized"},
		{table: "chain_facts", model: &models.ChainFact{}, field: "AmountRaw"},
		{table: "chain_facts", model: &models.ChainFact{}, field: "SourceEventType"},
		{table: "chain_facts", model: &models.ChainFact{}, field: "RawMetadataJSON"},
		{table: "chain_facts", model: &models.ChainFact{}, field: "ConfirmationsRequired"},
		{table: "chain_facts", model: &models.ChainFact{}, field: "Status"},
		{table: "chain_facts", model: &models.ChainFact{}, field: "ReorgedAt"},
		{table: "chain_facts", model: &models.ChainFact{}, field: "SupersededByEventID"},
		{table: "chain_facts", model: &models.ChainFact{}, field: "CorrectionReason"},
		{table: "webhook_deliveries", model: &models.WebhookDelivery{}, field: "ResourceType"},
		{table: "webhook_deliveries", model: &models.WebhookDelivery{}, field: "ResourceID"},
		{table: "webhook_deliveries", model: &models.WebhookDelivery{}, field: "Sequence"},
		{table: "webhook_deliveries", model: &models.WebhookDelivery{}, field: "IdempotencyKey"},
		{table: "webhook_resource_sequences", model: &models.WebhookResourceSequence{}, field: "ID"},
		{table: "webhook_resource_sequences", model: &models.WebhookResourceSequence{}, field: "MerchantID"},
		{table: "webhook_resource_sequences", model: &models.WebhookResourceSequence{}, field: "DomainID"},
		{table: "webhook_resource_sequences", model: &models.WebhookResourceSequence{}, field: "ResourceType"},
		{table: "webhook_resource_sequences", model: &models.WebhookResourceSequence{}, field: "ResourceID"},
		{table: "webhook_resource_sequences", model: &models.WebhookResourceSequence{}, field: "LastSequence"},
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
		{table: "deposits", model: &models.Deposit{}, field: "ObservationStatus"},
		{table: "deposits", model: &models.Deposit{}, field: "Memo"},
		{table: "deposits", model: &models.Deposit{}, field: "MemoNormalized"},
		{table: "deposits", model: &models.Deposit{}, field: "MemoStatus"},
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
		{table: "domains", model: &models.Domain{}, field: "APIScopes"},
		{table: "domains", model: &models.Domain{}, field: "APIIPAllowlist"},
		{table: "domains", model: &models.Domain{}, field: "APISecretLastRotatedAt"},
		{table: "domains", model: &models.Domain{}, field: "APISecretRevokedAt"},
		{table: "domains", model: &models.Domain{}, field: "APISecretRotationPolicy"},
		{table: "domains", model: &models.Domain{}, field: "NotificationMode"},
		{table: "domains", model: &models.Domain{}, field: "NATSURL"},
		{table: "domains", model: &models.Domain{}, field: "NATSSubject"},
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
		{table: "ledger_balance_projections", model: &models.LedgerBalanceProjection{}, field: "ID"},
		{table: "ledger_balance_projections", model: &models.LedgerBalanceProjection{}, field: "ScopeType"},
		{table: "ledger_balance_projections", model: &models.LedgerBalanceProjection{}, field: "ScopeKey"},
		{table: "ledger_balance_projections", model: &models.LedgerBalanceProjection{}, field: "MerchantID"},
		{table: "ledger_balance_projections", model: &models.LedgerBalanceProjection{}, field: "DomainID"},
		{table: "ledger_balance_projections", model: &models.LedgerBalanceProjection{}, field: "WalletID"},
		{table: "ledger_balance_projections", model: &models.LedgerBalanceProjection{}, field: "ChainID"},
		{table: "ledger_balance_projections", model: &models.LedgerBalanceProjection{}, field: "Token"},
		{table: "ledger_balance_projections", model: &models.LedgerBalanceProjection{}, field: "TokenFingerprint"},
		{table: "ledger_balance_projections", model: &models.LedgerBalanceProjection{}, field: "Symbol"},
		{table: "ledger_balance_projections", model: &models.LedgerBalanceProjection{}, field: "Decimals"},
		{table: "ledger_balance_projections", model: &models.LedgerBalanceProjection{}, field: "Account"},
		{table: "ledger_balance_projections", model: &models.LedgerBalanceProjection{}, field: "BalanceRaw"},
		{table: "ledger_balance_projections", model: &models.LedgerBalanceProjection{}, field: "SourceLedgerEntryCount"},
		{table: "ledger_balance_projections", model: &models.LedgerBalanceProjection{}, field: "ProjectedAt"},
		{table: "wallets", model: &models.Wallet{}, field: "ID"},
		{table: "products", model: &models.Product{}, field: "ID"},
		{table: "products", model: &models.Product{}, field: "LinkType"},
		{table: "products", model: &models.Product{}, field: "DefaultChainID"},
		{table: "products", model: &models.Product{}, field: "DefaultSymbol"},
		{table: "products", model: &models.Product{}, field: "DefaultToken"},
		{table: "payment_sessions", model: &models.PaymentSession{}, field: "ID"},
		{table: "payment_sessions", model: &models.PaymentSession{}, field: "LinkType"},
		{table: "payment_sessions", model: &models.PaymentSession{}, field: "PaymentOutcome"},
		{table: "payment_sessions", model: &models.PaymentSession{}, field: "PaymentOutcomeReason"},
		{table: "payment_sessions", model: &models.PaymentSession{}, field: "MatchedAmountRaw"},
		{table: "payment_sessions", model: &models.PaymentSession{}, field: "ShortfallAmountRaw"},
		{table: "payment_sessions", model: &models.PaymentSession{}, field: "ExcessAmountRaw"},
		{table: "payment_sessions", model: &models.PaymentSession{}, field: "RequiredMemo"},
		{table: "payment_sessions", model: &models.PaymentSession{}, field: "RequiredMemoNormalized"},
		{table: "payment_sessions", model: &models.PaymentSession{}, field: "SettlementPolicy"},
		{table: "payment_sessions", model: &models.PaymentSession{}, field: "ProductSnapshot"},
		{table: "payment_deposit_allocations", model: &models.PaymentDepositAllocation{}, field: "ID"},
		{table: "payment_deposit_allocations", model: &models.PaymentDepositAllocation{}, field: "PaymentSessionID"},
		{table: "payment_deposit_allocations", model: &models.PaymentDepositAllocation{}, field: "DepositID"},
		{table: "payment_deposit_allocations", model: &models.PaymentDepositAllocation{}, field: "TransactionUniqueHash"},
		{table: "payment_deposit_allocations", model: &models.PaymentDepositAllocation{}, field: "ChainFactEventID"},
		{table: "payment_deposit_allocations", model: &models.PaymentDepositAllocation{}, field: "TxHash"},
		{table: "payment_deposit_allocations", model: &models.PaymentDepositAllocation{}, field: "ChainID"},
		{table: "payment_deposit_allocations", model: &models.PaymentDepositAllocation{}, field: "ObservedAddress"},
		{table: "payment_deposit_allocations", model: &models.PaymentDepositAllocation{}, field: "ObservedAddressNormalized"},
		{table: "payment_deposit_allocations", model: &models.PaymentDepositAllocation{}, field: "Token"},
		{table: "payment_deposit_allocations", model: &models.PaymentDepositAllocation{}, field: "Symbol"},
		{table: "payment_deposit_allocations", model: &models.PaymentDepositAllocation{}, field: "Decimals"},
		{table: "payment_deposit_allocations", model: &models.PaymentDepositAllocation{}, field: "AmountRaw"},
		{table: "payment_deposit_allocations", model: &models.PaymentDepositAllocation{}, field: "Memo"},
		{table: "payment_deposit_allocations", model: &models.PaymentDepositAllocation{}, field: "MemoNormalized"},
		{table: "payment_deposit_allocations", model: &models.PaymentDepositAllocation{}, field: "MemoStatus"},
		{table: "payment_deposit_allocations", model: &models.PaymentDepositAllocation{}, field: "Status"},
		{table: "payment_deposit_allocations", model: &models.PaymentDepositAllocation{}, field: "Outcome"},
		{table: "payment_deposit_allocations", model: &models.PaymentDepositAllocation{}, field: "Reason"},
		{table: "payment_deposit_allocations", model: &models.PaymentDepositAllocation{}, field: "ReorgedAt"},
		{table: "idempotency_keys", model: &models.IdempotencyKey{}, field: "Key"},
		{table: "idempotency_keys", model: &models.IdempotencyKey{}, field: "ResourceType"},
		{table: "idempotency_keys", model: &models.IdempotencyKey{}, field: "ResourceID"},
		{table: "api_rate_limit_counters", model: &models.APIRateLimitCounter{}, field: "ID"},
		{table: "api_rate_limit_counters", model: &models.APIRateLimitCounter{}, field: "KeyHash"},
		{table: "api_rate_limit_counters", model: &models.APIRateLimitCounter{}, field: "Count"},
		{table: "api_rate_limit_counters", model: &models.APIRateLimitCounter{}, field: "ResetAt"},
		{table: "api_signed_request_replays", model: &models.APISignedRequestReplay{}, field: "ID"},
		{table: "api_signed_request_replays", model: &models.APISignedRequestReplay{}, field: "ReplayKey"},
		{table: "api_signed_request_replays", model: &models.APISignedRequestReplay{}, field: "DomainID"},
		{table: "api_signed_request_replays", model: &models.APISignedRequestReplay{}, field: "ExpiresAt"},
		{table: "money_event_outboxes", model: &models.MoneyEventOutbox{}, field: "EventID"},
		{table: "money_event_outboxes", model: &models.MoneyEventOutbox{}, field: "IdempotencyKey"},
		{table: "money_event_outboxes", model: &models.MoneyEventOutbox{}, field: "PayloadJSON"},
		{table: "money_event_outboxes", model: &models.MoneyEventOutbox{}, field: "Status"},
		{table: "money_event_inboxes", model: &models.MoneyEventInbox{}, field: "ID"},
		{table: "money_event_inboxes", model: &models.MoneyEventInbox{}, field: "EventID"},
		{table: "money_event_inboxes", model: &models.MoneyEventInbox{}, field: "ConsumerName"},
		{table: "money_event_inboxes", model: &models.MoneyEventInbox{}, field: "IdempotencyScope"},
		{table: "money_event_inboxes", model: &models.MoneyEventInbox{}, field: "EventType"},
		{table: "money_event_inboxes", model: &models.MoneyEventInbox{}, field: "ResourceType"},
		{table: "money_event_inboxes", model: &models.MoneyEventInbox{}, field: "ResourceID"},
		{table: "money_event_inboxes", model: &models.MoneyEventInbox{}, field: "Status"},
		{table: "money_event_inboxes", model: &models.MoneyEventInbox{}, field: "Attempts"},
		{table: "money_event_inboxes", model: &models.MoneyEventInbox{}, field: "MaxAttempts"},
		{table: "money_event_inboxes", model: &models.MoneyEventInbox{}, field: "LockedUntil"},
		{table: "money_event_inboxes", model: &models.MoneyEventInbox{}, field: "ProcessedAt"},
		{table: "money_event_inboxes", model: &models.MoneyEventInbox{}, field: "LastError"},
		{table: "money_event_inboxes", model: &models.MoneyEventInbox{}, field: "FailureCategory"},
		{table: "money_event_inboxes", model: &models.MoneyEventInbox{}, field: "EvidenceJSON"},
		{table: "worker_leases", model: &models.WorkerLease{}, field: "ID"},
		{table: "worker_leases", model: &models.WorkerLease{}, field: "LeaseKey"},
		{table: "worker_leases", model: &models.WorkerLease{}, field: "OwnerID"},
		{table: "worker_leases", model: &models.WorkerLease{}, field: "Purpose"},
		{table: "worker_leases", model: &models.WorkerLease{}, field: "Status"},
		{table: "worker_leases", model: &models.WorkerLease{}, field: "Attempts"},
		{table: "worker_leases", model: &models.WorkerLease{}, field: "LeaseUntil"},
		{table: "worker_leases", model: &models.WorkerLease{}, field: "AcquiredAt"},
		{table: "worker_leases", model: &models.WorkerLease{}, field: "LastHeartbeat"},
		{table: "worker_leases", model: &models.WorkerLease{}, field: "ReleasedAt"},
		{table: "worker_leases", model: &models.WorkerLease{}, field: "LastError"},
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
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "BatchID"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "BatchKey"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "BatchOrdinal"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "BatchSize"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "BatchPolicy"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "PrefundAttempts"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "PrefundMaxAttempts"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "PrefundLastError"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "PrefundFailureCategory"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "PrefundLastAttemptAt"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "PrefundedAt"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "OperatorAction"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "OperatorNote"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "RecoveryAction"},
		{table: "sweep_jobs", model: &models.SweepJob{}, field: "RecoveredAt"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "ID"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "IdempotencyKey"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "ResourceType"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "ResourceID"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "MerchantID"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "DomainID"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "WalletID"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "ChainID"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "ChainName"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "Token"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "Symbol"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "Decimals"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "AmountRaw"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "ToAddress"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "Status"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "TxHash"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "Attempts"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "MaxAttempts"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "LockedUntil"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "NextRunAt"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "SignedAt"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "BroadcastAttemptedAt"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "BroadcastedAt"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "FinalizedAt"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "FeePolicyJSON"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "ReplacementParentID"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "ReplacementReason"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "ReplacesTxHash"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "ErrorCategory"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "ErrorDetail"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "ActorID"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, field: "CorrelationID"},
		{table: "outbound_chain_resource_reservations", model: &models.OutboundChainResourceReservation{}, field: "ID"},
		{table: "outbound_chain_resource_reservations", model: &models.OutboundChainResourceReservation{}, field: "OutboundTransactionID"},
		{table: "outbound_chain_resource_reservations", model: &models.OutboundChainResourceReservation{}, field: "ResourceType"},
		{table: "outbound_chain_resource_reservations", model: &models.OutboundChainResourceReservation{}, field: "ResourceKey"},
		{table: "outbound_chain_resource_reservations", model: &models.OutboundChainResourceReservation{}, field: "Status"},
		{table: "outbound_chain_resource_reservations", model: &models.OutboundChainResourceReservation{}, field: "ChainID"},
		{table: "outbound_chain_resource_reservations", model: &models.OutboundChainResourceReservation{}, field: "ChainName"},
		{table: "outbound_chain_resource_reservations", model: &models.OutboundChainResourceReservation{}, field: "WalletID"},
		{table: "outbound_chain_resource_reservations", model: &models.OutboundChainResourceReservation{}, field: "WalletAddress"},
		{table: "outbound_chain_resource_reservations", model: &models.OutboundChainResourceReservation{}, field: "OwnerType"},
		{table: "outbound_chain_resource_reservations", model: &models.OutboundChainResourceReservation{}, field: "OwnerID"},
		{table: "outbound_chain_resource_reservations", model: &models.OutboundChainResourceReservation{}, field: "Intent"},
		{table: "outbound_chain_resource_reservations", model: &models.OutboundChainResourceReservation{}, field: "Nonce"},
		{table: "outbound_chain_resource_reservations", model: &models.OutboundChainResourceReservation{}, field: "UTXOTxID"},
		{table: "outbound_chain_resource_reservations", model: &models.OutboundChainResourceReservation{}, field: "UTXOVout"},
		{table: "outbound_chain_resource_reservations", model: &models.OutboundChainResourceReservation{}, field: "UTXOValueRaw"},
		{table: "outbound_chain_resource_reservations", model: &models.OutboundChainResourceReservation{}, field: "LeaseExpiresAt"},
		{table: "outbound_chain_resource_reservations", model: &models.OutboundChainResourceReservation{}, field: "ConsumedAt"},
		{table: "outbound_chain_resource_reservations", model: &models.OutboundChainResourceReservation{}, field: "ReleasedAt"},
		{table: "outbound_chain_resource_reservations", model: &models.OutboundChainResourceReservation{}, field: "TxHash"},
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
		{table: "activity_logs", model: &models.ActivityLog{}, field: "DomainID"},
		{table: "activity_logs", model: &models.ActivityLog{}, field: "ActorRole"},
		{table: "activity_logs", model: &models.ActivityLog{}, field: "Decision"},
		{table: "activity_logs", model: &models.ActivityLog{}, field: "Reason"},
		{table: "activity_logs", model: &models.ActivityLog{}, field: "BeforeStatus"},
		{table: "activity_logs", model: &models.ActivityLog{}, field: "AfterStatus"},
		{table: "activity_logs", model: &models.ActivityLog{}, field: "CorrelationID"},
		{table: "outbound_policy_settings", model: &models.OutboundPolicySetting{}, field: "ID"},
		{table: "outbound_policy_settings", model: &models.OutboundPolicySetting{}, field: "MerchantID"},
		{table: "outbound_policy_settings", model: &models.OutboundPolicySetting{}, field: "DomainID"},
		{table: "outbound_policy_settings", model: &models.OutboundPolicySetting{}, field: "Chain"},
		{table: "outbound_policy_settings", model: &models.OutboundPolicySetting{}, field: "Token"},
		{table: "outbound_policy_settings", model: &models.OutboundPolicySetting{}, field: "WhitelistRequired"},
		{table: "outbound_policy_settings", model: &models.OutboundPolicySetting{}, field: "EmergencyFrozen"},
		{table: "outbound_policy_settings", model: &models.OutboundPolicySetting{}, field: "MaxAmountRaw"},
		{table: "outbound_policy_settings", model: &models.OutboundPolicySetting{}, field: "VelocityLimitRaw"},
		{table: "outbound_policy_settings", model: &models.OutboundPolicySetting{}, field: "VelocityWindowSecs"},
		{table: "outbound_address_whitelists", model: &models.OutboundAddressWhitelist{}, field: "ID"},
		{table: "outbound_address_whitelists", model: &models.OutboundAddressWhitelist{}, field: "MerchantID"},
		{table: "outbound_address_whitelists", model: &models.OutboundAddressWhitelist{}, field: "DomainID"},
		{table: "outbound_address_whitelists", model: &models.OutboundAddressWhitelist{}, field: "Chain"},
		{table: "outbound_address_whitelists", model: &models.OutboundAddressWhitelist{}, field: "Token"},
		{table: "outbound_address_whitelists", model: &models.OutboundAddressWhitelist{}, field: "Address"},
		{table: "outbound_address_whitelists", model: &models.OutboundAddressWhitelist{}, field: "IsActive"},
		{table: "provider_health_snapshots", model: &models.ProviderHealthSnapshot{}, field: "ID"},
		{table: "provider_health_snapshots", model: &models.ProviderHealthSnapshot{}, field: "ChainID"},
		{table: "provider_health_snapshots", model: &models.ProviderHealthSnapshot{}, field: "ChainName"},
		{table: "provider_health_snapshots", model: &models.ProviderHealthSnapshot{}, field: "ProviderLabel"},
		{table: "provider_health_snapshots", model: &models.ProviderHealthSnapshot{}, field: "ProviderURLHash"},
		{table: "provider_health_snapshots", model: &models.ProviderHealthSnapshot{}, field: "Reachable"},
		{table: "provider_health_snapshots", model: &models.ProviderHealthSnapshot{}, field: "Status"},
		{table: "provider_health_snapshots", model: &models.ProviderHealthSnapshot{}, field: "LatestHeight"},
		{table: "provider_health_snapshots", model: &models.ProviderHealthSnapshot{}, field: "HeadHash"},
		{table: "provider_health_snapshots", model: &models.ProviderHealthSnapshot{}, field: "ResponseLatencyMS"},
		{table: "provider_health_snapshots", model: &models.ProviderHealthSnapshot{}, field: "LagFromReference"},
		{table: "provider_health_snapshots", model: &models.ProviderHealthSnapshot{}, field: "ErrorCategory"},
		{table: "provider_health_snapshots", model: &models.ProviderHealthSnapshot{}, field: "ErrorDetail"},
		{table: "provider_health_snapshots", model: &models.ProviderHealthSnapshot{}, field: "Selected"},
		{table: "provider_health_snapshots", model: &models.ProviderHealthSnapshot{}, field: "FailoverReason"},
		{table: "provider_health_snapshots", model: &models.ProviderHealthSnapshot{}, field: "ConsecutiveFailures"},
		{table: "provider_health_snapshots", model: &models.ProviderHealthSnapshot{}, field: "CheckedAt"},
		{table: "wallet_address_lookups", model: &models.WalletAddressLookup{}, field: "ID"},
		{table: "wallet_address_lookups", model: &models.WalletAddressLookup{}, field: "ChainID"},
		{table: "wallet_address_lookups", model: &models.WalletAddressLookup{}, field: "ChainName"},
		{table: "wallet_address_lookups", model: &models.WalletAddressLookup{}, field: "Address"},
		{table: "wallet_address_lookups", model: &models.WalletAddressLookup{}, field: "NormalizedAddress"},
		{table: "wallet_address_lookups", model: &models.WalletAddressLookup{}, field: "Asset"},
		{table: "wallet_address_lookups", model: &models.WalletAddressLookup{}, field: "MerchantID"},
		{table: "wallet_address_lookups", model: &models.WalletAddressLookup{}, field: "DomainID"},
		{table: "wallet_address_lookups", model: &models.WalletAddressLookup{}, field: "WalletID"},
		{table: "wallet_address_lookups", model: &models.WalletAddressLookup{}, field: "ProductID"},
		{table: "wallet_address_lookups", model: &models.WalletAddressLookup{}, field: "UserID"},
		{table: "wallet_address_lookups", model: &models.WalletAddressLookup{}, field: "Source"},
		{table: "wallet_address_reservations", model: &models.WalletAddressReservation{}, field: "ID"},
		{table: "wallet_address_reservations", model: &models.WalletAddressReservation{}, field: "MerchantID"},
		{table: "wallet_address_reservations", model: &models.WalletAddressReservation{}, field: "DomainID"},
		{table: "wallet_address_reservations", model: &models.WalletAddressReservation{}, field: "ProductID"},
		{table: "wallet_address_reservations", model: &models.WalletAddressReservation{}, field: "UserID"},
		{table: "wallet_address_reservations", model: &models.WalletAddressReservation{}, field: "HDAccountID"},
		{table: "wallet_address_reservations", model: &models.WalletAddressReservation{}, field: "HDAddressID"},
		{table: "wallet_address_reservations", model: &models.WalletAddressReservation{}, field: "Purpose"},
		{table: "wallet_address_reservations", model: &models.WalletAddressReservation{}, field: "LifecycleStatus"},
		{table: "wallet_address_reservations", model: &models.WalletAddressReservation{}, field: "ReusePolicy"},
		{table: "wallet_address_reservations", model: &models.WalletAddressReservation{}, field: "WalletID"},
		{table: "wallet_address_reservations", model: &models.WalletAddressReservation{}, field: "ReservedAt"},
		{table: "wallet_address_reservations", model: &models.WalletAddressReservation{}, field: "AssignedAt"},
		{table: "wallet_address_reservations", model: &models.WalletAddressReservation{}, field: "ExpiresAt"},
		{table: "wallet_address_reservations", model: &models.WalletAddressReservation{}, field: "ReleasedAt"},
		{table: "wallet_address_reservations", model: &models.WalletAddressReservation{}, field: "RetiredAt"},
		{table: "wallet_address_reservations", model: &models.WalletAddressReservation{}, field: "CreatedAt"},
		{table: "wallet_address_reservations", model: &models.WalletAddressReservation{}, field: "UpdatedAt"},
		{table: "wallet_addresses", model: &models.WalletAddress{}, field: "ID"},
		{table: "wallet_addresses", model: &models.WalletAddress{}, field: "ChainID"},
		{table: "wallet_addresses", model: &models.WalletAddress{}, field: "ChainName"},
		{table: "wallet_addresses", model: &models.WalletAddress{}, field: "Address"},
		{table: "wallet_addresses", model: &models.WalletAddress{}, field: "NormalizedAddress"},
		{table: "wallet_addresses", model: &models.WalletAddress{}, field: "Asset"},
		{table: "wallet_addresses", model: &models.WalletAddress{}, field: "MerchantID"},
		{table: "wallet_addresses", model: &models.WalletAddress{}, field: "DomainID"},
		{table: "wallet_addresses", model: &models.WalletAddress{}, field: "WalletID"},
		{table: "wallet_addresses", model: &models.WalletAddress{}, field: "ProductID"},
		{table: "wallet_addresses", model: &models.WalletAddress{}, field: "UserID"},
		{table: "wallet_addresses", model: &models.WalletAddress{}, field: "HDAccountID"},
		{table: "wallet_addresses", model: &models.WalletAddress{}, field: "HDAddressID"},
		{table: "wallet_addresses", model: &models.WalletAddress{}, field: "Purpose"},
		{table: "wallet_addresses", model: &models.WalletAddress{}, field: "LifecycleStatus"},
		{table: "wallet_addresses", model: &models.WalletAddress{}, field: "ReusePolicy"},
		{table: "wallet_addresses", model: &models.WalletAddress{}, field: "Source"},
		{table: "wallet_addresses", model: &models.WalletAddress{}, field: "ReservationID"},
		{table: "wallet_addresses", model: &models.WalletAddress{}, field: "ReservedAt"},
		{table: "wallet_addresses", model: &models.WalletAddress{}, field: "AssignedAt"},
		{table: "wallet_addresses", model: &models.WalletAddress{}, field: "ActivatedAt"},
		{table: "wallet_addresses", model: &models.WalletAddress{}, field: "UsedAt"},
		{table: "wallet_addresses", model: &models.WalletAddress{}, field: "ExpiresAt"},
		{table: "wallet_addresses", model: &models.WalletAddress{}, field: "ReleasedAt"},
		{table: "wallet_addresses", model: &models.WalletAddress{}, field: "RetiredAt"},
		{table: "wallet_addresses", model: &models.WalletAddress{}, field: "CreatedAt"},
		{table: "wallet_addresses", model: &models.WalletAddress{}, field: "UpdatedAt"},
		{table: "wallet_address_gap_scan_cursors", model: &models.WalletAddressGapScanCursor{}, field: "ID"},
		{table: "wallet_address_gap_scan_cursors", model: &models.WalletAddressGapScanCursor{}, field: "ChainID"},
		{table: "wallet_address_gap_scan_cursors", model: &models.WalletAddressGapScanCursor{}, field: "ChainName"},
		{table: "wallet_address_gap_scan_cursors", model: &models.WalletAddressGapScanCursor{}, field: "HDAccountID"},
		{table: "wallet_address_gap_scan_cursors", model: &models.WalletAddressGapScanCursor{}, field: "Purpose"},
		{table: "wallet_address_gap_scan_cursors", model: &models.WalletAddressGapScanCursor{}, field: "Lookahead"},
		{table: "wallet_address_gap_scan_cursors", model: &models.WalletAddressGapScanCursor{}, field: "LastScannedIndex"},
		{table: "wallet_address_gap_scan_cursors", model: &models.WalletAddressGapScanCursor{}, field: "HighestUsedIndex"},
		{table: "wallet_address_gap_scan_cursors", model: &models.WalletAddressGapScanCursor{}, field: "DiscoveredUsedIndexesJSON"},
		{table: "wallet_address_gap_scan_cursors", model: &models.WalletAddressGapScanCursor{}, field: "AnomalyCount"},
		{table: "wallet_address_gap_scan_cursors", model: &models.WalletAddressGapScanCursor{}, field: "LastAnomaly"},
		{table: "wallet_address_gap_scan_cursors", model: &models.WalletAddressGapScanCursor{}, field: "ScannedAt"},
		{table: "wallet_address_gap_scan_cursors", model: &models.WalletAddressGapScanCursor{}, field: "CreatedAt"},
		{table: "wallet_address_gap_scan_cursors", model: &models.WalletAddressGapScanCursor{}, field: "UpdatedAt"},
		{table: "wallet_address_gap_scan_anomalies", model: &models.WalletAddressGapScanAnomaly{}, field: "ID"},
		{table: "wallet_address_gap_scan_anomalies", model: &models.WalletAddressGapScanAnomaly{}, field: "ChainID"},
		{table: "wallet_address_gap_scan_anomalies", model: &models.WalletAddressGapScanAnomaly{}, field: "ChainName"},
		{table: "wallet_address_gap_scan_anomalies", model: &models.WalletAddressGapScanAnomaly{}, field: "HDAccountID"},
		{table: "wallet_address_gap_scan_anomalies", model: &models.WalletAddressGapScanAnomaly{}, field: "HDAddressID"},
		{table: "wallet_address_gap_scan_anomalies", model: &models.WalletAddressGapScanAnomaly{}, field: "Purpose"},
		{table: "wallet_address_gap_scan_anomalies", model: &models.WalletAddressGapScanAnomaly{}, field: "Address"},
		{table: "wallet_address_gap_scan_anomalies", model: &models.WalletAddressGapScanAnomaly{}, field: "Category"},
		{table: "wallet_address_gap_scan_anomalies", model: &models.WalletAddressGapScanAnomaly{}, field: "Detail"},
		{table: "wallet_address_gap_scan_anomalies", model: &models.WalletAddressGapScanAnomaly{}, field: "DetectedAt"},
		{table: "wallet_address_gap_scan_anomalies", model: &models.WalletAddressGapScanAnomaly{}, field: "CreatedAt"},
		{table: "wallet_address_gap_scan_anomalies", model: &models.WalletAddressGapScanAnomaly{}, field: "UpdatedAt"},
		{table: "network_operational_states", model: &models.NetworkOperationalState{}, field: "ID"},
		{table: "network_operational_states", model: &models.NetworkOperationalState{}, field: "ChainID"},
		{table: "network_operational_states", model: &models.NetworkOperationalState{}, field: "Mode"},
		{table: "network_operational_states", model: &models.NetworkOperationalState{}, field: "Reason"},
		{table: "network_operational_states", model: &models.NetworkOperationalState{}, field: "UpdatedBy"},
		{table: "network_operational_states", model: &models.NetworkOperationalState{}, field: "CreatedAt"},
		{table: "network_operational_states", model: &models.NetworkOperationalState{}, field: "UpdatedAt"},
		{table: "admins", model: &models.Admin{}, field: "ID"},
		{table: "admins", model: &models.Admin{}, field: "Role"},
		{table: "transactions", model: &models.Transaction{}, field: "WebhookLockedUntil"},
		{table: "payment_sessions", model: &models.PaymentSession{}, field: "WebhookLockedUntil"},
		{table: "admins", model: &models.Admin{}, field: "TOTPSecret"},
	}
}

func requiredSchemaIndexes() []requiredSchemaIndex {
	return []requiredSchemaIndex{
		{table: "wallets", model: &models.Wallet{}, name: walletChilizSpicyIndexName},
		{table: "money_event_outboxes", model: &models.MoneyEventOutbox{}, name: "ux_money_event_outboxes_event_id"},
		{table: "money_event_outboxes", model: &models.MoneyEventOutbox{}, name: "ux_money_event_outboxes_idempotency_scope"},
		{table: "money_event_inboxes", model: &models.MoneyEventInbox{}, name: "ux_money_event_inbox_consumer_event"},
		{table: "money_event_inboxes", model: &models.MoneyEventInbox{}, name: "idx_money_event_inbox_consumer_status"},
		{table: "money_event_inboxes", model: &models.MoneyEventInbox{}, name: "idx_money_event_inboxes_idempotency_scope"},
		{table: "money_event_inboxes", model: &models.MoneyEventInbox{}, name: "idx_money_event_inboxes_locked_until"},
		{table: "money_event_inboxes", model: &models.MoneyEventInbox{}, name: "idx_money_event_inboxes_failure_category"},
		{table: "worker_leases", model: &models.WorkerLease{}, name: "ux_worker_leases_key"},
		{table: "worker_leases", model: &models.WorkerLease{}, name: "idx_worker_leases_owner_id"},
		{table: "worker_leases", model: &models.WorkerLease{}, name: "idx_worker_leases_status"},
		{table: "worker_leases", model: &models.WorkerLease{}, name: "idx_worker_leases_lease_until"},
		{table: "webhook_resource_sequences", model: &models.WebhookResourceSequence{}, name: "ux_webhook_resource_sequence"},
		{table: "webhook_deliveries", model: &models.WebhookDelivery{}, name: "idx_webhook_delivery_resource_order"},
		{table: "blocks", model: &models.Block{}, name: "ux_blocks_chain_hash"},
		{table: "blocks", model: &models.Block{}, name: "ux_blocks_chain_number_hash"},
		{table: "chain_facts", model: &models.ChainFact{}, name: "ux_chain_facts_event_id"},
		{table: "deposits", model: &models.Deposit{}, name: "ux_deposits_chain_fact_event_id"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, name: "ux_ledger_idempotent_account"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, name: "idx_ledger_entries_sweep_job_id"},
		{table: "ledger_balance_projections", model: &models.LedgerBalanceProjection{}, name: "ux_ledger_balance_projection_scope"},
		{table: "ledger_balance_projections", model: &models.LedgerBalanceProjection{}, name: "idx_ledger_balance_projections_scope_key"},
		{table: "ledger_balance_projections", model: &models.LedgerBalanceProjection{}, name: "idx_ledger_balance_projections_merchant_id"},
		{table: "ledger_balance_projections", model: &models.LedgerBalanceProjection{}, name: "idx_ledger_balance_projections_wallet_id"},
		{table: "api_signed_request_replays", model: &models.APISignedRequestReplay{}, name: "ux_api_signed_request_replays_key"},
		{table: "api_signed_request_replays", model: &models.APISignedRequestReplay{}, name: "idx_api_signed_request_replays_domain_id"},
		{table: "api_signed_request_replays", model: &models.APISignedRequestReplay{}, name: "idx_api_signed_request_replays_expires_at"},
		{table: "payment_deposit_allocations", model: &models.PaymentDepositAllocation{}, name: "ux_payment_deposit_alloc_tx"},
		{table: "payment_deposit_allocations", model: &models.PaymentDepositAllocation{}, name: "ux_payment_deposit_alloc_session_tx"},
		{table: "sweep_jobs", model: &models.SweepJob{}, name: "idx_sweep_jobs_transaction_unique_hash"},
		{table: "sweep_jobs", model: &models.SweepJob{}, name: "idx_sweep_jobs_batch_id"},
		{table: "sweep_jobs", model: &models.SweepJob{}, name: "idx_sweep_jobs_batch_key"},
		{table: "sweep_jobs", model: &models.SweepJob{}, name: "idx_sweep_jobs_failure_category"},
		{table: "sweep_jobs", model: &models.SweepJob{}, name: "idx_sweep_jobs_prefund_failure_category"},
		{table: "sweep_jobs", model: &models.SweepJob{}, name: "idx_sweep_jobs_prefund_last_attempt_at"},
		{table: "sweep_jobs", model: &models.SweepJob{}, name: "idx_sweep_jobs_operator_action"},
		{table: "sweep_jobs", model: &models.SweepJob{}, name: "idx_sweep_jobs_recovery_action"},
		{table: "sweep_jobs", model: &models.SweepJob{}, name: "idx_sweep_jobs_recovered_at"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, name: "ux_outbound_transactions_idempotency_key"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, name: "idx_outbound_transactions_resource"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, name: "idx_outbound_transactions_status"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, name: "idx_outbound_transactions_tx_hash"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, name: "idx_outbound_transactions_locked_until"},
		{table: "outbound_transactions", model: &models.OutboundTransaction{}, name: "idx_outbound_transactions_next_run_at"},
		{table: "outbound_chain_resource_reservations", model: &models.OutboundChainResourceReservation{}, name: "idx_outbound_chain_resource_reservations_resource_key"},
		{table: "outbound_chain_resource_reservations", model: &models.OutboundChainResourceReservation{}, name: "idx_outbound_resource_type_status"},
		{table: "outbound_chain_resource_reservations", model: &models.OutboundChainResourceReservation{}, name: "idx_outbound_resource_owner"},
		{table: "outbound_chain_resource_reservations", model: &models.OutboundChainResourceReservation{}, name: "idx_outbound_chain_resource_reservations_outbound_trans7563aee7"},
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
		{table: "api_rate_limit_counters", model: &models.APIRateLimitCounter{}, name: "idx_api_rate_limit_counters_reset_at"},
		{table: "api_rate_limit_counters", model: &models.APIRateLimitCounter{}, name: "idx_api_rate_limit_counters_key_hash"},
		{table: "reconciliation_jobs", model: &models.ReconciliationJob{}, name: "idx_reconciliation_jobs_merchant_id"},
		{table: "reconciliation_jobs", model: &models.ReconciliationJob{}, name: "idx_reconciliation_jobs_domain_id"},
		{table: "reconciliation_jobs", model: &models.ReconciliationJob{}, name: "idx_reconciliation_jobs_scope_key"},
		{table: "reconciliation_jobs", model: &models.ReconciliationJob{}, name: "idx_reconciliation_jobs_resource_type"},
		{table: "reconciliation_jobs", model: &models.ReconciliationJob{}, name: "idx_reconciliation_jobs_resource_id"},
		{table: "reconciliation_jobs", model: &models.ReconciliationJob{}, name: "idx_reconciliation_jobs_outcome"},
		{table: "reconciliation_jobs", model: &models.ReconciliationJob{}, name: "idx_reconciliation_jobs_next_run_at"},
		{table: "activity_logs", model: &models.ActivityLog{}, name: "idx_activity_logs_domain_id"},
		{table: "activity_logs", model: &models.ActivityLog{}, name: "idx_activity_logs_actor_role"},
		{table: "activity_logs", model: &models.ActivityLog{}, name: "idx_activity_logs_decision"},
		{table: "activity_logs", model: &models.ActivityLog{}, name: "idx_activity_logs_before_status"},
		{table: "activity_logs", model: &models.ActivityLog{}, name: "idx_activity_logs_after_status"},
		{table: "activity_logs", model: &models.ActivityLog{}, name: "idx_activity_logs_correlation_id"},
		{table: "outbound_policy_settings", model: &models.OutboundPolicySetting{}, name: "idx_outbound_policy_settings_merchant_id"},
		{table: "outbound_policy_settings", model: &models.OutboundPolicySetting{}, name: "idx_outbound_policy_settings_domain_id"},
		{table: "outbound_policy_settings", model: &models.OutboundPolicySetting{}, name: "idx_outbound_policy_settings_chain"},
		{table: "outbound_policy_settings", model: &models.OutboundPolicySetting{}, name: "idx_outbound_policy_settings_token"},
		{table: "outbound_address_whitelists", model: &models.OutboundAddressWhitelist{}, name: "idx_outbound_address_whitelists_merchant_id"},
		{table: "outbound_address_whitelists", model: &models.OutboundAddressWhitelist{}, name: "idx_outbound_address_whitelists_domain_id"},
		{table: "outbound_address_whitelists", model: &models.OutboundAddressWhitelist{}, name: "idx_outbound_address_whitelists_chain"},
		{table: "outbound_address_whitelists", model: &models.OutboundAddressWhitelist{}, name: "idx_outbound_address_whitelists_token"},
		{table: "outbound_address_whitelists", model: &models.OutboundAddressWhitelist{}, name: "idx_outbound_address_whitelists_address"},
		{table: "outbound_address_whitelists", model: &models.OutboundAddressWhitelist{}, name: "idx_outbound_address_whitelists_is_active"},
		{table: "provider_health_snapshots", model: &models.ProviderHealthSnapshot{}, name: "ux_provider_health_chain_provider"},
		{table: "provider_health_snapshots", model: &models.ProviderHealthSnapshot{}, name: "idx_provider_health_snapshots_chain_id"},
		{table: "provider_health_snapshots", model: &models.ProviderHealthSnapshot{}, name: "idx_provider_health_snapshots_chain_name"},
		{table: "provider_health_snapshots", model: &models.ProviderHealthSnapshot{}, name: "idx_provider_health_snapshots_provider_url_hash"},
		{table: "provider_health_snapshots", model: &models.ProviderHealthSnapshot{}, name: "idx_provider_health_snapshots_status"},
		{table: "provider_health_snapshots", model: &models.ProviderHealthSnapshot{}, name: "idx_provider_health_snapshots_error_category"},
		{table: "provider_health_snapshots", model: &models.ProviderHealthSnapshot{}, name: "idx_provider_health_snapshots_selected"},
		{table: "provider_health_snapshots", model: &models.ProviderHealthSnapshot{}, name: "idx_provider_health_snapshots_checked_at"},
		{table: "wallet_address_lookups", model: &models.WalletAddressLookup{}, name: "ux_wallet_address_lookup_chain_address"},
		{table: "wallet_address_lookups", model: &models.WalletAddressLookup{}, name: "idx_wallet_address_lookups_chain_id"},
		{table: "wallet_address_lookups", model: &models.WalletAddressLookup{}, name: "idx_wallet_address_lookups_chain_name"},
		{table: "wallet_address_lookups", model: &models.WalletAddressLookup{}, name: "idx_wallet_address_lookups_normalized_address"},
		{table: "wallet_address_lookups", model: &models.WalletAddressLookup{}, name: "idx_wallet_address_lookups_asset"},
		{table: "wallet_address_lookups", model: &models.WalletAddressLookup{}, name: "idx_wallet_address_lookups_merchant_id"},
		{table: "wallet_address_lookups", model: &models.WalletAddressLookup{}, name: "idx_wallet_address_lookups_domain_id"},
		{table: "wallet_address_lookups", model: &models.WalletAddressLookup{}, name: "idx_wallet_address_lookups_wallet_id"},
		{table: "wallet_address_lookups", model: &models.WalletAddressLookup{}, name: "idx_wallet_address_lookups_product_id"},
		{table: "wallet_address_lookups", model: &models.WalletAddressLookup{}, name: "idx_wallet_address_lookups_user_id"},
		{table: "wallet_address_lookups", model: &models.WalletAddressLookup{}, name: "idx_wallet_address_lookups_source"},
		{table: "wallet_address_reservations", model: &models.WalletAddressReservation{}, name: "ux_wallet_address_reservations_owner"},
		{table: "wallet_address_reservations", model: &models.WalletAddressReservation{}, name: "ux_wallet_address_reservations_hd"},
		{table: "wallet_addresses", model: &models.WalletAddress{}, name: "ux_wallet_addresses_chain_address"},
		{table: "wallet_addresses", model: &models.WalletAddress{}, name: "ux_wallet_addresses_hd_chain"},
		{table: "wallet_address_gap_scan_cursors", model: &models.WalletAddressGapScanCursor{}, name: "ux_wallet_address_gap_scan_scope"},
		{table: "network_operational_states", model: &models.NetworkOperationalState{}, name: "ux_network_operational_states_chain_id"},
		{table: "network_operational_states", model: &models.NetworkOperationalState{}, name: "idx_network_operational_states_mode"},
		{table: "admins", model: &models.Admin{}, name: "idx_admins_role"},
	}
}

func requiredSchemaConstraints() []requiredSchemaConstraint {
	return []requiredSchemaConstraint{
		{table: "ledger_entries", model: &models.LedgerEntry{}, name: "ledger_entries_entry_type_check"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, name: "ledger_entries_account_check"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, name: "ledger_entries_direction_check"},
		{table: "ledger_entries", model: &models.LedgerEntry{}, name: "ledger_entries_status_check"},
		{table: "network_operational_states", model: &models.NetworkOperationalState{}, name: "network_operational_states_mode_check"},
	}
}

func requiredSchemaTriggers() []requiredSchemaTrigger {
	return []requiredSchemaTrigger{
		{table: "ledger_entries", name: ledgerEntryImmutabilityTriggerName},
	}
}

func ledgerEntryCheckConstraintSpecs() []checkConstraintSpec {
	return []checkConstraintSpec{
		{
			table:  "ledger_entries",
			name:   "ledger_entries_entry_type_check",
			column: "entry_type",
			values: []string{
				models.LedgerEntryTypeDepositPending,
				models.LedgerEntryTypeDepositAvailable,
				models.LedgerEntryTypeWithdrawalHold,
				models.LedgerEntryTypeWithdrawalRelease,
				models.LedgerEntryTypeWithdrawalDebit,
				models.LedgerEntryTypeRefundHold,
				models.LedgerEntryTypeRefundRelease,
				models.LedgerEntryTypeRefundDebit,
				models.LedgerEntryTypeSweepHold,
				models.LedgerEntryTypeSweepRelease,
				models.LedgerEntryTypeSweepDebit,
				models.LedgerEntryTypeReorgReversal,
				models.LedgerEntryTypeAdjustment,
			},
		},
		{
			table:  "ledger_entries",
			name:   "ledger_entries_account_check",
			column: "account",
			values: []string{
				models.LedgerAccountMerchantPending,
				models.LedgerAccountMerchantAvailable,
				models.LedgerAccountPlatformClearing,
				models.LedgerAccountWithdrawalTransit,
				models.LedgerAccountRefundTransit,
				models.LedgerAccountSweepTransit,
			},
		},
		{
			table:  "ledger_entries",
			name:   "ledger_entries_direction_check",
			column: "direction",
			values: []string{
				models.LedgerDirectionCredit,
				models.LedgerDirectionDebit,
			},
		},
		{
			table:  "ledger_entries",
			name:   "ledger_entries_status_check",
			column: "status",
			values: []string{
				models.LedgerStatusPending,
				models.LedgerStatusPosted,
				models.LedgerStatusVoided,
			},
		},
	}
}

func ReconcileLedgerEntryCheckConstraints(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, spec := range ledgerEntryCheckConstraintSpecs() {
			definition, found, err := postgresConstraintDefinition(ctx, tx, spec.table, spec.name)
			if err != nil {
				return err
			}
			if found && checkConstraintValuesMatch(definition, spec.values) {
				continue
			}
			if err := tx.Exec(fmt.Sprintf(
				`ALTER TABLE %s DROP CONSTRAINT IF EXISTS %s`,
				quotePostgresIdentifier(spec.table),
				quotePostgresIdentifier(spec.name),
			)).Error; err != nil {
				return err
			}
			if err := tx.Exec(fmt.Sprintf(
				`ALTER TABLE %s ADD CONSTRAINT %s CHECK (%s)`,
				quotePostgresIdentifier(spec.table),
				quotePostgresIdentifier(spec.name),
				checkConstraintExpression(spec),
			)).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func ReconcileWalletChilizSpicyAddressIndex(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return gorm.ErrInvalidDB
	}
	if db.Dialector == nil || db.Dialector.Name() != "postgres" {
		return nil
	}
	definition, predicate, unique, found, err := postgresIndexDefinition(
		ctx,
		db,
		"wallets",
		walletChilizSpicyIndexName,
	)
	if err != nil {
		return err
	}
	if found && walletChilizSpicyIndexMatches(definition, predicate, unique) {
		return nil
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(fmt.Sprintf(
			`DROP INDEX IF EXISTS %s`,
			quotePostgresIdentifier(walletChilizSpicyIndexName),
		)).Error; err != nil {
			return err
		}
		return tx.Exec(fmt.Sprintf(
			`CREATE UNIQUE INDEX %s ON %s (%s) WHERE %s <> ''`,
			quotePostgresIdentifier(walletChilizSpicyIndexName),
			quotePostgresIdentifier("wallets"),
			quotePostgresIdentifier("chiliz_spicy_address"),
			quotePostgresIdentifier("chiliz_spicy_address"),
		)).Error
	})
}

func ReconcileLedgerEntryImmutabilityGuard(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return gorm.ErrInvalidDB
	}
	if db.Dialector == nil || db.Dialector.Name() != "postgres" {
		return nil
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(fmt.Sprintf(`
			CREATE OR REPLACE FUNCTION %s()
			RETURNS trigger AS $$
			BEGIN
				IF current_setting('app.%s', true) IN ('1', 'on', 'true') THEN
					IF TG_OP = 'DELETE' THEN
						RETURN OLD;
					END IF;
					RETURN NEW;
				END IF;
				RAISE EXCEPTION 'ledger_entries is append-only';
			END;
			$$ LANGUAGE plpgsql
		`, quotePostgresIdentifier(ledgerEntryImmutabilityFunctionName), models.LedgerEntryMutationContextKey)).Error; err != nil {
			return err
		}
		if err := tx.Exec(fmt.Sprintf(
			`DROP TRIGGER IF EXISTS %s ON %s`,
			quotePostgresIdentifier(ledgerEntryImmutabilityTriggerName),
			quotePostgresIdentifier("ledger_entries"),
		)).Error; err != nil {
			return err
		}
		return tx.Exec(fmt.Sprintf(
			`CREATE TRIGGER %s BEFORE UPDATE OR DELETE ON %s FOR EACH ROW EXECUTE FUNCTION %s()`,
			quotePostgresIdentifier(ledgerEntryImmutabilityTriggerName),
			quotePostgresIdentifier("ledger_entries"),
			quotePostgresIdentifier(ledgerEntryImmutabilityFunctionName),
		)).Error
	})
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
	for _, trigger := range requiredSchemaTriggers() {
		found, err := postgresTriggerMatches(ctx, db, trigger.table, trigger.name, ledgerEntryImmutabilityFunctionName)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("schema check failed: %s trigger %s is missing or stale", trigger.table, trigger.name)
		}
	}
	for _, spec := range ledgerEntryCheckConstraintSpecs() {
		definition, found, err := postgresConstraintDefinition(ctx, db, spec.table, spec.name)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("schema check failed: %s constraint %s is missing", spec.table, spec.name)
		}
		if !checkConstraintValuesMatch(definition, spec.values) {
			return fmt.Errorf("schema check failed: %s constraint %s allowed values are stale", spec.table, spec.name)
		}
	}
	if db.Dialector != nil && db.Dialector.Name() == "postgres" {
		definition, predicate, unique, found, err := postgresIndexDefinition(ctx, db, "wallets", walletChilizSpicyIndexName)
		if err != nil {
			return err
		}
		if !found || !walletChilizSpicyIndexMatches(definition, predicate, unique) {
			return fmt.Errorf("schema check failed: wallets index %s is missing or stale", walletChilizSpicyIndexName)
		}
	}
	return nil
}

func postgresIndexDefinition(ctx context.Context, db *gorm.DB, tableName, indexName string) (definition string, predicate string, unique bool, found bool, err error) {
	err = db.WithContext(ctx).Raw(`
		SELECT pg_get_indexdef(i.indexrelid), COALESCE(pg_get_expr(i.indpred, i.indrelid), ''), i.indisunique
		FROM pg_index i
		JOIN pg_class idx ON idx.oid = i.indexrelid
		JOIN pg_class tbl ON tbl.oid = i.indrelid
		JOIN pg_namespace n ON n.oid = tbl.relnamespace
		WHERE n.nspname = current_schema()
		  AND tbl.relname = ?
		  AND idx.relname = ?
	`, tableName, indexName).Row().Scan(&definition, &predicate, &unique)
	if err == sql.ErrNoRows {
		return "", "", false, false, nil
	}
	if err != nil {
		return "", "", false, false, err
	}
	return definition, predicate, unique, true, nil
}

func walletChilizSpicyIndexMatches(definition string, predicate string, unique bool) bool {
	if !unique {
		return false
	}
	normalizedDefinition := normalizePostgresIndexExpression(definition)
	if !strings.Contains(normalizedDefinition, "chiliz_spicy_address") {
		return false
	}
	normalizedPredicate := normalizePostgresIndexExpression(predicate)
	return normalizedPredicate == "chiliz_spicy_address<>''"
}

func normalizePostgresIndexExpression(value string) string {
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, `"`, "")
	value = strings.ReplaceAll(value, "::character varying", "")
	value = strings.ReplaceAll(value, "::text", "")
	value = strings.ReplaceAll(value, "(", "")
	value = strings.ReplaceAll(value, ")", "")
	return strings.Join(strings.Fields(value), "")
}

func postgresConstraintDefinition(ctx context.Context, db *gorm.DB, tableName, constraintName string) (string, bool, error) {
	var definition string
	err := db.WithContext(ctx).Raw(`
		SELECT pg_get_constraintdef(c.oid)
		FROM pg_constraint c
		JOIN pg_class t ON t.oid = c.conrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		WHERE n.nspname = current_schema()
		  AND t.relname = ?
		  AND c.conname = ?
	`, tableName, constraintName).Row().Scan(&definition)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return definition, true, nil
}

func postgresTriggerMatches(ctx context.Context, db *gorm.DB, tableName, triggerName string, functionName string) (bool, error) {
	if db == nil {
		return false, gorm.ErrInvalidDB
	}
	if db.Dialector == nil || db.Dialector.Name() != "postgres" {
		return true, nil
	}
	var row struct {
		Found              bool
		IsRow              bool
		IsBefore           bool
		HasUpdate          bool
		HasDelete          bool
		HasInsert          bool
		HasTruncate        bool
		FunctionName       string
		FunctionDefinition string
	}
	err := db.WithContext(ctx).Raw(`
		SELECT TRUE AS found,
		       (tg.tgtype & 1) = 1 AS is_row,
		       (tg.tgtype & 2) = 2 AS is_before,
		       (tg.tgtype & 16) = 16 AS has_update,
		       (tg.tgtype & 8) = 8 AS has_delete,
		       (tg.tgtype & 4) = 4 AS has_insert,
		       (tg.tgtype & 32) = 32 AS has_truncate,
		       p.proname AS function_name,
		       pg_get_functiondef(p.oid) AS function_definition
		FROM pg_trigger tg
		JOIN pg_class t ON t.oid = tg.tgrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		JOIN pg_proc p ON p.oid = tg.tgfoid
		JOIN pg_namespace pn ON pn.oid = p.pronamespace
		WHERE n.nspname = current_schema()
		  AND pn.nspname = current_schema()
		  AND t.relname = ?
		  AND tg.tgname = ?
		  AND NOT tg.tgisinternal
	`, tableName, triggerName).Row().Scan(
		&row.Found,
		&row.IsRow,
		&row.IsBefore,
		&row.HasUpdate,
		&row.HasDelete,
		&row.HasInsert,
		&row.HasTruncate,
		&row.FunctionName,
		&row.FunctionDefinition,
	)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !row.Found || !row.IsRow || !row.IsBefore || !row.HasUpdate || !row.HasDelete || row.HasInsert || row.HasTruncate {
		return false, nil
	}
	if row.FunctionName != functionName {
		return false, nil
	}
	return ledgerEntryImmutabilityFunctionDefinitionMatches(row.FunctionDefinition), nil
}

func ledgerEntryImmutabilityFunctionDefinitionMatches(definition string) bool {
	for _, marker := range []string{
		"current_setting('app." + models.LedgerEntryMutationContextKey + "'",
		"TG_OP = 'DELETE'",
		"RAISE EXCEPTION 'ledger_entries is append-only'",
	} {
		if !strings.Contains(definition, marker) {
			return false
		}
	}
	return true
}

func checkConstraintExpression(spec checkConstraintSpec) string {
	quotedValues := make([]string, 0, len(spec.values))
	for _, value := range spec.values {
		quotedValues = append(quotedValues, quotePostgresLiteral(value))
	}
	return fmt.Sprintf("%s IN (%s)", quotePostgresIdentifier(spec.column), strings.Join(quotedValues, ","))
}

func checkConstraintValuesMatch(definition string, expectedValues []string) bool {
	actual := postgresConstraintStringLiterals(definition)
	if len(actual) != len(expectedValues) {
		return false
	}
	for _, value := range expectedValues {
		if _, ok := actual[value]; !ok {
			return false
		}
	}
	return true
}

func postgresConstraintStringLiterals(definition string) map[string]struct{} {
	if postgresStringLiteralPattern == nil {
		return map[string]struct{}{}
	}
	matches := postgresStringLiteralPattern.FindAllStringSubmatch(definition, -1)
	values := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		values[strings.ReplaceAll(match[1], "''", "'")] = struct{}{}
	}
	return values
}

func quotePostgresIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func quotePostgresLiteral(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
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
