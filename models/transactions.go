package models

import (
	"core/constants"
	"time"

	"github.com/google/uuid"
)

type Transaction struct {
	ID      uuid.UUID         `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	ChainID constants.ChainID `gorm:"type:bigint;not null;index" json:"chain_id"`

	Hash     string `gorm:"type:varchar(66);not null;index" json:"hash"`
	LogIndex *uint  `gorm:"index" json:"log_index,omitempty"` // ERC20 için

	BlockNumber int64  `gorm:"not null;index" json:"block_number"`
	BlockHash   string `gorm:"type:varchar(66);index" json:"block_hash"`

	AssetAddress *string `gorm:"type:varchar(42);index" json:"asset_address,omitempty"` // ERC20 contract
	Symbol       string  `gorm:"type:varchar(20);not null" json:"symbol"`

	FromAddress string `gorm:"type:varchar(42);not null;index" json:"from_address"`
	ToAddress   string `gorm:"type:varchar(42);not null;index" json:"to_address"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
