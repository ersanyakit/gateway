package models

import (
	"time"

	"core/constants"

	"github.com/google/uuid"
)

const (
	PaymentDepositAllocationStatusApplied     = "applied"
	PaymentDepositAllocationStatusQuarantined = "quarantined"
	PaymentDepositAllocationStatusReorged     = "reorged"
)

type PaymentDepositAllocation struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	PaymentSessionID uuid.UUID  `gorm:"type:uuid;not null;index;uniqueIndex:ux_payment_deposit_alloc_session_tx" json:"payment_session_id"`
	DepositID        *uuid.UUID `gorm:"type:uuid;index" json:"deposit_id,omitempty"`

	TransactionUniqueHash string `gorm:"size:256;not null;uniqueIndex:ux_payment_deposit_alloc_tx;uniqueIndex:ux_payment_deposit_alloc_session_tx" json:"transaction_unique_hash"`
	ChainFactEventID      string `gorm:"size:256;index" json:"chain_fact_event_id,omitempty"`
	TxHash                string `gorm:"size:128;index" json:"tx_hash,omitempty"`

	ChainID                   constants.ChainID `gorm:"type:bigint;not null;index" json:"chain_id"`
	ObservedAddress           string            `gorm:"size:180;index" json:"observed_address,omitempty"`
	ObservedAddressNormalized string            `gorm:"size:180;index" json:"observed_address_normalized,omitempty"`
	Token                     *string           `gorm:"size:160;index" json:"token,omitempty"`
	Symbol                    string            `gorm:"size:32;not null;index" json:"symbol"`
	Decimals                  uint8             `gorm:"not null" json:"decimals"`
	AmountRaw                 string            `gorm:"type:text;not null" json:"amount_raw"`

	Memo           string `gorm:"size:180;index" json:"memo,omitempty"`
	MemoNormalized string `gorm:"size:180;index" json:"memo_normalized,omitempty"`
	MemoStatus     string `gorm:"size:32;not null;default:'not_required';index" json:"memo_status"`

	Status  string `gorm:"size:32;not null;default:'applied';index" json:"status"`
	Outcome string `gorm:"size:40;index" json:"outcome,omitempty"`
	Reason  string `gorm:"size:500" json:"reason,omitempty"`

	ReorgedAt *time.Time `gorm:"index" json:"reorged_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}
