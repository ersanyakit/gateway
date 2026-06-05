package models

import (
	"time"

	"github.com/google/uuid"
)

type Product struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	MerchantID uuid.UUID `gorm:"type:uuid;not null;index" json:"merchant_id"`
	Merchant   Merchant  `gorm:"constraint:OnDelete:CASCADE;" json:"-"`
	DomainID   uuid.UUID `gorm:"type:uuid;not null;index" json:"domain_id"`
	Domain     Domain    `gorm:"constraint:OnDelete:CASCADE;" json:"-"`

	Name        string `gorm:"size:180;not null" json:"name"`
	Description string `gorm:"size:500" json:"description,omitempty"`
	Amount      string `gorm:"size:80;not null" json:"amount"`
	Currency    string `gorm:"size:20;not null;index" json:"currency"`
	Language    string `gorm:"size:8;not null;default:'tr'" json:"language"`
	SuccessURL  string `gorm:"size:500" json:"success_url,omitempty"`
	CancelURL   string `gorm:"size:500" json:"cancel_url,omitempty"`

	LinkToken string `gorm:"size:80;uniqueIndex;not null" json:"link_token"`
	IsActive  bool   `gorm:"not null;default:true" json:"is_active"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
