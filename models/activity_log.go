package models

import (
	"time"

	"github.com/google/uuid"
)

type ActivityLog struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	MerchantID *uuid.UUID `gorm:"type:uuid;index" json:"merchant_id,omitempty"`
	Merchant   *Merchant  `gorm:"constraint:OnDelete:SET NULL;" json:"merchant,omitempty"`

	ActorType  string `gorm:"size:40;not null;index" json:"actor_type"`
	ActorEmail string `gorm:"size:255;index" json:"actor_email,omitempty"`
	Event      string `gorm:"size:80;not null;index" json:"event"`
	Status     string `gorm:"size:32;not null;index" json:"status"`

	SubjectType string `gorm:"size:80;index" json:"subject_type,omitempty"`
	SubjectID   string `gorm:"size:128;index" json:"subject_id,omitempty"`
	Description string `gorm:"type:text" json:"description,omitempty"`

	IPAddress string `gorm:"size:64;index" json:"ip_address,omitempty"`
	UserAgent string `gorm:"type:text" json:"user_agent,omitempty"`
	Method    string `gorm:"size:16" json:"method,omitempty"`
	Path      string `gorm:"size:255" json:"path,omitempty"`

	CreatedAt time.Time `gorm:"index" json:"created_at"`
}
