package models

import (
	"core/constants"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type NetworkOperationalMode string

const (
	NetworkOperationalModeActive         NetworkOperationalMode = "active"
	NetworkOperationalModeDepositsOff    NetworkOperationalMode = "deposits_off"
	NetworkOperationalModeWithdrawalsOff NetworkOperationalMode = "withdrawals_off"
	NetworkOperationalModeMaintenance    NetworkOperationalMode = "maintenance"
)

var (
	ErrNetworkOperationalStateInvalid     = errors.New("network operational state is invalid")
	ErrNetworkOperationalModeInvalid      = errors.New("network operational mode is invalid")
	ErrNetworkOperationalChainUnsupported = errors.New("network operational chain is unsupported")
	ErrNetworkOperationalReasonTooLong    = errors.New("network operational reason exceeds 500 characters")
)

type NetworkOperationalState struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	ChainID constants.ChainID      `gorm:"type:bigint;not null;uniqueIndex:ux_network_operational_states_chain_id" json:"chain_id"`
	Mode    NetworkOperationalMode `gorm:"size:32;not null;default:'active';index;check:network_operational_states_mode_check,mode IN ('active','deposits_off','withdrawals_off','maintenance')" json:"mode"`
	Reason  string                 `gorm:"size:500" json:"reason,omitempty"`

	UpdatedBy string    `gorm:"size:255" json:"updated_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func NormalizeNetworkOperationalMode(mode NetworkOperationalMode) NetworkOperationalMode {
	return NetworkOperationalMode(strings.ToLower(strings.TrimSpace(string(mode))))
}

func IsValidNetworkOperationalMode(mode NetworkOperationalMode) bool {
	switch NormalizeNetworkOperationalMode(mode) {
	case NetworkOperationalModeActive,
		NetworkOperationalModeDepositsOff,
		NetworkOperationalModeWithdrawalsOff,
		NetworkOperationalModeMaintenance:
		return true
	default:
		return false
	}
}

func (s *NetworkOperationalState) Normalize() {
	if s == nil {
		return
	}
	s.Mode = NormalizeNetworkOperationalMode(s.Mode)
	s.Reason = strings.TrimSpace(s.Reason)
	if s.Mode == NetworkOperationalModeActive {
		s.Reason = ""
	}
	s.UpdatedBy = strings.TrimSpace(s.UpdatedBy)
}

func (s NetworkOperationalState) Validate() error {
	if !constants.IsSupportedChainID(s.ChainID) {
		return fmt.Errorf("%w: %w: %d", ErrNetworkOperationalStateInvalid, ErrNetworkOperationalChainUnsupported, s.ChainID)
	}
	if !IsValidNetworkOperationalMode(s.Mode) {
		return fmt.Errorf("%w: %w: %q", ErrNetworkOperationalStateInvalid, ErrNetworkOperationalModeInvalid, s.Mode)
	}
	if utf8.RuneCountInString(strings.TrimSpace(s.Reason)) > 500 {
		return fmt.Errorf("%w: %w", ErrNetworkOperationalStateInvalid, ErrNetworkOperationalReasonTooLong)
	}
	return nil
}

func (s NetworkOperationalState) BlocksDeposits() bool {
	switch NormalizeNetworkOperationalMode(s.Mode) {
	case NetworkOperationalModeDepositsOff, NetworkOperationalModeMaintenance:
		return true
	default:
		return false
	}
}

func (s NetworkOperationalState) BlocksWithdrawals() bool {
	switch NormalizeNetworkOperationalMode(s.Mode) {
	case NetworkOperationalModeWithdrawalsOff, NetworkOperationalModeMaintenance:
		return true
	default:
		return false
	}
}

func (s *NetworkOperationalState) BeforeCreate(_ *gorm.DB) error {
	s.Normalize()
	if s.Mode == "" {
		s.Mode = NetworkOperationalModeActive
		s.Reason = ""
	}
	return s.Validate()
}

func (s *NetworkOperationalState) BeforeUpdate(_ *gorm.DB) error {
	s.Normalize()
	return s.Validate()
}
