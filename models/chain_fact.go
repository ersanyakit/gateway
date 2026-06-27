package models

import (
	"time"

	"core/constants"

	"github.com/google/uuid"
)

const (
	ChainFactDirectionInbound  = "inbound"
	ChainFactDirectionOutbound = "outbound"
	ChainFactDirectionUnknown  = "unknown"
)

type ChainFact struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	EventID   string            `gorm:"size:256;not null;uniqueIndex:ux_chain_facts_event_id" json:"event_id"`
	ChainID   constants.ChainID `gorm:"type:bigint;not null;index" json:"chain_id"`
	EventType string            `gorm:"size:80;not null;index" json:"event_type"`

	BlockNumber int64  `gorm:"not null;index" json:"block_number"`
	BlockHash   string `gorm:"size:128;index" json:"block_hash,omitempty"`
	TxHash      string `gorm:"size:128;not null;index" json:"tx_hash"`
	LogIndex    string `gorm:"size:80;not null;index" json:"log_index"`

	ObservedAddress string `gorm:"size:128;index" json:"observed_address,omitempty"`
	Direction       string `gorm:"size:16;not null;index" json:"direction"`

	Token    string `gorm:"size:128;index" json:"token,omitempty"`
	Symbol   string `gorm:"size:20;not null" json:"symbol"`
	Decimals uint8  `json:"decimals,omitempty"`
	Amount   string `gorm:"type:text;not null" json:"amount"`

	FinalityStatus        string `gorm:"size:32;index" json:"finality_status,omitempty"`
	Confirmations         uint   `gorm:"not null;default:0" json:"confirmations"`
	ConfirmationsRequired uint   `gorm:"not null;default:1" json:"confirmations_required"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
