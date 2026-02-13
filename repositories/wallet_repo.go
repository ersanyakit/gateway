package repositories

import (
	"core/models"
	"core/types"
	"fmt"

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

func (r *WalletRepo) GetNextWalletHDIndex() (uint32, error) {
	var nextVal int64
	err := r.DB().Raw("SELECT nextval('wallet_hd_address_seq')").Scan(&nextVal).Error
	if err != nil {
		return 0, err
	}
	return uint32(nextVal), nil
}

func (r *WalletRepo) Create(params types.WalletParams) (*models.Wallet, error) {

	nextIndex, err := r.GetNextWalletHDIndex()
	if err != nil {
		return nil, err
	}

	fmt.Println("CODER", nextIndex)

	return nil, nil
}
