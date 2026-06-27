package webhook

import (
	"strings"

	"core/constants"
)

const (
	EventRelationCanonical    = "canonical"
	EventRelationCurrentAlias = "current-alias"
	EventRelationLegacyAlias  = "legacy-alias"
	EventRelationLegacyOnly   = "legacy-only"
)

type MoneyEventAlias struct {
	Name     string
	Relation string
	Note     string
}

type MoneyEventCorrection struct {
	OriginalEventIDField    string
	OriginalResourceIDField string
	Semantics               string
}

type MoneyEventCatalogItem struct {
	Name            string
	Version         string
	Family          string
	Producer        string
	Consumers       []string
	ResourceType    string
	Terminal        bool
	Lifecycle       string
	RequiredFields  []string
	FamilyFields    []string
	Aliases         []MoneyEventAlias
	DeprecationNote string
	Correction      *MoneyEventCorrection
	Example         map[string]any
}

var commonMoneyEventFields = []string{
	"event_id",
	"event_type",
	"event_version",
	"occurred_at",
	"merchant_id",
	"domain_id",
	"resource_type",
	"resource_id",
	"resource_status",
	"idempotency_key",
	"correlation_id",
}

func CommonMoneyEventFields() []string {
	return cloneStrings(commonMoneyEventFields)
}

func MoneyEventCatalog() []MoneyEventCatalogItem {
	return cloneCatalog(moneyEventCatalog)
}

func MoneyEventCatalogEntry(name string) (MoneyEventCatalogItem, bool) {
	name = strings.TrimSpace(name)
	for _, entry := range moneyEventCatalog {
		if entry.Name == name {
			return cloneCatalogItem(entry), true
		}
	}
	return MoneyEventCatalogItem{}, false
}

func MoneyEventCatalogEntryForEmittedEvent(name string) (MoneyEventCatalogItem, string, bool) {
	name = strings.TrimSpace(name)
	for _, entry := range moneyEventCatalog {
		if entry.Name == name {
			return cloneCatalogItem(entry), EventRelationCanonical, true
		}
		for _, alias := range entry.Aliases {
			if alias.Name == name {
				return cloneCatalogItem(entry), alias.Relation, true
			}
		}
	}
	return MoneyEventCatalogItem{}, "", false
}

var moneyEventCatalog = []MoneyEventCatalogItem{
	catalogItem("deposit.detected.v1", "deposit", "chain_indexer", "deposit", "Deposit observed on-chain before finality.", false, []string{
		"chain_id", "tx_hash", "tx_unique_hash", "log_index", "amount_raw", "symbol", "token", "from_address", "to_address", "confirmations",
	}, []MoneyEventAlias{
		{Name: constants.WebhookEventNativeTransfer, Relation: EventRelationLegacyAlias, Note: "Current transaction webhook name for native/token transfer detection."},
	}, nil),
	catalogItem("deposit.finalized.v1", "deposit", "deposit", "deposit", "Deposit reached required finality and can be matched to wallet/payment state.", true, []string{
		"chain_id", "tx_hash", "tx_unique_hash", "amount_raw", "symbol", "token", "wallet_id",
	}, nil, nil),
	catalogItem("payment.succeeded.v1", "payment", "payment", "payment", "Payment reached a successful terminal state.", true, []string{
		"payment_id", "order_id", "amount", "currency", "tx_hash", "tx_unique_hash",
	}, []MoneyEventAlias{
		{Name: constants.WebhookEventPaymentSucceeded, Relation: EventRelationLegacyAlias, Note: "Current payment webhook name retained for existing integrations."},
	}, nil),
	catalogItem("payment.failed.v1", "payment", "payment", "payment", "Payment reached a failed terminal state or was corrected after settlement uncertainty.", true, []string{
		"payment_id", "order_id", "amount", "currency", "failure_reason",
	}, []MoneyEventAlias{
		{Name: constants.WebhookEventPaymentFailed, Relation: EventRelationLegacyAlias, Note: "Current payment webhook name retained for existing integrations."},
	}, nil),
	catalogItem("payment.expired.v1", "payment", "payment", "payment", "Payment checkout expired before successful settlement.", true, []string{
		"payment_id", "order_id", "amount", "currency", "expires_at",
	}, []MoneyEventAlias{
		{Name: constants.WebhookEventPaymentExpired, Relation: EventRelationLegacyAlias, Note: "Current payment webhook name retained for existing integrations."},
	}, nil),
	catalogItem("payment.underpaid.v1", "payment", "payment", "payment", "Payment received less than the expected amount and needs merchant/operator follow-up.", true, []string{
		"payment_id", "order_id", "amount", "currency", "expected_amount_raw", "matched_amount_raw", "shortfall_amount_raw", "payment_outcome",
	}, []MoneyEventAlias{
		{Name: constants.WebhookEventPaymentUnderpaid, Relation: EventRelationLegacyAlias, Note: "Current payment webhook name for explicit underpayment outcome."},
	}, nil),
	catalogItem("payment.overpaid.v1", "payment", "payment", "payment", "Payment received more than the expected amount and may require refund or reconciliation.", true, []string{
		"payment_id", "order_id", "amount", "currency", "expected_amount_raw", "matched_amount_raw", "excess_amount_raw", "payment_outcome",
	}, []MoneyEventAlias{
		{Name: constants.WebhookEventPaymentOverpaid, Relation: EventRelationLegacyAlias, Note: "Current payment webhook name for explicit overpayment outcome."},
	}, nil),
	catalogItem("payment.partial_paid.v1", "payment", "payment", "payment", "Partial deposit received; automatic checkout aggregation is not supported.", true, []string{
		"payment_id", "order_id", "amount", "currency", "expected_amount_raw", "matched_amount_raw", "shortfall_amount_raw", "payment_outcome",
	}, []MoneyEventAlias{
		{Name: constants.WebhookEventPaymentPartialPaid, Relation: EventRelationLegacyAlias, Note: "Current payment webhook name for explicit partial payment outcome."},
	}, nil),
	catalogItem("withdrawal.requested.v1", "withdrawal", "withdrawal", "withdrawal", "Withdrawal/payout request was created and awaits review or policy checks.", false, []string{
		"withdrawal_id", "wallet_id", "chain", "symbol", "token", "amount_raw", "to_address",
	}, []MoneyEventAlias{
		{Name: constants.WebhookEventPayoutRequestedV1, Relation: EventRelationCurrentAlias, Note: "Current implementation uses payout naming for withdrawal requests."},
	}, nil),
	catalogItem("withdrawal.broadcast.v1", "withdrawal", "withdrawal", "withdrawal", "Withdrawal transaction was broadcast and awaits finality.", false, []string{
		"withdrawal_id", "wallet_id", "chain", "symbol", "token", "amount_raw", "to_address", "tx_hash",
	}, []MoneyEventAlias{
		{Name: constants.WebhookEventPayoutBroadcastV1, Relation: EventRelationCurrentAlias, Note: "Current implementation uses payout naming for withdrawal broadcast."},
	}, nil),
	catalogItem("withdrawal.finalized.v1", "withdrawal", "withdrawal", "withdrawal", "Withdrawal completed on-chain and ledger lifecycle can finalize.", true, []string{
		"withdrawal_id", "wallet_id", "chain", "symbol", "token", "amount_raw", "to_address", "tx_hash",
	}, []MoneyEventAlias{
		{Name: constants.WebhookEventPayoutFinalizedV1, Relation: EventRelationCurrentAlias, Note: "Current implementation uses payout naming for withdrawal finalization."},
	}, nil),
	catalogItem("withdrawal.failed.v1", "withdrawal", "withdrawal", "withdrawal", "Withdrawal failed, was rejected, or cannot continue without operator intervention.", true, []string{
		"withdrawal_id", "wallet_id", "chain", "symbol", "token", "amount_raw", "to_address", "failure_reason",
	}, []MoneyEventAlias{
		{Name: constants.WebhookEventPayoutFailedV1, Relation: EventRelationCurrentAlias, Note: "Current implementation uses payout naming for withdrawal failure."},
		{Name: constants.WebhookEventPayoutRejectedV1, Relation: EventRelationCurrentAlias, Note: "Current implementation uses payout.rejected.v1 for rejected withdrawal requests."},
	}, nil),
	catalogItem("refund.requested.v1", "refund", "refund", "refund", "Refund request was created and awaits review or policy checks.", false, []string{
		"refund_id", "payment_id", "amount_raw", "reason",
	}, nil, nil),
	catalogItem("refund.broadcast.v1", "refund", "refund", "refund", "Refund transaction was broadcast and awaits finality.", false, []string{
		"refund_id", "payment_id", "amount_raw", "tx_hash",
	}, nil, nil),
	catalogItem("refund.succeeded.v1", "refund", "refund", "refund", "Refund completed successfully and refundable hold/debit lifecycle is terminal.", true, []string{
		"refund_id", "payment_id", "amount_raw", "tx_hash",
	}, nil, nil),
	catalogItem("refund.rejected.v1", "refund", "refund", "refund", "Refund was rejected by policy or admin review.", true, []string{
		"refund_id", "payment_id", "amount_raw", "reason",
	}, nil, nil),
	catalogItem("refund.failed.v1", "refund", "refund", "refund", "Refund failed before final completion.", true, []string{
		"refund_id", "payment_id", "amount_raw", "failure_reason",
	}, nil, nil),
	catalogItem("sweep.requested.v1", "sweep", "sweep", "sweep", "Sweep job was requested for wallet or address consolidation.", false, []string{
		"sweep_id", "wallet_id", "chain_id", "amount_raw",
	}, nil, nil),
	catalogItem("sweep.succeeded.v1", "sweep", "sweep", "sweep", "Sweep completed successfully.", true, []string{
		"sweep_id", "wallet_id", "chain_id", "amount_raw", "sweep_tx_hash",
	}, nil, nil),
	catalogItem("sweep.failed.v1", "sweep", "sweep", "sweep", "Sweep failed but may be retried or reconciled.", true, []string{
		"sweep_id", "wallet_id", "chain_id", "failure_reason",
	}, nil, nil),
	catalogItem("sweep.dead_lettered.v1", "sweep", "sweep", "sweep", "Sweep exhausted retry policy and requires operator action.", true, []string{
		"sweep_id", "wallet_id", "chain_id", "failure_reason", "operator_action",
	}, nil, nil),
	catalogItem("transaction.reorged.v1", "correction", "chain_indexer", "transaction", "A previously observed transaction was invalidated or corrected by chain reorg handling.", true, []string{
		"transaction_id", "tx_unique_hash", "original_event_id", "original_resource_id", "correction_reason",
	}, []MoneyEventAlias{
		{Name: constants.WebhookEventTransactionReorged, Relation: EventRelationLegacyAlias, Note: "Current correction webhook name retained for existing integrations."},
	}, &MoneyEventCorrection{
		OriginalEventIDField:    "original_event_id",
		OriginalResourceIDField: "original_resource_id",
		Semantics:               "Correction events are non-destructive: prior event history remains immutable and consumers apply the correction using the original event/resource relation.",
	}),
	catalogItem("webhook.delivery.succeeded.v1", "webhook_delivery", "webhook", "webhook_delivery", "Webhook delivery reached a terminal success state.", true, []string{
		"delivery_id", "target_url", "attempts",
	}, nil, nil),
	catalogItem("webhook.delivery.failed.v1", "webhook_delivery", "webhook", "webhook_delivery", "Webhook delivery attempt failed and remains retryable.", false, []string{
		"delivery_id", "attempts", "failure_reason", "next_retry_at",
	}, nil, nil),
	catalogItem("webhook.delivery.dead_lettered.v1", "webhook_delivery", "webhook", "webhook_delivery", "Webhook delivery exhausted retry policy and needs operator action.", true, []string{
		"delivery_id", "attempts", "failure_reason", "operator_action",
	}, nil, nil),
	catalogItem("webhook.delivery.replayed.v1", "webhook_delivery", "webhook", "webhook_delivery", "Operator replay was requested for an existing event delivery.", false, []string{
		"delivery_id", "original_event_id", "replay_reason",
	}, nil, nil),
}

func catalogItem(name, family, producer, resourceType, lifecycle string, terminal bool, familyFields []string, aliases []MoneyEventAlias, correction *MoneyEventCorrection) MoneyEventCatalogItem {
	fields := append(cloneStrings(commonMoneyEventFields), familyFields...)
	return MoneyEventCatalogItem{
		Name:            name,
		Version:         constants.WebhookEventVersionV1,
		Family:          family,
		Producer:        producer,
		Consumers:       []string{"merchant_webhook_consumer", "exchange_webhook_consumer", "operator_diagnostics"},
		ResourceType:    resourceType,
		Terminal:        terminal,
		Lifecycle:       lifecycle,
		RequiredFields:  fields,
		FamilyFields:    cloneStrings(familyFields),
		Aliases:         cloneAliases(aliases),
		DeprecationNote: deprecationNote(aliases),
		Correction:      correction,
		Example:         eventExample(name, resourceType, lifecycle, fields, correction),
	}
}

func deprecationNote(aliases []MoneyEventAlias) string {
	if len(aliases) == 0 {
		return ""
	}
	for _, alias := range aliases {
		if alias.Relation == EventRelationCurrentAlias {
			return "Current payout.* names remain supported as compatibility aliases until a versioned withdrawal naming migration is published."
		}
	}
	return "Legacy aliases remain supported until a published event catalog migration announces their retirement."
}

func eventExample(name, resourceType, lifecycle string, fields []string, correction *MoneyEventCorrection) map[string]any {
	example := map[string]any{
		"event_id":        resourceType + "_uuid:" + name,
		"event_type":      name,
		"event_version":   constants.WebhookEventVersionV1,
		"occurred_at":     "2026-06-27T12:00:00Z",
		"merchant_id":     "merchant_uuid",
		"domain_id":       "domain_uuid",
		"resource_type":   resourceType,
		"resource_id":     resourceType + "_uuid",
		"resource_status": lifecycleStatus(name),
		"idempotency_key": resourceType + "_uuid:" + name,
		"correlation_id":  "corr_" + resourceType + "_uuid",
	}
	for _, field := range fields {
		if _, ok := example[field]; ok {
			continue
		}
		example[field] = exampleValue(field)
	}
	if correction != nil {
		example[correction.OriginalEventIDField] = "original_event_id"
		example[correction.OriginalResourceIDField] = "original_resource_id"
		example["correction_reason"] = "chain_reorg"
	}
	return example
}

func lifecycleStatus(name string) string {
	parts := strings.Split(name, ".")
	if len(parts) >= 2 {
		return parts[len(parts)-2]
	}
	return "unknown"
}

func exampleValue(field string) any {
	switch field {
	case "chain_id":
		return int64(1)
	case "confirmations", "attempts":
		return 1
	case "amount", "amount_raw":
		return "25000000"
	case "token":
		return "0xdAC17F958D2ee523a2206206994597C13D831ec7"
	case "next_retry_at", "expires_at":
		return "2026-06-27T12:05:00Z"
	case "target_url":
		return "https://merchant.example.com/webhook"
	default:
		return field + "_value"
	}
}

func cloneCatalog(items []MoneyEventCatalogItem) []MoneyEventCatalogItem {
	out := make([]MoneyEventCatalogItem, 0, len(items))
	for _, item := range items {
		out = append(out, cloneCatalogItem(item))
	}
	return out
}

func cloneCatalogItem(item MoneyEventCatalogItem) MoneyEventCatalogItem {
	item.Consumers = cloneStrings(item.Consumers)
	item.RequiredFields = cloneStrings(item.RequiredFields)
	item.FamilyFields = cloneStrings(item.FamilyFields)
	item.Aliases = cloneAliases(item.Aliases)
	if item.Example != nil {
		example := make(map[string]any, len(item.Example))
		for k, v := range item.Example {
			example[k] = v
		}
		item.Example = example
	}
	if item.Correction != nil {
		correction := *item.Correction
		item.Correction = &correction
	}
	return item
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func cloneAliases(values []MoneyEventAlias) []MoneyEventAlias {
	if values == nil {
		return nil
	}
	out := make([]MoneyEventAlias, len(values))
	copy(out, values)
	return out
}
