package models

import (
	"time"

	"github.com/google/uuid"
)

type Merchant struct {
	ID       uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	Name     string    `gorm:"size:255;not null" json:"name"`
	Email    string    `gorm:"size:255;not null;index" json:"email"`
	Password string    `json:"-"`
	Domains  []Domain  `gorm:"foreignKey:MerchantID" json:"domains,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
