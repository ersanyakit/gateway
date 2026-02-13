package services

import (
	"core/models"
	"core/repositories"
	"core/types"
)

type WalletService struct {
	walletRepo *repositories.WalletRepo
}

func NewWalletService(walletRepo *repositories.WalletRepo) *WalletService {
	return &WalletService{walletRepo: walletRepo}
}

func (s *WalletService) ServiceName() string {
	return "WalletService"
}

func (s *WalletService) Create(params types.WalletParams) (*models.Wallet, error) {
	return s.walletRepo.Create(params)
}
