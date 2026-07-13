package models

import (
	"core/constants"
	"time"

	"github.com/google/uuid"
)

const (
	ReconciliationStatusOpen                = "open"
	ReconciliationStatusProcessing          = "processing"
	ReconciliationStatusResolved            = "resolved"
	ReconciliationStatusFailed              = "failed"
	ReconciliationStatusNeedsOperatorAction = "needs_operator_action"
	ReconciliationStatusRetryScheduled      = "retry_scheduled"
)

type ReconciliationJob struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	ChainID   constants.ChainID `gorm:"type:bigint;not null;index" json:"chain_id"`
	FromBlock int64             `gorm:"not null;index" json:"from_block"`
	ToBlock   int64             `gorm:"not null;index" json:"to_block"`
	Reason    string            `gorm:"size:120;not null;index" json:"reason"`
	Status    string            `gorm:"size:32;not null;index" json:"status"`
	Error     string            `gorm:"type:text" json:"error,omitempty"`
	Attempts  uint              `gorm:"not null;default:0" json:"attempts"`

	MerchantID              *uuid.UUID `gorm:"type:uuid;index" json:"merchant_id,omitempty"`
	DomainID                *uuid.UUID `gorm:"type:uuid;index" json:"domain_id,omitempty"`
	ScopeKey                string     `gorm:"size:256;index" json:"scope_key,omitempty"`
	ResourceType            string     `gorm:"size:64;index" json:"resource_type,omitempty"`
	ResourceID              string     `gorm:"size:256;index" json:"resource_id,omitempty"`
	AffectedResourceIDsJSON string     `gorm:"type:jsonb;default:'[]'" json:"affected_resource_ids_json,omitempty"`
	EvidenceJSON            string     `gorm:"type:jsonb;default:'{}'" json:"evidence_json,omitempty"`
	Outcome                 string     `gorm:"size:64;index" json:"outcome,omitempty"`

	NextRunAt  *time.Time `gorm:"index" json:"next_run_at,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}
