package types

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
)

type WalletParams struct {
	Context    context.Context `json:"-"`
	MerchantId *string         `json:"merchant_id,omitempty"`
	DomainId   *string         `json:"domain_id,omitempty"`
	ProductId  *string         `json:"product_id,omitempty"`
	UserId     *string         `json:"user_id,omitempty"`
}

type WalletCreateResponse struct {
	ID               string `json:"id"`
	HDAccountID      uint32 `json:"hd_account_id"`
	HDAddressID      uint32 `json:"hd_address_id"`
	MerchantID       string `json:"merchant_id"`
	DomainID         string `json:"domain_id"`
	ProductID        string `json:"product_id"`
	UserID           string `json:"user_id"`
	BitcoinAddress   string `json:"bitcoin"`
	EthereumAddress  string `json:"ethereum"`
	AvalancheAddress string `json:"avalanche"`
	BinanceAddress   string `json:"bnbchain"`
	BaseAddress      string `json:"base"`
	UnichainAddress  string `json:"unichain"`
	TronAddress      string `json:"tron"`
	SolanaAddress    string `json:"solana"`
	ChilizAddress    string `json:"chiliz"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

func (wp *WalletParams) Validate() error {
	if wp.MerchantId == nil || *wp.MerchantId == "" {
		return errors.New("MerchantId is required")
	}
	if wp.DomainId == nil || *wp.DomainId == "" {
		return errors.New("DomainId is required")
	}
	if wp.ProductId == nil || strings.TrimSpace(*wp.ProductId) == "" {
		return errors.New("ProductId is required")
	}
	if wp.UserId == nil || strings.TrimSpace(*wp.UserId) == "" {
		return errors.New("UserId is required")
	}
	if _, err := uuid.Parse(*wp.MerchantId); err != nil {
		return errors.New("invalid MerchantId format")
	}
	if _, err := uuid.Parse(*wp.DomainId); err != nil {
		return errors.New("invalid DomainId format")
	}

	productID := strings.TrimSpace(*wp.ProductId)
	userID := strings.TrimSpace(*wp.UserId)
	wp.ProductId = &productID
	wp.UserId = &userID

	return nil
}
