package models

import (
	"time"

	"core/constants"

	"github.com/google/uuid"
)

const (
	OutboundResourceWithdrawal = "withdrawal"
	OutboundResourceRefund     = "refund"
	OutboundResourceSweepJob   = "sweep_job"
)

const (
	OutboundStatusPrepared            = "prepared"
	OutboundStatusSigned              = "signed"
	OutboundStatusBroadcastAttempted  = "broadcast_attempted"
	OutboundStatusBroadcasted         = "broadcasted"
	OutboundStatusConfirming          = "confirming"
	OutboundStatusFinalized           = "finalized"
	OutboundStatusFailed              = "failed"
	OutboundStatusReplaced            = "replaced"
	OutboundStatusAbandoned           = "abandoned"
	OutboundStatusNeedsOperatorAction = "needs_operator_action"
)

type OutboundTransaction struct {
	ID             uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	IdempotencyKey string    `gorm:"size:220;not null;uniqueIndex:ux_outbound_transactions_idempotency_key" json:"idempotency_key"`

	ResourceType string     `gorm:"size:40;not null;index:idx_outbound_transactions_resource,priority:1" json:"resource_type"`
	ResourceID   uuid.UUID  `gorm:"type:uuid;not null;index:idx_outbound_transactions_resource,priority:2" json:"resource_id"`
	MerchantID   uuid.UUID  `gorm:"type:uuid;not null;index" json:"merchant_id"`
	DomainID     *uuid.UUID `gorm:"type:uuid;index" json:"domain_id,omitempty"`
	WalletID     uuid.UUID  `gorm:"type:uuid;not null;index" json:"wallet_id"`

	ChainID   constants.ChainID `gorm:"type:bigint;not null;index" json:"chain_id"`
	ChainName string            `gorm:"size:64;not null;index" json:"chain_name"`
	Token     *string           `gorm:"type:varchar(128);index" json:"token,omitempty"`
	Symbol    string            `gorm:"size:32;not null;default:'';index" json:"symbol"`
	Decimals  uint8             `json:"decimals"`
	AmountRaw string            `gorm:"type:text;not null" json:"amount_raw"`
	ToAddress string            `gorm:"size:180;not null" json:"to_address"`

	Status      string     `gorm:"size:40;not null;index" json:"status"`
	TxHash      string     `gorm:"size:160;index" json:"tx_hash,omitempty"`
	Attempts    uint       `gorm:"not null;default:0" json:"attempts"`
	MaxAttempts uint       `gorm:"not null;default:5" json:"max_attempts"`
	NextRunAt   *time.Time `gorm:"index" json:"next_run_at,omitempty"`
	LockedUntil *time.Time `gorm:"index" json:"locked_until,omitempty"`

	SignedAt             *time.Time `gorm:"index" json:"signed_at,omitempty"`
	BroadcastAttemptedAt *time.Time `gorm:"index" json:"broadcast_attempted_at,omitempty"`
	BroadcastedAt        *time.Time `gorm:"index" json:"broadcasted_at,omitempty"`
	FinalizedAt          *time.Time `gorm:"index" json:"finalized_at,omitempty"`

	FeePolicyJSON string `gorm:"type:text" json:"fee_policy_json,omitempty"`

	ReplacementParentID *uuid.UUID `gorm:"type:uuid;index" json:"replacement_parent_id,omitempty"`
	ReplacementReason   string     `gorm:"size:160" json:"replacement_reason,omitempty"`
	ReplacesTxHash      string     `gorm:"size:160;index" json:"replaces_tx_hash,omitempty"`

	ErrorCategory string `gorm:"size:80;index" json:"error_category,omitempty"`
	ErrorDetail   string `gorm:"size:1000" json:"error_detail,omitempty"`
	ActorID       string `gorm:"size:255;index" json:"actor_id,omitempty"`
	CorrelationID string `gorm:"size:220;index" json:"correlation_id,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
