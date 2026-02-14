package repositories

import (
	"context"
	"core/models"
	"core/types"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type WalletRepo struct {
	merchantRepo *MerchantRepo
}

func (r *WalletRepo) DB() *gorm.DB {
	return r.merchantRepo.DB()
}

func (r *WalletRepo) Merchant() *MerchantRepo {
	return r.merchantRepo
}

func NewWalletRepo(merchantRepo *MerchantRepo) *WalletRepo {
	return &WalletRepo{merchantRepo: merchantRepo}
}

func (r *WalletRepo) GetNextHDIndex(ctx context.Context, merchantID, domainID uuid.UUID) (uint32, error) {
	var maxIndex uint32
	err := r.DB().WithContext(ctx).
		Model(&models.Wallet{}).
		Where("merchant_id = ? AND domain_id = ?", merchantID, domainID).
		Select("COALESCE(MAX(hd_address_index), 0)").
		Scan(&maxIndex).Error
	if err != nil {
		return 0, err
	}
	return maxIndex + 1, nil
}

func (r *WalletRepo) Create(params types.WalletParams) (*models.Wallet, error) {

	domainId, err := uuid.Parse(*params.DomainId)
	if err != nil {
		return nil, errors.New("invalid domain id")
	}

	merchantId, err := uuid.Parse(*params.MerchantId)
	if err != nil {
		return nil, errors.New("invalid merchant id")
	}

	nextIndex, err := r.GetNextHDIndex(params.Context, merchantId, domainId)
	if err != nil {
		return nil, err
	}

	fmt.Println("CODER", nextIndex)

	return nil, nil
}
