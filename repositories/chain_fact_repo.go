package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"core/constants"
	"core/models"
	"core/types"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrChainFactInvalid = errors.New("invalid chain fact")

const chainFactIndexedMemoMaxRunes = 180

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
	txHash := strings.ToLower(trimChainFactDBString(ptrString(tx.Hash)))
	logIndex := normalizeChainFactLogIndex(ptrString(tx.LogIndex))
	blockValue := trimChainFactDBString(ptrString(tx.Block))
	blockNumber := int64(0)
	if blockValue != "" {
		parsed, err := strconv.ParseInt(blockValue, 10, 64)
		if err != nil || parsed <= 0 {
			return models.ChainFact{}, invalidChainFact("block/slot height is required")
		}
		blockNumber = parsed
	}
	observedAddress, direction := chainFactObservedAddress(tx)
	observationStatus := chainFactObservationStatus(tx, blockNumber)
	fact := models.ChainFact{
		ID:                uuid.New(),
		EventID:           ChainFactEventID(tx.ChainID, txHash, logIndex),
		ChainID:           tx.ChainID,
		BlockNumber:       blockNumber,
		BlockHash:         trimChainFactDBString(ptrString(tx.BlockHash)),
		TxHash:            txHash,
		LogIndex:          logIndex,
		ObservedAddress:   observedAddress,
		Direction:         direction,
		ObservationStatus: observationStatus,
		Memo:              trimChainFactDBString(ptrString(tx.Memo)),
		MemoNormalized:    normalizePaymentMemo(ptrString(tx.Memo)),
		Token:             trimOptionalString(tx.Token),
		Symbol:            trimChainFactDBString(ptrString(tx.Symbol)),
		Decimals:          tx.Decimals,
		AmountRaw:         trimChainFactDBString(ptrString(tx.Amount)),
		Confirmations:     params.Confirmations,
		Finalized:         strings.EqualFold(trimChainFactDBString(ptrString(tx.Status)), models.TransactionStatusConfirmed),
		SourceEventType:   trimChainFactDBString(params.EventType),
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
	return r.RecordOrUpdate(ctx, fact)
}

func (r *ChainFactRepo) RecordOrUpdate(ctx context.Context, fact *models.ChainFact) (*models.ChainFact, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, gorm.ErrInvalidDB
	}
	prepared, err := prepareChainFact(fact)
	if err != nil {
		return nil, false, err
	}
	*fact = prepared

	var out models.ChainFact
	created := false
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing models.ChainFact
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&existing, "event_id = ?", prepared.EventID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if err := tx.Create(&prepared).Error; err != nil {
				return err
			}
			out = prepared
			created = true
			return nil
		}
		if err != nil {
			return err
		}

		next := existing
		if prepared.Confirmations > next.Confirmations {
			next.Confirmations = prepared.Confirmations
		}
		if next.ConfirmationsRequired == 0 || prepared.ConfirmationsRequired > next.ConfirmationsRequired {
			next.ConfirmationsRequired = prepared.ConfirmationsRequired
		}
		if prepared.Finalized || (next.ConfirmationsRequired > 0 && next.Confirmations >= next.ConfirmationsRequired) {
			next.Finalized = true
		}
		if next.BlockHash == "" && prepared.BlockHash != "" {
			next.BlockHash = prepared.BlockHash
		}
		if prepared.BlockNumber > 0 {
			next.BlockNumber = prepared.BlockNumber
		}
		if prepared.ObservationStatus != "" {
			next.ObservationStatus = prepared.ObservationStatus
		}
		if chainFactCanUpgradeTransferPayload(existing, prepared) {
			next.ObservedAddress = prepared.ObservedAddress
			next.Direction = prepared.Direction
			next.Token = prepared.Token
			next.Symbol = prepared.Symbol
			next.Decimals = prepared.Decimals
			next.AmountRaw = prepared.AmountRaw
			next.SourceEventType = prepared.SourceEventType
			next.RawMetadataJSON = prepared.RawMetadataJSON
		}
		if strings.TrimSpace(prepared.Memo) != "" {
			next.Memo = prepared.Memo
			next.MemoNormalized = prepared.MemoNormalized
		}
		if strings.TrimSpace(next.RawMetadataJSON) == "{}" && strings.TrimSpace(prepared.RawMetadataJSON) != "{}" {
			next.RawMetadataJSON = prepared.RawMetadataJSON
		}
		next.UpdatedAt = time.Now()
		if err := tx.Save(&next).Error; err != nil {
			return err
		}
		out = next
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return &out, created, nil
}

func (r *ChainFactRepo) RecordTransaction(ctx context.Context, eventType string, tx types.TransactionParam) (*models.ChainFact, bool, error) {
	fact, err := BuildChainFactFromTransaction(eventType, tx)
	if err != nil {
		return nil, false, err
	}
	return r.Record(ctx, &fact)
}

func chainFactCanUpgradeTransferPayload(existing, prepared models.ChainFact) bool {
	if !chainFactPositiveRaw(prepared.AmountRaw) {
		return false
	}
	if chainFactPositiveRaw(existing.AmountRaw) {
		return false
	}
	if strings.TrimSpace(prepared.SourceEventType) == "" || strings.TrimSpace(prepared.Symbol) == "" {
		return false
	}
	if strings.TrimSpace(prepared.ObservedAddress) == "" || strings.TrimSpace(prepared.Direction) == "" {
		return false
	}
	return existing.EventID == prepared.EventID &&
		existing.ChainID == prepared.ChainID &&
		strings.EqualFold(strings.TrimSpace(existing.TxHash), strings.TrimSpace(prepared.TxHash)) &&
		normalizeChainFactLogIndex(existing.LogIndex) == normalizeChainFactLogIndex(prepared.LogIndex)
}

func chainFactPositiveRaw(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	amount, ok := new(big.Int).SetString(value, 10)
	return ok && amount.Sign() > 0
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

func (r *ChainFactRepo) MarkIgnored(ctx context.Context, eventID string, reason string) error {
	if r == nil || r.db == nil {
		return gorm.ErrInvalidDB
	}
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return invalidChainFact("event id is required")
	}
	now := time.Now()
	return r.db.WithContext(ctx).
		Model(&models.ChainFact{}).
		Where("event_id = ?", eventID).
		Where("(status IS NULL OR status = '' OR status = ?)", models.ChainFactStatusObserved).
		Updates(map[string]any{
			"status":            models.ChainFactStatusIgnored,
			"correction_reason": boundedCorrectionReason(reason),
			"updated_at":        now,
		}).Error
}

func (r *ChainFactRepo) ListForDepositProcessing(ctx context.Context, limit int) ([]models.ChainFact, error) {
	if r == nil || r.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	return r.listOwnedForDepositProcessing(ctx, limit)
}

func (r *ChainFactRepo) listOwnedForDepositProcessing(ctx context.Context, limit int) ([]models.ChainFact, error) {
	var facts []models.ChainFact
	err := r.db.WithContext(ctx).
		Table("chain_facts").
		Select("chain_facts.*").
		Where("(chain_facts.status IS NULL OR chain_facts.status = '' OR chain_facts.status = ?)", models.ChainFactStatusObserved).
		Where("chain_facts.direction = ?", models.ChainFactDirectionTo).
		Where("TRIM(chain_facts.amount_raw) <> '' AND TRIM(chain_facts.amount_raw) <> '0'").
		Joins(`
			JOIN wallet_address_lookups wal
			  ON wal.chain_id = chain_facts.chain_id
			 AND wal.normalized_address = chain_facts.observed_address
			 AND wal.normalized_address <> ''
		`).
		Joins("LEFT JOIN deposits ON deposits.chain_fact_event_id = chain_facts.event_id").
		Where(`(
			deposits.id IS NULL
			OR (
				deposits.wallet_id IS NOT NULL
				AND deposits.status IN ?
				AND chain_facts.finalized = ?
			)
			OR (
				deposits.wallet_id IS NULL
				AND deposits.status = ?
			)
		)`,
			[]string{models.DepositStatusPending, models.DepositStatusConfirming},
			true,
			models.DepositStatusUnmatched,
		).
		Order("chain_facts.updated_at DESC, chain_facts.created_at DESC").
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
	prepared.EventID = trimChainFactDBString(prepared.EventID)
	prepared.TxHash = strings.ToLower(trimChainFactDBString(prepared.TxHash))
	prepared.LogIndex = normalizeChainFactLogIndex(prepared.LogIndex)
	prepared.BlockHash = trimChainFactDBString(prepared.BlockHash)
	prepared.ObservedAddress = NormalizeWalletLookupAddress(prepared.ChainID, trimChainFactDBString(prepared.ObservedAddress))
	prepared.Direction = trimChainFactDBString(prepared.Direction)
	prepared.ObservationStatus = normalizeChainFactObservationStatus(prepared.ObservationStatus, prepared.BlockNumber)
	prepared.Memo = boundedChainFactIndexedMemo(prepared.Memo)
	prepared.MemoNormalized = boundedChainFactIndexedMemo(prepared.MemoNormalized)
	prepared.Token = trimOptionalString(prepared.Token)
	prepared.Symbol = trimChainFactDBString(prepared.Symbol)
	prepared.AmountRaw = trimChainFactDBString(prepared.AmountRaw)
	prepared.Status = trimChainFactDBString(prepared.Status)
	prepared.CorrectionReason = trimChainFactDBString(prepared.CorrectionReason)
	prepared.SupersededByEventID = trimChainFactDBString(prepared.SupersededByEventID)
	prepared.SourceEventType = trimChainFactDBString(prepared.SourceEventType)
	prepared.RawMetadataJSON = trimChainFactDBString(prepared.RawMetadataJSON)
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
	rawMetadataJSON, err := normalizeChainFactRawMetadataJSON(prepared.RawMetadataJSON)
	if err != nil {
		return models.ChainFact{}, err
	}
	prepared.RawMetadataJSON = rawMetadataJSON
	if prepared.Memo == "" {
		prepared.Memo = chainFactIndexedMetadataMemo(prepared.RawMetadataJSON, "memo", "tag", "payment_id", "paymentId")
	}
	if prepared.Memo == "" {
		prepared.MemoNormalized = ""
	} else if prepared.MemoNormalized == "" {
		prepared.MemoNormalized = normalizePaymentMemo(prepared.Memo)
	} else {
		prepared.MemoNormalized = normalizePaymentMemo(prepared.MemoNormalized)
	}
	prepared.MemoNormalized = boundedChainFactIndexedMemo(prepared.MemoNormalized)
	now := time.Now()
	if prepared.CreatedAt.IsZero() {
		prepared.CreatedAt = now
	}
	prepared.UpdatedAt = now

	if prepared.EventID == "" ||
		!constants.IsSupportedChainID(prepared.ChainID) ||
		prepared.TxHash == "" ||
		prepared.LogIndex == "" ||
		prepared.ObservedAddress == "" ||
		prepared.Symbol == "" ||
		prepared.AmountRaw == "" ||
		prepared.SourceEventType == "" {
		return models.ChainFact{}, invalidChainFact("required field is empty")
	}
	if chainFactRequiresPositiveAmount(prepared.SourceEventType) && !chainFactPositiveRaw(prepared.AmountRaw) {
		return models.ChainFact{}, invalidChainFact("amount_raw must be a positive integer")
	}
	if prepared.BlockNumber <= 0 && prepared.ObservationStatus != models.ChainFactObservationMempool {
		return models.ChainFact{}, invalidChainFact("block/slot height is required")
	}
	if prepared.BlockNumber <= 0 && prepared.Finalized {
		return models.ChainFact{}, invalidChainFact("finalized fact requires block/slot height")
	}
	return prepared, nil
}

func chainFactRequiresPositiveAmount(eventType string) bool {
	eventType = strings.ToLower(strings.TrimSpace(eventType))
	return strings.Contains(eventType, "transfer") || strings.Contains(eventType, "deposit")
}

func chainFactObservedAddress(tx types.TransactionParam) (string, string) {
	if to := trimChainFactDBString(ptrString(tx.To)); to != "" {
		return to, models.ChainFactDirectionTo
	}
	if from := trimChainFactDBString(ptrString(tx.From)); from != "" {
		return from, models.ChainFactDirectionFrom
	}
	return "", models.ChainFactDirectionUnknown
}

func chainFactObservationStatus(tx types.TransactionParam, blockNumber int64) string {
	status := strings.ToLower(trimChainFactDBString(ptrString(tx.Status)))
	if blockNumber <= 0 && (status == "" || status == models.TransactionStatusPending || status == models.TransactionStatusPendingConfirmation) {
		return models.ChainFactObservationMempool
	}
	return models.ChainFactObservationConfirmed
}

func normalizeChainFactObservationStatus(value string, blockNumber int64) string {
	switch strings.ToLower(trimChainFactDBString(value)) {
	case models.ChainFactObservationMempool:
		return models.ChainFactObservationMempool
	case models.ChainFactObservationConfirmed:
		return models.ChainFactObservationConfirmed
	default:
		if blockNumber <= 0 {
			return models.ChainFactObservationMempool
		}
		return models.ChainFactObservationConfirmed
	}
}

func chainFactRawMetadataJSON(tx types.TransactionParam) (string, error) {
	body, err := json.Marshal(map[string]any{
		"from":           trimChainFactDBString(ptrString(tx.From)),
		"from_addresses": tx.FromAddresses,
		"to":             trimChainFactDBString(ptrString(tx.To)),
		"memo":           chainFactDBString(ptrString(tx.Memo)),
		"status":         trimChainFactDBString(ptrString(tx.Status)),
		"gas_used":       trimChainFactDBString(ptrString(tx.GasUsed)),
		"gas_price":      trimChainFactDBString(ptrString(tx.GasPrice)),
		"external_id":    trimChainFactDBString(ptrString(tx.ExternalID)),
		"parent_hash":    trimChainFactDBString(ptrString(tx.ParentHash)),
	})
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func normalizeChainFactRawMetadataJSON(rawJSON string) (string, error) {
	rawJSON = trimChainFactDBString(rawJSON)
	if rawJSON == "" {
		return "{}", nil
	}
	decoder := json.NewDecoder(strings.NewReader(rawJSON))
	decoder.UseNumber()
	var raw map[string]any
	if err := decoder.Decode(&raw); err != nil || raw == nil {
		return "", invalidChainFact("raw metadata must be a JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return "", invalidChainFact("raw metadata must be a JSON object")
	}
	body, err := json.Marshal(sanitizeChainFactRawMetadataValue(raw))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func sanitizeChainFactRawMetadataValue(value any) any {
	switch typed := value.(type) {
	case string:
		return chainFactDBString(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = sanitizeChainFactRawMetadataValue(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			cleanKey := chainFactDBString(key)
			if cleanKey == "" {
				continue
			}
			out[cleanKey] = sanitizeChainFactRawMetadataValue(item)
		}
		return out
	default:
		return value
	}
}

func chainFactIndexedMetadataMemo(rawJSON string, keys ...string) string {
	var raw map[string]any
	if err := json.Unmarshal([]byte(trimChainFactDBString(rawJSON)), &raw); err != nil {
		return ""
	}
	for _, key := range keys {
		if value, ok := raw[key].(string); ok && trimChainFactDBString(value) != "" {
			if bounded := boundedChainFactIndexedMemo(value); bounded != "" {
				return bounded
			}
		}
	}
	return ""
}

func normalizePaymentMemo(value string) string {
	return strings.ToLower(trimChainFactDBString(value))
}

func boundedChainFactIndexedMemo(value string) string {
	value = trimChainFactDBString(value)
	if utf8.RuneCountInString(value) > chainFactIndexedMemoMaxRunes {
		return ""
	}
	return value
}

func normalizeChainFactLogIndex(value string) string {
	value = trimChainFactDBString(value)
	if value == "" {
		return "0"
	}
	return value
}

func trimOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := trimChainFactDBString(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func trimChainFactDBString(value string) string {
	return strings.TrimSpace(chainFactDBString(value))
}

func chainFactDBString(value string) string {
	if value == "" {
		return ""
	}
	value = strings.ToValidUTF8(value, "")
	return strings.ReplaceAll(value, "\x00", "")
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
