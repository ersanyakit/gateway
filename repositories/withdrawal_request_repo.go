package repositories

import (
	"context"
	"core/models"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type WithdrawalRequestRepo struct {
	db *gorm.DB
}

func NewWithdrawalRequestRepo(db *gorm.DB) *WithdrawalRequestRepo {
	return &WithdrawalRequestRepo{db: db}
}

func (r *WithdrawalRequestRepo) DB() *gorm.DB { return r.db }

func (r *WithdrawalRequestRepo) Create(ctx context.Context, request *models.WithdrawalRequest) error {
	return r.db.WithContext(ctx).Create(request).Error
}

func (r *WithdrawalRequestRepo) CreateWithHold(ctx context.Context, request *models.WithdrawalRequest, ledger *LedgerRepo) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(request).Error; err != nil {
			return err
		}
		if ledger == nil {
			return nil
		}
		return ledger.CreateWithdrawalHoldWithDB(ctx, tx, *request)
	})
}

func (r *WithdrawalRequestRepo) ListByMerchant(ctx context.Context, merchantID uuid.UUID, limit int) ([]models.WithdrawalRequest, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var requests []models.WithdrawalRequest
	err := r.db.WithContext(ctx).
		Preload("Wallet").
		Where("merchant_id = ?", merchantID).
		Order("created_at DESC").
		Limit(limit).
		Find(&requests).Error
	return requests, err
}

func (r *WithdrawalRequestRepo) ListByMerchantPage(ctx context.Context, merchantID uuid.UUID, page, limit int) ([]models.WithdrawalRequest, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 20
	}
	var total int64
	if err := r.db.WithContext(ctx).Model(&models.WithdrawalRequest{}).Where("merchant_id = ?", merchantID).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var requests []models.WithdrawalRequest
	err := r.db.WithContext(ctx).
		Where("merchant_id = ?", merchantID).
		Order("created_at DESC").
		Limit(limit).Offset((page - 1) * limit).
		Find(&requests).Error
	return requests, total, err
}

func (r *WithdrawalRequestRepo) List(ctx context.Context, status string, limit int) ([]models.WithdrawalRequest, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	query := r.db.WithContext(ctx).Preload("Merchant").Preload("Wallet").Order("created_at DESC").Limit(limit)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var requests []models.WithdrawalRequest
	err := query.Find(&requests).Error
	return requests, err
}

func (r *WithdrawalRequestRepo) ListPage(ctx context.Context, page, limit int) ([]models.WithdrawalRequest, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	var total int64
	if err := r.db.WithContext(ctx).Model(&models.WithdrawalRequest{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var requests []models.WithdrawalRequest
	err := r.db.WithContext(ctx).
		Preload("Merchant").Preload("Wallet").
		Order("created_at DESC").
		Limit(limit).Offset((page - 1) * limit).
		Find(&requests).Error
	return requests, total, err
}

func (r *WithdrawalRequestRepo) Find(ctx context.Context, id uuid.UUID) (*models.WithdrawalRequest, error) {
	var request models.WithdrawalRequest
	err := r.db.WithContext(ctx).Preload("Merchant").Preload("Wallet").First(&request, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &request, nil
}

func (r *WithdrawalRequestRepo) MarkApproved(ctx context.Context, id uuid.UUID, reviewedBy string, txHash string) error {
	now := time.Now()
	return r.db.WithContext(ctx).
		Model(&models.WithdrawalRequest{}).
		Where("id = ? AND status = ?", id, models.WithdrawalStatusPending).
		Updates(map[string]any{
			"status":      models.WithdrawalStatusApproved,
			"reviewed_by": reviewedBy,
			"reviewed_at": &now,
			"tx_hash":     txHash,
			"error":       "",
		}).Error
}

func (r *WithdrawalRequestRepo) MarkRejected(ctx context.Context, id uuid.UUID, reviewedBy string, reason string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.WithdrawalRequest{}).
			Where("id = ? AND status = ?", id, models.WithdrawalStatusPending).
			Updates(map[string]any{
				"status":      models.WithdrawalStatusRejected,
				"reviewed_by": reviewedBy,
				"reviewed_at": &now,
				"error":       reason,
			}).Error; err != nil {
			return err
		}
		return NewLedgerRepo(tx).VoidWithdrawalHoldWithDB(ctx, tx, id)
	})
}

func (r *WithdrawalRequestRepo) MarkFailed(ctx context.Context, id uuid.UUID, reviewedBy string, errText string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.WithdrawalRequest{}).
			Where("id = ? AND status <> ?", id, models.WithdrawalStatusApproved).
			Updates(map[string]any{
				"status":      models.WithdrawalStatusFailed,
				"reviewed_by": reviewedBy,
				"reviewed_at": &now,
				"error":       errText,
			}).Error; err != nil {
			return err
		}
		return NewLedgerRepo(tx).VoidWithdrawalHoldWithDB(ctx, tx, id)
	})
}

func (r *WithdrawalRequestRepo) LockPending(ctx context.Context, id uuid.UUID) (*models.WithdrawalRequest, error) {
	var request models.WithdrawalRequest
	err := r.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Preload("Merchant").
		Preload("Wallet").
		First(&request, "id = ? AND status = ?", id, models.WithdrawalStatusPending).Error
	if err != nil {
		return nil, err
	}
	return &request, nil
}

func (r *WithdrawalRequestRepo) ApproveWithTransfer(ctx context.Context, id uuid.UUID, reviewedBy string, ledger *LedgerRepo, transfer func(*models.WithdrawalRequest) (string, error)) (*models.WithdrawalRequest, error) {
	if transfer == nil {
		return nil, errors.New("transfer callback is required")
	}

	var approved models.WithdrawalRequest
	var transferErr error
	var ledgerErr error
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var request models.WithdrawalRequest
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Preload("Merchant").
			Preload("Wallet").
			First(&request, "id = ? AND status = ?", id, models.WithdrawalStatusPending).Error; err != nil {
			return err
		}

		txHash, err := transfer(&request)
		if err != nil {
			now := time.Now()
			if markErr := tx.Model(&models.WithdrawalRequest{}).
				Where("id = ?", id).
				Updates(map[string]any{
					"status":      models.WithdrawalStatusFailed,
					"reviewed_by": reviewedBy,
					"reviewed_at": &now,
					"error":       err.Error(),
				}).Error; markErr != nil {
				return markErr
			}
			if ledger != nil {
				if voidErr := ledger.VoidWithdrawalHoldWithDB(ctx, tx, id); voidErr != nil {
					return voidErr
				}
			}
			transferErr = err
			return nil
		}

		now := time.Now()
		if err := tx.Model(&models.WithdrawalRequest{}).
			Where("id = ? AND status = ?", id, models.WithdrawalStatusPending).
			Updates(map[string]any{
				"status":      models.WithdrawalStatusApproved,
				"reviewed_by": reviewedBy,
				"reviewed_at": &now,
				"tx_hash":     txHash,
				"error":       "",
			}).Error; err != nil {
			return err
		}
		request.Status = models.WithdrawalStatusApproved
		request.ReviewedBy = reviewedBy
		request.ReviewedAt = &now
		request.TxHash = txHash
		request.Error = ""
		if ledger != nil {
			if err := ledger.PostWithdrawalDebitWithDB(ctx, tx, request, txHash); err != nil {
				ledgerErr = err
				if markErr := tx.Model(&models.WithdrawalRequest{}).
					Where("id = ?", id).
					Update("error", "ledger update failed: "+err.Error()).Error; markErr != nil {
					return markErr
				}
			}
		}
		approved = request
		return nil
	})
	if err != nil {
		return nil, err
	}
	if transferErr != nil {
		return nil, transferErr
	}
	if ledgerErr != nil {
		return &approved, ledgerErr
	}
	return &approved, nil
}
