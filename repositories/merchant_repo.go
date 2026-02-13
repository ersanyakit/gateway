package repositories

import (
	"core/blockchain"
	helpers "core/helpers"
	"core/models"
	"core/types"
	"errors"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type MerchantRepo struct {
	db          *gorm.DB
	blockchains *blockchain.ChainFactory
}

func (r *MerchantRepo) DB() *gorm.DB {
	return r.db
}

func (r *MerchantRepo) Blockchains() *blockchain.ChainFactory {
	return r.blockchains
}

func NewMerchantRepo(db *gorm.DB, blockchains *blockchain.ChainFactory) *MerchantRepo {
	return &MerchantRepo{db: db, blockchains: blockchains}
}

func (r *MerchantRepo) Create(params types.MerchantParams) (*models.Merchant, error) {

	tx := r.db.WithContext(params.Context).Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}

	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	var count int64
	if err := tx.Model(&models.Merchant{}).
		Where("email = ?", *params.Email).
		Count(&count).Error; err != nil {

		tx.Rollback()
		return nil, err
	}

	if count > 0 {
		tx.Rollback()
		return nil, errors.New("email already exists")
	}

	// UUID v7
	merchantID, err := uuid.NewV7()
	if err != nil {
		tx.Rollback()
		return nil, err
	}

	hashedPassword, err := bcrypt.GenerateFromPassword(
		[]byte(*params.Password),
		bcrypt.DefaultCost,
	)
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	merchant := &models.Merchant{
		ID:       merchantID,
		Name:     *params.Name,
		Email:    *params.Email,
		Password: string(hashedPassword),
	}

	if err := tx.Create(merchant).Error; err != nil {
		tx.Rollback()
		return nil, err
	}
	if err := tx.Commit().Error; err != nil {
		return nil, err
	}
	return merchant, nil

}

func (r *MerchantRepo) FindByEmail(params types.MerchantParams) (*models.Merchant, error) {

	if err := params.ValidateEmail(); err != nil {
		return nil, err
	}

	var merchant models.Merchant

	err := r.db.WithContext(params.Context).
		Where("LOWER(email) = LOWER(?)", *params.Email).
		First(&merchant).Error

	if err != nil {
		return nil, err
	}

	return &merchant, nil
}

func (r *MerchantRepo) FindByID(params types.MerchantParams) (*models.Merchant, error) {

	if err := params.ValidateID(); err != nil {
		return nil, err
	}

	var merchant models.Merchant

	err := r.db.WithContext(params.Context).
		First(&merchant, "id = ?", *params.ID).Error

	if err != nil {
		return nil, err
	}

	return &merchant, nil
}

func (r *MerchantRepo) DeleteByEmail(params types.MerchantParams) error {
	return r.db.WithContext(params.Context).Transaction(func(tx *gorm.DB) error {
		var merchant models.Merchant
		err := tx.Where("LOWER(email) = LOWER(?)", params.Email).First(&merchant).Error
		if err != nil {
			return err
		}
		if err := tx.Delete(&merchant).Error; err != nil {
			return err
		}
		return nil
	})
}

func (r *MerchantRepo) DeleteByID(params types.MerchantParams) error {
	if err := params.ValidateID(); err != nil {
		return err
	}
	return r.db.WithContext(params.Context).Transaction(func(tx *gorm.DB) error {
		var merchant models.Merchant
		if err := tx.
			Where("id = ?", *params.ID).
			First(&merchant).Error; err != nil {
			return err
		}
		if err := tx.Delete(&merchant).Error; err != nil {
			return err
		}
		return nil
	})
}

func (r *MerchantRepo) Fetch(params types.MerchantParams) ([]models.Merchant, *uuid.UUID, error) {

	if params.Context == nil {
		return nil, nil, errors.New("context is required")
	}

	limit := params.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	query := r.db.WithContext(params.Context).
		Model(&models.Merchant{}).
		Where("deleted_at IS NULL")

	if params.Cursor != nil {
		query = query.Where("id > ?", *params.Cursor)
	}

	var merchants []models.Merchant

	err := query.
		Order("id ASC").
		Limit(limit).
		Find(&merchants).Error

	if err != nil {
		return nil, nil, err
	}

	var nextCursor *uuid.UUID
	if len(merchants) == limit {
		lastID := merchants[len(merchants)-1].ID
		nextCursor = &lastID
	}

	return merchants, nextCursor, nil
}

func (r *MerchantRepo) CreateDomain() (*models.Domain, error) {

	env := "live" //test
	domainURL := "coolvibes.io"
	webhookURL := "https://coolvibes.io/api/webhook"

	merchantID, _ := uuid.NewV7()

	keyID, apiKey, err := helpers.GenerateAPIKey(env)
	if err != nil {
		return nil, err
	}

	secret, err := helpers.GenerateSecret()
	if err != nil {
		return nil, err
	}

	apiKeyHash := helpers.HashSHA256(apiKey)

	encryptedSecret, err := helpers.EncryptSecret(secret)
	if err != nil {
		return nil, err
	}

	domain := &models.Domain{
		ID:         uuid.Must(uuid.NewV7()),
		MerchantID: merchantID,
		DomainURL:  domainURL,

		KeyID:     keyID,
		APIKey:    apiKeyHash,
		APISecret: encryptedSecret,
		IsEnabled: true,

		WebhookURL: webhookURL,
	}

	if err := r.db.Create(domain).Error; err != nil {
		return nil, err
	}

	return domain, nil

}
