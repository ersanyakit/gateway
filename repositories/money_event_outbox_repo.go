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
	ErrMoneyEventOutboxInvalid  = errors.New("invalid money event outbox record")
	ErrMoneyEventOutboxConflict = errors.New("money event outbox idempotency key conflicts with an existing event")
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
	*event = prepared

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
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	bodyString := strings.TrimSpace(string(body))
	if bodyString == "" || bodyString == "null" {
		return "", invalidMoneyEventOutbox("payload JSON must be an object or array")
	}
	return bodyString, nil
}

func invalidMoneyEventOutbox(format string, args ...any) error {
	if format == "" {
		return errors.Join(ErrMoneyEventOutboxInvalid, gorm.ErrInvalidData)
	}
	return errors.Join(ErrMoneyEventOutboxInvalid, gorm.ErrInvalidData, fmt.Errorf(format, args...))
}
