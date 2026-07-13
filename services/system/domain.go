package services

import (
	"context"
	"core/helpers"
	"core/models"
	"core/repositories"
	"core/types"
	"strings"

	"github.com/google/uuid"
)

type DomainService struct {
	domainRepo *repositories.DomainRepo
}

func NewDomainService(domainRepo *repositories.DomainRepo) *DomainService {
	return &DomainService{domainRepo: domainRepo}
}

func (s *DomainService) ServiceName() string {
	return "DomainService"
}

func (s *DomainService) Create(params types.DomainParams) (*models.Domain, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if params.DomainURL != nil {
		if err := helpers.ValidateDomainHost(*params.DomainURL); err != nil {
			return nil, err
		}
	}
	mode := strings.ToLower(strings.TrimSpace(params.NotificationMode))
	if mode == "" {
		mode = strings.ToLower(strings.TrimSpace(params.NotificationMethod))
	}
	params.NotificationMode = models.NormalizeDomainNotificationMode(mode)
	if params.NotificationMode == models.DomainNotificationWebhook {
		if params.WebhookURL == nil {
			return nil, helpers.ValidateWebhookURL("")
		}
		if err := helpers.ValidateWebhookURL(*params.WebhookURL); err != nil {
			return nil, err
		}
	} else {
		if params.NATSURL == nil {
			return nil, helpers.ValidateNATSURL("")
		}
		if err := helpers.ValidateNATSURL(*params.NATSURL); err != nil {
			return nil, err
		}
		if params.NATSSubject != nil {
			if err := helpers.ValidateNATSSubject(*params.NATSSubject); err != nil {
				return nil, err
			}
		}
	}
	return s.domainRepo.Create(params)
}

func (s *DomainService) ListByMerchant(ctx context.Context, merchantID uuid.UUID) ([]models.Domain, error) {
	return s.domainRepo.ListByMerchant(ctx, merchantID)
}

func (s *DomainService) FindByID(params types.DomainParams) (*models.Domain, error) {
	return s.domainRepo.FindByID(params)
}

func (s *DomainService) FindByAPIKey(params types.DomainParams) (*models.Domain, error) {
	return s.domainRepo.FindByAPIKey(params)
}

func (s *DomainService) FindBySecret(params types.DomainParams) (*models.Domain, error) {
	return s.domainRepo.FindByAPISecret(params)
}

func (s *DomainService) FindByURL(params types.DomainParams) (*models.Domain, error) {
	return s.domainRepo.FindByURL(params)
}

func (s *DomainService) UpdateWebhook(ctx context.Context, domainID uuid.UUID, merchantID uuid.UUID, webhookURL string, plainSecret string) error {
	if err := helpers.ValidateWebhookURL(webhookURL); err != nil {
		return err
	}
	return s.domainRepo.UpdateWebhook(ctx, domainID, merchantID, webhookURL, plainSecret)
}

func (s *DomainService) Update(ctx context.Context, domainID uuid.UUID, merchantID uuid.UUID, domainURL string, webhookURL string, plainSecret *string) error {
	return s.UpdateConfiguration(ctx, domainID, merchantID, domainURL, models.DomainNotificationWebhook, webhookURL, plainSecret, "", "")
}

func (s *DomainService) UpdateConfiguration(ctx context.Context, domainID uuid.UUID, merchantID uuid.UUID, domainURL string, notificationMode string, webhookURL string, plainSecret *string, natsURL string, natsSubject string) error {
	if err := helpers.ValidateDomainHost(domainURL); err != nil {
		return err
	}
	notificationMode = models.NormalizeDomainNotificationMode(notificationMode)
	if notificationMode == models.DomainNotificationWebhook {
		if err := helpers.ValidateWebhookURL(webhookURL); err != nil {
			return err
		}
	} else {
		if err := helpers.ValidateNATSURL(natsURL); err != nil {
			return err
		}
		if err := helpers.ValidateNATSSubject(natsSubject); err != nil {
			return err
		}
	}
	return s.domainRepo.UpdateConfiguration(ctx, domainID, merchantID, domainURL, notificationMode, webhookURL, plainSecret, natsURL, natsSubject)
}

func (s *DomainService) RotateAPISecret(ctx context.Context, domainID uuid.UUID, merchantID uuid.UUID) (string, error) {
	return s.domainRepo.RotateAPISecret(ctx, domainID, merchantID)
}

func (s *DomainService) CreateReserve(ctx context.Context, merchantID uuid.UUID) (*models.Domain, error) {
	return s.domainRepo.CreateReserveDomain(ctx, merchantID)
}
