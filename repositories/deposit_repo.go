package repositories

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrDepositInvalid             = errors.New("invalid deposit")
	ErrDepositRevivalNotCanonical = errors.New("reorged deposit revival is not canonical")
)

type DepositRepo struct {
	db *gorm.DB
}

func NewDepositRepo(db *gorm.DB) *DepositRepo {
	return &DepositRepo{db: db}
}

func (r *DepositRepo) DB() *gorm.DB {
	return r.db
}

func (r *DepositRepo) FindByIDForUpdate(ctx context.Context, id uuid.UUID) (*models.Deposit, error) {
	if r == nil || r.db == nil || id == uuid.Nil {
		return nil, gorm.ErrInvalidDB
	}
	var deposit models.Deposit
	if err := r.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).First(&deposit, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &deposit, nil
}

func (r *DepositRepo) ListPendingFinality(ctx context.Context, limit int) ([]models.Deposit, error) {
	if r == nil || r.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var deposits []models.Deposit
	err := r.db.WithContext(ctx).
		Where("wallet_id IS NOT NULL").
		Where("status IN ?", []string{models.DepositStatusPending, models.DepositStatusConfirming}).
		Order("created_at ASC").
		Limit(limit).
		Find(&deposits).Error
	return deposits, err
}

func (r *DepositRepo) MarkReorgedByTransactionWithDB(ctx context.Context, tx *gorm.DB, txModel models.Transaction, reason string) error {
	if r == nil || tx == nil || strings.TrimSpace(txModel.UniqueHash) == "" {
		return nil
	}
	now := time.Now()
	return tx.WithContext(ctx).
		Model(&models.Deposit{}).
		Where("transaction_unique_hash = ?", txModel.UniqueHash).
		Where("status NOT IN ?", []string{models.DepositStatusReorged, models.DepositStatusSuperseded}).
		Updates(map[string]any{
			"status":            models.DepositStatusReorged,
			"reorged_at":        &now,
			"correction_reason": boundedCorrectionReason(reason),
			"updated_at":        now,
		}).Error
}

func (r *DepositRepo) ConsumeChainFact(ctx context.Context, fact models.ChainFact, wallet *models.Wallet) (*models.Deposit, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, gorm.ErrInvalidDB
	}
	prepared, err := buildDepositFromChainFact(fact, wallet)
	if err != nil {
		return nil, false, err
	}

	var out models.Deposit
	created := false
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "chain_fact_event_id"}},
			DoNothing: true,
		}).Create(&prepared)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 1 {
			out = prepared
			created = true
			if out.Status == models.DepositStatusFinalized {
				return recordDepositFinalizedEvent(ctx, tx, out)
			}
			return nil
		}

		var current models.Deposit
		if err := tx.WithContext(ctx).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&current, "chain_fact_event_id = ?", prepared.ChainFactEventID).Error; err != nil {
			return err
		}
		reviving := current.Status == models.DepositStatusReorged
		if reviving {
			canonical, err := canonicalBlockMatchesWithDB(ctx, tx, prepared.ChainID, prepared.BlockNumber, prepared.BlockHash)
			if err != nil {
				return err
			}
			if !canonical || fact.Status != models.ChainFactStatusObserved || !depositEconomicIdentityEqual(current, prepared) {
				return ErrDepositRevivalNotCanonical
			}
		}
		reorgedAt := current.ReorgedAt
		next, finalizedNow := mergeDepositFromFact(current, prepared)
		if err := tx.WithContext(ctx).Save(&next).Error; err != nil {
			return err
		}
		out = next
		if reviving {
			return recordDepositReappearanceEvent(ctx, tx, out, current, reorgedAt)
		}
		if finalizedNow {
			if depositReappearanceEpoch(out.CorrectionReason) != "" {
				return recordDepositReappearanceEvent(ctx, tx, out, current, nil)
			}
			return recordDepositFinalizedEvent(ctx, tx, out)
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return &out, created, nil
}

func buildDepositFromChainFact(fact models.ChainFact, wallet *models.Wallet) (models.Deposit, error) {
	if wallet == nil {
		return models.Deposit{}, invalidDeposit("wallet is required")
	}

	prepared := models.Deposit{
		ID:                    uuid.New(),
		ChainFactID:           fact.ID,
		ChainFactEventID:      strings.TrimSpace(fact.EventID),
		ChainID:               fact.ChainID,
		BlockNumber:           fact.BlockNumber,
		BlockHash:             strings.TrimSpace(fact.BlockHash),
		TxHash:                normalizeChainFactTransactionHash(fact.ChainID, fact.TxHash),
		LogIndex:              strings.TrimSpace(fact.LogIndex),
		ObservedAddress:       strings.TrimSpace(fact.ObservedAddress),
		Direction:             strings.TrimSpace(fact.Direction),
		ObservationStatus:     depositObservationStatusFromFact(fact),
		Memo:                  strings.TrimSpace(fact.Memo),
		MemoNormalized:        normalizePaymentMemo(firstNonEmptyString(fact.MemoNormalized, fact.Memo)),
		MemoStatus:            depositMemoStatusFromFact(fact),
		Token:                 trimOptionalString(fact.Token),
		Symbol:                strings.TrimSpace(fact.Symbol),
		Decimals:              fact.Decimals,
		AmountRaw:             strings.TrimSpace(fact.AmountRaw),
		Confirmations:         fact.Confirmations,
		ConfirmationsRequired: fact.ConfirmationsRequired,
		TransactionUniqueHash: depositTransactionUniqueHash(fact),
		SourceEventType:       strings.TrimSpace(fact.SourceEventType),
		DetectedAt:            fact.CreatedAt,
	}
	if prepared.Direction == "" {
		prepared.Direction = models.ChainFactDirectionUnknown
	}
	if prepared.ObservationStatus == "" {
		prepared.ObservationStatus = models.DepositObservationConfirmed
	}
	if prepared.MemoStatus == "" {
		prepared.MemoStatus = models.DepositMemoStatusNotRequired
	}
	if prepared.ConfirmationsRequired == 0 {
		prepared.ConfirmationsRequired = 1
	}
	if prepared.DetectedAt.IsZero() {
		prepared.DetectedAt = time.Now()
	}
	now := time.Now()
	prepared.CreatedAt = now
	prepared.UpdatedAt = now

	walletID := wallet.ID
	merchantID := wallet.MerchantID
	domainID := wallet.DomainID
	prepared.WalletID = &walletID
	prepared.MerchantID = &merchantID
	prepared.DomainID = &domainID
	prepared.ProductID = strings.TrimSpace(wallet.ProductID)
	prepared.UserID = strings.TrimSpace(wallet.UserID)
	prepared.Status = depositStatusForFinality(prepared.Confirmations, prepared.ConfirmationsRequired, fact.Finalized)
	if prepared.Status == models.DepositStatusFinalized {
		prepared.FinalizedAt = &now
	}

	if prepared.ChainFactEventID == "" ||
		!constants.IsSupportedChainID(prepared.ChainID) ||
		prepared.TxHash == "" ||
		prepared.LogIndex == "" ||
		prepared.ObservedAddress == "" ||
		prepared.Symbol == "" ||
		prepared.AmountRaw == "" ||
		prepared.SourceEventType == "" ||
		prepared.Status == "" {
		return models.Deposit{}, invalidDeposit("required field is empty")
	}
	if prepared.BlockNumber <= 0 && prepared.ObservationStatus != models.DepositObservationMempool {
		return models.Deposit{}, invalidDeposit("block/slot height is required")
	}
	if prepared.BlockNumber <= 0 && prepared.Status == models.DepositStatusFinalized {
		return models.Deposit{}, invalidDeposit("finalized deposit requires block/slot height")
	}
	return prepared, nil
}

func mergeDepositFromFact(current, incoming models.Deposit) (models.Deposit, bool) {
	next := current
	if current.Status == models.DepositStatusReorged {
		next.ChainFactID = incoming.ChainFactID
		next.BlockNumber = incoming.BlockNumber
		next.BlockHash = incoming.BlockHash
		next.TxHash = incoming.TxHash
		next.LogIndex = incoming.LogIndex
		next.ObservedAddress = incoming.ObservedAddress
		next.Direction = incoming.Direction
		next.ObservationStatus = incoming.ObservationStatus
		next.Memo = incoming.Memo
		next.MemoNormalized = incoming.MemoNormalized
		next.MemoStatus = incoming.MemoStatus
		next.Token = incoming.Token
		next.Symbol = incoming.Symbol
		next.Decimals = incoming.Decimals
		next.AmountRaw = incoming.AmountRaw
		next.Confirmations = incoming.Confirmations
		next.ConfirmationsRequired = incoming.ConfirmationsRequired
		next.TransactionUniqueHash = incoming.TransactionUniqueHash
		next.SourceEventType = incoming.SourceEventType
		next.Status = incoming.Status
		next.FinalizedAt = incoming.FinalizedAt
		next.ReorgedAt = nil
		next.SupersededByEventID = ""
		next.CorrectionReason = depositReappearanceMarker(current.ReorgedAt)
		if incoming.WalletID != nil {
			next.WalletID = incoming.WalletID
			next.MerchantID = incoming.MerchantID
			next.DomainID = incoming.DomainID
			next.ProductID = incoming.ProductID
			next.UserID = incoming.UserID
			next.UnmatchedReason = ""
		}
		next.UpdatedAt = time.Now()
		return next, next.Status == models.DepositStatusFinalized
	}
	next.ChainFactID = incoming.ChainFactID
	next.BlockNumber = incoming.BlockNumber
	next.BlockHash = incoming.BlockHash
	next.TxHash = incoming.TxHash
	next.LogIndex = incoming.LogIndex
	next.ObservedAddress = incoming.ObservedAddress
	next.Direction = incoming.Direction
	next.ObservationStatus = incoming.ObservationStatus
	next.Memo = incoming.Memo
	next.MemoNormalized = incoming.MemoNormalized
	next.MemoStatus = incoming.MemoStatus
	next.Token = incoming.Token
	next.Symbol = incoming.Symbol
	next.Decimals = incoming.Decimals
	next.AmountRaw = incoming.AmountRaw
	if incoming.Confirmations > next.Confirmations {
		next.Confirmations = incoming.Confirmations
	}
	if incoming.ConfirmationsRequired > next.ConfirmationsRequired {
		next.ConfirmationsRequired = incoming.ConfirmationsRequired
	}
	next.TransactionUniqueHash = incoming.TransactionUniqueHash
	next.SourceEventType = incoming.SourceEventType
	if incoming.WalletID != nil {
		next.WalletID = incoming.WalletID
		next.MerchantID = incoming.MerchantID
		next.DomainID = incoming.DomainID
		next.ProductID = incoming.ProductID
		next.UserID = incoming.UserID
		next.UnmatchedReason = ""
	}
	if next.DetectedAt.IsZero() {
		next.DetectedAt = incoming.DetectedAt
	}

	finalizedBefore := next.FinalizedAt != nil || next.Status == models.DepositStatusFinalized
	if !finalizedBefore && incoming.Status == models.DepositStatusFinalized {
		next.Status = models.DepositStatusFinalized
		next.FinalizedAt = incoming.FinalizedAt
		if next.FinalizedAt == nil {
			now := time.Now()
			next.FinalizedAt = &now
		}
	} else if !finalizedBefore && incoming.WalletID != nil {
		next.Status = depositStatusForFinality(next.Confirmations, next.ConfirmationsRequired, false)
	} else if next.WalletID == nil {
		next.Status = models.DepositStatusUnmatched
	}
	next.UpdatedAt = time.Now()
	return next, !finalizedBefore && next.Status == models.DepositStatusFinalized
}

func depositEconomicIdentityEqual(existing, incoming models.Deposit) bool {
	return existing.ChainFactEventID == incoming.ChainFactEventID &&
		existing.ChainID == incoming.ChainID &&
		chainFactTransactionHashesEqual(existing.ChainID, existing.TxHash, incoming.TxHash) &&
		normalizeChainFactLogIndex(existing.LogIndex) == normalizeChainFactLogIndex(incoming.LogIndex) &&
		NormalizeWalletLookupAddress(existing.ChainID, existing.ObservedAddress) == NormalizeWalletLookupAddress(incoming.ChainID, incoming.ObservedAddress) &&
		strings.TrimSpace(existing.Direction) == strings.TrimSpace(incoming.Direction) &&
		chainAssetIdentityEqual(existing.ChainID, existing.Token, incoming.Token) &&
		strings.EqualFold(strings.TrimSpace(existing.Symbol), strings.TrimSpace(incoming.Symbol)) &&
		existing.Decimals == incoming.Decimals &&
		strings.TrimSpace(existing.AmountRaw) == strings.TrimSpace(incoming.AmountRaw) &&
		normalizePaymentMemo(firstNonEmptyString(existing.MemoNormalized, existing.Memo)) == normalizePaymentMemo(firstNonEmptyString(incoming.MemoNormalized, incoming.Memo))
}

func depositStatusForFinality(confirmations, required uint, finalized bool) string {
	if required == 0 {
		required = 1
	}
	if finalized || confirmations >= required {
		return models.DepositStatusFinalized
	}
	if confirmations > 0 {
		return models.DepositStatusConfirming
	}
	return models.DepositStatusPending
}

func depositObservationStatusFromFact(fact models.ChainFact) string {
	switch strings.ToLower(strings.TrimSpace(fact.ObservationStatus)) {
	case models.ChainFactObservationMempool:
		return models.DepositObservationMempool
	default:
		return models.DepositObservationConfirmed
	}
}

func depositMemoStatusFromFact(fact models.ChainFact) string {
	if normalizePaymentMemo(firstNonEmptyString(fact.MemoNormalized, fact.Memo)) == "" {
		return models.DepositMemoStatusNotRequired
	}
	return models.DepositMemoStatusPresent
}

func depositTransactionUniqueHash(fact models.ChainFact) string {
	return fmt.Sprintf("%d-%s-%s", fact.ChainID, normalizeChainFactTransactionHash(fact.ChainID, fact.TxHash), strings.TrimSpace(fact.LogIndex))
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func recordDepositFinalizedEvent(ctx context.Context, tx *gorm.DB, deposit models.Deposit) error {
	if deposit.MerchantID == nil || deposit.DomainID == nil || deposit.WalletID == nil {
		return nil
	}
	eventType := "deposit.finalized.v1"
	eventID := deposit.ID.String() + ":" + eventType
	payload := map[string]any{
		"event_id":               eventID,
		"event_type":             eventType,
		"event_version":          constants.WebhookEventVersionV1,
		"occurred_at":            time.Now().UTC().Format(time.RFC3339Nano),
		"merchant_id":            deposit.MerchantID.String(),
		"domain_id":              deposit.DomainID.String(),
		"resource_type":          "deposit",
		"resource_id":            deposit.ID.String(),
		"resource_status":        "finalized",
		"idempotency_key":        eventID,
		"correlation_id":         "chain_fact:" + deposit.ChainFactEventID,
		"chain_id":               int64(deposit.ChainID),
		"tx_hash":                deposit.TxHash,
		"tx_unique_hash":         deposit.TransactionUniqueHash,
		"log_index":              deposit.LogIndex,
		"amount_raw":             deposit.AmountRaw,
		"symbol":                 deposit.Symbol,
		"token":                  deposit.Token,
		"wallet_id":              deposit.WalletID.String(),
		"confirmations":          deposit.Confirmations,
		"confirmations_required": deposit.ConfirmationsRequired,
		"source_event_type":      deposit.SourceEventType,
	}
	event, err := BuildMoneyEventOutbox(MoneyEventOutboxBuildParams{
		EventName:      eventType,
		EventID:        eventID,
		AggregateType:  "deposit",
		AggregateID:    deposit.ID.String(),
		MerchantID:     *deposit.MerchantID,
		DomainID:       *deposit.DomainID,
		IdempotencyKey: eventID,
		Payload:        payload,
	})
	if err != nil {
		return err
	}
	_, _, err = NewMoneyEventOutboxRepo(tx).RecordWithDB(ctx, tx, &event)
	return err
}

const depositReappearanceMarkerPrefix = "canonical_reappearance:"

func depositReappearanceMarker(reorgedAt *time.Time) string {
	epoch := moneyEventGenerationUnixNano(time.Now())
	if reorgedAt != nil && !reorgedAt.IsZero() {
		epoch = moneyEventGenerationUnixNano(*reorgedAt)
	}
	return depositReappearanceMarkerPrefix + strconv.FormatInt(epoch, 10)
}

func depositReappearanceEpoch(reason string) string {
	reason = strings.TrimSpace(reason)
	if !strings.HasPrefix(reason, depositReappearanceMarkerPrefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(reason, depositReappearanceMarkerPrefix))
}

func recordDepositReappearanceEvent(ctx context.Context, tx *gorm.DB, deposit, previous models.Deposit, reorgedAt *time.Time) error {
	if deposit.MerchantID == nil || deposit.DomainID == nil || deposit.WalletID == nil {
		return nil
	}
	epoch := depositReappearanceEpoch(deposit.CorrectionReason)
	if epoch == "" {
		epoch = strings.TrimPrefix(depositReappearanceMarker(reorgedAt), depositReappearanceMarkerPrefix)
	}
	eventType := "deposit.detected.v1"
	if deposit.Status == models.DepositStatusFinalized {
		eventType = "deposit.finalized.v1"
	}
	eventID := deposit.ID.String() + ":" + eventType + ":restored:" + epoch
	payload := map[string]any{
		"event_id":               eventID,
		"event_type":             eventType,
		"event_version":          constants.WebhookEventVersionV1,
		"occurred_at":            time.Now().UTC().Format(time.RFC3339Nano),
		"merchant_id":            deposit.MerchantID.String(),
		"domain_id":              deposit.DomainID.String(),
		"resource_type":          "deposit",
		"resource_id":            deposit.ID.String(),
		"resource_status":        deposit.Status,
		"idempotency_key":        eventID,
		"correlation_id":         "chain_fact:" + deposit.ChainFactEventID,
		"restoration":            true,
		"restoration_reason":     "transaction reappeared in an exact canonical block",
		"chain_id":               int64(deposit.ChainID),
		"tx_hash":                deposit.TxHash,
		"tx_unique_hash":         deposit.TransactionUniqueHash,
		"log_index":              deposit.LogIndex,
		"amount_raw":             deposit.AmountRaw,
		"symbol":                 deposit.Symbol,
		"token":                  deposit.Token,
		"wallet_id":              deposit.WalletID.String(),
		"confirmations":          deposit.Confirmations,
		"confirmations_required": deposit.ConfirmationsRequired,
		"source_event_type":      deposit.SourceEventType,
		"previous_block_number":  previous.BlockNumber,
		"previous_block_hash":    previous.BlockHash,
		"canonical_block_number": deposit.BlockNumber,
		"canonical_block_hash":   deposit.BlockHash,
	}
	event, err := BuildMoneyEventOutbox(MoneyEventOutboxBuildParams{
		EventName:      eventType,
		EventID:        eventID,
		AggregateType:  "deposit",
		AggregateID:    deposit.ID.String(),
		MerchantID:     *deposit.MerchantID,
		DomainID:       *deposit.DomainID,
		IdempotencyKey: eventID,
		Payload:        payload,
	})
	if err != nil {
		return err
	}
	_, _, err = NewMoneyEventOutboxRepo(tx).RecordWithDB(ctx, tx, &event)
	return err
}

func invalidDeposit(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrDepositInvalid, fmt.Sprintf(format, args...))
}
