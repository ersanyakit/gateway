package webhook

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"core/constants"
	"core/models"
	"core/types"

	"github.com/google/uuid"
)

type DeliveryQueue interface {
	ClaimDue(ctx context.Context, limit int) ([]models.WebhookDelivery, error)
	MarkAttempt(ctx context.Context, id uuid.UUID, delivered bool, lastErr error) error
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
	err := b.deliverOne(ctx, row)
	delivered := err == nil
	safeErr := RedactDeliveryError(err)
	if markErr := b.markDeliveryAttempt(ctx, row.ID, delivered, safeErr); markErr != nil && err == nil {
		err = markErr
		delivered = false
		safeErr = RedactDeliveryError(err)
	}
	b.markSourceAttempt(ctx, row, delivered, safeErr)
	b.logAttempt(ctx, row, delivered, err)
	return err
}

func (b DeliveryBoundary) deliverOne(ctx context.Context, row models.WebhookDelivery) error {
	if b.Notifier == nil {
		return permanent(errors.New("webhook notifier is not configured"))
	}
	domain, err := b.domain(ctx, row.DomainID)
	if err != nil {
		return permanent(err)
	}

	deliveryCtx := ctx
	cancel := func() {}
	if timeout := b.timeout(); timeout > 0 {
		deliveryCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	switch {
	case row.TransactionID != nil:
		if b.Transactions == nil {
			return permanent(errors.New("transaction repository is not configured"))
		}
		txModel, err := b.Transactions.FindByID(ctx, *row.TransactionID)
		if err != nil {
			return permanent(err)
		}
		if txModel == nil {
			return permanent(errors.New("transaction lookup returned nil"))
		}
		return b.Notifier.Deliver(deliveryCtx, *domain, *txModel)
	case row.PaymentID != nil:
		if b.Payments == nil {
			return permanent(errors.New("payment repository is not configured"))
		}
		session, err := b.Payments.FindByID(ctx, *row.PaymentID)
		if err != nil {
			return permanent(err)
		}
		if session == nil {
			return permanent(errors.New("payment lookup returned nil"))
		}
		return b.Notifier.DeliverPayment(deliveryCtx, *domain, *session)
	case strings.TrimSpace(row.PayloadJSON) != "":
		eventVersion := row.EventVersion
		if eventVersion == "" {
			eventVersion = constants.WebhookEventVersionV1
		}
		return b.Notifier.DeliverRaw(deliveryCtx, *domain, row.EventType, row.EventID, eventVersion, []byte(row.PayloadJSON))
	default:
		return permanent(errors.New("delivery row has no transaction, payment, or lifecycle payload"))
	}
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

func (b DeliveryBoundary) markDeliveryAttempt(ctx context.Context, id uuid.UUID, delivered bool, err error) error {
	if b.Queue == nil || id == uuid.Nil {
		return nil
	}
	return b.Queue.MarkAttempt(ctx, id, delivered, err)
}

func (b DeliveryBoundary) markSourceAttempt(ctx context.Context, row models.WebhookDelivery, delivered bool, err error) {
	if row.TransactionID != nil && b.Transactions != nil {
		if txModel, findErr := b.Transactions.FindByID(ctx, *row.TransactionID); findErr == nil && txModel != nil && txModel.UniqueHash != "" {
			_ = b.Transactions.MarkWebhookAttempt(ctx, txModel.UniqueHash, delivered, err)
		}
		return
	}
	if row.PaymentID != nil && b.Payments != nil {
		_ = b.Payments.MarkWebhookAttempt(ctx, *row.PaymentID, delivered, err)
	}
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

func (q webhookDeliveryBoundaryQueue) MarkAttempt(ctx context.Context, id uuid.UUID, delivered bool, err error) error {
	if q.queue == nil {
		return nil
	}
	if queue, ok := q.queue.(interface {
		MarkAttempt(context.Context, uuid.UUID, bool, error) error
	}); ok {
		return queue.MarkAttempt(ctx, id, delivered, err)
	}
	return nil
}
