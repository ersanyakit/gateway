package repositories

import (
	helpers "core/helper"
	"core/models"
	"core/types"
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type MerchantRepo struct {
	db *gorm.DB
}

func (r *MerchantRepo) DB() *gorm.DB {
	return r.db
}

func NewMerchantRepo(db *gorm.DB) *MerchantRepo {
	return &MerchantRepo{db: db}
}

func (r *MerchantRepo) Create(params types.MerchantParams) (*models.Merchant, error) {

	merchantID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}

	h := sha256.Sum256([]byte(merchantID.String()))
	merchantIndex := binary.BigEndian.Uint32(h[:4])

	fmt.Println("MERCHANTID", merchantIndex)

	return nil, nil
}

func (r *MerchantRepo) GetMerchantByID(merchantId uuid.UUID) (*models.Merchant, error) {

	return nil, nil
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
		Site:       domainURL,

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
