package models

import (
	"core/constants"
	"time"

	"github.com/google/uuid"
)

const (
	SweepJobStatusPending    = "pending"
	SweepJobStatusProcessing = "processing"
	SweepJobStatusSucceeded  = "succeeded"
	SweepJobStatusFailed     = "failed"
	SweepJobStatusDeadLetter = "dead_letter"
)

type SweepJob struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	TransactionUniqueHash string            `gorm:"size:256;not null;uniqueIndex" json:"transaction_unique_hash"`
	TransactionHash       string            `gorm:"size:128;index" json:"transaction_hash"`
	WalletID              uuid.UUID         `gorm:"type:uuid;not null;index" json:"wallet_id"`
	MerchantID            uuid.UUID         `gorm:"type:uuid;not null;index" json:"merchant_id"`
	ChainID               constants.ChainID `gorm:"type:bigint;not null;index" json:"chain_id"`
	Token                 *string           `gorm:"type:varchar(128);index" json:"token,omitempty"`

	Status      string     `gorm:"size:24;not null;index" json:"status"`
	Attempts    uint       `gorm:"not null;default:0" json:"attempts"`
	MaxAttempts uint       `gorm:"not null;default:12" json:"max_attempts"`
	LastError   string     `gorm:"type:text" json:"last_error,omitempty"`
	NextRunAt   *time.Time `gorm:"index" json:"next_run_at,omitempty"`
	LockedUntil *time.Time `gorm:"index" json:"locked_until,omitempty"`
	SweepTxHash string     `gorm:"size:128;index" json:"sweep_tx_hash,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
