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

func TestOutboundLifecyclePayloadsIncludeCommonMoneyEventMetadata(t *testing.T) {
	domainID := uuid.New()
	walletID := uuid.New()
	request := models.WithdrawalRequest{
		ID:             uuid.New(),
		MerchantID:     uuid.New(),
		DomainID:       &domainID,
		WalletID:       walletID,
		Chain:          "ethereum",
		Symbol:         "ETH",
		AmountRaw:      "10",
		ToAddress:      "0xto",
		Status:         models.WithdrawalStatusProcessing,
		TxHash:         "0xtx",
		IdempotencyKey: "payout-key",
		CorrelationID:  "corr-payout",
	}
	payout := NewPayoutPayload(constants.WebhookEventPayoutBroadcastV1, request)
	if payout.OccurredAt == "" || payout.ResourceType != EntityTypePayout || payout.ResourceID != request.ID.String() || payout.ResourceStatus != request.Status {
		t.Fatalf("payout common resource metadata missing: %#v", payout)
	}
	if payout.IdempotencyKey != "payout-key" || payout.CorrelationID != "corr-payout" || payout.WalletID != walletID.String() {
		t.Fatalf("payout idempotency/correlation/source metadata missing: %#v", payout)
	}

	refundWalletID := uuid.New()
	refund := models.Refund{
		ID:             uuid.New(),
		MerchantID:     uuid.New(),
		DomainID:       uuid.New(),
		PaymentID:      uuid.New(),
		WalletID:       &refundWalletID,
		Chain:          "ethereum",
		Symbol:         "ETH",
		AmountRaw:      "10",
		ToAddress:      "0xrefund",
		Status:         models.RefundStatusProcessing,
		TxHash:         "0xrefundtx",
		IdempotencyKey: "refund-key",
		CorrelationID:  "corr-refund",
	}
	refundPayload := NewRefundPayload(constants.WebhookEventRefundBroadcastV1, refund)
	if refundPayload.OccurredAt == "" || refundPayload.ResourceType != EntityTypeRefund || refundPayload.ResourceID != refund.ID.String() || refundPayload.ResourceStatus != refund.Status {
		t.Fatalf("refund common resource metadata missing: %#v", refundPayload)
	}
	if refundPayload.IdempotencyKey != "refund-key" || refundPayload.CorrelationID != "corr-refund" || refundPayload.WalletID != refundWalletID.String() || refundPayload.ToAddress != "0xrefund" {
		t.Fatalf("refund idempotency/correlation/source metadata missing: %#v", refundPayload)
	}
}
