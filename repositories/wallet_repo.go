package repositories

import (
	"context"
	"core/models"
	"core/types"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type WalletRepo struct {
	domainRepo *DomainRepo
}

func (r *WalletRepo) DB() *gorm.DB {
	return r.domainRepo.DB()
}

func (r *WalletRepo) Domain() *DomainRepo {
	return r.domainRepo
}

func NewWalletRepo(domainRepo *DomainRepo) *WalletRepo {
	return &WalletRepo{domainRepo: domainRepo}
}

func (r *WalletRepo) GetNextHDIndex(ctx context.Context, merchantID, domainID uuid.UUID) (uint32, error) {
	var maxIndex uint32
	err := r.DB().WithContext(ctx).
		Model(&models.Wallet{}).
		Where("merchant_id = ? AND domain_id = ?", merchantID, domainID).
		Select("COALESCE(MAX(hd_address_id), 0)").
		Scan(&maxIndex).Error
	if err != nil {
		return 0, err
	}
	return maxIndex + 1, nil
}

func (r *WalletRepo) CreateEx(params types.WalletParams) (*models.Wallet, error) {

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

func (r *WalletRepo) Create(params types.WalletParams) (*models.Wallet, error) {
	tx := r.DB().WithContext(params.Context).Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}

	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	merchantUUID, err := uuid.Parse(*params.MerchantId)
	if err != nil {
		tx.Rollback()
		return nil, errors.New("invalid merchant id")
	}

	domainUUID, err := uuid.Parse(*params.DomainId)
	if err != nil {
		tx.Rollback()
		return nil, errors.New("invalid domain id")
	}

	domainParams := types.DomainParams{
		Context:  params.Context,
		DomainID: params.DomainId,
	}

	domain, err := r.domainRepo.FindByID(domainParams)
	if err != nil {
		fmt.Println("Domain bulunamadı:", err)
		return nil, err
	}

	hdAccountId, err := r.GetNextHDIndex(params.Context, merchantUUID, domainUUID)
	if err != nil {
		tx.Rollback()
		return nil, err
	}

	walletsMap, errorsMap := r.domainRepo.MerchantRepo().blockchains.CreateHDWallets(params.Context, int(domain.HDAccountID), int(hdAccountId))
	if len(errorsMap) > 0 {
		tx.Rollback()
		errStrings := make([]string, 0, len(errorsMap))
		for chainName, err := range errorsMap {
			errStrings = append(errStrings, chainName+": "+err.Error())
		}
		return nil, fmt.Errorf("failed to create wallets: %s", strings.Join(errStrings, "; "))
	}

	wallet := &models.Wallet{
		ID:               uuid.New(),
		HDAddressId:      hdAccountId,
		HDAccountID:      domain.HDAccountID,
		MerchantID:       merchantUUID,
		DomainID:         domainUUID,
		BitcoinAddress:   walletsMap["bitcoin"].Address,
		EthereumAddress:  walletsMap["ethereum"].Address,
		AvalancheAddress: walletsMap["avalanche"].Address,
		TronAddress:      walletsMap["tron"].Address,
		SolanaAddress:    walletsMap["solana"].Address,
		ChilizAddress:    walletsMap["chiliz"].Address,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	if err := tx.Create(wallet).Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	if err := tx.Commit().Error; err != nil {
		return nil, err
	}

	return wallet, nil
}
