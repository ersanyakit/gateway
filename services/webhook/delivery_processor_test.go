package webhook

import (
	"context"
	"errors"
	"testing"
	"time"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
)

type deliveryProcessorFakeRepo struct {
	rows  []models.WebhookDelivery
	marks []deliveryProcessorMark
}

type deliveryProcessorMark struct {
	id        uuid.UUID
	delivered bool
	err       error
}

func (r *deliveryProcessorFakeRepo) ClaimDue(_ context.Context, _ int) ([]models.WebhookDelivery, error) {
	return append([]models.WebhookDelivery(nil), r.rows...), nil
}

func (r *deliveryProcessorFakeRepo) MarkAttempt(_ context.Context, id uuid.UUID, delivered bool, err error) error {
	r.marks = append(r.marks, deliveryProcessorMark{id: id, delivered: delivered, err: err})
	return nil
}

type deliveryProcessorLockingRepo struct {
	rows      []models.WebhookDelivery
	claimLock time.Duration
}

func (r *deliveryProcessorLockingRepo) ClaimDue(_ context.Context, _ int, lockFor time.Duration) ([]models.WebhookDelivery, error) {
	r.claimLock = lockFor
	return append([]models.WebhookDelivery(nil), r.rows...), nil
}

func (r *deliveryProcessorLockingRepo) MarkAttempt(context.Context, uuid.UUID, bool, error) error {
	return nil
}

type deliveryProcessorThreeArgClaimRepo struct {
	deliveryProcessorFakeRepo
	claimLockSupported bool
}

func (r *deliveryProcessorThreeArgClaimRepo) ClaimDue(_ context.Context, _ int, _ time.Duration) ([]models.WebhookDelivery, error) {
	r.claimLockSupported = true
	return append([]models.WebhookDelivery(nil), r.rows...), nil
}

type deliveryProcessorFakeNotifier struct {
	err             error
	transactionSent int
	paymentSent     int
	rawSent         int
}

func (n *deliveryProcessorFakeNotifier) Deliver(context.Context, models.Domain, models.Transaction) error {
	n.transactionSent++
	return n.err
}

func (n *deliveryProcessorFakeNotifier) DeliverPayment(context.Context, models.Domain, models.PaymentSession) error {
	n.paymentSent++
	return n.err
}

func (n *deliveryProcessorFakeNotifier) DeliverRaw(context.Context, models.Domain, string, string, string, []byte) error {
	n.rawSent++
	return n.err
}

func TestDeliveryProcessorDeliversClaimedRowsAndMarksSources(t *testing.T) {
	domainID := uuid.New()
	transactionID := uuid.New()
	paymentID := uuid.New()
	repo := &deliveryProcessorFakeRepo{rows: []models.WebhookDelivery{
		{
			ID:            uuid.New(),
			DomainID:      domainID,
			TransactionID: &transactionID,
			EventID:       "tx:event",
			EventType:     constants.WebhookEventNativeTransfer,
			EventVersion:  constants.WebhookEventVersionV1,
		},
		{
			ID:           uuid.New(),
			DomainID:     domainID,
			PaymentID:    &paymentID,
			EventID:      "payment:event",
			EventType:    constants.WebhookEventPaymentSucceeded,
			EventVersion: constants.WebhookEventVersionV1,
		},
		{
			ID:           uuid.New(),
			DomainID:     domainID,
			EventID:      "lifecycle:event",
			EventType:    constants.WebhookEventPayoutFinalizedV1,
			EventVersion: constants.WebhookEventVersionV1,
			PayloadJSON:  `{"event_id":"lifecycle:event"}`,
		},
	}}
	notifier := &deliveryProcessorFakeNotifier{}
	var transactionMarked bool
	var paymentMarked bool
	processor := DeliveryProcessor{
		DeliveryRepo: repo,
		Notifier:     notifier,
		DomainLookup: func(context.Context, uuid.UUID) (*models.Domain, error) {
			return &models.Domain{ID: domainID, WebhookURL: "http://127.0.0.1/webhook"}, nil
		},
		TransactionLookup: func(context.Context, uuid.UUID) (*models.Transaction, error) {
			return &models.Transaction{ID: transactionID, UniqueHash: "tx-unique", EventType: constants.WebhookEventNativeTransfer}, nil
		},
		PaymentLookup: func(context.Context, uuid.UUID) (*models.PaymentSession, error) {
			return &models.PaymentSession{ID: paymentID, WebhookEvent: constants.WebhookEventPaymentSucceeded}, nil
		},
		MarkTransactionAttempt: func(_ context.Context, uniqueHash string, delivered bool, err error) error {
			transactionMarked = uniqueHash == "tx-unique" && delivered && err == nil
			return nil
		},
		MarkPaymentAttempt: func(_ context.Context, id uuid.UUID, delivered bool, err error) error {
			paymentMarked = id == paymentID && delivered && err == nil
			return nil
		},
	}

	stats, err := processor.ProcessDue(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Claimed != 3 || stats.Delivered != 3 || stats.Failed != 0 {
		t.Fatalf("stats = %#v", stats)
	}
	if notifier.transactionSent != 1 || notifier.paymentSent != 1 || notifier.rawSent != 1 {
		t.Fatalf("notifier sends tx=%d payment=%d raw=%d", notifier.transactionSent, notifier.paymentSent, notifier.rawSent)
	}
	if len(repo.marks) != 3 {
		t.Fatalf("delivery marks = %d, want 3", len(repo.marks))
	}
	for _, mark := range repo.marks {
		if !mark.delivered || mark.err != nil {
			t.Fatalf("delivery mark = %#v, want delivered success", mark)
		}
	}
	if !transactionMarked || !paymentMarked {
		t.Fatalf("source marks transaction=%v payment=%v", transactionMarked, paymentMarked)
	}
}

func TestDeliveryProcessorRecordsTransientFailure(t *testing.T) {
	paymentID := uuid.New()
	failure := errors.New("timeout")
	repo := &deliveryProcessorFakeRepo{rows: []models.WebhookDelivery{{
		ID:           uuid.New(),
		DomainID:     uuid.New(),
		PaymentID:    &paymentID,
		EventID:      "payment:event",
		EventType:    constants.WebhookEventPaymentSucceeded,
		EventVersion: constants.WebhookEventVersionV1,
	}}}
	notifier := &deliveryProcessorFakeNotifier{err: failure}
	var paymentMarkedFailure bool
	processor := DeliveryProcessor{
		DeliveryRepo: repo,
		Notifier:     notifier,
		DomainLookup: func(context.Context, uuid.UUID) (*models.Domain, error) {
			return &models.Domain{ID: uuid.New(), WebhookURL: "http://127.0.0.1/webhook"}, nil
		},
		PaymentLookup: func(context.Context, uuid.UUID) (*models.PaymentSession, error) {
			return &models.PaymentSession{ID: paymentID, WebhookEvent: constants.WebhookEventPaymentSucceeded}, nil
		},
		MarkPaymentAttempt: func(_ context.Context, id uuid.UUID, delivered bool, err error) error {
			paymentMarkedFailure = id == paymentID && !delivered && errors.Is(err, failure)
			return nil
		},
	}

	stats, err := processor.ProcessDue(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Claimed != 1 || stats.Delivered != 0 || stats.Failed != 1 {
		t.Fatalf("stats = %#v", stats)
	}
	if len(repo.marks) != 1 || repo.marks[0].delivered || !errors.Is(repo.marks[0].err, failure) {
		t.Fatalf("delivery marks = %#v", repo.marks)
	}
	if !paymentMarkedFailure {
		t.Fatal("payment source attempt was not marked failed")
	}
}

func TestDeliveryProcessorSupportsLockingClaimRepos(t *testing.T) {
	repo := &deliveryProcessorLockingRepo{}
	processor := DeliveryProcessor{
		DeliveryRepo: repo,
		Notifier:     &deliveryProcessorFakeNotifier{},
	}

	stats, err := processor.ProcessDue(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Claimed != 0 {
		t.Fatalf("stats = %#v", stats)
	}
	if repo.claimLock != 0 {
		t.Fatalf("claim lock = %s, want repository default sentinel 0", repo.claimLock)
	}
}

func TestDeliveryProcessorSupportsRepositoryClaimLockSignature(t *testing.T) {
	repo := &deliveryProcessorThreeArgClaimRepo{}
	processor := DeliveryProcessor{
		DeliveryRepo: repo,
		Notifier:     &deliveryProcessorFakeNotifier{},
	}

	stats, err := processor.ProcessDue(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Claimed != 0 || !repo.claimLockSupported {
		t.Fatalf("stats=%#v claimLockSupported=%v", stats, repo.claimLockSupported)
	}
}
