package webhook

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"core/constants"
)

func TestMoneyEventCatalogCoversRequiredCanonicalEvents(t *testing.T) {
	required := []string{
		"deposit.detected.v1",
		"deposit.finalized.v1",
		"transaction.detected.v1",
		"payment.succeeded.v1",
		"payment.failed.v1",
		"payment.expired.v1",
		"payment.underpaid.v1",
		"payment.overpaid.v1",
		"payment.partial_paid.v1",
		"withdrawal.requested.v1",
		"withdrawal.broadcast.v1",
		"withdrawal.finalized.v1",
		"withdrawal.failed.v1",
		"refund.succeeded.v1",
		"sweep.succeeded.v1",
		"transaction.reorged.v1",
		"transaction.restored.v1",
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

func TestMoneyEventCatalogExamplesMatchDeclaredSchemas(t *testing.T) {
	for _, entry := range MoneyEventCatalog() {
		t.Run(entry.Name, func(t *testing.T) {
			requireExampleFields(t, entry, entry.RequiredFields...)
			if got, _ := entry.Example["event_type"].(string); got != entry.Name {
				t.Fatalf("example event_type = %q, want %q", got, entry.Name)
			}
			if got, _ := entry.Example["event_version"].(string); got != constants.WebhookEventVersionV1 {
				t.Fatalf("example event_version = %q, want %q", got, constants.WebhookEventVersionV1)
			}
			if got, _ := entry.Example["resource_type"].(string); got != entry.ResourceType {
				t.Fatalf("example resource_type = %q, want %q", got, entry.ResourceType)
			}
		})
	}
}

func TestMoneyEventCatalogV1FieldSnapshot(t *testing.T) {
	expectedFamilyFields := map[string][]string{
		"deposit.detected.v1":               {"chain_id", "tx_hash", "tx_unique_hash", "log_index", "amount_raw", "symbol", "token", "from_address", "to_address", "confirmations"},
		"deposit.finalized.v1":              {"chain_id", "tx_hash", "tx_unique_hash", "amount_raw", "symbol", "token", "wallet_id"},
		"transaction.detected.v1":           {"transaction_id", "chain_id", "hash", "log_index", "block_number", "block_hash", "amount_raw", "symbol", "token", "from", "to"},
		"payment.succeeded.v1":              {"payment_id", "order_id", "amount", "currency", "tx_hash", "tx_unique_hash"},
		"payment.failed.v1":                 {"payment_id", "order_id", "amount", "currency", "failure_reason"},
		"payment.expired.v1":                {"payment_id", "order_id", "amount", "currency", "expires_at"},
		"payment.underpaid.v1":              {"payment_id", "order_id", "amount", "currency", "expected_amount_raw", "matched_amount_raw", "shortfall_amount_raw", "payment_outcome"},
		"payment.overpaid.v1":               {"payment_id", "order_id", "amount", "currency", "expected_amount_raw", "matched_amount_raw", "excess_amount_raw", "payment_outcome"},
		"payment.partial_paid.v1":           {"payment_id", "order_id", "amount", "currency", "expected_amount_raw", "matched_amount_raw", "shortfall_amount_raw", "payment_outcome"},
		"withdrawal.requested.v1":           {"withdrawal_id", "wallet_id", "chain", "symbol", "token", "amount_raw", "to_address"},
		"withdrawal.broadcast.v1":           {"withdrawal_id", "wallet_id", "chain", "symbol", "token", "amount_raw", "to_address", "tx_hash"},
		"withdrawal.finalized.v1":           {"withdrawal_id", "wallet_id", "chain", "symbol", "token", "amount_raw", "to_address", "tx_hash"},
		"withdrawal.failed.v1":              {"withdrawal_id", "wallet_id", "chain", "symbol", "token", "amount_raw", "to_address", "failure_reason"},
		"refund.requested.v1":               {"refund_id", "payment_id", "amount_raw", "reason"},
		"refund.broadcast.v1":               {"refund_id", "payment_id", "amount_raw", "tx_hash"},
		"refund.succeeded.v1":               {"refund_id", "payment_id", "amount_raw", "tx_hash"},
		"refund.rejected.v1":                {"refund_id", "payment_id", "amount_raw", "reason"},
		"refund.failed.v1":                  {"refund_id", "payment_id", "amount_raw", "failure_reason"},
		"sweep.requested.v1":                {"sweep_id", "wallet_id", "chain_id", "amount_raw"},
		"sweep.succeeded.v1":                {"sweep_id", "wallet_id", "chain_id", "amount_raw", "sweep_tx_hash"},
		"sweep.failed.v1":                   {"sweep_id", "wallet_id", "chain_id", "failure_reason"},
		"sweep.dead_lettered.v1":            {"sweep_id", "wallet_id", "chain_id", "failure_reason", "operator_action"},
		"transaction.reorged.v1":            {"transaction_id", "tx_unique_hash", "original_event_id", "original_resource_id", "correction_reason"},
		"transaction.restored.v1":           {"transaction_id", "tx_unique_hash", "reorg_event_id", "canonical_block_number", "canonical_block_hash", "restoration_reason"},
		"webhook.delivery.succeeded.v1":     {"delivery_id", "target_url", "attempts"},
		"webhook.delivery.failed.v1":        {"delivery_id", "attempts", "failure_reason", "next_retry_at"},
		"webhook.delivery.dead_lettered.v1": {"delivery_id", "attempts", "failure_reason", "operator_action"},
		"webhook.delivery.replayed.v1":      {"delivery_id", "original_event_id", "replay_reason"},
	}

	catalog := MoneyEventCatalog()
	if len(catalog) != len(expectedFamilyFields) {
		t.Fatalf("catalog entries = %d, snapshot entries = %d", len(catalog), len(expectedFamilyFields))
	}
	commonFields := CommonMoneyEventFields()
	for _, entry := range catalog {
		t.Run(entry.Name, func(t *testing.T) {
			wantFamilyFields, ok := expectedFamilyFields[entry.Name]
			if !ok {
				t.Fatalf("new v1 event %q must be added to compatibility snapshot", entry.Name)
			}
			requireExactFields(t, entry.FamilyFields, wantFamilyFields...)
			requireExactFields(t, entry.RequiredFields, append(cloneStrings(commonFields), wantFamilyFields...)...)
		})
	}
}

func TestMoneyEventCatalogCoversCurrentWebhookConstants(t *testing.T) {
	currentEvents := webhookEventConstantsFromSource(t)

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

func TestPaymentMismatchWebhookConstantsEmitCanonicalVersionedEvents(t *testing.T) {
	for _, eventName := range []string{
		constants.WebhookEventPaymentUnderpaid,
		constants.WebhookEventPaymentOverpaid,
		constants.WebhookEventPaymentPartialPaid,
	} {
		entry, relation, ok := MoneyEventCatalogEntryForEmittedEvent(eventName)
		if !ok {
			t.Fatalf("mismatch event %q is missing from catalog", eventName)
		}
		if relation != EventRelationCanonical || entry.Name != eventName {
			t.Fatalf("mismatch event %q maps to %q relation=%q, want canonical self", eventName, entry.Name, relation)
		}
	}
}

func webhookEventConstantsFromSource(t *testing.T) []string {
	t.Helper()
	contentBytes, err := os.ReadFile("../../constants/webhook_events.go")
	if err != nil {
		t.Fatalf("read webhook event constants: %v", err)
	}
	re := regexp.MustCompile(`(?m)^\s*(WebhookEvent[A-Za-z0-9]+)\s*=\s*"([^"]+)"`)
	matches := re.FindAllStringSubmatch(string(contentBytes), -1)
	if len(matches) == 0 {
		t.Fatal("no webhook event constants found")
	}
	events := make([]string, 0, len(matches))
	for _, match := range matches {
		if match[1] == "WebhookEventVersionV1" {
			continue
		}
		events = append(events, match[2])
	}
	if len(events) == 0 {
		t.Fatal("no webhook event type constants found")
	}
	return events
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
		"repositories/payment_repo.go":      {constants.WebhookEventPaymentSucceeded, constants.WebhookEventPaymentFailed, constants.WebhookEventPaymentExpired, constants.WebhookEventPaymentUnderpaid, constants.WebhookEventPaymentOverpaid, constants.WebhookEventPaymentPartialPaid},
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
		if !strings.HasSuffix(entry.Name, "."+constants.WebhookEventVersionV1) || strings.Count(entry.Name, ".") < 2 {
			t.Fatalf("alias target %s is not a versioned canonical event", entry.Name)
		}
		for _, alias := range entry.Aliases {
			if strings.TrimSpace(alias.Name) == "" || strings.TrimSpace(alias.Relation) == "" || strings.TrimSpace(alias.Note) == "" {
				t.Fatalf("entry %s has incomplete alias migration metadata: %#v", entry.Name, alias)
			}
			if alias.Relation == EventRelationCanonical {
				t.Fatalf("alias %s on %s must not be marked canonical", alias.Name, entry.Name)
			}
			resolved, relation, ok := MoneyEventCatalogEntryForEmittedEvent(alias.Name)
			if !ok {
				t.Fatalf("alias %s on %s does not resolve through emitted-event lookup", alias.Name, entry.Name)
			}
			if resolved.Name != entry.Name || relation != alias.Relation {
				t.Fatalf("alias %s resolved to %s/%s, want %s/%s", alias.Name, resolved.Name, relation, entry.Name, alias.Relation)
			}
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

func requireExactFields(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("fields = %v, want %v", got, want)
		}
	}
}
