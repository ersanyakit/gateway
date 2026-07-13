package webhook

import (
	"encoding/json"
	"fmt"
	"time"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
)

const (
	EntityTypePayout = "payout"
	EntityTypeRefund = "refund"
	EntityTypeSweep  = "sweep"
)

type LifecyclePayload struct {
	EventID        string `json:"event_id"`
	EventType      string `json:"event_type"`
	EventVersion   string `json:"event_version"`
	OccurredAt     string `json:"occurred_at,omitempty"`
	EntityType     string `json:"entity_type"`
	EntityID       string `json:"entity_id"`
	ResourceType   string `json:"resource_type,omitempty"`
	ResourceID     string `json:"resource_id,omitempty"`
	ResourceStatus string `json:"resource_status,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	CorrelationID  string `json:"correlation_id,omitempty"`

	MerchantID string `json:"merchant_id"`
	DomainID   string `json:"domain_id,omitempty"`
	WalletID   string `json:"wallet_id,omitempty"`
	PaymentID  string `json:"payment_id,omitempty"`

	ChainID  int64   `json:"chain_id,omitempty"`
	Chain    string  `json:"chain,omitempty"`
	Token    *string `json:"token,omitempty"`
	Symbol   string  `json:"symbol,omitempty"`
	Decimals uint8   `json:"decimals,omitempty"`

	AmountRaw string `json:"amount_raw,omitempty"`
	ToAddress string `json:"to_address,omitempty"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
	Error     string `json:"error,omitempty"`

	TxHash                string `json:"tx_hash,omitempty"`
	TransactionHash       string `json:"transaction_hash,omitempty"`
	TransactionUniqueHash string `json:"transaction_unique_hash,omitempty"`
	SweepTxHash           string `json:"sweep_tx_hash,omitempty"`

	RequestedBy   string  `json:"requested_by,omitempty"`
	ReviewedBy    string  `json:"reviewed_by,omitempty"`
	ReviewedAt    *string `json:"reviewed_at,omitempty"`
	BroadcastedAt *string `json:"broadcasted_at,omitempty"`
	FinalizedAt   *string `json:"finalized_at,omitempty"`
	CreatedAt     string  `json:"created_at,omitempty"`
	UpdatedAt     string  `json:"updated_at,omitempty"`
}

func (p LifecyclePayload) Body() ([]byte, error) {
	return json.Marshal(p)
}

func (p LifecyclePayload) EntityUUID() *uuid.UUID {
	id, err := uuid.Parse(p.EntityID)
	if err != nil {
		return nil
	}
	return &id
}

func lifecycleEventID(entityID uuid.UUID, eventType string) string {
	return fmt.Sprintf("%s:%s", entityID.String(), eventType)
}

func lifecycleOccurredAt() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func lifecycleFallback(value string, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func formatTimePtr(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339Nano)
	return &formatted
}

func NewPayoutPayload(eventType string, request models.WithdrawalRequest) LifecyclePayload {
	eventID := lifecycleEventID(request.ID, eventType)
	payload := LifecyclePayload{
		EventID:        eventID,
		EventType:      eventType,
		EventVersion:   constants.WebhookEventVersionV1,
		OccurredAt:     lifecycleOccurredAt(),
		EntityType:     EntityTypePayout,
		EntityID:       request.ID.String(),
		ResourceType:   EntityTypePayout,
		ResourceID:     request.ID.String(),
		ResourceStatus: request.Status,
		IdempotencyKey: lifecycleFallback(request.IdempotencyKey, eventID),
		CorrelationID:  lifecycleFallback(request.CorrelationID, "payout:"+request.ID.String()),
		MerchantID:     request.MerchantID.String(),
		WalletID:       request.WalletID.String(),
		Chain:          request.Chain,
		Token:          request.Token,
		Symbol:         request.Symbol,
		Decimals:       request.Decimals,
		AmountRaw:      request.AmountRaw,
		ToAddress:      request.ToAddress,
		Status:         request.Status,
		Error:          request.Error,
		TxHash:         request.TxHash,
		RequestedBy:    request.RequestedBy,
		ReviewedBy:     request.ReviewedBy,
		ReviewedAt:     formatTimePtr(request.ReviewedAt),
		BroadcastedAt:  formatTimePtr(request.BroadcastedAt),
		FinalizedAt:    formatTimePtr(request.FinalizedAt),
		CreatedAt:      request.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:      request.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if request.DomainID != nil {
		payload.DomainID = request.DomainID.String()
	}
	return payload
}

func NewRefundPayload(eventType string, refund models.Refund) LifecyclePayload {
	eventID := lifecycleEventID(refund.ID, eventType)
	walletID := ""
	if refund.WalletID != nil {
		walletID = refund.WalletID.String()
	}
	return LifecyclePayload{
		EventID:        eventID,
		EventType:      eventType,
		EventVersion:   constants.WebhookEventVersionV1,
		OccurredAt:     lifecycleOccurredAt(),
		EntityType:     EntityTypeRefund,
		EntityID:       refund.ID.String(),
		ResourceType:   EntityTypeRefund,
		ResourceID:     refund.ID.String(),
		ResourceStatus: refund.Status,
		IdempotencyKey: lifecycleFallback(refund.IdempotencyKey, eventID),
		CorrelationID:  lifecycleFallback(refund.CorrelationID, "refund:"+refund.ID.String()),
		MerchantID:     refund.MerchantID.String(),
		DomainID:       refund.DomainID.String(),
		WalletID:       walletID,
		PaymentID:      refund.PaymentID.String(),
		Chain:          refund.Chain,
		Token:          refund.Token,
		Symbol:         refund.Symbol,
		Decimals:       refund.Decimals,
		AmountRaw:      refund.AmountRaw,
		ToAddress:      refund.ToAddress,
		Status:         refund.Status,
		Reason:         refund.Reason,
		Error:          refund.Error,
		TxHash:         refund.TxHash,
		RequestedBy:    refund.RequestedBy,
		ReviewedBy:     refund.ReviewedBy,
		ReviewedAt:     formatTimePtr(refund.ReviewedAt),
		BroadcastedAt:  formatTimePtr(refund.BroadcastedAt),
		FinalizedAt:    formatTimePtr(refund.FinalizedAt),
		CreatedAt:      refund.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:      refund.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func NewSweepPayload(eventType string, job models.SweepJob, txModel *models.Transaction, errText string) LifecyclePayload {
	eventID := lifecycleEventID(job.ID, eventType)
	payload := LifecyclePayload{
		EventID:               eventID,
		EventType:             eventType,
		EventVersion:          constants.WebhookEventVersionV1,
		OccurredAt:            lifecycleOccurredAt(),
		EntityType:            EntityTypeSweep,
		EntityID:              job.ID.String(),
		ResourceType:          EntityTypeSweep,
		ResourceID:            job.ID.String(),
		ResourceStatus:        job.Status,
		IdempotencyKey:        eventID,
		CorrelationID:         "sweep:" + job.ID.String(),
		MerchantID:            job.MerchantID.String(),
		WalletID:              job.WalletID.String(),
		ChainID:               int64(job.ChainID),
		Token:                 job.Token,
		Status:                job.Status,
		Error:                 errText,
		TransactionHash:       job.TransactionHash,
		TransactionUniqueHash: job.TransactionUniqueHash,
		SweepTxHash:           job.SweepTxHash,
		CreatedAt:             job.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:             job.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if txModel != nil {
		if txModel.DomainID != nil {
			payload.DomainID = txModel.DomainID.String()
		}
		payload.Symbol = txModel.Symbol
		payload.Decimals = txModel.Decimals
		payload.AmountRaw = txModel.Amount
		payload.TransactionHash = txModel.Hash
		payload.TransactionUniqueHash = txModel.UniqueHash
	}
	return payload
}
