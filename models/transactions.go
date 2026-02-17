package models

import (
	"core/constants"
	"time"

	"github.com/google/uuid"
)

type Transaction struct {
	ID         uuid.UUID         `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	ChainID    constants.ChainID `gorm:"type:bigint;not null;index" json:"chain_id"`
	UniqueHash string            `gorm:"type:varchar(128);uniqueIndex" json:"unique_hash"`

	Hash        string  `gorm:"type:varchar(66);not null;index" json:"hash"`
	LogIndex    *string `json:"log_index,omitempty"`
	BlockNumber string  `gorm:"not null;index" json:"block_number"`
	BlockHash   string  `gorm:"type:varchar(66);index" json:"block_hash"`

	Token  *string `gorm:"type:varchar(42);index" json:"asset_address,omitempty"`
	Symbol string  `gorm:"type:varchar(20);not null" json:"symbol"`

	FromAddress string `gorm:"type:varchar(42);not null;index" json:"from_address"`
	ToAddress   string `gorm:"type:varchar(42);not null;index" json:"to_address"`
	Amount      string `gorm:"type:text;not null" json:"amount"`

	Status string `gorm:"type:varchar(20);not null;index" json:"status"` // pending, confirmed, failed vs.

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
