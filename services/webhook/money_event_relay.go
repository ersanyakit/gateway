package webhook

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"core/models"
	"core/types"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// MoneyEventRelayQueue is the durable source side of the transactional outbox
// bridge. Delivered means that a second durable webhook_deliveries row exists;
// it does not mean the external HTTP/NATS destination has acknowledged it yet.
type MoneyEventRelayQueue interface {
	ClaimDueForNotifications(context.Context, int, time.Duration) ([]models.MoneyEventOutbox, error)
	MarkRelayAttempt(context.Context, uuid.UUID, uuid.UUID, bool, error) error
}

type MoneyEventDeliveryQueue interface {
	EnqueueMoneyEvent(context.Context, models.Domain, models.MoneyEventOutbox) (*models.WebhookDelivery, bool, error)
}

type MoneyEventRelay struct {
	Queue      MoneyEventRelayQueue
	Deliveries MoneyEventDeliveryQueue
	Domains    DeliveryDomainLookup
	ClaimLock  time.Duration
}

type MoneyEventRelaySummary struct {
	Claimed int
	Relayed int
	Failed  int
}

func (r MoneyEventRelay) RunOnce(ctx context.Context, limit int) (MoneyEventRelaySummary, error) {
	if r.Queue == nil {
		return MoneyEventRelaySummary{}, errors.New("money event outbox queue is not configured")
	}
	if r.Deliveries == nil {
		return MoneyEventRelaySummary{}, errors.New("money event delivery queue is not configured")
	}
	if r.Domains == nil {
		return MoneyEventRelaySummary{}, errors.New("money event domain lookup is not configured")
	}

	rows, err := r.Queue.ClaimDueForNotifications(ctx, limit, r.ClaimLock)
	if err != nil {
		return MoneyEventRelaySummary{}, err
	}
	summary := MoneyEventRelaySummary{Claimed: len(rows)}
	rowErrors := make([]error, 0)
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return summary, errors.Join(append(rowErrors, err)...)
		}
		if row.LeaseToken == nil || *row.LeaseToken == uuid.Nil {
			summary.Failed++
			rowErrors = append(rowErrors, fmt.Errorf("relay money event %s: claimed row has no lease token", row.EventID))
			continue
		}
		relayErr := r.relayOne(ctx, row)
		if relayErr != nil {
			summary.Failed++
			rowErrors = append(rowErrors, fmt.Errorf("relay money event %s: %w", row.EventID, relayErr))
		} else {
			summary.Relayed++
		}
		if markErr := r.Queue.MarkRelayAttempt(ctx, row.ID, *row.LeaseToken, relayErr == nil, relayErr); markErr != nil {
			if relayErr == nil {
				summary.Failed++
				summary.Relayed--
			}
			rowErrors = append(rowErrors, fmt.Errorf("mark money event %s relay attempt: %w", row.EventID, markErr))
		}
	}
	return summary, errors.Join(rowErrors...)
}

func (r MoneyEventRelay) relayOne(ctx context.Context, row models.MoneyEventOutbox) error {
	domainID := row.DomainID.String()
	domain, err := r.Domains.FindByID(types.DomainParams{Context: ctx, DomainID: &domainID})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return permanent(fmt.Errorf("notification domain %s not found: %w", row.DomainID, err))
		}
		return fmt.Errorf("load notification domain %s: %w", row.DomainID, err)
	}
	if domain == nil {
		return permanent(fmt.Errorf("notification domain %s lookup returned nil", row.DomainID))
	}
	if domain.ID != row.DomainID || domain.MerchantID != row.MerchantID {
		return permanent(errors.New("money event notification scope does not match domain"))
	}
	if !configuredNotificationTarget(*domain) {
		return permanent(errors.New("notification target is not configured"))
	}

	delivery, _, err := r.Deliveries.EnqueueMoneyEvent(ctx, *domain, row)
	if err != nil {
		return err
	}
	if delivery == nil || delivery.ID == uuid.Nil {
		return errors.New("money event delivery enqueue returned no durable row")
	}
	return nil
}

func configuredNotificationTarget(domain models.Domain) bool {
	if domain.UsesNATS() {
		return strings.TrimSpace(domain.NATSURL) != ""
	}
	return strings.TrimSpace(domain.WebhookURL) != "" && strings.TrimSpace(domain.WebhookSecret) != ""
}
