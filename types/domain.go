package types

import (
	"errors"
)

type DomainParams struct {
	MerchantID    *string `json:"merchant_id"`
	DomainURL     *string `json:"domain_url"`
	WebhookURL    *string `json:"webhook_url,omitempty"`
	WebhookSecret *string `json:"webhook_secret,omitempty"`
}

func (d *DomainParams) Validate() error {
	if d.MerchantID == nil || *d.MerchantID == "" {
		return errors.New("MerchantID is required")
	}
	if d.DomainURL == nil || *d.DomainURL == "" {
		return errors.New("DomainURL is required")
	}
	return nil
}
