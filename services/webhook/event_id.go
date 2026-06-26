package webhook

import (
	"strings"

	"core/models"
)

func TransactionEventID(tx models.Transaction) string {
	eventType := strings.TrimSpace(tx.EventType)
	if eventType == "" {
		eventType = "transaction"
	}
	return strings.TrimSpace(tx.UniqueHash) + ":" + eventType
}

func PaymentEventID(session models.PaymentSession) string {
	eventType := strings.TrimSpace(session.WebhookEvent)
	if eventType == "" {
		eventType = "payment"
	}
	return session.ID.String() + ":" + eventType
}
