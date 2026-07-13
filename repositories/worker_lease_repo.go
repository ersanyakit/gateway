package repositories

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"core/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrWorkerLeaseInvalid = errors.New("invalid worker lease")
	ErrWorkerLeaseBusy    = errors.New("worker lease is held by another owner")
)

type WorkerLeaseRepo struct {
	db *gorm.DB
}

type WorkerLeaseRequest struct {
	LeaseKey string
	Purpose  string
	OwnerID  string
	TTL      time.Duration
}

func NewWorkerLeaseRepo(db *gorm.DB) *WorkerLeaseRepo {
	return &WorkerLeaseRepo{db: db}
}

func (r *WorkerLeaseRepo) EnsureRows(ctx context.Context, requests []WorkerLeaseRequest) error {
	if r == nil || r.db == nil {
		return gorm.ErrInvalidDB
	}
	if len(requests) == 0 {
		return nil
	}
	now := time.Now()
	releasedAt := now
	rows := make([]models.WorkerLease, 0, len(requests))
	for _, req := range requests {
		prepared, err := prepareWorkerLease(req)
		if err != nil {
			return err
		}
		prepared.Status = models.WorkerLeaseStatusReleased
		prepared.Attempts = 0
		prepared.LeaseUntil = now.Add(-time.Second)
		prepared.AcquiredAt = now
		prepared.LastHeartbeat = nil
		prepared.ReleasedAt = &releasedAt
		prepared.LastError = ""
		prepared.CreatedAt = now
		prepared.UpdatedAt = now
		rows = append(rows, prepared)
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "lease_key"}},
			DoNothing: true,
		}).
		Create(&rows).Error
}

func (r *WorkerLeaseRepo) CountByStatus(ctx context.Context, statuses ...string) (map[string]int64, error) {
	counts := make(map[string]int64, len(statuses))
	for _, status := range statuses {
		counts[status] = 0
	}
	if r == nil || r.db == nil {
		return counts, gorm.ErrInvalidDB
	}
	if len(statuses) == 0 {
		return counts, nil
	}
	type row struct {
		Status string
		Count  int64
	}
	var rows []row
	if err := r.db.WithContext(ctx).
		Model(&models.WorkerLease{}).
		Select("status, COUNT(*) AS count").
		Where("status IN ?", statuses).
		Group("status").
		Find(&rows).Error; err != nil {
		return counts, err
	}
	for _, row := range rows {
		counts[row.Status] = row.Count
	}
	return counts, nil
}

func (r *WorkerLeaseRepo) TryAcquire(ctx context.Context, req WorkerLeaseRequest) (*models.WorkerLease, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, gorm.ErrInvalidDB
	}
	prepared, err := prepareWorkerLease(req)
	if err != nil {
		return nil, false, err
	}
	acquired := false
	out := prepared
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if tx.Dialector != nil && tx.Dialector.Name() == "postgres" {
			if err := tx.WithContext(ctx).Exec("SELECT pg_advisory_xact_lock(hashtext(?))", "worker-lease:"+prepared.LeaseKey).Error; err != nil {
				return err
			}
		}
		now := time.Now()
		seed := prepared
		releasedAt := now
		seed.Status = models.WorkerLeaseStatusReleased
		seed.Attempts = 0
		seed.LeaseUntil = now.Add(-time.Second)
		seed.AcquiredAt = now
		seed.LastHeartbeat = nil
		seed.ReleasedAt = &releasedAt
		seed.LastError = ""
		seed.CreatedAt = now
		seed.UpdatedAt = now
		if err := tx.WithContext(ctx).
			Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "lease_key"}},
				DoNothing: true,
			}).
			Create(&seed).Error; err != nil {
			return err
		}

		var existing models.WorkerLease
		err := tx.WithContext(ctx).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&existing, "lease_key = ?", prepared.LeaseKey).Error
		if isWorkerLeaseRecordNotFound(err) {
			if err := tx.WithContext(ctx).Create(&prepared).Error; err != nil {
				return err
			}
			out = prepared
			acquired = true
			return nil
		}
		if err != nil {
			return err
		}
		if existing.Status == models.WorkerLeaseStatusActive && existing.LeaseUntil.After(now) && existing.OwnerID != prepared.OwnerID {
			out = existing
			return nil
		}
		updates := map[string]any{
			"owner_id":       prepared.OwnerID,
			"purpose":        prepared.Purpose,
			"status":         models.WorkerLeaseStatusActive,
			"attempts":       gorm.Expr("attempts + 1"),
			"lease_until":    prepared.LeaseUntil,
			"acquired_at":    prepared.AcquiredAt,
			"last_heartbeat": nil,
			"released_at":    nil,
			"last_error":     "",
			"updated_at":     now,
		}
		if err := tx.WithContext(ctx).Model(&models.WorkerLease{}).Where("id = ?", existing.ID).Updates(updates).Error; err != nil {
			return err
		}
		var refreshed models.WorkerLease
		if err := tx.WithContext(ctx).First(&refreshed, "id = ?", existing.ID).Error; err != nil {
			return err
		}
		out = refreshed
		acquired = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return &out, acquired, nil
}

func isWorkerLeaseRecordNotFound(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, gorm.ErrRecordNotFound) ||
		strings.Contains(strings.ToLower(err.Error()), "record not found")
}

func (r *WorkerLeaseRepo) Release(ctx context.Context, id uuid.UUID, ownerID string) error {
	if r == nil || r.db == nil {
		return gorm.ErrInvalidDB
	}
	now := time.Now()
	q := r.db.WithContext(ctx).Model(&models.WorkerLease{}).Where("id = ?", id)
	ownerID = strings.TrimSpace(ownerID)
	if ownerID != "" {
		q = q.Where("owner_id = ?", ownerID)
	}
	return q.Updates(map[string]any{
		"status":      models.WorkerLeaseStatusReleased,
		"released_at": &now,
		"updated_at":  now,
	}).Error
}

func (r *WorkerLeaseRepo) Heartbeat(ctx context.Context, id uuid.UUID, ownerID string, ttl time.Duration) error {
	if r == nil || r.db == nil {
		return gorm.ErrInvalidDB
	}
	if ttl <= 0 {
		ttl = 2 * time.Minute
	}
	now := time.Now()
	leaseUntil := now.Add(ttl)
	q := r.db.WithContext(ctx).Model(&models.WorkerLease{}).Where("id = ?", id)
	ownerID = strings.TrimSpace(ownerID)
	if ownerID != "" {
		q = q.Where("owner_id = ?", ownerID)
	}
	return q.Updates(map[string]any{
		"last_heartbeat": &now,
		"lease_until":    leaseUntil,
		"status":         models.WorkerLeaseStatusActive,
		"updated_at":     now,
	}).Error
}

func (r *WorkerLeaseRepo) MarkError(ctx context.Context, id uuid.UUID, ownerID string, err error) error {
	if r == nil || r.db == nil {
		return gorm.ErrInvalidDB
	}
	detail := ""
	if err != nil {
		detail = sanitizeReliabilityText(err.Error(), 1000)
	}
	q := r.db.WithContext(ctx).Model(&models.WorkerLease{}).Where("id = ?", id)
	ownerID = strings.TrimSpace(ownerID)
	if ownerID != "" {
		q = q.Where("owner_id = ?", ownerID)
	}
	return q.Updates(map[string]any{
		"last_error": detail,
		"updated_at": time.Now(),
	}).Error
}

func prepareWorkerLease(req WorkerLeaseRequest) (models.WorkerLease, error) {
	key := strings.TrimSpace(req.LeaseKey)
	purpose := strings.TrimSpace(req.Purpose)
	owner := strings.TrimSpace(req.OwnerID)
	if owner == "" {
		owner = defaultWorkerLeaseOwner()
	}
	if key == "" || purpose == "" || owner == "" {
		return models.WorkerLease{}, fmt.Errorf("%w: key, purpose and owner are required", ErrWorkerLeaseInvalid)
	}
	ttl := req.TTL
	if ttl <= 0 {
		ttl = 2 * time.Minute
	}
	now := time.Now()
	return models.WorkerLease{
		ID:         uuid.New(),
		LeaseKey:   key,
		OwnerID:    owner,
		Purpose:    purpose,
		Status:     models.WorkerLeaseStatusActive,
		Attempts:   1,
		LeaseUntil: now.Add(ttl),
		AcquiredAt: now,
		CreatedAt:  now,
		UpdatedAt:  now,
	}, nil
}

func defaultWorkerLeaseOwner() string {
	host, _ := os.Hostname()
	host = strings.TrimSpace(host)
	if host == "" {
		host = "gateway"
	}
	return fmt.Sprintf("%s:%d", host, os.Getpid())
}
