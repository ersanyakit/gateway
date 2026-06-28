package database

import (
	"reflect"
	"testing"

	"core/models"
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
		"PrefundAttempts":        false,
		"PrefundLastError":       false,
		"PrefundFailureCategory": false,
		"PrefundLastAttemptAt":   false,
		"PrefundedAt":            false,
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
		"idx_sweep_jobs_failure_category":         false,
		"idx_sweep_jobs_prefund_failure_category": false,
		"idx_sweep_jobs_prefund_last_attempt_at":  false,
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

func TestApplyGORMMigrationsEntrypointExists(t *testing.T) {
	if reflect.ValueOf(ApplyGORMMigrations).IsNil() {
		t.Fatal("ApplyGORMMigrations must be available for GORM-managed migration jobs")
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
