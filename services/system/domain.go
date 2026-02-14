package services

import (
	"core/models"
	"core/repositories"
	"core/types"
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
	return s.domainRepo.Create(params)
}

func (s *DomainService) FindByID(params types.DomainParams) (*models.Domain, error) {
	return s.domainRepo.FindByID(params)
}

func (s *DomainService) FindByAPIKey(params types.DomainParams) (*models.Domain, error) {
	return s.domainRepo.FindByAPIKey(params)
}

func (s *DomainService) FindBySecret(params types.DomainParams) (*models.Domain, error) {
	return s.domainRepo.FindByAPIKey(params)
}

func (s *DomainService) FindByURL(params types.DomainParams) (*models.Domain, error) {
	return s.domainRepo.FindByURL(params)
}
