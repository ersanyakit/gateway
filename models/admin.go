package models

import (
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	AdminRoleOwner    = "owner"
	AdminRoleSecurity = "security"
	AdminRoleOperator = "operator"
	AdminRoleViewer   = "viewer"
)

type Admin struct {
	ID          uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	Email       string    `gorm:"size:255;not null;uniqueIndex" json:"email"`
	Password    string    `json:"-"`
	Name        string    `gorm:"size:128" json:"name"`
	Role        string    `gorm:"size:32;not null;default:'operator';index" json:"role"`
	TOTPSecret  string    `gorm:"size:512" json:"-"`
	TOTPEnabled bool      `gorm:"not null;default:false" json:"totp_enabled"`
	IsActive    bool      `gorm:"not null;default:true" json:"is_active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (a *Admin) BeforeCreate(tx *gorm.DB) error {
	a.Role = NormalizeAdminRole(a.Role)
	return nil
}

func (a *Admin) BeforeUpdate(tx *gorm.DB) error {
	a.Role = NormalizeAdminRole(a.Role)
	return nil
}

func NormalizeAdminRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case AdminRoleOwner:
		return AdminRoleOwner
	case AdminRoleSecurity:
		return AdminRoleSecurity
	case AdminRoleViewer:
		return AdminRoleViewer
	case AdminRoleOperator:
		return AdminRoleOperator
	default:
		return AdminRoleOperator
	}
}

func EffectiveAdminRole(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" {
		return AdminRoleOwner
	}
	return NormalizeAdminRole(role)
}

func AdminRoleAllowed(role string, allowed ...string) bool {
	effective := EffectiveAdminRole(role)
	for _, candidate := range allowed {
		if effective == NormalizeAdminRole(candidate) {
			return true
		}
	}
	return false
}
