package webhook

import (
	"context"
	"errors"
	"testing"
	"time"

	"core/models"
	"core/types"

	"github.com/google/uuid"
)

type moneyRelayQueueFake struct {
	rows      []models.MoneyEventOutbox
	claimLock time.Duration
	marks     []moneyRelayMark
}

type moneyRelayMark struct {
	id         uuid.UUID
	leaseToken uuid.UUID
	delivered  bool
	err        error
}

func (f *moneyRelayQueueFake) ClaimDueForNotifications(_ context.Context, _ int, lockFor time.Duration) ([]models.MoneyEventOutbox, error) {
	f.claimLock = lockFor
	return append([]models.MoneyEventOutbox(nil), f.rows...), nil
}

func (f *moneyRelayQueueFake) MarkRelayAttempt(_ context.Context, id, leaseToken uuid.UUID, delivered bool, err error) error {
	f.marks = append(f.marks, moneyRelayMark{id: id, leaseToken: leaseToken, delivered: delivered, err: err})
	return nil
}

type moneyRelayDeliveryFake struct {
	failEvent string
	enqueued  []string
}

func (f *moneyRelayDeliveryFake) EnqueueMoneyEvent(_ context.Context, _ models.Domain, event models.MoneyEventOutbox) (*models.WebhookDelivery, bool, error) {
	if event.EventID == f.failEvent {
		return nil, false, errors.New("enqueue unavailable")
	}
	f.enqueued = append(f.enqueued, event.EventID)
	return &models.WebhookDelivery{ID: uuid.New(), EventID: event.EventID}, true, nil
}

type moneyRelayDomainLookupFake struct {
	domain models.Domain
	err    error
}

func (f moneyRelayDomainLookupFake) FindByID(types.DomainParams) (*models.Domain, error) {
	if f.err != nil {
		return nil, f.err
	}
	domain := f.domain
	return &domain, nil
}

func TestMoneyEventRelayCreatesDurableDeliveriesBeforeClosingOutbox(t *testing.T) {
	merchantID := uuid.New()
	domainID := uuid.New()
	firstLease := uuid.New()
	secondLease := uuid.New()
	first := models.MoneyEventOutbox{ID: uuid.New(), EventID: "evt-1", MerchantID: merchantID, DomainID: domainID, LeaseToken: &firstLease}
	second := models.MoneyEventOutbox{ID: uuid.New(), EventID: "evt-2", MerchantID: merchantID, DomainID: domainID, LeaseToken: &secondLease}
	queue := &moneyRelayQueueFake{rows: []models.MoneyEventOutbox{first, second}}
	deliveries := &moneyRelayDeliveryFake{failEvent: second.EventID}
	relay := MoneyEventRelay{
		Queue:      queue,
		Deliveries: deliveries,
		Domains: moneyRelayDomainLookupFake{domain: models.Domain{
			ID: domainID, MerchantID: merchantID, NotificationMode: models.DomainNotificationNATS, NATSURL: "nats://127.0.0.1:4222",
		}},
		ClaimLock: 45 * time.Second,
	}

	summary, err := relay.RunOnce(context.Background(), 10)
	if err == nil {
		t.Fatal("row enqueue failure should be reported")
	}
	if summary.Claimed != 2 || summary.Relayed != 1 || summary.Failed != 1 {
		t.Fatalf("summary = %#v", summary)
	}
	if queue.claimLock != 45*time.Second {
		t.Fatalf("claim lock = %s", queue.claimLock)
	}
	if len(deliveries.enqueued) != 1 || deliveries.enqueued[0] != first.EventID {
		t.Fatalf("enqueued = %#v", deliveries.enqueued)
	}
	if len(queue.marks) != 2 || queue.marks[0].leaseToken != firstLease || queue.marks[1].leaseToken != secondLease || !queue.marks[0].delivered || queue.marks[0].err != nil || queue.marks[1].delivered || queue.marks[1].err == nil {
		t.Fatalf("relay marks = %#v", queue.marks)
	}
}

func TestMoneyEventRelayRejectsCrossTenantDomain(t *testing.T) {
	leaseToken := uuid.New()
	row := models.MoneyEventOutbox{ID: uuid.New(), EventID: "evt-scope", MerchantID: uuid.New(), DomainID: uuid.New(), LeaseToken: &leaseToken}
	queue := &moneyRelayQueueFake{rows: []models.MoneyEventOutbox{row}}
	relay := MoneyEventRelay{
		Queue:      queue,
		Deliveries: &moneyRelayDeliveryFake{},
		Domains: moneyRelayDomainLookupFake{domain: models.Domain{
			ID: row.DomainID, MerchantID: uuid.New(), WebhookURL: "https://merchant.example/webhook", WebhookSecret: "encrypted",
		}},
	}

	summary, err := relay.RunOnce(context.Background(), 1)
	if err == nil || summary.Failed != 1 || len(queue.marks) != 1 || queue.marks[0].delivered || !IsPermanent(queue.marks[0].err) {
		t.Fatalf("summary=%#v err=%v marks=%#v", summary, err, queue.marks)
	}
}

func TestMoneyEventRelayRefusesClaimWithoutLeaseToken(t *testing.T) {
	row := models.MoneyEventOutbox{ID: uuid.New(), EventID: "evt-missing-lease", MerchantID: uuid.New(), DomainID: uuid.New()}
	queue := &moneyRelayQueueFake{rows: []models.MoneyEventOutbox{row}}
	deliveries := &moneyRelayDeliveryFake{}
	relay := MoneyEventRelay{
		Queue:      queue,
		Deliveries: deliveries,
		Domains: moneyRelayDomainLookupFake{domain: models.Domain{
			ID: row.DomainID, MerchantID: row.MerchantID, WebhookURL: "https://merchant.example/webhook", WebhookSecret: "encrypted",
		}},
	}

	summary, err := relay.RunOnce(context.Background(), 1)
	if err == nil || summary.Claimed != 1 || summary.Failed != 1 || summary.Relayed != 0 {
		t.Fatalf("summary=%#v err=%v", summary, err)
	}
	if len(deliveries.enqueued) != 0 || len(queue.marks) != 0 {
		t.Fatalf("missing lease claim escaped to delivery/mark: enqueued=%#v marks=%#v", deliveries.enqueued, queue.marks)
	}
}
