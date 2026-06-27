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

var ErrMoneyEventOutboxConflict = errors.New("money event outbox idempotency key conflicts with an existing event")

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

func NewMoneyEventOutboxRepo(db *gorm.DB) *MoneyEventOutboxRepo {
	return &MoneyEventOutboxRepo{db: db}
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
	if err := validateMoneyEventOutbox(event); err != nil {
		return nil, false, err
	}
	if event.ID == uuid.Nil {
		event.ID = uuid.New()
	}
	now := time.Now()
	if event.CreatedAt.IsZero() {
		event.CreatedAt = now
	}
	event.UpdatedAt = now

	result := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(event)
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected == 1 {
		return event, true, nil
	}

	existing, err := findExistingMoneyEventOutbox(ctx, tx, event)
	if err != nil {
		return nil, false, err
	}
	if !moneyEventOutboxCompatible(existing, event) {
		return existing, false, ErrMoneyEventOutboxConflict
	}
	return existing, false, nil
}

func BuildMoneyEventOutboxRecord(input MoneyEventOutboxBuildInput) (*models.MoneyEventOutbox, error) {
	eventType := strings.TrimSpace(input.EventType)
	catalogEntry, _, ok := webhooksvc.MoneyEventCatalogEntryForEmittedEvent(eventType)
	if !ok {
		return nil, fmt.Errorf("money event type %q is not cataloged", eventType)
	}
	payloadJSON, err := marshalMoneyEventOutboxPayload(input.Payload)
	if err != nil {
		return nil, err
	}
	aggregateType := strings.TrimSpace(input.AggregateType)
	if aggregateType == "" {
		aggregateType = catalogEntry.ResourceType
	}
	record := &models.MoneyEventOutbox{
		EventID:        strings.TrimSpace(input.EventID),
		EventType:      eventType,
		EventVersion:   catalogEntry.Version,
		AggregateType:  aggregateType,
		AggregateID:    strings.TrimSpace(input.AggregateID),
		MerchantID:     input.MerchantID,
		DomainID:       input.DomainID,
		IdempotencyKey: strings.TrimSpace(input.IdempotencyKey),
		PayloadJSON:    payloadJSON,
		Status:         models.MoneyEventOutboxStatusPending,
	}
	if err := validateMoneyEventOutbox(record); err != nil {
		return nil, err
	}
	return record, nil
}

func validateMoneyEventOutbox(event *models.MoneyEventOutbox) error {
	if event == nil {
		return gorm.ErrInvalidData
	}
	event.EventID = strings.TrimSpace(event.EventID)
	event.EventType = strings.TrimSpace(event.EventType)
	event.EventVersion = strings.TrimSpace(event.EventVersion)
	event.AggregateType = strings.TrimSpace(event.AggregateType)
	event.AggregateID = strings.TrimSpace(event.AggregateID)
	event.IdempotencyKey = strings.TrimSpace(event.IdempotencyKey)
	event.Status = strings.TrimSpace(event.Status)

	if event.EventID == "" ||
		event.EventType == "" ||
		event.EventVersion == "" ||
		event.AggregateType == "" ||
		event.AggregateID == "" ||
		event.IdempotencyKey == "" ||
		event.MerchantID == uuid.Nil ||
		event.DomainID == uuid.Nil {
		return gorm.ErrInvalidData
	}
	if event.Status == "" {
		event.Status = models.MoneyEventOutboxStatusPending
	}
	payloadJSON, err := canonicalMoneyEventOutboxJSON(event.PayloadJSON)
	if err != nil {
		return err
	}
	event.PayloadJSON = payloadJSON
	return nil
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
		existing.IdempotencyKey == incoming.IdempotencyKey &&
		existingPayload == incomingPayload
}

func marshalMoneyEventOutboxPayload(payload any) (string, error) {
	if payload == nil {
		return "", gorm.ErrInvalidData
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
		return "", gorm.ErrInvalidData
	}
	var payload any
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return "", err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return "", gorm.ErrInvalidData
	} else if !errors.Is(err, io.EOF) {
		return "", err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	bodyString := strings.TrimSpace(string(body))
	if bodyString == "" || bodyString == "null" {
		return "", gorm.ErrInvalidData
	}
	return bodyString, nil
}
