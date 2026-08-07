package webhook

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"core/models"
	"core/types"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type failingDeliveryPaymentStore struct {
	markErr error
}

type recordingDeliveryQueue struct {
	marked     bool
	leaseToken uuid.UUID
	delivered  bool
	err        error
	markErr    error
}

func (*recordingDeliveryQueue) ClaimDue(context.Context, int) ([]models.WebhookDelivery, error) {
	return nil, nil
}

func (q *recordingDeliveryQueue) MarkAttempt(_ context.Context, _ uuid.UUID, leaseToken uuid.UUID, delivered bool, err error) error {
	q.marked = true
	q.leaseToken = leaseToken
	q.delivered = delivered
	q.err = err
	return q.markErr
}

func TestDeliveryBoundaryLeaseLossDoesNotProjectStaleResultToSource(t *testing.T) {
	domain := &models.Domain{ID: uuid.New(), MerchantID: uuid.New()}
	paymentID := uuid.New()
	leaseLost := errors.New("lease lost")
	queue := &recordingDeliveryQueue{markErr: leaseLost}
	payments := &countingDeliveryPaymentStore{eventType: "payment.succeeded.v1"}
	boundary := DeliveryBoundary{
		Queue:    queue,
		Domains:  staticDeliveryDomainLookup{domain: domain},
		Payments: payments,
		Notifier: &rawRecordingDeliveryNotifier{},
	}
	row := models.WebhookDelivery{
		ID:          uuid.New(),
		LeaseToken:  newWebhookLeaseToken(),
		MerchantID:  domain.MerchantID,
		DomainID:    domain.ID,
		PaymentID:   &paymentID,
		EventID:     "payment:event:stale-worker",
		EventType:   "payment.succeeded.v1",
		PayloadJSON: `{"event_id":"payment:event:stale-worker"}`,
	}

	err := boundary.DeliverOne(context.Background(), row)
	if !errors.Is(err, leaseLost) {
		t.Fatalf("delivery error = %v, want lease loss", err)
	}
	if payments.markCalls != 0 {
		t.Fatalf("stale worker projected %d source attempts, want 0", payments.markCalls)
	}
}

func TestDeliveryBoundaryRejectsUnleasedRowBeforeExternalSend(t *testing.T) {
	domain := &models.Domain{ID: uuid.New(), MerchantID: uuid.New()}
	queue := &recordingDeliveryQueue{}
	notifier := &rawRecordingDeliveryNotifier{}
	boundary := DeliveryBoundary{
		Queue:    queue,
		Domains:  staticDeliveryDomainLookup{domain: domain},
		Notifier: notifier,
	}
	row := models.WebhookDelivery{
		ID:          uuid.New(),
		MerchantID:  domain.MerchantID,
		DomainID:    domain.ID,
		EventID:     "unleased:event",
		EventType:   "payment.succeeded.v1",
		PayloadJSON: `{"event_id":"unleased:event"}`,
	}

	if err := boundary.DeliverOne(context.Background(), row); err == nil {
		t.Fatal("unleased row was accepted")
	}
	if notifier.rawCalls != 0 || queue.marked {
		t.Fatalf("unleased row side effects notifier=%d marked=%v, want none", notifier.rawCalls, queue.marked)
	}
}

func (s failingDeliveryPaymentStore) FindByID(context.Context, uuid.UUID) (*models.PaymentSession, error) {
	return &models.PaymentSession{WebhookEvent: "payment_succeeded"}, nil
}

func (s failingDeliveryPaymentStore) MarkWebhookAttempt(context.Context, uuid.UUID, bool, error) error {
	return s.markErr
}

func TestMarkSourceAttemptPropagatesPaymentPersistenceError(t *testing.T) {
	paymentID := uuid.New()
	sentinel := errors.New("payment state unavailable")
	boundary := DeliveryBoundary{Payments: failingDeliveryPaymentStore{markErr: sentinel}}

	err := boundary.markSourceAttempt(context.Background(), models.WebhookDelivery{PaymentID: &paymentID, EventType: "payment.succeeded.v1"}, true, nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want payment persistence error", err)
	}
}

func TestDeliveryBoundarySourceProjectionFailureDoesNotRetryAcknowledgedDelivery(t *testing.T) {
	domain := &models.Domain{ID: uuid.New(), MerchantID: uuid.New()}
	paymentID := uuid.New()
	queue := &recordingDeliveryQueue{}
	boundary := DeliveryBoundary{
		Queue:    queue,
		Domains:  staticDeliveryDomainLookup{domain: domain},
		Payments: failingDeliveryPaymentStore{markErr: errors.New("source database unavailable")},
		Notifier: &rawRecordingDeliveryNotifier{},
	}
	row := models.WebhookDelivery{
		ID:          uuid.New(),
		LeaseToken:  newWebhookLeaseToken(),
		MerchantID:  domain.MerchantID,
		DomainID:    domain.ID,
		PaymentID:   &paymentID,
		EventID:     "payment:event",
		EventType:   "payment.succeeded.v1",
		PayloadJSON: `{"event_id":"payment:event"}`,
	}

	if err := boundary.DeliverOne(context.Background(), row); err != nil {
		t.Fatalf("acknowledged delivery returned source projection error: %v", err)
	}
	if !queue.marked || queue.leaseToken != *row.LeaseToken || !queue.delivered || queue.err != nil {
		t.Fatalf("durable mark = marked:%v token:%s delivered:%v err:%v, want claimed-token success", queue.marked, queue.leaseToken, queue.delivered, queue.err)
	}
}

type staticDeliveryDomainLookup struct {
	domain *models.Domain
	err    error
}

func (s staticDeliveryDomainLookup) FindByID(types.DomainParams) (*models.Domain, error) {
	return s.domain, s.err
}

type countingDeliveryPaymentStore struct {
	findCalls int
	markCalls int
	findErr   error
	eventType string
}

func (s *countingDeliveryPaymentStore) FindByID(context.Context, uuid.UUID) (*models.PaymentSession, error) {
	s.findCalls++
	if s.findErr != nil {
		return nil, s.findErr
	}
	return &models.PaymentSession{WebhookEvent: s.eventType}, nil
}

func (s *countingDeliveryPaymentStore) MarkWebhookAttempt(context.Context, uuid.UUID, bool, error) error {
	s.markCalls++
	return nil
}

type countingDeliveryTransactionStore struct {
	eventType string
	findCalls int
	markCalls int
}

func (s *countingDeliveryTransactionStore) FindByID(context.Context, uuid.UUID) (*models.Transaction, error) {
	s.findCalls++
	return &models.Transaction{UniqueHash: "transaction-unique-hash", EventType: s.eventType}, nil
}

func (s *countingDeliveryTransactionStore) MarkWebhookAttempt(context.Context, string, bool, error) error {
	s.markCalls++
	return nil
}

type rawRecordingDeliveryNotifier struct {
	rawCalls     int
	paymentCalls int
	body         string
}

type targetRecordingDeliveryNotifier struct {
	domain models.Domain
}

func (n *targetRecordingDeliveryNotifier) Deliver(_ context.Context, domain models.Domain, _ models.Transaction) error {
	n.domain = domain
	return nil
}

func (n *targetRecordingDeliveryNotifier) DeliverPayment(_ context.Context, domain models.Domain, _ models.PaymentSession) error {
	n.domain = domain
	return nil
}

func (n *targetRecordingDeliveryNotifier) DeliverRaw(_ context.Context, domain models.Domain, _, _, _ string, _ []byte) error {
	n.domain = domain
	return nil
}

func TestDeliveryBoundaryUsesImmutableTransportTargetSnapshot(t *testing.T) {
	domain := &models.Domain{
		ID:               uuid.New(),
		MerchantID:       uuid.New(),
		NotificationMode: models.DomainNotificationWebhook,
		WebhookURL:       "https://new.example/webhook",
	}
	queue := &recordingDeliveryQueue{}
	notifier := &targetRecordingDeliveryNotifier{}
	row := models.WebhookDelivery{
		ID:               uuid.New(),
		LeaseToken:       newWebhookLeaseToken(),
		MerchantID:       domain.MerchantID,
		DomainID:         domain.ID,
		EventID:          "immutable-target-event",
		EventType:        "transaction.detected.v1",
		PayloadJSON:      `{"event_id":"immutable-target-event"}`,
		NotificationMode: models.DomainNotificationNATS,
		TargetURL:        "nats://queued.example:4222",
		TargetSubject:    "merchant.queued.events",
	}
	boundary := DeliveryBoundary{
		Queue:    queue,
		Domains:  staticDeliveryDomainLookup{domain: domain},
		Notifier: notifier,
	}
	if err := boundary.DeliverOne(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	if notifier.domain.EffectiveNotificationMode() != models.DomainNotificationNATS ||
		notifier.domain.NATSURL != row.TargetURL ||
		notifier.domain.NATSSubject != row.TargetSubject {
		t.Fatalf("delivery target = %#v, want queued NATS snapshot", notifier.domain)
	}
}

func newWebhookLeaseToken() *uuid.UUID {
	token := uuid.New()
	return &token
}

func (*rawRecordingDeliveryNotifier) Deliver(context.Context, models.Domain, models.Transaction) error {
	return nil
}

func (n *rawRecordingDeliveryNotifier) DeliverPayment(context.Context, models.Domain, models.PaymentSession) error {
	n.paymentCalls++
	return nil
}

func (n *rawRecordingDeliveryNotifier) DeliverRaw(_ context.Context, _ models.Domain, _, _, _ string, body []byte) error {
	n.rawCalls++
	n.body = string(body)
	return nil
}

func TestDeliveryBoundaryPrefersPayloadSnapshotOverPaymentReload(t *testing.T) {
	domain := &models.Domain{ID: uuid.New(), MerchantID: uuid.New()}
	paymentID := uuid.New()
	payments := &countingDeliveryPaymentStore{findErr: errors.New("source row unavailable")}
	notifier := &rawRecordingDeliveryNotifier{}
	row := models.WebhookDelivery{
		ID:           uuid.New(),
		MerchantID:   domain.MerchantID,
		DomainID:     domain.ID,
		PaymentID:    &paymentID,
		EventID:      paymentID.String() + ":payment_succeeded",
		EventType:    "payment_succeeded",
		EventVersion: "v1",
		PayloadJSON:  `{"event_id":"immutable-event","amount":"25.00"}`,
	}
	boundary := DeliveryBoundary{
		Domains:  staticDeliveryDomainLookup{domain: domain},
		Payments: payments,
		Notifier: notifier,
	}

	if err := boundary.deliverOne(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	if notifier.rawCalls != 1 || notifier.paymentCalls != 0 || payments.findCalls != 0 {
		t.Fatalf("delivery calls raw=%d payment=%d source_find=%d", notifier.rawCalls, notifier.paymentCalls, payments.findCalls)
	}
	if notifier.body != row.PayloadJSON {
		t.Fatalf("raw body = %q, want immutable snapshot %q", notifier.body, row.PayloadJSON)
	}
}

func TestDeliveryLookupErrorOnlyMakesNotFoundAndConfigurationPermanent(t *testing.T) {
	transient := errors.New("database temporarily unavailable")
	err := deliveryLookupError("payment", transient)
	if IsPermanent(err) || !errors.Is(err, transient) {
		t.Fatalf("transient lookup error classification = %#v", err)
	}

	for _, sourceErr := range []error{
		gorm.ErrRecordNotFound,
		sql.ErrNoRows,
		gorm.ErrInvalidDB,
		errDeliveryDomainLookupNotConfigured,
		errDeliveryTransactionLookupNotConfigured,
		errDeliveryPaymentLookupNotConfigured,
	} {
		err := deliveryLookupError("payment", sourceErr)
		if !IsPermanent(err) || !errors.Is(err, sourceErr) {
			t.Fatalf("permanent lookup error classification for %v = %#v", sourceErr, err)
		}
	}
}

func TestMarkSourceAttemptGuardsPaymentEventIdentityAndAcceptsCatalogAlias(t *testing.T) {
	paymentID := uuid.New()
	payments := &countingDeliveryPaymentStore{eventType: "payment_succeeded"}
	boundary := DeliveryBoundary{Payments: payments}

	if err := boundary.markSourceAttempt(context.Background(), models.WebhookDelivery{
		PaymentID: &paymentID,
		EventType: "payment.partial_paid.v1",
	}, true, nil); err != nil {
		t.Fatal(err)
	}
	if payments.markCalls != 0 {
		t.Fatal("stale partial delivery marked the current success event as sent")
	}

	if err := boundary.markSourceAttempt(context.Background(), models.WebhookDelivery{
		PaymentID: &paymentID,
		EventType: "payment.succeeded.v1",
	}, true, nil); err != nil {
		t.Fatal(err)
	}
	if payments.markCalls != 1 {
		t.Fatalf("canonical success did not match legacy success alias; marks=%d", payments.markCalls)
	}
}

func TestMarkSourceAttemptGuardsTransactionEventIdentityAndAcceptsCatalogAlias(t *testing.T) {
	transactionID := uuid.New()
	transactions := &countingDeliveryTransactionStore{eventType: "native_transfer"}
	boundary := DeliveryBoundary{Transactions: transactions}

	if err := boundary.markSourceAttempt(context.Background(), models.WebhookDelivery{
		TransactionID: &transactionID,
		EventType:     "transaction.reorged.v1",
	}, true, nil); err != nil {
		t.Fatal(err)
	}
	if transactions.markCalls != 0 {
		t.Fatal("stale correction delivery marked a different transaction event as sent")
	}

	if err := boundary.markSourceAttempt(context.Background(), models.WebhookDelivery{
		TransactionID: &transactionID,
		EventType:     "transaction.detected.v1",
	}, true, nil); err != nil {
		t.Fatal(err)
	}
	if transactions.markCalls != 1 {
		t.Fatalf("canonical transaction did not match native_transfer alias; marks=%d", transactions.markCalls)
	}
}
