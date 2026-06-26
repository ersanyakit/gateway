package webhook

import (
	"testing"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
)

func TestNewPayoutPayloadUsesStableEventID(t *testing.T) {
	id := uuid.New()
	domainID := uuid.New()
	request := models.WithdrawalRequest{
		ID:         id,
		MerchantID: uuid.New(),
		DomainID:   &domainID,
		WalletID:   uuid.New(),
		Chain:      "ethereum",
		Symbol:     "USDT",
		AmountRaw:  "1000000",
		ToAddress:  "0xrecipient",
		Status:     models.WithdrawalStatusApproved,
		TxHash:     "0xtx",
	}

	payload := NewPayoutPayload(constants.WebhookEventPayoutFinalizedV1, request)

	if payload.EventID != id.String()+":"+constants.WebhookEventPayoutFinalizedV1 {
		t.Fatalf("event id = %q", payload.EventID)
	}
	if payload.EventVersion != constants.WebhookEventVersionV1 {
		t.Fatalf("event version = %q", payload.EventVersion)
	}
	if payload.EntityType != EntityTypePayout || payload.EntityID != id.String() {
		t.Fatalf("entity = %s/%s", payload.EntityType, payload.EntityID)
	}
	if payload.DomainID != domainID.String() {
		t.Fatalf("domain id = %q", payload.DomainID)
	}
}
