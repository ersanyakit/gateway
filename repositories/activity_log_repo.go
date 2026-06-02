package repositories

import (
	"context"
	"core/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type ActivityLogRepo struct {
	db *gorm.DB
}

func NewActivityLogRepo(db *gorm.DB) *ActivityLogRepo {
	return &ActivityLogRepo{db: db}
}

func (r *ActivityLogRepo) Create(ctx context.Context, log *models.ActivityLog) error {
	if log == nil {
		return nil
	}
	return r.db.WithContext(ctx).Create(log).Error
}

func (r *ActivityLogRepo) ListByMerchant(ctx context.Context, merchantID uuid.UUID, limit int) ([]models.ActivityLog, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	var logs []models.ActivityLog
	err := r.db.WithContext(ctx).
		Where("merchant_id = ?", merchantID).
		Order("created_at DESC").
		Limit(limit).
		Find(&logs).Error
	return logs, err
}
