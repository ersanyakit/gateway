package models

import (
	"time"

	"github.com/google/uuid"
)

const (
	RefundStatusPending    = "pending"
	RefundStatusProcessing = "processing"
	RefundStatusApproved   = "approved"
	RefundStatusRejected   = "rejected"
	RefundStatusSucceeded  = "succeeded"
	RefundStatusFailed     = "failed"
)

type Refund struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	MerchantID uuid.UUID  `gorm:"type:uuid;not null;index" json:"merchant_id"`
	DomainID   uuid.UUID  `gorm:"type:uuid;not null;index" json:"domain_id"`
	PaymentID  uuid.UUID  `gorm:"type:uuid;not null;index" json:"payment_id"`
	WalletID   *uuid.UUID `gorm:"type:uuid;index" json:"wallet_id,omitempty"`

	AmountRaw string `gorm:"type:text;not null" json:"amount_raw"`
	Reason    string `gorm:"size:500" json:"reason,omitempty"`
	Status    string `gorm:"size:32;not null;index" json:"status"`
	TxHash    string `gorm:"size:128;index" json:"tx_hash,omitempty"`
	Error     string `gorm:"type:text" json:"error,omitempty"`

	Chain     string  `gorm:"size:40;not null;default:'';index" json:"chain,omitempty"`
	Token     *string `gorm:"type:varchar(128);index" json:"token,omitempty"`
	Symbol    string  `gorm:"size:20;not null;default:'';index" json:"symbol,omitempty"`
	Decimals  uint8   `json:"decimals,omitempty"`
	ToAddress string  `gorm:"size:160" json:"to_address,omitempty"`

	RequestedBy string     `gorm:"size:255" json:"requested_by,omitempty"`
	ReviewedBy  string     `gorm:"size:255" json:"reviewed_by,omitempty"`
	ReviewedAt  *time.Time `json:"reviewed_at,omitempty"`

	BroadcastedAt  *time.Time `gorm:"index" json:"broadcasted_at,omitempty"`
	FinalizedAt    *time.Time `gorm:"index" json:"finalized_at,omitempty"`
	IdempotencyKey string     `gorm:"size:180;index" json:"idempotency_key,omitempty"`
	CorrelationID  string     `gorm:"size:180;index" json:"correlation_id,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
