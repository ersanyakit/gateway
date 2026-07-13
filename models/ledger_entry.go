package models

import (
	"core/constants"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	LedgerEntryTypeDepositPending    = "deposit_pending"
	LedgerEntryTypeDepositAvailable  = "deposit_available"
	LedgerEntryTypeWithdrawalHold    = "withdrawal_hold"
	LedgerEntryTypeWithdrawalRelease = "withdrawal_release"
	LedgerEntryTypeWithdrawalDebit   = "withdrawal_debit"
	LedgerEntryTypeRefundHold        = "refund_hold"
	LedgerEntryTypeRefundRelease     = "refund_release"
	LedgerEntryTypeRefundDebit       = "refund_debit"
	LedgerEntryTypeSweepHold         = "sweep_hold"
	LedgerEntryTypeSweepRelease      = "sweep_release"
	LedgerEntryTypeSweepDebit        = "sweep_debit"
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
	LedgerAccountSweepTransit      = "sweep_transit"
)

const (
	LedgerEntryMutationContextKey = "allow_ledger_entry_mutation"

	LedgerBalanceProjectionScopeMerchant = "merchant"
	LedgerBalanceProjectionScopeDomain   = "domain"
	LedgerBalanceProjectionScopeWallet   = "wallet"
	LedgerBalanceProjectionScopePlatform = "platform"
)

var ErrLedgerEntryAppendOnly = errors.New("ledger entry is append-only")

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
	SweepJobID            *uuid.UUID `gorm:"type:uuid;index" json:"sweep_job_id,omitempty"`

	ChainID  constants.ChainID `gorm:"type:bigint;not null;index" json:"chain_id"`
	Token    *string           `gorm:"type:varchar(128);index" json:"token,omitempty"`
	Symbol   string            `gorm:"size:20;not null;index" json:"symbol"`
	Decimals uint8             `json:"decimals"`

	EntryType string `gorm:"size:40;not null;index;check:ledger_entries_entry_type_check,entry_type IN ('deposit_pending','deposit_available','withdrawal_hold','withdrawal_release','withdrawal_debit','refund_hold','refund_release','refund_debit','sweep_hold','sweep_release','sweep_debit','reorg_reversal','adjustment')" json:"entry_type"`
	Account   string `gorm:"size:40;not null;index;uniqueIndex:ux_ledger_idempotent_account;check:ledger_entries_account_check,account IN ('merchant_pending','merchant_available','platform_clearing','withdrawal_transit','refund_transit','sweep_transit')" json:"account"`
	Direction string `gorm:"size:12;not null;index;check:ledger_entries_direction_check,direction IN ('credit','debit')" json:"direction"`
	Status    string `gorm:"size:20;not null;index;check:ledger_entries_status_check,status IN ('pending','posted','voided')" json:"status"`
	AmountRaw string `gorm:"type:text;not null" json:"amount_raw"`

	IdempotencyKey string `gorm:"size:180;index;uniqueIndex:ux_ledger_idempotent_account" json:"idempotency_key,omitempty"`
	Reference      string `gorm:"size:256;index" json:"reference,omitempty"`
	Description    string `gorm:"size:500" json:"description,omitempty"`

	PostedAt  *time.Time `json:"posted_at,omitempty"`
	VoidedAt  *time.Time `json:"voided_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

func (LedgerEntry) BeforeUpdate(tx *gorm.DB) error {
	if ledgerEntryMutationAllowed(tx) {
		return nil
	}
	return ErrLedgerEntryAppendOnly
}

func (LedgerEntry) BeforeDelete(tx *gorm.DB) error {
	if ledgerEntryMutationAllowed(tx) {
		return nil
	}
	return ErrLedgerEntryAppendOnly
}

func ledgerEntryMutationAllowed(tx *gorm.DB) bool {
	if tx == nil {
		return false
	}
	value, ok := tx.Get(LedgerEntryMutationContextKey)
	if !ok {
		return false
	}
	allowed, ok := value.(bool)
	return ok && allowed
}

type LedgerBalanceProjection struct {
	ID uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`

	ScopeType string `gorm:"size:24;not null;index;uniqueIndex:ux_ledger_balance_projection_scope" json:"scope_type"`
	ScopeKey  string `gorm:"size:256;not null;index;uniqueIndex:ux_ledger_balance_projection_scope" json:"scope_key"`

	MerchantID *uuid.UUID `gorm:"type:uuid;index" json:"merchant_id,omitempty"`
	DomainID   *uuid.UUID `gorm:"type:uuid;index" json:"domain_id,omitempty"`
	WalletID   *uuid.UUID `gorm:"type:uuid;index" json:"wallet_id,omitempty"`

	ChainID          constants.ChainID `gorm:"type:bigint;not null;index;uniqueIndex:ux_ledger_balance_projection_scope" json:"chain_id"`
	Token            *string           `gorm:"type:varchar(128);index" json:"token,omitempty"`
	TokenFingerprint string            `gorm:"size:160;not null;index;uniqueIndex:ux_ledger_balance_projection_scope" json:"token_fingerprint"`
	Symbol           string            `gorm:"size:20;not null;index;uniqueIndex:ux_ledger_balance_projection_scope" json:"symbol"`
	Decimals         uint8             `gorm:"not null;uniqueIndex:ux_ledger_balance_projection_scope" json:"decimals"`
	Account          string            `gorm:"size:40;not null;index;uniqueIndex:ux_ledger_balance_projection_scope" json:"account"`
	BalanceRaw       string            `gorm:"type:text;not null" json:"balance_raw"`

	SourceLedgerEntryCount int64     `gorm:"not null;default:0" json:"source_ledger_entry_count"`
	ProjectedAt            time.Time `gorm:"not null;index" json:"projected_at"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}
