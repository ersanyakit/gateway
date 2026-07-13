package webhook

import (
	"context"
	"errors"
	"testing"

	"core/models"

	"github.com/google/uuid"
)

type failingDeliveryPaymentStore struct {
	markErr error
}

func (s failingDeliveryPaymentStore) FindByID(context.Context, uuid.UUID) (*models.PaymentSession, error) {
	return &models.PaymentSession{}, nil
}

func (s failingDeliveryPaymentStore) MarkWebhookAttempt(context.Context, uuid.UUID, bool, error) error {
	return s.markErr
}

func TestMarkSourceAttemptPropagatesPaymentPersistenceError(t *testing.T) {
	paymentID := uuid.New()
	sentinel := errors.New("payment state unavailable")
	boundary := DeliveryBoundary{Payments: failingDeliveryPaymentStore{markErr: sentinel}}

	err := boundary.markSourceAttempt(context.Background(), models.WebhookDelivery{PaymentID: &paymentID}, true, nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want payment persistence error", err)
	}
}
