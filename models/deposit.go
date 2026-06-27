package models

import (
	"time"

	"core/constants"

	"github.com/google/uuid"
)

const (
	DepositStatusUnmatched  = "unmatched"
	DepositStatusPending    = "pending"
	DepositStatusConfirming = "confirming"
	DepositStatusFinalized  = "finalized"
)

type Deposit struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	ChainFactID      uuid.UUID `gorm:"type:uuid;index" json:"chain_fact_id"`
	ChainFactEventID string    `gorm:"size:256;not null;uniqueIndex:ux_deposits_chain_fact_event_id" json:"chain_fact_event_id"`
	Status           string    `gorm:"size:32;not null;index" json:"status"`

	WalletID   *uuid.UUID `gorm:"type:uuid;index" json:"wallet_id,omitempty"`
	MerchantID *uuid.UUID `gorm:"type:uuid;index" json:"merchant_id,omitempty"`
	DomainID   *uuid.UUID `gorm:"type:uuid;index" json:"domain_id,omitempty"`
	ProductID  string     `gorm:"size:128;index" json:"product_id,omitempty"`
	UserID     string     `gorm:"size:128;index" json:"user_id,omitempty"`

	ChainID         constants.ChainID `gorm:"type:bigint;not null;index" json:"chain_id"`
	BlockNumber     int64             `gorm:"not null;index" json:"block_number"`
	BlockHash       string            `gorm:"size:128;index" json:"block_hash,omitempty"`
	TxHash          string            `gorm:"size:128;not null;index" json:"tx_hash"`
	LogIndex        string            `gorm:"size:80;not null;index" json:"log_index"`
	ObservedAddress string            `gorm:"size:160;not null;index" json:"observed_address"`
	Direction       string            `gorm:"size:16;not null;index" json:"direction"`

	Token     *string `gorm:"size:160;index" json:"token,omitempty"`
	Symbol    string  `gorm:"size:32;not null;index" json:"symbol"`
	Decimals  uint8   `gorm:"not null" json:"decimals"`
	AmountRaw string  `gorm:"type:text;not null" json:"amount_raw"`

	Confirmations         uint   `gorm:"not null;default:0" json:"confirmations"`
	ConfirmationsRequired uint   `gorm:"not null;default:1" json:"confirmations_required"`
	TransactionUniqueHash string `gorm:"size:256;index" json:"transaction_unique_hash,omitempty"`
	SourceEventType       string `gorm:"size:80;not null;index" json:"source_event_type"`
	UnmatchedReason       string `gorm:"type:text" json:"unmatched_reason,omitempty"`

	DetectedAt  time.Time  `gorm:"not null;index" json:"detected_at"`
	FinalizedAt *time.Time `gorm:"index" json:"finalized_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}
