package repositories

import (
	"context"
	"core/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
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
