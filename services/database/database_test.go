package database

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"core/constants"
	"core/models"
	"core/services/dbmigrations"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAutoMigrateEnabledByEnvironment(t *testing.T) {
	t.Setenv("APP_ENV", "")
	t.Setenv("ALLOW_AUTOMIGRATE_IN_PRODUCTION", "")
	if !AutoMigrateEnabled() {
		t.Fatal("AutoMigrate should be enabled outside production")
	}

	t.Setenv("APP_ENV", "development")
	if !AutoMigrateEnabled() {
		t.Fatal("AutoMigrate should be enabled in development")
	}

	t.Setenv("APP_ENV", "production")
	t.Setenv("ALLOW_AUTOMIGRATE_IN_PRODUCTION", "")
	if AutoMigrateEnabled() {
		t.Fatal("AutoMigrate should be disabled by default in production")
	}

	t.Setenv("ALLOW_AUTOMIGRATE_IN_PRODUCTION", "true")
	if !AutoMigrateEnabled() {
		t.Fatal("explicit production AutoMigrate override should be honored")
	}
}

func TestAllowAutoMigrateInProductionRequiresBooleanTrue(t *testing.T) {
	t.Setenv("ALLOW_AUTOMIGRATE_IN_PRODUCTION", "yes")
	if !AllowAutoMigrateInProduction() {
		t.Fatal("yes should be accepted as a true boolean")
	}

	t.Setenv("ALLOW_AUTOMIGRATE_IN_PRODUCTION", "definitely")
	if AllowAutoMigrateInProduction() {
		t.Fatal("invalid boolean values must not enable production AutoMigrate")
	}
}

func TestChainStateCheckpointSchemaIsRegistered(t *testing.T) {
	if !autoMigrateModelsIncludes(&models.ChainState{}) {
		t.Fatal("ChainState must be registered in AutoMigrate models")
	}

	required := map[string]bool{
		"ChainID":                 false,
		"LastProcessedBlock":      false,
		"LastProcessedHash":       false,
		"LastProcessedParentHash": false,
		"LastConfirmedBlock":      false,
		"ScannerStartBlock":       false,
		"ScannerStartPolicy":      false,
		"ContinuityStatus":        false,
		"ContinuityReason":        false,
	}
	for _, column := range requiredSchemaColumns() {
		if column.table != "chain_states" {
			continue
		}
		if _, ok := required[column.field]; ok {
			required[column.field] = true
		}
	}
	for field, found := range required {
		if !found {
			t.Fatalf("VerifySchema does not require chain_states.%s", field)
		}
	}
}

func TestMoneyEventOutboxSchemaIsRegistered(t *testing.T) {
	if !autoMigrateModelsIncludes(&models.MoneyEventOutbox{}) {
		t.Fatal("MoneyEventOutbox must be registered in AutoMigrate models")
	}

	required := map[string]bool{
		"EventID":        false,
		"IdempotencyKey": false,
		"PayloadJSON":    false,
		"Status":         false,
	}
	for _, column := range requiredSchemaColumns() {
		if column.table != "money_event_outboxes" {
			continue
		}
		if _, ok := required[column.field]; ok {
			required[column.field] = true
		}
	}
	for field, found := range required {
		if !found {
			t.Fatalf("VerifySchema does not require money_event_outboxes.%s", field)
		}
	}

	requiredIndexes := map[string]bool{
		"ux_money_event_outboxes_event_id":          false,
		"ux_money_event_outboxes_idempotency_scope": false,
	}
	for _, index := range requiredSchemaIndexes() {
		if index.table != "money_event_outboxes" {
			continue
		}
		if _, ok := requiredIndexes[index.name]; ok {
			requiredIndexes[index.name] = true
		}
	}
	for name, found := range requiredIndexes {
		if !found {
			t.Fatalf("VerifySchema does not require money_event_outboxes index %s", name)
		}
	}
}

func TestWebhookOrderingSchemaIsRegistered(t *testing.T) {
	for _, want := range []any{&models.WebhookDelivery{}, &models.WebhookResourceSequence{}} {
		if !autoMigrateModelsIncludes(want) {
			t.Fatalf("%T must be registered in AutoMigrate models", want)
		}
	}

	requiredColumns := map[string]map[string]bool{
		"webhook_deliveries": {
			"ResourceType": false, "ResourceID": false, "Sequence": false, "IdempotencyKey": false,
		},
		"webhook_resource_sequences": {
			"ID": false, "MerchantID": false, "DomainID": false,
			"ResourceType": false, "ResourceID": false, "LastSequence": false,
		},
	}
	for _, column := range requiredSchemaColumns() {
		fields, ok := requiredColumns[column.table]
		if !ok {
			continue
		}
		if _, ok := fields[column.field]; ok {
			fields[column.field] = true
		}
	}
	for table, fields := range requiredColumns {
		for field, found := range fields {
			if !found {
				t.Fatalf("VerifySchema does not require %s.%s", table, field)
			}
		}
	}

	requiredIndexes := map[string]bool{
		"ux_webhook_resource_sequence":        false,
		"idx_webhook_delivery_resource_order": false,
	}
	for _, index := range requiredSchemaIndexes() {
		if _, ok := requiredIndexes[index.name]; ok {
			requiredIndexes[index.name] = true
		}
	}
	for name, found := range requiredIndexes {
		if !found {
			t.Fatalf("VerifySchema does not require webhook ordering index %s", name)
		}
	}
}

func TestMerchantAPISecuritySchemaIsRegistered(t *testing.T) {
	if !autoMigrateModelsIncludes(&models.APIRateLimitCounter{}) {
		t.Fatal("APIRateLimitCounter must be registered in AutoMigrate models")
	}

	requiredColumns := map[string]map[string]bool{
		"domains": {
			"APIScopes": false, "APIIPAllowlist": false, "APISecretLastRotatedAt": false,
			"APISecretRevokedAt": false, "APISecretRotationPolicy": false,
		},
		"api_rate_limit_counters": {
			"ID": false, "KeyHash": false, "Count": false, "ResetAt": false,
		},
	}
	for _, column := range requiredSchemaColumns() {
		fields, ok := requiredColumns[column.table]
		if !ok {
			continue
		}
		if _, ok := fields[column.field]; ok {
			fields[column.field] = true
		}
	}
	for table, fields := range requiredColumns {
		for field, found := range fields {
			if !found {
				t.Fatalf("VerifySchema does not require %s.%s", table, field)
			}
		}
	}

	requiredIndexes := map[string]bool{
		"idx_api_rate_limit_counters_key_hash": false,
		"idx_api_rate_limit_counters_reset_at": false,
	}
	for _, index := range requiredSchemaIndexes() {
		if _, ok := requiredIndexes[index.name]; ok {
			requiredIndexes[index.name] = true
		}
	}
	for name, found := range requiredIndexes {
		if !found {
			t.Fatalf("VerifySchema does not require API security index %s", name)
		}
	}
}

func TestReliabilitySubstrateSchemaIsRegistered(t *testing.T) {
	for _, want := range []any{&models.MoneyEventInbox{}, &models.WorkerLease{}} {
		if !autoMigrateModelsIncludes(want) {
			t.Fatalf("%T must be registered in AutoMigrate models", want)
		}
	}

	requiredColumns := map[string]map[string]bool{
		"money_event_inboxes": {
			"ID": false, "EventID": false, "ConsumerName": false, "IdempotencyScope": false,
			"Status": false, "Attempts": false, "MaxAttempts": false, "LockedUntil": false,
			"ProcessedAt": false, "LastError": false, "FailureCategory": false, "EvidenceJSON": false,
		},
		"worker_leases": {
			"ID": false, "LeaseKey": false, "OwnerID": false, "Purpose": false, "Status": false,
			"Attempts": false, "LeaseUntil": false, "AcquiredAt": false, "LastHeartbeat": false,
			"ReleasedAt": false, "LastError": false,
		},
	}
	for _, column := range requiredSchemaColumns() {
		fields, ok := requiredColumns[column.table]
		if !ok {
			continue
		}
		if _, ok := fields[column.field]; ok {
			fields[column.field] = true
		}
	}
	for table, fields := range requiredColumns {
		for field, found := range fields {
			if !found {
				t.Fatalf("VerifySchema does not require %s.%s", table, field)
			}
		}
	}

	requiredIndexes := map[string]map[string]bool{
		"money_event_inboxes": {
			"ux_money_event_inbox_consumer_event":       false,
			"idx_money_event_inbox_consumer_status":     false,
			"idx_money_event_inboxes_idempotency_scope": false,
			"idx_money_event_inboxes_locked_until":      false,
		},
		"worker_leases": {
			"ux_worker_leases_key":          false,
			"idx_worker_leases_owner_id":    false,
			"idx_worker_leases_status":      false,
			"idx_worker_leases_lease_until": false,
		},
	}
	for _, index := range requiredSchemaIndexes() {
		indexes, ok := requiredIndexes[index.table]
		if !ok {
			continue
		}
		if _, ok := indexes[index.name]; ok {
			indexes[index.name] = true
		}
	}
	for table, indexes := range requiredIndexes {
		for name, found := range indexes {
			if !found {
				t.Fatalf("VerifySchema does not require %s index %s", table, name)
			}
		}
	}
}

func TestBlockSchemaIsRegistered(t *testing.T) {
	if !autoMigrateModelsIncludes(&models.Block{}) {
		t.Fatal("Block must be registered in AutoMigrate models")
	}

	required := map[string]bool{
		"ChainID":          false,
		"Number":           false,
		"Hash":             false,
		"ParentHash":       false,
		"Processed":        false,
		"Canonical":        false,
		"Status":           false,
		"ReorgedAt":        false,
		"SupersededByHash": false,
		"CorrectionReason": false,
	}
	for _, column := range requiredSchemaColumns() {
		if column.table != "blocks" {
			continue
		}
		if _, ok := required[column.field]; ok {
			required[column.field] = true
		}
	}
	for field, found := range required {
		if !found {
			t.Fatalf("VerifySchema does not require blocks.%s", field)
		}
	}

	requiredIndexes := map[string]bool{
		"ux_blocks_chain_hash":        false,
		"ux_blocks_chain_number_hash": false,
	}
	for _, index := range requiredSchemaIndexes() {
		if index.table != "blocks" {
			continue
		}
		if _, ok := requiredIndexes[index.name]; ok {
			requiredIndexes[index.name] = true
		}
	}
	for name, found := range requiredIndexes {
		if !found {
			t.Fatalf("VerifySchema does not require blocks index %s", name)
		}
	}
}

func TestLedgerEntrySchemaConstraintsAreRequired(t *testing.T) {
	required := map[string]bool{
		"ledger_entries_entry_type_check": false,
		"ledger_entries_account_check":    false,
		"ledger_entries_direction_check":  false,
		"ledger_entries_status_check":     false,
	}
	for _, constraint := range requiredSchemaConstraints() {
		if constraint.table != "ledger_entries" {
			continue
		}
		if _, ok := required[constraint.name]; ok {
			required[constraint.name] = true
		}
	}
	for name, found := range required {
		if !found {
			t.Fatalf("VerifySchema does not require ledger_entries constraint %s", name)
		}
	}
}

func TestLedgerEntryImmutabilityTriggerIsRequired(t *testing.T) {
	required := map[string]bool{
		"trg_ledger_entries_immutable": false,
	}
	for _, trigger := range requiredSchemaTriggers() {
		if trigger.table != "ledger_entries" {
			continue
		}
		if _, ok := required[trigger.name]; ok {
			required[trigger.name] = true
		}
	}
	for name, found := range required {
		if !found {
			t.Fatalf("VerifySchema does not require ledger_entries trigger %s", name)
		}
	}
}

func TestLedgerEntryImmutabilityTriggerRejectsRawMutations(t *testing.T) {
	db := openDatabasePostgresTestDB(t)
	ctx := context.Background()
	if err := db.AutoMigrate(&models.LedgerEntry{}); err != nil {
		t.Fatalf("automigrate ledger entries: %v", err)
	}
	if err := ReconcileLedgerEntryImmutabilityGuard(ctx, db); err != nil {
		t.Fatalf("install ledger immutability trigger: %v", err)
	}
	matches, err := postgresTriggerMatches(ctx, db, "ledger_entries", ledgerEntryImmutabilityTriggerName, ledgerEntryImmutabilityFunctionName)
	if err != nil {
		t.Fatalf("verify trigger definition: %v", err)
	}
	if !matches {
		t.Fatal("ledger immutability trigger definition did not match expected events/function")
	}

	row := databaseTestLedgerEntry("raw-trigger-" + uuid.NewString())
	if err := db.WithContext(ctx).Create(&row).Error; err != nil {
		t.Fatalf("seed ledger row: %v", err)
	}
	if err := db.WithContext(ctx).Exec("UPDATE ledger_entries SET description = ? WHERE id = ?", "raw update", row.ID).Error; err == nil || !strings.Contains(err.Error(), "ledger_entries is append-only") {
		t.Fatalf("raw update err = %v, want append-only trigger error", err)
	}
	if err := db.WithContext(ctx).Exec("DELETE FROM ledger_entries WHERE id = ?", row.ID).Error; err == nil || !strings.Contains(err.Error(), "ledger_entries is append-only") {
		t.Fatalf("raw delete err = %v, want append-only trigger error", err)
	}

	if err := WithLedgerEntryMutationAllowed(ctx, db, func(tx *gorm.DB) error {
		if err := tx.Exec("UPDATE ledger_entries SET description = ? WHERE id = ?", "migration update", row.ID).Error; err != nil {
			return err
		}
		return tx.Exec("DELETE FROM ledger_entries WHERE id = ?", row.ID).Error
	}); err != nil {
		t.Fatalf("scoped migration mutation: %v", err)
	}

	next := databaseTestLedgerEntry("raw-trigger-after-" + uuid.NewString())
	if err := db.WithContext(ctx).Create(&next).Error; err != nil {
		t.Fatalf("seed second ledger row: %v", err)
	}
	if err := db.WithContext(ctx).Exec("UPDATE ledger_entries SET description = ? WHERE id = ?", "leaked update", next.ID).Error; err == nil || !strings.Contains(err.Error(), "ledger_entries is append-only") {
		t.Fatalf("post-helper raw update err = %v, want append-only trigger error", err)
	}
}

func TestLedgerEntryCheckConstraintSpecsIncludeSweepValues(t *testing.T) {
	entryTypeSpec := ledgerCheckConstraintSpecByName(t, "ledger_entries_entry_type_check")
	for _, value := range []string{
		models.LedgerEntryTypeSweepHold,
		models.LedgerEntryTypeSweepRelease,
		models.LedgerEntryTypeSweepDebit,
		models.LedgerEntryTypeRefundRelease,
	} {
		requireConstraintSpecValue(t, entryTypeSpec, value)
	}

	accountSpec := ledgerCheckConstraintSpecByName(t, "ledger_entries_account_check")
	requireConstraintSpecValue(t, accountSpec, models.LedgerAccountSweepTransit)
}

func TestLedgerEntryCheckConstraintMatchingRejectsStaleSweepDefinitions(t *testing.T) {
	entryTypeSpec := ledgerCheckConstraintSpecByName(t, "ledger_entries_entry_type_check")
	staleEntryTypeDefinition := "CHECK (((entry_type)::text = ANY ((ARRAY['deposit_pending'::character varying, 'deposit_available'::character varying, 'withdrawal_hold'::character varying, 'withdrawal_release'::character varying, 'withdrawal_debit'::character varying, 'refund_hold'::character varying, 'refund_debit'::character varying, 'reorg_reversal'::character varying, 'adjustment'::character varying])::text[])))"
	if checkConstraintValuesMatch(staleEntryTypeDefinition, entryTypeSpec.values) {
		t.Fatal("stale entry_type constraint without sweep values must not verify")
	}
	if !checkConstraintValuesMatch("CHECK ("+checkConstraintExpression(entryTypeSpec)+")", entryTypeSpec.values) {
		t.Fatal("current entry_type constraint expression should verify")
	}

	accountSpec := ledgerCheckConstraintSpecByName(t, "ledger_entries_account_check")
	staleAccountDefinition := "CHECK (((account)::text = ANY ((ARRAY['merchant_pending'::character varying, 'merchant_available'::character varying, 'platform_clearing'::character varying, 'withdrawal_transit'::character varying, 'refund_transit'::character varying])::text[])))"
	if checkConstraintValuesMatch(staleAccountDefinition, accountSpec.values) {
		t.Fatal("stale account constraint without sweep_transit must not verify")
	}
	if !checkConstraintValuesMatch("CHECK ("+checkConstraintExpression(accountSpec)+")", accountSpec.values) {
		t.Fatal("current account constraint expression should verify")
	}
}

func TestApplyGORMMigrationsReconcilesLedgerCheckConstraints(t *testing.T) {
	sourceBytes, err := os.ReadFile("database.go")
	if err != nil {
		t.Fatalf("read database.go: %v", err)
	}
	source := string(sourceBytes)
	if !strings.Contains(source, "ReconcileLedgerEntryCheckConstraints(ctx, db)") {
		t.Fatal("ApplyGORMMigrations must reconcile ledger check constraints after AutoMigrate")
	}
	if !strings.Contains(source, "ReconcileLedgerEntryImmutabilityGuard(ctx, db)") {
		t.Fatal("ApplyGORMMigrations must reconcile ledger entry immutability guard after AutoMigrate")
	}
	if !strings.Contains(source, "ReconcileWalletChilizSpicyAddressIndex(ctx, db)") {
		t.Fatal("ApplyGORMMigrations must reconcile the optional Chiliz Spicy wallet address index after AutoMigrate")
	}
}

func TestReconcileWalletChilizSpicyAddressIndexUpgradesFullUniqueIndex(t *testing.T) {
	db := openDatabasePostgresTestDB(t)
	ctx := context.Background()
	if err := db.WithContext(ctx).AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}); err != nil {
		t.Fatalf("automigrate wallet tables: %v", err)
	}
	if err := db.WithContext(ctx).Exec("DROP INDEX IF EXISTS " + quotePostgresIdentifier(walletChilizSpicyIndexName)).Error; err != nil {
		t.Fatalf("drop partial wallet index: %v", err)
	}
	if err := db.WithContext(ctx).Exec(fmt.Sprintf(
		`CREATE UNIQUE INDEX %s ON %s (%s)`,
		quotePostgresIdentifier(walletChilizSpicyIndexName),
		quotePostgresIdentifier("wallets"),
		quotePostgresIdentifier("chiliz_spicy_address"),
	)).Error; err != nil {
		t.Fatalf("create stale full wallet index: %v", err)
	}

	definition, predicate, unique, found, err := postgresIndexDefinition(ctx, db, "wallets", walletChilizSpicyIndexName)
	if err != nil {
		t.Fatal(err)
	}
	if !found || walletChilizSpicyIndexMatches(definition, predicate, unique) {
		t.Fatalf("stale full index unexpectedly matched: definition=%q predicate=%q unique=%v", definition, predicate, unique)
	}
	if err := ReconcileWalletChilizSpicyAddressIndex(ctx, db); err != nil {
		t.Fatalf("reconcile wallet index: %v", err)
	}
	definition, predicate, unique, found, err = postgresIndexDefinition(ctx, db, "wallets", walletChilizSpicyIndexName)
	if err != nil {
		t.Fatal(err)
	}
	if !found || !walletChilizSpicyIndexMatches(definition, predicate, unique) {
		t.Fatalf("reconciled index is stale: definition=%q predicate=%q unique=%v", definition, predicate, unique)
	}

	merchantID := uuid.New()
	domainID := uuid.New()
	if err := db.WithContext(ctx).Create(&models.Merchant{
		ID: merchantID, Name: "Optional Spicy Address", Email: "optional-spicy-" + merchantID.String() + "@example.test", IsActive: true,
	}).Error; err != nil {
		t.Fatalf("seed merchant: %v", err)
	}
	if err := db.WithContext(ctx).Create(&models.Domain{
		ID: domainID, MerchantID: merchantID, DomainURL: "optional-spicy.example.test", APIKey: "pk_" + domainID.String(), APISecret: "secret", HDAccountID: 4242,
	}).Error; err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	wallets := make([]models.Wallet, 0, 2)
	for i := uint32(1); i <= 2; i++ {
		walletID := uuid.New()
		suffix := strings.ReplaceAll(walletID.String(), "-", "")
		wallets = append(wallets, models.Wallet{
			ID: walletID, MerchantID: merchantID, DomainID: domainID, HDAccountID: 4242, HDAddressId: i,
			ProductID: "wallet:" + suffix, UserID: "user:" + suffix,
			BitcoinAddress: "btc-" + suffix, EthereumAddress: "eth-" + suffix,
			AvalancheAddress: "avax-" + suffix, BinanceAddress: "bnb-" + suffix,
			BaseAddress: "base-" + suffix, ArbitrumAddress: "arb-" + suffix,
			UnichainAddress: "uni-" + suffix, TronAddress: "tron-" + suffix,
			SolanaAddress: "sol-" + suffix, ChilizAddress: "chz-" + suffix,
			ChilizSpicyAddress: "",
		})
	}
	if err := db.WithContext(ctx).Create(&wallets).Error; err != nil {
		t.Fatalf("create wallets with empty optional addresses: %v", err)
	}
	if err := db.WithContext(ctx).Model(&models.Wallet{}).Where("id = ?", wallets[0].ID).Update("chiliz_spicy_address", "spicy-duplicate").Error; err != nil {
		t.Fatalf("set first non-empty spicy address: %v", err)
	}
	if err := db.WithContext(ctx).Model(&models.Wallet{}).Where("id = ?", wallets[1].ID).Update("chiliz_spicy_address", "spicy-duplicate").Error; err == nil {
		t.Fatal("partial unique index must reject duplicate non-empty Chiliz Spicy addresses")
	}
}

func TestLedgerBalanceProjectionSchemaIsRegistered(t *testing.T) {
	if !autoMigrateModelsIncludes(&models.LedgerBalanceProjection{}) {
		t.Fatal("LedgerBalanceProjection must be registered in AutoMigrate models")
	}

	required := map[string]bool{
		"ID":                     false,
		"ScopeType":              false,
		"ScopeKey":               false,
		"MerchantID":             false,
		"DomainID":               false,
		"WalletID":               false,
		"ChainID":                false,
		"Token":                  false,
		"TokenFingerprint":       false,
		"Symbol":                 false,
		"Decimals":               false,
		"Account":                false,
		"BalanceRaw":             false,
		"SourceLedgerEntryCount": false,
		"ProjectedAt":            false,
	}
	for _, column := range requiredSchemaColumns() {
		if column.table != "ledger_balance_projections" {
			continue
		}
		if _, ok := required[column.field]; ok {
			required[column.field] = true
		}
	}
	for field, found := range required {
		if !found {
			t.Fatalf("VerifySchema does not require ledger_balance_projections.%s", field)
		}
	}

	requiredIndexes := map[string]bool{
		"ux_ledger_balance_projection_scope":         false,
		"idx_ledger_balance_projections_scope_key":   false,
		"idx_ledger_balance_projections_merchant_id": false,
		"idx_ledger_balance_projections_wallet_id":   false,
	}
	for _, index := range requiredSchemaIndexes() {
		if index.table != "ledger_balance_projections" {
			continue
		}
		if _, ok := requiredIndexes[index.name]; ok {
			requiredIndexes[index.name] = true
		}
	}
	for name, found := range requiredIndexes {
		if !found {
			t.Fatalf("VerifySchema does not require ledger_balance_projections index %s", name)
		}
	}
}

func TestOutboundHoldSchemaColumnsAreRequired(t *testing.T) {
	required := map[string]bool{
		"SweepJobID": false,
	}
	for _, column := range requiredSchemaColumns() {
		if column.table != "ledger_entries" {
			continue
		}
		if _, ok := required[column.field]; ok {
			required[column.field] = true
		}
	}
	for field, found := range required {
		if !found {
			t.Fatalf("VerifySchema does not require ledger_entries.%s", field)
		}
	}

	requiredIndexes := map[string]bool{
		"idx_ledger_entries_sweep_job_id": false,
	}
	for _, index := range requiredSchemaIndexes() {
		if index.table != "ledger_entries" {
			continue
		}
		if _, ok := requiredIndexes[index.name]; ok {
			requiredIndexes[index.name] = true
		}
	}
	for name, found := range requiredIndexes {
		if !found {
			t.Fatalf("VerifySchema does not require ledger_entries index %s", name)
		}
	}
}

func TestSweepJobRecoverySchemaColumnsAreRequired(t *testing.T) {
	if !autoMigrateModelsIncludes(&models.SweepJob{}) {
		t.Fatal("SweepJob must be registered in AutoMigrate models")
	}

	required := map[string]bool{
		"TransactionUniqueHash":  false,
		"Status":                 false,
		"Attempts":               false,
		"MaxAttempts":            false,
		"LastError":              false,
		"FailureCategory":        false,
		"NextRunAt":              false,
		"LockedUntil":            false,
		"SweepTxHash":            false,
		"BatchID":                false,
		"BatchKey":               false,
		"BatchOrdinal":           false,
		"BatchSize":              false,
		"BatchPolicy":            false,
		"PrefundAttempts":        false,
		"PrefundMaxAttempts":     false,
		"PrefundLastError":       false,
		"PrefundFailureCategory": false,
		"PrefundLastAttemptAt":   false,
		"PrefundedAt":            false,
		"OperatorAction":         false,
		"OperatorNote":           false,
		"RecoveryAction":         false,
		"RecoveredAt":            false,
	}
	for _, column := range requiredSchemaColumns() {
		if column.table != "sweep_jobs" {
			continue
		}
		if _, ok := required[column.field]; ok {
			required[column.field] = true
		}
	}
	for field, found := range required {
		if !found {
			t.Fatalf("VerifySchema does not require sweep_jobs.%s", field)
		}
	}

	requiredIndexes := map[string]bool{
		"idx_sweep_jobs_transaction_unique_hash":  false,
		"idx_sweep_jobs_batch_id":                 false,
		"idx_sweep_jobs_batch_key":                false,
		"idx_sweep_jobs_failure_category":         false,
		"idx_sweep_jobs_prefund_failure_category": false,
		"idx_sweep_jobs_prefund_last_attempt_at":  false,
		"idx_sweep_jobs_operator_action":          false,
		"idx_sweep_jobs_recovery_action":          false,
		"idx_sweep_jobs_recovered_at":             false,
	}
	for _, index := range requiredSchemaIndexes() {
		if index.table != "sweep_jobs" {
			continue
		}
		if _, ok := requiredIndexes[index.name]; ok {
			requiredIndexes[index.name] = true
		}
	}
	for name, found := range requiredIndexes {
		if !found {
			t.Fatalf("VerifySchema does not require sweep_jobs index %s", name)
		}
	}
}

func TestOutboundTransactionManagerSchemaIsRegistered(t *testing.T) {
	if !autoMigrateModelsIncludes(&models.OutboundTransaction{}) {
		t.Fatal("OutboundTransaction must be registered in AutoMigrate models")
	}
	if !autoMigrateModelsIncludes(&models.OutboundChainResourceReservation{}) {
		t.Fatal("OutboundChainResourceReservation must be registered in AutoMigrate models")
	}

	required := map[string]map[string]bool{
		"outbound_transactions": {
			"ID":                   false,
			"IdempotencyKey":       false,
			"ResourceType":         false,
			"ResourceID":           false,
			"MerchantID":           false,
			"DomainID":             false,
			"WalletID":             false,
			"ChainID":              false,
			"ChainName":            false,
			"Token":                false,
			"Symbol":               false,
			"Decimals":             false,
			"AmountRaw":            false,
			"ToAddress":            false,
			"Status":               false,
			"TxHash":               false,
			"Attempts":             false,
			"MaxAttempts":          false,
			"LockedUntil":          false,
			"NextRunAt":            false,
			"SignedAt":             false,
			"BroadcastAttemptedAt": false,
			"BroadcastedAt":        false,
			"FinalizedAt":          false,
			"FeePolicyJSON":        false,
			"ReplacementParentID":  false,
			"ReplacementReason":    false,
			"ReplacesTxHash":       false,
			"ErrorCategory":        false,
			"ErrorDetail":          false,
			"ActorID":              false,
			"CorrelationID":        false,
		},
		"outbound_chain_resource_reservations": {
			"ID":                    false,
			"OutboundTransactionID": false,
			"ResourceType":          false,
			"ResourceKey":           false,
			"Status":                false,
			"ChainID":               false,
			"ChainName":             false,
			"WalletID":              false,
			"WalletAddress":         false,
			"OwnerType":             false,
			"OwnerID":               false,
			"Intent":                false,
			"Nonce":                 false,
			"UTXOTxID":              false,
			"UTXOVout":              false,
			"UTXOValueRaw":          false,
			"LeaseExpiresAt":        false,
			"ConsumedAt":            false,
			"ReleasedAt":            false,
			"TxHash":                false,
		},
	}
	for _, column := range requiredSchemaColumns() {
		fields, ok := required[column.table]
		if !ok {
			continue
		}
		if _, ok := fields[column.field]; ok {
			fields[column.field] = true
		}
	}
	for table, fields := range required {
		for field, found := range fields {
			if !found {
				t.Fatalf("VerifySchema does not require %s.%s", table, field)
			}
		}
	}

	requiredIndexes := map[string]map[string]bool{
		"outbound_transactions": {
			"ux_outbound_transactions_idempotency_key": false,
			"idx_outbound_transactions_resource":       false,
			"idx_outbound_transactions_status":         false,
			"idx_outbound_transactions_tx_hash":        false,
			"idx_outbound_transactions_locked_until":   false,
			"idx_outbound_transactions_next_run_at":    false,
		},
		"outbound_chain_resource_reservations": {
			"idx_outbound_chain_resource_reservations_resource_key":           false,
			"idx_outbound_resource_type_status":                               false,
			"idx_outbound_resource_owner":                                     false,
			"idx_outbound_chain_resource_reservations_outbound_trans7563aee7": false,
		},
	}
	for _, index := range requiredSchemaIndexes() {
		indexes, ok := requiredIndexes[index.table]
		if !ok {
			continue
		}
		if _, ok := indexes[index.name]; ok {
			indexes[index.name] = true
		}
	}
	for table, indexes := range requiredIndexes {
		for name, found := range indexes {
			if !found {
				t.Fatalf("VerifySchema does not require %s index %s", table, name)
			}
		}
	}
}

func TestOutboundLifecycleSchemaColumnsAreRequired(t *testing.T) {
	required := map[string]map[string]bool{
		"withdrawal_requests": {
			"BroadcastedAt":  false,
			"FinalizedAt":    false,
			"IdempotencyKey": false,
			"CorrelationID":  false,
		},
		"refunds": {
			"WalletID":       false,
			"Chain":          false,
			"Token":          false,
			"Symbol":         false,
			"Decimals":       false,
			"ToAddress":      false,
			"BroadcastedAt":  false,
			"FinalizedAt":    false,
			"IdempotencyKey": false,
			"CorrelationID":  false,
		},
		"idempotency_keys": {
			"ResourceType": false,
			"ResourceID":   false,
		},
		"activity_logs": {
			"DomainID":      false,
			"ActorRole":     false,
			"Decision":      false,
			"Reason":        false,
			"BeforeStatus":  false,
			"AfterStatus":   false,
			"CorrelationID": false,
		},
	}
	for _, column := range requiredSchemaColumns() {
		fields, ok := required[column.table]
		if !ok {
			continue
		}
		if _, ok := fields[column.field]; ok {
			fields[column.field] = true
		}
	}
	for table, fields := range required {
		for field, found := range fields {
			if !found {
				t.Fatalf("VerifySchema does not require %s.%s", table, field)
			}
		}
	}

	requiredIndexes := map[string]bool{
		"idx_withdrawal_requests_idempotency_key": false,
		"idx_withdrawal_requests_correlation_id":  false,
		"idx_withdrawal_requests_broadcasted_at":  false,
		"idx_withdrawal_requests_finalized_at":    false,
		"idx_refunds_wallet_id":                   false,
		"idx_refunds_idempotency_key":             false,
		"idx_refunds_correlation_id":              false,
		"idx_refunds_broadcasted_at":              false,
		"idx_refunds_finalized_at":                false,
		"idx_idempotency_keys_resource_id":        false,
		"idx_activity_logs_domain_id":             false,
		"idx_activity_logs_actor_role":            false,
		"idx_activity_logs_decision":              false,
		"idx_activity_logs_before_status":         false,
		"idx_activity_logs_after_status":          false,
		"idx_activity_logs_correlation_id":        false,
	}
	for _, index := range requiredSchemaIndexes() {
		if _, ok := requiredIndexes[index.name]; ok {
			requiredIndexes[index.name] = true
		}
	}
	for name, found := range requiredIndexes {
		if !found {
			t.Fatalf("VerifySchema does not require lifecycle index %s", name)
		}
	}
}

func TestOutboundPolicySchemaIsRegistered(t *testing.T) {
	if !autoMigrateModelsIncludes(&models.OutboundPolicySetting{}) {
		t.Fatal("OutboundPolicySetting must be registered in AutoMigrate models")
	}
	if !autoMigrateModelsIncludes(&models.OutboundAddressWhitelist{}) {
		t.Fatal("OutboundAddressWhitelist must be registered in AutoMigrate models")
	}

	required := map[string]map[string]bool{
		"outbound_policy_settings": {
			"MerchantID":         false,
			"DomainID":           false,
			"Chain":              false,
			"Token":              false,
			"WhitelistRequired":  false,
			"EmergencyFrozen":    false,
			"MaxAmountRaw":       false,
			"VelocityLimitRaw":   false,
			"VelocityWindowSecs": false,
		},
		"outbound_address_whitelists": {
			"MerchantID": false,
			"DomainID":   false,
			"Chain":      false,
			"Token":      false,
			"Address":    false,
			"IsActive":   false,
		},
		"admins": {
			"Role": false,
		},
	}
	for _, column := range requiredSchemaColumns() {
		fields, ok := required[column.table]
		if !ok {
			continue
		}
		if _, ok := fields[column.field]; ok {
			fields[column.field] = true
		}
	}
	for table, fields := range required {
		for field, found := range fields {
			if !found {
				t.Fatalf("VerifySchema does not require %s.%s", table, field)
			}
		}
	}

	requiredIndexes := map[string]bool{
		"idx_outbound_policy_settings_merchant_id":    false,
		"idx_outbound_policy_settings_domain_id":      false,
		"idx_outbound_policy_settings_chain":          false,
		"idx_outbound_policy_settings_token":          false,
		"idx_outbound_address_whitelists_merchant_id": false,
		"idx_outbound_address_whitelists_domain_id":   false,
		"idx_outbound_address_whitelists_chain":       false,
		"idx_outbound_address_whitelists_token":       false,
		"idx_outbound_address_whitelists_address":     false,
		"idx_outbound_address_whitelists_is_active":   false,
		"idx_admins_role":                             false,
	}
	for _, index := range requiredSchemaIndexes() {
		if _, ok := requiredIndexes[index.name]; ok {
			requiredIndexes[index.name] = true
		}
	}
	for name, found := range requiredIndexes {
		if !found {
			t.Fatalf("VerifySchema does not require outbound policy index %s", name)
		}
	}
}

func TestNetworkOperationalStateSchemaIsRegistered(t *testing.T) {
	if !autoMigrateModelsIncludes(&models.NetworkOperationalState{}) {
		t.Fatal("NetworkOperationalState must be registered in AutoMigrate models")
	}

	requiredColumns := map[string]bool{
		"ID": false, "ChainID": false, "Mode": false, "Reason": false,
		"UpdatedBy": false, "CreatedAt": false, "UpdatedAt": false,
	}
	for _, column := range requiredSchemaColumns() {
		if column.table != "network_operational_states" {
			continue
		}
		if _, ok := requiredColumns[column.field]; ok {
			requiredColumns[column.field] = true
		}
	}
	for field, found := range requiredColumns {
		if !found {
			t.Fatalf("VerifySchema does not require network_operational_states.%s", field)
		}
	}

	requiredIndexes := map[string]bool{
		"ux_network_operational_states_chain_id": false,
		"idx_network_operational_states_mode":    false,
	}
	for _, index := range requiredSchemaIndexes() {
		if index.table != "network_operational_states" {
			continue
		}
		if _, ok := requiredIndexes[index.name]; ok {
			requiredIndexes[index.name] = true
		}
	}
	for name, found := range requiredIndexes {
		if !found {
			t.Fatalf("VerifySchema does not require network_operational_states index %s", name)
		}
	}

	foundConstraint := false
	for _, constraint := range requiredSchemaConstraints() {
		if constraint.table == "network_operational_states" && constraint.name == "network_operational_states_mode_check" {
			foundConstraint = true
			break
		}
	}
	if !foundConstraint {
		t.Fatal("VerifySchema does not require network operational mode check constraint")
	}
}

func TestProviderHealthSchemaIsRegistered(t *testing.T) {
	if !autoMigrateModelsIncludes(&models.ProviderHealthSnapshot{}) {
		t.Fatal("ProviderHealthSnapshot must be registered in AutoMigrate models")
	}

	required := map[string]bool{
		"ID":                  false,
		"ChainID":             false,
		"ChainName":           false,
		"ProviderLabel":       false,
		"ProviderURLHash":     false,
		"Reachable":           false,
		"Status":              false,
		"LatestHeight":        false,
		"HeadHash":            false,
		"ResponseLatencyMS":   false,
		"LagFromReference":    false,
		"ErrorCategory":       false,
		"ErrorDetail":         false,
		"Selected":            false,
		"FailoverReason":      false,
		"ConsecutiveFailures": false,
		"CheckedAt":           false,
	}
	for _, column := range requiredSchemaColumns() {
		if column.table != "provider_health_snapshots" {
			continue
		}
		if _, ok := required[column.field]; ok {
			required[column.field] = true
		}
	}
	for field, found := range required {
		if !found {
			t.Fatalf("VerifySchema does not require provider_health_snapshots.%s", field)
		}
	}

	requiredIndexes := map[string]bool{
		"ux_provider_health_chain_provider":               false,
		"idx_provider_health_snapshots_chain_id":          false,
		"idx_provider_health_snapshots_chain_name":        false,
		"idx_provider_health_snapshots_provider_url_hash": false,
		"idx_provider_health_snapshots_status":            false,
		"idx_provider_health_snapshots_error_category":    false,
		"idx_provider_health_snapshots_selected":          false,
		"idx_provider_health_snapshots_checked_at":        false,
	}
	for _, index := range requiredSchemaIndexes() {
		if _, ok := requiredIndexes[index.name]; ok {
			requiredIndexes[index.name] = true
		}
	}
	for name, found := range requiredIndexes {
		if !found {
			t.Fatalf("VerifySchema does not require provider health index %s", name)
		}
	}
}

func TestPaymentOutcomeSchemaColumnsAreRequired(t *testing.T) {
	required := map[string]bool{
		"LinkType":             false,
		"PaymentOutcome":       false,
		"PaymentOutcomeReason": false,
		"MatchedAmountRaw":     false,
		"ShortfallAmountRaw":   false,
		"ExcessAmountRaw":      false,
	}
	for _, column := range requiredSchemaColumns() {
		if column.table != "payment_sessions" {
			continue
		}
		if _, ok := required[column.field]; ok {
			required[column.field] = true
		}
	}
	for field, found := range required {
		if !found {
			t.Fatalf("VerifySchema does not require payment_sessions.%s", field)
		}
	}
}

func TestPaymentDepositAllocationSchemaColumnsAreRequired(t *testing.T) {
	required := map[string]bool{
		"ID":                        false,
		"PaymentSessionID":          false,
		"TransactionUniqueHash":     false,
		"ObservedAddress":           false,
		"ObservedAddressNormalized": false,
		"AmountRaw":                 false,
		"MemoStatus":                false,
		"Status":                    false,
		"ReorgedAt":                 false,
	}
	for _, column := range requiredSchemaColumns() {
		if column.table != "payment_deposit_allocations" {
			continue
		}
		if _, ok := required[column.field]; ok {
			required[column.field] = true
		}
	}
	for field, found := range required {
		if !found {
			t.Fatalf("VerifySchema does not require payment_deposit_allocations.%s", field)
		}
	}
}

func TestProductLinkTypeSchemaColumnIsRequired(t *testing.T) {
	required := map[string]bool{
		"LinkType": false,
	}
	for _, column := range requiredSchemaColumns() {
		if column.table != "products" {
			continue
		}
		if _, ok := required[column.field]; ok {
			required[column.field] = true
		}
	}
	for field, found := range required {
		if !found {
			t.Fatalf("VerifySchema does not require products.%s", field)
		}
	}
}

func TestReorgCorrectionSchemaColumnsAreRequired(t *testing.T) {
	required := map[string]map[string]bool{
		"chain_facts": {
			"Status":              false,
			"ReorgedAt":           false,
			"SupersededByEventID": false,
			"CorrectionReason":    false,
		},
		"deposits": {
			"ReorgedAt":           false,
			"SupersededByEventID": false,
			"CorrectionReason":    false,
		},
		"transactions": {
			"OriginalEventID":    false,
			"OriginalResourceID": false,
			"CorrectionReason":   false,
		},
	}
	for _, column := range requiredSchemaColumns() {
		fields, ok := required[column.table]
		if !ok {
			continue
		}
		if _, ok := fields[column.field]; ok {
			fields[column.field] = true
		}
	}
	for table, fields := range required {
		for field, found := range fields {
			if !found {
				t.Fatalf("VerifySchema does not require %s.%s", table, field)
			}
		}
	}
}

func TestScopedReconciliationSchemaColumnsAreRequired(t *testing.T) {
	required := map[string]bool{
		"MerchantID":              false,
		"DomainID":                false,
		"ScopeKey":                false,
		"ResourceType":            false,
		"ResourceID":              false,
		"AffectedResourceIDsJSON": false,
		"EvidenceJSON":            false,
		"Outcome":                 false,
		"NextRunAt":               false,
	}
	for _, column := range requiredSchemaColumns() {
		if column.table != "reconciliation_jobs" {
			continue
		}
		if _, ok := required[column.field]; ok {
			required[column.field] = true
		}
	}
	for field, found := range required {
		if !found {
			t.Fatalf("VerifySchema does not require reconciliation_jobs.%s", field)
		}
	}
	requiredIndexes := map[string]bool{
		"idx_reconciliation_jobs_merchant_id":   false,
		"idx_reconciliation_jobs_domain_id":     false,
		"idx_reconciliation_jobs_scope_key":     false,
		"idx_reconciliation_jobs_resource_type": false,
		"idx_reconciliation_jobs_resource_id":   false,
		"idx_reconciliation_jobs_outcome":       false,
		"idx_reconciliation_jobs_next_run_at":   false,
	}
	for _, index := range requiredSchemaIndexes() {
		if index.table != "reconciliation_jobs" {
			continue
		}
		if _, ok := requiredIndexes[index.name]; ok {
			requiredIndexes[index.name] = true
		}
	}
	for name, found := range requiredIndexes {
		if !found {
			t.Fatalf("VerifySchema does not require reconciliation_jobs index %s", name)
		}
	}
}

func TestWalletAddressLifecycleSchemaColumnsAreRequired(t *testing.T) {
	required := map[string]map[string]bool{
		"wallet_address_reservations": {
			"ReservedAt": false,
			"AssignedAt": false,
			"ExpiresAt":  false,
			"ReleasedAt": false,
			"RetiredAt":  false,
			"CreatedAt":  false,
			"UpdatedAt":  false,
		},
		"wallet_addresses": {
			"Source":        false,
			"ReservationID": false,
			"ReservedAt":    false,
			"AssignedAt":    false,
			"ActivatedAt":   false,
			"UsedAt":        false,
			"ExpiresAt":     false,
			"ReleasedAt":    false,
			"RetiredAt":     false,
			"CreatedAt":     false,
			"UpdatedAt":     false,
		},
		"wallet_address_gap_scan_cursors": {
			"ChainName":    false,
			"AnomalyCount": false,
			"LastAnomaly":  false,
			"ScannedAt":    false,
			"CreatedAt":    false,
			"UpdatedAt":    false,
		},
		"wallet_address_gap_scan_anomalies": {
			"ChainName": false,
			"Purpose":   false,
			"Address":   false,
			"Detail":    false,
			"CreatedAt": false,
			"UpdatedAt": false,
		},
	}
	for _, column := range requiredSchemaColumns() {
		fields, ok := required[column.table]
		if !ok {
			continue
		}
		if _, ok := fields[column.field]; ok {
			fields[column.field] = true
		}
	}
	for table, fields := range required {
		for field, found := range fields {
			if !found {
				t.Fatalf("VerifySchema does not require %s.%s", table, field)
			}
		}
	}
}

func TestApplyGORMMigrationsEntrypointExists(t *testing.T) {
	if reflect.ValueOf(ApplyGORMMigrations).IsNil() {
		t.Fatal("ApplyGORMMigrations must be available for GORM-managed migration jobs")
	}
}

func TestEmbeddedMigrationArtifactsAreValid(t *testing.T) {
	if err := dbmigrations.Validate(); err != nil {
		t.Fatalf("embedded migration artifacts are invalid: %v", err)
	}
	latest, err := dbmigrations.LatestID()
	if err != nil {
		t.Fatalf("latest migration id: %v", err)
	}
	if latest != "202607130012_wallet_chiliz_spicy_partial_unique" {
		t.Fatalf("latest migration id = %q, want wallet Chiliz Spicy partial-index migration", latest)
	}
}

func TestApplyGORMMigrationsAppliesBlankDatabase(t *testing.T) {
	db := openDatabasePostgresTestDB(t)
	ctx := context.Background()

	if err := ApplyGORMMigrations(ctx, db); err != nil {
		t.Fatalf("apply migrations to blank database: %v", err)
	}
	if err := VerifySchema(ctx, db); err != nil {
		t.Fatalf("verify blank database schema: %v", err)
	}
}

func TestApplyGORMMigrationsUpgradesPartialSchema(t *testing.T) {
	db := openDatabasePostgresTestDB(t)
	ctx := context.Background()

	if err := db.WithContext(ctx).AutoMigrate(
		&models.ChainState{},
		&models.Domain{},
		&models.WebhookDelivery{},
		&models.SweepJob{},
	); err != nil {
		t.Fatalf("seed partial schema: %v", err)
	}

	if err := ApplyGORMMigrations(ctx, db); err != nil {
		t.Fatalf("upgrade partial schema: %v", err)
	}
	if err := VerifySchema(ctx, db); err != nil {
		t.Fatalf("verify upgraded schema: %v", err)
	}
}

func TestVerifySchemaReportsMissingIndexDrift(t *testing.T) {
	db := openDatabasePostgresTestDB(t)
	ctx := context.Background()

	if err := ApplyGORMMigrations(ctx, db); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	if err := db.WithContext(ctx).Exec("DROP INDEX IF EXISTS " + quotePostgresIdentifier("idx_api_rate_limit_counters_reset_at")).Error; err != nil {
		t.Fatalf("drop test index: %v", err)
	}

	err := VerifySchema(ctx, db)
	if err == nil {
		t.Fatal("VerifySchema should fail when a required index is missing")
	}
	if !strings.Contains(err.Error(), "api_rate_limit_counters index idx_api_rate_limit_counters_reset_at is missing") {
		t.Fatalf("VerifySchema error = %q, want missing index detail", err.Error())
	}
}

func TestWalletAddressLookupSchemaIsRegistered(t *testing.T) {
	if !autoMigrateModelsIncludes(&models.WalletAddressLookup{}) {
		t.Fatal("WalletAddressLookup must be registered in AutoMigrate models")
	}

	required := map[string]bool{
		"ID":                false,
		"ChainID":           false,
		"ChainName":         false,
		"Address":           false,
		"NormalizedAddress": false,
		"Asset":             false,
		"MerchantID":        false,
		"DomainID":          false,
		"WalletID":          false,
		"ProductID":         false,
		"UserID":            false,
		"Source":            false,
	}
	for _, column := range requiredSchemaColumns() {
		if column.table != "wallet_address_lookups" {
			continue
		}
		if _, ok := required[column.field]; ok {
			required[column.field] = true
		}
	}
	for field, found := range required {
		if !found {
			t.Fatalf("VerifySchema does not require wallet_address_lookups.%s", field)
		}
	}

	requiredIndexes := map[string]bool{
		"ux_wallet_address_lookup_chain_address":        false,
		"idx_wallet_address_lookups_chain_id":           false,
		"idx_wallet_address_lookups_normalized_address": false,
		"idx_wallet_address_lookups_wallet_id":          false,
	}
	for _, index := range requiredSchemaIndexes() {
		if index.table != "wallet_address_lookups" {
			continue
		}
		if _, ok := requiredIndexes[index.name]; ok {
			requiredIndexes[index.name] = true
		}
	}
	for name, found := range requiredIndexes {
		if !found {
			t.Fatalf("VerifySchema does not require wallet_address_lookups index %s", name)
		}
	}
}

func openDatabasePostgresTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("OUTBOX_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("MONEY_OUTBOX_TEST_DATABASE_URL")
	}
	if dsn == "" {
		dsn = os.Getenv("TEST_DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("set OUTBOX_TEST_DATABASE_URL to run database Postgres tests")
	}

	adminDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("connect test postgres: %v", err)
	}
	if err := adminDB.Exec(`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`).Error; err != nil {
		t.Fatalf("enable uuid extension: %v", err)
	}
	schemaName := "database_test_" + strings.ReplaceAll(uuid.NewString(), "-", "_")
	quotedSchema := quotePostgresIdentifier(schemaName)
	if err := adminDB.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	if err := adminDB.Exec(
		"CREATE FUNCTION " + quotedSchema + ".uuid_generate_v4() RETURNS uuid LANGUAGE SQL VOLATILE PARALLEL SAFE AS 'SELECT public.uuid_generate_v4()'",
	).Error; err != nil {
		t.Fatalf("create schema-local uuid function: %v", err)
	}

	db, err := gorm.Open(postgres.Open(databasePostgresDSNWithSearchPath(dsn, schemaName)), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("connect schema-scoped test postgres: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get test sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = sqlDB.Close()
		_ = adminDB.Exec("DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE").Error
		if adminSQL, err := adminDB.DB(); err == nil {
			_ = adminSQL.Close()
		}
	})
	return db
}

func databasePostgresDSNWithSearchPath(dsn string, schemaName string) string {
	searchPath := schemaName
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

func databaseTestLedgerEntry(key string) models.LedgerEntry {
	now := time.Now()
	return models.LedgerEntry{
		ID:             uuid.New(),
		MerchantID:     uuid.New(),
		ChainID:        constants.Ethereum,
		Symbol:         "ETH",
		Decimals:       18,
		EntryType:      models.LedgerEntryTypeAdjustment,
		Account:        models.LedgerAccountMerchantAvailable,
		Direction:      models.LedgerDirectionCredit,
		Status:         models.LedgerStatusPosted,
		AmountRaw:      "1",
		IdempotencyKey: key,
		Reference:      key,
		PostedAt:       &now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

func TestWalletAddressLifecycleSchemaIsRegistered(t *testing.T) {
	for _, want := range []any{
		&models.WalletAddressReservation{},
		&models.WalletAddress{},
		&models.WalletAddressGapScanCursor{},
		&models.WalletAddressGapScanAnomaly{},
	} {
		if !autoMigrateModelsIncludes(want) {
			t.Fatalf("%T must be registered in AutoMigrate models", want)
		}
	}

	requiredColumns := map[string]map[string]bool{
		"wallet_address_reservations": {
			"ID": false, "MerchantID": false, "DomainID": false, "ProductID": false, "UserID": false,
			"HDAccountID": false, "HDAddressID": false, "Purpose": false, "LifecycleStatus": false, "ReusePolicy": false, "WalletID": false,
		},
		"wallet_addresses": {
			"ID": false, "ChainID": false, "ChainName": false, "Address": false, "NormalizedAddress": false,
			"MerchantID": false, "DomainID": false, "WalletID": false, "HDAccountID": false, "HDAddressID": false,
			"Purpose": false, "LifecycleStatus": false, "ReusePolicy": false,
		},
		"wallet_address_gap_scan_cursors": {
			"ID": false, "ChainID": false, "HDAccountID": false, "Purpose": false, "Lookahead": false,
			"LastScannedIndex": false, "HighestUsedIndex": false, "DiscoveredUsedIndexesJSON": false,
		},
		"wallet_address_gap_scan_anomalies": {
			"ID": false, "ChainID": false, "HDAccountID": false, "HDAddressID": false, "Category": false, "DetectedAt": false,
		},
	}
	for _, column := range requiredSchemaColumns() {
		fields, ok := requiredColumns[column.table]
		if !ok {
			continue
		}
		if _, ok := fields[column.field]; ok {
			fields[column.field] = true
		}
	}
	for table, fields := range requiredColumns {
		for field, found := range fields {
			if !found {
				t.Fatalf("VerifySchema does not require %s.%s", table, field)
			}
		}
	}

	requiredIndexes := map[string]map[string]bool{
		"wallet_address_reservations": {
			"ux_wallet_address_reservations_owner": false,
			"ux_wallet_address_reservations_hd":    false,
		},
		"wallet_addresses": {
			"ux_wallet_addresses_chain_address": false,
			"ux_wallet_addresses_hd_chain":      false,
		},
		"wallet_address_gap_scan_cursors": {
			"ux_wallet_address_gap_scan_scope": false,
		},
	}
	for _, index := range requiredSchemaIndexes() {
		indexes, ok := requiredIndexes[index.table]
		if !ok {
			continue
		}
		if _, ok := indexes[index.name]; ok {
			indexes[index.name] = true
		}
	}
	for table, indexes := range requiredIndexes {
		for name, found := range indexes {
			if !found {
				t.Fatalf("VerifySchema does not require %s index %s", table, name)
			}
		}
	}
}

func autoMigrateModelsIncludes(want any) bool {
	wantType := reflect.TypeOf(want)
	for _, model := range autoMigrateModels() {
		if reflect.TypeOf(model) == wantType {
			return true
		}
	}
	return false
}

func ledgerCheckConstraintSpecByName(t *testing.T, name string) checkConstraintSpec {
	t.Helper()
	for _, spec := range ledgerEntryCheckConstraintSpecs() {
		if spec.name == name {
			return spec
		}
	}
	t.Fatalf("ledger check constraint spec %s not found", name)
	return checkConstraintSpec{}
}

func requireConstraintSpecValue(t *testing.T, spec checkConstraintSpec, value string) {
	t.Helper()
	for _, candidate := range spec.values {
		if candidate == value {
			return
		}
	}
	t.Fatalf("constraint %s does not allow %q", spec.name, value)
}
