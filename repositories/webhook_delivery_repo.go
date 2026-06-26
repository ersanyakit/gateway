package repositories

import (
	"context"
	"errors"
	"time"

	"core/models"
	webhooksvc "core/services/webhook"

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
	return r.createWithDB(ctx, r.db, delivery)
}

func (r *WebhookDeliveryRepo) createWithDB(ctx context.Context, tx *gorm.DB, delivery *models.WebhookDelivery) error {
	if delivery.ID == uuid.Nil {
		delivery.ID = uuid.New()
	}
	if delivery.EventVersion == "" {
		delivery.EventVersion = "v1"
	}
	if delivery.Status == "" {
		delivery.Status = models.WebhookDeliveryStatusPending
	}
	now := time.Now()
	if delivery.CreatedAt.IsZero() {
		delivery.CreatedAt = now
	}
	delivery.UpdatedAt = now
	return tx.WithContext(ctx).Create(delivery).Error
}

func (r *WebhookDeliveryRepo) enqueueByEventID(ctx context.Context, eventID string, build func() *models.WebhookDelivery) (*models.WebhookDelivery, bool, error) {
	if eventID == "" || build == nil {
		return nil, false, gorm.ErrInvalidData
	}
	var delivery *models.WebhookDelivery
	created := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", "webhook-delivery:"+eventID).Error; err != nil {
			return err
		}
		var existing models.WebhookDelivery
		err := tx.WithContext(ctx).First(&existing, "event_id = ?", eventID).Error
		if err == nil {
			delivery = &existing
			return nil
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		next := build()
		if next == nil {
			return gorm.ErrInvalidData
		}
		next.EventID = eventID
		if err := r.createWithDB(ctx, tx, next); err != nil {
			return err
		}
		delivery = next
		created = true
		return nil
	})
	return delivery, created, err
}

func (r *WebhookDeliveryRepo) EnqueueTransaction(ctx context.Context, domain models.Domain, txModel models.Transaction) (*models.WebhookDelivery, bool, error) {
	if txModel.MerchantID == nil || txModel.DomainID == nil || txModel.ID == uuid.Nil || txModel.UniqueHash == "" || txModel.EventType == "" {
		return nil, false, gorm.ErrInvalidData
	}
	eventID := webhooksvc.TransactionEventID(txModel)
	if eventID == "" {
		return nil, false, gorm.ErrInvalidData
	}
	return r.enqueueByEventID(ctx, eventID, func() *models.WebhookDelivery {
		return &models.WebhookDelivery{
			MerchantID:    *txModel.MerchantID,
			DomainID:      *txModel.DomainID,
			TransactionID: &txModel.ID,
			EventType:     txModel.EventType,
			EventVersion:  "v1",
			EntityType:    "transaction",
			EntityID:      &txModel.ID,
			TargetURL:     domain.WebhookURL,
			Status:        models.WebhookDeliveryStatusPending,
		}
	})
}

func (r *WebhookDeliveryRepo) EnqueuePayment(ctx context.Context, domain models.Domain, session models.PaymentSession) (*models.WebhookDelivery, bool, error) {
	if session.ID == uuid.Nil || session.WebhookEvent == "" {
		return nil, false, gorm.ErrInvalidData
	}
	eventID := webhooksvc.PaymentEventID(session)
	if eventID == "" {
		return nil, false, gorm.ErrInvalidData
	}
	return r.enqueueByEventID(ctx, eventID, func() *models.WebhookDelivery {
		return &models.WebhookDelivery{
			MerchantID:   session.MerchantID,
			DomainID:     session.DomainID,
			PaymentID:    &session.ID,
			EventType:    session.WebhookEvent,
			EventVersion: "v1",
			EntityType:   "payment",
			EntityID:     &session.ID,
			TargetURL:    domain.WebhookURL,
			Status:       models.WebhookDeliveryStatusPending,
		}
	})
}

func (r *WebhookDeliveryRepo) EnqueueLifecycle(ctx context.Context, domain models.Domain, payload webhooksvc.LifecyclePayload) (*models.WebhookDelivery, bool, error) {
	if payload.EventID == "" || payload.EventType == "" {
		return nil, false, gorm.ErrInvalidData
	}
	body, err := payload.Body()
	if err != nil {
		return nil, false, err
	}
	return r.enqueueByEventID(ctx, payload.EventID, func() *models.WebhookDelivery {
		return &models.WebhookDelivery{
			MerchantID:   domain.MerchantID,
			DomainID:     domain.ID,
			EventType:    payload.EventType,
			EventVersion: payload.EventVersion,
			EntityType:   payload.EntityType,
			EntityID:     payload.EntityUUID(),
			PayloadJSON:  string(body),
			TargetURL:    domain.WebhookURL,
			Status:       models.WebhookDeliveryStatusPending,
		}
	})
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
		if isPermanentDeliveryError(lastErr) || current.Attempts+1 >= webhookMaxAttempts() {
			status = models.WebhookDeliveryStatusDeadLetter
		}
		updates["status"] = status
		if lastErr != nil {
			updates["last_error"] = lastErr.Error()
		}
		if status == models.WebhookDeliveryStatusDeadLetter {
			updates["next_retry_at"] = nil
		} else {
			next := time.Now().Add(webhookRetryBackoff(current.Attempts + 1))
			updates["next_retry_at"] = &next
		}
	}
	return r.db.WithContext(ctx).
		Model(&models.WebhookDelivery{}).
		Where("id = ?", id).
		Updates(updates).Error
}

func webhookMaxAttempts() uint {
	return uintFromEnv("WEBHOOK_MAX_ATTEMPTS", 8)
}

func (r *WebhookDeliveryRepo) ListDueLifecycle(ctx context.Context, limit int) ([]models.WebhookDelivery, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	now := time.Now()
	var rows []models.WebhookDelivery
	err := r.db.WithContext(ctx).
		Where("payment_id IS NULL").
		Where("transaction_id IS NULL").
		Where("payload_json <> ''").
		Where("status IN ?", []string{models.WebhookDeliveryStatusPending, models.WebhookDeliveryStatusFailed}).
		Where("next_retry_at IS NULL OR next_retry_at <= ?", now).
		Order("updated_at ASC").
		Limit(limit).
		Find(&rows).Error
	return rows, err
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
