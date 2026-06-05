package repositories

import (
	"context"
	"os"
	"strconv"
	"time"

	"core/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type WebhookDeliveryRepo struct {
	db *gorm.DB
}

func NewWebhookDeliveryRepo(db *gorm.DB) *WebhookDeliveryRepo {
	return &WebhookDeliveryRepo{db: db}
}

func (r *WebhookDeliveryRepo) Create(ctx context.Context, delivery *models.WebhookDelivery) error {
	if delivery.ID == uuid.Nil {
		delivery.ID = uuid.New()
	}
	if delivery.Status == "" {
		delivery.Status = models.WebhookDeliveryStatusPending
	}
	now := time.Now()
	if delivery.CreatedAt.IsZero() {
		delivery.CreatedAt = now
	}
	delivery.UpdatedAt = now
	return r.db.WithContext(ctx).Create(delivery).Error
}

func (r *WebhookDeliveryRepo) MarkAttempt(ctx context.Context, id uuid.UUID, delivered bool, lastErr error) error {
	var current models.WebhookDelivery
	if err := r.db.WithContext(ctx).First(&current, "id = ?", id).Error; err != nil {
		return err
	}

	updates := map[string]any{
		"attempts":   gorm.Expr("attempts + 1"),
		"updated_at": time.Now(),
	}
	if delivered {
		now := time.Now()
		updates["status"] = models.WebhookDeliveryStatusSucceeded
		updates["delivered_at"] = &now
		updates["last_error"] = ""
		updates["next_retry_at"] = nil
	} else {
		status := models.WebhookDeliveryStatusFailed
		if current.Attempts+1 >= webhookMaxAttempts() {
			status = models.WebhookDeliveryStatusDeadLetter
		}
		updates["status"] = status
		if lastErr != nil {
			updates["last_error"] = lastErr.Error()
		}
		if status == models.WebhookDeliveryStatusDeadLetter {
			updates["next_retry_at"] = nil
		} else {
			next := time.Now().Add(time.Minute)
			updates["next_retry_at"] = &next
		}
	}
	return r.db.WithContext(ctx).
		Model(&models.WebhookDelivery{}).
		Where("id = ?", id).
		Updates(updates).Error
}

func webhookMaxAttempts() uint {
	raw := os.Getenv("WEBHOOK_MAX_ATTEMPTS")
	if raw == "" {
		return 8
	}
	value, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || value == 0 {
		return 8
	}
	return uint(value)
}

func (r *WebhookDeliveryRepo) ListPage(ctx context.Context, page, limit int, status string) ([]models.WebhookDelivery, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	q := r.db.WithContext(ctx).Model(&models.WebhookDelivery{})
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []models.WebhookDelivery
	err := q.Order("updated_at DESC").
		Limit(limit).Offset((page - 1) * limit).
		Find(&rows).Error
	return rows, total, err
}

func (r *WebhookDeliveryRepo) Find(ctx context.Context, id uuid.UUID) (*models.WebhookDelivery, error) {
	var row models.WebhookDelivery
	if err := r.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &row, nil
}
