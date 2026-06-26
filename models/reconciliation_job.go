package models

import (
	"core/constants"
	"time"

	"github.com/google/uuid"
)

const (
	ReconciliationStatusOpen       = "open"
	ReconciliationStatusProcessing = "processing"
	ReconciliationStatusResolved   = "resolved"
	ReconciliationStatusFailed     = "failed"
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

	StartedAt  *time.Time `json:"started_at,omitempty"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}
