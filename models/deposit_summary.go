package models

import (
	"core/constants"
	"time"

	"github.com/google/uuid"
)

type DepositSummary struct {
	DomainID  uuid.UUID         `json:"domain_id"`
	ProductID string            `json:"product_id,omitempty"`
	UserID    string            `json:"user_id,omitempty"`
	ChainID   constants.ChainID `json:"chain_id"`
	Token     *string           `json:"token,omitempty"`
	Symbol    string            `json:"symbol"`
	Decimals  uint8             `json:"decimals"`

	AmountRaw        string     `json:"amount_raw"`
	TransactionCount int64      `json:"transaction_count"`
	UserCount        int64      `json:"user_count"`
	FirstDepositAt   *time.Time `json:"first_deposit_at,omitempty"`
	LastDepositAt    *time.Time `json:"last_deposit_at,omitempty"`
}
