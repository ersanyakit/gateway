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
	webhooksvc "core/services/webhook"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrMoneyEventOutboxInvalid   = errors.New("invalid money event outbox record")
	ErrMoneyEventOutboxConflict  = errors.New("money event outbox idempotency key conflicts with an existing event")
	ErrMoneyEventOutboxLeaseLost = errors.New("money event outbox lease is no longer owned by this worker")
)

type MoneyEventOutboxRepo struct {
	db *gorm.DB
}

type MoneyEventOutboxBuildInput struct {
	EventID        string
	EventType      string
	AggregateType  string
	AggregateID    string
	MerchantID     uuid.UUID
	DomainID       uuid.UUID
	IdempotencyKey string
	Payload        any
}

type MoneyEventOutboxBuildParams struct {
	EventName      string
	EventID        string
	AggregateType  string
	AggregateID    string
	MerchantID     uuid.UUID
	DomainID       uuid.UUID
	IdempotencyKey string
	Payload        any
}

func NewMoneyEventOutboxRepo(db *gorm.DB) *MoneyEventOutboxRepo {
	return &MoneyEventOutboxRepo{db: db}
}

func (r *MoneyEventOutboxRepo) FindByEventID(ctx context.Context, eventID string) (*models.MoneyEventOutbox, error) {
	if r == nil || r.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return nil, gorm.ErrInvalidData
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var event models.MoneyEventOutbox
	if err := r.db.WithContext(ctx).First(&event, "event_id = ?", eventID).Error; err != nil {
		return nil, err
	}
	return &event, nil
}

func (r *MoneyEventOutboxRepo) CountByStatus(ctx context.Context, statuses ...string) (map[string]int64, error) {
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
		Model(&models.MoneyEventOutbox{}).
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

// HasAggregateEvent reports whether the canonical outbox already owns a
// lifecycle notification for this aggregate. Legacy bridges must not create a
// second delivery while that durable event is pending, failed, or dead-lettered.
func (r *MoneyEventOutboxRepo) HasAggregateEvent(ctx context.Context, aggregateType, aggregateID, eventType string) (bool, error) {
	if r == nil || r.db == nil {
		return false, gorm.ErrInvalidDB
	}
	aggregateType = strings.TrimSpace(aggregateType)
	aggregateID = strings.TrimSpace(aggregateID)
	eventType = strings.TrimSpace(eventType)
	if aggregateType == "" || aggregateID == "" || eventType == "" {
		return false, gorm.ErrInvalidData
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var rows []models.MoneyEventOutbox
	if err := r.db.WithContext(ctx).
		Select("event_type").
		Where("aggregate_type = ? AND aggregate_id = ?", aggregateType, aggregateID).
		Find(&rows).Error; err != nil {
		return false, err
	}
	for _, row := range rows {
		if webhooksvc.MoneyEventTypesEquivalent(row.EventType, eventType) {
			return true, nil
		}
	}
	return false, nil
}

// HasAggregate reports whether the canonical outbox owns any lifecycle event
// for an aggregate. It is intentionally broader than HasAggregateEvent and is
// used by legacy bridges whose historical source event name may not exist in
// the current catalog. Once an aggregate has entered the canonical outbox, a
// second legacy queue must never race it.
func (r *MoneyEventOutboxRepo) HasAggregate(ctx context.Context, aggregateType, aggregateID string) (bool, error) {
	if r == nil || r.db == nil {
		return false, gorm.ErrInvalidDB
	}
	aggregateType = strings.TrimSpace(aggregateType)
	aggregateID = strings.TrimSpace(aggregateID)
	if aggregateType == "" || aggregateID == "" {
		return false, gorm.ErrInvalidData
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var count int64
	if err := r.db.WithContext(ctx).
		Model(&models.MoneyEventOutbox{}).
		Where("aggregate_type = ? AND aggregate_id = ?", aggregateType, aggregateID).
		Limit(1).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// ClaimDueForNotifications leases every durable money event, including rows
// whose domain or notification configuration is currently broken. Filtering
// those rows out here would strand them in pending forever without an attempt,
// error, or dead-letter trail. The relay performs the domain/config validation
// and persists the outcome. A processing row becomes claimable again after its
// lease expires, so a process crash cannot strand the event permanently.
func (r *MoneyEventOutboxRepo) ClaimDueForNotifications(ctx context.Context, limit int, lockFor time.Duration) ([]models.MoneyEventOutbox, error) {
	if r == nil || r.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if lockFor <= 0 {
		lockFor = durationFromEnv("MONEY_EVENT_OUTBOX_CLAIM_TIMEOUT", 2*time.Minute)
	}

	now := time.Now()
	lockUntil := now.Add(lockFor)
	rows := make([]models.MoneyEventOutbox, 0, limit)
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.
			Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Model(&models.MoneyEventOutbox{}).
			Where("money_event_outboxes.status IN ?", []string{
				models.MoneyEventOutboxStatusPending,
				models.MoneyEventOutboxStatusFailed,
				models.MoneyEventOutboxStatusProcessing,
			}).
			Where("money_event_outboxes.locked_until IS NULL OR money_event_outboxes.locked_until <= ?", now).
			Where(`(
				money_event_outboxes.sequence <= 0
				OR NOT EXISTS (
					SELECT 1
					FROM money_event_outboxes predecessor
					WHERE predecessor.merchant_id = money_event_outboxes.merchant_id
					  AND predecessor.domain_id = money_event_outboxes.domain_id
					  AND predecessor.aggregate_type = money_event_outboxes.aggregate_type
					  AND predecessor.aggregate_id = money_event_outboxes.aggregate_id
					  AND predecessor.sequence > 0
					  AND predecessor.sequence < money_event_outboxes.sequence
					  AND predecessor.status <> ?
				)
			)`, models.MoneyEventOutboxStatusDelivered).
			Order("money_event_outboxes.created_at ASC, money_event_outboxes.id ASC").
			Limit(limit)
		if err := query.Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}

		for i := range rows {
			leaseToken := uuid.New()
			rows[i].Status = models.MoneyEventOutboxStatusProcessing
			rows[i].LockedUntil = &lockUntil
			rows[i].LeaseToken = &leaseToken
			if err := tx.Model(&models.MoneyEventOutbox{}).
				Where("id = ?", rows[i].ID).
				Updates(map[string]any{
					"status":       models.MoneyEventOutboxStatusProcessing,
					"locked_until": &lockUntil,
					"lease_token":  leaseToken,
					"updated_at":   now,
				}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return rows, err
}

// MarkRelayAttempt closes a claimed outbox row only after the corresponding
// durable delivery row has been created. Failures remain retryable with bounded
// exponential backoff; permanent or exhausted failures move to dead letter.
// The lease token is rotated on every claim/reclaim, so a worker whose lease
// expired cannot overwrite the outcome of a newer owner.
func (r *MoneyEventOutboxRepo) MarkRelayAttempt(ctx context.Context, id, leaseToken uuid.UUID, delivered bool, lastErr error) error {
	if r == nil || r.db == nil {
		return gorm.ErrInvalidDB
	}
	if id == uuid.Nil || leaseToken == uuid.Nil {
		return gorm.ErrInvalidData
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var current models.MoneyEventOutbox
	if err := r.db.WithContext(ctx).
		Select("id", "status", "attempts", "lease_token").
		First(&current, "id = ?", id).Error; err != nil {
		return err
	}
	if current.Status != models.MoneyEventOutboxStatusProcessing ||
		current.LeaseToken == nil ||
		*current.LeaseToken != leaseToken {
		return ErrMoneyEventOutboxLeaseLost
	}

	now := time.Now()
	attempts := current.Attempts + 1
	updates := map[string]any{
		"attempts":     gorm.Expr("attempts + 1"),
		"locked_until": nil,
		"lease_token":  nil,
		"updated_at":   now,
	}
	if delivered {
		updates["status"] = models.MoneyEventOutboxStatusDelivered
		updates["last_error"] = ""
	} else {
		updates["last_error"] = webhooksvc.SanitizeDeliveryError(lastErr)
		if isPermanentDeliveryError(lastErr) || attempts >= moneyEventOutboxMaxAttempts() {
			updates["status"] = models.MoneyEventOutboxStatusDeadLetter
		} else {
			next := now.Add(moneyEventOutboxRetryBackoff(attempts))
			updates["status"] = models.MoneyEventOutboxStatusFailed
			updates["locked_until"] = &next
		}
	}
	result := r.db.WithContext(ctx).
		Model(&models.MoneyEventOutbox{}).
		Where("id = ? AND status = ? AND lease_token = ?", id, models.MoneyEventOutboxStatusProcessing, leaseToken).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrMoneyEventOutboxLeaseLost
	}
	return nil
}

// RequeueRelay moves an operator-reviewed failed/dead-lettered internal handoff
// back to pending. The immutable event identity and payload are preserved.
func (r *MoneyEventOutboxRepo) RequeueRelay(ctx context.Context, id uuid.UUID) (bool, error) {
	if r == nil || r.db == nil {
		return false, gorm.ErrInvalidDB
	}
	if id == uuid.Nil {
		return false, gorm.ErrInvalidData
	}
	if ctx == nil {
		ctx = context.Background()
	}
	result := r.db.WithContext(ctx).
		Model(&models.MoneyEventOutbox{}).
		Where("id = ?", id).
		Where("status IN ?", []string{models.MoneyEventOutboxStatusFailed, models.MoneyEventOutboxStatusDeadLetter}).
		Updates(map[string]any{
			"status":       models.MoneyEventOutboxStatusPending,
			"attempts":     0,
			"last_error":   "",
			"locked_until": nil,
			"lease_token":  nil,
			"updated_at":   time.Now(),
		})
	return result.RowsAffected == 1, result.Error
}

func moneyEventOutboxMaxAttempts() uint {
	return uintFromEnv("MONEY_EVENT_OUTBOX_MAX_ATTEMPTS", 16)
}

func moneyEventOutboxRetryBackoff(attempt uint) time.Duration {
	return exponentialBackoff(
		attempt,
		durationFromEnv("MONEY_EVENT_OUTBOX_RETRY_BACKOFF_BASE", 5*time.Second),
		durationFromEnv("MONEY_EVENT_OUTBOX_RETRY_BACKOFF_MAX", 5*time.Minute),
	)
}

func (r *MoneyEventOutboxRepo) Record(ctx context.Context, event *models.MoneyEventOutbox) (*models.MoneyEventOutbox, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, gorm.ErrInvalidDB
	}
	return r.RecordWithDB(ctx, r.db, event)
}

func (r *MoneyEventOutboxRepo) RecordWithDB(ctx context.Context, tx *gorm.DB, event *models.MoneyEventOutbox) (*models.MoneyEventOutbox, bool, error) {
	if tx == nil {
		return nil, false, gorm.ErrInvalidDB
	}
	prepared, err := prepareMoneyEventOutbox(event)
	if err != nil {
		return nil, false, err
	}
	existing, found, err := reserveMoneyEventOutboxSequence(ctx, tx, &prepared)
	if err != nil {
		return nil, false, err
	}
	if found {
		if prepared.Sequence <= 0 {
			prepared.Sequence = existing.Sequence
		}
		if !moneyEventOutboxCompatible(existing, &prepared) {
			return existing, false, ErrMoneyEventOutboxConflict
		}
		*event = *existing
		return existing, false, nil
	}
	*event = prepared

	result := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(event)
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected == 1 {
		return event, true, nil
	}

	existing, err = findExistingMoneyEventOutbox(ctx, tx, event)
	if err != nil {
		return nil, false, err
	}
	if !moneyEventOutboxCompatible(existing, event) {
		return existing, false, ErrMoneyEventOutboxConflict
	}
	return existing, false, nil
}

// RecordLifecycleWithDB persists a payout/refund/sweep lifecycle payload in the
// caller's transaction. State repositories use this boundary so committing a
// lifecycle transition without its notification evidence is impossible.
// EventID is also the delivery idempotency key: a business request's own
// idempotency key may span several distinct lifecycle events.
func (r *MoneyEventOutboxRepo) RecordLifecycleWithDB(ctx context.Context, tx *gorm.DB, payload webhooksvc.LifecyclePayload) (*models.MoneyEventOutbox, bool, error) {
	if tx == nil {
		return nil, false, gorm.ErrInvalidDB
	}
	eventID := strings.TrimSpace(payload.EventID)
	eventType := strings.TrimSpace(payload.EventType)
	aggregateType := strings.TrimSpace(payload.EntityType)
	aggregateID := strings.TrimSpace(payload.EntityID)
	merchantID, merchantErr := uuid.Parse(strings.TrimSpace(payload.MerchantID))
	domainID, domainErr := uuid.Parse(strings.TrimSpace(payload.DomainID))
	if eventID == "" || eventType == "" || aggregateType == "" || aggregateID == "" ||
		merchantErr != nil || domainErr != nil || merchantID == uuid.Nil || domainID == uuid.Nil {
		return nil, false, invalidMoneyEventOutbox("lifecycle payload identity or scope is invalid")
	}
	if eventID != aggregateID+":"+eventType {
		return nil, false, invalidMoneyEventOutbox("lifecycle event id %q does not match aggregate and event type", eventID)
	}
	if resourceType := strings.TrimSpace(payload.ResourceType); resourceType != "" && resourceType != aggregateType {
		return nil, false, invalidMoneyEventOutbox("lifecycle resource type %q does not match entity type %q", resourceType, aggregateType)
	}
	if resourceID := strings.TrimSpace(payload.ResourceID); resourceID != "" && resourceID != aggregateID {
		return nil, false, invalidMoneyEventOutbox("lifecycle resource id %q does not match entity id %q", resourceID, aggregateID)
	}

	event, err := BuildMoneyEventOutbox(MoneyEventOutboxBuildParams{
		EventName:      eventType,
		EventID:        eventID,
		AggregateType:  aggregateType,
		AggregateID:    aggregateID,
		MerchantID:     merchantID,
		DomainID:       domainID,
		IdempotencyKey: eventID,
		Payload:        payload,
	})
	if err != nil {
		return nil, false, err
	}
	return r.RecordWithDB(ctx, tx, &event)
}

func reserveMoneyEventOutboxSequence(ctx context.Context, tx *gorm.DB, event *models.MoneyEventOutbox) (*models.MoneyEventOutbox, bool, error) {
	if tx == nil || event == nil {
		return nil, false, gorm.ErrInvalidDB
	}
	lockKey := fmt.Sprintf("money-event-sequence:%s:%s:%s:%s", event.MerchantID, event.DomainID, event.AggregateType, event.AggregateID)
	if tx.Dialector != nil && tx.Dialector.Name() == "postgres" {
		if err := tx.WithContext(ctx).Exec("SELECT pg_advisory_xact_lock(hashtext(?))", lockKey).Error; err != nil {
			return nil, false, err
		}
	}
	var existing models.MoneyEventOutbox
	err := tx.WithContext(ctx).Where("event_id = ?", event.EventID).First(&existing).Error
	if err == nil {
		return &existing, true, nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, err
	}
	err = tx.WithContext(ctx).
		Where("merchant_id = ? AND domain_id = ? AND idempotency_key = ?", event.MerchantID, event.DomainID, event.IdempotencyKey).
		First(&existing).Error
	if err == nil {
		return &existing, true, nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, err
	}
	if event.Sequence > 0 {
		return nil, false, invalidMoneyEventOutbox("caller-supplied aggregate sequence is not allowed for a new event")
	}
	var lastSequence int64
	if err := tx.WithContext(ctx).
		Model(&models.MoneyEventOutbox{}).
		Select("COALESCE(MAX(sequence), 0)").
		Where("merchant_id = ? AND domain_id = ? AND aggregate_type = ? AND aggregate_id = ?", event.MerchantID, event.DomainID, event.AggregateType, event.AggregateID).
		Scan(&lastSequence).Error; err != nil {
		return nil, false, err
	}
	event.Sequence = lastSequence + 1
	return nil, false, nil
}

func BuildMoneyEventOutbox(params MoneyEventOutboxBuildParams) (models.MoneyEventOutbox, error) {
	eventName := strings.TrimSpace(params.EventName)
	catalogEntry, _, ok := webhooksvc.MoneyEventCatalogEntryForEmittedEvent(eventName)
	if !ok {
		return models.MoneyEventOutbox{}, invalidMoneyEventOutbox("event type %q is not cataloged", eventName)
	}
	payloadJSON, err := marshalMoneyEventOutboxPayload(params.Payload)
	if err != nil {
		return models.MoneyEventOutbox{}, err
	}
	aggregateType := strings.TrimSpace(params.AggregateType)
	if aggregateType == "" {
		aggregateType = catalogEntry.ResourceType
	}
	record := models.MoneyEventOutbox{
		EventID:        strings.TrimSpace(params.EventID),
		EventType:      eventName,
		EventVersion:   catalogEntry.Version,
		AggregateType:  aggregateType,
		AggregateID:    strings.TrimSpace(params.AggregateID),
		MerchantID:     params.MerchantID,
		DomainID:       params.DomainID,
		IdempotencyKey: strings.TrimSpace(params.IdempotencyKey),
		PayloadJSON:    payloadJSON,
		Status:         models.MoneyEventOutboxStatusPending,
	}
	return prepareMoneyEventOutbox(&record)
}

func BuildMoneyEventOutboxRecord(input MoneyEventOutboxBuildInput) (*models.MoneyEventOutbox, error) {
	record, err := BuildMoneyEventOutbox(MoneyEventOutboxBuildParams{
		EventID:        strings.TrimSpace(input.EventID),
		EventName:      strings.TrimSpace(input.EventType),
		AggregateType:  strings.TrimSpace(input.AggregateType),
		AggregateID:    strings.TrimSpace(input.AggregateID),
		MerchantID:     input.MerchantID,
		DomainID:       input.DomainID,
		IdempotencyKey: strings.TrimSpace(input.IdempotencyKey),
		Payload:        input.Payload,
	})
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func validateMoneyEventOutbox(event *models.MoneyEventOutbox) error {
	prepared, err := prepareMoneyEventOutbox(event)
	if err != nil {
		return err
	}
	*event = prepared
	return nil
}

func prepareMoneyEventOutbox(event *models.MoneyEventOutbox) (models.MoneyEventOutbox, error) {
	if event == nil {
		return models.MoneyEventOutbox{}, invalidMoneyEventOutbox("record is nil")
	}
	prepared := *event
	prepared.EventID = strings.TrimSpace(prepared.EventID)
	prepared.EventType = strings.TrimSpace(prepared.EventType)
	prepared.EventVersion = strings.TrimSpace(prepared.EventVersion)
	prepared.AggregateType = strings.TrimSpace(prepared.AggregateType)
	prepared.AggregateID = strings.TrimSpace(prepared.AggregateID)
	prepared.IdempotencyKey = strings.TrimSpace(prepared.IdempotencyKey)
	prepared.Status = strings.TrimSpace(prepared.Status)

	if prepared.EventVersion == "" {
		prepared.EventVersion = "v1"
	}
	if prepared.Status == "" {
		prepared.Status = models.MoneyEventOutboxStatusPending
	}
	if prepared.ID == uuid.Nil {
		prepared.ID = uuid.New()
	}
	now := time.Now()
	if prepared.CreatedAt.IsZero() {
		prepared.CreatedAt = now
	}
	prepared.UpdatedAt = now

	if prepared.EventID == "" ||
		prepared.EventType == "" ||
		prepared.EventVersion == "" ||
		prepared.AggregateType == "" ||
		prepared.AggregateID == "" ||
		prepared.IdempotencyKey == "" ||
		prepared.MerchantID == uuid.Nil ||
		prepared.DomainID == uuid.Nil {
		return models.MoneyEventOutbox{}, invalidMoneyEventOutbox("required field is empty")
	}
	payloadJSON, err := canonicalMoneyEventOutboxJSON(prepared.PayloadJSON)
	if err != nil {
		return models.MoneyEventOutbox{}, err
	}
	prepared.PayloadJSON = payloadJSON
	return prepared, nil
}

func findExistingMoneyEventOutbox(ctx context.Context, tx *gorm.DB, event *models.MoneyEventOutbox) (*models.MoneyEventOutbox, error) {
	var existing models.MoneyEventOutbox
	err := tx.WithContext(ctx).
		First(&existing, "event_id = ?", event.EventID).Error
	if err == nil {
		return &existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	err = tx.WithContext(ctx).
		First(&existing, "merchant_id = ? AND domain_id = ? AND idempotency_key = ?", event.MerchantID, event.DomainID, event.IdempotencyKey).Error
	if err != nil {
		return nil, err
	}
	return &existing, nil
}

func moneyEventOutboxCompatible(existing, incoming *models.MoneyEventOutbox) bool {
	if existing == nil || incoming == nil {
		return false
	}
	existingPayload, err := canonicalMoneyEventOutboxJSON(existing.PayloadJSON)
	if err != nil {
		return false
	}
	incomingPayload, err := canonicalMoneyEventOutboxJSON(incoming.PayloadJSON)
	if err != nil {
		return false
	}
	return existing.EventID == incoming.EventID &&
		existing.EventType == incoming.EventType &&
		existing.EventVersion == incoming.EventVersion &&
		existing.AggregateType == incoming.AggregateType &&
		existing.AggregateID == incoming.AggregateID &&
		existing.MerchantID == incoming.MerchantID &&
		existing.DomainID == incoming.DomainID &&
		existing.Sequence == incoming.Sequence &&
		existing.IdempotencyKey == incoming.IdempotencyKey &&
		existingPayload == incomingPayload
}

func marshalMoneyEventOutboxPayload(payload any) (string, error) {
	if payload == nil {
		return "", invalidMoneyEventOutbox("payload is nil")
	}
	if raw, ok := payload.(json.RawMessage); ok {
		return canonicalMoneyEventOutboxJSON(string(raw))
	}
	if raw, ok := payload.([]byte); ok {
		return canonicalMoneyEventOutboxJSON(string(raw))
	}
	if raw, ok := payload.(string); ok {
		return canonicalMoneyEventOutboxJSON(raw)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return canonicalMoneyEventOutboxJSON(string(body))
}

func canonicalMoneyEventOutboxJSON(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", invalidMoneyEventOutbox("payload is empty")
	}
	var payload any
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return "", invalidMoneyEventOutbox("payload JSON is invalid: %v", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return "", invalidMoneyEventOutbox("payload JSON has trailing data")
	} else if !errors.Is(err, io.EOF) {
		return "", invalidMoneyEventOutbox("payload JSON is invalid: %v", err)
	}
	if _, ok := payload.(map[string]any); !ok {
		return "", invalidMoneyEventOutbox("payload JSON must be an object")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	bodyString := strings.TrimSpace(string(body))
	if bodyString == "" || bodyString == "null" {
		return "", invalidMoneyEventOutbox("payload JSON must be an object")
	}
	return bodyString, nil
}

func invalidMoneyEventOutbox(format string, args ...any) error {
	if format == "" {
		return errors.Join(ErrMoneyEventOutboxInvalid, gorm.ErrInvalidData)
	}
	return errors.Join(ErrMoneyEventOutboxInvalid, gorm.ErrInvalidData, fmt.Errorf(format, args...))
}
