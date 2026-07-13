package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type WalletAddressRepo struct {
	db *gorm.DB
}

type WalletAddressReservationRequest struct {
	MerchantID  uuid.UUID
	DomainID    uuid.UUID
	ProductID   string
	UserID      string
	HDAccountID uint32
	Purpose     string
	ReusePolicy string
	ExpiresAt   *time.Time
}

type WalletAddressReuseDecision struct {
	Purpose        string
	Policy         string
	ReuseExisting  bool
	RequiresFresh  bool
	RotateAfterUse bool
}

type WalletAddressGapScanResult struct {
	ChainID          constants.ChainID
	HDAccountID      uint32
	Purpose          string
	Lookahead        uint32
	LastScannedIndex uint32
	UsedIndexes      []uint32
	Anomalies        []WalletAddressGapScanAnomalyInput
	ScannedAt        time.Time
}

type WalletAddressGapScanConfig struct {
	DefaultLookahead uint32
	ChainLookahead   map[constants.ChainID]uint32
}

type WalletAddressGapScanRequest struct {
	ChainID     constants.ChainID
	HDAccountID uint32
	Purpose     string
	StartIndex  uint32
	Lookahead   uint32
	ScannedAt   time.Time
	Derive      func(context.Context, uint32) (string, error)
	IsUsed      func(context.Context, string) (bool, error)
	Config      WalletAddressGapScanConfig
}

type WalletAddressGapScanAnomalyInput struct {
	HDAddressID uint32
	Address     string
	Category    string
	Detail      string
}

func NewWalletAddressRepo(db *gorm.DB) *WalletAddressRepo {
	return &WalletAddressRepo{db: db}
}

func (r *WalletAddressRepo) DB() *gorm.DB { return r.db }

var (
	ErrWalletAddressReservationTerminal = errors.New("wallet address reservation is terminal")
	ErrHDIndexExhausted                 = errors.New("wallet address hd index space exhausted")
)

const maxHDAddressIndex = ^uint32(0)

func DecideWalletAddressReusePolicy(purpose string, hasActiveAddress bool) WalletAddressReuseDecision {
	purpose = NormalizeWalletAddressPurpose(purpose)
	switch purpose {
	case models.WalletAddressPurposeStaticDeposit, models.WalletAddressPurposeCEXDeposit, models.WalletAddressPurposeReserve:
		return WalletAddressReuseDecision{
			Purpose:       purpose,
			Policy:        models.WalletAddressReusePolicyReuse,
			ReuseExisting: hasActiveAddress,
		}
	default:
		return WalletAddressReuseDecision{
			Purpose:        models.WalletAddressPurposeCheckout,
			Policy:         models.WalletAddressReusePolicyFresh,
			RequiresFresh:  true,
			RotateAfterUse: true,
		}
	}
}

func NormalizeWalletAddressPurpose(purpose string) string {
	switch strings.ToLower(strings.TrimSpace(purpose)) {
	case models.WalletAddressPurposeStaticDeposit, "static", "deposit_static":
		return models.WalletAddressPurposeStaticDeposit
	case models.WalletAddressPurposeCEXDeposit, "cex", "permanent_deposit", "wallet_provider":
		return models.WalletAddressPurposeCEXDeposit
	case models.WalletAddressPurposeReserve, "system_reserve":
		return models.WalletAddressPurposeReserve
	default:
		return models.WalletAddressPurposeCheckout
	}
}

func normalizeWalletAddressPurposeForRequest(purpose, productID, userID string) string {
	if strings.TrimSpace(purpose) == "" {
		return WalletAddressPurposeForProduct(productID, userID)
	}
	return NormalizeWalletAddressPurpose(purpose)
}

func WalletAddressPurposeForWallet(wallet models.Wallet) string {
	return WalletAddressPurposeForProduct(wallet.ProductID, wallet.UserID)
}

func WalletAddressPurposeForProduct(productID string, userID string) string {
	productID = strings.ToLower(strings.TrimSpace(productID))
	switch {
	case productID == "" && strings.TrimSpace(userID) == "":
		return models.WalletAddressPurposeReserve
	case strings.HasPrefix(productID, "static:"):
		return models.WalletAddressPurposeStaticDeposit
	case strings.HasPrefix(productID, "wallet:"):
		return models.WalletAddressPurposeCEXDeposit
	default:
		return models.WalletAddressPurposeCheckout
	}
}

func (r *WalletAddressRepo) ReserveNextHDIndex(ctx context.Context, req WalletAddressReservationRequest) (*models.WalletAddressReservation, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("wallet address repository is not configured")
	}
	var reservation *models.WalletAddressReservation
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		reservation, err = r.ReserveNextHDIndexTx(ctx, tx, req)
		return err
	})
	return reservation, err
}

func (r *WalletAddressRepo) ReserveNextHDIndexTx(ctx context.Context, tx *gorm.DB, req WalletAddressReservationRequest) (*models.WalletAddressReservation, error) {
	if tx == nil {
		return nil, errors.New("wallet address transaction is not configured")
	}
	req.ProductID = strings.TrimSpace(req.ProductID)
	req.UserID = strings.TrimSpace(req.UserID)
	req.Purpose = normalizeWalletAddressPurposeForRequest(req.Purpose, req.ProductID, req.UserID)
	decision := DecideWalletAddressReusePolicy(req.Purpose, false)
	if strings.TrimSpace(req.ReusePolicy) == "" {
		req.ReusePolicy = decision.Policy
	}
	if req.MerchantID == uuid.Nil || req.DomainID == uuid.Nil || req.HDAccountID == 0 {
		return nil, errors.New("wallet address reservation requires merchant, domain, and hd account")
	}

	if err := r.lockReservationScope(ctx, tx, req); err != nil {
		return nil, err
	}

	if existing, ok, err := r.findReservationByOwner(ctx, tx, req); err != nil || ok {
		return existing, err
	}

	next, err := r.nextHDIndexFromPool(ctx, tx, req.MerchantID, req.DomainID, req.HDAccountID)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	for attempts := 0; attempts < 8; attempts++ {
		reservation := models.WalletAddressReservation{
			ID:              uuid.New(),
			MerchantID:      req.MerchantID,
			DomainID:        req.DomainID,
			ProductID:       req.ProductID,
			UserID:          req.UserID,
			HDAccountID:     req.HDAccountID,
			HDAddressID:     next,
			Purpose:         req.Purpose,
			LifecycleStatus: models.WalletAddressStatusReserved,
			ReusePolicy:     req.ReusePolicy,
			ReservedAt:      now,
			ExpiresAt:       req.ExpiresAt,
		}
		err := tx.WithContext(ctx).Create(&reservation).Error
		if err == nil {
			return &reservation, nil
		}
		if !isUniqueConstraintError(err) {
			return nil, err
		}
		if existing, ok, findErr := r.findReservationByOwner(ctx, tx, req); findErr != nil || ok {
			return existing, findErr
		}
		next++
	}
	return nil, errors.New("wallet address reservation retry limit exceeded")
}

func (r *WalletAddressRepo) PeekNextHDIndex(ctx context.Context, tx *gorm.DB, merchantID, domainID uuid.UUID) (uint32, error) {
	if tx == nil {
		tx = r.db
	}
	return r.nextHDIndexFromPool(ctx, tx, merchantID, domainID, 0)
}

func (r *WalletAddressRepo) lockReservationScope(ctx context.Context, tx *gorm.DB, req WalletAddressReservationRequest) error {
	if tx.Dialector == nil || tx.Dialector.Name() != "postgres" {
		return nil
	}
	key := fmt.Sprintf("wallet-address-reservation:%s:%s:%d", req.MerchantID, req.DomainID, req.HDAccountID)
	return tx.WithContext(ctx).Exec("SELECT pg_advisory_xact_lock(hashtext(?))", key).Error
}

func (r *WalletAddressRepo) findReservationByOwner(ctx context.Context, tx *gorm.DB, req WalletAddressReservationRequest) (*models.WalletAddressReservation, bool, error) {
	var reservation models.WalletAddressReservation
	err := tx.WithContext(ctx).
		Where(
			"merchant_id = ? AND domain_id = ? AND product_id = ? AND user_id = ? AND purpose = ?",
			req.MerchantID,
			req.DomainID,
			req.ProductID,
			req.UserID,
			req.Purpose,
		).
		First(&reservation).Error
	if err == nil {
		if !walletAddressReservationReusable(reservation, time.Now().UTC()) {
			return nil, true, fmt.Errorf(
				"%w: owner=%s/%s/%s/%s purpose=%s status=%s",
				ErrWalletAddressReservationTerminal,
				req.MerchantID,
				req.DomainID,
				req.ProductID,
				req.UserID,
				req.Purpose,
				reservation.LifecycleStatus,
			)
		}
		return &reservation, true, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	return nil, false, err
}

func (r *WalletAddressRepo) nextHDIndexFromPool(ctx context.Context, tx *gorm.DB, merchantID, domainID uuid.UUID, hdAccountID uint32) (uint32, error) {
	maxIndex := uint32(0)
	for _, scan := range []struct {
		model any
		where string
		args  []any
	}{
		{model: &models.WalletAddressReservation{}, where: "merchant_id = ? AND domain_id = ?", args: []any{merchantID, domainID}},
		{model: &models.WalletAddress{}, where: "merchant_id = ? AND domain_id = ?", args: []any{merchantID, domainID}},
		{model: &models.Wallet{}, where: "merchant_id = ? AND domain_id = ?", args: []any{merchantID, domainID}},
	} {
		var candidate uint32
		if err := tx.WithContext(ctx).
			Model(scan.model).
			Where(scan.where, scan.args...).
			Select("COALESCE(MAX(hd_address_id), 0)").
			Scan(&candidate).Error; err != nil {
			return 0, err
		}
		if candidate > maxIndex {
			maxIndex = candidate
		}
	}
	if hdAccountID != 0 && tx.Migrator().HasTable(&models.WalletAddressGapScanCursor{}) {
		var candidate uint32
		if err := tx.WithContext(ctx).
			Model(&models.WalletAddressGapScanCursor{}).
			Where("hd_account_id = ?", hdAccountID).
			Select("COALESCE(MAX(highest_used_index), 0)").
			Scan(&candidate).Error; err != nil {
			return 0, err
		}
		if candidate > maxIndex {
			maxIndex = candidate
		}
	}
	if maxIndex == maxHDAddressIndex {
		return 0, ErrHDIndexExhausted
	}
	return maxIndex + 1, nil
}

func (r *WalletAddressRepo) UpsertWallet(ctx context.Context, wallet models.Wallet) error {
	if r == nil || r.db == nil {
		return errors.New("wallet address repository is not configured")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return r.UpsertWalletTx(ctx, tx, wallet)
	})
}

func (r *WalletAddressRepo) UpsertWalletTx(ctx context.Context, tx *gorm.DB, wallet models.Wallet) error {
	if tx == nil {
		return errors.New("wallet address transaction is not configured")
	}
	purpose := WalletAddressPurposeForWallet(wallet)
	decision := DecideWalletAddressReusePolicy(purpose, false)
	reservation, err := r.upsertReservationForWalletTx(ctx, tx, wallet, purpose, decision.Policy)
	if err != nil {
		return err
	}
	if !walletAddressReservationReusable(*reservation, time.Now().UTC()) {
		return nil
	}
	rows := WalletAddressRows(wallet, purpose, decision.Policy, models.WalletAddressStatusAssigned)
	for _, row := range rows {
		row.ReservationID = &reservation.ID
		if err := upsertWalletAddressRow(tx.WithContext(ctx), row); err != nil {
			return err
		}
	}
	return nil
}

func (r *WalletAddressRepo) AttachWalletToReservationTx(ctx context.Context, tx *gorm.DB, reservationID uuid.UUID, walletID uuid.UUID) error {
	if reservationID == uuid.Nil || walletID == uuid.Nil {
		return nil
	}
	now := time.Now().UTC()
	return tx.WithContext(ctx).
		Model(&models.WalletAddressReservation{}).
		Where("id = ?", reservationID).
		Updates(map[string]any{
			"wallet_id":        walletID,
			"lifecycle_status": models.WalletAddressStatusAssigned,
			"assigned_at":      now,
			"updated_at":       now,
		}).Error
}

func (r *WalletAddressRepo) upsertReservationForWalletTx(ctx context.Context, tx *gorm.DB, wallet models.Wallet, purpose string, reusePolicy string) (*models.WalletAddressReservation, error) {
	now := time.Now().UTC()
	req := WalletAddressReservationRequest{
		MerchantID:  wallet.MerchantID,
		DomainID:    wallet.DomainID,
		ProductID:   wallet.ProductID,
		UserID:      wallet.UserID,
		HDAccountID: wallet.HDAccountID,
		Purpose:     purpose,
		ReusePolicy: reusePolicy,
	}
	if existing, ok, err := r.findAnyReservationByOwner(ctx, tx, req); err != nil || ok {
		if err != nil {
			return nil, err
		}
		if !walletAddressReservationReusable(*existing, now) {
			return existing, nil
		}
		if existing.WalletID == nil || *existing.WalletID != wallet.ID || !walletAddressLifecycleStatusAtLeastAssigned(existing.LifecycleStatus) {
			walletID := wallet.ID
			if err := r.AttachWalletToReservationTx(ctx, tx, existing.ID, walletID); err != nil {
				return nil, err
			}
			existing.WalletID = &walletID
			existing.LifecycleStatus = models.WalletAddressStatusAssigned
			existing.AssignedAt = &now
		}
		return existing, nil
	}

	var existingByHD models.WalletAddressReservation
	err := tx.WithContext(ctx).
		Where("hd_account_id = ? AND hd_address_id = ?", wallet.HDAccountID, wallet.HDAddressId).
		First(&existingByHD).Error
	if err == nil {
		walletID := wallet.ID
		if err := tx.WithContext(ctx).Model(&existingByHD).Updates(map[string]any{
			"merchant_id":      wallet.MerchantID,
			"domain_id":        wallet.DomainID,
			"product_id":       wallet.ProductID,
			"user_id":          wallet.UserID,
			"purpose":          purpose,
			"reuse_policy":     reusePolicy,
			"wallet_id":        walletID,
			"lifecycle_status": models.WalletAddressStatusAssigned,
			"assigned_at":      now,
			"updated_at":       now,
		}).Error; err != nil {
			if isUniqueConstraintError(err) {
				if existing, ok, findErr := r.findReservationByOwner(ctx, tx, req); findErr != nil || ok {
					return existing, findErr
				}
			}
			return nil, err
		}
		existingByHD.WalletID = &walletID
		return &existingByHD, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	walletID := wallet.ID
	reservation := models.WalletAddressReservation{
		ID:              uuid.New(),
		MerchantID:      wallet.MerchantID,
		DomainID:        wallet.DomainID,
		ProductID:       wallet.ProductID,
		UserID:          wallet.UserID,
		HDAccountID:     wallet.HDAccountID,
		HDAddressID:     wallet.HDAddressId,
		Purpose:         purpose,
		LifecycleStatus: models.WalletAddressStatusAssigned,
		ReusePolicy:     reusePolicy,
		WalletID:        &walletID,
		ReservedAt:      now,
		AssignedAt:      &now,
	}
	if err := tx.WithContext(ctx).Create(&reservation).Error; err != nil {
		if isUniqueConstraintError(err) {
			if existing, ok, findErr := r.findReservationByOwner(ctx, tx, req); findErr != nil || ok {
				return existing, findErr
			}
			var existingByHD models.WalletAddressReservation
			findErr := tx.WithContext(ctx).
				Where("hd_account_id = ? AND hd_address_id = ?", wallet.HDAccountID, wallet.HDAddressId).
				First(&existingByHD).Error
			if findErr == nil {
				walletID := wallet.ID
				if updateErr := tx.WithContext(ctx).Model(&existingByHD).Updates(map[string]any{
					"merchant_id":      wallet.MerchantID,
					"domain_id":        wallet.DomainID,
					"product_id":       wallet.ProductID,
					"user_id":          wallet.UserID,
					"purpose":          purpose,
					"reuse_policy":     reusePolicy,
					"wallet_id":        walletID,
					"lifecycle_status": models.WalletAddressStatusAssigned,
					"assigned_at":      now,
					"updated_at":       now,
				}).Error; updateErr != nil {
					return nil, updateErr
				}
				existingByHD.WalletID = &walletID
				return &existingByHD, nil
			}
			if findErr != nil && !errors.Is(findErr, gorm.ErrRecordNotFound) {
				return nil, findErr
			}
		}
		return nil, err
	}
	return &reservation, nil
}

func (r *WalletAddressRepo) findAnyReservationByOwner(ctx context.Context, tx *gorm.DB, req WalletAddressReservationRequest) (*models.WalletAddressReservation, bool, error) {
	var reservation models.WalletAddressReservation
	err := tx.WithContext(ctx).
		Where(
			"merchant_id = ? AND domain_id = ? AND product_id = ? AND user_id = ? AND purpose = ?",
			req.MerchantID,
			req.DomainID,
			req.ProductID,
			req.UserID,
			req.Purpose,
		).
		First(&reservation).Error
	if err == nil {
		return &reservation, true, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	return nil, false, err
}

func WalletAddressRows(wallet models.Wallet, purpose string, reusePolicy string, status string) []models.WalletAddress {
	purpose = NormalizeWalletAddressPurpose(purpose)
	if strings.TrimSpace(reusePolicy) == "" {
		reusePolicy = DecideWalletAddressReusePolicy(purpose, false).Policy
	}
	if strings.TrimSpace(status) == "" {
		status = models.WalletAddressStatusGenerated
	}
	now := time.Now().UTC()
	lookupRows := WalletAddressLookupRows(wallet)
	rows := make([]models.WalletAddress, 0, len(lookupRows))
	for _, lookup := range lookupRows {
		row := models.WalletAddress{
			ID:                uuid.New(),
			ChainID:           lookup.ChainID,
			ChainName:         lookup.ChainName,
			Address:           lookup.Address,
			NormalizedAddress: lookup.NormalizedAddress,
			Asset:             lookup.Asset,
			MerchantID:        wallet.MerchantID,
			DomainID:          wallet.DomainID,
			WalletID:          wallet.ID,
			ProductID:         wallet.ProductID,
			UserID:            wallet.UserID,
			HDAccountID:       wallet.HDAccountID,
			HDAddressID:       wallet.HDAddressId,
			Purpose:           purpose,
			LifecycleStatus:   status,
			ReusePolicy:       reusePolicy,
			Source:            "wallet_columns",
		}
		switch status {
		case models.WalletAddressStatusReserved:
			row.ReservedAt = &now
		case models.WalletAddressStatusAssigned:
			row.AssignedAt = &now
		case models.WalletAddressStatusActive:
			row.ActivatedAt = &now
		case models.WalletAddressStatusUsed:
			row.UsedAt = &now
		}
		rows = append(rows, row)
	}
	return rows
}

func upsertWalletAddressRow(tx *gorm.DB, row models.WalletAddress) error {
	now := time.Now().UTC()
	row.Address = strings.TrimSpace(row.Address)
	row.NormalizedAddress = NormalizeWalletLookupAddress(row.ChainID, row.NormalizedAddress)
	if row.NormalizedAddress == "" {
		row.NormalizedAddress = NormalizeWalletLookupAddress(row.ChainID, row.Address)
	}
	if row.NormalizedAddress == "" {
		return nil
	}
	if row.ID == uuid.Nil {
		row.ID = uuid.New()
	}
	if row.ChainName == "" {
		row.ChainName = constants.ChainName(row.ChainID)
	}
	if row.Asset == "" {
		row.Asset = "native"
	}
	if row.Purpose == "" {
		row.Purpose = WalletAddressPurposeForProduct(row.ProductID, row.UserID)
	}
	row.Purpose = NormalizeWalletAddressPurpose(row.Purpose)
	if row.ReusePolicy == "" {
		row.ReusePolicy = DecideWalletAddressReusePolicy(row.Purpose, false).Policy
	}
	if row.LifecycleStatus == "" {
		row.LifecycleStatus = models.WalletAddressStatusGenerated
	}
	if row.Source == "" {
		row.Source = "wallet_columns"
	}

	var existing models.WalletAddress
	err := tx.Where("chain_id = ? AND normalized_address = ?", row.ChainID, row.NormalizedAddress).
		First(&existing).Error
	if err == nil {
		return updateExistingWalletAddressRow(tx, existing, row, now)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	row.CreatedAt = now
	row.UpdatedAt = now
	if err := tx.Create(&row).Error; err != nil {
		if !isUniqueConstraintError(err) {
			return err
		}
		return upsertWalletAddressRowAfterUniqueConflict(tx, row)
	}
	return nil
}

func upsertWalletAddressRowAfterUniqueConflict(tx *gorm.DB, row models.WalletAddress) error {
	now := time.Now().UTC()
	var existing models.WalletAddress
	err := tx.
		Where("chain_id = ? AND normalized_address = ?", row.ChainID, row.NormalizedAddress).
		Or("hd_account_id = ? AND hd_address_id = ? AND chain_id = ?", row.HDAccountID, row.HDAddressID, row.ChainID).
		First(&existing).Error
	if err != nil {
		return err
	}
	return updateExistingWalletAddressRow(tx, existing, row, now)
}

func updateExistingWalletAddressRow(tx *gorm.DB, existing models.WalletAddress, row models.WalletAddress, now time.Time) error {
	if existing.WalletID != row.WalletID {
		return fmt.Errorf(
			"%w: chain=%s normalized_address=%s existing_wallet=%s new_wallet=%s",
			ErrWalletAddressOwnershipConflict,
			constants.ChainName(row.ChainID),
			row.NormalizedAddress,
			existing.WalletID,
			row.WalletID,
		)
	}
	status := walletAddressMergedLifecycleStatus(existing.LifecycleStatus, row.LifecycleStatus)
	updates := map[string]any{
		"chain_name":         row.ChainName,
		"address":            row.Address,
		"asset":              row.Asset,
		"merchant_id":        row.MerchantID,
		"domain_id":          row.DomainID,
		"product_id":         row.ProductID,
		"user_id":            row.UserID,
		"hd_account_id":      row.HDAccountID,
		"hd_address_id":      row.HDAddressID,
		"purpose":            row.Purpose,
		"lifecycle_status":   status,
		"reuse_policy":       row.ReusePolicy,
		"source":             row.Source,
		"normalized_address": row.NormalizedAddress,
		"reservation_id":     row.ReservationID,
		"updated_at":         now,
	}
	if row.ReservedAt != nil && existing.ReservedAt == nil {
		updates["reserved_at"] = *row.ReservedAt
	}
	if row.AssignedAt != nil && existing.AssignedAt == nil {
		updates["assigned_at"] = *row.AssignedAt
	}
	if row.ActivatedAt != nil && existing.ActivatedAt == nil {
		updates["activated_at"] = *row.ActivatedAt
	}
	if row.UsedAt != nil && existing.UsedAt == nil {
		updates["used_at"] = *row.UsedAt
	}
	return tx.Model(&existing).Updates(updates).Error
}

func (r *WalletAddressRepo) BackfillWallets(ctx context.Context, batchSize int) (int, error) {
	if r == nil || r.db == nil {
		return 0, errors.New("wallet address repository is not configured")
	}
	if batchSize <= 0 || batchSize > 1000 {
		batchSize = 500
	}
	backfilled := 0
	var wallets []models.Wallet
	err := r.db.WithContext(ctx).FindInBatches(&wallets, batchSize, func(_ *gorm.DB, _ int) error {
		batchDB := r.db.WithContext(ctx).Session(&gorm.Session{NewDB: true})
		for _, wallet := range wallets {
			if err := r.UpsertWalletTx(ctx, batchDB, wallet); err != nil {
				return err
			}
			backfilled++
		}
		return nil
	}).Error
	if err != nil {
		return backfilled, err
	}
	if r.db.Migrator().HasTable(&models.WalletAddressLookup{}) {
		if err := r.backfillLookupRows(ctx, batchSize); err != nil {
			return backfilled, err
		}
	}
	return backfilled, nil
}

func (r *WalletAddressRepo) backfillLookupRows(ctx context.Context, batchSize int) error {
	var lookups []models.WalletAddressLookup
	return r.db.WithContext(ctx).FindInBatches(&lookups, batchSize, func(_ *gorm.DB, _ int) error {
		batchDB := r.db.WithContext(ctx).Session(&gorm.Session{NewDB: true})
		for _, lookup := range lookups {
			var wallet models.Wallet
			if err := batchDB.WithContext(ctx).First(&wallet, "id = ?", lookup.WalletID).Error; err != nil {
				return err
			}
			purpose := WalletAddressPurposeForWallet(wallet)
			decision := DecideWalletAddressReusePolicy(purpose, false)
			reservation, err := r.upsertReservationForWalletTx(ctx, batchDB, wallet, purpose, decision.Policy)
			if err != nil {
				return err
			}
			row := models.WalletAddress{
				ID:                uuid.New(),
				ChainID:           lookup.ChainID,
				ChainName:         lookup.ChainName,
				Address:           lookup.Address,
				NormalizedAddress: lookup.NormalizedAddress,
				Asset:             lookup.Asset,
				MerchantID:        lookup.MerchantID,
				DomainID:          lookup.DomainID,
				WalletID:          lookup.WalletID,
				ProductID:         lookup.ProductID,
				UserID:            lookup.UserID,
				HDAccountID:       wallet.HDAccountID,
				HDAddressID:       wallet.HDAddressId,
				Purpose:           purpose,
				LifecycleStatus:   models.WalletAddressStatusAssigned,
				ReusePolicy:       decision.Policy,
				Source:            lookup.Source,
				ReservationID:     &reservation.ID,
			}
			now := time.Now().UTC()
			row.AssignedAt = &now
			if err := upsertWalletAddressRow(batchDB.WithContext(ctx), row); err != nil {
				return err
			}
		}
		return nil
	}).Error
}

func (r *WalletAddressRepo) FindWallet(ctx context.Context, chainID constants.ChainID, address string) (*models.Wallet, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("wallet address repository is not configured")
	}
	normalized := NormalizeWalletLookupAddress(chainID, address)
	if normalized == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var row models.WalletAddress
	if err := r.db.WithContext(ctx).
		Where("chain_id = ? AND normalized_address = ?", chainID, normalized).
		Where("lifecycle_status NOT IN ?", walletAddressTerminalStatuses()).
		First(&row).Error; err != nil {
		return nil, err
	}
	var wallet models.Wallet
	if err := r.db.WithContext(ctx).
		Preload("Domain").
		First(&wallet, "id = ?", row.WalletID).Error; err != nil {
		return nil, err
	}
	return &wallet, nil
}

func (r *WalletAddressRepo) HasTerminalAddress(ctx context.Context, chainID constants.ChainID, address string) (bool, error) {
	if r == nil || r.db == nil {
		return false, errors.New("wallet address repository is not configured")
	}
	normalized := NormalizeWalletLookupAddress(chainID, address)
	if normalized == "" {
		return false, nil
	}
	var count int64
	err := r.db.WithContext(ctx).
		Model(&models.WalletAddress{}).
		Where("chain_id = ? AND normalized_address = ?", chainID, normalized).
		Where("lifecycle_status IN ?", walletAddressTerminalStatuses()).
		Count(&count).Error
	return count > 0, err
}

func (r *WalletAddressRepo) ValidateLookupParity(ctx context.Context) error {
	if r == nil || r.db == nil {
		return errors.New("wallet address repository is not configured")
	}
	if !r.db.Migrator().HasTable(&models.WalletAddressLookup{}) {
		return nil
	}
	var lookups []models.WalletAddressLookup
	return r.db.WithContext(ctx).FindInBatches(&lookups, 500, func(tx *gorm.DB, _ int) error {
		for _, lookup := range lookups {
			normalized := NormalizeWalletLookupAddress(lookup.ChainID, lookup.NormalizedAddress)
			var row models.WalletAddress
			err := tx.WithContext(ctx).
				Where("chain_id = ? AND normalized_address = ?", lookup.ChainID, normalized).
				First(&row).Error
			if err != nil {
				return fmt.Errorf("wallet address lookup parity missing chain=%s address=%s: %w", constants.ChainName(lookup.ChainID), normalized, err)
			}
			if row.WalletID != lookup.WalletID {
				return fmt.Errorf("wallet address lookup parity mismatch chain=%s address=%s lookup_wallet=%s pool_wallet=%s", constants.ChainName(lookup.ChainID), normalized, lookup.WalletID, row.WalletID)
			}
		}
		return nil
	}).Error
}

func (r *WalletAddressRepo) TransitionLifecycle(ctx context.Context, chainID constants.ChainID, address string, status string, at time.Time) error {
	if r == nil || r.db == nil {
		return errors.New("wallet address repository is not configured")
	}
	normalized := NormalizeWalletLookupAddress(chainID, address)
	if normalized == "" {
		return gorm.ErrRecordNotFound
	}
	status = strings.TrimSpace(status)
	if !walletAddressLifecycleStatusValid(status) {
		return fmt.Errorf("invalid wallet address lifecycle status %q", status)
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	updates := map[string]any{
		"lifecycle_status": status,
		"updated_at":       at,
	}
	switch status {
	case models.WalletAddressStatusActive:
		updates["activated_at"] = at
	case models.WalletAddressStatusUsed:
		updates["used_at"] = at
	case models.WalletAddressStatusExpired:
		updates["expires_at"] = at
	case models.WalletAddressStatusReleased:
		updates["released_at"] = at
	case models.WalletAddressStatusRetired:
		updates["retired_at"] = at
	}
	var row models.WalletAddress
	if err := r.db.WithContext(ctx).
		Where("chain_id = ? AND normalized_address = ?", chainID, normalized).
		First(&row).Error; err != nil {
		return err
	}
	if walletAddressLifecycleStatusTerminal(row.LifecycleStatus) && !walletAddressLifecycleStatusTerminal(status) {
		return gorm.ErrRecordNotFound
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&row).Updates(updates).Error; err != nil {
			return err
		}
		if row.ReservationID == nil {
			return nil
		}
		reservationUpdates := map[string]any{
			"lifecycle_status": status,
			"updated_at":       at,
		}
		switch status {
		case models.WalletAddressStatusAssigned:
			reservationUpdates["assigned_at"] = at
		case models.WalletAddressStatusExpired:
			reservationUpdates["expires_at"] = at
		case models.WalletAddressStatusReleased:
			reservationUpdates["released_at"] = at
		case models.WalletAddressStatusRetired:
			reservationUpdates["retired_at"] = at
		}
		return tx.Model(&models.WalletAddressReservation{}).
			Where("id = ?", *row.ReservationID).
			Updates(reservationUpdates).Error
	})
}

func (r *WalletAddressRepo) TransitionWalletLifecycle(ctx context.Context, chainID constants.ChainID, address string, status string, at time.Time) error {
	err := r.TransitionLifecycle(ctx, chainID, address, status, at)
	if errors.Is(err, gorm.ErrRecordNotFound) || isMissingWalletAddressTableError(err) {
		return nil
	}
	return err
}

func (r *WalletAddressRepo) ReleaseExpiredCheckoutAddress(ctx context.Context, chainID constants.ChainID, address string, at time.Time) error {
	if strings.TrimSpace(address) == "" {
		return nil
	}
	return r.TransitionWalletLifecycle(ctx, chainID, address, models.WalletAddressStatusExpired, at)
}

func (r *WalletAddressRepo) PersistGapScanResult(ctx context.Context, result WalletAddressGapScanResult) error {
	if r == nil || r.db == nil {
		return errors.New("wallet address repository is not configured")
	}
	result.Purpose = NormalizeWalletAddressPurpose(result.Purpose)
	if result.Lookahead == 0 {
		result.Lookahead = 20
	}
	if result.ScannedAt.IsZero() {
		result.ScannedAt = time.Now().UTC()
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		anomalies := append([]WalletAddressGapScanAnomalyInput{}, result.Anomalies...)
		highestUsed := uint32(0)
		for _, index := range result.UsedIndexes {
			if index > highestUsed {
				highestUsed = index
			}
			update := tx.WithContext(ctx).
				Model(&models.WalletAddress{}).
				Where("chain_id = ? AND hd_account_id = ? AND hd_address_id = ?", result.ChainID, result.HDAccountID, index).
				Updates(map[string]any{
					"lifecycle_status": models.WalletAddressStatusUsed,
					"used_at":          result.ScannedAt,
					"updated_at":       result.ScannedAt,
				})
			if update.Error != nil {
				return update.Error
			}
			if update.RowsAffected == 0 {
				anomalies = append(anomalies, WalletAddressGapScanAnomalyInput{
					HDAddressID: index,
					Category:    models.WalletAddressGapAnomalyUsedUnreserved,
					Detail:      "used index observed without a wallet_addresses row",
				})
			}
		}

		mergedUsed, err := r.mergeGapScanUsedIndexes(ctx, tx, result)
		if err != nil {
			return err
		}
		for _, index := range result.UsedIndexes {
			mergedUsed[index] = struct{}{}
		}
		usedIndexes := sortedUint32Keys(mergedUsed)
		usedJSON, err := json.Marshal(usedIndexes)
		if err != nil {
			return err
		}
		lastAnomaly := ""
		if len(anomalies) > 0 {
			lastAnomaly = anomalies[len(anomalies)-1].Category
		}
		if err := r.upsertGapScanCursorTx(ctx, tx, result, highestUsed, string(usedJSON), int64(len(anomalies)), lastAnomaly); err != nil {
			return err
		}
		for _, anomaly := range anomalies {
			if strings.TrimSpace(anomaly.Category) == "" {
				anomaly.Category = models.WalletAddressGapAnomalyScanError
			}
			row := models.WalletAddressGapScanAnomaly{
				ID:          uuid.New(),
				ChainID:     result.ChainID,
				ChainName:   constants.ChainName(result.ChainID),
				HDAccountID: result.HDAccountID,
				HDAddressID: anomaly.HDAddressID,
				Purpose:     result.Purpose,
				Address:     strings.TrimSpace(anomaly.Address),
				Category:    anomaly.Category,
				Detail:      strings.TrimSpace(anomaly.Detail),
				DetectedAt:  result.ScannedAt,
			}
			if err := tx.WithContext(ctx).Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *WalletAddressRepo) ScanGapLimit(ctx context.Context, req WalletAddressGapScanRequest) (*WalletAddressGapScanResult, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("wallet address repository is not configured")
	}
	if req.Derive == nil || req.IsUsed == nil {
		return nil, errors.New("wallet address gap scan requires derive and usage probes")
	}
	req.Purpose = NormalizeWalletAddressPurpose(req.Purpose)
	lookahead := req.Lookahead
	if lookahead == 0 {
		lookahead = req.Config.LookaheadForChain(req.ChainID)
	}
	if lookahead == 0 {
		lookahead = 20
	}
	if req.ScannedAt.IsZero() {
		req.ScannedAt = time.Now().UTC()
	}
	result := WalletAddressGapScanResult{
		ChainID:          req.ChainID,
		HDAccountID:      req.HDAccountID,
		Purpose:          req.Purpose,
		Lookahead:        lookahead,
		LastScannedIndex: req.StartIndex,
		ScannedAt:        req.ScannedAt,
	}
	for offset := uint32(0); offset < lookahead; offset++ {
		if req.StartIndex > maxHDAddressIndex-offset {
			return nil, ErrHDIndexExhausted
		}
		index := req.StartIndex + offset
		result.LastScannedIndex = index
		address, err := req.Derive(ctx, index)
		if err != nil {
			result.Anomalies = append(result.Anomalies, WalletAddressGapScanAnomalyInput{
				HDAddressID: index,
				Category:    models.WalletAddressGapAnomalyScanError,
				Detail:      err.Error(),
			})
			continue
		}
		used, err := req.IsUsed(ctx, address)
		if err != nil {
			result.Anomalies = append(result.Anomalies, WalletAddressGapScanAnomalyInput{
				HDAddressID: index,
				Address:     address,
				Category:    models.WalletAddressGapAnomalyScanError,
				Detail:      err.Error(),
			})
			continue
		}
		if used {
			result.UsedIndexes = append(result.UsedIndexes, index)
		}
	}
	if err := r.PersistGapScanResult(ctx, result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c WalletAddressGapScanConfig) LookaheadForChain(chainID constants.ChainID) uint32 {
	if c.ChainLookahead != nil {
		if lookahead := c.ChainLookahead[chainID]; lookahead > 0 {
			return lookahead
		}
	}
	return c.DefaultLookahead
}

func (r *WalletAddressRepo) upsertGapScanCursorTx(ctx context.Context, tx *gorm.DB, result WalletAddressGapScanResult, highestUsed uint32, usedJSON string, anomalyCount int64, lastAnomaly string) error {
	now := time.Now().UTC()
	var existing models.WalletAddressGapScanCursor
	err := tx.WithContext(ctx).
		Where("chain_id = ? AND hd_account_id = ? AND purpose = ?", result.ChainID, result.HDAccountID, result.Purpose).
		First(&existing).Error
	if err == nil {
		if highestUsed < existing.HighestUsedIndex {
			highestUsed = existing.HighestUsedIndex
		}
		if result.LastScannedIndex < existing.LastScannedIndex {
			result.LastScannedIndex = existing.LastScannedIndex
		}
		anomalyCount += existing.AnomalyCount
		if lastAnomaly == "" {
			lastAnomaly = existing.LastAnomaly
		}
		return tx.WithContext(ctx).Model(&existing).Updates(map[string]any{
			"chain_name":                   constants.ChainName(result.ChainID),
			"lookahead":                    result.Lookahead,
			"last_scanned_index":           result.LastScannedIndex,
			"highest_used_index":           highestUsed,
			"discovered_used_indexes_json": usedJSON,
			"anomaly_count":                anomalyCount,
			"last_anomaly":                 lastAnomaly,
			"scanned_at":                   result.ScannedAt,
			"updated_at":                   now,
		}).Error
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	cursor := models.WalletAddressGapScanCursor{
		ID:                        uuid.New(),
		ChainID:                   result.ChainID,
		ChainName:                 constants.ChainName(result.ChainID),
		HDAccountID:               result.HDAccountID,
		Purpose:                   result.Purpose,
		Lookahead:                 result.Lookahead,
		LastScannedIndex:          result.LastScannedIndex,
		HighestUsedIndex:          highestUsed,
		DiscoveredUsedIndexesJSON: usedJSON,
		AnomalyCount:              anomalyCount,
		LastAnomaly:               lastAnomaly,
		ScannedAt:                 result.ScannedAt,
	}
	return tx.WithContext(ctx).Create(&cursor).Error
}

func (r *WalletAddressRepo) mergeGapScanUsedIndexes(ctx context.Context, tx *gorm.DB, result WalletAddressGapScanResult) (map[uint32]struct{}, error) {
	merged := make(map[uint32]struct{}, len(result.UsedIndexes))
	var existing models.WalletAddressGapScanCursor
	err := tx.WithContext(ctx).
		Where("chain_id = ? AND hd_account_id = ? AND purpose = ?", result.ChainID, result.HDAccountID, result.Purpose).
		First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return merged, nil
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(existing.DiscoveredUsedIndexesJSON) == "" {
		return merged, nil
	}
	var previous []uint32
	if err := json.Unmarshal([]byte(existing.DiscoveredUsedIndexesJSON), &previous); err != nil {
		return nil, err
	}
	for _, index := range previous {
		merged[index] = struct{}{}
	}
	return merged, nil
}

func sortedUint32Keys(values map[uint32]struct{}) []uint32 {
	out := make([]uint32, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func walletAddressReservationReusable(reservation models.WalletAddressReservation, now time.Time) bool {
	if walletAddressLifecycleStatusTerminal(reservation.LifecycleStatus) {
		return false
	}
	return reservation.ExpiresAt == nil || reservation.ExpiresAt.After(now)
}

func walletAddressLifecycleStatusAtLeastAssigned(status string) bool {
	switch strings.TrimSpace(status) {
	case models.WalletAddressStatusAssigned, models.WalletAddressStatusActive, models.WalletAddressStatusUsed:
		return true
	default:
		return false
	}
}

func walletAddressLifecycleStatusValid(status string) bool {
	switch strings.TrimSpace(status) {
	case models.WalletAddressStatusGenerated,
		models.WalletAddressStatusReserved,
		models.WalletAddressStatusAssigned,
		models.WalletAddressStatusActive,
		models.WalletAddressStatusUsed,
		models.WalletAddressStatusExpired,
		models.WalletAddressStatusReleased,
		models.WalletAddressStatusRetired:
		return true
	default:
		return false
	}
}

func walletAddressLifecycleStatusTerminal(status string) bool {
	switch strings.TrimSpace(status) {
	case models.WalletAddressStatusExpired, models.WalletAddressStatusReleased, models.WalletAddressStatusRetired:
		return true
	default:
		return false
	}
}

func walletAddressTerminalStatuses() []string {
	return []string{
		models.WalletAddressStatusExpired,
		models.WalletAddressStatusReleased,
		models.WalletAddressStatusRetired,
	}
}

func walletAddressMergedLifecycleStatus(existing, incoming string) string {
	existing = strings.TrimSpace(existing)
	incoming = strings.TrimSpace(incoming)
	if incoming == "" {
		return existing
	}
	if walletAddressLifecycleStatusTerminal(existing) {
		return existing
	}
	if existing == models.WalletAddressStatusUsed && incoming != models.WalletAddressStatusRetired {
		return existing
	}
	if existing == models.WalletAddressStatusActive && (incoming == models.WalletAddressStatusAssigned || incoming == models.WalletAddressStatusReserved || incoming == models.WalletAddressStatusGenerated) {
		return existing
	}
	if existing == models.WalletAddressStatusAssigned && (incoming == models.WalletAddressStatusReserved || incoming == models.WalletAddressStatusGenerated) {
		return existing
	}
	return incoming
}

func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "constraint failed") ||
		strings.Contains(msg, "unique constraint failed")
}
