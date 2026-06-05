package repositories

import (
	"context"
	"core/models"
	"errors"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type AdminRepo struct {
	db *gorm.DB
}

func NewAdminRepo(db *gorm.DB) *AdminRepo {
	return &AdminRepo{db: db}
}

func (r *AdminRepo) DB() *gorm.DB { return r.db }

func (r *AdminRepo) Create(ctx context.Context, email, name, rawPassword string) (*models.Admin, error) {
	if email == "" || rawPassword == "" {
		return nil, errors.New("email and password required")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(rawPassword), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	admin := &models.Admin{
		ID:        uuid.New(),
		Email:     email,
		Name:      name,
		Password:  string(hash),
		IsActive:  true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := r.db.WithContext(ctx).Create(admin).Error; err != nil {
		return nil, err
	}
	return admin, nil
}

func (r *AdminRepo) FindByEmail(ctx context.Context, email string) (*models.Admin, error) {
	var admin models.Admin
	if err := r.db.WithContext(ctx).First(&admin, "email = ? AND is_active = true", email).Error; err != nil {
		return nil, err
	}
	return &admin, nil
}

func (r *AdminRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.Admin, error) {
	var admin models.Admin
	if err := r.db.WithContext(ctx).First(&admin, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &admin, nil
}

func (r *AdminRepo) List(ctx context.Context) ([]models.Admin, error) {
	var admins []models.Admin
	err := r.db.WithContext(ctx).Order("created_at ASC").Find(&admins).Error
	return admins, err
}

func (r *AdminRepo) SetActive(ctx context.Context, id uuid.UUID, active bool) error {
	return r.db.WithContext(ctx).Model(&models.Admin{}).
		Where("id = ?", id).
		Updates(map[string]any{"is_active": active, "updated_at": time.Now()}).Error
}

func (r *AdminRepo) SaveTOTPSecret(ctx context.Context, id uuid.UUID, secret string) error {
	return r.db.WithContext(ctx).Model(&models.Admin{}).
		Where("id = ?", id).
		Updates(map[string]any{"totp_secret": secret, "totp_enabled": true, "updated_at": time.Now()}).Error
}

func (r *AdminRepo) SetPassword(ctx context.Context, id uuid.UUID, rawPassword string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(rawPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Model(&models.Admin{}).
		Where("id = ?", id).
		Updates(map[string]any{"password": string(hash), "updated_at": time.Now()}).Error
}

func (r *AdminRepo) Authenticate(ctx context.Context, email, rawPassword string) (*models.Admin, error) {
	admin, err := r.FindByEmail(ctx, email)
	if err != nil {
		return nil, errors.New("geçersiz bilgiler")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(admin.Password), []byte(rawPassword)); err != nil {
		return nil, errors.New("geçersiz bilgiler")
	}
	return admin, nil
}

// EnsureBootstrapAdmin creates the first admin from env vars if no admin exists.
func (r *AdminRepo) EnsureBootstrapAdmin(ctx context.Context, email, name, rawPassword string) (*models.Admin, error) {
	var count int64
	r.db.WithContext(ctx).Model(&models.Admin{}).Count(&count)
	if count > 0 {
		return nil, nil
	}
	return r.Create(ctx, email, name, rawPassword)
}
