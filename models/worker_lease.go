package models

import (
	"time"

	"github.com/google/uuid"
)

const (
	WorkerLeaseStatusActive   = "active"
	WorkerLeaseStatusReleased = "released"
)

type WorkerLease struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	LeaseKey string `gorm:"size:220;not null;uniqueIndex:ux_worker_leases_key" json:"lease_key"`
	OwnerID  string `gorm:"size:180;not null;index" json:"owner_id"`
	Purpose  string `gorm:"size:120;not null;index" json:"purpose"`
	Status   string `gorm:"size:32;not null;index" json:"status"`

	Attempts      uint       `gorm:"not null;default:0" json:"attempts"`
	LeaseUntil    time.Time  `gorm:"not null;index" json:"lease_until"`
	AcquiredAt    time.Time  `gorm:"not null;index" json:"acquired_at"`
	LastHeartbeat *time.Time `gorm:"index" json:"last_heartbeat,omitempty"`
	ReleasedAt    *time.Time `gorm:"index" json:"released_at,omitempty"`

	LastError string `gorm:"type:text" json:"last_error,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
