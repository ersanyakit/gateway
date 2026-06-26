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
	EventID      string `json:"event_id"`
	EventType    string `json:"event_type"`
	EventVersion string `json:"event_version"`
	EntityType   string `json:"entity_type"`
	EntityID     string `json:"entity_id"`

	MerchantID string `json:"merchant_id"`
	DomainID   string `json:"domain_id,omitempty"`
	WalletID   string `json:"wallet_id,omitempty"`
	PaymentID  string `json:"payment_id,omitempty"`

	ChainID int64   `json:"chain_id,omitempty"`
	Chain   string  `json:"chain,omitempty"`
	Token   *string `json:"token,omitempty"`
	Symbol  string  `json:"symbol,omitempty"`
	Decimals uint8  `json:"decimals,omitempty"`

	AmountRaw string `json:"amount_raw,omitempty"`
	ToAddress string `json:"to_address,omitempty"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
	Error     string `json:"error,omitempty"`

	TxHash                string `json:"tx_hash,omitempty"`
	TransactionHash       string `json:"transaction_hash,omitempty"`
	TransactionUniqueHash string `json:"transaction_unique_hash,omitempty"`
	SweepTxHash           string `json:"sweep_tx_hash,omitempty"`

	RequestedBy string  `json:"requested_by,omitempty"`
	ReviewedBy  string  `json:"reviewed_by,omitempty"`
	ReviewedAt  *string `json:"reviewed_at,omitempty"`
	CreatedAt   string  `json:"created_at,omitempty"`
	UpdatedAt   string  `json:"updated_at,omitempty"`
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

func formatTimePtr(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339Nano)
	return &formatted
}

func NewPayoutPayload(eventType string, request models.WithdrawalRequest) LifecyclePayload {
	payload := LifecyclePayload{
		EventID:      lifecycleEventID(request.ID, eventType),
		EventType:    eventType,
		EventVersion: constants.WebhookEventVersionV1,
		EntityType:   EntityTypePayout,
		EntityID:     request.ID.String(),
		MerchantID:   request.MerchantID.String(),
		WalletID:     request.WalletID.String(),
		Chain:        request.Chain,
		Token:        request.Token,
		Symbol:       request.Symbol,
		Decimals:     request.Decimals,
		AmountRaw:    request.AmountRaw,
		ToAddress:    request.ToAddress,
		Status:       request.Status,
		Error:        request.Error,
		TxHash:       request.TxHash,
		RequestedBy:  request.RequestedBy,
		ReviewedBy:   request.ReviewedBy,
		ReviewedAt:   formatTimePtr(request.ReviewedAt),
		CreatedAt:    request.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:    request.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if request.DomainID != nil {
		payload.DomainID = request.DomainID.String()
	}
	return payload
}

func NewRefundPayload(eventType string, refund models.Refund) LifecyclePayload {
	return LifecyclePayload{
		EventID:      lifecycleEventID(refund.ID, eventType),
		EventType:    eventType,
		EventVersion: constants.WebhookEventVersionV1,
		EntityType:   EntityTypeRefund,
		EntityID:     refund.ID.String(),
		MerchantID:   refund.MerchantID.String(),
		DomainID:     refund.DomainID.String(),
		PaymentID:    refund.PaymentID.String(),
		AmountRaw:    refund.AmountRaw,
		Status:       refund.Status,
		Reason:       refund.Reason,
		Error:        refund.Error,
		TxHash:       refund.TxHash,
		RequestedBy:  refund.RequestedBy,
		ReviewedBy:   refund.ReviewedBy,
		ReviewedAt:   formatTimePtr(refund.ReviewedAt),
		CreatedAt:    refund.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:    refund.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func NewSweepPayload(eventType string, job models.SweepJob, txModel *models.Transaction, errText string) LifecyclePayload {
	payload := LifecyclePayload{
		EventID:               lifecycleEventID(job.ID, eventType),
		EventType:             eventType,
		EventVersion:          constants.WebhookEventVersionV1,
		EntityType:            EntityTypeSweep,
		EntityID:              job.ID.String(),
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

