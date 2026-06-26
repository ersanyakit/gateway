package repositories

import (
	"context"
	"core/models"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type RefundRepo struct {
	db *gorm.DB
}

func NewRefundRepo(db *gorm.DB) *RefundRepo {
	return &RefundRepo{db: db}
}

func (r *RefundRepo) Create(ctx context.Context, refund *models.Refund) error {
	if refund.ID == uuid.Nil {
		refund.ID = uuid.New()
	}
	if refund.Status == "" {
		refund.Status = models.RefundStatusPending
	}
	return r.db.WithContext(ctx).Create(refund).Error
}

func (r *RefundRepo) CreateWithHold(ctx context.Context, refund *models.Refund, session models.PaymentSession, ledger *LedgerRepo) error {
	if refund.ID == uuid.Nil {
		refund.ID = uuid.New()
	}
	if refund.Status == "" {
		refund.Status = models.RefundStatusPending
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(refund).Error; err != nil {
			return err
		}
		if ledger == nil {
			return nil
		}
		return NewLedgerRepo(tx).CreateRefundHoldWithDB(ctx, tx, *refund, session)
	})
}

func (r *RefundRepo) ListByMerchant(ctx context.Context, merchantID uuid.UUID, limit int) ([]models.Refund, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var refunds []models.Refund
	err := r.db.WithContext(ctx).
		Where("merchant_id = ?", merchantID).
		Order("created_at DESC").
		Limit(limit).
		Find(&refunds).Error
	return refunds, err
}

func (r *RefundRepo) ListPage(ctx context.Context, page, limit int, status string) ([]models.Refund, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	q := r.db.WithContext(ctx).Model(&models.Refund{})
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var refunds []models.Refund
	err := q.Order("created_at DESC").Limit(limit).Offset((page - 1) * limit).Find(&refunds).Error
	return refunds, total, err
}

func (r *RefundRepo) ListByMerchantPage(ctx context.Context, merchantID uuid.UUID, page, limit int) ([]models.Refund, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	q := r.db.WithContext(ctx).Model(&models.Refund{}).Where("merchant_id = ?", merchantID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var refunds []models.Refund
	err := q.Order("created_at DESC").Limit(limit).Offset((page - 1) * limit).Find(&refunds).Error
	return refunds, total, err
}

func (r *RefundRepo) ListByDomainPage(ctx context.Context, merchantID, domainID uuid.UUID, page, limit int) ([]models.Refund, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	q := r.db.WithContext(ctx).Model(&models.Refund{}).Where("merchant_id = ? AND domain_id = ?", merchantID, domainID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var refunds []models.Refund
	err := q.Order("created_at DESC").Limit(limit).Offset((page - 1) * limit).Find(&refunds).Error
	return refunds, total, err
}

func (r *RefundRepo) Find(ctx context.Context, id uuid.UUID) (*models.Refund, error) {
	var refund models.Refund
	if err := r.db.WithContext(ctx).First(&refund, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &refund, nil
}

func (r *RefundRepo) FindByDomain(ctx context.Context, merchantID, domainID, id uuid.UUID) (*models.Refund, error) {
	var refund models.Refund
	if err := r.db.WithContext(ctx).
		Where("merchant_id = ? AND domain_id = ? AND id = ?", merchantID, domainID, id).
		First(&refund).Error; err != nil {
		return nil, err
	}
	return &refund, nil
}

func (r *RefundRepo) ActiveTotalRawByPayment(ctx context.Context, paymentID uuid.UUID) (*big.Int, error) {
	var raw string
	err := r.db.WithContext(ctx).
		Model(&models.Refund{}).
		Select("COALESCE(SUM(amount_raw::numeric), 0)::text").
		Where("payment_id = ? AND status IN ?", paymentID, []string{
			models.RefundStatusPending,
			models.RefundStatusProcessing,
			models.RefundStatusApproved,
			models.RefundStatusSucceeded,
		}).
		Where("amount_raw ~ '^[0-9]+$'").
		Scan(&raw).Error
	if err != nil {
		return nil, err
	}
	if raw == "" {
		raw = "0"
	}
	total, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		return nil, gorm.ErrInvalidData
	}
	return total, nil
}

func (r *RefundRepo) ListProcessingWithTxHash(ctx context.Context, limit int) ([]models.Refund, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var refunds []models.Refund
	err := r.db.WithContext(ctx).
		Where("status = ?", models.RefundStatusProcessing).
		Where("tx_hash <> ''").
		Order("updated_at ASC").
		Limit(limit).
		Find(&refunds).Error
	return refunds, err
}

func (r *RefundRepo) ClaimPending(ctx context.Context, id uuid.UUID, reviewedBy string) (*models.Refund, error) {
	var claimed models.Refund
	now := time.Now()
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var refund models.Refund
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&refund, "id = ? AND status = ?", id, models.RefundStatusPending).Error; err != nil {
			return err
		}
		result := tx.Model(&models.Refund{}).
			Where("id = ? AND status = ?", id, models.RefundStatusPending).
			Updates(map[string]any{
				"status":      models.RefundStatusProcessing,
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
		refund.Status = models.RefundStatusProcessing
		refund.ReviewedBy = reviewedBy
		refund.ReviewedAt = &now
		refund.Error = ""
		claimed = refund
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &claimed, nil
}

func (r *RefundRepo) ClaimPendingWithHold(ctx context.Context, id uuid.UUID, reviewedBy string, session models.PaymentSession, ledger *LedgerRepo) (*models.Refund, error) {
	var claimed models.Refund
	now := time.Now()
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var refund models.Refund
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&refund, "id = ? AND status = ?", id, models.RefundStatusPending).Error; err != nil {
			return err
		}
		if ledger != nil {
			if err := NewLedgerRepo(tx).CreateRefundHoldWithDB(ctx, tx, refund, session); err != nil {
				return err
			}
		}
		result := tx.Model(&models.Refund{}).
			Where("id = ? AND status = ?", id, models.RefundStatusPending).
			Updates(map[string]any{
				"status":      models.RefundStatusProcessing,
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
		refund.Status = models.RefundStatusProcessing
		refund.ReviewedBy = reviewedBy
		refund.ReviewedAt = &now
		refund.Error = ""
		claimed = refund
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &claimed, nil
}

func (r *RefundRepo) RecordBroadcast(ctx context.Context, id uuid.UUID, reviewedBy string, txHash string) error {
	now := time.Now()
	result := r.db.WithContext(ctx).Model(&models.Refund{}).
		Where("id = ? AND status = ?", id, models.RefundStatusProcessing).
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

func (r *RefundRepo) MarkSucceeded(ctx context.Context, id uuid.UUID, reviewedBy string, txHash string) error {
	now := time.Now()
	result := r.db.WithContext(ctx).Model(&models.Refund{}).
		Where("id = ? AND status IN ?", id, []string{models.RefundStatusProcessing, models.RefundStatusApproved}).
		Updates(map[string]any{
			"status":      models.RefundStatusSucceeded,
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

func (r *RefundRepo) MarkSucceededWithLedger(ctx context.Context, id uuid.UUID, reviewedBy string, txHash string, session models.PaymentSession, ledger *LedgerRepo) error {
	now := time.Now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var refund models.Refund
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			First(&refund, "id = ? AND status IN ?", id, []string{models.RefundStatusProcessing, models.RefundStatusApproved}).Error; err != nil {
			return err
		}
		if ledger != nil {
			if err := NewLedgerRepo(tx).PostRefundDebitWithDB(ctx, tx, refund, session, txHash); err != nil {
				return err
			}
		}
		result := tx.Model(&models.Refund{}).
			Where("id = ? AND status IN ?", id, []string{models.RefundStatusProcessing, models.RefundStatusApproved}).
			Updates(map[string]any{
				"status":      models.RefundStatusSucceeded,
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
	})
}

func (r *RefundRepo) MarkRejected(ctx context.Context, id uuid.UUID, reviewedBy string, reason string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.Refund{}).
			Where("id = ? AND status = ?", id, models.RefundStatusPending).
			Updates(map[string]any{
				"status":      models.RefundStatusRejected,
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
		return NewLedgerRepo(tx).VoidRefundHoldWithDB(ctx, tx, id)
	})
}

func (r *RefundRepo) MarkFailed(ctx context.Context, id uuid.UUID, reviewedBy string, errText string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var refund models.Refund
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&refund, "id = ? AND status IN ?", id, []string{models.RefundStatusPending, models.RefundStatusProcessing, models.RefundStatusApproved}).Error; err != nil {
			return err
		}
		result := tx.Model(&models.Refund{}).
			Where("id = ? AND status IN ?", id, []string{models.RefundStatusPending, models.RefundStatusProcessing, models.RefundStatusApproved}).
			Updates(map[string]any{
				"status":      models.RefundStatusFailed,
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
		if strings.TrimSpace(refund.TxHash) != "" {
			return nil
		}
		return NewLedgerRepo(tx).VoidRefundHoldWithDB(ctx, tx, id)
	})
}

func (r *RefundRepo) SetProcessingError(ctx context.Context, id uuid.UUID, errText string) error {
	result := r.db.WithContext(ctx).Model(&models.Refund{}).
		Where("id = ? AND status = ?", id, models.RefundStatusProcessing).
		Update("error", errText)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
