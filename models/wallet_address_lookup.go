package models

import (
	"core/constants"
	"time"

	"github.com/google/uuid"
)

type WalletAddressLookup struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	ChainID           constants.ChainID `gorm:"type:bigint;not null;uniqueIndex:ux_wallet_address_lookup_chain_address,priority:1;index" json:"chain_id"`
	ChainName         string            `gorm:"size:64;not null;index" json:"chain_name"`
	Address           string            `gorm:"size:180;not null" json:"address"`
	NormalizedAddress string            `gorm:"size:180;not null;uniqueIndex:ux_wallet_address_lookup_chain_address,priority:2;index" json:"normalized_address"`
	Asset             string            `gorm:"size:128;not null;default:'native';index" json:"asset"`

	MerchantID uuid.UUID `gorm:"type:uuid;not null;index" json:"merchant_id"`
	DomainID   uuid.UUID `gorm:"type:uuid;not null;index" json:"domain_id"`
	WalletID   uuid.UUID `gorm:"type:uuid;not null;index" json:"wallet_id"`
	ProductID  string    `gorm:"size:128;index" json:"product_id"`
	UserID     string    `gorm:"size:128;index" json:"user_id"`
	Source     string    `gorm:"size:40;not null;default:'wallet_columns';index" json:"source"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
