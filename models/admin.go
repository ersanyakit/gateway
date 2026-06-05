package models

import (
	"time"

	"github.com/google/uuid"
)

type Admin struct {
	ID          uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	Email       string    `gorm:"size:255;not null;uniqueIndex" json:"email"`
	Password    string    `json:"-"`
	Name        string    `gorm:"size:128" json:"name"`
	TOTPSecret  string    `gorm:"size:64" json:"-"`
	TOTPEnabled bool      `gorm:"not null;default:false" json:"totp_enabled"`
	IsActive    bool      `gorm:"not null;default:true" json:"is_active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
