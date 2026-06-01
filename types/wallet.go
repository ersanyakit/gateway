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
