package webhook

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"core/constants"
	"core/models"
	"core/types"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	errDeliveryDomainLookupNotConfigured      = errors.New("domain lookup is not configured")
	errDeliveryTransactionLookupNotConfigured = errors.New("transaction lookup is not configured")
	errDeliveryPaymentLookupNotConfigured     = errors.New("payment lookup is not configured")
)

type DeliveryQueue interface {
	ClaimDue(ctx context.Context, limit int) ([]models.WebhookDelivery, error)
	MarkAttempt(ctx context.Context, id, leaseToken uuid.UUID, delivered bool, lastErr error) error
}

type DeliveryDomainLookup interface {
	FindByID(types.DomainParams) (*models.Domain, error)
}

type DeliveryTransactionStore interface {
	FindByID(ctx context.Context, id uuid.UUID) (*models.Transaction, error)
	MarkWebhookAttempt(ctx context.Context, uniqueHash string, delivered bool, lastErr error) error
}

type DeliveryPaymentStore interface {
	FindByID(ctx context.Context, id uuid.UUID) (*models.PaymentSession, error)
	MarkWebhookAttempt(ctx context.Context, sessionID uuid.UUID, delivered bool, lastErr error) error
}

type DeliveryNotifier interface {
	Deliver(ctx context.Context, domain models.Domain, tx models.Transaction) error
	DeliverPayment(ctx context.Context, domain models.Domain, session models.PaymentSession) error
	DeliverRaw(ctx context.Context, domain models.Domain, eventType, eventID, eventVersion string, body []byte) error
}

type DeliveryMetadataNotifier interface {
	DeliverWithMetadata(ctx context.Context, domain models.Domain, tx models.Transaction, metadata DeliveryMetadata) error
	DeliverPaymentWithMetadata(ctx context.Context, domain models.Domain, session models.PaymentSession, metadata DeliveryMetadata) error
	DeliverRawWithMetadata(ctx context.Context, domain models.Domain, eventType, eventID, eventVersion string, body []byte, metadata DeliveryMetadata) error
}

type DeliveryRunSummary struct {
	Claimed   int
	Delivered int
	Failed    int
}

type DeliveryBoundary struct {
	Queue           DeliveryQueue
	Domains         DeliveryDomainLookup
	Transactions    DeliveryTransactionStore
	Payments        DeliveryPaymentStore
	Notifier        DeliveryNotifier
	Logger          *slog.Logger
	DeliveryTimeout time.Duration
}

func (b DeliveryBoundary) DeliverDue(ctx context.Context, limit int) (DeliveryRunSummary, error) {
	if b.Queue == nil {
		return DeliveryRunSummary{}, errors.New("webhook delivery queue is not configured")
	}
	rows, err := b.Queue.ClaimDue(ctx, limit)
	if err != nil {
		return DeliveryRunSummary{}, err
	}
	summary := DeliveryRunSummary{Claimed: len(rows)}
	for _, row := range rows {
		if err := b.DeliverOne(ctx, row); err != nil {
			summary.Failed++
			continue
		}
		summary.Delivered++
	}
	return summary, nil
}

func (b DeliveryBoundary) DeliverOne(ctx context.Context, row models.WebhookDelivery) error {
	if b.Queue == nil {
		return errors.New("webhook delivery queue is not configured")
	}
	if row.ID == uuid.Nil || row.LeaseToken == nil || *row.LeaseToken == uuid.Nil {
		return errors.New("webhook delivery row has no lease ownership token")
	}
	deliveryErr := b.deliverOne(ctx, row)
	delivered := deliveryErr == nil
	safeDeliveryErr := RedactDeliveryError(deliveryErr)

	// The durable delivery row is authoritative. Source webhook fields are a
	// compatibility projection and must never turn an acknowledged external
	// delivery back into retryable work, which would send the same event again.
	if markErr := b.markDeliveryAttempt(ctx, row, delivered, safeDeliveryErr); markErr != nil {
		err := errors.Join(deliveryErr, markErr)
		b.logAttempt(ctx, row, false, err)
		return err
	}
	if sourceErr := b.markSourceAttempt(ctx, row, delivered, safeDeliveryErr); sourceErr != nil {
		b.logSourceProjectionError(ctx, row, sourceErr)
	}
	b.logAttempt(ctx, row, delivered, deliveryErr)
	return deliveryErr
}

func (b DeliveryBoundary) deliverOne(ctx context.Context, row models.WebhookDelivery) error {
	if b.Notifier == nil {
		return permanent(errors.New("webhook notifier is not configured"))
	}
	if b.Domains == nil {
		return permanent(errors.New("domain repository is not configured"))
	}
	domain, err := b.domain(ctx, row.DomainID)
	if err != nil {
		return deliveryLookupError("domain", err)
	}
	if domain == nil {
		return permanent(errors.New("domain lookup returned nil"))
	}
	deliveryDomain := deliveryTargetSnapshot(*domain, row)

	deliveryCtx := ctx
	cancel := func() {}
	if timeout := b.timeout(); timeout > 0 {
		deliveryCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	switch {
	case strings.TrimSpace(row.PayloadJSON) != "":
		eventVersion := row.EventVersion
		if eventVersion == "" {
			eventVersion = constants.WebhookEventVersionV1
		}
		if notifier, ok := b.Notifier.(DeliveryMetadataNotifier); ok {
			return notifier.DeliverRawWithMetadata(deliveryCtx, deliveryDomain, row.EventType, row.EventID, eventVersion, []byte(row.PayloadJSON), DeliveryMetadataFromRow(row))
		}
		return b.Notifier.DeliverRaw(deliveryCtx, deliveryDomain, row.EventType, row.EventID, eventVersion, []byte(row.PayloadJSON))
	case row.TransactionID != nil:
		if b.Transactions == nil {
			return permanent(errors.New("transaction repository is not configured"))
		}
		txModel, err := b.Transactions.FindByID(ctx, *row.TransactionID)
		if err != nil {
			return deliveryLookupError("transaction", err)
		}
		if txModel == nil {
			return permanent(errors.New("transaction lookup returned nil"))
		}
		if notifier, ok := b.Notifier.(DeliveryMetadataNotifier); ok {
			return notifier.DeliverWithMetadata(deliveryCtx, deliveryDomain, *txModel, DeliveryMetadataFromRow(row))
		}
		return b.Notifier.Deliver(deliveryCtx, deliveryDomain, *txModel)
	case row.PaymentID != nil:
		if b.Payments == nil {
			return permanent(errors.New("payment repository is not configured"))
		}
		session, err := b.Payments.FindByID(ctx, *row.PaymentID)
		if err != nil {
			return deliveryLookupError("payment", err)
		}
		if session == nil {
			return permanent(errors.New("payment lookup returned nil"))
		}
		if notifier, ok := b.Notifier.(DeliveryMetadataNotifier); ok {
			return notifier.DeliverPaymentWithMetadata(deliveryCtx, deliveryDomain, *session, DeliveryMetadataFromRow(row))
		}
		return b.Notifier.DeliverPayment(deliveryCtx, deliveryDomain, *session)
	default:
		return permanent(errors.New("delivery row has no transaction, payment, or lifecycle payload"))
	}
}

// deliveryTargetSnapshot keeps a queued event bound to the transport endpoint
// and subject selected at enqueue time. Credentials remain sourced from the
// current domain so secret rotation does not invalidate queued work. Rows from
// before target snapshots were introduced fall back to the current domain.
func deliveryTargetSnapshot(domain models.Domain, row models.WebhookDelivery) models.Domain {
	mode := strings.TrimSpace(row.NotificationMode)
	if mode == "" {
		return domain
	}
	domain.NotificationMode = models.NormalizeDomainNotificationMode(mode)
	if domain.NotificationMode == models.DomainNotificationNATS {
		domain.NATSURL = strings.TrimSpace(row.TargetURL)
		domain.NATSSubject = strings.TrimSpace(row.TargetSubject)
		return domain
	}
	domain.WebhookURL = strings.TrimSpace(row.TargetURL)
	return domain
}

func deliveryLookupError(resource string, err error) error {
	if err == nil {
		return nil
	}
	wrapped := fmt.Errorf("find webhook %s: %w", resource, err)
	if errors.Is(err, gorm.ErrRecordNotFound) ||
		errors.Is(err, sql.ErrNoRows) ||
		errors.Is(err, gorm.ErrInvalidDB) ||
		errors.Is(err, errDeliveryDomainLookupNotConfigured) ||
		errors.Is(err, errDeliveryTransactionLookupNotConfigured) ||
		errors.Is(err, errDeliveryPaymentLookupNotConfigured) {
		return permanent(wrapped)
	}
	return wrapped
}

func DeliveryMetadataFromRow(row models.WebhookDelivery) DeliveryMetadata {
	metadata := DeliveryMetadata{
		DeliveryID:     row.ID.String(),
		ResourceType:   firstNonEmpty(row.ResourceType, row.EntityType),
		ResourceID:     firstNonEmpty(row.ResourceID, webhookDeliveryEntityID(row)),
		Sequence:       row.Sequence,
		IdempotencyKey: firstNonEmpty(row.IdempotencyKey, row.EventID),
		ReplayCount:    row.ReplayCount,
	}
	if row.OriginalDeliveryID != nil {
		metadata.OriginalDeliveryID = row.OriginalDeliveryID.String()
	}
	return metadata
}

func webhookDeliveryEntityID(row models.WebhookDelivery) string {
	if row.EntityID != nil {
		return row.EntityID.String()
	}
	if row.PaymentID != nil {
		return row.PaymentID.String()
	}
	if row.TransactionID != nil {
		return row.TransactionID.String()
	}
	return ""
}

func (b DeliveryBoundary) domain(ctx context.Context, domainID uuid.UUID) (*models.Domain, error) {
	if b.Domains == nil {
		return nil, errors.New("domain repository is not configured")
	}
	domainIDString := domainID.String()
	return b.Domains.FindByID(types.DomainParams{
		Context:  ctx,
		DomainID: &domainIDString,
	})
}

func (b DeliveryBoundary) markDeliveryAttempt(ctx context.Context, row models.WebhookDelivery, delivered bool, err error) error {
	if b.Queue == nil || row.ID == uuid.Nil {
		return nil
	}
	if row.LeaseToken == nil || *row.LeaseToken == uuid.Nil {
		return errors.New("webhook delivery row has no lease token")
	}
	return b.Queue.MarkAttempt(ctx, row.ID, *row.LeaseToken, delivered, err)
}

func (b DeliveryBoundary) markSourceAttempt(ctx context.Context, row models.WebhookDelivery, delivered bool, err error) error {
	if row.TransactionID != nil && b.Transactions != nil {
		txModel, findErr := b.Transactions.FindByID(ctx, *row.TransactionID)
		if findErr != nil {
			return fmt.Errorf("find webhook source transaction: %w", findErr)
		}
		if txModel == nil || strings.TrimSpace(txModel.UniqueHash) == "" {
			return errors.New("webhook source transaction has no unique hash")
		}
		if !MoneyEventTypesEquivalent(row.EventType, txModel.EventType) {
			return nil
		}
		return b.Transactions.MarkWebhookAttempt(ctx, txModel.UniqueHash, delivered, err)
	}
	if row.PaymentID != nil && b.Payments != nil {
		session, findErr := b.Payments.FindByID(ctx, *row.PaymentID)
		if findErr != nil {
			return fmt.Errorf("find webhook source payment: %w", findErr)
		}
		if session == nil {
			return errors.New("webhook source payment lookup returned nil")
		}
		if !MoneyEventTypesEquivalent(row.EventType, session.WebhookEvent) {
			return nil
		}
		return b.Payments.MarkWebhookAttempt(ctx, *row.PaymentID, delivered, err)
	}
	return nil
}

func (b DeliveryBoundary) logAttempt(ctx context.Context, row models.WebhookDelivery, delivered bool, err error) {
	logger := b.Logger
	if logger == nil {
		logger = slog.Default()
	}
	level := slog.LevelInfo
	status := "succeeded"
	if !delivered {
		level = slog.LevelWarn
		status = "failed"
	}
	logger.LogAttrs(ctx, level, "webhook_delivery_attempt",
		slog.String("delivery_id", row.ID.String()),
		slog.String("event_id", row.EventID),
		slog.String("event_type", row.EventType),
		slog.String("event_version", firstNonEmpty(row.EventVersion, constants.WebhookEventVersionV1)),
		slog.String("merchant_id", row.MerchantID.String()),
		slog.String("domain_id", row.DomainID.String()),
		slog.String("entity_type", row.EntityType),
		slog.String("status", status),
		slog.String("failure_category", FailureCategory(err)),
	)
}

func (b DeliveryBoundary) logSourceProjectionError(ctx context.Context, row models.WebhookDelivery, err error) {
	logger := b.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.WarnContext(ctx, "webhook_source_projection_failed",
		slog.String("delivery_id", row.ID.String()),
		slog.String("event_id", row.EventID),
		slog.String("event_type", row.EventType),
		slog.String("failure_category", FailureCategory(err)),
	)
}

func (b DeliveryBoundary) timeout() time.Duration {
	if b.DeliveryTimeout > 0 {
		return b.DeliveryTimeout
	}
	return 20 * time.Second
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// WebhookDeliveryBoundary preserves the function-based API used by older tests and callers.
// DeliveryBoundary is the canonical sender implementation.
type WebhookDeliveryBoundary struct {
	Queue    any
	Notifier DeliveryProcessorNotifier

	FindDomain      func(context.Context, uuid.UUID) (*models.Domain, error)
	FindTransaction func(context.Context, uuid.UUID) (*models.Transaction, error)
	FindPayment     func(context.Context, uuid.UUID) (*models.PaymentSession, error)

	MarkTransactionAttempt func(context.Context, string, bool, error) error
	MarkPaymentAttempt     func(context.Context, uuid.UUID, bool, error) error

	ClaimLock       time.Duration
	DeliveryTimeout time.Duration
}

func (b WebhookDeliveryBoundary) RunOnce(ctx context.Context, limit int) (DeliveryRunSummary, error) {
	return b.boundary().DeliverDue(ctx, limit)
}

func (b WebhookDeliveryBoundary) DeliverOne(ctx context.Context, row models.WebhookDelivery) error {
	return b.boundary().DeliverOne(ctx, row)
}

func (b WebhookDeliveryBoundary) boundary() DeliveryBoundary {
	return DeliveryBoundary{
		Queue:           webhookDeliveryBoundaryQueue{queue: b.Queue, claimLock: b.ClaimLock},
		Domains:         domainLookupFunc(b.FindDomain),
		Transactions:    transactionStoreFuncs{find: b.FindTransaction, mark: b.MarkTransactionAttempt},
		Payments:        paymentStoreFuncs{find: b.FindPayment, mark: b.MarkPaymentAttempt},
		Notifier:        b.Notifier,
		DeliveryTimeout: b.DeliveryTimeout,
	}
}

type webhookDeliveryBoundaryQueue struct {
	queue     any
	claimLock time.Duration
}

func (q webhookDeliveryBoundaryQueue) ClaimDue(ctx context.Context, limit int) ([]models.WebhookDelivery, error) {
	if q.queue == nil {
		return nil, permanent(errors.New("webhook delivery queue is not configured"))
	}
	if queue, ok := q.queue.(interface {
		ClaimDue(context.Context, int, time.Duration) ([]models.WebhookDelivery, error)
	}); ok {
		lock := q.claimLock
		if lock <= 0 {
			lock = 2 * time.Minute
		}
		return queue.ClaimDue(ctx, limit, lock)
	}
	if queue, ok := q.queue.(interface {
		ClaimDue(context.Context, int) ([]models.WebhookDelivery, error)
	}); ok {
		return queue.ClaimDue(ctx, limit)
	}
	return nil, permanent(errors.New("webhook delivery queue has unsupported claim interface"))
}

func (q webhookDeliveryBoundaryQueue) MarkAttempt(ctx context.Context, id, leaseToken uuid.UUID, delivered bool, err error) error {
	if q.queue == nil {
		return nil
	}
	if queue, ok := q.queue.(interface {
		MarkAttempt(context.Context, uuid.UUID, uuid.UUID, bool, error) error
	}); ok {
		return queue.MarkAttempt(ctx, id, leaseToken, delivered, err)
	}
	return permanent(errors.New("webhook delivery queue has unsupported attempt interface"))
}
