package types

import (
	"context"
	"core/constants"

	"github.com/google/uuid"
)

type TransactionParam struct {
	Context context.Context `json:"-"`

	ID         *uuid.UUID        `json:"id,omitempty"`
	ExternalID *string           `json:"external_id,omitempty"` //chainId,txHash,logIndex hash
	ChainID    constants.ChainID `json:"chain_id,omitempty"`
	Hash       *string           `json:"hash,omitempty"`
	Block      *string           `json:"block,omitempty"`

	Token  *string `json:"token,omitempty"`
	Symbol *string `json:"symbol,omitempty"`

	From   *string `json:"from,omitempty"`
	To     *string `json:"to,omitempty"`
	Amount *string `json:"amount,omitempty"`

	LogIndex *string `json:"log_index,omitempty"`
	Status   *string `json:"status,omitempty"` // pending, confirmed, failed
	GasUsed  *string `json:"gas_used,omitempty"`
	GasPrice *string `json:"gas_price,omitempty"`
}
