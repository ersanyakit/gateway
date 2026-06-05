package models

import (
	"time"

	"github.com/google/uuid"
)

const (
	WebhookDeliveryStatusPending    = "pending"
	WebhookDeliveryStatusSucceeded  = "succeeded"
	WebhookDeliveryStatusFailed     = "failed"
	WebhookDeliveryStatusDeadLetter = "dead_letter"
)

type WebhookDelivery struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	MerchantID    uuid.UUID  `gorm:"type:uuid;not null;index" json:"merchant_id"`
	DomainID      uuid.UUID  `gorm:"type:uuid;not null;index" json:"domain_id"`
	PaymentID     *uuid.UUID `gorm:"type:uuid;index" json:"payment_id,omitempty"`
	TransactionID *uuid.UUID `gorm:"type:uuid;index" json:"transaction_id,omitempty"`

	EventID   string `gorm:"size:256;not null;index" json:"event_id"`
	EventType string `gorm:"size:80;not null;index" json:"event_type"`
	TargetURL string `gorm:"size:500;not null" json:"target_url"`
	Status    string `gorm:"size:24;not null;index" json:"status"`

	Attempts    uint       `gorm:"not null;default:0" json:"attempts"`
	LastError   string     `gorm:"type:text" json:"last_error,omitempty"`
	NextRetryAt *time.Time `gorm:"index" json:"next_retry_at,omitempty"`
	DeliveredAt *time.Time `json:"delivered_at,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
