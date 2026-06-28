package repositories

import (
	"context"
	"core/helpers"
	"core/models"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var ErrAdminInactive = errors.New("admin account is inactive")

type AdminRepo struct {
	db *gorm.DB
}

func NewAdminRepo(db *gorm.DB) *AdminRepo {
	return &AdminRepo{db: db}
}

func (r *AdminRepo) DB() *gorm.DB { return r.db }

func (r *AdminRepo) Create(ctx context.Context, email, name, rawPassword string) (*models.Admin, error) {
	return r.CreateWithRole(ctx, email, name, rawPassword, models.AdminRoleOperator)
}

func (r *AdminRepo) CreateWithRole(ctx context.Context, email, name, rawPassword string, role string) (*models.Admin, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	name = strings.TrimSpace(name)
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
		Role:      models.NormalizeAdminRole(role),
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
	email = strings.ToLower(strings.TrimSpace(email))
	var admin models.Admin
	if err := r.db.WithContext(ctx).First(&admin, "email = ? AND is_active = true", email).Error; err != nil {
		return nil, err
	}
	decryptAdminTOTPSecret(&admin)
	return &admin, nil
}

func (r *AdminRepo) FindAnyByEmail(ctx context.Context, email string) (*models.Admin, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var admin models.Admin
	if err := r.db.WithContext(ctx).First(&admin, "email = ?", email).Error; err != nil {
		return nil, err
	}
	decryptAdminTOTPSecret(&admin)
	return &admin, nil
}

func (r *AdminRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.Admin, error) {
	var admin models.Admin
	if err := r.db.WithContext(ctx).First(&admin, "id = ?", id).Error; err != nil {
		return nil, err
	}
	decryptAdminTOTPSecret(&admin)
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
	encrypted, err := helpers.EncryptSecret(secret)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Model(&models.Admin{}).
		Where("id = ?", id).
		Updates(map[string]any{"totp_secret": encrypted, "totp_enabled": false, "updated_at": time.Now()}).Error
}

func (r *AdminRepo) EnableTOTPSecret(ctx context.Context, id uuid.UUID, secret string) error {
	encrypted, err := helpers.EncryptSecret(secret)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Model(&models.Admin{}).
		Where("id = ?", id).
		Updates(map[string]any{"totp_secret": encrypted, "totp_enabled": true, "updated_at": time.Now()}).Error
}

func (r *AdminRepo) DisableTOTP(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Model(&models.Admin{}).
		Where("id = ?", id).
		Updates(map[string]any{"totp_secret": "", "totp_enabled": false, "updated_at": time.Now()}).Error
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

func (r *AdminRepo) EnsureOIDCAdmin(ctx context.Context, email, name string) (*models.Admin, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	name = strings.TrimSpace(name)
	if email == "" {
		return nil, errors.New("email required")
	}
	admin, err := r.FindAnyByEmail(ctx, email)
	if err == nil {
		if !admin.IsActive {
			return nil, ErrAdminInactive
		}
		return admin, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if len(name) < 3 {
		name = strings.Split(email, "@")[0]
	}
	if len(name) < 3 {
		name = "OIDC Admin"
	}
	return r.Create(ctx, email, name, uuid.NewString()+uuid.NewString())
}

// EnsureBootstrapAdmin creates the first admin if no admin exists.
// Uses a transaction to prevent the race where two simultaneous startups both
// read count=0 and both try to insert.
func (r *AdminRepo) EnsureBootstrapAdmin(ctx context.Context, email, name, rawPassword string) (*models.Admin, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	name = strings.TrimSpace(name)
	var created *models.Admin
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&models.Admin{}).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return nil
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(rawPassword), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		admin := &models.Admin{
			ID:        uuid.New(),
			Email:     email,
			Name:      name,
			Password:  string(hash),
			Role:      models.AdminRoleOwner,
			IsActive:  true,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := tx.Create(admin).Error; err != nil {
			return err
		}
		created = admin
		return nil
	})
	return created, err
}

func decryptAdminTOTPSecret(admin *models.Admin) {
	if admin == nil || admin.TOTPSecret == "" {
		return
	}
	if decrypted, err := helpers.DecryptSecret(admin.TOTPSecret); err == nil {
		admin.TOTPSecret = decrypted
	}
}
