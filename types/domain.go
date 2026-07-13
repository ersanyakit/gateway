package types

import (
	"context"
	"errors"
	"strings"
)

type DomainParams struct {
	Context            context.Context `json:"-"`
	MerchantID         *string         `json:"merchant_id"`
	DomainURL          *string         `json:"domain_url"`
	NotificationMode   string          `json:"notification_mode,omitempty"`
	NotificationMethod string          `json:"notification_method,omitempty"`
	WebhookURL         *string         `json:"webhook_url,omitempty"`
	WebhookSecret      *string         `json:"webhook_secret,omitempty"`
	NATSURL            *string         `json:"nats_url,omitempty"`
	NATSSubject        *string         `json:"nats_subject,omitempty"`

	DomainID  *string `json:"domain_id"`
	APIKey    *string `json:"api_key,omitempty"`
	APISecret *string `json:"api_secret,omitempty"`
}

type DomainCreateResponse struct {
	ID               string `json:"id"`
	MerchantID       string `json:"merchant_id"`
	DomainURL        string `json:"domain_url"`
	KeyID            string `json:"key_id"`
	APIKey           string `json:"api_key"`
	APISecret        string `json:"api_secret,omitempty"`
	HDAccountID      uint32 `json:"hd_account_id"`
	NotificationMode string `json:"notification_mode"`
	WebhookURL       string `json:"webhook_url,omitempty"`
	NATSURL          string `json:"nats_url,omitempty"`
	NATSSubject      string `json:"nats_subject,omitempty"`
	IsEnabled        bool   `json:"is_enabled"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

func (d *DomainParams) Validate() error {
	if d.MerchantID == nil || *d.MerchantID == "" {
		return errors.New("MerchantID is required")
	}
	if d.DomainURL == nil || *d.DomainURL == "" {
		return errors.New("DomainURL is required")
	}
	mode := strings.ToLower(strings.TrimSpace(d.NotificationMode))
	if mode == "" {
		mode = strings.ToLower(strings.TrimSpace(d.NotificationMethod))
	}
	if mode == "" {
		mode = "webhook"
	}
	if mode != "webhook" && mode != "nats" {
		return errors.New("NotificationMode must be webhook or nats")
	}
	d.NotificationMode = mode
	if mode == "webhook" {
		if d.WebhookURL == nil || strings.TrimSpace(*d.WebhookURL) == "" {
			return errors.New("WebhookURL is required for webhook notification mode")
		}
		if d.WebhookSecret == nil || strings.TrimSpace(*d.WebhookSecret) == "" {
			return errors.New("WebhookSecret is required for webhook notification mode")
		}
	} else if d.NATSURL == nil || strings.TrimSpace(*d.NATSURL) == "" {
		return errors.New("NATSURL is required for nats notification mode")
	}
	return nil
}
