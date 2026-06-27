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
		"&models.ChainFact{}",
		"&models.Deposit{}",
		"money_event_outboxes",
		"webhook_deliveries",
		"chain_facts",
		"deposits",
		"ledger_entries",
		"EventID",
		"IdempotencyKey",
		"PayloadJSON",
		"BlockNumber",
		"TxHash",
		"LogIndex",
		"ObservedAddress",
		"AmountRaw",
		"SourceEventType",
		"RawMetadataJSON",
		"ChainFactID",
		"ChainFactEventID",
		"WalletID",
		"MerchantID",
		"DomainID",
		"ProductID",
		"UserID",
		"TransactionUniqueHash",
		"Direction",
		"Token",
		"Symbol",
		"Decimals",
		"Confirmations",
		"DetectedAt",
		"UnmatchedReason",
		"FinalizedAt",
		"EntryType",
		"Account",
		"Direction",
		"PostedAt",
		"VoidedAt",
		"ux_ledger_idempotent_account",
		"ledger_entries_entry_type_check",
		"ledger_entries_account_check",
		"ledger_entries_direction_check",
		"ledger_entries_status_check",
		"FailureCategory",
		"OriginalDeliveryID",
		"ReplayCount",
		"ReplayRequestedBy",
		"ReplayRequestedAt",
		"OperatorAction",
	} {
		if !strings.Contains(source, token) {
			t.Fatalf("database schema registration missing %q", token)
		}
	}
}
