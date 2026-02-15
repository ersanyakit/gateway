package models

import (
	"core/constants"
	"time"

	"github.com/google/uuid"
)

type Block struct {
	ID         uuid.UUID         `gorm:"type:uuid;default:uuid_generate_v4();primaryKey"`
	ChainID    constants.ChainID `gorm:"type:bigint;index;not null"`
	Number     int64             `gorm:"index;not null"` // block number
	Hash       string            `gorm:"type:varchar(66);uniqueIndex"`
	ParentHash string            `gorm:"type:varchar(66)"`
	Timestamp  time.Time         `gorm:"index"`
	Processed  bool              `gorm:"index;default:false"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
