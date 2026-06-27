package database

import (
	"os"
	"strings"
	"testing"
)

func TestDatabaseMigrationRegistersMoneyEventOutbox(t *testing.T) {
	sourceBytes, err := os.ReadFile("database.go")
	if err != nil {
		t.Fatalf("read database.go: %v", err)
	}
	source := string(sourceBytes)
	for _, token := range []string{
		"&models.MoneyEventOutbox{}",
		"&models.WebhookDelivery{}",
		"money_event_outboxes",
		"webhook_deliveries",
		"EventID",
		"IdempotencyKey",
		"PayloadJSON",
		"FailureCategory",
	} {
		if !strings.Contains(source, token) {
			t.Fatalf("database schema registration missing %q", token)
		}
	}
}
