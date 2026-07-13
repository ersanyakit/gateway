package models

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	DomainAPIScopeRead              = "read"
	DomainAPIScopePaymentCreate     = "payment:create"
	DomainAPIScopeWalletCreate      = "wallet:create"
	DomainAPIScopePayoutCreate      = "payout:create"
	DomainAPIScopeRefundCreate      = "refund:create"
	DomainAPIScopeWebhookManage     = "webhook:manage"
	DomainAPIScopeTransactionRescan = "transaction:rescan"
	DomainAPIScopeAll               = "*"

	DomainAPISecretRotationImmediateRevoke = "immediate_revoke"
)

const (
	DomainNotificationWebhook = "webhook"
	DomainNotificationNATS    = "nats"
	DefaultNATSSubject        = "gateway.events"
)

type Domain struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey"`

	MerchantID uuid.UUID `gorm:"type:uuid;not null;index"`
	Merchant   Merchant  `gorm:"constraint:OnDelete:CASCADE;"`

	DomainURL string `gorm:"size:255;not null"`

	KeyID                   string     `gorm:"size:32;index"`
	APIKey                  string     `gorm:"size:128;uniqueIndex;not null"`
	APISecret               string     `gorm:"size:256;not null" json:"-"`
	APISecretPlain          string     `gorm:"-" json:"api_secret,omitempty"`
	APIScopes               string     `gorm:"type:text;not null;default:''" json:"api_scopes,omitempty"`
	APIIPAllowlist          string     `gorm:"type:text" json:"api_ip_allowlist,omitempty"`
	APISecretLastRotatedAt  *time.Time `json:"api_secret_last_rotated_at,omitempty"`
	APISecretRevokedAt      *time.Time `json:"api_secret_revoked_at,omitempty"`
	APISecretRotationPolicy string     `gorm:"size:40;not null;default:'immediate_revoke'" json:"api_secret_rotation_policy,omitempty"`

	HDAccountID uint32 `gorm:"not null;uniqueIndex"`

	NotificationMode string `gorm:"size:16;not null;default:'webhook';index" json:"notification_mode,omitempty"`
	WebhookURL       string `gorm:"size:500" json:"webhook_url,omitempty"`
	WebhookSecret    string `gorm:"size:256" json:"-"`
	NATSURL          string `gorm:"size:500" json:"nats_url,omitempty"`
	NATSSubject      string `gorm:"size:255" json:"nats_subject,omitempty"`
	IsEnabled        bool   `json:"is_enabled" gorm:"-"`

	CreatedAt time.Time
	UpdatedAt time.Time
}

func NormalizeDomainNotificationMode(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), DomainNotificationNATS) {
		return DomainNotificationNATS
	}
	return DomainNotificationWebhook
}

func (d Domain) EffectiveNotificationMode() string {
	return NormalizeDomainNotificationMode(d.NotificationMode)
}

func (d Domain) UsesNATS() bool {
	return d.EffectiveNotificationMode() == DomainNotificationNATS
}

func (d Domain) EffectiveNATSSubject() string {
	if subject := strings.TrimSpace(d.NATSSubject); subject != "" {
		return subject
	}
	return DefaultNATSSubject
}

func DefaultDomainAPIScopes() []string {
	return []string{
		DomainAPIScopeRead,
		DomainAPIScopePaymentCreate,
		DomainAPIScopeWalletCreate,
		DomainAPIScopePayoutCreate,
		DomainAPIScopeRefundCreate,
		DomainAPIScopeWebhookManage,
		DomainAPIScopeTransactionRescan,
	}
}

func DefaultDomainAPIScopesCSV() string {
	return strings.Join(DefaultDomainAPIScopes(), ",")
}

type APIRateLimitCounter struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	KeyHash string    `gorm:"size:96;not null;uniqueIndex" json:"key_hash"`
	Count   int       `gorm:"not null;default:0" json:"count"`
	ResetAt time.Time `gorm:"not null;index" json:"reset_at"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type APISignedRequestReplay struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	ReplayKey string    `gorm:"size:96;not null;uniqueIndex:ux_api_signed_request_replays_key" json:"replay_key"`
	DomainID  uuid.UUID `gorm:"type:uuid;not null;index" json:"domain_id"`
	ExpiresAt time.Time `gorm:"not null;index" json:"expires_at"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
