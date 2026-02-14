package models

import (
	"time"

	"github.com/google/uuid"
)

type Wallet struct {
	ID          uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	HDAddressId uint32    `gorm:"not null;uniqueIndex" json:"hd_address_id"`

	MerchantID uuid.UUID `gorm:"type:uuid;not null;index" json:"merchant_id"`
	Merchant   Merchant  `gorm:"constraint:OnDelete:CASCADE;" json:"-"`

	DomainID uuid.UUID `gorm:"type:uuid;not null;index" json:"domain_id"`
	Domain   Domain    `gorm:"constraint:OnDelete:CASCADE;" json:"-"`

	BitcoinAddress   string    `gorm:"size:128;uniqueIndex;not null" json:"bitcoin"`
	EthereumAddress  string    `gorm:"size:128;uniqueIndex;not null" json:"ethereum"`
	AvalancheAddress string    `gorm:"size:128;uniqueIndex;not null" json:"avalanche"`
	TronAddress      string    `gorm:"size:128;uniqueIndex;not null" json:"tron"`
	SolanaAddress    string    `gorm:"size:128;uniqueIndex;not null" json:"solana"`
	ChilizAddress    string    `gorm:"size:128;uniqueIndex;not null" json:"chiliz"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}
