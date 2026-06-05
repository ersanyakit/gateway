package repositories

import (
	"context"
	"core/models"
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
	return r.db.WithContext(ctx).
		Model(&models.WithdrawalRequest{}).
		Where("id = ? AND status = ?", id, models.WithdrawalStatusPending).
		Updates(map[string]any{
			"status":      models.WithdrawalStatusRejected,
			"reviewed_by": reviewedBy,
			"reviewed_at": &now,
			"error":       reason,
		}).Error
}

func (r *WithdrawalRequestRepo) MarkFailed(ctx context.Context, id uuid.UUID, reviewedBy string, errText string) error {
	now := time.Now()
	return r.db.WithContext(ctx).
		Model(&models.WithdrawalRequest{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":      models.WithdrawalStatusFailed,
			"reviewed_by": reviewedBy,
			"reviewed_at": &now,
			"error":       errText,
		}).Error
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
