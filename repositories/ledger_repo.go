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
	return r.db.WithContext(ctx).Create(&entries).Error
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
	return r.db.WithContext(ctx).Create(&entries).Error
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
		return dbtx.WithContext(ctx).Create(&entries).Error
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
		return dbtx.WithContext(ctx).Create(&entries).Error
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
	return tx.WithContext(ctx).Create(&entries).Error
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
	if err := r.RequireWithdrawalHoldForRequestWithDB(ctx, tx, request); err != nil {
		return err
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
	return tx.WithContext(ctx).Create(&entries).Error
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
	now := time.Now()
	return tx.WithContext(ctx).
		Model(&models.LedgerEntry{}).
		Where("withdrawal_id = ? AND entry_type = ? AND status = ?", withdrawalID, models.LedgerEntryTypeWithdrawalHold, models.LedgerStatusPending).
		Updates(map[string]any{
			"status":     models.LedgerStatusVoided,
			"voided_at":  &now,
			"updated_at": now,
		}).Error
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

func reverseLedgerDirection(direction string) string {
	if direction == models.LedgerDirectionCredit {
		return models.LedgerDirectionDebit
	}
	return models.LedgerDirectionCredit
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
			Reference:             txModel.UniqueHash,
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

func withdrawalDebitKey(id uuid.UUID) string {
	return "withdrawal-debit:" + id.String()
}

func refundHoldKey(id uuid.UUID) string {
	return "refund-hold:" + id.String()
}

func refundDebitKey(id uuid.UUID) string {
	return "refund-debit:" + id.String()
}

func sweepHoldKey(id uuid.UUID) string {
	return "sweep-hold:" + id.String()
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
	return tx.WithContext(ctx).Create(&entries).Error
}

func (r *LedgerRepo) AlignRefundHoldWalletWithDB(ctx context.Context, tx *gorm.DB, refundID uuid.UUID, walletID uuid.UUID) error {
	if refundID == uuid.Nil || walletID == uuid.Nil {
		return ErrLedgerReservationRequired
	}
	return tx.WithContext(ctx).
		Model(&models.LedgerEntry{}).
		Where("refund_id = ? AND entry_type = ? AND status = ?", refundID, models.LedgerEntryTypeRefundHold, models.LedgerStatusPending).
		Update("wallet_id", &walletID).Error
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
	now := time.Now()
	return tx.WithContext(ctx).
		Model(&models.LedgerEntry{}).
		Where("refund_id = ? AND entry_type = ? AND status = ?", refundID, models.LedgerEntryTypeRefundHold, models.LedgerStatusPending).
		Updates(map[string]any{
			"status":     models.LedgerStatusVoided,
			"voided_at":  &now,
			"updated_at": now,
		}).Error
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
	return tx.WithContext(ctx).Create(&entries).Error
}

func (r *LedgerRepo) VoidSweepHold(ctx context.Context, sweepJobID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return r.VoidSweepHoldWithDB(ctx, tx, sweepJobID)
	})
}

func (r *LedgerRepo) VoidSweepHoldWithDB(ctx context.Context, tx *gorm.DB, sweepJobID uuid.UUID) error {
	now := time.Now()
	return tx.WithContext(ctx).
		Model(&models.LedgerEntry{}).
		Where("sweep_job_id = ? AND entry_type = ? AND status = ?", sweepJobID, models.LedgerEntryTypeSweepHold, models.LedgerStatusPending).
		Updates(map[string]any{
			"status":     models.LedgerStatusVoided,
			"voided_at":  &now,
			"updated_at": now,
		}).Error
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
	if err := r.RequireSweepHoldForJobTransactionWithDB(ctx, tx, job, txModel); err != nil {
		return err
	}
	chainID := txModel.ChainID
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
	return tx.WithContext(ctx).Create(&entries).Error
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
	if err := r.RequireRefundHoldForRefundWithDB(ctx, tx, refund, session); err != nil {
		return err
	}
	now := time.Now()
	refundID := refund.ID
	paymentID := session.ID
	walletID := refundLedgerWalletID(refund, session)
	chainID, symbol, decimals, err := ledgerRefundAssetFromSession(session)
	if err != nil {
		return err
	}
	if err := r.lockLedgerAsset(ctx, tx, refund.MerchantID, &refund.DomainID, chainID, session.SelectedToken); err != nil {
		return err
	}
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
	return tx.WithContext(ctx).Create(&entries).Error
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
