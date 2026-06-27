package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"core/constants"
	"core/models"
	"core/types"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrChainFactInvalid = errors.New("invalid chain fact")

type ChainFactRepo struct {
	db *gorm.DB
}

type ChainFactBuildParams struct {
	EventType             string
	Transaction           types.TransactionParam
	Confirmations         uint
	ConfirmationsRequired uint
}

func NewChainFactRepo(db *gorm.DB) *ChainFactRepo {
	return &ChainFactRepo{db: db}
}

func ChainFactEventID(chainID constants.ChainID, txHash, logIndex string) string {
	return fmt.Sprintf("%d:%s:%s", chainID, strings.ToLower(strings.TrimSpace(txHash)), normalizeChainFactLogIndex(logIndex))
}

func BuildChainFactFromTransaction(eventType string, tx types.TransactionParam) (models.ChainFact, error) {
	return BuildChainFact(ChainFactBuildParams{
		EventType:   eventType,
		Transaction: tx,
	})
}

func BuildChainFact(params ChainFactBuildParams) (models.ChainFact, error) {
	tx := params.Transaction
	txHash := strings.ToLower(strings.TrimSpace(ptrString(tx.Hash)))
	logIndex := normalizeChainFactLogIndex(ptrString(tx.LogIndex))
	blockNumber, err := strconv.ParseInt(strings.TrimSpace(ptrString(tx.Block)), 10, 64)
	if err != nil || blockNumber <= 0 {
		return models.ChainFact{}, invalidChainFact("block/slot height is required")
	}
	observedAddress, direction := chainFactObservedAddress(tx)
	fact := models.ChainFact{
		ID:              uuid.New(),
		EventID:         ChainFactEventID(tx.ChainID, txHash, logIndex),
		ChainID:         tx.ChainID,
		BlockNumber:     blockNumber,
		BlockHash:       strings.TrimSpace(ptrString(tx.BlockHash)),
		TxHash:          txHash,
		LogIndex:        logIndex,
		ObservedAddress: observedAddress,
		Direction:       direction,
		Token:           trimOptionalString(tx.Token),
		Symbol:          strings.TrimSpace(ptrString(tx.Symbol)),
		Decimals:        tx.Decimals,
		AmountRaw:       strings.TrimSpace(ptrString(tx.Amount)),
		Confirmations:   params.Confirmations,
		Finalized:       strings.EqualFold(strings.TrimSpace(ptrString(tx.Status)), models.TransactionStatusConfirmed),
		SourceEventType: strings.TrimSpace(params.EventType),
	}
	if params.ConfirmationsRequired > 0 {
		fact.ConfirmationsRequired = params.ConfirmationsRequired
	}
	raw, err := chainFactRawMetadataJSON(tx)
	if err != nil {
		return models.ChainFact{}, err
	}
	fact.RawMetadataJSON = raw
	return prepareChainFact(&fact)
}

func (r *ChainFactRepo) Record(ctx context.Context, fact *models.ChainFact) (*models.ChainFact, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, gorm.ErrInvalidDB
	}
	prepared, err := prepareChainFact(fact)
	if err != nil {
		return nil, false, err
	}
	*fact = prepared

	result := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "event_id"}},
			DoNothing: true,
		}).
		Create(fact)
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected == 1 {
		return fact, true, nil
	}

	var existing models.ChainFact
	if err := r.db.WithContext(ctx).First(&existing, "event_id = ?", fact.EventID).Error; err != nil {
		return nil, false, err
	}
	return &existing, false, nil
}

func (r *ChainFactRepo) RecordTransaction(ctx context.Context, eventType string, tx types.TransactionParam) (*models.ChainFact, bool, error) {
	fact, err := BuildChainFactFromTransaction(eventType, tx)
	if err != nil {
		return nil, false, err
	}
	return r.Record(ctx, &fact)
}

func (r *ChainFactRepo) FindByEventID(ctx context.Context, eventID string) (*models.ChainFact, error) {
	if r == nil || r.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	var fact models.ChainFact
	if err := r.db.WithContext(ctx).First(&fact, "event_id = ?", strings.TrimSpace(eventID)).Error; err != nil {
		return nil, err
	}
	return &fact, nil
}

func (r *ChainFactRepo) ListForDepositProcessing(ctx context.Context, limit int) ([]models.ChainFact, error) {
	if r == nil || r.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var facts []models.ChainFact
	err := r.db.WithContext(ctx).
		Table("chain_facts").
		Select("chain_facts.*").
		Joins("LEFT JOIN deposits ON deposits.chain_fact_event_id = chain_facts.event_id").
		Where("(chain_facts.status IS NULL OR chain_facts.status = '' OR chain_facts.status = ?)", models.ChainFactStatusObserved).
		Where("deposits.id IS NULL OR (deposits.status <> ? AND chain_facts.finalized = ?)", models.DepositStatusFinalized, true).
		Order("chain_facts.created_at ASC").
		Limit(limit).
		Find(&facts).Error
	return facts, err
}

func (r *ChainFactRepo) MarkReorgedByTransactionWithDB(ctx context.Context, tx *gorm.DB, txModel models.Transaction, reason string) error {
	if r == nil || tx == nil || strings.TrimSpace(txModel.Hash) == "" {
		return nil
	}
	logIndex := ""
	if txModel.LogIndex != nil {
		logIndex = *txModel.LogIndex
	}
	eventID := ChainFactEventID(txModel.ChainID, txModel.Hash, logIndex)
	now := time.Now()
	return tx.WithContext(ctx).
		Model(&models.ChainFact{}).
		Where("event_id = ? OR (chain_id = ? AND tx_hash = ? AND log_index = ?)", eventID, txModel.ChainID, strings.ToLower(strings.TrimSpace(txModel.Hash)), normalizeChainFactLogIndex(logIndex)).
		Where("status <> ?", models.ChainFactStatusReorged).
		Updates(map[string]any{
			"status":            models.ChainFactStatusReorged,
			"reorged_at":        &now,
			"correction_reason": boundedCorrectionReason(reason),
			"updated_at":        now,
		}).Error
}

func prepareChainFact(fact *models.ChainFact) (models.ChainFact, error) {
	if fact == nil {
		return models.ChainFact{}, invalidChainFact("record is nil")
	}
	prepared := *fact
	prepared.EventID = strings.TrimSpace(prepared.EventID)
	prepared.TxHash = strings.ToLower(strings.TrimSpace(prepared.TxHash))
	prepared.LogIndex = normalizeChainFactLogIndex(prepared.LogIndex)
	prepared.BlockHash = strings.TrimSpace(prepared.BlockHash)
	prepared.ObservedAddress = strings.TrimSpace(prepared.ObservedAddress)
	prepared.Direction = strings.TrimSpace(prepared.Direction)
	prepared.Symbol = strings.TrimSpace(prepared.Symbol)
	prepared.AmountRaw = strings.TrimSpace(prepared.AmountRaw)
	prepared.Status = strings.TrimSpace(prepared.Status)
	prepared.SourceEventType = strings.TrimSpace(prepared.SourceEventType)
	prepared.RawMetadataJSON = strings.TrimSpace(prepared.RawMetadataJSON)
	if prepared.ID == uuid.Nil {
		prepared.ID = uuid.New()
	}
	if prepared.Direction == "" {
		prepared.Direction = models.ChainFactDirectionUnknown
	}
	if prepared.Status == "" {
		prepared.Status = models.ChainFactStatusObserved
	}
	if prepared.RawMetadataJSON == "" {
		prepared.RawMetadataJSON = "{}"
	}
	now := time.Now()
	if prepared.CreatedAt.IsZero() {
		prepared.CreatedAt = now
	}
	prepared.UpdatedAt = now

	if prepared.EventID == "" ||
		!constants.IsSupportedChainID(prepared.ChainID) ||
		prepared.BlockNumber <= 0 ||
		prepared.TxHash == "" ||
		prepared.LogIndex == "" ||
		prepared.ObservedAddress == "" ||
		prepared.Symbol == "" ||
		prepared.AmountRaw == "" ||
		prepared.SourceEventType == "" {
		return models.ChainFact{}, invalidChainFact("required field is empty")
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(prepared.RawMetadataJSON), &raw); err != nil || raw == nil {
		return models.ChainFact{}, invalidChainFact("raw metadata must be a JSON object")
	}
	return prepared, nil
}

func chainFactObservedAddress(tx types.TransactionParam) (string, string) {
	if to := strings.TrimSpace(ptrString(tx.To)); to != "" {
		return to, models.ChainFactDirectionTo
	}
	if from := strings.TrimSpace(ptrString(tx.From)); from != "" {
		return from, models.ChainFactDirectionFrom
	}
	return "", models.ChainFactDirectionUnknown
}

func chainFactRawMetadataJSON(tx types.TransactionParam) (string, error) {
	body, err := json.Marshal(map[string]any{
		"from":        strings.TrimSpace(ptrString(tx.From)),
		"to":          strings.TrimSpace(ptrString(tx.To)),
		"status":      strings.TrimSpace(ptrString(tx.Status)),
		"gas_used":    strings.TrimSpace(ptrString(tx.GasUsed)),
		"gas_price":   strings.TrimSpace(ptrString(tx.GasPrice)),
		"external_id": strings.TrimSpace(ptrString(tx.ExternalID)),
		"parent_hash": strings.TrimSpace(ptrString(tx.ParentHash)),
	})
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func normalizeChainFactLogIndex(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "0"
	}
	return value
}

func trimOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func ptrString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func invalidChainFact(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrChainFactInvalid, fmt.Sprintf(format, args...))
}
