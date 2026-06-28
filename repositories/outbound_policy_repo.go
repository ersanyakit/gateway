package repositories

import (
	"context"
	"core/models"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var ErrOutboundPolicyInvalidAmount = errors.New("outbound policy amount must be a positive integer")

type OutboundPolicyRepo struct {
	db *gorm.DB
}

type OutboundPolicyScope struct {
	MerchantID *uuid.UUID
	DomainID   *uuid.UUID
	Chain      string
	Token      *string
}

type OutboundPolicyUpdate struct {
	Scope              OutboundPolicyScope
	WhitelistRequired  bool
	EmergencyFrozen    bool
	MaxAmountRaw       string
	VelocityLimitRaw   string
	VelocityWindowSecs int64
	ActorEmail         string
}

type OutboundWhitelistCreate struct {
	Scope      OutboundPolicyScope
	Address    string
	Label      string
	ActorEmail string
}

func NewOutboundPolicyRepo(db *gorm.DB) *OutboundPolicyRepo {
	return &OutboundPolicyRepo{db: db}
}

func (r *OutboundPolicyRepo) DB() *gorm.DB { return r.db }

func (r *OutboundPolicyRepo) GetGlobal(ctx context.Context) (*models.OutboundPolicySetting, error) {
	var setting models.OutboundPolicySetting
	err := r.db.WithContext(ctx).
		Where("merchant_id IS NULL AND domain_id IS NULL AND chain = '' AND token IS NULL").
		First(&setting).Error
	if err != nil {
		return nil, err
	}
	return &setting, nil
}

func (r *OutboundPolicyRepo) Upsert(ctx context.Context, update OutboundPolicyUpdate) (*models.OutboundPolicySetting, error) {
	scope := normalizeOutboundPolicyScope(update.Scope)
	if err := validateOptionalPositiveRaw(update.MaxAmountRaw); err != nil {
		return nil, err
	}
	if err := validateOptionalPositiveRaw(update.VelocityLimitRaw); err != nil {
		return nil, err
	}
	if update.VelocityWindowSecs <= 0 {
		update.VelocityWindowSecs = 86400
	}
	actor := strings.TrimSpace(update.ActorEmail)
	var saved models.OutboundPolicySetting
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := outboundPolicyScopeQuery(tx.Model(&models.OutboundPolicySetting{}), scope)
		var existing models.OutboundPolicySetting
		err := query.First(&existing).Error
		now := time.Now().UTC()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			saved = models.OutboundPolicySetting{
				ID:                 uuid.New(),
				MerchantID:         scope.MerchantID,
				DomainID:           scope.DomainID,
				Chain:              scope.Chain,
				Token:              scope.Token,
				WhitelistRequired:  update.WhitelistRequired,
				EmergencyFrozen:    update.EmergencyFrozen,
				MaxAmountRaw:       strings.TrimSpace(update.MaxAmountRaw),
				VelocityLimitRaw:   strings.TrimSpace(update.VelocityLimitRaw),
				VelocityWindowSecs: update.VelocityWindowSecs,
				CreatedBy:          actor,
				UpdatedBy:          actor,
				CreatedAt:          now,
				UpdatedAt:          now,
			}
			return tx.Create(&saved).Error
		}
		if err != nil {
			return err
		}
		updates := map[string]any{
			"whitelist_required":   update.WhitelistRequired,
			"emergency_frozen":     update.EmergencyFrozen,
			"max_amount_raw":       strings.TrimSpace(update.MaxAmountRaw),
			"velocity_limit_raw":   strings.TrimSpace(update.VelocityLimitRaw),
			"velocity_window_secs": update.VelocityWindowSecs,
			"updated_by":           actor,
			"updated_at":           now,
		}
		if err := tx.Model(&models.OutboundPolicySetting{}).Where("id = ?", existing.ID).Updates(updates).Error; err != nil {
			return err
		}
		return tx.First(&saved, "id = ?", existing.ID).Error
	})
	if err != nil {
		return nil, err
	}
	return &saved, nil
}

func (r *OutboundPolicyRepo) FindEffective(ctx context.Context, merchantID uuid.UUID, domainID *uuid.UUID, chain string, token *string) (*models.OutboundPolicySetting, error) {
	scopes := outboundPolicyPrecedenceScopes(merchantID, domainID, chain, token)
	for _, scope := range scopes {
		var setting models.OutboundPolicySetting
		err := outboundPolicyScopeQuery(r.db.WithContext(ctx).Model(&models.OutboundPolicySetting{}), scope).First(&setting).Error
		if err == nil {
			return &setting, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *OutboundPolicyRepo) ListWhitelist(ctx context.Context, limit int) ([]models.OutboundAddressWhitelist, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	var rows []models.OutboundAddressWhitelist
	err := r.db.WithContext(ctx).
		Order("created_at DESC").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}

func (r *OutboundPolicyRepo) AddWhitelist(ctx context.Context, input OutboundWhitelistCreate) (*models.OutboundAddressWhitelist, error) {
	scope := normalizeOutboundPolicyScope(input.Scope)
	address := strings.ToLower(strings.TrimSpace(input.Address))
	if address == "" {
		return nil, errors.New("whitelist address is required")
	}
	entry := &models.OutboundAddressWhitelist{
		ID:         uuid.New(),
		MerchantID: scope.MerchantID,
		DomainID:   scope.DomainID,
		Chain:      scope.Chain,
		Token:      scope.Token,
		Address:    address,
		Label:      strings.TrimSpace(input.Label),
		IsActive:   true,
		CreatedBy:  strings.TrimSpace(input.ActorEmail),
		UpdatedBy:  strings.TrimSpace(input.ActorEmail),
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing models.OutboundAddressWhitelist
		err := outboundWhitelistScopeQuery(tx.Model(&models.OutboundAddressWhitelist{}), scope).
			Where("address = ?", address).
			First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Create(entry).Error
		}
		if err != nil {
			return err
		}
		updates := map[string]any{
			"label":      entry.Label,
			"is_active":  true,
			"updated_by": entry.UpdatedBy,
			"updated_at": entry.UpdatedAt,
		}
		if err := tx.Model(&models.OutboundAddressWhitelist{}).Where("id = ?", existing.ID).Updates(updates).Error; err != nil {
			return err
		}
		return tx.First(entry, "id = ?", existing.ID).Error
	})
	if err != nil {
		return nil, err
	}
	return entry, nil
}

func (r *OutboundPolicyRepo) SetWhitelistActive(ctx context.Context, id uuid.UUID, active bool, actorEmail string) error {
	result := r.db.WithContext(ctx).Model(&models.OutboundAddressWhitelist{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"is_active":  active,
			"updated_by": strings.TrimSpace(actorEmail),
			"updated_at": time.Now().UTC(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *OutboundPolicyRepo) IsAddressWhitelisted(ctx context.Context, merchantID uuid.UUID, domainID *uuid.UUID, chain string, token *string, address string) (bool, error) {
	address = strings.ToLower(strings.TrimSpace(address))
	if address == "" {
		return false, nil
	}
	for _, scope := range outboundPolicyPrecedenceScopes(merchantID, domainID, chain, token) {
		var count int64
		err := outboundWhitelistScopeQuery(r.db.WithContext(ctx).Model(&models.OutboundAddressWhitelist{}), scope).
			Where("address = ? AND is_active = true", address).
			Count(&count).Error
		if err != nil {
			return false, err
		}
		if count > 0 {
			return true, nil
		}
	}
	return false, nil
}

func outboundPolicyPrecedenceScopes(merchantID uuid.UUID, domainID *uuid.UUID, chain string, token *string) []OutboundPolicyScope {
	normalized := normalizeOutboundPolicyScope(OutboundPolicyScope{
		MerchantID: &merchantID,
		DomainID:   domainID,
		Chain:      chain,
		Token:      token,
	})
	scopes := make([]OutboundPolicyScope, 0, 12)
	if normalized.DomainID != nil {
		scopes = append(scopes, OutboundPolicyScope{DomainID: normalized.DomainID, Chain: normalized.Chain, Token: normalized.Token})
	}
	if normalized.MerchantID != nil {
		scopes = append(scopes, OutboundPolicyScope{MerchantID: normalized.MerchantID, Chain: normalized.Chain, Token: normalized.Token})
	}
	scopes = append(scopes, OutboundPolicyScope{Chain: normalized.Chain, Token: normalized.Token})
	if normalized.DomainID != nil {
		scopes = append(scopes, OutboundPolicyScope{DomainID: normalized.DomainID, Chain: normalized.Chain})
	}
	if normalized.MerchantID != nil {
		scopes = append(scopes, OutboundPolicyScope{MerchantID: normalized.MerchantID, Chain: normalized.Chain})
	}
	scopes = append(scopes, OutboundPolicyScope{Chain: normalized.Chain})
	if normalized.DomainID != nil {
		scopes = append(scopes, OutboundPolicyScope{DomainID: normalized.DomainID})
	}
	if normalized.MerchantID != nil {
		scopes = append(scopes, OutboundPolicyScope{MerchantID: normalized.MerchantID})
	}
	scopes = append(scopes, OutboundPolicyScope{})
	return scopes
}

func normalizeOutboundPolicyScope(scope OutboundPolicyScope) OutboundPolicyScope {
	normalized := OutboundPolicyScope{
		MerchantID: scope.MerchantID,
		DomainID:   scope.DomainID,
		Chain:      strings.ToLower(strings.TrimSpace(scope.Chain)),
	}
	if scope.Token != nil {
		token := strings.ToLower(strings.TrimSpace(*scope.Token))
		if token != "" {
			normalized.Token = &token
		}
	}
	return normalized
}

func outboundPolicyScopeQuery(query *gorm.DB, scope OutboundPolicyScope) *gorm.DB {
	scope = normalizeOutboundPolicyScope(scope)
	query = nullableUUIDWhere(query, "merchant_id", scope.MerchantID)
	query = nullableUUIDWhere(query, "domain_id", scope.DomainID)
	query = query.Where("chain = ?", scope.Chain)
	if scope.Token != nil {
		query = query.Where("LOWER(COALESCE(token, '')) = ?", strings.ToLower(strings.TrimSpace(*scope.Token)))
	} else {
		query = query.Where("token IS NULL")
	}
	return query
}

func outboundWhitelistScopeQuery(query *gorm.DB, scope OutboundPolicyScope) *gorm.DB {
	return outboundPolicyScopeQuery(query, scope)
}

func nullableUUIDWhere(query *gorm.DB, column string, id *uuid.UUID) *gorm.DB {
	if id == nil || *id == uuid.Nil {
		return query.Where(column + " IS NULL")
	}
	return query.Where(column+" = ?", *id)
}

func validateOptionalPositiveRaw(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	value, ok := new(big.Int).SetString(raw, 10)
	if !ok || value.Sign() <= 0 {
		return ErrOutboundPolicyInvalidAmount
	}
	return nil
}
