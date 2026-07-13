package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"core/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrMoneyEventInboxInvalid = errors.New("invalid money event inbox record")
	ErrMoneyEventInboxLocked  = errors.New("money event inbox record is locked")
)

type MoneyEventInboxRepo struct {
	db *gorm.DB
}

type MoneyEventInboxConsumeParams struct {
	EventID          string
	ConsumerName     string
	IdempotencyScope string
	EventType        string
	ResourceType     string
	ResourceID       string
	MaxAttempts      uint
	LockFor          time.Duration
	Evidence         any
}

func NewMoneyEventInboxRepo(db *gorm.DB) *MoneyEventInboxRepo {
	return &MoneyEventInboxRepo{db: db}
}

func (r *MoneyEventInboxRepo) DB() *gorm.DB { return r.db }

func (r *MoneyEventInboxRepo) CountByStatus(ctx context.Context, statuses ...string) (map[string]int64, error) {
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
		Model(&models.MoneyEventInbox{}).
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

func (r *MoneyEventInboxRepo) ProcessWithDB(ctx context.Context, params MoneyEventInboxConsumeParams, fn func(*gorm.DB) error) (*models.MoneyEventInbox, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, gorm.ErrInvalidDB
	}
	prepared, err := prepareMoneyEventInbox(params)
	if err != nil {
		return nil, false, err
	}
	var out *models.MoneyEventInbox
	processed := false
	var processErr error
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, shouldProcess, err := claimMoneyEventInboxWithDB(ctx, tx, prepared)
		if err != nil {
			return err
		}
		out = row
		if !shouldProcess {
			return nil
		}
		if fn != nil {
			if err := fn(tx); err != nil {
				processErr = err
				if markErr := markMoneyEventInboxFailedWithDB(ctx, tx, row.ID, row.MaxAttempts, err); markErr != nil {
					return markErr
				}
				return tx.WithContext(ctx).First(out, "id = ?", row.ID).Error
			}
		}
		if err := markMoneyEventInboxSucceededWithDB(ctx, tx, row.ID); err != nil {
			return err
		}
		processed = true
		return tx.WithContext(ctx).First(out, "id = ?", row.ID).Error
	})
	if err != nil {
		return out, processed, err
	}
	if processErr != nil {
		return out, false, processErr
	}
	return out, processed, nil
}

func (r *MoneyEventInboxRepo) OldestAgeSecondsByStatus(ctx context.Context, statuses ...string) (map[string]float64, error) {
	ages := make(map[string]float64, len(statuses))
	for _, status := range statuses {
		ages[status] = 0
	}
	if r == nil || r.db == nil {
		return ages, gorm.ErrInvalidDB
	}
	if len(statuses) == 0 {
		return ages, nil
	}
	type row struct {
		Status    string
		CreatedAt time.Time
	}
	var rows []row
	if err := r.db.WithContext(ctx).
		Model(&models.MoneyEventInbox{}).
		Select("status, MIN(created_at) AS created_at").
		Where("status IN ?", statuses).
		Group("status").
		Find(&rows).Error; err != nil {
		return ages, err
	}
	now := time.Now()
	for _, row := range rows {
		if row.CreatedAt.IsZero() {
			continue
		}
		age := now.Sub(row.CreatedAt).Seconds()
		if age < 0 {
			age = 0
		}
		ages[row.Status] = age
	}
	return ages, nil
}

func (r *MoneyEventInboxRepo) AttemptCountByStatus(ctx context.Context, statuses ...string) (map[string]int64, error) {
	attempts := make(map[string]int64, len(statuses))
	for _, status := range statuses {
		attempts[status] = 0
	}
	if r == nil || r.db == nil {
		return attempts, gorm.ErrInvalidDB
	}
	if len(statuses) == 0 {
		return attempts, nil
	}
	type row struct {
		Status   string
		Attempts int64
	}
	var rows []row
	if err := r.db.WithContext(ctx).
		Model(&models.MoneyEventInbox{}).
		Select("status, COALESCE(SUM(attempts), 0) AS attempts").
		Where("status IN ?", statuses).
		Group("status").
		Find(&rows).Error; err != nil {
		return attempts, err
	}
	for _, row := range rows {
		attempts[row.Status] = row.Attempts
	}
	return attempts, nil
}

func (r *MoneyEventInboxRepo) MarkSucceeded(ctx context.Context, id uuid.UUID) error {
	if r == nil || r.db == nil {
		return gorm.ErrInvalidDB
	}
	return markMoneyEventInboxSucceededWithDB(ctx, r.db, id)
}

func (r *MoneyEventInboxRepo) MarkFailed(ctx context.Context, id uuid.UUID, maxAttempts uint, err error) error {
	if r == nil || r.db == nil {
		return gorm.ErrInvalidDB
	}
	return markMoneyEventInboxFailedWithDB(ctx, r.db, id, maxAttempts, err)
}

func claimMoneyEventInboxWithDB(ctx context.Context, tx *gorm.DB, prepared models.MoneyEventInbox) (*models.MoneyEventInbox, bool, error) {
	lockKey := "money-event-inbox:" + prepared.ConsumerName + ":" + prepared.EventID
	if tx.Dialector != nil && tx.Dialector.Name() == "postgres" {
		if err := tx.WithContext(ctx).Exec("SELECT pg_advisory_xact_lock(hashtext(?))", lockKey).Error; err != nil {
			return nil, false, err
		}
	}
	now := time.Now()
	var existing models.MoneyEventInbox
	err := tx.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&existing, "consumer_name = ? AND event_id = ?", prepared.ConsumerName, prepared.EventID).Error
	if err == nil {
		if existing.Status == models.MoneyEventInboxStatusSucceeded || existing.Status == models.MoneyEventInboxStatusDeadLetter {
			return &existing, false, nil
		}
		if existing.LockedUntil != nil && existing.LockedUntil.After(now) && existing.Status == models.MoneyEventInboxStatusProcessing {
			return &existing, false, ErrMoneyEventInboxLocked
		}
		updates := map[string]any{
			"status":            models.MoneyEventInboxStatusProcessing,
			"attempts":          gorm.Expr("attempts + 1"),
			"max_attempts":      prepared.MaxAttempts,
			"locked_until":      prepared.LockedUntil,
			"idempotency_scope": prepared.IdempotencyScope,
			"event_type":        prepared.EventType,
			"resource_type":     prepared.ResourceType,
			"resource_id":       prepared.ResourceID,
			"evidence_json":     prepared.EvidenceJSON,
			"updated_at":        now,
		}
		if err := tx.WithContext(ctx).Model(&models.MoneyEventInbox{}).Where("id = ?", existing.ID).Updates(updates).Error; err != nil {
			return nil, false, err
		}
		if err := tx.WithContext(ctx).First(&existing, "id = ?", existing.ID).Error; err != nil {
			return nil, false, err
		}
		return &existing, true, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, err
	}
	if err := tx.WithContext(ctx).Create(&prepared).Error; err != nil {
		return nil, false, err
	}
	return &prepared, true, nil
}

func markMoneyEventInboxSucceededWithDB(ctx context.Context, tx *gorm.DB, id uuid.UUID) error {
	now := time.Now()
	return tx.WithContext(ctx).
		Model(&models.MoneyEventInbox{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":           models.MoneyEventInboxStatusSucceeded,
			"locked_until":     nil,
			"processed_at":     &now,
			"last_error":       "",
			"failure_category": "",
			"updated_at":       now,
		}).Error
}

func markMoneyEventInboxFailedWithDB(ctx context.Context, tx *gorm.DB, id uuid.UUID, maxAttempts uint, err error) error {
	now := time.Now()
	status := models.MoneyEventInboxStatusFailed
	var current models.MoneyEventInbox
	if findErr := tx.WithContext(ctx).First(&current, "id = ?", id).Error; findErr != nil {
		return findErr
	}
	if maxAttempts == 0 {
		maxAttempts = current.MaxAttempts
	}
	if maxAttempts == 0 {
		maxAttempts = 8
	}
	if current.Attempts >= maxAttempts {
		status = models.MoneyEventInboxStatusDeadLetter
	}
	detail := ""
	if err != nil {
		detail = sanitizeReliabilityText(err.Error(), 1000)
	}
	return tx.WithContext(ctx).
		Model(&models.MoneyEventInbox{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":           status,
			"locked_until":     nil,
			"last_error":       detail,
			"failure_category": reliabilityFailureCategory(err),
			"updated_at":       now,
		}).Error
}

func prepareMoneyEventInbox(params MoneyEventInboxConsumeParams) (models.MoneyEventInbox, error) {
	eventID := strings.TrimSpace(params.EventID)
	consumer := strings.ToLower(strings.TrimSpace(params.ConsumerName))
	scope := strings.TrimSpace(params.IdempotencyScope)
	if scope == "" {
		scope = consumer + ":" + eventID
	}
	if eventID == "" || consumer == "" || scope == "" {
		return models.MoneyEventInbox{}, fmt.Errorf("%w: event id, consumer and scope are required", ErrMoneyEventInboxInvalid)
	}
	maxAttempts := params.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = 8
	}
	lockFor := params.LockFor
	if lockFor <= 0 {
		lockFor = 2 * time.Minute
	}
	now := time.Now()
	lockUntil := now.Add(lockFor)
	evidence, err := reliabilityEvidenceJSON(params.Evidence)
	if err != nil {
		return models.MoneyEventInbox{}, err
	}
	return models.MoneyEventInbox{
		ID:               uuid.New(),
		EventID:          eventID,
		ConsumerName:     consumer,
		IdempotencyScope: scope,
		EventType:        strings.TrimSpace(params.EventType),
		ResourceType:     strings.TrimSpace(params.ResourceType),
		ResourceID:       strings.TrimSpace(params.ResourceID),
		Status:           models.MoneyEventInboxStatusProcessing,
		Attempts:         1,
		MaxAttempts:      maxAttempts,
		LockedUntil:      &lockUntil,
		EvidenceJSON:     evidence,
		CreatedAt:        now,
		UpdatedAt:        now,
	}, nil
}

func reliabilityEvidenceJSON(value any) (string, error) {
	if value == nil {
		return "{}", nil
	}
	body, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	raw := strings.TrimSpace(string(body))
	if raw == "" || raw == "null" {
		return "{}", nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return "", err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return "", errors.New("evidence JSON has trailing data")
	} else if !errors.Is(err, io.EOF) {
		return "", err
	}
	switch decoded.(type) {
	case map[string]any:
		return raw, nil
	default:
		return "{}", nil
	}
}

func reliabilityFailureCategory(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline"):
		return "timeout"
	case strings.Contains(msg, "validation"), strings.Contains(msg, "invalid"):
		return "validation"
	case strings.Contains(msg, "broadcast"), strings.Contains(msg, "mempool"), strings.Contains(msg, "nonce"):
		return "broadcast_uncertain"
	default:
		return "transient"
	}
}

func sanitizeReliabilityText(value string, limit int) string {
	value = strings.TrimSpace(value)
	for _, marker := range []string{"secret", "token", "password", "authorization"} {
		lower := strings.ToLower(value)
		if idx := strings.Index(lower, marker); idx >= 0 {
			value = strings.TrimSpace(value[:idx]) + " [redacted]"
			break
		}
	}
	if limit > 0 && len(value) > limit {
		value = value[:limit]
	}
	return value
}
