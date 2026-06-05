package models

import (
	"core/constants"
	"time"

	"github.com/google/uuid"
)

const (
	TransactionStatusPendingConfirmation = "pending_confirmation"
	TransactionStatusConfirmed           = "confirmed"
	TransactionStatusFailed              = "failed"
	TransactionStatusReorged             = "reorged"
)

type Transaction struct {
	ID         uuid.UUID         `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	ChainID    constants.ChainID `gorm:"type:bigint;not null;index" json:"chain_id"`
	UniqueHash string            `gorm:"type:varchar(256);uniqueIndex" json:"unique_hash"`

	Hash        string  `gorm:"type:varchar(128);not null;index" json:"hash"`
	LogIndex    *string `json:"log_index,omitempty"`
	BlockNumber string  `gorm:"not null;index" json:"block_number"`
	BlockHash   string  `gorm:"type:varchar(128);index" json:"block_hash"`

	Token    *string `gorm:"type:varchar(128);index" json:"asset_address,omitempty"`
	Symbol   string  `gorm:"type:varchar(20);not null" json:"symbol"`
	Decimals uint8   `json:"decimals,omitempty"`

	FromAddress string `gorm:"type:varchar(128);not null;index" json:"from_address"`
	ToAddress   string `gorm:"type:varchar(128);not null;index" json:"to_address"`
	Amount      string `gorm:"type:text;not null" json:"amount"`

	Status                string     `gorm:"type:varchar(32);not null;index" json:"status"` // pending, pending_confirmation, confirmed, failed.
	Confirmations         uint       `gorm:"not null;default:0" json:"confirmations"`
	ConfirmationsRequired uint       `gorm:"not null;default:1" json:"confirmations_required"`
	FinalizedAt           *time.Time `json:"finalized_at,omitempty"`
	ReorgedAt             *time.Time `json:"reorged_at,omitempty"`

	EventType  string     `gorm:"type:varchar(64);index" json:"event_type,omitempty"`
	WalletID   *uuid.UUID `gorm:"type:uuid;index" json:"wallet_id,omitempty"`
	MerchantID *uuid.UUID `gorm:"type:uuid;index" json:"merchant_id,omitempty"`
	DomainID   *uuid.UUID `gorm:"type:uuid;index" json:"domain_id,omitempty"`
	ProductID  string     `gorm:"size:128;index" json:"product_id,omitempty"`
	UserID     string     `gorm:"size:128;index" json:"user_id,omitempty"`

	WebhookSentAt    *time.Time `json:"webhook_sent_at,omitempty"`
	WebhookAttempts  uint       `gorm:"not null;default:0" json:"webhook_attempts"`
	WebhookLastError string     `gorm:"type:text" json:"webhook_last_error,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
