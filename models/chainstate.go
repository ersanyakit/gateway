package models

import (
	"core/constants"
	"time"
)

type ChainState struct {
	ChainID                 constants.ChainID `gorm:"primaryKey;type:bigint" json:"chain_id"`
	LastProcessedBlock      int64             `json:"last_processed_block"`
	LastProcessedHash       string            `gorm:"size:128" json:"last_processed_hash,omitempty"`
	LastProcessedParentHash string            `gorm:"size:128" json:"last_processed_parent_hash,omitempty"`
	LastConfirmedBlock      int64             `json:"last_confirmed_block"`
	ScannerStartBlock       int64             `json:"scanner_start_block,omitempty"`
	ScannerStartPolicy      string            `gorm:"size:32" json:"scanner_start_policy,omitempty"`
	ContinuityStatus        string            `gorm:"size:32" json:"continuity_status,omitempty"`
	ContinuityReason        string            `gorm:"size:256" json:"continuity_reason,omitempty"`
	UpdatedAt               time.Time         `json:"updated_at" gorm:"autoUpdateTime"`
}
