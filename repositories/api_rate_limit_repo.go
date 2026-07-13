package repositories

import (
	"context"
	"errors"
	"time"

	"core/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type APIRateLimitRepo struct {
	db *gorm.DB
}

func NewAPIRateLimitRepo(db *gorm.DB) *APIRateLimitRepo {
	return &APIRateLimitRepo{db: db}
}

func (r *APIRateLimitRepo) Allow(ctx context.Context, keyHash string, limit int, window time.Duration) (bool, error) {
	if r == nil || r.db == nil {
		return false, gorm.ErrInvalidDB
	}
	if limit <= 0 {
		limit = 1
	}
	if window <= 0 {
		window = time.Minute
	}
	now := time.Now().UTC()
	allowed := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", "api-rate-limit:"+keyHash).Error; err != nil {
			return err
		}
		var counter models.APIRateLimitCounter
		err := tx.First(&counter, "key_hash = ?", keyHash).Error
		if err == nil {
			if now.After(counter.ResetAt) {
				counter.Count = 1
				counter.ResetAt = now.Add(window)
				allowed = true
			} else if counter.Count < limit {
				counter.Count++
				allowed = true
			}
			if allowed {
				counter.UpdatedAt = now
				return tx.Save(&counter).Error
			}
			return nil
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		counter = models.APIRateLimitCounter{
			ID:      uuid.New(),
			KeyHash: keyHash,
			Count:   1,
			ResetAt: now.Add(window),
		}
		allowed = true
		return tx.Create(&counter).Error
	})
	return allowed, err
}
