package repositories

import (
	"context"
	"errors"
	"strings"
	"time"

	"core/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type SweepJobRepo struct {
	db *gorm.DB
}

func NewSweepJobRepo(db *gorm.DB) *SweepJobRepo {
	return &SweepJobRepo{db: db}
}

func (r *SweepJobRepo) EnqueueForTransaction(ctx context.Context, txModel models.Transaction) (*models.SweepJob, bool, error) {
	if txModel.WalletID == nil || txModel.MerchantID == nil {
		return nil, false, nil
	}
	if txModel.UniqueHash == "" {
		return nil, false, errors.New("transaction unique hash is required")
	}
	now := time.Now()
	job := &models.SweepJob{
		ID:                    uuid.New(),
		TransactionUniqueHash: txModel.UniqueHash,
		TransactionHash:       txModel.Hash,
		WalletID:              *txModel.WalletID,
		MerchantID:            *txModel.MerchantID,
		ChainID:               txModel.ChainID,
		Token:                 txModel.Token,
		Status:                models.SweepJobStatusPending,
		MaxAttempts:           uintFromEnv("SWEEP_MAX_ATTEMPTS", 12),
		NextRunAt:             &now,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(job)
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected == 1 {
		return job, true, nil
	}
	var existing models.SweepJob
	if err := r.db.WithContext(ctx).First(&existing, "transaction_unique_hash = ?", txModel.UniqueHash).Error; err != nil {
		return nil, false, err
	}
	return &existing, false, nil
}

func (r *SweepJobRepo) ClaimDue(ctx context.Context, limit int, lockFor time.Duration) ([]models.SweepJob, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if lockFor <= 0 {
		lockFor = 2 * time.Minute
	}
	now := time.Now()
	lockUntil := now.Add(lockFor)
	var jobs []models.SweepJob
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status IN ?", []string{models.SweepJobStatusPending, models.SweepJobStatusFailed}).
			Where("next_run_at IS NULL OR next_run_at <= ?", now).
			Where("locked_until IS NULL OR locked_until < ?", now).
			Order("created_at ASC").
			Limit(limit).
			Find(&jobs).Error; err != nil {
			return err
		}
		if len(jobs) == 0 {
			return nil
		}
		ids := make([]uuid.UUID, 0, len(jobs))
		for _, job := range jobs {
			ids = append(ids, job.ID)
		}
		return tx.Model(&models.SweepJob{}).
			Where("id IN ?", ids).
			Updates(map[string]any{
				"status":       models.SweepJobStatusProcessing,
				"locked_until": &lockUntil,
				"updated_at":   now,
			}).Error
	})
	return jobs, err
}

func (r *SweepJobRepo) MarkSucceeded(ctx context.Context, id uuid.UUID, txHash string) error {
	now := time.Now()
	return r.db.WithContext(ctx).
		Model(&models.SweepJob{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":        models.SweepJobStatusSucceeded,
			"sweep_tx_hash": strings.TrimSpace(txHash),
			"last_error":    "",
			"locked_until":  nil,
			"next_run_at":   nil,
			"updated_at":    now,
		}).Error
}

func (r *SweepJobRepo) MarkFailed(ctx context.Context, id uuid.UUID, err error) error {
	var job models.SweepJob
	if readErr := r.db.WithContext(ctx).First(&job, "id = ?", id).Error; readErr != nil {
		return readErr
	}
	attempts := job.Attempts + 1
	maxAttempts := job.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = uintFromEnv("SWEEP_MAX_ATTEMPTS", 12)
	}
	status := models.SweepJobStatusFailed
	var nextRunAt *time.Time
	if attempts >= maxAttempts {
		status = models.SweepJobStatusDeadLetter
	} else {
		next := time.Now().Add(sweepRetryBackoff(attempts))
		nextRunAt = &next
	}
	lastErr := ""
	if err != nil {
		lastErr = err.Error()
	}
	return r.db.WithContext(ctx).
		Model(&models.SweepJob{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":       status,
			"attempts":     attempts,
			"last_error":   lastErr,
			"locked_until": nil,
			"next_run_at":  nextRunAt,
			"updated_at":   time.Now(),
		}).Error
}

func (r *SweepJobRepo) ListDeadLetters(ctx context.Context, limit int) ([]models.SweepJob, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var jobs []models.SweepJob
	err := r.db.WithContext(ctx).
		Where("status = ?", models.SweepJobStatusDeadLetter).
		Order("updated_at DESC").
		Limit(limit).
		Find(&jobs).Error
	return jobs, err
}
