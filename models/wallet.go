package models

import (
	"time"

	"github.com/google/uuid"
)

type Wallet struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	HDAccountID uint32 `gorm:"not null;uniqueIndex:ux_wallet_hd" json:"hd_account_id"`
	HDAddressId uint32 `gorm:"not null;uniqueIndex:ux_wallet_hd" json:"hd_address_id"`

	MerchantID uuid.UUID `gorm:"type:uuid;not null;index;uniqueIndex:ux_wallet_hd;uniqueIndex:ux_wallet_owner" json:"merchant_id"`
	Merchant   Merchant  `gorm:"constraint:OnDelete:CASCADE;" json:"-"`

	DomainID uuid.UUID `gorm:"type:uuid;not null;index;uniqueIndex:ux_wallet_hd;uniqueIndex:ux_wallet_owner" json:"domain_id"`
	Domain   Domain    `gorm:"constraint:OnDelete:CASCADE;" json:"-"`

	ProductID string `gorm:"size:128;index;uniqueIndex:ux_wallet_owner" json:"product_id"`
	UserID    string `gorm:"size:128;index;uniqueIndex:ux_wallet_owner" json:"user_id"`

	BitcoinAddress      string `gorm:"size:128;uniqueIndex" json:"bitcoin"`
	EthereumAddress     string `gorm:"size:128;uniqueIndex" json:"ethereum"`
	AvalancheAddress    string `gorm:"size:128;uniqueIndex" json:"avalanche"`
	BinanceAddress      string `gorm:"size:128;uniqueIndex" json:"bnbchain"`
	BaseAddress         string `gorm:"size:128;uniqueIndex" json:"base"`
	UnichainAddress     string `gorm:"size:128;uniqueIndex" json:"unichain"`
	TronAddress         string `gorm:"size:128;uniqueIndex" json:"tron"`
	SolanaAddress       string `gorm:"size:128;uniqueIndex" json:"solana"`
	ChilizAddress       string `gorm:"size:128;uniqueIndex" json:"chiliz"`
	ChilizSpicyAddress  string `gorm:"size:128;uniqueIndex" json:"chiliz_spicy"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}
