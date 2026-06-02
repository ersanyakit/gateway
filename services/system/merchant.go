package services

import (
	"core/models"
	"core/repositories"
	"core/types"
	"errors"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
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

func (s *MerchantService) Authenticate(params types.MerchantParams) (*models.Merchant, error) {
	if params.Email == nil || strings.TrimSpace(*params.Email) == "" {
		return nil, errors.New("email is required")
	}
	if params.Password == nil || *params.Password == "" {
		return nil, errors.New("password is required")
	}

	email := strings.TrimSpace(*params.Email)
	params.Email = &email
	merchant, err := s.merchantRepo.FindByEmail(params)
	if err != nil {
		return nil, errors.New("invalid email or password")
	}

	if bcrypt.CompareHashAndPassword([]byte(merchant.Password), []byte(*params.Password)) == nil {
		return merchant, nil
	}

	legacyPassword := strings.ToLower(*params.Password)
	if legacyPassword != *params.Password &&
		bcrypt.CompareHashAndPassword([]byte(merchant.Password), []byte(legacyPassword)) == nil {
		return merchant, nil
	}

	return nil, errors.New("invalid email or password")
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
