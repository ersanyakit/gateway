package models

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	PaymentLinkTypeFixed    = "fixed"
	PaymentLinkTypeDonation = "donation"
)

func NormalizePaymentLinkType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case PaymentLinkTypeDonation:
		return PaymentLinkTypeDonation
	default:
		return PaymentLinkTypeFixed
	}
}

func IsDonationLinkType(value string) bool {
	return NormalizePaymentLinkType(value) == PaymentLinkTypeDonation
}

type Product struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	MerchantID uuid.UUID `gorm:"type:uuid;not null;index" json:"merchant_id"`
	Merchant   Merchant  `gorm:"constraint:OnDelete:CASCADE;" json:"-"`
	DomainID   uuid.UUID `gorm:"type:uuid;not null;index" json:"domain_id"`
	Domain     Domain    `gorm:"constraint:OnDelete:CASCADE;" json:"-"`

	Name        string `gorm:"size:180;not null" json:"name"`
	Description string `gorm:"size:500" json:"description,omitempty"`
	LinkType    string `gorm:"size:32;not null;default:'fixed';index" json:"link_type"`
	Amount      string `gorm:"size:80;not null" json:"amount"`
	Currency    string `gorm:"size:20;not null;index" json:"currency"`
	Language    string `gorm:"size:8;not null;default:'tr'" json:"language"`
	SuccessURL  string `gorm:"size:500" json:"success_url,omitempty"`
	CancelURL   string `gorm:"size:500" json:"cancel_url,omitempty"`

	DefaultChainID *int64  `gorm:"type:bigint;index" json:"default_chain_id,omitempty"`
	DefaultSymbol  string  `gorm:"size:32;index" json:"default_symbol,omitempty"`
	DefaultToken   *string `gorm:"size:255" json:"default_token,omitempty"`

	X402Enabled bool `gorm:"not null;default:false;index" json:"x402_enabled"`

	LogoURL   string `gorm:"size:500" json:"logo_url,omitempty"`
	LinkToken string `gorm:"size:80;uniqueIndex;not null" json:"link_token"`
	IsActive  bool   `gorm:"not null;default:true" json:"is_active"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PaymentProductSnapshot is the immutable product context copied into a
// payment session when a payment link is opened. It keeps webhook consumers
// independent from later edits to (or deletion of) the payment link.
type PaymentProductSnapshot struct {
	ID           string                       `json:"id"`
	LinkToken    string                       `json:"link_token,omitempty"`
	Name         string                       `json:"name"`
	Description  string                       `json:"description,omitempty"`
	LinkType     string                       `json:"link_type"`
	Amount       string                       `json:"amount"`
	Currency     string                       `json:"currency"`
	Language     string                       `json:"language"`
	SuccessURL   string                       `json:"success_url,omitempty"`
	CancelURL    string                       `json:"cancel_url,omitempty"`
	LogoURL      string                       `json:"logo_url,omitempty"`
	X402Enabled  bool                         `json:"x402_enabled"`
	DefaultAsset *PaymentProductAssetSnapshot `json:"default_asset,omitempty"`
}

type PaymentProductAssetSnapshot struct {
	ChainID int64  `json:"chain_id"`
	Symbol  string `json:"symbol"`
	Token   string `json:"token,omitempty"`
}

func NewPaymentProductSnapshot(product Product) PaymentProductSnapshot {
	snapshot := PaymentProductSnapshot{
		ID:          product.ID.String(),
		LinkToken:   product.LinkToken,
		Name:        product.Name,
		Description: product.Description,
		LinkType:    NormalizePaymentLinkType(product.LinkType),
		Amount:      product.Amount,
		Currency:    product.Currency,
		Language:    product.Language,
		SuccessURL:  product.SuccessURL,
		CancelURL:   product.CancelURL,
		LogoURL:     product.LogoURL,
		X402Enabled: product.X402Enabled,
	}
	if product.DefaultChainID != nil {
		snapshot.DefaultAsset = &PaymentProductAssetSnapshot{
			ChainID: *product.DefaultChainID,
			Symbol:  strings.ToUpper(strings.TrimSpace(product.DefaultSymbol)),
		}
		if product.DefaultToken != nil {
			snapshot.DefaultAsset.Token = strings.TrimSpace(*product.DefaultToken)
		}
	}
	return snapshot
}

func MarshalPaymentProductSnapshot(product Product) (JSONData, error) {
	raw, err := json.Marshal(NewPaymentProductSnapshot(product))
	if err != nil {
		return "", err
	}
	return JSONData(raw), nil
}
