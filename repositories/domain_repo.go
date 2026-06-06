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

// CreateReserveDomain creates a system-internal domain (no real URL, no webhook) used as the
// home for the merchant's reserve wallet (HD address index 0). Called once at merchant registration.
func (r *DomainRepo) CreateReserveDomain(ctx context.Context, merchantID uuid.UUID) (*models.Domain, error) {
	// Idempotent: if a reserve domain already exists, return it.
	var existing models.Domain
	if err := r.DB().WithContext(ctx).
		Where("merchant_id = ? AND domain_url = ?", merchantID, "_reserve_").
		First(&existing).Error; err == nil {
		return &existing, nil
	}

	keyID, apiKey, err := helpers.GenerateAPIKey("live")
	if err != nil {
		return nil, err
	}
	apiSecretPlain, err := helpers.GenerateSecret()
	if err != nil {
		return nil, err
	}
	hashedAPISecret, err := helpers.HMACSecret(apiSecretPlain)
	if err != nil {
		return nil, err
	}
	hdIndex, err := r.GetNextDomainHDIndex(ctx, merchantID)
	if err != nil {
		return nil, err
	}
	domain := &models.Domain{
		MerchantID:  merchantID,
		DomainURL:   "_reserve_",
		KeyID:       keyID,
		APIKey:      apiKey,
		APISecret:   hashedAPISecret,
		HDAccountID: hdIndex,
	}
	if err := r.DB().WithContext(ctx).Create(domain).Error; err != nil {
		return nil, err
	}
	return domain, nil
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
	hashed, err := helpers.HMACSecret(*params.APISecret)
	if err != nil {
		return nil, err
	}
	var domain models.Domain
	err = r.merchantRepo.DB().WithContext(params.Context).
		Where("api_secret = ?", hashed).
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

func (r *DomainRepo) ListByMerchant(ctx context.Context, merchantID uuid.UUID) ([]models.Domain, error) {
	var domains []models.Domain
	err := r.merchantRepo.DB().WithContext(ctx).
		Where("merchant_id = ?", merchantID).
		Order("created_at DESC").
		Find(&domains).Error
	if err != nil {
		return nil, err
	}
	return domains, nil
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

func (r *DomainRepo) UpdateWebhook(ctx context.Context, domainID uuid.UUID, merchantID uuid.UUID, webhookURL string, plainSecret string) error {
	encryptedSecret, err := helpers.EncryptSecret(plainSecret)
	if err != nil {
		return err
	}
	result := r.DB().WithContext(ctx).
		Model(&models.Domain{}).
		Where("id = ? AND merchant_id = ?", domainID, merchantID).
		Updates(map[string]interface{}{
			"webhook_url":    webhookURL,
			"webhook_secret": encryptedSecret,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("domain not found")
	}
	return nil
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

	encryptedWebhookSecret, err := helpers.EncryptSecret(*params.WebhookSecret)
	if err != nil {
		tx.Rollback()
		return nil, err
	}

	apiSecretPlain, err := helpers.GenerateSecret()
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	// Store HMAC(MASTER_KEY, secret) so FindByAPISecret can do a deterministic lookup.
	// AES-GCM encryption is non-deterministic and cannot be used for DB WHERE clauses.
	hashedAPISecret, err := helpers.HMACSecret(apiSecretPlain)
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
		APISecret:     hashedAPISecret,
		WebhookURL:    *params.WebhookURL,
		WebhookSecret: encryptedWebhookSecret,
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
