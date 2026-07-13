package repositories

import (
	"context"
	helpers "core/helpers"
	"core/models"
	"core/types"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const domainHDAccountLockKey = "domain-hd-account-id"

const configuredDomainNotificationTargetWhere = `
(
	(
		LOWER(TRIM(COALESCE(domains.notification_mode, ''))) = ?
		AND TRIM(COALESCE(domains.nats_url, '')) <> ''
	)
	OR
	(
		LOWER(TRIM(COALESCE(domains.notification_mode, ''))) <> ?
		AND TRIM(COALESCE(domains.webhook_url, '')) <> ''
		AND TRIM(COALESCE(domains.webhook_secret, '')) <> ''
	)
)`

func whereConfiguredDomainNotificationTarget(db *gorm.DB) *gorm.DB {
	return db.Where(
		configuredDomainNotificationTargetWhere,
		models.DomainNotificationNATS,
		models.DomainNotificationNATS,
	)
}

type DomainRepo struct {
	merchantRepo *MerchantRepo
}

func (r *DomainRepo) DB() *gorm.DB {
	if r == nil || r.merchantRepo == nil {
		return nil
	}
	return r.merchantRepo.DB()
}

func (r *DomainRepo) MerchantRepo() *MerchantRepo {
	if r == nil {
		return nil
	}
	return r.merchantRepo
}

func NewDomainRepo(merchantRepo *MerchantRepo) *DomainRepo {
	return &DomainRepo{merchantRepo: merchantRepo}
}

func (r *DomainRepo) AcceptSignedRequestReplay(ctx context.Context, replayKey string, domainID uuid.UUID, expiresAt time.Time) (bool, error) {
	replayKey = strings.TrimSpace(replayKey)
	if r == nil || r.DB() == nil {
		return false, gorm.ErrInvalidDB
	}
	if replayKey == "" || domainID == uuid.Nil || expiresAt.IsZero() {
		return false, gorm.ErrInvalidData
	}
	now := time.Now().UTC()
	row := models.APISignedRequestReplay{
		ID:        uuid.New(),
		ReplayKey: replayKey,
		DomainID:  domainID,
		ExpiresAt: expiresAt.UTC(),
		CreatedAt: now,
		UpdatedAt: now,
	}
	accepted := false
	err := r.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("expires_at < ?", now).Delete(&models.APISignedRequestReplay{}).Error; err != nil {
			return err
		}
		result := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "replay_key"}},
			DoNothing: true,
		}).Create(&row)
		if result.Error != nil {
			return result.Error
		}
		accepted = result.RowsAffected == 1
		return nil
	})
	return accepted, err
}

// CreateReserveDomain creates a system-internal domain (no real URL, no webhook) used as the
// home for the merchant's reserve wallet (HD address index 0). Called once at merchant registration.
func (r *DomainRepo) CreateReserveDomain(ctx context.Context, merchantID uuid.UUID) (*models.Domain, error) {
	var domain *models.Domain

	err := r.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := r.lockDomainHDAccountID(ctx, tx); err != nil {
			return err
		}

		// Idempotent: if a reserve domain already exists, return it.
		var existing models.Domain
		if err := tx.
			Where("merchant_id = ? AND domain_url = ?", merchantID, "_reserve_").
			First(&existing).Error; err == nil {
			domain = &existing
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		keyID, apiKey, err := helpers.GenerateAPIKey("live")
		if err != nil {
			return err
		}
		apiSecretPlain, err := helpers.GenerateSecret()
		if err != nil {
			return err
		}
		hashedAPISecret, err := helpers.HMACSecret(apiSecretPlain)
		if err != nil {
			return err
		}
		hdIndex, err := r.getNextDomainHDIndex(ctx, tx)
		if err != nil {
			return err
		}
		domain = &models.Domain{
			MerchantID:              merchantID,
			DomainURL:               "_reserve_",
			KeyID:                   keyID,
			APIKey:                  apiKey,
			APISecret:               hashedAPISecret,
			APIScopes:               models.DefaultDomainAPIScopesCSV(),
			APISecretRotationPolicy: models.DomainAPISecretRotationImmediateRevoke,
			HDAccountID:             hdIndex,
		}
		return tx.Create(domain).Error
	})
	if err != nil {
		return nil, err
	}
	return domain, nil
}

func (r *DomainRepo) GetNextDomainHDIndex(ctx context.Context, _ uuid.UUID) (uint32, error) {
	return r.getNextDomainHDIndex(ctx, r.DB())
}

func (r *DomainRepo) getNextDomainHDIndex(ctx context.Context, db *gorm.DB) (uint32, error) {
	var maxIndex uint32
	err := db.WithContext(ctx).
		Model(&models.Domain{}).
		Select("COALESCE(MAX(hd_account_id), 0)").
		Scan(&maxIndex).Error
	if err != nil {
		return 0, err
	}
	return maxIndex + 1, nil
}

func (r *DomainRepo) lockDomainHDAccountID(ctx context.Context, tx *gorm.DB) error {
	if tx == nil || tx.Dialector == nil || tx.Dialector.Name() != "postgres" {
		return nil
	}
	return tx.WithContext(ctx).Exec("SELECT pg_advisory_xact_lock(hashtext(?))", domainHDAccountLockKey).Error
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
	if params.APIKey == nil || *params.APIKey == "" {
		return nil, errors.New("api key is required")
	}
	var domain models.Domain
	err := r.merchantRepo.DB().WithContext(params.Context).
		Joins("JOIN merchants ON merchants.id = domains.merchant_id").
		Where("domains.api_key = ? AND merchants.is_active = ? AND merchants.deleted_at IS NULL", *params.APIKey, true).
		First(&domain).Error
	if err != nil {
		return nil, err
	}
	return &domain, nil
}

func (r *DomainRepo) FindByAPISecret(params types.DomainParams) (*models.Domain, error) {
	if params.APISecret == nil || *params.APISecret == "" {
		return nil, errors.New("api secret is required")
	}
	hashed, err := helpers.HMACSecret(*params.APISecret)
	if err != nil {
		return nil, err
	}
	var domain models.Domain
	err = r.merchantRepo.DB().WithContext(params.Context).
		Joins("JOIN merchants ON merchants.id = domains.merchant_id").
		Where("domains.api_secret = ? AND merchants.is_active = ? AND merchants.deleted_at IS NULL", hashed, true).
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
	return r.isDomainNotificationTargetExistsWithDB(
		ctx,
		r.merchantRepo.DB(),
		merchantID,
		domainURL,
		models.DomainNotificationWebhook,
		webhookURL,
		"",
		"",
		nil,
	)
}

func (r *DomainRepo) isDomainNotificationTargetExistsWithDB(
	ctx context.Context,
	db *gorm.DB,
	merchantID uuid.UUID,
	domainURL string,
	notificationMode string,
	webhookURL string,
	natsURL string,
	natsSubject string,
	excludeDomainID *uuid.UUID,
) (bool, error) {
	notificationMode = models.NormalizeDomainNotificationMode(notificationMode)
	query := db.WithContext(ctx).
		Model(&models.Domain{}).
		Where("merchant_id = ? AND domain_url = ?", merchantID, domainURL)
	if excludeDomainID != nil {
		query = query.Where("id <> ?", *excludeDomainID)
	}
	if notificationMode == models.DomainNotificationNATS {
		query = query.
			Where("LOWER(TRIM(COALESCE(notification_mode, ''))) = ?", models.DomainNotificationNATS).
			Where("nats_url = ? AND nats_subject = ?", strings.TrimSpace(natsURL), strings.TrimSpace(natsSubject))
	} else {
		query = query.
			Where("LOWER(TRIM(COALESCE(notification_mode, ''))) <> ?", models.DomainNotificationNATS).
			Where("webhook_url = ?", strings.TrimSpace(webhookURL))
	}

	var count int64
	err := query.Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *DomainRepo) UpdateWebhook(ctx context.Context, domainID uuid.UUID, merchantID uuid.UUID, webhookURL string, plainSecret string) error {
	return r.UpdateConfiguration(ctx, domainID, merchantID, "", models.DomainNotificationWebhook, webhookURL, &plainSecret, "", "")
}

func (r *DomainRepo) Update(ctx context.Context, domainID uuid.UUID, merchantID uuid.UUID, domainURL string, webhookURL string, plainSecret *string) error {
	return r.UpdateConfiguration(ctx, domainID, merchantID, domainURL, models.DomainNotificationWebhook, webhookURL, plainSecret, "", "")
}

func (r *DomainRepo) UpdateConfiguration(ctx context.Context, domainID uuid.UUID, merchantID uuid.UUID, domainURL string, notificationMode string, webhookURL string, plainSecret *string, natsURL string, natsSubject string) error {
	notificationMode = models.NormalizeDomainNotificationMode(notificationMode)
	if strings.TrimSpace(domainURL) == "" {
		var current models.Domain
		if err := r.DB().WithContext(ctx).First(&current, "id = ? AND merchant_id = ?", domainID, merchantID).Error; err != nil {
			return err
		}
		domainURL = current.DomainURL
	}
	webhookURL = strings.TrimSpace(webhookURL)
	natsURL = strings.TrimSpace(natsURL)
	natsSubject = strings.TrimSpace(natsSubject)
	if natsSubject == "" {
		natsSubject = models.DefaultNATSSubject
	}

	exists, err := r.isDomainNotificationTargetExistsWithDB(
		ctx,
		r.DB(),
		merchantID,
		domainURL,
		notificationMode,
		webhookURL,
		natsURL,
		natsSubject,
		&domainID,
	)
	if err != nil {
		return err
	}
	if exists {
		return errors.New("domain with this notification target already exists for the merchant")
	}

	updates := map[string]interface{}{
		"domain_url":        domainURL,
		"notification_mode": notificationMode,
		"webhook_url":       webhookURL,
		"nats_url":          natsURL,
		"nats_subject":      natsSubject,
	}
	if plainSecret != nil && *plainSecret != "" {
		encryptedSecret, err := helpers.EncryptSecret(*plainSecret)
		if err != nil {
			return err
		}
		updates["webhook_secret"] = encryptedSecret
	}

	result := r.DB().WithContext(ctx).
		Model(&models.Domain{}).
		Where("id = ? AND merchant_id = ?", domainID, merchantID).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("domain not found")
	}
	return nil
}

func (r *DomainRepo) RotateAPISecret(ctx context.Context, domainID uuid.UUID, merchantID uuid.UUID) (string, error) {
	apiSecretPlain, err := helpers.GenerateSecret()
	if err != nil {
		return "", err
	}
	hashedAPISecret, err := helpers.HMACSecret(apiSecretPlain)
	if err != nil {
		return "", err
	}
	result := r.DB().WithContext(ctx).
		Model(&models.Domain{}).
		Where("id = ? AND merchant_id = ?", domainID, merchantID).
		Updates(map[string]any{
			"api_secret":                 hashedAPISecret,
			"api_secret_last_rotated_at": time.Now(),
			"api_secret_revoked_at":      nil,
			"api_secret_rotation_policy": models.DomainAPISecretRotationImmediateRevoke,
			"updated_at":                 time.Now(),
		})
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected == 0 {
		return "", errors.New("domain not found")
	}
	return apiSecretPlain, nil
}

func (r *DomainRepo) Create(params types.DomainParams) (domain *models.Domain, err error) {
	if params.Context == nil {
		return nil, repositoryOperationError("domain create", "context is required")
	}
	if r == nil || r.DB() == nil {
		return nil, repositoryOperationError("domain create", "database is not configured")
	}

	var tx *gorm.DB
	defer func() {
		if recovered := recover(); recovered != nil {
			domain = nil
			err = recoverRepositoryTransactionPanic("domain create", tx, recovered)
		}
	}()
	if err := params.Validate(); err != nil {
		return nil, err
	}

	tx = r.DB().WithContext(params.Context).Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}

	merchantUUID, err := uuid.Parse(*params.MerchantID)
	if err != nil {
		tx.Rollback()
		return nil, errors.New("invalid merchant id")
	}

	if err := r.lockDomainHDAccountID(params.Context, tx); err != nil {
		tx.Rollback()
		return nil, err
	}

	notificationMode := strings.TrimSpace(params.NotificationMode)
	if notificationMode == "" {
		notificationMode = strings.TrimSpace(params.NotificationMethod)
	}
	notificationMode = models.NormalizeDomainNotificationMode(notificationMode)
	webhookURL := ""
	if params.WebhookURL != nil {
		webhookURL = strings.TrimSpace(*params.WebhookURL)
	}
	natsURL := ""
	if params.NATSURL != nil {
		natsURL = strings.TrimSpace(*params.NATSURL)
	}
	natsSubject := ""
	if params.NATSSubject != nil {
		natsSubject = strings.TrimSpace(*params.NATSSubject)
	}
	if natsSubject == "" {
		natsSubject = models.DefaultNATSSubject
	}
	exists, err := r.isDomainNotificationTargetExistsWithDB(
		params.Context,
		tx,
		merchantUUID,
		*params.DomainURL,
		notificationMode,
		webhookURL,
		natsURL,
		natsSubject,
		nil,
	)
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	if exists {
		tx.Rollback()
		return nil, errors.New("domain with this notification target already exists for the merchant")
	}

	keyID, apiKey, err := helpers.GenerateAPIKey("live")
	if err != nil {
		tx.Rollback()
		return nil, err
	}

	masterKey := os.Getenv("MASTER_KEY")
	if masterKey == "" {
		tx.Rollback()
		return nil, errors.New("MASTER_KEY not set")
	}

	webhookSecret := ""
	if params.WebhookSecret != nil {
		webhookSecret = strings.TrimSpace(*params.WebhookSecret)
	}
	encryptedWebhookSecret := ""
	if webhookSecret != "" {
		encryptedWebhookSecret, err = helpers.EncryptSecret(webhookSecret)
		if err != nil {
			tx.Rollback()
			return nil, err
		}
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

	hdIndex, err := r.getNextDomainHDIndex(params.Context, tx)
	if err != nil {
		tx.Rollback()
		return nil, err
	}

	domain = &models.Domain{
		MerchantID:              merchantUUID,
		DomainURL:               *params.DomainURL,
		KeyID:                   keyID,
		APIKey:                  apiKey,
		APISecret:               hashedAPISecret,
		APIScopes:               models.DefaultDomainAPIScopesCSV(),
		APISecretRotationPolicy: models.DomainAPISecretRotationImmediateRevoke,
		NotificationMode:        notificationMode,
		WebhookURL:              webhookURL,
		WebhookSecret:           encryptedWebhookSecret,
		NATSURL:                 natsURL,
		NATSSubject:             natsSubject,
		HDAccountID:             hdIndex,
	}

	if err := tx.Create(domain).Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	if err := tx.Commit().Error; err != nil {
		return nil, err
	}

	domain.APISecretPlain = apiSecretPlain
	return domain, nil
}
