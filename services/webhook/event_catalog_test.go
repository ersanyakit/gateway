package webhook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"core/constants"
)

func TestMoneyEventCatalogCoversRequiredCanonicalEvents(t *testing.T) {
	required := []string{
		"deposit.detected.v1",
		"deposit.finalized.v1",
		"payment.succeeded.v1",
		"payment.failed.v1",
		"payment.expired.v1",
		"withdrawal.requested.v1",
		"withdrawal.broadcast.v1",
		"withdrawal.finalized.v1",
		"withdrawal.failed.v1",
		"refund.succeeded.v1",
		"sweep.succeeded.v1",
		"transaction.reorged.v1",
	}
	commonFields := CommonMoneyEventFields()

	for _, eventName := range required {
		t.Run(eventName, func(t *testing.T) {
			entry, ok := MoneyEventCatalogEntry(eventName)
			if !ok {
				t.Fatalf("catalog missing canonical event %q", eventName)
			}
			if entry.Name != eventName || entry.Version != constants.WebhookEventVersionV1 {
				t.Fatalf("entry identity = %#v", entry)
			}
			if entry.Family == "" || entry.Producer == "" || len(entry.Consumers) == 0 || entry.ResourceType == "" || entry.Lifecycle == "" {
				t.Fatalf("entry lacks required metadata: %#v", entry)
			}
			requireFields(t, entry.RequiredFields, commonFields...)
			requireExampleFields(t, entry, commonFields...)
		})
	}
}

func TestMoneyEventCatalogCoversCurrentWebhookConstants(t *testing.T) {
	currentEvents := []string{
		constants.WebhookEventNativeTransfer,
		constants.WebhookEventTransactionReorged,
		constants.WebhookEventPaymentSucceeded,
		constants.WebhookEventPaymentFailed,
		constants.WebhookEventPaymentExpired,
		constants.WebhookEventPayoutRequestedV1,
		constants.WebhookEventPayoutBroadcastV1,
		constants.WebhookEventPayoutFinalizedV1,
		constants.WebhookEventPayoutRejectedV1,
		constants.WebhookEventPayoutFailedV1,
		constants.WebhookEventRefundRequestedV1,
		constants.WebhookEventRefundBroadcastV1,
		constants.WebhookEventRefundSucceededV1,
		constants.WebhookEventRefundRejectedV1,
		constants.WebhookEventRefundFailedV1,
		constants.WebhookEventSweepRequestedV1,
		constants.WebhookEventSweepSucceededV1,
		constants.WebhookEventSweepFailedV1,
		constants.WebhookEventSweepDeadLetteredV1,
	}

	for _, eventName := range currentEvents {
		t.Run(eventName, func(t *testing.T) {
			entry, relation, ok := MoneyEventCatalogEntryForEmittedEvent(eventName)
			if !ok {
				t.Fatalf("current emitted event %q is missing from catalog", eventName)
			}
			if relation == "" {
				t.Fatalf("catalog relation missing for emitted event %q in %#v", eventName, entry)
			}
			if strings.HasPrefix(eventName, "payout.") && !strings.HasPrefix(entry.Name, "withdrawal.") {
				t.Fatalf("payout compatibility event %q should map to withdrawal canonical entry, got %q", eventName, entry.Name)
			}
		})
	}
}

func TestMoneyEventCatalogExamplesExcludeSensitiveFields(t *testing.T) {
	for _, entry := range MoneyEventCatalog() {
		t.Run(entry.Name, func(t *testing.T) {
			for _, forbidden := range []string{"api_secret", "webhook_secret", "private_key", "mnemonic", "raw_signature", "stack_trace", "diagnostics"} {
				if _, exists := entry.Example[forbidden]; exists {
					t.Fatalf("catalog example for %s exposes forbidden field %q", entry.Name, forbidden)
				}
			}
		})
	}
}

func TestMoneyEventCatalogCoversRawEventLiteralsInMoneyPaths(t *testing.T) {
	sourceFiles := []string{
		"../../api/handlers/payment.go",
		"../../repositories/payment_repo.go",
		"../../repositories/transaction_repo.go",
		"../../services/txrescan/service.go",
		"../../workers/listeners/evm/listener.go",
		"../../workers/listeners/tron/tron.go",
	}
	eventsBySource := map[string][]string{
		"api/handlers/payment.go":           {constants.WebhookEventPaymentFailed},
		"repositories/payment_repo.go":      {constants.WebhookEventPaymentSucceeded, constants.WebhookEventPaymentFailed, constants.WebhookEventPaymentExpired},
		"repositories/transaction_repo.go":  {constants.WebhookEventTransactionReorged},
		"services/txrescan/service.go":      {constants.WebhookEventNativeTransfer},
		"workers/listeners/evm/listener.go": {constants.WebhookEventNativeTransfer},
		"workers/listeners/tron/tron.go":    {constants.WebhookEventNativeTransfer},
	}

	for _, sourceFile := range sourceFiles {
		contentBytes, err := os.ReadFile(sourceFile)
		if err != nil {
			t.Fatalf("read source file %s: %v", sourceFile, err)
		}
		content := string(contentBytes)
		normalized := filepath.ToSlash(strings.TrimPrefix(sourceFile, "../../"))
		for _, eventName := range eventsBySource[normalized] {
			if !strings.Contains(content, `"`+eventName+`"`) {
				continue
			}
			if _, relation, ok := MoneyEventCatalogEntryForEmittedEvent(eventName); !ok || relation == "" {
				t.Fatalf("raw emitted event %q in %s is missing from catalog", eventName, normalized)
			}
		}
	}
}

func TestMoneyEventCatalogDocumentsAliasDeprecationNotes(t *testing.T) {
	for _, entry := range MoneyEventCatalog() {
		if len(entry.Aliases) == 0 {
			continue
		}
		if strings.TrimSpace(entry.DeprecationNote) == "" {
			t.Fatalf("entry %s has aliases without deprecation/migration note", entry.Name)
		}
	}
}

func TestMoneyEventCatalogDocumentsCorrectionSemantics(t *testing.T) {
	entry, ok := MoneyEventCatalogEntry("transaction.reorged.v1")
	if !ok {
		t.Fatal("transaction.reorged.v1 missing")
	}
	if entry.Correction == nil {
		t.Fatalf("transaction.reorged.v1 must define correction semantics: %#v", entry)
	}
	if entry.Correction.OriginalEventIDField == "" || entry.Correction.OriginalResourceIDField == "" {
		t.Fatalf("correction relation fields missing: %#v", entry.Correction)
	}
	if !strings.Contains(strings.ToLower(entry.Correction.Semantics), "non-destructive") {
		t.Fatalf("correction semantics must state non-destructive history behavior: %#v", entry.Correction)
	}
}

func requireFields(t *testing.T, fields []string, required ...string) {
	t.Helper()
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		seen[field] = struct{}{}
	}
	for _, field := range required {
		if _, ok := seen[field]; !ok {
			t.Fatalf("required field %q missing from %v", field, fields)
		}
	}
}

func requireExampleFields(t *testing.T, entry MoneyEventCatalogItem, required ...string) {
	t.Helper()
	for _, field := range required {
		if _, ok := entry.Example[field]; !ok {
			t.Fatalf("example for %s missing %q: %#v", entry.Name, field, entry.Example)
		}
	}
}
