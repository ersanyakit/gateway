package types

import (
	"context"
	"errors"
	"strings"

	"core/constants"

	"github.com/google/uuid"
)

type DepositSummaryParams struct {
	Context context.Context `json:"-"`

	MerchantID *string            `json:"merchant_id,omitempty"`
	DomainID   *string            `json:"domain_id,omitempty"`
	ProductID  *string            `json:"product_id,omitempty"`
	UserID     *string            `json:"user_id,omitempty"`
	ChainID    *constants.ChainID `json:"chain_id,omitempty"`
	Symbol     *string            `json:"symbol,omitempty"`

	GroupByUser *bool `json:"group_by_user,omitempty"`
}

func (p *DepositSummaryParams) Validate() error {
	if p.DomainID == nil || strings.TrimSpace(*p.DomainID) == "" {
		return errors.New("DomainID is required")
	}
	if _, err := uuid.Parse(strings.TrimSpace(*p.DomainID)); err != nil {
		return errors.New("invalid DomainID format")
	}
	if p.MerchantID != nil && strings.TrimSpace(*p.MerchantID) != "" {
		if _, err := uuid.Parse(strings.TrimSpace(*p.MerchantID)); err != nil {
			return errors.New("invalid MerchantID format")
		}
	}

	trim := func(value *string) *string {
		if value == nil {
			return nil
		}
		v := strings.TrimSpace(*value)
		if v == "" {
			return nil
		}
		return &v
	}

	p.DomainID = trim(p.DomainID)
	p.MerchantID = trim(p.MerchantID)
	p.ProductID = trim(p.ProductID)
	p.UserID = trim(p.UserID)
	p.Symbol = trim(p.Symbol)

	return nil
}

func (p *DepositSummaryParams) ShouldGroupByUser() bool {
	return p.GroupByUser != nil && *p.GroupByUser
}
