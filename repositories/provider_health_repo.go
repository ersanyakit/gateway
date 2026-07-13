package repositories

import (
	"context"
	"core/constants"
	"core/models"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ProviderHealthRepo struct {
	db *gorm.DB
}

func NewProviderHealthRepo(db *gorm.DB) *ProviderHealthRepo {
	return &ProviderHealthRepo{db: db}
}

func (r *ProviderHealthRepo) DB() *gorm.DB { return r.db }

func (r *ProviderHealthRepo) UpsertLatest(ctx context.Context, snapshots []models.ProviderHealthSnapshot) error {
	if r == nil || r.db == nil {
		return errors.New("provider health repository is not configured")
	}
	now := time.Now().UTC()
	for i := range snapshots {
		snapshots[i].ChainName = strings.TrimSpace(snapshots[i].ChainName)
		snapshots[i].ProviderLabel = strings.TrimSpace(snapshots[i].ProviderLabel)
		snapshots[i].ProviderURLHash = strings.TrimSpace(snapshots[i].ProviderURLHash)
		snapshots[i].Status = strings.TrimSpace(snapshots[i].Status)
		snapshots[i].ErrorCategory = strings.TrimSpace(snapshots[i].ErrorCategory)
		snapshots[i].ErrorDetail = strings.TrimSpace(snapshots[i].ErrorDetail)
		snapshots[i].FailoverReason = strings.TrimSpace(snapshots[i].FailoverReason)
		if snapshots[i].ChainName == "" {
			snapshots[i].ChainName = constants.ChainName(snapshots[i].ChainID)
		}
		if snapshots[i].ProviderURLHash == "" {
			return errors.New("provider url hash is required")
		}
		if snapshots[i].ProviderLabel == "" {
			snapshots[i].ProviderLabel = shortProviderHashLabel(snapshots[i].ProviderURLHash)
		}
		if snapshots[i].Status == "" {
			snapshots[i].Status = models.ProviderHealthStatusUnknown
		}
		if snapshots[i].Status == models.ProviderHealthStatusUnhealthy && snapshots[i].ConsecutiveFailures <= 0 {
			snapshots[i].ConsecutiveFailures = 1
		} else if snapshots[i].Status != models.ProviderHealthStatusUnhealthy {
			snapshots[i].ConsecutiveFailures = 0
		}
		if snapshots[i].CheckedAt.IsZero() {
			snapshots[i].CheckedAt = now
		}
	}
	if len(snapshots) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "chain_id"}, {Name: "provider_url_hash"}},
		DoUpdates: clause.Assignments(map[string]any{
			"chain_name":           gorm.Expr("EXCLUDED.chain_name"),
			"provider_label":       gorm.Expr("EXCLUDED.provider_label"),
			"reachable":            gorm.Expr("EXCLUDED.reachable"),
			"status":               gorm.Expr("EXCLUDED.status"),
			"latest_height":        gorm.Expr("EXCLUDED.latest_height"),
			"head_hash":            gorm.Expr("EXCLUDED.head_hash"),
			"response_latency_ms":  gorm.Expr("EXCLUDED.response_latency_ms"),
			"lag_from_reference":   gorm.Expr("EXCLUDED.lag_from_reference"),
			"error_category":       gorm.Expr("EXCLUDED.error_category"),
			"error_detail":         gorm.Expr("EXCLUDED.error_detail"),
			"selected":             gorm.Expr("EXCLUDED.selected"),
			"failover_reason":      gorm.Expr("EXCLUDED.failover_reason"),
			"consecutive_failures": gorm.Expr("CASE WHEN EXCLUDED.status = ? THEN provider_health_snapshots.consecutive_failures + 1 ELSE 0 END", models.ProviderHealthStatusUnhealthy),
			"checked_at":           gorm.Expr("EXCLUDED.checked_at"),
			"updated_at":           gorm.Expr("EXCLUDED.updated_at"),
		}),
	}).Create(&snapshots).Error
}

func shortProviderHashLabel(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 12 {
		return value[:12]
	}
	return value
}

func (r *ProviderHealthRepo) ListLatest(ctx context.Context) ([]models.ProviderHealthSnapshot, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("provider health repository is not configured")
	}
	var snapshots []models.ProviderHealthSnapshot
	err := r.db.WithContext(ctx).
		Order("chain_id ASC, selected DESC, provider_label ASC").
		Find(&snapshots).Error
	return snapshots, err
}

func (r *ProviderHealthRepo) ListByChain(ctx context.Context, chainID constants.ChainID) ([]models.ProviderHealthSnapshot, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("provider health repository is not configured")
	}
	var snapshots []models.ProviderHealthSnapshot
	err := r.db.WithContext(ctx).
		Where("chain_id = ?", chainID).
		Order("selected DESC, provider_label ASC").
		Find(&snapshots).Error
	return snapshots, err
}
