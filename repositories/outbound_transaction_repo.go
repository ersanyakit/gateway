package repositories

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrOutboundTransactionInvalid      = errors.New("invalid outbound transaction")
	ErrOutboundResourceAlreadyReserved = errors.New("outbound chain resource already reserved")
)

type OutboundTransactionRepo struct {
	db *gorm.DB
}

func NewOutboundTransactionRepo(db *gorm.DB) *OutboundTransactionRepo {
	return &OutboundTransactionRepo{db: db}
}

func (r *OutboundTransactionRepo) DB() *gorm.DB { return r.db }

func (r *OutboundTransactionRepo) CountByStatus(ctx context.Context, statuses ...string) (map[string]int64, error) {
	counts := make(map[string]int64, len(statuses))
	for _, status := range statuses {
		counts[status] = 0
	}
	if r == nil || r.db == nil {
		return counts, gorm.ErrInvalidDB
	}
	if len(statuses) == 0 {
		return counts, nil
	}
	type row struct {
		Status string
		Count  int64
	}
	var rows []row
	if err := r.db.WithContext(ctx).
		Model(&models.OutboundTransaction{}).
		Select("status, COUNT(*) AS count").
		Where("status IN ?", statuses).
		Group("status").
		Find(&rows).Error; err != nil {
		return counts, err
	}
	for _, row := range rows {
		counts[row.Status] = row.Count
	}
	return counts, nil
}

type OutboundTransactionCreate struct {
	IdempotencyKey string
	ResourceType   string
	ResourceID     uuid.UUID
	MerchantID     uuid.UUID
	DomainID       *uuid.UUID
	WalletID       uuid.UUID
	ChainID        constants.ChainID
	ChainName      string
	Token          *string
	Symbol         string
	Decimals       uint8
	AmountRaw      string
	ToAddress      string
	FeePolicyJSON  string
	ActorID        string
	CorrelationID  string
	NextRunAt      *time.Time
	MaxAttempts    uint
}

func (r *OutboundTransactionRepo) Create(ctx context.Context, params OutboundTransactionCreate) (*models.OutboundTransaction, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, gorm.ErrInvalidDB
	}
	prepared, err := prepareOutboundTransaction(params)
	if err != nil {
		return nil, false, err
	}

	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&prepared)
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected == 1 {
		return &prepared, true, nil
	}

	var existing models.OutboundTransaction
	if err := r.db.WithContext(ctx).First(&existing, "idempotency_key = ?", prepared.IdempotencyKey).Error; err != nil {
		return nil, false, err
	}
	return &existing, false, nil
}

func (r *OutboundTransactionRepo) CreateForWithdrawal(ctx context.Context, request models.WithdrawalRequest, actorID string) (*models.OutboundTransaction, bool, error) {
	chainID, ok := outboundRepoChainIDFromName(request.Chain)
	if !ok {
		return nil, false, fmt.Errorf("%w: unsupported withdrawal chain %q", ErrOutboundTransactionInvalid, request.Chain)
	}
	return r.Create(ctx, OutboundTransactionCreate{
		IdempotencyKey: fmt.Sprintf("%s:%s:primary", models.OutboundResourceWithdrawal, request.ID),
		ResourceType:   models.OutboundResourceWithdrawal,
		ResourceID:     request.ID,
		MerchantID:     request.MerchantID,
		DomainID:       request.DomainID,
		WalletID:       request.WalletID,
		ChainID:        chainID,
		ChainName:      constants.ChainName(chainID),
		Token:          normalizedOptionalString(request.Token),
		Symbol:         request.Symbol,
		Decimals:       request.Decimals,
		AmountRaw:      request.AmountRaw,
		ToAddress:      request.ToAddress,
		ActorID:        actorID,
		CorrelationID:  request.CorrelationID,
	})
}

func (r *OutboundTransactionRepo) CreateForRefund(ctx context.Context, refund models.Refund, actorID string) (*models.OutboundTransaction, bool, error) {
	chainID, ok := outboundRepoChainIDFromName(refund.Chain)
	if !ok {
		return nil, false, fmt.Errorf("%w: unsupported refund chain %q", ErrOutboundTransactionInvalid, refund.Chain)
	}
	if refund.WalletID == nil || *refund.WalletID == uuid.Nil {
		return nil, false, fmt.Errorf("%w: refund wallet id is required", ErrOutboundTransactionInvalid)
	}
	domainID := refund.DomainID
	return r.Create(ctx, OutboundTransactionCreate{
		IdempotencyKey: fmt.Sprintf("%s:%s:primary", models.OutboundResourceRefund, refund.ID),
		ResourceType:   models.OutboundResourceRefund,
		ResourceID:     refund.ID,
		MerchantID:     refund.MerchantID,
		DomainID:       &domainID,
		WalletID:       *refund.WalletID,
		ChainID:        chainID,
		ChainName:      constants.ChainName(chainID),
		Token:          normalizedOptionalString(refund.Token),
		Symbol:         refund.Symbol,
		Decimals:       refund.Decimals,
		AmountRaw:      refund.AmountRaw,
		ToAddress:      refund.ToAddress,
		ActorID:        actorID,
		CorrelationID:  refund.CorrelationID,
	})
}

func (r *OutboundTransactionRepo) CreateForSweepJob(ctx context.Context, job models.SweepJob, txModel models.Transaction, reserveAddress string) (*models.OutboundTransaction, bool, error) {
	return r.Create(ctx, OutboundTransactionCreate{
		IdempotencyKey: fmt.Sprintf("%s:%s:primary", models.OutboundResourceSweepJob, job.ID),
		ResourceType:   models.OutboundResourceSweepJob,
		ResourceID:     job.ID,
		MerchantID:     job.MerchantID,
		WalletID:       job.WalletID,
		ChainID:        job.ChainID,
		ChainName:      constants.ChainName(job.ChainID),
		Token:          normalizedOptionalString(job.Token),
		Symbol:         txModel.Symbol,
		Decimals:       txModel.Decimals,
		AmountRaw:      txModel.Amount,
		ToAddress:      reserveAddress,
		ActorID:        "sweep_worker",
		CorrelationID:  "sweep_job:" + job.ID.String(),
	})
}

func (r *OutboundTransactionRepo) ClaimDue(ctx context.Context, resourceTypes []string, limit int, lockFor time.Duration) ([]models.OutboundTransaction, error) {
	if r == nil || r.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	if lockFor <= 0 {
		lockFor = 90 * time.Second
	}
	resourceTypes = normalizeOutboundResourceTypes(resourceTypes)
	if len(resourceTypes) == 0 {
		resourceTypes = []string{models.OutboundResourceWithdrawal, models.OutboundResourceRefund}
	}

	now := time.Now()
	lockUntil := now.Add(lockFor)
	var rows []models.OutboundTransaction
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.
			Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("resource_type IN ?", resourceTypes).
			Where("status IN ?", []string{models.OutboundStatusPrepared, models.OutboundStatusSigned, models.OutboundStatusBroadcastAttempted, models.OutboundStatusFailed}).
			Where("(next_run_at IS NULL OR next_run_at <= ?)", now).
			Where("(locked_until IS NULL OR locked_until < ?)", now).
			Order("created_at ASC").
			Limit(limit).
			Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		ids := make([]uuid.UUID, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		return tx.Model(&models.OutboundTransaction{}).
			Where("id IN ?", ids).
			Updates(map[string]any{
				"locked_until": &lockUntil,
				"updated_at":   now,
			}).Error
	})
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].LockedUntil = &lockUntil
	}
	return rows, nil
}

func (r *OutboundTransactionRepo) MarkBroadcastAttempted(ctx context.Context, id uuid.UUID) error {
	now := time.Now()
	return r.updateState(ctx, id, []string{models.OutboundStatusPrepared, models.OutboundStatusSigned, models.OutboundStatusFailed}, map[string]any{
		"status":                 models.OutboundStatusBroadcastAttempted,
		"attempts":               gorm.Expr("attempts + 1"),
		"broadcast_attempted_at": &now,
		"error_category":         "",
		"error_detail":           "",
		"updated_at":             now,
	})
}

func (r *OutboundTransactionRepo) MarkBroadcasted(ctx context.Context, id uuid.UUID, txHash string) error {
	txHash = strings.TrimSpace(txHash)
	if txHash == "" {
		return ErrTxHashRequired
	}
	now := time.Now()
	return r.updateState(ctx, id, []string{models.OutboundStatusPrepared, models.OutboundStatusSigned, models.OutboundStatusBroadcastAttempted, models.OutboundStatusBroadcasted}, map[string]any{
		"status":         models.OutboundStatusBroadcasted,
		"tx_hash":        txHash,
		"broadcasted_at": &now,
		"locked_until":   nil,
		"next_run_at":    nil,
		"error_category": "",
		"error_detail":   "",
		"updated_at":     now,
	})
}

func (r *OutboundTransactionRepo) MarkNeedsOperatorAction(ctx context.Context, id uuid.UUID, category string, detail string) error {
	now := time.Now()
	return r.updateState(ctx, id, nil, map[string]any{
		"status":         models.OutboundStatusNeedsOperatorAction,
		"error_category": boundedOutboundText(category, 80),
		"error_detail":   boundedOutboundText(detail, 1000),
		"locked_until":   nil,
		"next_run_at":    nil,
		"updated_at":     now,
	})
}

func (r *OutboundTransactionRepo) MarkFailed(ctx context.Context, id uuid.UUID, err error, retryAfter time.Duration) error {
	detail := ""
	if err != nil {
		detail = err.Error()
	}
	now := time.Now()
	var nextRunAt *time.Time
	status := models.OutboundStatusFailed
	if retryAfter > 0 {
		next := now.Add(retryAfter)
		nextRunAt = &next
	} else {
		status = models.OutboundStatusNeedsOperatorAction
	}
	return r.updateState(ctx, id, nil, map[string]any{
		"status":         status,
		"error_category": outboundErrorCategory(err),
		"error_detail":   boundedOutboundText(detail, 1000),
		"locked_until":   nil,
		"next_run_at":    nextRunAt,
		"updated_at":     now,
	})
}

// DeferForNetworkState releases a claimed transaction without consuming a
// broadcast attempt. It is used while an administrator has outbound activity
// disabled for the transaction's network.
func (r *OutboundTransactionRepo) DeferForNetworkState(ctx context.Context, id uuid.UUID, detail string, retryAfter time.Duration) error {
	if retryAfter <= 0 {
		retryAfter = 30 * time.Second
	}
	now := time.Now()
	next := now.Add(retryAfter)
	return r.updateState(ctx, id, []string{models.OutboundStatusPrepared, models.OutboundStatusSigned, models.OutboundStatusFailed}, map[string]any{
		"error_category": "network_maintenance",
		"error_detail":   boundedOutboundText(detail, 1000),
		"locked_until":   nil,
		"next_run_at":    &next,
		"updated_at":     now,
	})
}

func (r *OutboundTransactionRepo) MarkFinalized(ctx context.Context, id uuid.UUID) error {
	now := time.Now()
	return r.updateState(ctx, id, []string{models.OutboundStatusBroadcasted, models.OutboundStatusConfirming}, map[string]any{
		"status":       models.OutboundStatusFinalized,
		"finalized_at": &now,
		"updated_at":   now,
	})
}

func (r *OutboundTransactionRepo) MarkResourceFinalized(ctx context.Context, resourceType string, resourceID uuid.UUID) error {
	if r == nil || r.db == nil {
		return gorm.ErrInvalidDB
	}
	now := time.Now()
	return r.updateResourceState(ctx, resourceType, resourceID, []string{models.OutboundStatusBroadcasted, models.OutboundStatusConfirming, models.OutboundStatusFinalized}, map[string]any{
		"status":         models.OutboundStatusFinalized,
		"finalized_at":   &now,
		"locked_until":   nil,
		"next_run_at":    nil,
		"error_category": "",
		"error_detail":   "",
		"updated_at":     now,
	})
}

func (r *OutboundTransactionRepo) MarkResourceTerminalFailed(ctx context.Context, resourceType string, resourceID uuid.UUID, err error) error {
	if r == nil || r.db == nil {
		return gorm.ErrInvalidDB
	}
	detail := ""
	if err != nil {
		detail = err.Error()
	}
	now := time.Now()
	return r.updateResourceState(ctx, resourceType, resourceID, []string{models.OutboundStatusBroadcasted, models.OutboundStatusConfirming, models.OutboundStatusFailed}, map[string]any{
		"status":         models.OutboundStatusFailed,
		"finalized_at":   &now,
		"locked_until":   nil,
		"next_run_at":    nil,
		"error_category": "terminal_failed",
		"error_detail":   boundedOutboundText(detail, 1000),
		"updated_at":     now,
	})
}

func (r *OutboundTransactionRepo) CreateReplacementIntent(ctx context.Context, parentID uuid.UUID, reason string) (*models.OutboundTransaction, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, gorm.ErrInvalidDB
	}
	var parent models.OutboundTransaction
	if err := r.db.WithContext(ctx).First(&parent, "id = ?", parentID).Error; err != nil {
		return nil, false, err
	}
	replacementID := uuid.New()
	params := OutboundTransactionCreate{
		IdempotencyKey: fmt.Sprintf("%s:%s:replacement:%s", parent.ResourceType, parent.ResourceID, replacementID),
		ResourceType:   parent.ResourceType,
		ResourceID:     parent.ResourceID,
		MerchantID:     parent.MerchantID,
		DomainID:       parent.DomainID,
		WalletID:       parent.WalletID,
		ChainID:        parent.ChainID,
		ChainName:      parent.ChainName,
		Token:          parent.Token,
		Symbol:         parent.Symbol,
		Decimals:       parent.Decimals,
		AmountRaw:      parent.AmountRaw,
		ToAddress:      parent.ToAddress,
		ActorID:        parent.ActorID,
		CorrelationID:  parent.CorrelationID,
	}
	child, created, err := r.Create(ctx, params)
	if err != nil {
		return nil, false, err
	}
	updates := map[string]any{
		"status":                models.OutboundStatusNeedsOperatorAction,
		"replacement_parent_id": &parent.ID,
		"replacement_reason":    boundedOutboundText(reason, 160),
		"replaces_tx_hash":      parent.TxHash,
		"error_category":        "replacement_requires_operator",
		"error_detail":          "chain replacement broadcast is not automated by the current chain interface",
	}
	if err := r.db.WithContext(ctx).Model(&models.OutboundTransaction{}).Where("id = ?", child.ID).Updates(updates).Error; err != nil {
		return nil, false, err
	}
	if err := r.db.WithContext(ctx).Model(&models.OutboundTransaction{}).Where("id = ?", parent.ID).Update("status", models.OutboundStatusReplaced).Error; err != nil {
		return nil, false, err
	}
	if err := r.db.WithContext(ctx).First(child, "id = ?", child.ID).Error; err != nil {
		return nil, false, err
	}
	return child, created, nil
}

func (r *OutboundTransactionRepo) updateState(ctx context.Context, id uuid.UUID, allowed []string, updates map[string]any) error {
	if r == nil || r.db == nil {
		return gorm.ErrInvalidDB
	}
	q := r.db.WithContext(ctx).Model(&models.OutboundTransaction{}).Where("id = ?", id)
	if len(allowed) > 0 {
		q = q.Where("status IN ?", allowed)
	}
	result := q.Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *OutboundTransactionRepo) updateResourceState(ctx context.Context, resourceType string, resourceID uuid.UUID, allowed []string, updates map[string]any) error {
	resourceType = strings.ToLower(strings.TrimSpace(resourceType))
	if resourceType == "" || resourceID == uuid.Nil {
		return ErrOutboundTransactionInvalid
	}
	q := r.db.WithContext(ctx).
		Model(&models.OutboundTransaction{}).
		Where("resource_type = ? AND resource_id = ?", resourceType, resourceID)
	if len(allowed) > 0 {
		q = q.Where("status IN ?", allowed)
	}
	result := q.Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

type OutboundResourceReservationRequest struct {
	OutboundTransactionID uuid.UUID
	ResourceType          string
	ChainID               constants.ChainID
	ChainName             string
	WalletID              uuid.UUID
	WalletAddress         string
	OwnerType             string
	OwnerID               uuid.UUID
	Intent                string
	Nonce                 *uint64
	UTXOTxID              string
	UTXOVout              *uint32
	UTXOValueRaw          string
	LeaseFor              time.Duration
}

func (r *OutboundTransactionRepo) ReserveSequence(ctx context.Context, outbound models.OutboundTransaction, walletAddress string, leaseFor time.Duration) (*models.OutboundChainResourceReservation, bool, error) {
	return r.ReserveResource(ctx, OutboundResourceReservationRequest{
		OutboundTransactionID: outbound.ID,
		ResourceType:          models.OutboundResourceReservationSequence,
		ChainID:               outbound.ChainID,
		ChainName:             outbound.ChainName,
		WalletID:              outbound.WalletID,
		WalletAddress:         walletAddress,
		OwnerType:             outbound.ResourceType,
		OwnerID:               outbound.ResourceID,
		Intent:                outbound.ResourceType + ":broadcast",
		LeaseFor:              leaseFor,
	})
}

func (r *OutboundTransactionRepo) ReserveResource(ctx context.Context, req OutboundResourceReservationRequest) (*models.OutboundChainResourceReservation, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, gorm.ErrInvalidDB
	}
	prepared, err := prepareOutboundResourceReservation(req)
	if err != nil {
		return nil, false, err
	}

	var out models.OutboundChainResourceReservation
	created := false
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", prepared.ResourceKey).Error; err != nil {
			return err
		}
		var existing models.OutboundChainResourceReservation
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("resource_key = ? AND status IN ?", prepared.ResourceKey, blockingReservationStatuses(prepared.ResourceType)).
			Order("created_at ASC").
			First(&existing).Error
		if err == nil {
			if existing.OutboundTransactionID == prepared.OutboundTransactionID {
				out = existing
				return nil
			}
			return ErrOutboundResourceAlreadyReserved
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Create(&prepared).Error; err != nil {
			return err
		}
		out = prepared
		created = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return &out, created, nil
}

func (r *OutboundTransactionRepo) ConsumeResource(ctx context.Context, reservationID uuid.UUID, txHash string) error {
	txHash = strings.TrimSpace(txHash)
	now := time.Now()
	return r.db.WithContext(ctx).Model(&models.OutboundChainResourceReservation{}).
		Where("id = ? AND status = ?", reservationID, models.OutboundResourceReservationReserved).
		Updates(map[string]any{
			"status":      models.OutboundResourceReservationConsumed,
			"consumed_at": &now,
			"tx_hash":     txHash,
			"updated_at":  now,
		}).Error
}

func (r *OutboundTransactionRepo) ReleaseResource(ctx context.Context, reservationID uuid.UUID) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&models.OutboundChainResourceReservation{}).
		Where("id = ? AND status = ?", reservationID, models.OutboundResourceReservationReserved).
		Updates(map[string]any{
			"status":      models.OutboundResourceReservationReleased,
			"released_at": &now,
			"updated_at":  now,
		}).Error
}

func prepareOutboundTransaction(params OutboundTransactionCreate) (models.OutboundTransaction, error) {
	resourceType := strings.ToLower(strings.TrimSpace(params.ResourceType))
	if resourceType == "" || params.ResourceID == uuid.Nil || params.MerchantID == uuid.Nil || params.WalletID == uuid.Nil {
		return models.OutboundTransaction{}, fmt.Errorf("%w: resource, merchant and wallet are required", ErrOutboundTransactionInvalid)
	}
	if !constants.IsSupportedChainID(params.ChainID) {
		return models.OutboundTransaction{}, fmt.Errorf("%w: supported chain is required", ErrOutboundTransactionInvalid)
	}
	amountRaw := strings.TrimSpace(params.AmountRaw)
	toAddress := strings.TrimSpace(params.ToAddress)
	if amountRaw == "" || toAddress == "" {
		return models.OutboundTransaction{}, fmt.Errorf("%w: amount and destination are required", ErrOutboundTransactionInvalid)
	}
	key := strings.TrimSpace(params.IdempotencyKey)
	if key == "" {
		key = fmt.Sprintf("%s:%s:primary", resourceType, params.ResourceID)
	}
	chainName := strings.TrimSpace(params.ChainName)
	if chainName == "" {
		chainName = constants.ChainName(params.ChainID)
	}
	maxAttempts := params.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = 5
	}
	return models.OutboundTransaction{
		ID:             uuid.New(),
		IdempotencyKey: key,
		ResourceType:   resourceType,
		ResourceID:     params.ResourceID,
		MerchantID:     params.MerchantID,
		DomainID:       params.DomainID,
		WalletID:       params.WalletID,
		ChainID:        params.ChainID,
		ChainName:      chainName,
		Token:          normalizedOptionalString(params.Token),
		Symbol:         strings.ToUpper(strings.TrimSpace(params.Symbol)),
		Decimals:       params.Decimals,
		AmountRaw:      amountRaw,
		ToAddress:      toAddress,
		Status:         models.OutboundStatusPrepared,
		MaxAttempts:    maxAttempts,
		NextRunAt:      params.NextRunAt,
		FeePolicyJSON:  strings.TrimSpace(params.FeePolicyJSON),
		ActorID:        strings.TrimSpace(params.ActorID),
		CorrelationID:  strings.TrimSpace(params.CorrelationID),
	}, nil
}

func prepareOutboundResourceReservation(req OutboundResourceReservationRequest) (models.OutboundChainResourceReservation, error) {
	resourceType := strings.ToLower(strings.TrimSpace(req.ResourceType))
	if req.OutboundTransactionID == uuid.Nil || resourceType == "" || req.OwnerID == uuid.Nil || req.WalletID == uuid.Nil {
		return models.OutboundChainResourceReservation{}, fmt.Errorf("%w: outbound, resource owner and wallet are required", ErrOutboundTransactionInvalid)
	}
	if !constants.IsSupportedChainID(req.ChainID) {
		return models.OutboundChainResourceReservation{}, fmt.Errorf("%w: supported chain is required", ErrOutboundTransactionInvalid)
	}
	walletAddress := strings.TrimSpace(req.WalletAddress)
	if walletAddress == "" {
		return models.OutboundChainResourceReservation{}, fmt.Errorf("%w: wallet address is required", ErrOutboundTransactionInvalid)
	}
	key, err := outboundResourceKey(req)
	if err != nil {
		return models.OutboundChainResourceReservation{}, err
	}
	var leaseExpiresAt *time.Time
	if req.LeaseFor > 0 {
		expires := time.Now().Add(req.LeaseFor)
		leaseExpiresAt = &expires
	}
	chainName := strings.TrimSpace(req.ChainName)
	if chainName == "" {
		chainName = constants.ChainName(req.ChainID)
	}
	return models.OutboundChainResourceReservation{
		ID:                    uuid.New(),
		OutboundTransactionID: req.OutboundTransactionID,
		ResourceType:          resourceType,
		ResourceKey:           key,
		Status:                models.OutboundResourceReservationReserved,
		ChainID:               req.ChainID,
		ChainName:             chainName,
		WalletID:              req.WalletID,
		WalletAddress:         walletAddress,
		OwnerType:             strings.ToLower(strings.TrimSpace(req.OwnerType)),
		OwnerID:               req.OwnerID,
		Intent:                strings.TrimSpace(req.Intent),
		Nonce:                 req.Nonce,
		UTXOTxID:              strings.TrimSpace(req.UTXOTxID),
		UTXOVout:              req.UTXOVout,
		UTXOValueRaw:          strings.TrimSpace(req.UTXOValueRaw),
		LeaseExpiresAt:        leaseExpiresAt,
	}, nil
}

func outboundResourceKey(req OutboundResourceReservationRequest) (string, error) {
	chain := constants.ChainName(req.ChainID)
	wallet := strings.ToLower(strings.TrimSpace(req.WalletAddress))
	switch strings.ToLower(strings.TrimSpace(req.ResourceType)) {
	case models.OutboundResourceReservationSequence:
		return fmt.Sprintf("%s:%s:sequence", chain, wallet), nil
	case models.OutboundResourceReservationNonce:
		if req.Nonce == nil {
			return "", fmt.Errorf("%w: nonce is required", ErrOutboundTransactionInvalid)
		}
		return fmt.Sprintf("%s:%s:nonce:%d", chain, wallet, *req.Nonce), nil
	case models.OutboundResourceReservationUTXO:
		txID := strings.ToLower(strings.TrimSpace(req.UTXOTxID))
		if txID == "" || req.UTXOVout == nil {
			return "", fmt.Errorf("%w: utxo txid and vout are required", ErrOutboundTransactionInvalid)
		}
		return fmt.Sprintf("%s:utxo:%s:%d", chain, txID, *req.UTXOVout), nil
	default:
		return "", fmt.Errorf("%w: unsupported resource type", ErrOutboundTransactionInvalid)
	}
}

func blockingReservationStatuses(resourceType string) []string {
	switch strings.ToLower(strings.TrimSpace(resourceType)) {
	case models.OutboundResourceReservationSequence:
		return []string{models.OutboundResourceReservationReserved}
	default:
		return []string{models.OutboundResourceReservationReserved, models.OutboundResourceReservationConsumed}
	}
}

func outboundRepoChainIDFromName(name string) (constants.ChainID, bool) {
	normalized := strings.ToLower(strings.TrimSpace(name))
	switch normalized {
	case "binance", "bsc", "bnb":
		normalized = constants.ChainName(constants.Binance)
	case "nile", "tron-nile", "trx-nile", "tron-shasta", "shasta":
		normalized = constants.ChainName(constants.TRONTestnet)
	}
	for _, chainID := range constants.AllChainIDs() {
		if normalized == strings.ToLower(constants.ChainName(chainID)) {
			return chainID, true
		}
	}
	return 0, false
}

func normalizedOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func normalizeOutboundResourceTypes(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func outboundErrorCategory(err error) string {
	if err == nil {
		return ""
	}
	if OutboundTransferFailureBroadcastUncertain(err) {
		return "broadcast_uncertain"
	}
	return "broadcast_failed"
}

func boundedOutboundText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit > 0 && len(value) > limit {
		return value[:limit]
	}
	return value
}
