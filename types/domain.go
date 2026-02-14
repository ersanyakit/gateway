package types

import (
	"context"
	"errors"
)

type DomainParams struct {
	Context       context.Context `json:"-"`
	MerchantID    *string         `json:"merchant_id"`
	DomainURL     *string         `json:"domain_url"`
	WebhookURL    *string         `json:"webhook_url,omitempty"`
	WebhookSecret *string         `json:"webhook_secret,omitempty"`

	DomainID  *string `json:"domain_id"`
	APIKey    *string `json:"api_key,omitempty"`
	APISecret *string `json:"api_secret,omitempty"`
}

func (d *DomainParams) Validate() error {
	if d.MerchantID == nil || *d.MerchantID == "" {
		return errors.New("MerchantID is required")
	}
	if d.DomainURL == nil || *d.DomainURL == "" {
		return errors.New("DomainURL is required")
	}
	if d.WebhookURL == nil || *d.WebhookURL == "" {
		return errors.New("WebhookURL is required")
	}
	if d.WebhookSecret == nil || *d.WebhookSecret == "" {
		return errors.New("WebhookSecret is required")
	}
	return nil
}
