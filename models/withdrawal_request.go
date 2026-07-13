package models

import (
	"time"

	"github.com/google/uuid"
)

const (
	WithdrawalStatusPending    = "pending"
	WithdrawalStatusProcessing = "processing"
	WithdrawalStatusApproved   = "approved"
	WithdrawalStatusFinalized  = "finalized"
	WithdrawalStatusRejected   = "rejected"
	WithdrawalStatusFailed     = "failed"
)

type WithdrawalRequest struct {
	ID         uuid.UUID  `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	MerchantID uuid.UUID  `gorm:"type:uuid;not null;index" json:"merchant_id"`
	Merchant   Merchant   `gorm:"constraint:OnDelete:CASCADE;" json:"-"`
	DomainID   *uuid.UUID `gorm:"type:uuid;index" json:"domain_id,omitempty"`
	WalletID   uuid.UUID  `gorm:"type:uuid;not null;index" json:"wallet_id"`
	Wallet     Wallet     `gorm:"constraint:OnDelete:CASCADE;" json:"-"`

	Chain     string  `gorm:"size:40;not null;index" json:"chain"`
	Token     *string `gorm:"type:varchar(128);index" json:"token,omitempty"`
	Symbol    string  `gorm:"size:20;not null;default:'';index" json:"symbol,omitempty"`
	Decimals  uint8   `json:"decimals,omitempty"`
	ToAddress string  `gorm:"size:160;not null" json:"to_address"`
	AmountRaw string  `gorm:"type:text;not null" json:"amount_raw"`
	Note      string  `gorm:"size:500" json:"note,omitempty"`
	Status    string  `gorm:"size:32;not null;index" json:"status"`

	RequestedBy string     `gorm:"size:255" json:"requested_by,omitempty"`
	ReviewedBy  string     `gorm:"size:255" json:"reviewed_by,omitempty"`
	ReviewedAt  *time.Time `json:"reviewed_at,omitempty"`
	TxHash      string     `gorm:"size:128;index" json:"tx_hash,omitempty"`
	Error       string     `gorm:"type:text" json:"error,omitempty"`

	BroadcastedAt  *time.Time `gorm:"index" json:"broadcasted_at,omitempty"`
	FinalizedAt    *time.Time `gorm:"index" json:"finalized_at,omitempty"`
	IdempotencyKey string     `gorm:"size:180;index" json:"idempotency_key,omitempty"`
	CorrelationID  string     `gorm:"size:180;index" json:"correlation_id,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
