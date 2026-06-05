package repositories

import (
	"context"
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
		updates["status"] = models.WebhookDeliveryStatusFailed
		if lastErr != nil {
			updates["last_error"] = lastErr.Error()
		}
		next := time.Now().Add(time.Minute)
		updates["next_retry_at"] = &next
	}
	return r.db.WithContext(ctx).
		Model(&models.WebhookDelivery{}).
		Where("id = ?", id).
		Updates(updates).Error
}
