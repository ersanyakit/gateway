package repositories

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"core/models"
	"core/types"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrChainFactInvalid  = errors.New("invalid chain fact")
	ErrChainFactConflict = errors.New("chain fact conflicts with existing event id")
)

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

func (r *ChainFactRepo) DB() *gorm.DB { return r.db }

func (r *ChainFactRepo) Record(ctx context.Context, fact *models.ChainFact) (*models.ChainFact, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, gorm.ErrInvalidDB
	}
	return r.RecordWithDB(ctx, r.db, fact)
}

func (r *ChainFactRepo) RecordWithDB(ctx context.Context, tx *gorm.DB, fact *models.ChainFact) (*models.ChainFact, bool, error) {
	if tx == nil {
		return nil, false, gorm.ErrInvalidDB
	}
	prepared, err := prepareChainFact(fact)
	if err != nil {
		return nil, false, err
	}
	*fact = prepared

	result := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(fact)
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected == 1 {
		return fact, true, nil
	}

	var existing models.ChainFact
	if err := tx.WithContext(ctx).First(&existing, "event_id = ?", fact.EventID).Error; err != nil {
		return nil, false, err
	}
	if !chainFactCompatible(existing, fact) {
		return &existing, false, ErrChainFactConflict
	}
	return &existing, false, nil
}

func BuildChainFact(params ChainFactBuildParams) (models.ChainFact, error) {
	tx := params.Transaction
	hash := normalizeChainFactHash(tx.Hash)
	logIndex := normalizeChainFactLogIndex(tx.LogIndex)
	blockNumber, err := parseChainFactBlock(tx.Block)
	if err != nil {
		return models.ChainFact{}, err
	}
	eventID, err := ChainFactEventID(tx)
	if err != nil {
		return models.ChainFact{}, err
	}

	token := ""
	if tx.Token != nil {
		token = strings.TrimSpace(*tx.Token)
	}
	symbol := ""
	if tx.Symbol != nil {
		symbol = strings.TrimSpace(*tx.Symbol)
	}
	amount := ""
	if tx.Amount != nil {
		amount = strings.TrimSpace(*tx.Amount)
	}
	blockHash := ""
	if tx.BlockHash != nil {
		blockHash = strings.TrimSpace(*tx.BlockHash)
	}
	status := ""
	if tx.Status != nil {
		status = strings.TrimSpace(*tx.Status)
	}
	observedAddress, direction := chainFactObservedAddress(tx)

	fact := models.ChainFact{
		EventID:               eventID,
		ChainID:               tx.ChainID,
		EventType:             strings.TrimSpace(params.EventType),
		BlockNumber:           blockNumber,
		BlockHash:             blockHash,
		TxHash:                hash,
		LogIndex:              logIndex,
		ObservedAddress:       observedAddress,
		Direction:             direction,
		Token:                 token,
		Symbol:                symbol,
		Decimals:              tx.Decimals,
		Amount:                amount,
		FinalityStatus:        status,
		Confirmations:         params.Confirmations,
		ConfirmationsRequired: params.ConfirmationsRequired,
	}
	return prepareChainFact(&fact)
}

func ChainFactEventID(tx types.TransactionParam) (string, error) {
	hash := normalizeChainFactHash(tx.Hash)
	if hash == "" {
		return "", errors.Join(ErrChainFactInvalid, gorm.ErrInvalidData, errors.New("hash is required"))
	}
	return fmt.Sprintf("%d:%s:%s", tx.ChainID, hash, normalizeChainFactLogIndex(tx.LogIndex)), nil
}

func prepareChainFact(fact *models.ChainFact) (models.ChainFact, error) {
	if fact == nil {
		return models.ChainFact{}, errors.Join(ErrChainFactInvalid, gorm.ErrInvalidData, errors.New("record is nil"))
	}
	prepared := *fact
	prepared.EventID = strings.TrimSpace(prepared.EventID)
	prepared.EventType = strings.TrimSpace(prepared.EventType)
	prepared.TxHash = normalizeChainFactHash(&prepared.TxHash)
	prepared.LogIndex = strings.TrimSpace(prepared.LogIndex)
	if prepared.LogIndex == "" {
		prepared.LogIndex = "tx"
	}
	prepared.BlockHash = strings.TrimSpace(prepared.BlockHash)
	prepared.ObservedAddress = strings.TrimSpace(prepared.ObservedAddress)
	prepared.Direction = normalizeChainFactDirection(prepared.Direction)
	prepared.Token = strings.TrimSpace(prepared.Token)
	prepared.Symbol = strings.TrimSpace(prepared.Symbol)
	prepared.Amount = strings.TrimSpace(prepared.Amount)
	prepared.FinalityStatus = strings.TrimSpace(prepared.FinalityStatus)
	if prepared.ID == uuid.Nil {
		prepared.ID = uuid.New()
	}
	if prepared.ConfirmationsRequired == 0 {
		prepared.ConfirmationsRequired = 1
	}
	now := time.Now()
	if prepared.CreatedAt.IsZero() {
		prepared.CreatedAt = now
	}
	prepared.UpdatedAt = now

	if prepared.EventID == "" ||
		prepared.ChainID == 0 ||
		prepared.EventType == "" ||
		prepared.BlockNumber <= 0 ||
		prepared.TxHash == "" ||
		prepared.LogIndex == "" ||
		prepared.Symbol == "" ||
		prepared.Amount == "" {
		return models.ChainFact{}, errors.Join(ErrChainFactInvalid, gorm.ErrInvalidData, errors.New("required field is empty"))
	}
	return prepared, nil
}

func chainFactCompatible(existing models.ChainFact, incoming *models.ChainFact) bool {
	if incoming == nil {
		return false
	}
	return existing.EventID == incoming.EventID &&
		existing.ChainID == incoming.ChainID &&
		existing.EventType == incoming.EventType &&
		existing.BlockNumber == incoming.BlockNumber &&
		existing.BlockHash == incoming.BlockHash &&
		existing.TxHash == incoming.TxHash &&
		existing.LogIndex == incoming.LogIndex &&
		existing.ObservedAddress == incoming.ObservedAddress &&
		existing.Direction == incoming.Direction &&
		existing.Token == incoming.Token &&
		existing.Symbol == incoming.Symbol &&
		existing.Decimals == incoming.Decimals &&
		existing.Amount == incoming.Amount &&
		existing.FinalityStatus == incoming.FinalityStatus &&
		existing.Confirmations == incoming.Confirmations &&
		existing.ConfirmationsRequired == incoming.ConfirmationsRequired
}

func chainFactObservedAddress(tx types.TransactionParam) (string, string) {
	if tx.To != nil && strings.TrimSpace(*tx.To) != "" {
		return strings.TrimSpace(*tx.To), models.ChainFactDirectionInbound
	}
	if tx.From != nil && strings.TrimSpace(*tx.From) != "" {
		return strings.TrimSpace(*tx.From), models.ChainFactDirectionOutbound
	}
	return "", models.ChainFactDirectionUnknown
}

func normalizeChainFactDirection(direction string) string {
	switch strings.ToLower(strings.TrimSpace(direction)) {
	case models.ChainFactDirectionInbound:
		return models.ChainFactDirectionInbound
	case models.ChainFactDirectionOutbound:
		return models.ChainFactDirectionOutbound
	default:
		return models.ChainFactDirectionUnknown
	}
}

func normalizeChainFactHash(hash *string) string {
	if hash == nil {
		return ""
	}
	value := strings.TrimSpace(*hash)
	if strings.HasPrefix(strings.ToLower(value), "0x") {
		return strings.ToLower(value)
	}
	return value
}

func normalizeChainFactLogIndex(logIndex *string) string {
	if logIndex == nil {
		return "tx"
	}
	value := strings.TrimSpace(*logIndex)
	if value == "" {
		return "tx"
	}
	return value
}

func parseChainFactBlock(block *string) (int64, error) {
	if block == nil || strings.TrimSpace(*block) == "" {
		return 0, errors.Join(ErrChainFactInvalid, gorm.ErrInvalidData, errors.New("block number is required"))
	}
	value, err := strconv.ParseInt(strings.TrimSpace(*block), 10, 64)
	if err != nil || value <= 0 {
		return 0, errors.Join(ErrChainFactInvalid, gorm.ErrInvalidData, fmt.Errorf("invalid block number %q", strings.TrimSpace(*block)))
	}
	return value, nil
}
