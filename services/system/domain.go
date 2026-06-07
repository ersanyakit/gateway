package services

import (
	"context"
	"core/helpers"
	"core/models"
	"core/repositories"
	"core/types"

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
	if params.WebhookURL != nil {
		if err := helpers.ValidateWebhookURL(*params.WebhookURL); err != nil {
			return nil, err
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

func (s *DomainService) RotateAPISecret(ctx context.Context, domainID uuid.UUID, merchantID uuid.UUID) (string, error) {
	return s.domainRepo.RotateAPISecret(ctx, domainID, merchantID)
}

func (s *DomainService) CreateReserve(ctx context.Context, merchantID uuid.UUID) (*models.Domain, error) {
	return s.domainRepo.CreateReserveDomain(ctx, merchantID)
}
