package models

import (
	"time"

	"github.com/google/uuid"
)

const (
	RefundStatusPending   = "pending"
	RefundStatusApproved  = "approved"
	RefundStatusRejected  = "rejected"
	RefundStatusSucceeded = "succeeded"
	RefundStatusFailed    = "failed"
)

type Refund struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	MerchantID uuid.UUID `gorm:"type:uuid;not null;index" json:"merchant_id"`
	DomainID   uuid.UUID `gorm:"type:uuid;not null;index" json:"domain_id"`
	PaymentID  uuid.UUID `gorm:"type:uuid;not null;index" json:"payment_id"`

	AmountRaw string `gorm:"type:text;not null" json:"amount_raw"`
	Reason    string `gorm:"size:500" json:"reason,omitempty"`
	Status    string `gorm:"size:32;not null;index" json:"status"`
	TxHash    string `gorm:"size:128;index" json:"tx_hash,omitempty"`
	Error     string `gorm:"type:text" json:"error,omitempty"`

	RequestedBy string     `gorm:"size:255" json:"requested_by,omitempty"`
	ReviewedBy  string     `gorm:"size:255" json:"reviewed_by,omitempty"`
	ReviewedAt  *time.Time `json:"reviewed_at,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
