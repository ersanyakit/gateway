package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Merchant struct {
	ID       uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	Name     string    `gorm:"size:255;not null" json:"name"`
	Email    string    `gorm:"size:255;not null;index" json:"email"`
	Password string    `json:"-"`
	Domains  []Domain  `gorm:"foreignKey:MerchantID" json:"domains,omitempty"`
	IsActive bool      `gorm:"not null;default:true;index" json:"is_active"`

	HideTestnets bool   `gorm:"not null;default:false" json:"hide_testnets"`
	HiddenChains string `gorm:"size:1024;default:''" json:"hidden_chains"`

	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index"`
}
