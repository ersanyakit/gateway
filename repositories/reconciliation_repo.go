package repositories

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

const (
	reconciliationReasonMaxLength = 120
	reconciliationScopeMaxLength  = 256
	reconciliationJSONMaxBytes    = 32 * 1024
)

type ReconciliationScope struct {
	ChainID             constants.ChainID
	FromBlock           int64
	ToBlock             int64
	Reason              string
	MerchantID          *uuid.UUID
	DomainID            *uuid.UUID
	ScopeKey            string
	ResourceType        string
	ResourceID          string
	AffectedResourceIDs []string
	Evidence            any
}

func NewReconciliationRepo(db *gorm.DB) *ReconciliationRepo {
	return &ReconciliationRepo{db: db}
}

func (r *ReconciliationRepo) CreateOpenIfMissing(ctx context.Context, chainID constants.ChainID, fromBlock, toBlock int64, reason string) (*models.ReconciliationJob, bool, error) {
	return r.CreateScopedOpenIfMissing(ctx, ReconciliationScope{
		ChainID:   chainID,
		FromBlock: fromBlock,
		ToBlock:   toBlock,
		Reason:    reason,
	})
}

func (r *ReconciliationRepo) CreateScopedOpenIfMissing(ctx context.Context, scope ReconciliationScope) (*models.ReconciliationJob, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, gorm.ErrInvalidDB
	}
	prepared, err := prepareReconciliationScope(scope)
	if err != nil {
		return nil, false, err
	}
	var existing models.ReconciliationJob
	err = r.db.WithContext(ctx).
		Where("scope_key = ? AND status IN ?", prepared.ScopeKey, activeReconciliationStatuses()).
		First(&existing).Error
	if err == nil {
		return &existing, false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, err
	}
	if strings.TrimSpace(scope.ScopeKey) == "" {
		err = r.db.WithContext(ctx).
			Where("chain_id = ? AND from_block = ? AND to_block = ? AND reason = ? AND status IN ?", prepared.ChainID, prepared.FromBlock, prepared.ToBlock, prepared.Reason, activeReconciliationStatuses()).
			Order("created_at ASC").
			First(&existing).Error
		if err == nil {
			return &existing, false, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, err
		}
	}
	now := time.Now()
	job := &models.ReconciliationJob{
		ID:                      uuid.New(),
		ChainID:                 prepared.ChainID,
		FromBlock:               prepared.FromBlock,
		ToBlock:                 prepared.ToBlock,
		Reason:                  prepared.Reason,
		Status:                  models.ReconciliationStatusOpen,
		MerchantID:              prepared.MerchantID,
		DomainID:                prepared.DomainID,
		ScopeKey:                prepared.ScopeKey,
		ResourceType:            prepared.ResourceType,
		ResourceID:              prepared.ResourceID,
		AffectedResourceIDsJSON: prepared.AffectedResourceIDsJSON,
		EvidenceJSON:            prepared.EvidenceJSON,
		CreatedAt:               now,
		UpdatedAt:               now,
	}
	if err := r.db.WithContext(ctx).Create(job).Error; err != nil {
		return nil, false, err
	}
	return job, true, nil
}

func (r *ReconciliationRepo) OpenWebhookDeliveryDrift(ctx context.Context, delivery models.WebhookDelivery, reason string) (*models.ReconciliationJob, bool, error) {
	if delivery.ID == uuid.Nil || strings.TrimSpace(delivery.EventID) == "" {
		return nil, false, gorm.ErrInvalidData
	}
	if strings.TrimSpace(reason) == "" {
		reason = "webhook_drift:" + delivery.EventID
	}
	merchantID := delivery.MerchantID
	domainID := delivery.DomainID
	return r.CreateScopedOpenIfMissing(ctx, ReconciliationScope{
		Reason:              reason,
		MerchantID:          &merchantID,
		DomainID:            &domainID,
		ScopeKey:            "webhook_drift:" + delivery.ID.String(),
		ResourceType:        "webhook_delivery",
		ResourceID:          delivery.ID.String(),
		AffectedResourceIDs: []string{delivery.ID.String(), delivery.EventID},
		Evidence: map[string]any{
			"delivery_id":      delivery.ID.String(),
			"event_id":         delivery.EventID,
			"event_type":       delivery.EventType,
			"entity_type":      delivery.EntityType,
			"status":           delivery.Status,
			"attempts":         delivery.Attempts,
			"failure_category": delivery.FailureCategory,
			"operator_action":  delivery.OperatorAction,
		},
	})
}

func (r *ReconciliationRepo) OpenStuckLifecycleJob(ctx context.Context, chainID constants.ChainID, merchantID *uuid.UUID, domainID *uuid.UUID, resourceType string, resourceID string, lifecycleStatus string, reason string, evidence any) (*models.ReconciliationJob, bool, error) {
	resourceType = strings.TrimSpace(resourceType)
	resourceID = strings.TrimSpace(resourceID)
	if resourceType == "" || resourceID == "" {
		return nil, false, gorm.ErrInvalidData
	}
	if strings.TrimSpace(reason) == "" {
		reason = "stuck_lifecycle:" + resourceType + ":" + resourceID
	}
	mergedEvidence := map[string]any{
		"resource_type":    resourceType,
		"resource_id":      resourceID,
		"lifecycle_status": strings.TrimSpace(lifecycleStatus),
	}
	if evidence != nil {
		mergedEvidence["details"] = evidence
	}
	return r.CreateScopedOpenIfMissing(ctx, ReconciliationScope{
		ChainID:             chainID,
		Reason:              reason,
		MerchantID:          merchantID,
		DomainID:            domainID,
		ScopeKey:            "stuck_lifecycle:" + resourceType + ":" + resourceID,
		ResourceType:        resourceType,
		ResourceID:          resourceID,
		AffectedResourceIDs: []string{resourceID},
		Evidence:            mergedEvidence,
	})
}

type preparedReconciliationScope struct {
	ChainID                 constants.ChainID
	FromBlock               int64
	ToBlock                 int64
	Reason                  string
	MerchantID              *uuid.UUID
	DomainID                *uuid.UUID
	ScopeKey                string
	ResourceType            string
	ResourceID              string
	AffectedResourceIDsJSON string
	EvidenceJSON            string
}

func prepareReconciliationScope(scope ReconciliationScope) (preparedReconciliationScope, error) {
	reason := boundedReconciliationValue(scope.Reason, reconciliationReasonMaxLength)
	if reason == "" {
		return preparedReconciliationScope{}, errors.New("reconciliation reason is required")
	}
	fromBlock := scope.FromBlock
	toBlock := scope.ToBlock
	if fromBlock < 0 {
		fromBlock = 0
	}
	if toBlock < fromBlock {
		toBlock = fromBlock
	}
	affectedJSON, err := marshalReconciliationJSON(scope.AffectedResourceIDs, "[]")
	if err != nil {
		return preparedReconciliationScope{}, fmt.Errorf("affected resource ids: %w", err)
	}
	evidenceJSON, err := marshalReconciliationJSON(scope.Evidence, "{}")
	if err != nil {
		return preparedReconciliationScope{}, fmt.Errorf("evidence: %w", err)
	}
	resourceType := boundedReconciliationValue(scope.ResourceType, 64)
	resourceID := boundedReconciliationValue(scope.ResourceID, reconciliationScopeMaxLength)
	scopeKey := strings.TrimSpace(scope.ScopeKey)
	if scopeKey == "" {
		scopeKey = defaultReconciliationScopeKey(scope, fromBlock, toBlock, reason, resourceType, resourceID)
	}
	scopeKey = boundedReconciliationValue(scopeKey, reconciliationScopeMaxLength)
	return preparedReconciliationScope{
		ChainID:                 scope.ChainID,
		FromBlock:               fromBlock,
		ToBlock:                 toBlock,
		Reason:                  reason,
		MerchantID:              scope.MerchantID,
		DomainID:                scope.DomainID,
		ScopeKey:                scopeKey,
		ResourceType:            resourceType,
		ResourceID:              resourceID,
		AffectedResourceIDsJSON: affectedJSON,
		EvidenceJSON:            evidenceJSON,
	}, nil
}

func defaultReconciliationScopeKey(scope ReconciliationScope, fromBlock int64, toBlock int64, reason string, resourceType string, resourceID string) string {
	merchantID := ""
	if scope.MerchantID != nil {
		merchantID = scope.MerchantID.String()
	}
	domainID := ""
	if scope.DomainID != nil {
		domainID = scope.DomainID.String()
	}
	return fmt.Sprintf("%d:%d:%d:%s:%s:%s:%s:%s", scope.ChainID, fromBlock, toBlock, reason, merchantID, domainID, resourceType, resourceID)
}

func boundedReconciliationValue(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	suffix := ":" + hex.EncodeToString(sum[:])[:16]
	prefixLen := limit - len(suffix)
	if prefixLen <= 0 {
		return suffix[len(suffix)-limit:]
	}
	return value[:prefixLen] + suffix
}

func marshalReconciliationJSON(value any, empty string) (string, error) {
	if value == nil {
		return empty, nil
	}
	body, err := json.Marshal(sanitizeReconciliationEvidence(value))
	if err != nil {
		return "", err
	}
	if len(body) > reconciliationJSONMaxBytes {
		return "", fmt.Errorf("payload exceeds %d bytes", reconciliationJSONMaxBytes)
	}
	return string(body), nil
}

func sanitizeReconciliationEvidence(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			if reconciliationEvidenceKeySensitive(key) {
				out[key] = "[redacted]"
				continue
			}
			out[key] = sanitizeReconciliationEvidence(child)
		}
		return out
	case map[string]string:
		out := make(map[string]string, len(typed))
		for key, child := range typed {
			if reconciliationEvidenceKeySensitive(key) {
				out[key] = "[redacted]"
				continue
			}
			out[key] = child
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = sanitizeReconciliationEvidence(child)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(typed))
		for i, child := range typed {
			sanitized, _ := sanitizeReconciliationEvidence(child).(map[string]any)
			out[i] = sanitized
		}
		return out
	default:
		return value
	}
}

func reconciliationEvidenceKeySensitive(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, marker := range []string{
		"api_secret",
		"webhook_secret",
		"private_key",
		"mnemonic",
		"raw_signature",
		"signature",
		"authorization",
		"password",
	} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

func activeReconciliationStatuses() []string {
	return []string{
		models.ReconciliationStatusOpen,
		models.ReconciliationStatusProcessing,
		models.ReconciliationStatusNeedsOperatorAction,
		models.ReconciliationStatusRetryScheduled,
	}
}

func (r *ReconciliationRepo) ClaimOpen(ctx context.Context, limit int) ([]models.ReconciliationJob, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	now := time.Now()
	var jobs []models.ReconciliationJob
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("(status = ? OR (status = ? AND (next_run_at IS NULL OR next_run_at <= ?)))", models.ReconciliationStatusOpen, models.ReconciliationStatusRetryScheduled, now).
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
				"status":      models.ReconciliationStatusProcessing,
				"started_at":  &now,
				"next_run_at": nil,
				"attempts":    gorm.Expr("attempts + 1"),
				"updated_at":  now,
			}).Error
	})
	return jobs, err
}

func (r *ReconciliationRepo) MarkResolved(ctx context.Context, id uuid.UUID) error {
	return r.MarkResolvedWithEvidence(ctx, id, nil, "")
}

func (r *ReconciliationRepo) RecordEvidence(ctx context.Context, id uuid.UUID, evidence any) error {
	evidenceJSON, err := marshalReconciliationJSON(evidence, "{}")
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).
		Model(&models.ReconciliationJob{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"evidence_json": evidenceJSON,
			"updated_at":    time.Now(),
		}).Error
}

func (r *ReconciliationRepo) MarkResolvedWithEvidence(ctx context.Context, id uuid.UUID, evidence any, outcome string) error {
	now := time.Now()
	updates := map[string]any{
		"status":      models.ReconciliationStatusResolved,
		"error":       "",
		"outcome":     boundedReconciliationValue(outcome, 64),
		"next_run_at": nil,
		"resolved_at": &now,
		"updated_at":  now,
	}
	if evidence != nil {
		evidenceJSON, err := marshalReconciliationJSON(evidence, "{}")
		if err != nil {
			return err
		}
		updates["evidence_json"] = evidenceJSON
	}
	return r.db.WithContext(ctx).Model(&models.ReconciliationJob{}).Where("id = ?", id).Updates(updates).Error
}

func (r *ReconciliationRepo) MarkNeedsOperatorAction(ctx context.Context, id uuid.UUID, evidence any, outcome string) error {
	return r.markEvidenceStatus(ctx, id, models.ReconciliationStatusNeedsOperatorAction, evidence, outcome, nil, "")
}

func (r *ReconciliationRepo) MarkRetryScheduled(ctx context.Context, id uuid.UUID, nextRunAt time.Time, evidence any, outcome string) error {
	return r.markEvidenceStatus(ctx, id, models.ReconciliationStatusRetryScheduled, evidence, outcome, &nextRunAt, "")
}

func (r *ReconciliationRepo) MarkFailed(ctx context.Context, id uuid.UUID, err error) error {
	lastErr := ""
	if err != nil {
		lastErr = err.Error()
	}
	return r.markEvidenceStatus(ctx, id, models.ReconciliationStatusFailed, nil, "", nil, lastErr)
}

func (r *ReconciliationRepo) markEvidenceStatus(ctx context.Context, id uuid.UUID, status string, evidence any, outcome string, nextRunAt *time.Time, lastErr string) error {
	updates := map[string]any{
		"status":      status,
		"outcome":     boundedReconciliationValue(outcome, 64),
		"error":       lastErr,
		"next_run_at": nextRunAt,
		"updated_at":  time.Now(),
	}
	if evidence != nil {
		evidenceJSON, err := marshalReconciliationJSON(evidence, "{}")
		if err != nil {
			return err
		}
		updates["evidence_json"] = evidenceJSON
	}
	return r.db.WithContext(ctx).
		Model(&models.ReconciliationJob{}).
		Where("id = ?", id).
		Updates(updates).Error
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
