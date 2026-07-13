package models

import (
	"time"

	"core/constants"

	"github.com/google/uuid"
)

const (
	OutboundResourceReservationNonce    = "nonce"
	OutboundResourceReservationUTXO     = "utxo"
	OutboundResourceReservationSequence = "sequence"
)

const (
	OutboundResourceReservationReserved = "reserved"
	OutboundResourceReservationConsumed = "consumed"
	OutboundResourceReservationReleased = "released"
)

type OutboundChainResourceReservation struct {
	ID                    uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OutboundTransactionID uuid.UUID `gorm:"type:uuid;not null;index" json:"outbound_transaction_id"`

	ResourceType string `gorm:"size:32;not null;index:idx_outbound_resource_type_status,priority:1" json:"resource_type"`
	ResourceKey  string `gorm:"size:260;not null;index" json:"resource_key"`
	Status       string `gorm:"size:32;not null;index:idx_outbound_resource_type_status,priority:2" json:"status"`

	ChainID       constants.ChainID `gorm:"type:bigint;not null;index" json:"chain_id"`
	ChainName     string            `gorm:"size:64;not null;index" json:"chain_name"`
	WalletID      uuid.UUID         `gorm:"type:uuid;not null;index" json:"wallet_id"`
	WalletAddress string            `gorm:"size:180;not null;index" json:"wallet_address"`
	OwnerType     string            `gorm:"size:40;not null;index:idx_outbound_resource_owner,priority:1" json:"owner_type"`
	OwnerID       uuid.UUID         `gorm:"type:uuid;not null;index:idx_outbound_resource_owner,priority:2" json:"owner_id"`
	Intent        string            `gorm:"size:120" json:"intent,omitempty"`

	Nonce        *uint64 `gorm:"index" json:"nonce,omitempty"`
	UTXOTxID     string  `gorm:"size:160;index" json:"utxo_txid,omitempty"`
	UTXOVout     *uint32 `gorm:"index" json:"utxo_vout,omitempty"`
	UTXOValueRaw string  `gorm:"type:text" json:"utxo_value_raw,omitempty"`

	LeaseExpiresAt *time.Time `gorm:"index" json:"lease_expires_at,omitempty"`
	ConsumedAt     *time.Time `gorm:"index" json:"consumed_at,omitempty"`
	ReleasedAt     *time.Time `gorm:"index" json:"released_at,omitempty"`
	TxHash         string     `gorm:"size:160;index" json:"tx_hash,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
