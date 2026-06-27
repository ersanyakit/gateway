package repositories

import (
	"context"
	"errors"
	"strings"
	"time"

	"core/models"
	webhooksvc "core/services/webhook"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type WebhookDeliveryRepo struct {
	db *gorm.DB
}

var ErrWebhookReplayScopeDenied = errors.New("webhook delivery not found")

type WebhookReplayParams struct {
	DeliveryID  uuid.UUID
	ActorEmail  string
	MerchantID  *uuid.UUID
	DomainID    *uuid.UUID
	RequestedAt time.Time
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
	if strings.TrimSpace(payload.EventID) == "" ||
		strings.TrimSpace(payload.EventType) == "" ||
		strings.TrimSpace(payload.EntityType) == "" ||
		strings.TrimSpace(payload.EntityID) == "" {
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
		updates["failure_category"] = ""
		updates["next_retry_at"] = nil
		updates["operator_action"] = ""
	} else {
		status, operatorAction := webhookDeliveryFailureState(current.Attempts, lastErr)
		updates["status"] = status
		if lastErr != nil {
			updates["last_error"] = webhooksvc.SanitizeDeliveryError(lastErr)
			updates["failure_category"] = webhooksvc.FailureCategory(lastErr)
		}
		if status == models.WebhookDeliveryStatusDeadLetter {
			updates["next_retry_at"] = nil
		} else {
			next := time.Now().Add(webhookRetryBackoff(current.Attempts + 1))
			updates["next_retry_at"] = &next
		}
		updates["operator_action"] = operatorAction
	}
	return r.db.WithContext(ctx).
		Model(&models.WebhookDelivery{}).
		Where("id = ?", id).
		Updates(updates).Error
}

func webhookDeliveryFailureState(currentAttempts uint, lastErr error) (string, string) {
	if isPermanentDeliveryError(lastErr) || currentAttempts+1 >= webhookMaxAttempts() {
		return models.WebhookDeliveryStatusDeadLetter, "replay_or_investigate"
	}
	return models.WebhookDeliveryStatusFailed, "waiting_retry"
}

func webhookMaxAttempts() uint {
	return uintFromEnv("WEBHOOK_MAX_ATTEMPTS", 8)
}

func webhookDeliveryClaimTimeout() time.Duration {
	return durationFromEnv("WEBHOOK_DELIVERY_CLAIM_TIMEOUT", 2*time.Minute)
}

func (r *WebhookDeliveryRepo) ClaimDue(ctx context.Context, limit int, lockFor time.Duration) ([]models.WebhookDelivery, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	now := time.Now()
	var rows []models.WebhookDelivery
	lockDuration := webhookDeliveryClaimTimeout()
	if lockFor > 0 {
		lockDuration = lockFor
	}
	lockUntil := now.Add(lockDuration)
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Raw(`
			WITH active AS (
				SELECT
					id,
					event_id,
					updated_at,
					created_at,
					ROW_NUMBER() OVER (
						PARTITION BY COALESCE(NULLIF(TRIM(event_id), ''), id::text)
						ORDER BY updated_at ASC, created_at ASC, id ASC
					) AS event_rank,
					(
						(status IN ? AND (next_retry_at IS NULL OR next_retry_at <= ?))
						OR (status = ? AND next_retry_at <= ?)
					) AS is_due
				FROM webhook_deliveries
				WHERE status IN ?
			),
			suppressed AS (
				UPDATE webhook_deliveries wd
				SET status = ?,
				    last_error = ?,
				    failure_category = ?,
				    next_retry_at = NULL,
				    operator_action = ?,
				    updated_at = ?
				FROM active
				WHERE wd.id = active.id
				  AND active.event_rank > 1
				  AND active.is_due
				RETURNING wd.id
			),
			due AS (
				SELECT id
				FROM active
				WHERE event_rank = 1
				  AND is_due
				ORDER BY updated_at ASC, created_at ASC, id ASC
				LIMIT ?
			)
			SELECT wd.*
			FROM webhook_deliveries wd
			JOIN due ON due.id = wd.id
			FOR UPDATE OF wd SKIP LOCKED
		`,
			[]string{models.WebhookDeliveryStatusPending, models.WebhookDeliveryStatusFailed},
			now,
			models.WebhookDeliveryStatusProcessing,
			now,
			[]string{models.WebhookDeliveryStatusPending, models.WebhookDeliveryStatusFailed, models.WebhookDeliveryStatusProcessing},
			models.WebhookDeliveryStatusDeadLetter,
			"duplicate webhook delivery event_id suppressed before delivery",
			"duplicate",
			"duplicate_suppressed",
			now,
			limit,
		).Scan(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		ids := make([]uuid.UUID, 0, len(rows))
		for i := range rows {
			rows[i].Status = models.WebhookDeliveryStatusProcessing
			rows[i].NextRetryAt = &lockUntil
			ids = append(ids, rows[i].ID)
		}
		if err := tx.Model(&models.WebhookDelivery{}).
			Where("id IN ?", ids).
			Updates(map[string]any{
				"status":          models.WebhookDeliveryStatusProcessing,
				"operator_action": "delivery_in_progress",
				"updated_at":      now,
			}).Error; err != nil {
			return err
		}
		return tx.Model(&models.WebhookDelivery{}).
			Where("id IN ?", ids).
			Update("next_retry_at", lockUntil).Error
	})
	return rows, err
}

func (r *WebhookDeliveryRepo) EnqueueReplay(ctx context.Context, params WebhookReplayParams) (*models.WebhookDelivery, bool, error) {
	if params.DeliveryID == uuid.Nil {
		return nil, false, gorm.ErrInvalidData
	}
	now := params.RequestedAt
	if now.IsZero() {
		now = time.Now()
	}
	actor := webhooksvc.SanitizeDeliveryText(strings.TrimSpace(params.ActorEmail))

	var delivery *models.WebhookDelivery
	created := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var selected models.WebhookDelivery
		if err := tx.First(&selected, "id = ?", params.DeliveryID).Error; err != nil {
			return err
		}

		rootID := selected.ID
		if selected.OriginalDeliveryID != nil {
			rootID = *selected.OriginalDeliveryID
		}
		var root models.WebhookDelivery
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&root, "id = ?", rootID).Error; err != nil {
			return err
		}
		if params.MerchantID != nil && root.MerchantID != *params.MerchantID {
			return ErrWebhookReplayScopeDenied
		}
		if params.DomainID != nil && root.DomainID != *params.DomainID {
			return ErrWebhookReplayScopeDenied
		}

		var existing models.WebhookDelivery
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("original_delivery_id = ? AND status IN ?", root.ID, webhookReplayActiveStatuses()).
			Order("created_at DESC").
			First(&existing).Error
		if err == nil {
			delivery = &existing
			return nil
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		replay := newWebhookReplayDelivery(root, actor, now)
		if err := r.createWithDB(ctx, tx, &replay); err != nil {
			return err
		}
		delivery = &replay
		created = true
		return tx.Model(&models.WebhookDelivery{}).
			Where("id = ?", root.ID).
			Updates(map[string]any{
				"replay_count":        gorm.Expr("replay_count + 1"),
				"replay_requested_by": actor,
				"replay_requested_at": &now,
				"operator_action":     "replay_queued",
				"updated_at":          now,
			}).Error
	})
	return delivery, created, err
}

func webhookReplayActiveStatuses() []string {
	return []string{
		models.WebhookDeliveryStatusPending,
		models.WebhookDeliveryStatusProcessing,
		models.WebhookDeliveryStatusFailed,
	}
}

func newWebhookReplayDelivery(root models.WebhookDelivery, actor string, now time.Time) models.WebhookDelivery {
	return models.WebhookDelivery{
		MerchantID:         root.MerchantID,
		DomainID:           root.DomainID,
		PaymentID:          root.PaymentID,
		TransactionID:      root.TransactionID,
		EventID:            root.EventID,
		EventType:          root.EventType,
		EventVersion:       root.EventVersion,
		EntityType:         root.EntityType,
		EntityID:           root.EntityID,
		PayloadJSON:        root.PayloadJSON,
		TargetURL:          root.TargetURL,
		Status:             models.WebhookDeliveryStatusPending,
		OriginalDeliveryID: &root.ID,
		ReplayCount:        root.ReplayCount + 1,
		ReplayRequestedBy:  actor,
		ReplayRequestedAt:  &now,
		OperatorAction:     "delivery_pending",
	}
}

func (r *WebhookDeliveryRepo) ListDueLifecycle(ctx context.Context, limit int) ([]models.WebhookDelivery, error) {
	return r.ClaimDue(ctx, limit, 0)
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

func (r *WebhookDeliveryRepo) CountByStatus(ctx context.Context, statuses ...string) (map[string]int64, error) {
	out := make(map[string]int64, len(statuses))
	for _, status := range statuses {
		out[status] = 0
	}
	if len(statuses) == 0 {
		return out, nil
	}
	var rows []struct {
		Status string
		Count  int64
	}
	err := r.db.WithContext(ctx).
		Model(&models.WebhookDelivery{}).
		Select("status, COUNT(*) AS count").
		Where("status IN ?", statuses).
		Group("status").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.Status] = row.Count
	}
	return out, nil
}

func (r *WebhookDeliveryRepo) Find(ctx context.Context, id uuid.UUID) (*models.WebhookDelivery, error) {
	var row models.WebhookDelivery
	if err := r.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &row, nil
}
