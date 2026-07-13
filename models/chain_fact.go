package models

import (
	"time"

	"core/constants"

	"github.com/google/uuid"
)

const (
	ChainFactDirectionTo      = "to"
	ChainFactDirectionFrom    = "from"
	ChainFactDirectionUnknown = "unknown"

	ChainFactObservationConfirmed = "confirmed"
	ChainFactObservationMempool   = "mempool"

	ChainFactStatusObserved   = "observed"
	ChainFactStatusIgnored    = "ignored"
	ChainFactStatusReorged    = "reorged"
	ChainFactStatusSuperseded = "superseded"
)

type ChainFact struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	EventID string            `gorm:"size:256;not null;uniqueIndex:ux_chain_facts_event_id" json:"event_id"`
	ChainID constants.ChainID `gorm:"type:bigint;not null;index" json:"chain_id"`

	BlockNumber int64  `gorm:"not null;index" json:"block_number"`
	BlockHash   string `gorm:"size:128;index" json:"block_hash,omitempty"`
	TxHash      string `gorm:"size:128;not null;index" json:"tx_hash"`
	LogIndex    string `gorm:"size:80;not null;index" json:"log_index"`

	ObservedAddress   string `gorm:"size:160;not null;index" json:"observed_address"`
	Direction         string `gorm:"size:16;not null;index" json:"direction"`
	ObservationStatus string `gorm:"size:32;not null;default:'confirmed';index" json:"observation_status"`
	Memo              string `gorm:"size:180;index" json:"memo,omitempty"`
	MemoNormalized    string `gorm:"size:180;index" json:"memo_normalized,omitempty"`

	Token     *string `gorm:"size:160;index" json:"token,omitempty"`
	Symbol    string  `gorm:"size:32;not null;index" json:"symbol"`
	Decimals  uint8   `gorm:"not null" json:"decimals"`
	AmountRaw string  `gorm:"type:text;not null" json:"amount_raw"`

	Confirmations         uint       `gorm:"not null;default:0" json:"confirmations"`
	ConfirmationsRequired uint       `gorm:"not null;default:0" json:"confirmations_required"`
	Finalized             bool       `gorm:"not null;default:false;index" json:"finalized"`
	Status                string     `gorm:"size:32;not null;default:'observed';index" json:"status"`
	ReorgedAt             *time.Time `gorm:"index" json:"reorged_at,omitempty"`
	SupersededByEventID   string     `gorm:"size:256;index" json:"superseded_by_event_id,omitempty"`
	CorrectionReason      string     `gorm:"size:256" json:"correction_reason,omitempty"`
	SourceEventType       string     `gorm:"size:80;not null;index" json:"source_event_type"`
	RawMetadataJSON       string     `gorm:"type:jsonb" json:"raw_metadata_json,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
