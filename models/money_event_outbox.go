package models

import (
	"time"

	"github.com/google/uuid"
)

const (
	MoneyEventOutboxStatusPending    = "pending"
	MoneyEventOutboxStatusProcessing = "processing"
	MoneyEventOutboxStatusDelivered  = "delivered"
	MoneyEventOutboxStatusFailed     = "failed"
	MoneyEventOutboxStatusDeadLetter = "dead_letter"
)

type MoneyEventOutbox struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	EventID      string `gorm:"size:256;not null;uniqueIndex:ux_money_event_outboxes_event_id" json:"event_id"`
	EventType    string `gorm:"size:120;not null;index" json:"event_type"`
	EventVersion string `gorm:"size:16;not null;default:v1" json:"event_version"`

	AggregateType string `gorm:"size:64;not null;index:idx_money_event_outbox_aggregate,priority:1" json:"aggregate_type"`
	AggregateID   string `gorm:"size:256;not null;index:idx_money_event_outbox_aggregate,priority:2" json:"aggregate_id"`

	MerchantID uuid.UUID `gorm:"type:uuid;not null;index;uniqueIndex:ux_money_event_outboxes_idempotency_scope,priority:1" json:"merchant_id"`
	DomainID   uuid.UUID `gorm:"type:uuid;not null;index;uniqueIndex:ux_money_event_outboxes_idempotency_scope,priority:2" json:"domain_id"`

	IdempotencyKey string `gorm:"size:256;not null;uniqueIndex:ux_money_event_outboxes_idempotency_scope,priority:3" json:"idempotency_key"`
	PayloadJSON    string `gorm:"type:jsonb;not null;check:money_event_outboxes_payload_object_check,jsonb_typeof(payload_json) = 'object'" json:"payload_json"`

	Status   string `gorm:"size:32;not null;index" json:"status"`
	Attempts uint   `gorm:"not null;default:0" json:"attempts"`

	LockedUntil *time.Time `gorm:"index" json:"locked_until,omitempty"`
	LastError   string     `gorm:"type:text" json:"last_error,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
