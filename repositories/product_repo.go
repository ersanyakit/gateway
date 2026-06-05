package repositories

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"core/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type ProductRepo struct {
	db *gorm.DB
}

func NewProductRepo(db *gorm.DB) *ProductRepo {
	return &ProductRepo{db: db}
}

func (r *ProductRepo) Create(ctx context.Context, product *models.Product) error {
	if product == nil {
		return errors.New("product is required")
	}
	if product.ID == uuid.Nil {
		product.ID = uuid.New()
	}
	if strings.TrimSpace(product.LinkToken) == "" {
		token, err := newProductLinkToken()
		if err != nil {
			return err
		}
		product.LinkToken = token
	}
	if strings.TrimSpace(product.Language) == "" {
		product.Language = "tr"
	}
	now := time.Now()
	if product.CreatedAt.IsZero() {
		product.CreatedAt = now
	}
	product.UpdatedAt = now
	return r.db.WithContext(ctx).Create(product).Error
}

func (r *ProductRepo) FindByToken(ctx context.Context, token string) (*models.Product, error) {
	var product models.Product
	err := r.db.WithContext(ctx).
		Preload("Merchant").
		Preload("Domain").
		First(&product, "link_token = ? AND is_active = ?", strings.TrimSpace(token), true).Error
	if err != nil {
		return nil, err
	}
	return &product, nil
}

func (r *ProductRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.Product, error) {
	var product models.Product
	err := r.db.WithContext(ctx).First(&product, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &product, nil
}

func (r *ProductRepo) ListByMerchant(ctx context.Context, merchantID uuid.UUID, limit int) ([]models.Product, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var products []models.Product
	err := r.db.WithContext(ctx).
		Preload("Domain").
		Where("merchant_id = ?", merchantID).
		Order("created_at DESC").
		Limit(limit).
		Find(&products).Error
	return products, err
}

func (r *ProductRepo) ListPage(ctx context.Context, page, limit int) ([]models.Product, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	var total int64
	if err := r.db.WithContext(ctx).Model(&models.Product{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var products []models.Product
	err := r.db.WithContext(ctx).
		Preload("Merchant").
		Preload("Domain").
		Order("created_at DESC").
		Limit(limit).Offset((page - 1) * limit).
		Find(&products).Error
	return products, total, err
}

func (r *ProductRepo) DB() *gorm.DB { return r.db }

func newProductLinkToken() (string, error) {
	bytes := make([]byte, 18)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
