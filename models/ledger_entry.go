package models

import (
	"core/constants"
	"time"

	"github.com/google/uuid"
)

const (
	LedgerEntryTypeDepositPending    = "deposit_pending"
	LedgerEntryTypeDepositAvailable  = "deposit_available"
	LedgerEntryTypeWithdrawalHold    = "withdrawal_hold"
	LedgerEntryTypeWithdrawalRelease = "withdrawal_release"
	LedgerEntryTypeWithdrawalDebit   = "withdrawal_debit"
	LedgerEntryTypeRefundHold        = "refund_hold"
	LedgerEntryTypeRefundDebit       = "refund_debit"
	LedgerEntryTypeReorgReversal     = "reorg_reversal"
	LedgerEntryTypeAdjustment        = "adjustment"

	LedgerDirectionCredit = "credit"
	LedgerDirectionDebit  = "debit"

	LedgerStatusPending = "pending"
	LedgerStatusPosted  = "posted"
	LedgerStatusVoided  = "voided"

	LedgerAccountMerchantPending   = "merchant_pending"
	LedgerAccountMerchantAvailable = "merchant_available"
	LedgerAccountPlatformClearing  = "platform_clearing"
	LedgerAccountWithdrawalTransit = "withdrawal_transit"
	LedgerAccountRefundTransit     = "refund_transit"
)

type LedgerEntry struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	MerchantID uuid.UUID  `gorm:"type:uuid;not null;index" json:"merchant_id"`
	DomainID   *uuid.UUID `gorm:"type:uuid;index" json:"domain_id,omitempty"`
	WalletID   *uuid.UUID `gorm:"type:uuid;index" json:"wallet_id,omitempty"`
	PaymentID  *uuid.UUID `gorm:"type:uuid;index" json:"payment_id,omitempty"`

	TransactionUniqueHash string     `gorm:"size:256;index" json:"transaction_unique_hash,omitempty"`
	TransactionHash       string     `gorm:"size:128;index" json:"transaction_hash,omitempty"`
	WithdrawalID          *uuid.UUID `gorm:"type:uuid;index" json:"withdrawal_id,omitempty"`
	RefundID              *uuid.UUID `gorm:"type:uuid;index" json:"refund_id,omitempty"`

	ChainID  constants.ChainID `gorm:"type:bigint;not null;index" json:"chain_id"`
	Token    *string           `gorm:"type:varchar(128);index" json:"token,omitempty"`
	Symbol   string            `gorm:"size:20;not null;index" json:"symbol"`
	Decimals uint8             `json:"decimals"`

	EntryType string `gorm:"size:40;not null;index" json:"entry_type"`
	Account   string `gorm:"size:40;not null;index;uniqueIndex:ux_ledger_idempotent_account" json:"account"`
	Direction string `gorm:"size:12;not null;index" json:"direction"`
	Status    string `gorm:"size:20;not null;index" json:"status"`
	AmountRaw string `gorm:"type:text;not null" json:"amount_raw"`

	IdempotencyKey string `gorm:"size:180;index;uniqueIndex:ux_ledger_idempotent_account" json:"idempotency_key,omitempty"`
	Reference      string `gorm:"size:256;index" json:"reference,omitempty"`
	Description    string `gorm:"size:500" json:"description,omitempty"`

	PostedAt  *time.Time `json:"posted_at,omitempty"`
	VoidedAt  *time.Time `json:"voided_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}
