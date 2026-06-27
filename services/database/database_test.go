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
