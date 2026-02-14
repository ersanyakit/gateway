package repositories

import (
	"context"
	helpers "core/helpers"
	"core/models"
	"core/types"
	"errors"
	"os"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type DomainRepo struct {
	merchantRepo *MerchantRepo
}

func (r *DomainRepo) DB() *gorm.DB {
	return r.merchantRepo.DB()
}

func (r *DomainRepo) MerchantRepo() *MerchantRepo {
	return r.merchantRepo
}

func NewDomainRepo(merchantRepo *MerchantRepo) *DomainRepo {
	return &DomainRepo{merchantRepo: merchantRepo}
}

func (r *DomainRepo) GetNextDomainHDIndex(ctx context.Context, merchantID uuid.UUID) (uint32, error) {
	var maxIndex uint32
	err := r.DB().WithContext(ctx).
		Model(&models.Domain{}).
		Where("merchant_id = ?", merchantID).
		Select("COALESCE(MAX(hd_account_id), 0)").
		Scan(&maxIndex).Error
	if err != nil {
		return 0, err
	}
	return maxIndex + 1, nil
}

func (r *DomainRepo) FindByID(params types.DomainParams) (*models.Domain, error) {
	var domain models.Domain
	err := r.merchantRepo.DB().WithContext(params.Context).
		First(&domain, "id = ?", params.DomainID).Error
	if err != nil {
		return nil, err
	}
	return &domain, nil
}

func (r *DomainRepo) FindByAPIKey(params types.DomainParams) (*models.Domain, error) {
	var domain models.Domain
	err := r.merchantRepo.DB().WithContext(params.Context).
		Where("api_key = ?", params.APIKey).
		First(&domain).Error
	if err != nil {
		return nil, err
	}
	return &domain, nil
}

func (r *DomainRepo) FindByAPISecret(params types.DomainParams) (*models.Domain, error) {
	encryptedSecret, err := helpers.EncryptSecret(*params.APISecret)
	if err != nil {
		return nil, err
	}

	var domain models.Domain
	err = r.merchantRepo.DB().WithContext(params.Context).
		Where("api_secret = ?", encryptedSecret).
		First(&domain).Error
	if err != nil {
		return nil, err
	}
	return &domain, nil
}

func (r *DomainRepo) FindByURL(params types.DomainParams) (*models.Domain, error) {
	var domain models.Domain
	err := r.merchantRepo.DB().WithContext(params.Context).
		First(&domain, "domain_url = ?", params.DomainURL).Error
	if err != nil {
		return nil, err
	}
	return &domain, nil
}

func (r *DomainRepo) IsDomainExists(ctx context.Context, merchantID uuid.UUID, domainURL, webhookURL string) (bool, error) {
	var count int64
	err := r.merchantRepo.DB().WithContext(ctx).
		Model(&models.Domain{}).
		Where("merchant_id = ? AND domain_url = ? AND webhook_url = ?", merchantID, domainURL, webhookURL).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *DomainRepo) Create(params types.DomainParams) (*models.Domain, error) {

	tx := r.merchantRepo.DB().WithContext(params.Context).Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}

	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	merchantUUID, err := uuid.Parse(*params.MerchantID)
	if err != nil {
		tx.Rollback()
		return nil, errors.New("invalid merchant id")
	}

	exists, err := r.IsDomainExists(params.Context, merchantUUID, *params.DomainURL, *params.WebhookURL)
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	if exists {
		tx.Rollback()
		return nil, errors.New("domain with this webhook already exists for the merchant")
	}

	keyID, apiKey, err := helpers.GenerateAPIKey("live")
	if err != nil {
		tx.Rollback()
		return nil, err
	}

	masterKey := os.Getenv("MASTER_KEY")
	if masterKey == "" {
		return nil, errors.New("MASTER_KEY not set")
	}

	encryptedSecret, err := helpers.EncryptSecret(*params.WebhookSecret)
	if err != nil {
		tx.Rollback()
		return nil, err
	}

	hdIndex, err := r.GetNextDomainHDIndex(params.Context, merchantUUID)
	if err != nil {
		tx.Rollback()
		return nil, err
	}

	domain := &models.Domain{
		MerchantID:    merchantUUID,
		DomainURL:     *params.DomainURL,
		KeyID:         keyID,
		APIKey:        apiKey,
		APISecret:     encryptedSecret,
		WebhookURL:    *params.WebhookURL,
		WebhookSecret: *params.WebhookSecret,
		HDAccountID:   hdIndex,
	}

	if err := tx.Create(domain).Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	if err := tx.Commit().Error; err != nil {
		return nil, err
	}

	return domain, nil
}
