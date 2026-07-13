package models

import (
	"time"

	"github.com/google/uuid"
)

const (
	MoneyEventInboxStatusReceived   = "received"
	MoneyEventInboxStatusProcessing = "processing"
	MoneyEventInboxStatusSucceeded  = "succeeded"
	MoneyEventInboxStatusFailed     = "failed"
	MoneyEventInboxStatusDeadLetter = "dead_letter"
)

type MoneyEventInbox struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	EventID          string `gorm:"size:256;not null;uniqueIndex:ux_money_event_inbox_consumer_event,priority:2" json:"event_id"`
	ConsumerName     string `gorm:"size:120;not null;uniqueIndex:ux_money_event_inbox_consumer_event,priority:1;index:idx_money_event_inbox_consumer_status,priority:1" json:"consumer_name"`
	IdempotencyScope string `gorm:"size:256;not null;index" json:"idempotency_scope"`

	EventType    string `gorm:"size:120;index" json:"event_type,omitempty"`
	ResourceType string `gorm:"size:80;index" json:"resource_type,omitempty"`
	ResourceID   string `gorm:"size:256;index" json:"resource_id,omitempty"`

	Status      string     `gorm:"size:32;not null;index:idx_money_event_inbox_consumer_status,priority:2" json:"status"`
	Attempts    uint       `gorm:"not null;default:0" json:"attempts"`
	MaxAttempts uint       `gorm:"not null;default:8" json:"max_attempts"`
	LockedUntil *time.Time `gorm:"index" json:"locked_until,omitempty"`
	ProcessedAt *time.Time `gorm:"index" json:"processed_at,omitempty"`

	LastError       string `gorm:"type:text" json:"last_error,omitempty"`
	FailureCategory string `gorm:"size:80;index" json:"failure_category,omitempty"`
	EvidenceJSON    string `gorm:"type:jsonb;not null;default:'{}'" json:"evidence_json,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
