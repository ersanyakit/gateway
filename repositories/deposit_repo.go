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

var ErrDepositInvalid = errors.New("invalid deposit")

type DepositRepo struct {
	db *gorm.DB
}

func NewDepositRepo(db *gorm.DB) *DepositRepo {
	return &DepositRepo{db: db}
}

func (r *DepositRepo) DB() *gorm.DB {
	return r.db
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
		current, found, err := findDepositByChainFactEventID(ctx, tx, prepared.ChainFactEventID)
		if err != nil {
			return err
		}
		if !found {
			if err := tx.WithContext(ctx).Create(&prepared).Error; err != nil {
				return err
			}
			out = prepared
			created = true
			if out.Status == models.DepositStatusFinalized {
				return recordDepositFinalizedEvent(ctx, tx, out)
			}
			return nil
		}

		next, finalizedNow := mergeDepositFromFact(current, prepared)
		if err := tx.WithContext(ctx).Save(&next).Error; err != nil {
			return err
		}
		out = next
		if finalizedNow {
			return recordDepositFinalizedEvent(ctx, tx, out)
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return &out, created, nil
}

func findDepositByChainFactEventID(ctx context.Context, tx *gorm.DB, eventID string) (models.Deposit, bool, error) {
	var deposit models.Deposit
	err := tx.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&deposit, "chain_fact_event_id = ?", eventID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.Deposit{}, false, nil
	}
	if err != nil {
		return models.Deposit{}, false, err
	}
	return deposit, true, nil
}

func buildDepositFromChainFact(fact models.ChainFact, wallet *models.Wallet) (models.Deposit, error) {
	prepared := models.Deposit{
		ID:                    uuid.New(),
		ChainFactID:           fact.ID,
		ChainFactEventID:      strings.TrimSpace(fact.EventID),
		ChainID:               fact.ChainID,
		BlockNumber:           fact.BlockNumber,
		BlockHash:             strings.TrimSpace(fact.BlockHash),
		TxHash:                strings.ToLower(strings.TrimSpace(fact.TxHash)),
		LogIndex:              strings.TrimSpace(fact.LogIndex),
		ObservedAddress:       strings.TrimSpace(fact.ObservedAddress),
		Direction:             strings.TrimSpace(fact.Direction),
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
	if prepared.ConfirmationsRequired == 0 {
		prepared.ConfirmationsRequired = 1
	}
	if prepared.DetectedAt.IsZero() {
		prepared.DetectedAt = time.Now()
	}
	now := time.Now()
	prepared.CreatedAt = now
	prepared.UpdatedAt = now

	if wallet == nil {
		prepared.Status = models.DepositStatusUnmatched
		prepared.UnmatchedReason = "observed address is not owned by a wallet"
	} else {
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
	}

	if prepared.ChainFactEventID == "" ||
		!constants.IsSupportedChainID(prepared.ChainID) ||
		prepared.BlockNumber <= 0 ||
		prepared.TxHash == "" ||
		prepared.LogIndex == "" ||
		prepared.ObservedAddress == "" ||
		prepared.Symbol == "" ||
		prepared.AmountRaw == "" ||
		prepared.SourceEventType == "" ||
		prepared.Status == "" {
		return models.Deposit{}, invalidDeposit("required field is empty")
	}
	return prepared, nil
}

func mergeDepositFromFact(current, incoming models.Deposit) (models.Deposit, bool) {
	next := current
	next.ChainFactID = incoming.ChainFactID
	next.BlockNumber = incoming.BlockNumber
	next.BlockHash = incoming.BlockHash
	next.TxHash = incoming.TxHash
	next.LogIndex = incoming.LogIndex
	next.ObservedAddress = incoming.ObservedAddress
	next.Direction = incoming.Direction
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

func depositTransactionUniqueHash(fact models.ChainFact) string {
	return fmt.Sprintf("%d-%s-%s", fact.ChainID, strings.ToLower(strings.TrimSpace(fact.TxHash)), strings.TrimSpace(fact.LogIndex))
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

func invalidDeposit(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrDepositInvalid, fmt.Sprintf(format, args...))
}
