package models

import (
	"time"

	"github.com/google/uuid"
)

type Wallet struct {
	ID             uuid.UUID `gorm:"type:uuid;default:uuid_generate_v7();primaryKey" json:"id"`
	HDAddressIndex uint32    `gorm:"not null;default:nextval('wallet_hd_address_seq');uniqueIndex"`
	DomainID       uuid.UUID `gorm:"type:uuid;not null;index" json:"domain_id"`
	Domain         Domain    `gorm:"constraint:OnDelete:CASCADE;" json:"-"`

	BitcoinAddress   string    `gorm:"size:128;uniqueIndex;not null" json:"bitcoin"`
	EthereumAddress  string    `gorm:"size:128;uniqueIndex;not null" json:"ethereum"`
	AvalancheAddress string    `gorm:"size:128;uniqueIndex;not null" json:"avalanche"`
	TronAddress      string    `gorm:"size:128;uniqueIndex;not null" json:"tron"`
	SolanaAddress    string    `gorm:"size:128;uniqueIndex;not null" json:"solana"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}
