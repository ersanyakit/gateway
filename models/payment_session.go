package models

import (
	"core/constants"
	"time"

	"github.com/google/uuid"
)

const (
	PaymentStatusPending         = "pending"
	PaymentStatusAwaitingPayment = "awaiting_payment"
	PaymentStatusPaid            = "paid"
	PaymentStatusCanceled        = "canceled"
	PaymentStatusExpired         = "expired"
	PaymentStatusFailed          = "failed"
	PaymentStatusUnderpaid       = "underpaid"
	PaymentStatusOverpaid        = "overpaid"
	PaymentStatusPartialPaid     = "partial_paid"
)

const (
	PaymentOutcomeExact               = "exact"
	PaymentOutcomeUnderpaid           = "underpaid"
	PaymentOutcomeOverpaid            = "overpaid"
	PaymentOutcomePartialUnsupported  = "partial_unsupported"
	PaymentOutcomeExpiredAfterDeposit = "expired_after_deposit"
	PaymentOutcomeWrongAsset          = "wrong_asset"
	PaymentOutcomeWrongChain          = "wrong_chain"
)

const PaymentOutcomeReasonReorged = "matched transaction was reorged"

type PaymentSession struct {
	ID           uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	SessionToken string    `gorm:"size:80;uniqueIndex;not null" json:"session_token"`

	MerchantID uuid.UUID `gorm:"type:uuid;not null;index" json:"merchant_id"`
	Merchant   Merchant  `gorm:"constraint:OnDelete:CASCADE;" json:"-"`
	DomainID   uuid.UUID `gorm:"type:uuid;not null;index" json:"domain_id"`
	Domain     Domain    `gorm:"constraint:OnDelete:CASCADE;" json:"-"`
	WalletID   uuid.UUID `gorm:"type:uuid;not null;index" json:"wallet_id"`
	Wallet     Wallet    `gorm:"constraint:OnDelete:CASCADE;" json:"-"`

	OrderID        string `gorm:"size:128;not null;index" json:"order_id"`
	ProductID      string `gorm:"size:128;index" json:"product_id"`
	UserID         string `gorm:"size:128;index" json:"user_id"`
	Amount         string `gorm:"size:80;not null" json:"amount"`
	Currency       string `gorm:"size:20;not null;index" json:"currency"`
	SuccessURL     string `gorm:"size:500" json:"success_url,omitempty"`
	CancelURL      string `gorm:"size:500" json:"cancel_url,omitempty"`
	IdempotencyKey string `gorm:"size:180;index" json:"idempotency_key,omitempty"`

	SelectedChainID   *constants.ChainID `gorm:"type:bigint;index" json:"selected_chain_id,omitempty"`
	SelectedToken     *string            `gorm:"type:varchar(128);index" json:"selected_token,omitempty"`
	SelectedSymbol    string             `gorm:"size:20;index" json:"selected_symbol,omitempty"`
	SelectedDecimals  uint8              `json:"selected_decimals,omitempty"`
	ExpectedAmountRaw string             `gorm:"type:text" json:"expected_amount_raw,omitempty"`
	DepositAddress    string             `gorm:"size:128;index" json:"deposit_address,omitempty"`

	Status string `gorm:"size:32;not null;index" json:"status"`

	PaymentOutcome       string `gorm:"size:40;index" json:"payment_outcome,omitempty"`
	PaymentOutcomeReason string `gorm:"size:500" json:"payment_outcome_reason,omitempty"`
	MatchedAmountRaw     string `gorm:"type:text" json:"matched_amount_raw,omitempty"`
	ShortfallAmountRaw   string `gorm:"type:text" json:"shortfall_amount_raw,omitempty"`
	ExcessAmountRaw      string `gorm:"type:text" json:"excess_amount_raw,omitempty"`

	PaidAt                *time.Time `json:"paid_at,omitempty"`
	ExpiresAt             *time.Time `json:"expires_at,omitempty"`
	TxUniqueHash          *string    `gorm:"size:256;uniqueIndex:ux_payment_tx_unique_hash" json:"tx_unique_hash,omitempty"`
	TxHash                *string    `gorm:"size:128;index" json:"tx_hash,omitempty"`
	ConfirmationsRequired uint       `gorm:"not null;default:1" json:"confirmations_required"`
	ConfirmedAt           *time.Time `json:"confirmed_at,omitempty"`

	WebhookEvent       string     `gorm:"size:64;index" json:"webhook_event,omitempty"`
	WebhookSentAt      *time.Time `json:"webhook_sent_at,omitempty"`
	WebhookAttempts    uint       `gorm:"not null;default:0" json:"webhook_attempts"`
	WebhookLastError   string     `gorm:"type:text" json:"webhook_last_error,omitempty"`
	WebhookLockedUntil *time.Time `gorm:"index" json:"webhook_locked_until,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
