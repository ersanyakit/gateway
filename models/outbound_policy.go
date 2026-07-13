package models

import (
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type OutboundPolicySetting struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	MerchantID *uuid.UUID `gorm:"type:uuid;index" json:"merchant_id,omitempty"`
	DomainID   *uuid.UUID `gorm:"type:uuid;index" json:"domain_id,omitempty"`
	Chain      string     `gorm:"size:40;not null;default:'';index" json:"chain,omitempty"`
	Token      *string    `gorm:"type:varchar(128);index" json:"token,omitempty"`

	WhitelistRequired  bool   `gorm:"not null;default:false;index" json:"whitelist_required"`
	EmergencyFrozen    bool   `gorm:"not null;default:false;index" json:"emergency_frozen"`
	MaxAmountRaw       string `gorm:"type:text;not null;default:''" json:"max_amount_raw,omitempty"`
	VelocityLimitRaw   string `gorm:"type:text;not null;default:''" json:"velocity_limit_raw,omitempty"`
	VelocityWindowSecs int64  `gorm:"not null;default:86400" json:"velocity_window_secs"`

	CreatedBy string    `gorm:"size:255" json:"created_by,omitempty"`
	UpdatedBy string    `gorm:"size:255" json:"updated_by,omitempty"`
	CreatedAt time.Time `gorm:"index" json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (p *OutboundPolicySetting) BeforeCreate(tx *gorm.DB) error {
	normalizeOutboundPolicySetting(p)
	return nil
}

func (p *OutboundPolicySetting) BeforeUpdate(tx *gorm.DB) error {
	normalizeOutboundPolicySetting(p)
	return nil
}

type OutboundAddressWhitelist struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	MerchantID *uuid.UUID `gorm:"type:uuid;index" json:"merchant_id,omitempty"`
	DomainID   *uuid.UUID `gorm:"type:uuid;index" json:"domain_id,omitempty"`
	Chain      string     `gorm:"size:40;not null;default:'';index" json:"chain,omitempty"`
	Token      *string    `gorm:"type:varchar(128);index" json:"token,omitempty"`
	Address    string     `gorm:"size:180;not null;index" json:"address"`
	Label      string     `gorm:"size:160" json:"label,omitempty"`
	IsActive   bool       `gorm:"not null;default:true;index" json:"is_active"`
	CreatedBy  string     `gorm:"size:255" json:"created_by,omitempty"`
	UpdatedBy  string     `gorm:"size:255" json:"updated_by,omitempty"`
	CreatedAt  time.Time  `gorm:"index" json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

func (w *OutboundAddressWhitelist) BeforeCreate(tx *gorm.DB) error {
	normalizeOutboundWhitelist(w)
	return nil
}

func (w *OutboundAddressWhitelist) BeforeUpdate(tx *gorm.DB) error {
	normalizeOutboundWhitelist(w)
	return nil
}

func normalizeOutboundPolicySetting(p *OutboundPolicySetting) {
	if p == nil {
		return
	}
	p.Chain = strings.ToLower(strings.TrimSpace(p.Chain))
	normalizeTokenPointer(&p.Token)
	p.MaxAmountRaw = strings.TrimSpace(p.MaxAmountRaw)
	p.VelocityLimitRaw = strings.TrimSpace(p.VelocityLimitRaw)
	if p.VelocityWindowSecs <= 0 {
		p.VelocityWindowSecs = 86400
	}
	p.CreatedBy = strings.TrimSpace(p.CreatedBy)
	p.UpdatedBy = strings.TrimSpace(p.UpdatedBy)
}

func normalizeOutboundWhitelist(w *OutboundAddressWhitelist) {
	if w == nil {
		return
	}
	w.Chain = strings.ToLower(strings.TrimSpace(w.Chain))
	normalizeTokenPointer(&w.Token)
	w.Address = strings.ToLower(strings.TrimSpace(w.Address))
	w.Label = strings.TrimSpace(w.Label)
	w.CreatedBy = strings.TrimSpace(w.CreatedBy)
	w.UpdatedBy = strings.TrimSpace(w.UpdatedBy)
}

func normalizeTokenPointer(token **string) {
	if token == nil || *token == nil {
		return
	}
	value := strings.ToLower(strings.TrimSpace(**token))
	if value == "" {
		*token = nil
		return
	}
	*token = &value
}
