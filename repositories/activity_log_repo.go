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

func (r *ActivityLogRepo) List(ctx context.Context, limit int) ([]models.ActivityLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	var logs []models.ActivityLog
	err := r.db.WithContext(ctx).
		Order("created_at DESC").
		Limit(limit).
		Find(&logs).Error
	return logs, err
}

func (r *ActivityLogRepo) ListPage(ctx context.Context, page, limit int, merchantID *uuid.UUID) ([]models.ActivityLog, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	q := r.db.WithContext(ctx).Model(&models.ActivityLog{})
	if merchantID != nil {
		q = q.Where("merchant_id = ?", *merchantID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	q2 := r.db.WithContext(ctx).Model(&models.ActivityLog{})
	if merchantID != nil {
		q2 = q2.Where("merchant_id = ?", *merchantID)
	}
	var logs []models.ActivityLog
	err := q2.Order("created_at DESC").
		Limit(limit).Offset((page - 1) * limit).
		Find(&logs).Error
	return logs, total, err
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
