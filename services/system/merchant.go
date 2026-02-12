package services

import (
	"core/models"
	"core/repositories"
	"core/types"
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
