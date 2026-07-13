package models

import (
	"core/constants"
	"time"

	"github.com/google/uuid"
)

const (
	WalletAddressStatusGenerated = "generated"
	WalletAddressStatusReserved  = "reserved"
	WalletAddressStatusAssigned  = "assigned"
	WalletAddressStatusActive    = "active"
	WalletAddressStatusUsed      = "used"
	WalletAddressStatusExpired   = "expired"
	WalletAddressStatusReleased  = "released"
	WalletAddressStatusRetired   = "retired"
)

const (
	WalletAddressPurposeCheckout      = "checkout"
	WalletAddressPurposeStaticDeposit = "static_deposit"
	WalletAddressPurposeCEXDeposit    = "cex_deposit"
	WalletAddressPurposeReserve       = "reserve"
)

const (
	WalletAddressReusePolicyFresh  = "fresh"
	WalletAddressReusePolicyRotate = "rotate"
	WalletAddressReusePolicyReuse  = "reuse"
)

const (
	WalletAddressGapAnomalyUsedUnreserved = "used_unreserved"
	WalletAddressGapAnomalyScanError      = "scan_error"
)

type WalletAddressReservation struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	MerchantID uuid.UUID `gorm:"type:uuid;not null;index;uniqueIndex:ux_wallet_address_reservations_owner,priority:1" json:"merchant_id"`
	DomainID   uuid.UUID `gorm:"type:uuid;not null;index;uniqueIndex:ux_wallet_address_reservations_owner,priority:2" json:"domain_id"`
	ProductID  string    `gorm:"size:128;not null;index;uniqueIndex:ux_wallet_address_reservations_owner,priority:3" json:"product_id"`
	UserID     string    `gorm:"size:128;not null;index;uniqueIndex:ux_wallet_address_reservations_owner,priority:4" json:"user_id"`

	HDAccountID uint32 `gorm:"not null;index;uniqueIndex:ux_wallet_address_reservations_hd,priority:1" json:"hd_account_id"`
	HDAddressID uint32 `gorm:"not null;index;uniqueIndex:ux_wallet_address_reservations_hd,priority:2" json:"hd_address_id"`

	Purpose         string `gorm:"size:40;not null;default:'checkout';index;uniqueIndex:ux_wallet_address_reservations_owner,priority:5" json:"purpose"`
	LifecycleStatus string `gorm:"size:24;not null;default:'reserved';index" json:"lifecycle_status"`
	ReusePolicy     string `gorm:"size:24;not null;default:'fresh';index" json:"reuse_policy"`

	WalletID *uuid.UUID `gorm:"type:uuid;index" json:"wallet_id,omitempty"`

	ReservedAt time.Time  `gorm:"not null;index" json:"reserved_at"`
	AssignedAt *time.Time `gorm:"index" json:"assigned_at,omitempty"`
	ExpiresAt  *time.Time `gorm:"index" json:"expires_at,omitempty"`
	ReleasedAt *time.Time `gorm:"index" json:"released_at,omitempty"`
	RetiredAt  *time.Time `gorm:"index" json:"retired_at,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type WalletAddress struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	ChainID           constants.ChainID `gorm:"type:bigint;not null;uniqueIndex:ux_wallet_addresses_chain_address,priority:1;index;uniqueIndex:ux_wallet_addresses_hd_chain,priority:3" json:"chain_id"`
	ChainName         string            `gorm:"size:64;not null;index" json:"chain_name"`
	Address           string            `gorm:"size:180;not null" json:"address"`
	NormalizedAddress string            `gorm:"size:180;not null;uniqueIndex:ux_wallet_addresses_chain_address,priority:2;index" json:"normalized_address"`
	Asset             string            `gorm:"size:128;not null;default:'native';index" json:"asset"`

	MerchantID uuid.UUID `gorm:"type:uuid;not null;index" json:"merchant_id"`
	DomainID   uuid.UUID `gorm:"type:uuid;not null;index" json:"domain_id"`
	WalletID   uuid.UUID `gorm:"type:uuid;not null;index" json:"wallet_id"`
	ProductID  string    `gorm:"size:128;not null;index" json:"product_id"`
	UserID     string    `gorm:"size:128;not null;index" json:"user_id"`

	HDAccountID uint32 `gorm:"not null;index;uniqueIndex:ux_wallet_addresses_hd_chain,priority:1" json:"hd_account_id"`
	HDAddressID uint32 `gorm:"not null;index;uniqueIndex:ux_wallet_addresses_hd_chain,priority:2" json:"hd_address_id"`

	Purpose         string `gorm:"size:40;not null;default:'checkout';index" json:"purpose"`
	LifecycleStatus string `gorm:"size:24;not null;default:'generated';index" json:"lifecycle_status"`
	ReusePolicy     string `gorm:"size:24;not null;default:'fresh';index" json:"reuse_policy"`
	Source          string `gorm:"size:40;not null;default:'wallet_columns';index" json:"source"`

	ReservationID *uuid.UUID `gorm:"type:uuid;index" json:"reservation_id,omitempty"`

	ReservedAt  *time.Time `gorm:"index" json:"reserved_at,omitempty"`
	AssignedAt  *time.Time `gorm:"index" json:"assigned_at,omitempty"`
	ActivatedAt *time.Time `gorm:"index" json:"activated_at,omitempty"`
	UsedAt      *time.Time `gorm:"index" json:"used_at,omitempty"`
	ExpiresAt   *time.Time `gorm:"index" json:"expires_at,omitempty"`
	ReleasedAt  *time.Time `gorm:"index" json:"released_at,omitempty"`
	RetiredAt   *time.Time `gorm:"index" json:"retired_at,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type WalletAddressGapScanCursor struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	ChainID     constants.ChainID `gorm:"type:bigint;not null;index;uniqueIndex:ux_wallet_address_gap_scan_scope,priority:1" json:"chain_id"`
	ChainName   string            `gorm:"size:64;not null;index" json:"chain_name"`
	HDAccountID uint32            `gorm:"not null;index;uniqueIndex:ux_wallet_address_gap_scan_scope,priority:2" json:"hd_account_id"`
	Purpose     string            `gorm:"size:40;not null;default:'checkout';index;uniqueIndex:ux_wallet_address_gap_scan_scope,priority:3" json:"purpose"`

	Lookahead                 uint32    `gorm:"not null;default:20" json:"lookahead"`
	LastScannedIndex          uint32    `gorm:"not null;default:0" json:"last_scanned_index"`
	HighestUsedIndex          uint32    `gorm:"not null;default:0" json:"highest_used_index"`
	DiscoveredUsedIndexesJSON string    `gorm:"type:text;not null;default:'[]'" json:"discovered_used_indexes_json"`
	AnomalyCount              int64     `gorm:"not null;default:0" json:"anomaly_count"`
	LastAnomaly               string    `gorm:"size:256" json:"last_anomaly"`
	ScannedAt                 time.Time `gorm:"not null;index" json:"scanned_at"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type WalletAddressGapScanAnomaly struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	ChainID     constants.ChainID `gorm:"type:bigint;not null;index" json:"chain_id"`
	ChainName   string            `gorm:"size:64;not null;index" json:"chain_name"`
	HDAccountID uint32            `gorm:"not null;index" json:"hd_account_id"`
	HDAddressID uint32            `gorm:"not null;index" json:"hd_address_id"`
	Purpose     string            `gorm:"size:40;not null;default:'checkout';index" json:"purpose"`
	Address     string            `gorm:"size:180" json:"address"`
	Category    string            `gorm:"size:64;not null;index" json:"category"`
	Detail      string            `gorm:"size:512" json:"detail"`
	DetectedAt  time.Time         `gorm:"not null;index" json:"detected_at"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
