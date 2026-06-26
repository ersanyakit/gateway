package models

import (
	"core/constants"
	"time"

	"github.com/google/uuid"
)

type PriceQuote struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	PaymentID uuid.UUID         `gorm:"type:uuid;not null;index" json:"payment_id"`
	ChainID   constants.ChainID `gorm:"type:bigint;not null;index" json:"chain_id"`
	Token     *string           `gorm:"type:varchar(128);index" json:"token,omitempty"`
	Symbol    string            `gorm:"size:20;not null;index" json:"symbol"`
	Decimals  uint8             `json:"decimals"`

	FiatCurrency      string `gorm:"size:20;not null;index" json:"fiat_currency"`
	FiatAmount        string `gorm:"size:80;not null" json:"fiat_amount"`
	ExpectedAmountRaw string `gorm:"type:text;not null" json:"expected_amount_raw"`
	PriceSource       string `gorm:"size:80;not null" json:"price_source"`
	Price             string `gorm:"size:80;not null" json:"price"`

	QuotedAt  time.Time `gorm:"not null;index" json:"quoted_at"`
	ExpiresAt time.Time `gorm:"not null;index" json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
