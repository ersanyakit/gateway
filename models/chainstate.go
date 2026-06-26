package models

import (
	"core/constants"
	"time"
)

type ChainState struct {
	ChainID            constants.ChainID `gorm:"primaryKey;type:bigint" json:"chain_id"`
	LastProcessedBlock int64             `json:"last_processed_block"`
	LastConfirmedBlock int64             `json:"last_confirmed_block"`
	UpdatedAt          time.Time         `json:"updated_at" gorm:"autoUpdateTime"`
}
