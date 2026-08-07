package models

import (
	"core/constants"
	"time"

	"github.com/google/uuid"
)

const (
	BlockStatusCanonical = "canonical"
	BlockStatusReorged   = "reorged"
)

type Block struct {
	ID         uuid.UUID         `gorm:"type:uuid;default:uuid_generate_v4();primaryKey"`
	ChainID    constants.ChainID `gorm:"type:bigint;index;not null;uniqueIndex:ux_blocks_chain_hash,priority:1;uniqueIndex:ux_blocks_chain_number_hash,priority:1;uniqueIndex:ux_blocks_one_canonical_height,priority:1,where:canonical = true"`
	Number     int64             `gorm:"index;not null;uniqueIndex:ux_blocks_chain_number_hash,priority:2;uniqueIndex:ux_blocks_one_canonical_height,priority:2,where:canonical = true"` // block number
	Hash       string            `gorm:"type:varchar(128);not null;index;uniqueIndex:ux_blocks_chain_hash,priority:2;uniqueIndex:ux_blocks_chain_number_hash,priority:3"`
	ParentHash string            `gorm:"type:varchar(128);index"`
	Timestamp  time.Time         `gorm:"index"`
	Processed  bool              `gorm:"index;default:false"`
	Canonical  bool              `gorm:"not null;default:true;index"`
	Status     string            `gorm:"type:varchar(32);not null;default:canonical;index"`
	ReorgedAt  *time.Time

	SupersededByHash string `gorm:"type:varchar(128);index"`
	CorrectionReason string `gorm:"size:256"`

	CreatedAt time.Time
	UpdatedAt time.Time
}
