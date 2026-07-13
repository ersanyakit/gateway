package repositories

import (
	"context"
	"core/constants"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"core/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type LedgerRepo struct {
	db *gorm.DB
}

var ErrInsufficientAvailableBalance = errors.New("insufficient available balance")
var ErrLedgerReservationRequired = errors.New("ledger reservation is required before outbound transfer")

func NewLedgerRepo(db *gorm.DB) *LedgerRepo {
	return &LedgerRepo{db: db}
}

func (r *LedgerRepo) DB() *gorm.DB { return r.db }

func (r *LedgerRepo) amountIsPositive(amountRaw string) bool {
	value, ok := new(big.Int).SetString(amountRaw, 10)
	return ok && value.Sign() > 0
}

func (r *LedgerRepo) exists(ctx context.Context, key string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&models.LedgerEntry{}).
		Where("idempotency_key = ?", key).
		Count(&count).Error
	return count > 0, err
}

func (r *LedgerRepo) CreateDepositPending(ctx context.Context, txModel models.Transaction) error {
	if txModel.MerchantID == nil || txModel.Amount == "" || !r.amountIsPositive(txModel.Amount) {
		return nil
	}
	key := "deposit-pending:" + txModel.UniqueHash
	exists, err := r.exists(ctx, key)
	if err != nil || exists {
		return err
	}
	now := time.Now()
	domainID := txModel.DomainID
	walletID := txModel.WalletID
	entries := []models.LedgerEntry{
		{
			ID:                    uuid.New(),
			MerchantID:            *txModel.MerchantID,
			DomainID:              domainID,
			WalletID:              walletID,
			TransactionUniqueHash: txModel.UniqueHash,
			TransactionHash:       txModel.Hash,
			ChainID:               txModel.ChainID,
			Token:                 txModel.Token,
			Symbol:                txModel.Symbol,
			Decimals:              txModel.Decimals,
			EntryType:             models.LedgerEntryTypeDepositPending,
			Account:               models.LedgerAccountMerchantPending,
			Direction:             models.LedgerDirectionCredit,
			Status:                models.LedgerStatusPending,
			AmountRaw:             txModel.Amount,
			IdempotencyKey:        key,
			Reference:             txModel.UniqueHash,
			PostedAt:              &now,
			CreatedAt:             now,
			UpdatedAt:             now,
		},
		{
			ID:                    uuid.New(),
			MerchantID:            *txModel.MerchantID,
			DomainID:              domainID,
			WalletID:              walletID,
			TransactionUniqueHash: txModel.UniqueHash,
			TransactionHash:       txModel.Hash,
			ChainID:               txModel.ChainID,
			Token:                 txModel.Token,
			Symbol:                txModel.Symbol,
			Decimals:              txModel.Decimals,
			EntryType:             models.LedgerEntryTypeDepositPending,
			Account:               models.LedgerAccountPlatformClearing,
			Direction:             models.LedgerDirectionDebit,
			Status:                models.LedgerStatusPending,
			AmountRaw:             txModel.Amount,
			IdempotencyKey:        key,
			Reference:             txModel.UniqueHash,
			PostedAt:              &now,
			CreatedAt:             now,
			UpdatedAt:             now,
		},
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return r.appendLedgerEntries(ctx, tx, entries)
	})
}

func (r *LedgerRepo) PostDepositAvailable(ctx context.Context, session models.PaymentSession, txModel models.Transaction) error {
	if txModel.MerchantID == nil || txModel.Amount == "" || !r.amountIsPositive(txModel.Amount) {
		return nil
	}
	key := "deposit-available:" + session.ID.String() + ":" + txModel.UniqueHash
	exists, err := r.exists(ctx, key)
	if err != nil || exists {
		return err
	}
	now := time.Now()
	paymentID := session.ID
	domainID := txModel.DomainID
	walletID := txModel.WalletID
	entries := []models.LedgerEntry{
		{
			ID:                    uuid.New(),
			MerchantID:            *txModel.MerchantID,
			DomainID:              domainID,
			WalletID:              walletID,
			PaymentID:             &paymentID,
			TransactionUniqueHash: txModel.UniqueHash,
			TransactionHash:       txModel.Hash,
			ChainID:               txModel.ChainID,
			Token:                 txModel.Token,
			Symbol:                txModel.Symbol,
			Decimals:              txModel.Decimals,
			EntryType:             models.LedgerEntryTypeDepositAvailable,
			Account:               models.LedgerAccountMerchantAvailable,
			Direction:             models.LedgerDirectionCredit,
			Status:                models.LedgerStatusPosted,
			AmountRaw:             txModel.Amount,
			IdempotencyKey:        key,
			Reference:             session.ID.String(),
			PostedAt:              &now,
			CreatedAt:             now,
			UpdatedAt:             now,
		},
		{
			ID:                    uuid.New(),
			MerchantID:            *txModel.MerchantID,
			DomainID:              domainID,
			WalletID:              walletID,
			PaymentID:             &paymentID,
			TransactionUniqueHash: txModel.UniqueHash,
			TransactionHash:       txModel.Hash,
			ChainID:               txModel.ChainID,
			Token:                 txModel.Token,
			Symbol:                txModel.Symbol,
			Decimals:              txModel.Decimals,
			EntryType:             models.LedgerEntryTypeDepositAvailable,
			Account:               models.LedgerAccountMerchantPending,
			Direction:             models.LedgerDirectionDebit,
			Status:                models.LedgerStatusPosted,
			AmountRaw:             txModel.Amount,
			IdempotencyKey:        key,
			Reference:             session.ID.String(),
			PostedAt:              &now,
			CreatedAt:             now,
			UpdatedAt:             now,
		},
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return r.appendLedgerEntries(ctx, tx, entries)
	})
}

func (r *LedgerRepo) PostStandaloneDepositAvailable(ctx context.Context, txModel models.Transaction) error {
	if txModel.MerchantID == nil || txModel.Amount == "" || !r.amountIsPositive(txModel.Amount) {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(dbtx *gorm.DB) error {
		key := "deposit-standalone-available:" + txModel.UniqueHash
		exists, err := r.existsWithDB(ctx, dbtx, key)
		if err != nil || exists {
			return err
		}
		if err := r.lockLedgerAsset(ctx, dbtx, *txModel.MerchantID, txModel.DomainID, txModel.ChainID, txModel.Token); err != nil {
			return err
		}
		now := time.Now()
		domainID := txModel.DomainID
		walletID := txModel.WalletID
		entries := []models.LedgerEntry{
			{
				ID:                    uuid.New(),
				MerchantID:            *txModel.MerchantID,
				DomainID:              domainID,
				WalletID:              walletID,
				TransactionUniqueHash: txModel.UniqueHash,
				TransactionHash:       txModel.Hash,
				ChainID:               txModel.ChainID,
				Token:                 txModel.Token,
				Symbol:                txModel.Symbol,
				Decimals:              txModel.Decimals,
				EntryType:             models.LedgerEntryTypeDepositAvailable,
				Account:               models.LedgerAccountMerchantAvailable,
				Direction:             models.LedgerDirectionCredit,
				Status:                models.LedgerStatusPosted,
				AmountRaw:             txModel.Amount,
				IdempotencyKey:        key,
				Reference:             txModel.UniqueHash,
				PostedAt:              &now,
				CreatedAt:             now,
				UpdatedAt:             now,
			},
			{
				ID:                    uuid.New(),
				MerchantID:            *txModel.MerchantID,
				DomainID:              domainID,
				WalletID:              walletID,
				TransactionUniqueHash: txModel.UniqueHash,
				TransactionHash:       txModel.Hash,
				ChainID:               txModel.ChainID,
				Token:                 txModel.Token,
				Symbol:                txModel.Symbol,
				Decimals:              txModel.Decimals,
				EntryType:             models.LedgerEntryTypeDepositAvailable,
				Account:               models.LedgerAccountMerchantPending,
				Direction:             models.LedgerDirectionDebit,
				Status:                models.LedgerStatusPosted,
				AmountRaw:             txModel.Amount,
				IdempotencyKey:        key,
				Reference:             txModel.UniqueHash,
				PostedAt:              &now,
				CreatedAt:             now,
				UpdatedAt:             now,
			},
		}
		return r.appendLedgerEntries(ctx, dbtx, entries)
	})
}

func (r *LedgerRepo) PostManualDeposit(ctx context.Context, txModel models.Transaction) error {
	if txModel.MerchantID == nil || txModel.Amount == "" || !r.amountIsPositive(txModel.Amount) {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(dbtx *gorm.DB) error {
		domainID := txModel.DomainID
		pendingKey := "manual-deposit-pending:" + txModel.UniqueHash
		availableKey := "manual-deposit-available:" + txModel.UniqueHash
		for _, key := range []string{pendingKey, availableKey} {
			exists, err := r.existsWithDB(ctx, dbtx, key)
			if err != nil || exists {
				return err
			}
		}
		if err := r.lockLedgerAsset(ctx, dbtx, *txModel.MerchantID, domainID, txModel.ChainID, txModel.Token); err != nil {
			return err
		}
		now := time.Now()
		walletID := txModel.WalletID
		entries := []models.LedgerEntry{
			{
				ID:                    uuid.New(),
				MerchantID:            *txModel.MerchantID,
				DomainID:              domainID,
				WalletID:              walletID,
				TransactionUniqueHash: txModel.UniqueHash,
				TransactionHash:       txModel.Hash,
				ChainID:               txModel.ChainID,
				Token:                 txModel.Token,
				Symbol:                txModel.Symbol,
				Decimals:              txModel.Decimals,
				EntryType:             models.LedgerEntryTypeDepositPending,
				Account:               models.LedgerAccountMerchantPending,
				Direction:             models.LedgerDirectionCredit,
				Status:                models.LedgerStatusPosted,
				AmountRaw:             txModel.Amount,
				IdempotencyKey:        pendingKey,
				Reference:             txModel.UniqueHash,
				Description:           "Manual admin test deposit",
				PostedAt:              &now,
				CreatedAt:             now,
				UpdatedAt:             now,
			},
			{
				ID:                    uuid.New(),
				MerchantID:            *txModel.MerchantID,
				DomainID:              domainID,
				WalletID:              walletID,
				TransactionUniqueHash: txModel.UniqueHash,
				TransactionHash:       txModel.Hash,
				ChainID:               txModel.ChainID,
				Token:                 txModel.Token,
				Symbol:                txModel.Symbol,
				Decimals:              txModel.Decimals,
				EntryType:             models.LedgerEntryTypeDepositPending,
				Account:               models.LedgerAccountPlatformClearing,
				Direction:             models.LedgerDirectionDebit,
				Status:                models.LedgerStatusPosted,
				AmountRaw:             txModel.Amount,
				IdempotencyKey:        pendingKey,
				Reference:             txModel.UniqueHash,
				Description:           "Manual admin test deposit",
				PostedAt:              &now,
				CreatedAt:             now,
				UpdatedAt:             now,
			},
			{
				ID:                    uuid.New(),
				MerchantID:            *txModel.MerchantID,
				DomainID:              domainID,
				WalletID:              walletID,
				TransactionUniqueHash: txModel.UniqueHash,
				TransactionHash:       txModel.Hash,
				ChainID:               txModel.ChainID,
				Token:                 txModel.Token,
				Symbol:                txModel.Symbol,
				Decimals:              txModel.Decimals,
				EntryType:             models.LedgerEntryTypeDepositAvailable,
				Account:               models.LedgerAccountMerchantAvailable,
				Direction:             models.LedgerDirectionCredit,
				Status:                models.LedgerStatusPosted,
				AmountRaw:             txModel.Amount,
				IdempotencyKey:        availableKey,
				Reference:             txModel.UniqueHash,
				Description:           "Manual admin test deposit",
				PostedAt:              &now,
				CreatedAt:             now,
				UpdatedAt:             now,
			},
			{
				ID:                    uuid.New(),
				MerchantID:            *txModel.MerchantID,
				DomainID:              domainID,
				WalletID:              walletID,
				TransactionUniqueHash: txModel.UniqueHash,
				TransactionHash:       txModel.Hash,
				ChainID:               txModel.ChainID,
				Token:                 txModel.Token,
				Symbol:                txModel.Symbol,
				Decimals:              txModel.Decimals,
				EntryType:             models.LedgerEntryTypeDepositAvailable,
				Account:               models.LedgerAccountMerchantPending,
				Direction:             models.LedgerDirectionDebit,
				Status:                models.LedgerStatusPosted,
				AmountRaw:             txModel.Amount,
				IdempotencyKey:        availableKey,
				Reference:             txModel.UniqueHash,
				Description:           "Manual admin test deposit",
				PostedAt:              &now,
				CreatedAt:             now,
				UpdatedAt:             now,
			},
		}
		return r.appendLedgerEntries(ctx, dbtx, entries)
	})
}

func (r *LedgerRepo) CreateWithdrawalHold(ctx context.Context, request models.WithdrawalRequest) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return r.createWithdrawalHold(ctx, tx, request)
	})
}

func (r *LedgerRepo) CreateWithdrawalHoldWithDB(ctx context.Context, tx *gorm.DB, request models.WithdrawalRequest) error {
	return r.createWithdrawalHold(ctx, tx, request)
}

func (r *LedgerRepo) createWithdrawalHold(ctx context.Context, tx *gorm.DB, request models.WithdrawalRequest) error {
	key := withdrawalHoldKey(request.ID)
	exists, err := r.existsWithDB(ctx, tx, key)
	if err != nil || exists {
		return err
	}
	if !r.amountIsPositive(request.AmountRaw) {
		return errors.New("withdrawal amount must be positive")
	}
	now := time.Now()
	withdrawalID := request.ID
	walletID := request.WalletID
	chainID, ok := ledgerChainIDFromName(request.Chain)
	if !ok {
		return errors.New("unsupported withdrawal chain")
	}
	symbol := strings.ToUpper(strings.TrimSpace(request.Symbol))
	if symbol == "" {
		symbol = strings.ToUpper(strings.TrimSpace(request.Chain))
	}
	if err := r.lockLedgerAsset(ctx, tx, request.MerchantID, request.DomainID, chainID, request.Token); err != nil {
		return err
	}
	if err := r.ensureAvailableBalance(ctx, tx, request.MerchantID, request.DomainID, chainID, request.Token, request.AmountRaw); err != nil {
		return err
	}
	entries := []models.LedgerEntry{
		{
			ID:             uuid.New(),
			MerchantID:     request.MerchantID,
			DomainID:       request.DomainID,
			WalletID:       &walletID,
			WithdrawalID:   &withdrawalID,
			ChainID:        chainID,
			Token:          request.Token,
			Symbol:         symbol,
			Decimals:       request.Decimals,
			EntryType:      models.LedgerEntryTypeWithdrawalHold,
			Account:        models.LedgerAccountMerchantAvailable,
			Direction:      models.LedgerDirectionDebit,
			Status:         models.LedgerStatusPending,
			AmountRaw:      request.AmountRaw,
			IdempotencyKey: key,
			Reference:      request.ID.String(),
			PostedAt:       &now,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		{
			ID:             uuid.New(),
			MerchantID:     request.MerchantID,
			DomainID:       request.DomainID,
			WalletID:       &walletID,
			WithdrawalID:   &withdrawalID,
			ChainID:        chainID,
			Token:          request.Token,
			Symbol:         symbol,
			Decimals:       request.Decimals,
			EntryType:      models.LedgerEntryTypeWithdrawalHold,
			Account:        models.LedgerAccountWithdrawalTransit,
			Direction:      models.LedgerDirectionCredit,
			Status:         models.LedgerStatusPending,
			AmountRaw:      request.AmountRaw,
			IdempotencyKey: key,
			Reference:      request.ID.String(),
			PostedAt:       &now,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	return r.appendLedgerEntries(ctx, tx, entries)
}

func (r *LedgerRepo) PostWithdrawalDebit(ctx context.Context, request models.WithdrawalRequest, txHash string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return r.postWithdrawalDebit(ctx, tx, request, txHash)
	})
}

func (r *LedgerRepo) PostWithdrawalDebitWithDB(ctx context.Context, tx *gorm.DB, request models.WithdrawalRequest, txHash string) error {
	return r.postWithdrawalDebit(ctx, tx, request, txHash)
}

func (r *LedgerRepo) postWithdrawalDebit(ctx context.Context, tx *gorm.DB, request models.WithdrawalRequest, txHash string) error {
	key := withdrawalDebitKey(request.ID)
	exists, err := r.existsWithDB(ctx, tx, key)
	if err != nil || exists {
		return err
	}
	if !r.amountIsPositive(request.AmountRaw) {
		return errors.New("withdrawal amount must be positive")
	}
	chainID, ok := ledgerChainIDFromName(request.Chain)
	if !ok {
		return errors.New("unsupported withdrawal chain")
	}
	if err := r.lockLedgerAsset(ctx, tx, request.MerchantID, request.DomainID, chainID, request.Token); err != nil {
		return err
	}
	if err := r.RequireWithdrawalHoldForRequestWithDB(ctx, tx, request); err != nil {
		return err
	}
	now := time.Now()
	withdrawalID := request.ID
	walletID := request.WalletID
	symbol := strings.ToUpper(strings.TrimSpace(request.Symbol))
	if symbol == "" {
		symbol = strings.ToUpper(strings.TrimSpace(request.Chain))
	}
	entries := []models.LedgerEntry{
		{
			ID:              uuid.New(),
			MerchantID:      request.MerchantID,
			DomainID:        request.DomainID,
			WalletID:        &walletID,
			WithdrawalID:    &withdrawalID,
			TransactionHash: txHash,
			ChainID:         chainID,
			Token:           request.Token,
			Symbol:          symbol,
			Decimals:        request.Decimals,
			EntryType:       models.LedgerEntryTypeWithdrawalDebit,
			Account:         models.LedgerAccountWithdrawalTransit,
			Direction:       models.LedgerDirectionDebit,
			Status:          models.LedgerStatusPosted,
			AmountRaw:       request.AmountRaw,
			IdempotencyKey:  key,
			Reference:       request.ID.String(),
			PostedAt:        &now,
			CreatedAt:       now,
			UpdatedAt:       now,
		},
		{
			ID:              uuid.New(),
			MerchantID:      request.MerchantID,
			DomainID:        request.DomainID,
			WalletID:        &walletID,
			WithdrawalID:    &withdrawalID,
			TransactionHash: txHash,
			ChainID:         chainID,
			Token:           request.Token,
			Symbol:          symbol,
			Decimals:        request.Decimals,
			EntryType:       models.LedgerEntryTypeWithdrawalDebit,
			Account:         models.LedgerAccountPlatformClearing,
			Direction:       models.LedgerDirectionCredit,
			Status:          models.LedgerStatusPosted,
			AmountRaw:       request.AmountRaw,
			IdempotencyKey:  key,
			Reference:       request.ID.String(),
			PostedAt:        &now,
			CreatedAt:       now,
			UpdatedAt:       now,
		},
	}
	return r.appendLedgerEntries(ctx, tx, entries)
}

func (r *LedgerRepo) VoidWithdrawalHold(ctx context.Context, withdrawalID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return r.voidWithdrawalHold(ctx, tx, withdrawalID)
	})
}

func (r *LedgerRepo) VoidWithdrawalHoldWithDB(ctx context.Context, tx *gorm.DB, withdrawalID uuid.UUID) error {
	return r.voidWithdrawalHold(ctx, tx, withdrawalID)
}

func (r *LedgerRepo) voidWithdrawalHold(ctx context.Context, tx *gorm.DB, withdrawalID uuid.UUID) error {
	return r.releaseHeldEntries(ctx, tx, ledgerHoldReleaseRequest{
		idColumn:       "withdrawal_id",
		id:             withdrawalID,
		holdType:       models.LedgerEntryTypeWithdrawalHold,
		releaseType:    models.LedgerEntryTypeWithdrawalRelease,
		transitAccount: models.LedgerAccountWithdrawalTransit,
		idempotencyKey: withdrawalReleaseKey(withdrawalID),
		consumedIdempotencyKeys: []string{
			withdrawalDebitKey(withdrawalID),
		},
		description: "Withdrawal hold release for ledger entry",
	})
}

func (r *LedgerRepo) RequireWithdrawalHoldWithDB(ctx context.Context, tx *gorm.DB, withdrawalID uuid.UUID) error {
	if tx == nil || withdrawalID == uuid.Nil {
		return ErrLedgerReservationRequired
	}
	var request models.WithdrawalRequest
	if err := tx.WithContext(ctx).First(&request, "id = ?", withdrawalID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrLedgerReservationRequired
		}
		return err
	}
	return r.RequireWithdrawalHoldForRequestWithDB(ctx, tx, request)
}

func (r *LedgerRepo) RequireWithdrawalHoldForRequestWithDB(ctx context.Context, tx *gorm.DB, request models.WithdrawalRequest) error {
	if request.ID == uuid.Nil || request.MerchantID == uuid.Nil || request.WalletID == uuid.Nil {
		return ErrLedgerReservationRequired
	}
	chainID, ok := ledgerChainIDFromName(request.Chain)
	if !ok {
		return ErrLedgerReservationRequired
	}
	walletID := request.WalletID
	return requireLedgerHold(ctx, tx, ledgerHoldRequirement{
		idColumn:       "withdrawal_id",
		id:             request.ID,
		entryType:      models.LedgerEntryTypeWithdrawalHold,
		releaseType:    models.LedgerEntryTypeWithdrawalRelease,
		terminalTypes:  []string{models.LedgerEntryTypeWithdrawalRelease, models.LedgerEntryTypeWithdrawalDebit},
		transitAccount: models.LedgerAccountWithdrawalTransit,
		strict:         true,
		merchantID:     request.MerchantID,
		domainID:       request.DomainID,
		walletID:       &walletID,
		chainID:        chainID,
		token:          request.Token,
		amountRaw:      request.AmountRaw,
	})
}

func (r *LedgerRepo) RequireRefundHoldWithDB(ctx context.Context, tx *gorm.DB, refundID uuid.UUID) error {
	return requireLedgerHold(ctx, tx, ledgerHoldRequirement{
		idColumn:       "refund_id",
		id:             refundID,
		entryType:      models.LedgerEntryTypeRefundHold,
		releaseType:    models.LedgerEntryTypeRefundRelease,
		terminalTypes:  []string{models.LedgerEntryTypeRefundRelease, models.LedgerEntryTypeRefundDebit},
		transitAccount: models.LedgerAccountRefundTransit,
	})
}

func (r *LedgerRepo) RequireRefundHoldForRefundWithDB(ctx context.Context, tx *gorm.DB, refund models.Refund, session models.PaymentSession) error {
	if refund.ID == uuid.Nil || refund.MerchantID == uuid.Nil || refund.DomainID == uuid.Nil || session.WalletID == uuid.Nil {
		return ErrLedgerReservationRequired
	}
	chainID, _, _, err := ledgerRefundAssetFromSession(session)
	if err != nil {
		return err
	}
	domainID := refund.DomainID
	walletID := refundLedgerWalletID(refund, session)
	return requireLedgerHold(ctx, tx, ledgerHoldRequirement{
		idColumn:       "refund_id",
		id:             refund.ID,
		entryType:      models.LedgerEntryTypeRefundHold,
		releaseType:    models.LedgerEntryTypeRefundRelease,
		terminalTypes:  []string{models.LedgerEntryTypeRefundDebit},
		transitAccount: models.LedgerAccountRefundTransit,
		strict:         true,
		merchantID:     refund.MerchantID,
		domainID:       &domainID,
		walletID:       &walletID,
		chainID:        chainID,
		token:          session.SelectedToken,
		amountRaw:      refund.AmountRaw,
	})
}

func (r *LedgerRepo) RequireSweepHoldWithDB(ctx context.Context, tx *gorm.DB, sweepJobID uuid.UUID) error {
	return requireLedgerHold(ctx, tx, ledgerHoldRequirement{
		idColumn:       "sweep_job_id",
		id:             sweepJobID,
		entryType:      models.LedgerEntryTypeSweepHold,
		releaseType:    models.LedgerEntryTypeSweepRelease,
		terminalTypes:  []string{models.LedgerEntryTypeSweepRelease},
		transitAccount: models.LedgerAccountSweepTransit,
	})
}

func (r *LedgerRepo) RequireSweepHoldForJobTransactionWithDB(ctx context.Context, tx *gorm.DB, job models.SweepJob, txModel models.Transaction) error {
	if err := validateSweepJobTransaction(job, txModel); err != nil {
		return err
	}
	if txModel.MerchantID == nil || txModel.WalletID == nil {
		return ErrLedgerReservationRequired
	}
	return requireLedgerHold(ctx, tx, ledgerHoldRequirement{
		idColumn:       "sweep_job_id",
		id:             job.ID,
		entryType:      models.LedgerEntryTypeSweepHold,
		releaseType:    models.LedgerEntryTypeSweepRelease,
		terminalTypes:  []string{models.LedgerEntryTypeSweepRelease},
		transitAccount: models.LedgerAccountSweepTransit,
		strict:         true,
		merchantID:     *txModel.MerchantID,
		domainID:       txModel.DomainID,
		walletID:       txModel.WalletID,
		chainID:        txModel.ChainID,
		token:          txModel.Token,
		amountRaw:      txModel.Amount,
	})
}

type ledgerHoldRequirement struct {
	idColumn       string
	id             uuid.UUID
	entryType      string
	releaseType    string
	terminalTypes  []string
	transitAccount string
	strict         bool
	merchantID     uuid.UUID
	domainID       *uuid.UUID
	walletID       *uuid.UUID
	chainID        constants.ChainID
	token          *string
	amountRaw      string
}

func requireLedgerHold(ctx context.Context, tx *gorm.DB, req ledgerHoldRequirement) error {
	if tx == nil || req.id == uuid.Nil {
		return ErrLedgerReservationRequired
	}
	var rows []models.LedgerEntry
	if err := tx.WithContext(ctx).
		Model(&models.LedgerEntry{}).
		Where(req.idColumn+" = ? AND entry_type = ? AND status = ?", req.id, req.entryType, models.LedgerStatusPending).
		Where("account IN ?", []string{models.LedgerAccountMerchantAvailable, req.transitAccount}).
		Where(`
			NOT EXISTS (
				SELECT 1
				FROM ledger_entries releases
				WHERE releases.reference = ledger_entries.id::text
				  AND releases.entry_type = ?
				  AND releases.status <> ?
			)
			`, req.releaseType, models.LedgerStatusVoided).
		Where(`
				NOT EXISTS (
					SELECT 1
					FROM ledger_entries terminal
					WHERE terminal.`+req.idColumn+` = ledger_entries.`+req.idColumn+`
					  AND terminal.entry_type IN ?
					  AND terminal.status <> ?
				)
			`, req.terminalTypes, models.LedgerStatusVoided).
		Find(&rows).Error; err != nil {
		return err
	}
	matches := map[string]bool{}
	for _, row := range rows {
		if !ledgerHoldEntryMatches(row, req) {
			continue
		}
		matches[row.Account] = true
	}
	if !matches[models.LedgerAccountMerchantAvailable] || !matches[req.transitAccount] {
		return ErrLedgerReservationRequired
	}
	return nil
}

func ledgerHoldEntryMatches(row models.LedgerEntry, req ledgerHoldRequirement) bool {
	if !req.strict {
		return true
	}
	if req.merchantID != uuid.Nil && row.MerchantID != req.merchantID {
		return false
	}
	if req.domainID != nil || row.DomainID != nil {
		if !sameUUIDPtr(row.DomainID, req.domainID) {
			return false
		}
	}
	if req.walletID != nil || row.WalletID != nil {
		if !sameUUIDPtr(row.WalletID, req.walletID) {
			return false
		}
	}
	if req.amountRaw != "" && strings.TrimSpace(row.AmountRaw) != strings.TrimSpace(req.amountRaw) {
		return false
	}
	if req.chainID != row.ChainID {
		return false
	}
	if !sameOptionalToken(row.Token, req.token) {
		return false
	}
	if row.Account == models.LedgerAccountMerchantAvailable && row.Direction != models.LedgerDirectionDebit {
		return false
	}
	if row.Account == req.transitAccount && row.Direction != models.LedgerDirectionCredit {
		return false
	}
	return true
}

func sameUUIDPtr(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func sameOptionalToken(left, right *string) bool {
	leftValue := ""
	if left != nil {
		leftValue = strings.TrimSpace(*left)
	}
	rightValue := ""
	if right != nil {
		rightValue = strings.TrimSpace(*right)
	}
	if leftValue == "" || rightValue == "" {
		return leftValue == "" && rightValue == ""
	}
	return strings.EqualFold(leftValue, rightValue)
}

func (r *LedgerRepo) existsWithDB(ctx context.Context, tx *gorm.DB, key string) (bool, error) {
	var count int64
	err := tx.WithContext(ctx).
		Model(&models.LedgerEntry{}).
		Where("idempotency_key = ?", key).
		Count(&count).Error
	return count > 0, err
}

func (r *LedgerRepo) appendLedgerEntries(ctx context.Context, tx *gorm.DB, entries []models.LedgerEntry) error {
	if len(entries) == 0 {
		return nil
	}
	if tx == nil {
		return gorm.ErrInvalidDB
	}
	projectionTableReady := tx.Migrator().HasTable(&models.LedgerBalanceProjection{})
	if err := tx.WithContext(ctx).Create(&entries).Error; err != nil {
		return err
	}
	if !projectionTableReady {
		return nil
	}
	if err := r.refreshLedgerBalanceProjectionsForEntries(ctx, tx, entries); err != nil {
		return err
	}
	return nil
}

type ledgerProjectionRefreshKey struct {
	ScopeType        string
	ScopeKey         string
	MerchantID       uuid.UUID
	DomainID         uuid.UUID
	WalletID         uuid.UUID
	HasMerchantID    bool
	HasDomainID      bool
	HasWalletID      bool
	ChainID          constants.ChainID
	TokenFingerprint string
	Symbol           string
	Decimals         uint8
	Account          string
}

func (r *LedgerRepo) refreshLedgerBalanceProjectionsForEntries(ctx context.Context, tx *gorm.DB, entries []models.LedgerEntry) error {
	keys := make(map[ledgerProjectionRefreshKey]struct{})
	for _, entry := range entries {
		if !ledgerEntryAffectsBalanceProjection(entry) {
			continue
		}
		tokenFingerprint := ledgerBalanceProjectionTokenFingerprint(entry.Token)
		addKey := func(scopeType string, scopeKey string, merchantID uuid.UUID, domainID *uuid.UUID, walletID *uuid.UUID) {
			scopeKey = strings.TrimSpace(scopeKey)
			if scopeKey == "" {
				return
			}
			key := ledgerProjectionRefreshKey{
				ScopeType:        scopeType,
				ScopeKey:         scopeKey,
				ChainID:          entry.ChainID,
				TokenFingerprint: tokenFingerprint,
				Symbol:           entry.Symbol,
				Decimals:         entry.Decimals,
				Account:          entry.Account,
			}
			if merchantID != uuid.Nil {
				key.MerchantID = merchantID
				key.HasMerchantID = true
			}
			if domainID != nil {
				key.DomainID = *domainID
				key.HasDomainID = true
			}
			if walletID != nil {
				key.WalletID = *walletID
				key.HasWalletID = true
			}
			keys[key] = struct{}{}
		}
		addKey(
			models.LedgerBalanceProjectionScopeMerchant,
			ledgerBalanceProjectionScopeKey(models.LedgerBalanceProjectionScopeMerchant, entry.MerchantID, nil, nil),
			entry.MerchantID,
			nil,
			nil,
		)
		if entry.DomainID != nil {
			addKey(
				models.LedgerBalanceProjectionScopeDomain,
				ledgerBalanceProjectionScopeKey(models.LedgerBalanceProjectionScopeDomain, entry.MerchantID, entry.DomainID, nil),
				entry.MerchantID,
				entry.DomainID,
				nil,
			)
		}
		if entry.DomainID != nil && entry.WalletID != nil {
			addKey(
				models.LedgerBalanceProjectionScopeWallet,
				ledgerBalanceProjectionScopeKey(models.LedgerBalanceProjectionScopeWallet, entry.MerchantID, entry.DomainID, entry.WalletID),
				entry.MerchantID,
				entry.DomainID,
				entry.WalletID,
			)
		}
		if ledgerProjectionPlatformAccount(entry.Account) {
			addKey(
				models.LedgerBalanceProjectionScopePlatform,
				ledgerBalanceProjectionScopeKey(models.LedgerBalanceProjectionScopePlatform, uuid.Nil, nil, nil),
				uuid.Nil,
				nil,
				nil,
			)
		}
	}
	for key := range keys {
		if err := r.refreshLedgerBalanceProjection(ctx, tx, key); err != nil {
			return err
		}
	}
	return nil
}

func ledgerEntryAffectsBalanceProjection(entry models.LedgerEntry) bool {
	if entry.MerchantID == uuid.Nil {
		return false
	}
	if entry.Status != models.LedgerStatusPending && entry.Status != models.LedgerStatusPosted {
		return false
	}
	if strings.TrimSpace(entry.AmountRaw) == "" {
		return false
	}
	if _, ok := new(big.Int).SetString(strings.TrimSpace(entry.AmountRaw), 10); !ok {
		return false
	}
	return true
}

func ledgerProjectionPlatformAccount(account string) bool {
	switch strings.TrimSpace(account) {
	case models.LedgerAccountMerchantPending,
		models.LedgerAccountMerchantAvailable,
		models.LedgerAccountWithdrawalTransit,
		models.LedgerAccountRefundTransit,
		models.LedgerAccountSweepTransit:
		return true
	default:
		return false
	}
}

func (r *LedgerRepo) refreshLedgerBalanceProjection(ctx context.Context, tx *gorm.DB, key ledgerProjectionRefreshKey) error {
	query := tx.WithContext(ctx).
		Model(&models.LedgerEntry{}).
		Select(`
			COALESCE(SUM(CASE WHEN direction = 'credit' THEN amount_raw::numeric ELSE -amount_raw::numeric END), 0)::text AS balance_raw,
			COUNT(*)::bigint AS source_ledger_entry_count
		`).
		Where("status IN ?", []string{models.LedgerStatusPending, models.LedgerStatusPosted}).
		Where("amount_raw ~ '^[0-9]+$'").
		Where("chain_id = ? AND symbol = ? AND decimals = ? AND account = ?", key.ChainID, key.Symbol, key.Decimals, key.Account)
	if key.TokenFingerprint == "native" {
		query = query.Where("token IS NULL OR btrim(token) = ''")
	} else {
		query = query.Where("LOWER(btrim(token)) = ?", key.TokenFingerprint)
	}
	switch key.ScopeType {
	case models.LedgerBalanceProjectionScopeMerchant:
		query = query.Where("merchant_id = ?", key.MerchantID)
	case models.LedgerBalanceProjectionScopeDomain:
		query = query.Where("merchant_id = ? AND domain_id = ?", key.MerchantID, key.DomainID)
	case models.LedgerBalanceProjectionScopeWallet:
		query = query.Where("merchant_id = ? AND domain_id = ? AND wallet_id = ?", key.MerchantID, key.DomainID, key.WalletID)
	case models.LedgerBalanceProjectionScopePlatform:
		if !ledgerProjectionPlatformAccount(key.Account) {
			return nil
		}
	default:
		return nil
	}

	var aggregate struct {
		BalanceRaw             string
		SourceLedgerEntryCount int64
	}
	if err := query.Scan(&aggregate).Error; err != nil {
		return err
	}
	if aggregate.SourceLedgerEntryCount == 0 ||
		(key.ScopeType == models.LedgerBalanceProjectionScopePlatform && strings.TrimSpace(aggregate.BalanceRaw) == "0") {
		return tx.WithContext(ctx).
			Where("scope_type = ? AND scope_key = ? AND chain_id = ? AND token_fingerprint = ? AND symbol = ? AND decimals = ? AND account = ?",
				key.ScopeType, key.ScopeKey, key.ChainID, key.TokenFingerprint, key.Symbol, key.Decimals, key.Account).
			Delete(&models.LedgerBalanceProjection{}).Error
	}

	now := time.Now()
	projection := models.LedgerBalanceProjection{
		ID:                     uuid.New(),
		ScopeType:              key.ScopeType,
		ScopeKey:               key.ScopeKey,
		MerchantID:             ledgerProjectionUUIDPtr(key.MerchantID, key.HasMerchantID),
		DomainID:               ledgerProjectionUUIDPtr(key.DomainID, key.HasDomainID),
		WalletID:               ledgerProjectionUUIDPtr(key.WalletID, key.HasWalletID),
		ChainID:                key.ChainID,
		Token:                  ledgerProjectionTokenPtr(key.TokenFingerprint),
		TokenFingerprint:       key.TokenFingerprint,
		Symbol:                 key.Symbol,
		Decimals:               key.Decimals,
		Account:                key.Account,
		BalanceRaw:             aggregate.BalanceRaw,
		SourceLedgerEntryCount: aggregate.SourceLedgerEntryCount,
		ProjectedAt:            now,
		CreatedAt:              now,
		UpdatedAt:              now,
	}
	return tx.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "scope_type"},
			{Name: "scope_key"},
			{Name: "chain_id"},
			{Name: "token_fingerprint"},
			{Name: "symbol"},
			{Name: "decimals"},
			{Name: "account"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"merchant_id",
			"domain_id",
			"wallet_id",
			"token",
			"balance_raw",
			"source_ledger_entry_count",
			"projected_at",
			"updated_at",
		}),
	}).Create(&projection).Error
}

func ledgerProjectionUUIDPtr(id uuid.UUID, ok bool) *uuid.UUID {
	if !ok {
		return nil
	}
	value := id
	return &value
}

func ledgerProjectionTokenPtr(tokenFingerprint string) *string {
	if strings.TrimSpace(tokenFingerprint) == "" || tokenFingerprint == "native" {
		return nil
	}
	value := strings.ToLower(strings.TrimSpace(tokenFingerprint))
	return &value
}

func reverseLedgerDirection(direction string) string {
	if direction == models.LedgerDirectionCredit {
		return models.LedgerDirectionDebit
	}
	return models.LedgerDirectionCredit
}

type ledgerHoldReleaseRequest struct {
	idColumn                string
	id                      uuid.UUID
	holdType                string
	releaseType             string
	transitAccount          string
	idempotencyKey          string
	consumedIdempotencyKeys []string
	description             string
}

func (r *LedgerRepo) activeHoldRows(ctx context.Context, tx *gorm.DB, req ledgerHoldReleaseRequest) ([]models.LedgerEntry, error) {
	if tx == nil || req.id == uuid.Nil {
		return nil, ErrLedgerReservationRequired
	}
	var rows []models.LedgerEntry
	err := tx.WithContext(ctx).
		Table("ledger_entries AS holds").
		Select("holds.*").
		Where("holds."+req.idColumn+" = ? AND holds.entry_type = ? AND holds.status = ?", req.id, req.holdType, models.LedgerStatusPending).
		Where("holds.account IN ?", []string{models.LedgerAccountMerchantAvailable, req.transitAccount}).
		Where("holds.amount_raw ~ '^[0-9]+$'").
		Where(`
			NOT EXISTS (
				SELECT 1
				FROM ledger_entries releases
				WHERE releases.reference = holds.id::text
				  AND releases.entry_type = ?
				  AND releases.status <> ?
			)
		`, req.releaseType, models.LedgerStatusVoided).
		Order("holds.account ASC").
		Find(&rows).Error
	return rows, err
}

func (r *LedgerRepo) releaseHeldEntries(ctx context.Context, tx *gorm.DB, req ledgerHoldReleaseRequest) error {
	if strings.TrimSpace(req.idempotencyKey) == "" {
		return errors.New("ledger release idempotency key is required")
	}
	consumed, err := r.releaseAlreadyConsumed(ctx, tx, req)
	if err != nil || consumed {
		return err
	}
	rows, err := r.activeHoldRows(ctx, tx, req)
	if err != nil || len(rows) == 0 {
		return err
	}
	if err := validateHoldReleaseRows(rows, req.transitAccount); err != nil {
		return err
	}
	if err := r.lockHeldEntryAssets(ctx, tx, rows); err != nil {
		return err
	}
	consumed, err = r.releaseAlreadyConsumed(ctx, tx, req)
	if err != nil || consumed {
		return err
	}
	rows, err = r.activeHoldRows(ctx, tx, req)
	if err != nil || len(rows) == 0 {
		return err
	}
	return r.appendHoldReleaseEntries(ctx, tx, rows, req.transitAccount, req.releaseType, req.idempotencyKey, req.description)
}

func (r *LedgerRepo) releaseAlreadyConsumed(ctx context.Context, tx *gorm.DB, req ledgerHoldReleaseRequest) (bool, error) {
	exists, err := r.existsWithDB(ctx, tx, req.idempotencyKey)
	if err != nil || exists {
		return exists, err
	}
	for _, consumedKey := range req.consumedIdempotencyKeys {
		consumed, err := r.existsWithDB(ctx, tx, consumedKey)
		if err != nil || consumed {
			return consumed, err
		}
	}
	return false, nil
}

func (r *LedgerRepo) appendHoldReleaseEntries(ctx context.Context, tx *gorm.DB, rows []models.LedgerEntry, transitAccount string, releaseType string, key string, description string) error {
	if len(rows) == 0 {
		return nil
	}
	if err := validateHoldReleaseRows(rows, transitAccount); err != nil {
		return err
	}
	if err := r.lockHeldEntryAssets(ctx, tx, rows); err != nil {
		return err
	}
	now := time.Now()
	releases := make([]models.LedgerEntry, 0, len(rows))
	for _, row := range rows {
		releases = append(releases, models.LedgerEntry{
			ID:                    uuid.New(),
			MerchantID:            row.MerchantID,
			DomainID:              row.DomainID,
			WalletID:              row.WalletID,
			PaymentID:             row.PaymentID,
			TransactionUniqueHash: row.TransactionUniqueHash,
			TransactionHash:       row.TransactionHash,
			WithdrawalID:          row.WithdrawalID,
			RefundID:              row.RefundID,
			SweepJobID:            row.SweepJobID,
			ChainID:               row.ChainID,
			Token:                 row.Token,
			Symbol:                row.Symbol,
			Decimals:              row.Decimals,
			EntryType:             releaseType,
			Account:               row.Account,
			Direction:             reverseLedgerDirection(row.Direction),
			Status:                models.LedgerStatusPosted,
			AmountRaw:             row.AmountRaw,
			IdempotencyKey:        key,
			Reference:             row.ID.String(),
			Description:           strings.TrimSpace(description + " " + row.ID.String()),
			PostedAt:              &now,
			CreatedAt:             now,
			UpdatedAt:             now,
		})
	}
	return r.appendLedgerEntries(ctx, tx, releases)
}

func (r *LedgerRepo) lockHeldEntryAssets(ctx context.Context, tx *gorm.DB, rows []models.LedgerEntry) error {
	locked := map[string]struct{}{}
	for _, row := range rows {
		lockKey := ledgerAssetLockMapKey(row.MerchantID, row.DomainID, row.ChainID, row.Token)
		if _, ok := locked[lockKey]; ok {
			continue
		}
		if err := r.lockLedgerAsset(ctx, tx, row.MerchantID, row.DomainID, row.ChainID, row.Token); err != nil {
			return err
		}
		locked[lockKey] = struct{}{}
	}
	return nil
}

func validateHoldReleaseRows(rows []models.LedgerEntry, transitAccount string) error {
	if len(rows) != 2 || strings.TrimSpace(transitAccount) == "" {
		return ErrLedgerReservationRequired
	}
	var available *models.LedgerEntry
	var transit *models.LedgerEntry
	for i := range rows {
		row := &rows[i]
		switch row.Account {
		case models.LedgerAccountMerchantAvailable:
			if row.Direction != models.LedgerDirectionDebit || available != nil {
				return ErrLedgerReservationRequired
			}
			available = row
		case transitAccount:
			if row.Direction != models.LedgerDirectionCredit || transit != nil {
				return ErrLedgerReservationRequired
			}
			transit = row
		default:
			return ErrLedgerReservationRequired
		}
	}
	if available == nil || transit == nil {
		return ErrLedgerReservationRequired
	}
	if !ledgerRowsShareReleaseScope(*available, *transit) {
		return ErrLedgerReservationRequired
	}
	return nil
}

func ledgerRowsShareReleaseScope(left models.LedgerEntry, right models.LedgerEntry) bool {
	if left.MerchantID != right.MerchantID || !sameUUIDPtr(left.DomainID, right.DomainID) || !sameUUIDPtr(left.WalletID, right.WalletID) {
		return false
	}
	if left.ChainID != right.ChainID || !sameOptionalToken(left.Token, right.Token) {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(left.Symbol), strings.TrimSpace(right.Symbol)) || left.Decimals != right.Decimals {
		return false
	}
	if strings.TrimSpace(left.AmountRaw) != strings.TrimSpace(right.AmountRaw) {
		return false
	}
	amount, ok := new(big.Int).SetString(strings.TrimSpace(left.AmountRaw), 10)
	return ok && amount.Sign() > 0
}

func ledgerAssetLockMapKey(merchantID uuid.UUID, domainID *uuid.UUID, chainID constants.ChainID, token *string) string {
	domain := ""
	if domainID != nil {
		domain = domainID.String()
	}
	tokenValue := "native"
	if token != nil && strings.TrimSpace(*token) != "" {
		tokenValue = strings.ToLower(strings.TrimSpace(*token))
	}
	return fmt.Sprintf("%s:%s:%d:%s", merchantID.String(), domain, chainID, tokenValue)
}

func (r *LedgerRepo) PostTransactionReversal(ctx context.Context, txModel models.Transaction) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return r.PostTransactionReversalWithDB(ctx, tx, txModel)
	})
}

func (r *LedgerRepo) PostTransactionReversalWithDB(ctx context.Context, tx *gorm.DB, txModel models.Transaction) error {
	if txModel.UniqueHash == "" {
		return nil
	}
	var originals []models.LedgerEntry
	if err := tx.WithContext(ctx).
		Where("transaction_unique_hash = ?", txModel.UniqueHash).
		Where("status <> ?", models.LedgerStatusVoided).
		Where("entry_type <> ?", models.LedgerEntryTypeReorgReversal).
		Where("amount_raw ~ '^[0-9]+$'").
		Find(&originals).Error; err != nil {
		return err
	}
	if len(originals) == 0 {
		return nil
	}
	now := time.Now()
	reversals := make([]models.LedgerEntry, 0, len(originals))
	for _, original := range originals {
		key := "reorg-reversal:" + original.ID.String()
		exists, err := r.existsWithDB(ctx, tx, key)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		reversals = append(reversals, models.LedgerEntry{
			ID:                    uuid.New(),
			MerchantID:            original.MerchantID,
			DomainID:              original.DomainID,
			WalletID:              original.WalletID,
			PaymentID:             original.PaymentID,
			TransactionUniqueHash: original.TransactionUniqueHash,
			TransactionHash:       original.TransactionHash,
			WithdrawalID:          original.WithdrawalID,
			RefundID:              original.RefundID,
			SweepJobID:            original.SweepJobID,
			ChainID:               original.ChainID,
			Token:                 original.Token,
			Symbol:                original.Symbol,
			Decimals:              original.Decimals,
			EntryType:             models.LedgerEntryTypeReorgReversal,
			Account:               original.Account,
			Direction:             reverseLedgerDirection(original.Direction),
			Status:                models.LedgerStatusPosted,
			AmountRaw:             original.AmountRaw,
			IdempotencyKey:        key,
			Reference:             original.ID.String(),
			Description:           "Reorg reversal for ledger entry " + original.ID.String(),
			PostedAt:              &now,
			CreatedAt:             now,
			UpdatedAt:             now,
		})
	}
	if len(reversals) == 0 {
		return nil
	}
	return tx.WithContext(ctx).Create(&reversals).Error
}

func (r *LedgerRepo) lockLedgerAsset(ctx context.Context, tx *gorm.DB, merchantID uuid.UUID, domainID *uuid.UUID, chainID constants.ChainID, token *string) error {
	tokenKey := "native"
	if token != nil && strings.TrimSpace(*token) != "" {
		tokenKey = strings.ToLower(strings.TrimSpace(*token))
	}
	scope := "merchant"
	if domainID != nil {
		scope = domainID.String()
	}
	lockKey := fmt.Sprintf("ledger-balance:%s:%s:%d:%s", merchantID.String(), scope, chainID, tokenKey)
	return tx.WithContext(ctx).Exec("SELECT pg_advisory_xact_lock(hashtext(?))", lockKey).Error
}

func (r *LedgerRepo) ensureAvailableBalance(ctx context.Context, tx *gorm.DB, merchantID uuid.UUID, domainID *uuid.UUID, chainID constants.ChainID, token *string, amountRaw string) error {
	requested, ok := new(big.Int).SetString(amountRaw, 10)
	if !ok || requested.Sign() <= 0 {
		return errors.New("withdrawal amount must be positive")
	}

	query := tx.WithContext(ctx).
		Model(&models.LedgerEntry{}).
		Select("COALESCE(SUM(CASE WHEN direction = 'credit' THEN amount_raw::numeric ELSE -amount_raw::numeric END), 0)::text").
		Where("merchant_id = ? AND chain_id = ? AND account = ? AND status IN ?", merchantID, chainID, models.LedgerAccountMerchantAvailable, []string{models.LedgerStatusPending, models.LedgerStatusPosted}).
		Where("amount_raw ~ '^[0-9]+$'")
	if domainID != nil {
		query = query.Where("domain_id = ?", *domainID)
	}
	if token == nil || strings.TrimSpace(*token) == "" {
		query = query.Where("token IS NULL OR token = ''")
	} else {
		query = query.Where("LOWER(token) = LOWER(?)", strings.TrimSpace(*token))
	}

	var raw string
	if err := query.Scan(&raw).Error; err != nil {
		return err
	}
	if raw == "" {
		raw = "0"
	}
	available, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		return fmt.Errorf("invalid available balance: %s", raw)
	}
	if available.Cmp(requested) < 0 {
		return fmt.Errorf("%w: available=%s amount=%s", ErrInsufficientAvailableBalance, available.String(), requested.String())
	}
	return nil
}

func withdrawalHoldKey(id uuid.UUID) string {
	return "withdrawal-hold:" + id.String()
}

func withdrawalReleaseKey(id uuid.UUID) string {
	return "withdrawal-release:" + id.String()
}

func withdrawalDebitKey(id uuid.UUID) string {
	return "withdrawal-debit:" + id.String()
}

func refundHoldKey(id uuid.UUID) string {
	return "refund-hold:" + id.String()
}

func refundReleaseKey(id uuid.UUID) string {
	return "refund-release:" + id.String()
}

func refundWalletRealignmentReleaseKey(refundID uuid.UUID, walletID uuid.UUID) string {
	return "refund-release:" + refundID.String() + ":wallet-realign:" + walletID.String()
}

func refundWalletRealignmentHoldKey(refundID uuid.UUID, walletID uuid.UUID) string {
	return "refund-hold:" + refundID.String() + ":wallet-realign:" + walletID.String()
}

func refundDebitKey(id uuid.UUID) string {
	return "refund-debit:" + id.String()
}

func sweepHoldKey(id uuid.UUID) string {
	return "sweep-hold:" + id.String()
}

func sweepHoldReleaseKey(id uuid.UUID) string {
	return "sweep-hold-release:" + id.String()
}

func sweepReleaseKey(id uuid.UUID) string {
	return "sweep-release:" + id.String()
}

func ledgerRefundAssetFromSession(session models.PaymentSession) (constants.ChainID, string, uint8, error) {
	if session.SelectedChainID == nil {
		return 0, "", 0, errors.New("refund payment asset chain is missing")
	}
	if !constants.IsSupportedChainID(*session.SelectedChainID) {
		return 0, "", 0, errors.New("refund payment asset chain is unsupported")
	}
	symbol := strings.ToUpper(strings.TrimSpace(session.SelectedSymbol))
	if symbol == "" {
		symbol = strings.ToUpper(constants.ChainName(*session.SelectedChainID))
	}
	return *session.SelectedChainID, symbol, session.SelectedDecimals, nil
}

func refundLedgerWalletID(refund models.Refund, session models.PaymentSession) uuid.UUID {
	if refund.WalletID != nil && *refund.WalletID != uuid.Nil {
		return *refund.WalletID
	}
	return session.WalletID
}

func (r *LedgerRepo) CreateRefundHold(ctx context.Context, refund models.Refund, session models.PaymentSession) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return r.createRefundHold(ctx, tx, refund, session)
	})
}

func (r *LedgerRepo) CreateRefundHoldWithDB(ctx context.Context, tx *gorm.DB, refund models.Refund, session models.PaymentSession) error {
	return r.createRefundHold(ctx, tx, refund, session)
}

func (r *LedgerRepo) createRefundHold(ctx context.Context, tx *gorm.DB, refund models.Refund, session models.PaymentSession) error {
	key := refundHoldKey(refund.ID)
	exists, err := r.existsWithDB(ctx, tx, key)
	if err != nil || exists {
		return err
	}
	if !r.amountIsPositive(refund.AmountRaw) {
		return errors.New("refund amount must be positive")
	}
	if refund.PaymentID != session.ID {
		return errors.New("refund payment mismatch")
	}
	if refund.MerchantID != session.MerchantID || refund.DomainID != session.DomainID {
		return errors.New("refund payment merchant/domain mismatch")
	}
	chainID, symbol, decimals, err := ledgerRefundAssetFromSession(session)
	if err != nil {
		return err
	}
	domainID := refund.DomainID
	refundID := refund.ID
	paymentID := session.ID
	walletID := refundLedgerWalletID(refund, session)
	if err := r.lockLedgerAsset(ctx, tx, refund.MerchantID, &domainID, chainID, session.SelectedToken); err != nil {
		return err
	}
	if err := r.ensureAvailableBalance(ctx, tx, refund.MerchantID, &domainID, chainID, session.SelectedToken, refund.AmountRaw); err != nil {
		return err
	}
	now := time.Now()
	entries := []models.LedgerEntry{
		{
			ID:             uuid.New(),
			MerchantID:     refund.MerchantID,
			DomainID:       &domainID,
			WalletID:       &walletID,
			PaymentID:      &paymentID,
			RefundID:       &refundID,
			ChainID:        chainID,
			Token:          session.SelectedToken,
			Symbol:         symbol,
			Decimals:       decimals,
			EntryType:      models.LedgerEntryTypeRefundHold,
			Account:        models.LedgerAccountMerchantAvailable,
			Direction:      models.LedgerDirectionDebit,
			Status:         models.LedgerStatusPending,
			AmountRaw:      refund.AmountRaw,
			IdempotencyKey: key,
			Reference:      refund.ID.String(),
			PostedAt:       &now,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		{
			ID:             uuid.New(),
			MerchantID:     refund.MerchantID,
			DomainID:       &domainID,
			WalletID:       &walletID,
			PaymentID:      &paymentID,
			RefundID:       &refundID,
			ChainID:        chainID,
			Token:          session.SelectedToken,
			Symbol:         symbol,
			Decimals:       decimals,
			EntryType:      models.LedgerEntryTypeRefundHold,
			Account:        models.LedgerAccountRefundTransit,
			Direction:      models.LedgerDirectionCredit,
			Status:         models.LedgerStatusPending,
			AmountRaw:      refund.AmountRaw,
			IdempotencyKey: key,
			Reference:      refund.ID.String(),
			PostedAt:       &now,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	return r.appendLedgerEntries(ctx, tx, entries)
}

func (r *LedgerRepo) AlignRefundHoldWalletWithDB(ctx context.Context, tx *gorm.DB, refundID uuid.UUID, walletID uuid.UUID) error {
	if refundID == uuid.Nil || walletID == uuid.Nil {
		return ErrLedgerReservationRequired
	}
	req := ledgerHoldReleaseRequest{
		idColumn:       "refund_id",
		id:             refundID,
		holdType:       models.LedgerEntryTypeRefundHold,
		releaseType:    models.LedgerEntryTypeRefundRelease,
		transitAccount: models.LedgerAccountRefundTransit,
	}
	rows, err := r.activeHoldRows(ctx, tx, req)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return ErrLedgerReservationRequired
	}
	if ledgerHoldRowsContainWalletPair(rows, walletID, models.LedgerAccountRefundTransit) {
		return nil
	}
	releaseKey := refundWalletRealignmentReleaseKey(refundID, walletID)
	exists, err := r.existsWithDB(ctx, tx, releaseKey)
	if err != nil || exists {
		return err
	}
	if err := r.appendHoldReleaseEntries(ctx, tx, rows, models.LedgerAccountRefundTransit, models.LedgerEntryTypeRefundRelease, releaseKey, "Refund hold wallet realignment release for ledger entry"); err != nil {
		return err
	}
	holdKey := refundWalletRealignmentHoldKey(refundID, walletID)
	exists, err = r.existsWithDB(ctx, tx, holdKey)
	if err != nil || exists {
		return err
	}
	return r.appendRefundReplacementHoldEntries(ctx, tx, rows, walletID, holdKey)
}

func ledgerHoldRowsContainWalletPair(rows []models.LedgerEntry, walletID uuid.UUID, transitAccount string) bool {
	matches := map[string]bool{}
	for _, row := range rows {
		if row.WalletID == nil || *row.WalletID != walletID {
			continue
		}
		if row.Account == models.LedgerAccountMerchantAvailable || row.Account == transitAccount {
			matches[row.Account] = true
		}
	}
	return matches[models.LedgerAccountMerchantAvailable] && matches[transitAccount]
}

func (r *LedgerRepo) appendRefundReplacementHoldEntries(ctx context.Context, tx *gorm.DB, originals []models.LedgerEntry, walletID uuid.UUID, key string) error {
	if len(originals) == 0 {
		return ErrLedgerReservationRequired
	}
	now := time.Now()
	entries := make([]models.LedgerEntry, 0, len(originals))
	for _, original := range originals {
		next := original
		next.ID = uuid.New()
		next.WalletID = &walletID
		next.EntryType = models.LedgerEntryTypeRefundHold
		next.Status = models.LedgerStatusPending
		next.IdempotencyKey = key
		next.Reference = ""
		if original.RefundID != nil {
			next.Reference = original.RefundID.String()
		}
		next.Description = "Refund hold wallet realignment from ledger entry " + original.ID.String()
		next.PostedAt = &now
		next.VoidedAt = nil
		next.CreatedAt = now
		next.UpdatedAt = now
		entries = append(entries, next)
	}
	return r.appendLedgerEntries(ctx, tx, entries)
}

func (r *LedgerRepo) VoidRefundHold(ctx context.Context, refundID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return r.voidRefundHold(ctx, tx, refundID)
	})
}

func (r *LedgerRepo) VoidRefundHoldWithDB(ctx context.Context, tx *gorm.DB, refundID uuid.UUID) error {
	return r.voidRefundHold(ctx, tx, refundID)
}

func (r *LedgerRepo) voidRefundHold(ctx context.Context, tx *gorm.DB, refundID uuid.UUID) error {
	return r.releaseHeldEntries(ctx, tx, ledgerHoldReleaseRequest{
		idColumn:       "refund_id",
		id:             refundID,
		holdType:       models.LedgerEntryTypeRefundHold,
		releaseType:    models.LedgerEntryTypeRefundRelease,
		transitAccount: models.LedgerAccountRefundTransit,
		idempotencyKey: refundReleaseKey(refundID),
		consumedIdempotencyKeys: []string{
			refundDebitKey(refundID),
		},
		description: "Refund hold release for ledger entry",
	})
}

func (r *LedgerRepo) CreateSweepHold(ctx context.Context, job models.SweepJob, txModel models.Transaction) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return r.createSweepHold(ctx, tx, job, txModel)
	})
}

func (r *LedgerRepo) CreateSweepHoldWithDB(ctx context.Context, tx *gorm.DB, job models.SweepJob, txModel models.Transaction) error {
	return r.createSweepHold(ctx, tx, job, txModel)
}

func (r *LedgerRepo) createSweepHold(ctx context.Context, tx *gorm.DB, job models.SweepJob, txModel models.Transaction) error {
	if job.ID == uuid.Nil || txModel.MerchantID == nil || txModel.WalletID == nil || txModel.UniqueHash == "" {
		return ErrLedgerReservationRequired
	}
	if err := validateSweepJobTransaction(job, txModel); err != nil {
		return err
	}
	if !r.amountIsPositive(txModel.Amount) {
		return errors.New("sweep amount must be positive")
	}
	key := sweepHoldKey(job.ID)
	exists, err := r.existsWithDB(ctx, tx, key)
	if err != nil || exists {
		return err
	}
	chainID := txModel.ChainID
	symbol := strings.ToUpper(strings.TrimSpace(txModel.Symbol))
	if symbol == "" {
		symbol = strings.ToUpper(constants.ChainName(chainID))
	}
	if err := r.lockLedgerAsset(ctx, tx, *txModel.MerchantID, txModel.DomainID, chainID, txModel.Token); err != nil {
		return err
	}
	if err := r.ensureAvailableBalance(ctx, tx, *txModel.MerchantID, txModel.DomainID, chainID, txModel.Token, txModel.Amount); err != nil {
		return err
	}
	now := time.Now()
	sweepJobID := job.ID
	entries := []models.LedgerEntry{
		{
			ID:                    uuid.New(),
			MerchantID:            *txModel.MerchantID,
			DomainID:              txModel.DomainID,
			WalletID:              txModel.WalletID,
			TransactionUniqueHash: txModel.UniqueHash,
			TransactionHash:       txModel.Hash,
			SweepJobID:            &sweepJobID,
			ChainID:               chainID,
			Token:                 txModel.Token,
			Symbol:                symbol,
			Decimals:              txModel.Decimals,
			EntryType:             models.LedgerEntryTypeSweepHold,
			Account:               models.LedgerAccountMerchantAvailable,
			Direction:             models.LedgerDirectionDebit,
			Status:                models.LedgerStatusPending,
			AmountRaw:             txModel.Amount,
			IdempotencyKey:        key,
			Reference:             job.ID.String(),
			PostedAt:              &now,
			CreatedAt:             now,
			UpdatedAt:             now,
		},
		{
			ID:                    uuid.New(),
			MerchantID:            *txModel.MerchantID,
			DomainID:              txModel.DomainID,
			WalletID:              txModel.WalletID,
			TransactionUniqueHash: txModel.UniqueHash,
			TransactionHash:       txModel.Hash,
			SweepJobID:            &sweepJobID,
			ChainID:               chainID,
			Token:                 txModel.Token,
			Symbol:                symbol,
			Decimals:              txModel.Decimals,
			EntryType:             models.LedgerEntryTypeSweepHold,
			Account:               models.LedgerAccountSweepTransit,
			Direction:             models.LedgerDirectionCredit,
			Status:                models.LedgerStatusPending,
			AmountRaw:             txModel.Amount,
			IdempotencyKey:        key,
			Reference:             job.ID.String(),
			PostedAt:              &now,
			CreatedAt:             now,
			UpdatedAt:             now,
		},
	}
	return r.appendLedgerEntries(ctx, tx, entries)
}

func (r *LedgerRepo) VoidSweepHold(ctx context.Context, sweepJobID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return r.VoidSweepHoldWithDB(ctx, tx, sweepJobID)
	})
}

func (r *LedgerRepo) VoidSweepHoldWithDB(ctx context.Context, tx *gorm.DB, sweepJobID uuid.UUID) error {
	return r.releaseHeldEntries(ctx, tx, ledgerHoldReleaseRequest{
		idColumn:       "sweep_job_id",
		id:             sweepJobID,
		holdType:       models.LedgerEntryTypeSweepHold,
		releaseType:    models.LedgerEntryTypeSweepRelease,
		transitAccount: models.LedgerAccountSweepTransit,
		idempotencyKey: sweepHoldReleaseKey(sweepJobID),
		consumedIdempotencyKeys: []string{
			sweepReleaseKey(sweepJobID),
		},
		description: "Sweep hold release for ledger entry",
	})
}

func (r *LedgerRepo) ReleaseSweepHoldsForWithdrawalWithDB(ctx context.Context, tx *gorm.DB, request models.WithdrawalRequest) error {
	if tx == nil || request.ID == uuid.Nil || request.MerchantID == uuid.Nil || request.WalletID == uuid.Nil {
		return ErrLedgerReservationRequired
	}
	requested, ok := new(big.Int).SetString(strings.TrimSpace(request.AmountRaw), 10)
	if !ok || requested.Sign() <= 0 {
		return errors.New("withdrawal amount must be positive")
	}
	chainID, ok := ledgerChainIDFromName(request.Chain)
	if !ok {
		return errors.New("unsupported withdrawal chain")
	}
	if err := r.lockLedgerAsset(ctx, tx, request.MerchantID, request.DomainID, chainID, request.Token); err != nil {
		return err
	}

	type sweepHoldCandidate struct {
		SweepJobID uuid.UUID `gorm:"column:sweep_job_id"`
		AmountRaw  string    `gorm:"column:amount_raw"`
	}
	var candidates []sweepHoldCandidate
	query := tx.WithContext(ctx).
		Model(&models.LedgerEntry{}).
		Select("sweep_job_id, SUM(amount_raw::numeric)::text AS amount_raw").
		Where("sweep_job_id IS NOT NULL").
		Where("entry_type = ? AND account = ? AND status = ?", models.LedgerEntryTypeSweepHold, models.LedgerAccountSweepTransit, models.LedgerStatusPending).
		Where("merchant_id = ? AND wallet_id = ? AND chain_id = ?", request.MerchantID, request.WalletID, chainID).
		Where("amount_raw ~ '^[0-9]+$'").
		Where(`
			NOT EXISTS (
				SELECT 1
				FROM ledger_entries releases
				WHERE releases.reference = ledger_entries.id::text
				  AND releases.entry_type = ?
				  AND releases.status <> ?
			)
		`, models.LedgerEntryTypeSweepRelease, models.LedgerStatusVoided)
	if request.DomainID != nil {
		query = query.Where("domain_id = ?", *request.DomainID)
	} else {
		query = query.Where("domain_id IS NULL")
	}
	if request.Token == nil || strings.TrimSpace(*request.Token) == "" {
		query = query.Where("token IS NULL OR token = ''")
	} else {
		query = query.Where("LOWER(token) = LOWER(?)", strings.TrimSpace(*request.Token))
	}
	if err := query.Group("sweep_job_id").Order("MIN(created_at) ASC, sweep_job_id ASC").Find(&candidates).Error; err != nil {
		return err
	}

	released := big.NewInt(0)
	for _, candidate := range candidates {
		if candidate.SweepJobID == uuid.Nil {
			continue
		}
		amount, ok := new(big.Int).SetString(strings.TrimSpace(candidate.AmountRaw), 10)
		if !ok || amount.Sign() <= 0 {
			continue
		}
		if err := r.VoidSweepHoldWithDB(ctx, tx, candidate.SweepJobID); err != nil {
			return err
		}
		released.Add(released, amount)
		if released.Cmp(requested) >= 0 {
			return nil
		}
	}
	return fmt.Errorf("%w: recoverable_sweep_locked=%s amount=%s", ErrInsufficientAvailableBalance, released.String(), requested.String())
}

func (r *LedgerRepo) PostSweepRelease(ctx context.Context, job models.SweepJob, txModel models.Transaction, sweepTxHash string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return r.PostSweepReleaseWithDB(ctx, tx, job, txModel, sweepTxHash)
	})
}

func (r *LedgerRepo) PostSweepReleaseWithDB(ctx context.Context, tx *gorm.DB, job models.SweepJob, txModel models.Transaction, sweepTxHash string) error {
	if job.ID == uuid.Nil || txModel.MerchantID == nil || txModel.WalletID == nil {
		return ErrLedgerReservationRequired
	}
	sweepTxHash = strings.TrimSpace(sweepTxHash)
	if sweepTxHash == "" {
		return errors.New("sweep transaction hash is required")
	}
	if err := validateSweepJobTransaction(job, txModel); err != nil {
		return err
	}
	if !r.amountIsPositive(txModel.Amount) {
		return errors.New("sweep amount must be positive")
	}
	key := sweepReleaseKey(job.ID)
	exists, err := r.existsWithDB(ctx, tx, key)
	if err != nil || exists {
		return err
	}
	chainID := txModel.ChainID
	if err := r.lockLedgerAsset(ctx, tx, *txModel.MerchantID, txModel.DomainID, chainID, txModel.Token); err != nil {
		return err
	}
	if err := r.RequireSweepHoldForJobTransactionWithDB(ctx, tx, job, txModel); err != nil {
		return err
	}
	symbol := strings.ToUpper(strings.TrimSpace(txModel.Symbol))
	if symbol == "" {
		symbol = strings.ToUpper(constants.ChainName(chainID))
	}
	now := time.Now()
	sweepJobID := job.ID
	entries := []models.LedgerEntry{
		{
			ID:                    uuid.New(),
			MerchantID:            *txModel.MerchantID,
			DomainID:              txModel.DomainID,
			WalletID:              txModel.WalletID,
			TransactionUniqueHash: txModel.UniqueHash,
			TransactionHash:       sweepTxHash,
			SweepJobID:            &sweepJobID,
			ChainID:               chainID,
			Token:                 txModel.Token,
			Symbol:                symbol,
			Decimals:              txModel.Decimals,
			EntryType:             models.LedgerEntryTypeSweepRelease,
			Account:               models.LedgerAccountMerchantAvailable,
			Direction:             models.LedgerDirectionCredit,
			Status:                models.LedgerStatusPosted,
			AmountRaw:             txModel.Amount,
			IdempotencyKey:        key,
			Reference:             job.ID.String(),
			PostedAt:              &now,
			CreatedAt:             now,
			UpdatedAt:             now,
		},
		{
			ID:                    uuid.New(),
			MerchantID:            *txModel.MerchantID,
			DomainID:              txModel.DomainID,
			WalletID:              txModel.WalletID,
			TransactionUniqueHash: txModel.UniqueHash,
			TransactionHash:       sweepTxHash,
			SweepJobID:            &sweepJobID,
			ChainID:               chainID,
			Token:                 txModel.Token,
			Symbol:                symbol,
			Decimals:              txModel.Decimals,
			EntryType:             models.LedgerEntryTypeSweepRelease,
			Account:               models.LedgerAccountSweepTransit,
			Direction:             models.LedgerDirectionDebit,
			Status:                models.LedgerStatusPosted,
			AmountRaw:             txModel.Amount,
			IdempotencyKey:        key,
			Reference:             job.ID.String(),
			PostedAt:              &now,
			CreatedAt:             now,
			UpdatedAt:             now,
		},
	}
	return r.appendLedgerEntries(ctx, tx, entries)
}

func validateSweepJobTransaction(job models.SweepJob, txModel models.Transaction) error {
	if job.TransactionUniqueHash == "" || txModel.UniqueHash == "" || job.TransactionUniqueHash != txModel.UniqueHash {
		return errors.New("sweep job transaction mismatch")
	}
	if txModel.MerchantID == nil || job.MerchantID == uuid.Nil || job.MerchantID != *txModel.MerchantID {
		return errors.New("sweep job merchant mismatch")
	}
	if txModel.WalletID == nil || job.WalletID == uuid.Nil || job.WalletID != *txModel.WalletID {
		return errors.New("sweep job wallet mismatch")
	}
	if job.ChainID != txModel.ChainID {
		return errors.New("sweep job chain mismatch")
	}
	if !sameOptionalToken(job.Token, txModel.Token) {
		return errors.New("sweep job token mismatch")
	}
	return nil
}

func (r *LedgerRepo) PostRefundDebit(ctx context.Context, refund models.Refund, session models.PaymentSession, txHash string) error {
	return r.PostRefundDebitWithDB(ctx, r.db, refund, session, txHash)
}

func (r *LedgerRepo) PostRefundDebitWithDB(ctx context.Context, tx *gorm.DB, refund models.Refund, session models.PaymentSession, txHash string) error {
	key := refundDebitKey(refund.ID)
	exists, err := r.existsWithDB(ctx, tx, key)
	if err != nil || exists {
		return err
	}
	if !r.amountIsPositive(refund.AmountRaw) {
		return errors.New("refund amount must be positive")
	}
	chainID, symbol, decimals, err := ledgerRefundAssetFromSession(session)
	if err != nil {
		return err
	}
	if err := r.lockLedgerAsset(ctx, tx, refund.MerchantID, &refund.DomainID, chainID, session.SelectedToken); err != nil {
		return err
	}
	if err := r.RequireRefundHoldForRefundWithDB(ctx, tx, refund, session); err != nil {
		return err
	}
	now := time.Now()
	refundID := refund.ID
	paymentID := session.ID
	walletID := refundLedgerWalletID(refund, session)
	entries := []models.LedgerEntry{
		{
			ID:              uuid.New(),
			MerchantID:      refund.MerchantID,
			DomainID:        &refund.DomainID,
			WalletID:        &walletID,
			PaymentID:       &paymentID,
			RefundID:        &refundID,
			TransactionHash: txHash,
			ChainID:         chainID,
			Token:           session.SelectedToken,
			Symbol:          symbol,
			Decimals:        decimals,
			EntryType:       models.LedgerEntryTypeRefundDebit,
			Account:         models.LedgerAccountRefundTransit,
			Direction:       models.LedgerDirectionDebit,
			Status:          models.LedgerStatusPosted,
			AmountRaw:       refund.AmountRaw,
			IdempotencyKey:  key,
			Reference:       refund.ID.String(),
			PostedAt:        &now,
			CreatedAt:       now,
			UpdatedAt:       now,
		},
		{
			ID:              uuid.New(),
			MerchantID:      refund.MerchantID,
			DomainID:        &refund.DomainID,
			WalletID:        &walletID,
			PaymentID:       &paymentID,
			RefundID:        &refundID,
			TransactionHash: txHash,
			ChainID:         chainID,
			Token:           session.SelectedToken,
			Symbol:          symbol,
			Decimals:        decimals,
			EntryType:       models.LedgerEntryTypeRefundDebit,
			Account:         models.LedgerAccountPlatformClearing,
			Direction:       models.LedgerDirectionCredit,
			Status:          models.LedgerStatusPosted,
			AmountRaw:       refund.AmountRaw,
			IdempotencyKey:  key,
			Reference:       refund.ID.String(),
			PostedAt:        &now,
			CreatedAt:       now,
			UpdatedAt:       now,
		},
	}
	return r.appendLedgerEntries(ctx, tx, entries)
}

func ledgerChainIDFromName(name string) (constants.ChainID, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "bitcoin", "btc":
		return constants.Bitcoin, true
	case "ethereum", "eth":
		return constants.Ethereum, true
	case "base":
		return constants.Base, true
	case "arbitrum", "arb", "arbitrum-one":
		return constants.Arbitrum, true
	case "bnbchain", "bsc", "binance":
		return constants.Binance, true
	case "unichain":
		return constants.Unichain, true
	case "avalanche", "avax":
		return constants.Avalanche, true
	case "chiliz", "chz":
		return constants.Chiliz, true
	case "chiliz-spicy", "spicy":
		return constants.ChilizSpicy, true
	case "solana", "sol":
		return constants.Solana, true
	case "tron", "trx":
		return constants.TRON, true
	case "tron-testnet", "trx-testnet", "nile", "tron-nile", "trx-nile", "tron-shasta", "shasta":
		return constants.TRONTestnet, true
	default:
		return 0, false
	}
}

type LedgerBalanceRow struct {
	MerchantID uuid.UUID
	DomainID   *uuid.UUID
	WalletID   *uuid.UUID
	ChainID    int64
	Token      *string
	Symbol     string
	Decimals   uint8
	Account    string
	BalanceRaw string
}

type LedgerWalletBalanceIDRow struct {
	WalletID     uuid.UUID
	AvailableRaw string
}

type LedgerInvariantIssue struct {
	IdempotencyKey string
	MerchantID     uuid.UUID
	DomainID       *uuid.UUID
	ChainID        int64
	Token          *string
	Symbol         string
	NetRaw         string
}

func (r *LedgerRepo) FindInvariantIssues(ctx context.Context, limit int) ([]LedgerInvariantIssue, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var rows []LedgerInvariantIssue
	err := r.db.WithContext(ctx).Raw(`
		SELECT idempotency_key,
		       merchant_id,
		       domain_id,
		       chain_id,
		       token,
		       symbol,
		       SUM(CASE WHEN direction = 'credit' THEN amount_raw::numeric ELSE -amount_raw::numeric END)::text AS net_raw
		FROM ledger_entries
		WHERE idempotency_key <> ''
		  AND status <> 'voided'
		  AND amount_raw ~ '^[0-9]+$'
		GROUP BY idempotency_key, merchant_id, domain_id, chain_id, token, symbol
		HAVING SUM(CASE WHEN direction = 'credit' THEN amount_raw::numeric ELSE -amount_raw::numeric END) <> 0
		ORDER BY idempotency_key ASC
		LIMIT ?
	`, limit).Scan(&rows).Error
	return rows, err
}

func (r *LedgerRepo) WalletIDsWithPositiveAvailableBalance(ctx context.Context, chainID constants.ChainID, token *string, page, limit int) ([]uuid.UUID, int64, error) {
	if r == nil || r.db == nil {
		return nil, 0, gorm.ErrInvalidDB
	}
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	tokenValue := ""
	if token != nil {
		tokenValue = strings.ToLower(strings.TrimSpace(*token))
	}

	tokenPredicate := "(token IS NULL OR token = '')"
	args := []any{
		int64(chainID),
	}
	if tokenValue != "" {
		tokenPredicate = "LOWER(token) = ?"
		args = append(args, tokenValue)
	}

	baseQuery := fmt.Sprintf(`
		FROM ledger_entries
		WHERE wallet_id IS NOT NULL
		  AND chain_id = ?
		  AND account IN ('merchant_available', 'sweep_transit')
		  AND status IN ('pending', 'posted')
		  AND amount_raw ~ '^[0-9]+$'
		  AND %s
		GROUP BY wallet_id
		HAVING SUM(CASE WHEN direction = 'credit' THEN amount_raw::numeric ELSE -amount_raw::numeric END) > 0
	`, tokenPredicate)

	var total int64
	countQuery := "SELECT COUNT(*) FROM (SELECT wallet_id " + baseQuery + ") positive_wallets"
	if err := r.db.WithContext(ctx).Raw(countQuery, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []uuid.UUID{}, 0, nil
	}

	rows := make([]LedgerWalletBalanceIDRow, 0, limit)
	queryArgs := append(append([]any{}, args...), limit, (page-1)*limit)
	query := `
		SELECT wallet_id,
		       SUM(CASE WHEN direction = 'credit' THEN amount_raw::numeric ELSE -amount_raw::numeric END)::text AS available_raw
	` + baseQuery + `
		ORDER BY SUM(CASE WHEN direction = 'credit' THEN amount_raw::numeric ELSE -amount_raw::numeric END) DESC, wallet_id ASC
		LIMIT ? OFFSET ?
	`
	if err := r.db.WithContext(ctx).Raw(query, queryArgs...).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		if row.WalletID == uuid.Nil {
			continue
		}
		ids = append(ids, row.WalletID)
	}
	return ids, total, nil
}

func (r *LedgerRepo) WalletBalancesByWalletIDs(ctx context.Context, walletIDs []uuid.UUID) ([]LedgerBalanceRow, error) {
	if r == nil || r.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	seen := make(map[uuid.UUID]struct{}, len(walletIDs))
	ids := make([]uuid.UUID, 0, len(walletIDs))
	for _, id := range walletIDs {
		if id == uuid.Nil {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return []LedgerBalanceRow{}, nil
	}
	if rows, ok, err := r.walletBalanceProjectionsByWalletIDs(ctx, ids); err != nil {
		return nil, err
	} else if ok {
		return rows, nil
	}

	var rows []LedgerBalanceRow
	err := r.db.WithContext(ctx).Raw(`
		SELECT merchant_id,
		       domain_id,
		       wallet_id,
		       chain_id,
		       token,
		       symbol,
		       decimals,
		       account,
		       SUM(CASE WHEN direction = 'credit' THEN amount_raw::numeric ELSE -amount_raw::numeric END)::text AS balance_raw
		FROM ledger_entries
			WHERE wallet_id IN ?
			  AND status IN ('pending', 'posted')
			  AND amount_raw ~ '^[0-9]+$'
		GROUP BY merchant_id, domain_id, wallet_id, chain_id, token, symbol, decimals, account
		ORDER BY wallet_id ASC, chain_id ASC, symbol ASC, account ASC
	`, ids).Scan(&rows).Error
	return rows, err
}

func (r *LedgerRepo) MerchantBalances(ctx context.Context, merchantID uuid.UUID) ([]LedgerBalanceRow, error) {
	if rows, ok, err := r.balanceProjectionsOrEmpty(ctx, models.LedgerBalanceProjectionScopeMerchant, ledgerBalanceProjectionScopeKey(models.LedgerBalanceProjectionScopeMerchant, merchantID, nil, nil)); err != nil {
		return nil, err
	} else if ok {
		return rows, nil
	}
	var rows []LedgerBalanceRow
	err := r.db.WithContext(ctx).Raw(`
		SELECT merchant_id,
		       chain_id,
		       token,
		       symbol,
		       decimals,
		       account,
		       SUM(CASE WHEN direction = 'credit' THEN amount_raw::numeric ELSE -amount_raw::numeric END)::text AS balance_raw
		FROM ledger_entries
		WHERE merchant_id = ?
		  AND status IN ('pending', 'posted')
		  AND amount_raw ~ '^[0-9]+$'
		GROUP BY merchant_id, chain_id, token, symbol, decimals, account
		ORDER BY chain_id ASC, symbol ASC, account ASC
	`, merchantID).Scan(&rows).Error
	return rows, err
}

func (r *LedgerRepo) PlatformBalances(ctx context.Context) ([]LedgerBalanceRow, error) {
	if rows, ok, err := r.balanceProjectionsOrEmpty(ctx, models.LedgerBalanceProjectionScopePlatform, ledgerBalanceProjectionScopeKey(models.LedgerBalanceProjectionScopePlatform, uuid.Nil, nil, nil)); err != nil {
		return nil, err
	} else if ok {
		return rows, nil
	}
	var rows []LedgerBalanceRow
	err := r.db.WithContext(ctx).Raw(`
		SELECT chain_id,
		       token,
		       symbol,
		       decimals,
		       account,
		       SUM(CASE WHEN direction = 'credit' THEN amount_raw::numeric ELSE -amount_raw::numeric END)::text AS balance_raw
		FROM ledger_entries
		WHERE account IN ('merchant_pending', 'merchant_available', 'withdrawal_transit', 'refund_transit', 'sweep_transit')
		  AND status IN ('pending', 'posted')
		  AND amount_raw ~ '^[0-9]+$'
		GROUP BY chain_id, token, symbol, decimals, account
		HAVING SUM(CASE WHEN direction = 'credit' THEN amount_raw::numeric ELSE -amount_raw::numeric END) <> 0
		ORDER BY chain_id ASC, symbol ASC, account ASC
	`).Scan(&rows).Error
	return rows, err
}

func (r *LedgerRepo) DomainBalances(ctx context.Context, merchantID, domainID uuid.UUID) ([]LedgerBalanceRow, error) {
	if rows, ok, err := r.balanceProjectionsOrEmpty(ctx, models.LedgerBalanceProjectionScopeDomain, ledgerBalanceProjectionScopeKey(models.LedgerBalanceProjectionScopeDomain, merchantID, &domainID, nil)); err != nil {
		return nil, err
	} else if ok {
		return rows, nil
	}
	var rows []LedgerBalanceRow
	err := r.db.WithContext(ctx).Raw(`
		SELECT merchant_id,
		       domain_id,
		       chain_id,
		       token,
		       symbol,
		       decimals,
		       account,
		       SUM(CASE WHEN direction = 'credit' THEN amount_raw::numeric ELSE -amount_raw::numeric END)::text AS balance_raw
		FROM ledger_entries
		WHERE merchant_id = ?
		  AND domain_id = ?
		  AND status IN ('pending', 'posted')
		  AND amount_raw ~ '^[0-9]+$'
		GROUP BY merchant_id, domain_id, chain_id, token, symbol, decimals, account
		ORDER BY chain_id ASC, symbol ASC, account ASC
	`, merchantID, domainID).Scan(&rows).Error
	return rows, err
}

func (r *LedgerRepo) WalletBalances(ctx context.Context, merchantID, domainID, walletID uuid.UUID) ([]LedgerBalanceRow, error) {
	if rows, ok, err := r.balanceProjectionsOrEmpty(ctx, models.LedgerBalanceProjectionScopeWallet, ledgerBalanceProjectionScopeKey(models.LedgerBalanceProjectionScopeWallet, merchantID, &domainID, &walletID)); err != nil {
		return nil, err
	} else if ok {
		return rows, nil
	}
	var rows []LedgerBalanceRow
	err := r.db.WithContext(ctx).Raw(`
		SELECT merchant_id,
		       domain_id,
		       wallet_id,
		       chain_id,
		       token,
		       symbol,
		       decimals,
		       account,
		       SUM(CASE WHEN direction = 'credit' THEN amount_raw::numeric ELSE -amount_raw::numeric END)::text AS balance_raw
		FROM ledger_entries
		WHERE merchant_id = ?
		  AND domain_id = ?
		  AND wallet_id = ?
		  AND status IN ('pending', 'posted')
		  AND amount_raw ~ '^[0-9]+$'
		GROUP BY merchant_id, domain_id, wallet_id, chain_id, token, symbol, decimals, account
		ORDER BY chain_id ASC, symbol ASC, account ASC
	`, merchantID, domainID, walletID).Scan(&rows).Error
	return rows, err
}

type ledgerProjectionAggregateRow struct {
	ScopeType              string
	ScopeKey               string
	MerchantID             *uuid.UUID
	DomainID               *uuid.UUID
	WalletID               *uuid.UUID
	ChainID                constants.ChainID
	Token                  *string
	Symbol                 string
	Decimals               uint8
	Account                string
	BalanceRaw             string
	SourceLedgerEntryCount int64
}

func (r *LedgerRepo) RebuildBalanceProjections(ctx context.Context) (int64, error) {
	if r == nil || r.db == nil {
		return 0, gorm.ErrInvalidDB
	}
	var written int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		aggregates, err := r.ledgerProjectionAggregates(ctx, tx)
		if err != nil {
			return err
		}
		now := time.Now()
		if len(aggregates) == 0 {
			if err := tx.Where("projected_at < ?", now).Delete(&models.LedgerBalanceProjection{}).Error; err != nil {
				return err
			}
			return nil
		}
		projections := make([]models.LedgerBalanceProjection, 0, len(aggregates))
		for _, row := range aggregates {
			projections = append(projections, models.LedgerBalanceProjection{
				ID:                     uuid.New(),
				ScopeType:              row.ScopeType,
				ScopeKey:               row.ScopeKey,
				MerchantID:             row.MerchantID,
				DomainID:               row.DomainID,
				WalletID:               row.WalletID,
				ChainID:                row.ChainID,
				Token:                  row.Token,
				TokenFingerprint:       ledgerBalanceProjectionTokenFingerprint(row.Token),
				Symbol:                 row.Symbol,
				Decimals:               row.Decimals,
				Account:                row.Account,
				BalanceRaw:             row.BalanceRaw,
				SourceLedgerEntryCount: row.SourceLedgerEntryCount,
				ProjectedAt:            now,
				CreatedAt:              now,
				UpdatedAt:              now,
			})
		}
		if err := tx.WithContext(ctx).Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "scope_type"},
				{Name: "scope_key"},
				{Name: "chain_id"},
				{Name: "token_fingerprint"},
				{Name: "symbol"},
				{Name: "decimals"},
				{Name: "account"},
			},
			DoUpdates: clause.AssignmentColumns([]string{
				"merchant_id",
				"domain_id",
				"wallet_id",
				"token",
				"balance_raw",
				"source_ledger_entry_count",
				"projected_at",
				"updated_at",
			}),
		}).CreateInBatches(&projections, 500).Error; err != nil {
			return err
		}
		if err := tx.Where("projected_at < ?", now).Delete(&models.LedgerBalanceProjection{}).Error; err != nil {
			return err
		}
		written = int64(len(projections))
		return nil
	})
	return written, err
}

func (r *LedgerRepo) ledgerProjectionAggregates(ctx context.Context, tx *gorm.DB) ([]ledgerProjectionAggregateRow, error) {
	var rows []ledgerProjectionAggregateRow
	err := tx.WithContext(ctx).Raw(`
			WITH active AS (
				SELECT merchant_id,
				       domain_id,
				       wallet_id,
				       chain_id,
				       CASE
				           WHEN token IS NULL OR btrim(token) = '' THEN NULL
				           ELSE lower(btrim(token))
				       END AS token,
				       symbol,
				       decimals,
				       account,
			       CASE WHEN direction = 'credit' THEN amount_raw::numeric ELSE -amount_raw::numeric END AS signed_amount
			FROM ledger_entries
			WHERE status IN ('pending', 'posted')
			  AND amount_raw ~ '^[0-9]+$'
		)
		SELECT 'merchant' AS scope_type,
		       'merchant:' || merchant_id::text AS scope_key,
		       merchant_id,
		       NULL::uuid AS domain_id,
		       NULL::uuid AS wallet_id,
		       chain_id,
		       token,
		       symbol,
		       decimals,
		       account,
		       SUM(signed_amount)::text AS balance_raw,
		       COUNT(*)::bigint AS source_ledger_entry_count
		FROM active
		GROUP BY merchant_id, chain_id, token, symbol, decimals, account
		UNION ALL
		SELECT 'domain' AS scope_type,
		       'domain:' || merchant_id::text || ':' || domain_id::text AS scope_key,
		       merchant_id,
		       domain_id,
		       NULL::uuid AS wallet_id,
		       chain_id,
		       token,
		       symbol,
		       decimals,
		       account,
		       SUM(signed_amount)::text AS balance_raw,
		       COUNT(*)::bigint AS source_ledger_entry_count
		FROM active
		WHERE domain_id IS NOT NULL
		GROUP BY merchant_id, domain_id, chain_id, token, symbol, decimals, account
		UNION ALL
		SELECT 'wallet' AS scope_type,
		       'wallet:' || merchant_id::text || ':' || domain_id::text || ':' || wallet_id::text AS scope_key,
		       merchant_id,
		       domain_id,
		       wallet_id,
		       chain_id,
		       token,
		       symbol,
		       decimals,
		       account,
		       SUM(signed_amount)::text AS balance_raw,
		       COUNT(*)::bigint AS source_ledger_entry_count
		FROM active
		WHERE domain_id IS NOT NULL
		  AND wallet_id IS NOT NULL
		GROUP BY merchant_id, domain_id, wallet_id, chain_id, token, symbol, decimals, account
		UNION ALL
		SELECT 'platform' AS scope_type,
		       'platform' AS scope_key,
		       NULL::uuid AS merchant_id,
		       NULL::uuid AS domain_id,
		       NULL::uuid AS wallet_id,
		       chain_id,
		       token,
		       symbol,
		       decimals,
		       account,
		       SUM(signed_amount)::text AS balance_raw,
		       COUNT(*)::bigint AS source_ledger_entry_count
		FROM active
		WHERE account IN ('merchant_pending', 'merchant_available', 'withdrawal_transit', 'refund_transit', 'sweep_transit')
		GROUP BY chain_id, token, symbol, decimals, account
		HAVING SUM(signed_amount) <> 0
		ORDER BY scope_type ASC, scope_key ASC, chain_id ASC, symbol ASC, account ASC
	`).Scan(&rows).Error
	return rows, err
}

func (r *LedgerRepo) BalanceProjections(ctx context.Context, scopeType string, scopeKey string) ([]LedgerBalanceRow, error) {
	if r == nil || r.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	scopeType = strings.TrimSpace(scopeType)
	scopeKey = strings.TrimSpace(scopeKey)
	if scopeType == "" || scopeKey == "" {
		return []LedgerBalanceRow{}, nil
	}
	var projections []models.LedgerBalanceProjection
	if err := r.db.WithContext(ctx).
		Where("scope_type = ? AND scope_key = ?", scopeType, scopeKey).
		Order("chain_id ASC, symbol ASC, account ASC").
		Find(&projections).Error; err != nil {
		return nil, err
	}
	rows := make([]LedgerBalanceRow, 0, len(projections))
	for _, projection := range projections {
		row := LedgerBalanceRow{
			DomainID:   projection.DomainID,
			WalletID:   projection.WalletID,
			ChainID:    int64(projection.ChainID),
			Token:      projection.Token,
			Symbol:     projection.Symbol,
			Decimals:   projection.Decimals,
			Account:    projection.Account,
			BalanceRaw: projection.BalanceRaw,
		}
		if projection.MerchantID != nil {
			row.MerchantID = *projection.MerchantID
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (r *LedgerRepo) balanceProjectionsOrEmpty(ctx context.Context, scopeType string, scopeKey string) ([]LedgerBalanceRow, bool, error) {
	if scopeKey == "" {
		return nil, false, nil
	}
	rows, err := r.BalanceProjections(ctx, scopeType, scopeKey)
	if err != nil {
		if isMissingLedgerBalanceProjectionTableError(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if len(rows) == 0 {
		return nil, false, nil
	}
	return rows, true, nil
}

func (r *LedgerRepo) walletBalanceProjectionsByWalletIDs(ctx context.Context, walletIDs []uuid.UUID) ([]LedgerBalanceRow, bool, error) {
	var projections []models.LedgerBalanceProjection
	if err := r.db.WithContext(ctx).
		Where("scope_type = ? AND wallet_id IN ?", models.LedgerBalanceProjectionScopeWallet, walletIDs).
		Order("wallet_id ASC, chain_id ASC, symbol ASC, account ASC").
		Find(&projections).Error; err != nil {
		if isMissingLedgerBalanceProjectionTableError(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if len(projections) == 0 {
		return nil, false, nil
	}
	rows := make([]LedgerBalanceRow, 0, len(projections))
	for _, projection := range projections {
		row := LedgerBalanceRow{
			DomainID:   projection.DomainID,
			WalletID:   projection.WalletID,
			ChainID:    int64(projection.ChainID),
			Token:      projection.Token,
			Symbol:     projection.Symbol,
			Decimals:   projection.Decimals,
			Account:    projection.Account,
			BalanceRaw: projection.BalanceRaw,
		}
		if projection.MerchantID != nil {
			row.MerchantID = *projection.MerchantID
		}
		rows = append(rows, row)
	}
	return rows, true, nil
}

func isMissingLedgerBalanceProjectionTableError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "ledger_balance_projections") &&
		(strings.Contains(msg, "does not exist") || strings.Contains(msg, "no such table"))
}

func ledgerBalanceProjectionScopeKey(scopeType string, merchantID uuid.UUID, domainID *uuid.UUID, walletID *uuid.UUID) string {
	switch strings.TrimSpace(scopeType) {
	case models.LedgerBalanceProjectionScopeMerchant:
		return "merchant:" + merchantID.String()
	case models.LedgerBalanceProjectionScopeDomain:
		if domainID == nil {
			return ""
		}
		return "domain:" + merchantID.String() + ":" + domainID.String()
	case models.LedgerBalanceProjectionScopeWallet:
		if domainID == nil || walletID == nil {
			return ""
		}
		return "wallet:" + merchantID.String() + ":" + domainID.String() + ":" + walletID.String()
	case models.LedgerBalanceProjectionScopePlatform:
		return "platform"
	default:
		return ""
	}
}

func ledgerBalanceProjectionTokenFingerprint(token *string) string {
	if token == nil || strings.TrimSpace(*token) == "" {
		return "native"
	}
	return strings.ToLower(strings.TrimSpace(*token))
}

func (r *LedgerRepo) OpenInvariantReconciliationJobs(ctx context.Context, reconciliation *ReconciliationRepo, limit int) (int, error) {
	if r == nil || r.db == nil || reconciliation == nil {
		return 0, gorm.ErrInvalidDB
	}
	issues, err := r.FindInvariantIssues(ctx, limit)
	if err != nil {
		return 0, err
	}
	createdCount := 0
	for _, issue := range issues {
		merchantID := issue.MerchantID
		scope := ReconciliationScope{
			ChainID:      constants.ChainID(issue.ChainID),
			Reason:       "ledger_invariant",
			MerchantID:   &merchantID,
			DomainID:     issue.DomainID,
			ScopeKey:     ledgerInvariantScopeKey(issue),
			ResourceType: "ledger_invariant",
			ResourceID:   issue.IdempotencyKey,
			AffectedResourceIDs: []string{
				issue.IdempotencyKey,
			},
			Evidence: map[string]any{
				"idempotency_key": issue.IdempotencyKey,
				"merchant_id":     issue.MerchantID.String(),
				"domain_id":       uuidPtrString(issue.DomainID),
				"chain_id":        issue.ChainID,
				"token":           stringPtrValue(issue.Token),
				"symbol":          issue.Symbol,
				"net_raw":         issue.NetRaw,
			},
		}
		_, created, err := reconciliation.CreateScopedOpenIfMissing(ctx, scope)
		if err != nil {
			return createdCount, err
		}
		if created {
			createdCount++
		}
	}
	return createdCount, nil
}

func ledgerInvariantScopeKey(issue LedgerInvariantIssue) string {
	return fmt.Sprintf(
		"ledger_invariant:%s:%s:%s:%d:%s:%s",
		issue.IdempotencyKey,
		issue.MerchantID.String(),
		uuidPtrString(issue.DomainID),
		issue.ChainID,
		stringPtrValue(issue.Token),
		issue.Symbol,
	)
}

func uuidPtrString(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

func stringPtrValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
