package types

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

type WalletParams struct {
	Context    context.Context `json:"-"`
	MerchantId *string         `json:"merchant_id,omitempty"`
	DomainId   *string         `json:"domain_id,omitempty"`
}

func (wp *WalletParams) Validate() error {
	if wp.MerchantId == nil || *wp.MerchantId == "" {
		return errors.New("MerchantId is required")
	}
	if wp.DomainId == nil || *wp.DomainId == "" {
		return errors.New("DomainId is required")
	}
	if _, err := uuid.Parse(*wp.MerchantId); err != nil {
		return errors.New("invalid MerchantId format")
	}
	if _, err := uuid.Parse(*wp.DomainId); err != nil {
		return errors.New("invalid DomainId format")
	}

	return nil
}
