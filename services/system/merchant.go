package services

import (
	"core/models"
	"core/repositories"
	"core/types"

	"github.com/google/uuid"
)

type MerchantService struct {
	merchantRepo *repositories.MerchantRepo
}

func NewMerchantService(merchantRepo *repositories.MerchantRepo) *MerchantService {
	return &MerchantService{merchantRepo: merchantRepo}
}

func (s *MerchantService) ServiceName() string {
	return "MerchantService"
}

func (s *MerchantService) Create(params types.MerchantParams) (*models.Merchant, error) {
	return s.merchantRepo.Create(params)
}

func (s *MerchantService) FindByEmail(params types.MerchantParams) (*models.Merchant, error) {
	return s.merchantRepo.FindByEmail(params)
}

func (s *MerchantService) FindByID(params types.MerchantParams) (*models.Merchant, error) {
	return s.merchantRepo.FindByID(params)
}

func (s *MerchantService) DeleteByEmail(params types.MerchantParams) error {
	return s.merchantRepo.DeleteByEmail(params)
}

func (s *MerchantService) DeleteByID(params types.MerchantParams) error {
	return s.merchantRepo.DeleteByID(params)
}

func (s *MerchantService) Fetch(params types.MerchantParams) ([]models.Merchant, *uuid.UUID, error) {
	return s.merchantRepo.Fetch(params)
}
