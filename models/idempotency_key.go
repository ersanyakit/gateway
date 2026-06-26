package models

import (
	"time"

	"github.com/google/uuid"
)

const (
	IdempotencyStatusInProgress = "in_progress"
	IdempotencyStatusCompleted  = "completed"
	IdempotencyStatusFailed     = "failed"
)

type IdempotencyKey struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	DomainID   uuid.UUID `gorm:"type:uuid;not null;index:ux_idempotency_scope,unique" json:"domain_id"`
	MerchantID uuid.UUID `gorm:"type:uuid;not null;index" json:"merchant_id"`
	Key        string    `gorm:"size:180;not null;index:ux_idempotency_scope,unique" json:"key"`

	RequestHash      string     `gorm:"size:128;not null" json:"request_hash"`
	Status           string     `gorm:"size:32;not null;index" json:"status"`
	PaymentSessionID *uuid.UUID `gorm:"type:uuid;index" json:"payment_session_id,omitempty"`
	ResponseBody     string     `gorm:"type:text" json:"response_body,omitempty"`
	Error            string     `gorm:"type:text" json:"error,omitempty"`
	ExpiresAt        *time.Time `gorm:"index" json:"expires_at,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
