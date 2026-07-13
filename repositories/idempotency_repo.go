package repositories

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"core/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrIdempotencyConflict = errors.New("idempotency key was already used with a different request")

type IdempotencyRepo struct {
	db *gorm.DB
}

func NewIdempotencyRepo(db *gorm.DB) *IdempotencyRepo {
	return &IdempotencyRepo{db: db}
}

func (r *IdempotencyRepo) RequestHash(payload any) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func (r *IdempotencyRepo) Begin(ctx context.Context, domainID, merchantID uuid.UUID, key, requestHash string, ttl time.Duration) (*models.IdempotencyKey, bool, error) {
	if key == "" {
		return nil, true, nil
	}
	expiresAt := time.Now().Add(ttl)
	record := &models.IdempotencyKey{
		ID:          uuid.New(),
		DomainID:    domainID,
		MerchantID:  merchantID,
		Key:         key,
		RequestHash: requestHash,
		Status:      models.IdempotencyStatusInProgress,
		ExpiresAt:   &expiresAt,
	}
	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(record)
	if result.Error != nil {
		return nil, false, result.Error
	}
	var current models.IdempotencyKey
	if err := r.db.WithContext(ctx).
		First(&current, "domain_id = ? AND key = ?", domainID, key).Error; err != nil {
		return nil, false, err
	}
	if current.RequestHash != requestHash {
		return &current, false, ErrIdempotencyConflict
	}
	if current.Status == models.IdempotencyStatusFailed {
		if err := r.db.WithContext(ctx).
			Model(&models.IdempotencyKey{}).
			Where("id = ?", current.ID).
			Updates(map[string]any{
				"status":             models.IdempotencyStatusInProgress,
				"payment_session_id": nil,
				"resource_type":      "",
				"resource_id":        nil,
				"response_body":      "",
				"error":              "",
				"expires_at":         &expiresAt,
				"updated_at":         time.Now(),
			}).Error; err != nil {
			return &current, false, err
		}
		current.Status = models.IdempotencyStatusInProgress
		current.PaymentSessionID = nil
		current.ResourceType = ""
		current.ResourceID = nil
		current.ResponseBody = ""
		current.Error = ""
		current.ExpiresAt = &expiresAt
		return &current, true, nil
	}
	return &current, result.RowsAffected == 1, nil
}

func (r *IdempotencyRepo) Complete(ctx context.Context, id uuid.UUID, sessionID uuid.UUID, responseBody string) error {
	return r.db.WithContext(ctx).
		Model(&models.IdempotencyKey{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":             models.IdempotencyStatusCompleted,
			"payment_session_id": &sessionID,
			"resource_type":      "payment_session",
			"resource_id":        &sessionID,
			"response_body":      responseBody,
			"error":              "",
			"updated_at":         time.Now(),
		}).Error
}

func (r *IdempotencyRepo) CompleteResource(ctx context.Context, id uuid.UUID, resourceType string, resourceID uuid.UUID, responseBody string) error {
	return r.db.WithContext(ctx).
		Model(&models.IdempotencyKey{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":        models.IdempotencyStatusCompleted,
			"resource_type": resourceType,
			"resource_id":   &resourceID,
			"response_body": responseBody,
			"error":         "",
			"updated_at":    time.Now(),
		}).Error
}

func (r *IdempotencyRepo) CompleteResourceByKey(ctx context.Context, domainID uuid.UUID, key string, resourceType string, resourceID uuid.UUID, responseBody string) error {
	return r.db.WithContext(ctx).
		Model(&models.IdempotencyKey{}).
		Where("domain_id = ? AND key = ?", domainID, key).
		Updates(map[string]any{
			"status":        models.IdempotencyStatusCompleted,
			"resource_type": resourceType,
			"resource_id":   &resourceID,
			"response_body": responseBody,
			"error":         "",
			"updated_at":    time.Now(),
		}).Error
}

func (r *IdempotencyRepo) Fail(ctx context.Context, id uuid.UUID, errText string) error {
	return r.db.WithContext(ctx).
		Model(&models.IdempotencyKey{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":     models.IdempotencyStatusFailed,
			"error":      errText,
			"updated_at": time.Now(),
		}).Error
}
