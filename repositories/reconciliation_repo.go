package repositories

import (
	"context"
	"errors"
	"time"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ReconciliationRepo struct {
	db *gorm.DB
}

func NewReconciliationRepo(db *gorm.DB) *ReconciliationRepo {
	return &ReconciliationRepo{db: db}
}

func (r *ReconciliationRepo) CreateOpenIfMissing(ctx context.Context, chainID constants.ChainID, fromBlock, toBlock int64, reason string) (*models.ReconciliationJob, bool, error) {
	var existing models.ReconciliationJob
	err := r.db.WithContext(ctx).
		Where("chain_id = ? AND from_block = ? AND to_block = ? AND reason = ? AND status IN ?", chainID, fromBlock, toBlock, reason, []string{models.ReconciliationStatusOpen, models.ReconciliationStatusProcessing}).
		First(&existing).Error
	if err == nil {
		return &existing, false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, err
	}
	now := time.Now()
	job := &models.ReconciliationJob{
		ID:        uuid.New(),
		ChainID:   chainID,
		FromBlock: fromBlock,
		ToBlock:   toBlock,
		Reason:    reason,
		Status:    models.ReconciliationStatusOpen,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := r.db.WithContext(ctx).Create(job).Error; err != nil {
		return nil, false, err
	}
	return job, true, nil
}

func (r *ReconciliationRepo) ClaimOpen(ctx context.Context, limit int) ([]models.ReconciliationJob, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	now := time.Now()
	var jobs []models.ReconciliationJob
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status = ?", models.ReconciliationStatusOpen).
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
		return tx.Model(&models.ReconciliationJob{}).
			Where("id IN ?", ids).
			Updates(map[string]any{
				"status":     models.ReconciliationStatusProcessing,
				"started_at": &now,
				"attempts":   gorm.Expr("attempts + 1"),
				"updated_at": now,
			}).Error
	})
	return jobs, err
}

func (r *ReconciliationRepo) MarkResolved(ctx context.Context, id uuid.UUID) error {
	now := time.Now()
	return r.db.WithContext(ctx).
		Model(&models.ReconciliationJob{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":      models.ReconciliationStatusResolved,
			"error":       "",
			"resolved_at": &now,
			"updated_at":  now,
		}).Error
}

func (r *ReconciliationRepo) MarkFailed(ctx context.Context, id uuid.UUID, err error) error {
	lastErr := ""
	if err != nil {
		lastErr = err.Error()
	}
	return r.db.WithContext(ctx).
		Model(&models.ReconciliationJob{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":     models.ReconciliationStatusFailed,
			"error":      lastErr,
			"updated_at": time.Now(),
		}).Error
}

func (r *ReconciliationRepo) CountByStatus(ctx context.Context, statuses ...string) (map[string]int64, error) {
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
		Model(&models.ReconciliationJob{}).
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
