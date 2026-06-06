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

func (r *WithdrawalRequestRepo) ListProcessingWithTxHash(ctx context.Context, limit int) ([]models.WithdrawalRequest, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var requests []models.WithdrawalRequest
	err := r.db.WithContext(ctx).
		Preload("Merchant").Preload("Wallet").
		Where("status = ?", models.WithdrawalStatusProcessing).
		Where("tx_hash <> ''").
		Order("updated_at ASC").
		Limit(limit).
		Find(&requests).Error
	return requests, err
}

func (r *WithdrawalRequestRepo) Find(ctx context.Context, id uuid.UUID) (*models.WithdrawalRequest, error) {
	var request models.WithdrawalRequest
	err := r.db.WithContext(ctx).Preload("Merchant").Preload("Wallet").First(&request, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &request, nil
}

func (r *WithdrawalRequestRepo) RecordBroadcast(ctx context.Context, id uuid.UUID, reviewedBy string, txHash string) error {
	now := time.Now()
	result := r.db.WithContext(ctx).Model(&models.WithdrawalRequest{}).
		Where("id = ? AND status = ?", id, models.WithdrawalStatusProcessing).
		Updates(map[string]any{
			"reviewed_by": reviewedBy,
			"reviewed_at": &now,
			"tx_hash":     txHash,
			"error":       "finalizing ledger",
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *WithdrawalRequestRepo) FinalizeProcessingWithLedger(ctx context.Context, id uuid.UUID, ledger *LedgerRepo) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var request models.WithdrawalRequest
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			First(&request, "id = ? AND status = ? AND tx_hash <> ''", id, models.WithdrawalStatusProcessing).Error; err != nil {
			return err
		}
		if ledger != nil {
			if err := ledger.PostWithdrawalDebitWithDB(ctx, tx, request, request.TxHash); err != nil {
				return err
			}
		}
		now := time.Now()
		result := tx.Model(&models.WithdrawalRequest{}).
			Where("id = ? AND status = ?", id, models.WithdrawalStatusProcessing).
			Updates(map[string]any{
				"status":      models.WithdrawalStatusApproved,
				"reviewed_at": &now,
				"error":       "",
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func (r *WithdrawalRequestRepo) MarkApproved(ctx context.Context, id uuid.UUID, reviewedBy string, txHash string) error {
	now := time.Now()
	result := r.db.WithContext(ctx).
		Model(&models.WithdrawalRequest{}).
		Where("id = ? AND status IN ?", id, []string{models.WithdrawalStatusPending, models.WithdrawalStatusProcessing}).
		Updates(map[string]any{
			"status":      models.WithdrawalStatusApproved,
			"reviewed_by": reviewedBy,
			"reviewed_at": &now,
			"tx_hash":     txHash,
			"error":       "",
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *WithdrawalRequestRepo) MarkRejected(ctx context.Context, id uuid.UUID, reviewedBy string, reason string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.WithdrawalRequest{}).
			Where("id = ? AND status = ?", id, models.WithdrawalStatusPending).
			Updates(map[string]any{
				"status":      models.WithdrawalStatusRejected,
				"reviewed_by": reviewedBy,
				"reviewed_at": &now,
				"error":       reason,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return NewLedgerRepo(tx).VoidWithdrawalHoldWithDB(ctx, tx, id)
	})
}

func (r *WithdrawalRequestRepo) MarkFailed(ctx context.Context, id uuid.UUID, reviewedBy string, errText string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.WithdrawalRequest{}).
			Where("id = ? AND status IN ?", id, []string{models.WithdrawalStatusPending, models.WithdrawalStatusProcessing}).
			Updates(map[string]any{
				"status":      models.WithdrawalStatusFailed,
				"reviewed_by": reviewedBy,
				"reviewed_at": &now,
				"error":       errText,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
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

	var request models.WithdrawalRequest
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Preload("Merchant").
			Preload("Wallet").
			First(&request, "id = ? AND status = ?", id, models.WithdrawalStatusPending).Error; err != nil {
			return err
		}

		now := time.Now()
		result := tx.Model(&models.WithdrawalRequest{}).
			Where("id = ? AND status = ?", id, models.WithdrawalStatusPending).
			Updates(map[string]any{
				"status":      models.WithdrawalStatusProcessing,
				"reviewed_by": reviewedBy,
				"reviewed_at": &now,
				"error":       "",
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		request.Status = models.WithdrawalStatusProcessing
		request.ReviewedBy = reviewedBy
		request.ReviewedAt = &now
		request.Error = ""
		return nil
	})
	if err != nil {
		return nil, err
	}

	txHash, transferErr := transfer(&request)
	if transferErr != nil {
		_ = r.MarkFailed(ctx, id, reviewedBy, transferErr.Error())
		request.Status = models.WithdrawalStatusFailed
		request.Error = transferErr.Error()
		return &request, transferErr
	}
	if err := r.RecordBroadcast(ctx, id, reviewedBy, txHash); err != nil {
		request.TxHash = txHash
		request.Error = "broadcast sent but tx hash persist failed: " + err.Error()
		return &request, err
	}
	request.TxHash = txHash

	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		result := tx.Model(&models.WithdrawalRequest{}).
			Where("id = ? AND status = ?", id, models.WithdrawalStatusProcessing).
			Updates(map[string]any{
				"status":      models.WithdrawalStatusApproved,
				"reviewed_by": reviewedBy,
				"reviewed_at": &now,
				"tx_hash":     txHash,
				"error":       "",
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		request.Status = models.WithdrawalStatusApproved
		request.ReviewedBy = reviewedBy
		request.ReviewedAt = &now
		request.TxHash = txHash
		request.Error = ""
		if ledger != nil {
			return ledger.PostWithdrawalDebitWithDB(ctx, tx, request, txHash)
		}
		return nil
	})
	if err != nil {
		msg := "ledger/finalize failed: " + err.Error()
		_ = r.db.WithContext(ctx).Model(&models.WithdrawalRequest{}).
			Where("id = ? AND status = ?", id, models.WithdrawalStatusProcessing).
			Update("error", msg).Error
		request.Status = models.WithdrawalStatusProcessing
		request.Error = msg
		return &request, err
	}
	return &request, nil
}
