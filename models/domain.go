package models

import (
	"time"

	"github.com/google/uuid"
)

type Domain struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey"`

	MerchantID uuid.UUID `gorm:"type:uuid;not null;index"`
	Merchant   Merchant  `gorm:"constraint:OnDelete:CASCADE;"`

	DomainURL string `gorm:"size:255;not null"`

	KeyID     string `gorm:"size:32;index"`
	APIKey    string `gorm:"size:128;uniqueIndex;not null"`
	APISecret string `gorm:"size:256;not null" json:"-"`

	HDAccountID uint32 `gorm:"not null;uniqueIndex;default:nextval('merchant_hd_account_seq')"`

	WebhookURL    string `gorm:"size:500"`
	WebhookSecret string `gorm:"size:256" json:"-"`
	IsEnabled     bool   `json:"is_enabled" gorm:"-"`

	CreatedAt time.Time
	UpdatedAt time.Time
}
