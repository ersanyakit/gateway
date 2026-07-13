package webhook

import (
	"context"
	"errors"
	"time"

	"core/models"
	"core/types"

	"github.com/google/uuid"
)

type DeliveryProcessorRepo interface {
	MarkAttempt(ctx context.Context, id uuid.UUID, delivered bool, err error) error
}

type DeliveryProcessorNotifier interface {
	Deliver(ctx context.Context, domain models.Domain, tx models.Transaction) error
	DeliverPayment(ctx context.Context, domain models.Domain, session models.PaymentSession) error
	DeliverRaw(ctx context.Context, domain models.Domain, eventType, eventID, eventVersion string, body []byte) error
}

type DeliveryProcessorStats struct {
	Claimed   int
	Delivered int
	Failed    int
}

type DeliveryProcessor struct {
	DeliveryRepo any
	Notifier     DeliveryProcessorNotifier

	DomainLookup      func(context.Context, uuid.UUID) (*models.Domain, error)
	TransactionLookup func(context.Context, uuid.UUID) (*models.Transaction, error)
	PaymentLookup     func(context.Context, uuid.UUID) (*models.PaymentSession, error)

	MarkTransactionAttempt func(context.Context, string, bool, error) error
	MarkPaymentAttempt     func(context.Context, uuid.UUID, bool, error) error

	DeliveryTimeout time.Duration
}

func (p DeliveryProcessor) ProcessDue(ctx context.Context, limit int) (DeliveryProcessorStats, error) {
	if p.DeliveryRepo == nil {
		return DeliveryProcessorStats{}, permanent(errors.New("webhook delivery repository is not configured"))
	}
	summary, err := DeliveryBoundary{
		Queue:           deliveryProcessorQueue{queue: p.DeliveryRepo},
		Domains:         domainLookupFunc(p.DomainLookup),
		Transactions:    transactionStoreFuncs{find: p.TransactionLookup, mark: p.MarkTransactionAttempt},
		Payments:        paymentStoreFuncs{find: p.PaymentLookup, mark: p.MarkPaymentAttempt},
		Notifier:        p.Notifier,
		DeliveryTimeout: p.DeliveryTimeout,
	}.DeliverDue(ctx, limit)
	return DeliveryProcessorStats(summary), err
}

type deliveryProcessorQueue struct {
	queue any
}

func (q deliveryProcessorQueue) ClaimDue(ctx context.Context, limit int) ([]models.WebhookDelivery, error) {
	if queue, ok := q.queue.(interface {
		ClaimDue(context.Context, int, time.Duration) ([]models.WebhookDelivery, error)
	}); ok {
		return queue.ClaimDue(ctx, limit, 0)
	}
	if queue, ok := q.queue.(interface {
		ClaimDue(context.Context, int) ([]models.WebhookDelivery, error)
	}); ok {
		return queue.ClaimDue(ctx, limit)
	}
	return nil, permanent(errors.New("webhook delivery queue has unsupported claim interface"))
}

func (q deliveryProcessorQueue) MarkAttempt(ctx context.Context, id uuid.UUID, delivered bool, err error) error {
	if queue, ok := q.queue.(interface {
		MarkAttempt(context.Context, uuid.UUID, bool, error) error
	}); ok {
		return queue.MarkAttempt(ctx, id, delivered, err)
	}
	return nil
}

type domainLookupFunc func(context.Context, uuid.UUID) (*models.Domain, error)

func (f domainLookupFunc) FindByID(params types.DomainParams) (*models.Domain, error) {
	if f == nil {
		return nil, errors.New("domain lookup is not configured")
	}
	if params.DomainID == nil || *params.DomainID == "" {
		return nil, errors.New("domain id is required")
	}
	domainID, err := uuid.Parse(*params.DomainID)
	if err != nil {
		return nil, err
	}
	return f(params.Context, domainID)
}

type transactionStoreFuncs struct {
	find func(context.Context, uuid.UUID) (*models.Transaction, error)
	mark func(context.Context, string, bool, error) error
}

func (f transactionStoreFuncs) FindByID(ctx context.Context, id uuid.UUID) (*models.Transaction, error) {
	if f.find == nil {
		return nil, errors.New("transaction lookup is not configured")
	}
	return f.find(ctx, id)
}

func (f transactionStoreFuncs) MarkWebhookAttempt(ctx context.Context, uniqueHash string, delivered bool, lastErr error) error {
	if f.mark == nil {
		return nil
	}
	return f.mark(ctx, uniqueHash, delivered, lastErr)
}

type paymentStoreFuncs struct {
	find func(context.Context, uuid.UUID) (*models.PaymentSession, error)
	mark func(context.Context, uuid.UUID, bool, error) error
}

func (f paymentStoreFuncs) FindByID(ctx context.Context, id uuid.UUID) (*models.PaymentSession, error) {
	if f.find == nil {
		return nil, errors.New("payment lookup is not configured")
	}
	return f.find(ctx, id)
}

func (f paymentStoreFuncs) MarkWebhookAttempt(ctx context.Context, id uuid.UUID, delivered bool, lastErr error) error {
	if f.mark == nil {
		return nil
	}
	return f.mark(ctx, id, delivered, lastErr)
}
