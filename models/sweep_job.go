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

const (
	SweepOperatorActionReconcileBroadcast = "reconcile_broadcast"
	SweepOperatorActionReviewGasFunding   = "review_gas_funding"
	SweepOperatorActionReviewPolicy       = "review_policy"
)

const (
	SweepRecoveryActionRetry        = "retry"
	SweepRecoveryActionMarkSuccess  = "mark_success"
	SweepRecoveryActionPreserveHold = "preserve_hold"
	SweepRecoveryActionReleaseHold  = "release_hold"
)

const (
	SweepJobErrorMaxLength = 500
)

const (
	SweepFailureCategoryUnknown            = "unknown"
	SweepFailureCategoryPolicy             = "policy"
	SweepFailureCategoryTimeout            = "timeout"
	SweepFailureCategoryTransient          = "transient"
	SweepFailureCategoryBroadcastUncertain = "broadcast_uncertain"
)

type SweepJob struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	TransactionUniqueHash string            `gorm:"size:256;not null;uniqueIndex" json:"transaction_unique_hash"`
	TransactionHash       string            `gorm:"size:128;index" json:"transaction_hash"`
	WalletID              uuid.UUID         `gorm:"type:uuid;not null;index" json:"wallet_id"`
	MerchantID            uuid.UUID         `gorm:"type:uuid;not null;index" json:"merchant_id"`
	ChainID               constants.ChainID `gorm:"type:bigint;not null;index" json:"chain_id"`
	Token                 *string           `gorm:"type:varchar(128);index" json:"token,omitempty"`

	Status          string     `gorm:"size:24;not null;index" json:"status"`
	Attempts        uint       `gorm:"not null;default:0" json:"attempts"`
	MaxAttempts     uint       `gorm:"not null;default:12" json:"max_attempts"`
	LastError       string     `gorm:"type:text" json:"last_error,omitempty"`
	FailureCategory string     `gorm:"size:64;index" json:"failure_category,omitempty"`
	NextRunAt       *time.Time `gorm:"index" json:"next_run_at,omitempty"`
	LockedUntil     *time.Time `gorm:"index" json:"locked_until,omitempty"`
	SweepTxHash     string     `gorm:"size:128;index" json:"sweep_tx_hash,omitempty"`

	BatchID      *uuid.UUID `gorm:"type:uuid;index" json:"batch_id,omitempty"`
	BatchKey     string     `gorm:"size:160;index" json:"batch_key,omitempty"`
	BatchOrdinal uint       `gorm:"not null;default:0" json:"batch_ordinal"`
	BatchSize    uint       `gorm:"not null;default:0" json:"batch_size"`
	BatchPolicy  string     `gorm:"size:80" json:"batch_policy,omitempty"`

	PrefundAttempts        uint       `gorm:"not null;default:0" json:"prefund_attempts"`
	PrefundMaxAttempts     uint       `gorm:"not null;default:3" json:"prefund_max_attempts"`
	PrefundLastError       string     `gorm:"type:text" json:"prefund_last_error,omitempty"`
	PrefundFailureCategory string     `gorm:"size:64;index" json:"prefund_failure_category,omitempty"`
	PrefundLastAttemptAt   *time.Time `gorm:"index" json:"prefund_last_attempt_at,omitempty"`
	PrefundedAt            *time.Time `gorm:"index" json:"prefunded_at,omitempty"`

	OperatorAction string     `gorm:"size:80;index" json:"operator_action,omitempty"`
	OperatorNote   string     `gorm:"type:text" json:"operator_note,omitempty"`
	RecoveryAction string     `gorm:"size:80;index" json:"recovery_action,omitempty"`
	RecoveredAt    *time.Time `gorm:"index" json:"recovered_at,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
