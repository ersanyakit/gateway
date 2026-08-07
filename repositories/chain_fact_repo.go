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

func (r *ChainFactRepo) DB() *gorm.DB {
	if r == nil {
		return nil
	}
	return r.db
}

func ChainFactEventID(chainID constants.ChainID, txHash, logIndex string) string {
	return fmt.Sprintf("%d:%s:%s", chainID, normalizeChainFactTransactionHash(chainID, txHash), normalizeChainFactLogIndex(logIndex))
}

func normalizeChainFactTransactionHash(chainID constants.ChainID, txHash string) string {
	txHash = strings.TrimSpace(txHash)
	// Solana signatures are base58 identifiers and therefore case-sensitive.
	// Bitcoin/TRON and EVM transaction hashes are hexadecimal identifiers.
	if chainID == constants.Solana {
		return txHash
	}
	return strings.ToLower(txHash)
}

func chainFactTransactionHashesEqual(chainID constants.ChainID, left, right string) bool {
	return normalizeChainFactTransactionHash(chainID, left) == normalizeChainFactTransactionHash(chainID, right)
}

func BuildChainFactFromTransaction(eventType string, tx types.TransactionParam) (models.ChainFact, error) {
	return BuildChainFact(ChainFactBuildParams{
		EventType:   eventType,
		Transaction: tx,
	})
}

func BuildChainFact(params ChainFactBuildParams) (models.ChainFact, error) {
	tx := params.Transaction
	txHash := normalizeChainFactTransactionHash(tx.ChainID, trimChainFactDBString(ptrString(tx.Hash)))
	logIndex := normalizeChainFactLogIndex(ptrString(tx.LogIndex))
	blockValue := trimChainFactDBString(ptrString(tx.Block))
	blockHash := trimChainFactDBString(ptrString(tx.BlockHash))
	blockNumber := int64(0)
	if blockValue != "" {
		parsed, err := strconv.ParseInt(blockValue, 10, 64)
		if err != nil || parsed <= 0 {
			return models.ChainFact{}, invalidChainFact("block/slot height is required")
		}
		blockNumber = parsed
	}
	if blockNumber > 0 && blockHash == "" {
		return models.ChainFact{}, invalidChainFact("block hash is required for a confirmed observation")
	}
	observedAddress, direction := chainFactObservedAddress(tx)
	observationStatus := chainFactObservationStatus(tx, blockNumber)
	fact := models.ChainFact{
		ID:                uuid.New(),
		EventID:           ChainFactEventID(tx.ChainID, txHash, logIndex),
		ChainID:           tx.ChainID,
		BlockNumber:       blockNumber,
		BlockHash:         blockHash,
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
		if err := AcquireCanonicalBlockLockWithDB(ctx, tx, prepared.ChainID, prepared.BlockNumber); err != nil {
			return err
		}
		incomingCanonical := true
		// Isolated repository tests can intentionally migrate only chain_facts;
		// the production schema always has blocks and is guarded by the startup
		// migration contract. Whenever canonical authority is available, an exact
		// tuple match is mandatory before a fact may be observable.
		hasCanonicalIdentity := prepared.BlockNumber > 0 && strings.TrimSpace(prepared.BlockHash) != ""
		if hasCanonicalIdentity && tx.Migrator().HasTable(&models.Block{}) {
			incomingCanonical, err = canonicalBlockMatchesWithDB(ctx, tx, prepared.ChainID, prepared.BlockNumber, prepared.BlockHash)
			if err != nil {
				return err
			}
		}
		if !incomingCanonical {
			now := time.Now()
			prepared.Status = models.ChainFactStatusReorged
			prepared.Finalized = false
			prepared.ReorgedAt = &now
			prepared.CorrectionReason = "chain_fact_not_canonical_at_observation"
		}
		// SELECT ... FOR UPDATE cannot lock a row that does not exist. Two live
		// scanners (or a scanner and a rescan) could therefore both observe a
		// miss and race on Create, making the loser return a unique-constraint
		// error for an otherwise idempotent duplicate. Let the database arbitrate
		// the first insert, then lock and merge the winning row.
		result := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "event_id"}},
			DoNothing: true,
		}).Create(&prepared)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 1 {
			out = prepared
			created = true
			return nil
		}

		var existing models.ChainFact
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&existing, "event_id = ?", prepared.EventID).Error; err != nil {
			return err
		}
		if !incomingCanonical {
			// Do not let an orphan replay revive or merge into an existing row. If
			// this is the same stale tuple, quarantine legacy/racing observed state
			// so only a later exact canonical reappearance can revive it.
			if existing.BlockNumber == prepared.BlockNumber && historicalBlockHashesEqual(existing.ChainID, existing.BlockHash, prepared.BlockHash) && existing.Status != models.ChainFactStatusReorged {
				now := time.Now()
				existing.Status = models.ChainFactStatusReorged
				existing.Finalized = false
				existing.ReorgedAt = &now
				existing.CorrectionReason = "chain_fact_not_canonical_at_observation"
				existing.UpdatedAt = now
				if err := tx.Save(&existing).Error; err != nil {
					return err
				}
			}
			out = existing
			return nil
		}
		if existing.Status == models.ChainFactStatusReorged {
			// A replay from the orphaned branch must never be able to revive money
			// state by itself. Block observers persist canonical identity before
			// dispatching facts, so the exact (chain, height, hash) tuple is the
			// authority here rather than the provider payload being merged.
			if !chainFactEconomicIdentityEqual(existing, prepared) {
				if err := recordChainFactReappearanceMismatch(ctx, tx, existing, prepared); err != nil {
					return err
				}
				out = existing
				return nil
			}

			next := reviveChainFact(existing, prepared)
			if err := tx.Save(&next).Error; err != nil {
				return err
			}
			if err := resetDepositFactInboxForReappearance(ctx, tx, next); err != nil {
				return err
			}
			out = next
			return nil
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
		if prepared.BlockNumber > 0 && (next.BlockNumber != prepared.BlockNumber || !historicalBlockHashesEqual(next.ChainID, next.BlockHash, prepared.BlockHash)) {
			// A canonical observation of the same economic event in a new block is
			// a lifecycle reappearance, never a partial BlockNumber-only merge.
			if !chainFactEconomicIdentityEqual(existing, prepared) {
				if err := recordChainFactReappearanceMismatch(ctx, tx, existing, prepared); err != nil {
					return err
				}
				out = existing
				return nil
			}
			next = reviveChainFact(existing, prepared)
			if err := resetDepositFactInboxForReappearance(ctx, tx, next); err != nil {
				return err
			}
		} else if prepared.BlockNumber > 0 {
			next.BlockNumber = prepared.BlockNumber
			next.BlockHash = prepared.BlockHash
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

func historicalBlockHashesEqual(chainID constants.ChainID, left, right string) bool {
	left = normalizeBlockIdentifier(left)
	right = normalizeBlockIdentifier(right)
	if chainID == constants.Solana {
		return left == right
	}
	return strings.EqualFold(left, right)
}

func canonicalBlockMatchesWithDB(ctx context.Context, tx *gorm.DB, chainID constants.ChainID, blockNumber int64, blockHash string) (bool, error) {
	if tx == nil || blockNumber <= 0 || strings.TrimSpace(blockHash) == "" {
		return false, nil
	}
	blockHash = normalizeBlockIdentifier(blockHash)
	var block models.Block
	// Do not take a block-row SHARE lock while the caller owns a fact/transaction
	// row lock: canonical correction takes those locks in block -> money-row
	// order, so the inverse order can deadlock. A concurrent correction that
	// commits after this snapshot will wait for and then reorg the revived row in
	// the same atomic correction transaction.
	query := tx.WithContext(ctx).
		Where("chain_id = ?", chainID).
		Where("number = ?", blockNumber).
		Where("canonical = ?", true).
		Where("status = ?", models.BlockStatusCanonical)
	if hasHexPrefix(blockHash) {
		query = query.Where("LOWER(hash) = ?", strings.ToLower(blockHash))
	} else {
		query = query.Where("hash = ?", blockHash)
	}
	err := query.First(&block).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return err == nil, err
}

// CanonicalBlockMatches lets a money-state consumer recheck the exact
// (chain,height,hash) tuple through the same transaction-bound repository used
// for its locked ChainFact read. Callers must still lock/reload the fact first;
// this method deliberately does not take a block row lock so canonical
// correction retains the global block -> money-row lock order.
func (r *ChainFactRepo) CanonicalBlockMatches(ctx context.Context, chainID constants.ChainID, blockNumber int64, blockHash string) (bool, error) {
	if r == nil || r.db == nil {
		return false, gorm.ErrInvalidDB
	}
	return canonicalBlockMatchesWithDB(ctx, r.db, chainID, blockNumber, blockHash)
}

func chainFactEconomicIdentityEqual(existing, incoming models.ChainFact) bool {
	if existing.EventID != incoming.EventID ||
		existing.ChainID != incoming.ChainID ||
		!chainFactTransactionHashesEqual(existing.ChainID, existing.TxHash, incoming.TxHash) ||
		normalizeChainFactLogIndex(existing.LogIndex) != normalizeChainFactLogIndex(incoming.LogIndex) ||
		NormalizeWalletLookupAddress(existing.ChainID, existing.ObservedAddress) != NormalizeWalletLookupAddress(incoming.ChainID, incoming.ObservedAddress) ||
		strings.TrimSpace(existing.Direction) != strings.TrimSpace(incoming.Direction) ||
		!chainAssetIdentityEqual(existing.ChainID, existing.Token, incoming.Token) ||
		!strings.EqualFold(strings.TrimSpace(existing.Symbol), strings.TrimSpace(incoming.Symbol)) ||
		existing.Decimals != incoming.Decimals ||
		strings.TrimSpace(existing.AmountRaw) != strings.TrimSpace(incoming.AmountRaw) ||
		normalizePaymentMemo(firstNonEmptyString(existing.MemoNormalized, existing.Memo)) != normalizePaymentMemo(firstNonEmptyString(incoming.MemoNormalized, incoming.Memo)) ||
		!strings.EqualFold(strings.TrimSpace(existing.SourceEventType), strings.TrimSpace(incoming.SourceEventType)) {
		return false
	}
	return chainFactPositiveRaw(incoming.AmountRaw)
}

func chainAssetIdentityEqual(chainID constants.ChainID, left, right *string) bool {
	leftValue := ""
	if left != nil {
		leftValue = strings.TrimSpace(*left)
	}
	rightValue := ""
	if right != nil {
		rightValue = strings.TrimSpace(*right)
	}
	return NormalizeWalletLookupAddress(chainID, leftValue) == NormalizeWalletLookupAddress(chainID, rightValue)
}

func reviveChainFact(existing, incoming models.ChainFact) models.ChainFact {
	next := existing
	next.BlockNumber = incoming.BlockNumber
	next.BlockHash = incoming.BlockHash
	next.ObservedAddress = incoming.ObservedAddress
	next.Direction = incoming.Direction
	next.ObservationStatus = incoming.ObservationStatus
	next.Memo = incoming.Memo
	next.MemoNormalized = incoming.MemoNormalized
	next.Token = incoming.Token
	next.Symbol = incoming.Symbol
	next.Decimals = incoming.Decimals
	next.AmountRaw = incoming.AmountRaw
	next.Confirmations = incoming.Confirmations
	next.ConfirmationsRequired = incoming.ConfirmationsRequired
	next.Finalized = incoming.Finalized
	next.Status = models.ChainFactStatusObserved
	next.ReorgedAt = nil
	next.SupersededByEventID = ""
	next.CorrectionReason = ""
	next.SourceEventType = incoming.SourceEventType
	next.RawMetadataJSON = incoming.RawMetadataJSON
	next.UpdatedAt = time.Now()
	return next
}

func resetDepositFactInboxForReappearance(ctx context.Context, tx *gorm.DB, fact models.ChainFact) error {
	now := time.Now()
	return tx.WithContext(ctx).
		Model(&models.MoneyEventInbox{}).
		Where("consumer_name = ? AND event_id = ?", "deposit_fact_processor", fact.EventID).
		Updates(map[string]any{
			"status":           models.MoneyEventInboxStatusReceived,
			"attempts":         0,
			"locked_until":     nil,
			"processed_at":     nil,
			"last_error":       "",
			"failure_category": "",
			"resource_id":      fact.ID.String(),
			"updated_at":       now,
		}).Error
}

func recordChainFactReappearanceMismatch(ctx context.Context, tx *gorm.DB, existing, incoming models.ChainFact) error {
	reason := "chain_fact_reappearance_payload_mismatch"
	_, _, err := NewReconciliationRepo(tx).CreateScopedOpenIfMissing(ctx, ReconciliationScope{
		ChainID:             existing.ChainID,
		FromBlock:           incoming.BlockNumber,
		ToBlock:             incoming.BlockNumber,
		Reason:              reason,
		ScopeKey:            reason + ":" + existing.EventID + ":" + normalizeBlockIdentifier(incoming.BlockHash),
		ResourceType:        "chain_fact",
		ResourceID:          existing.EventID,
		AffectedResourceIDs: []string{existing.EventID},
		Evidence: map[string]any{
			"old_block_number": existing.BlockNumber,
			"old_block_hash":   existing.BlockHash,
			"new_block_number": incoming.BlockNumber,
			"new_block_hash":   incoming.BlockHash,
			"tx_hash":          existing.TxHash,
			"log_index":        existing.LogIndex,
		},
	})
	return err
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

// FindByEventIDForUpdate is used by money-state consumers so a canonical
// correction cannot race between the consumer's status check and its writes.
// The block observer/reorg transaction will wait for this row lock and then
// reverse any state committed by the consumer as one atomic correction.
func (r *ChainFactRepo) FindByEventIDForUpdate(ctx context.Context, eventID string) (*models.ChainFact, error) {
	if r == nil || r.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	var fact models.ChainFact
	if err := r.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&fact, "event_id = ?", strings.TrimSpace(eventID)).Error; err != nil {
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
		Where(`NOT EXISTS (
			SELECT 1
			FROM money_event_inboxes deposit_inbox
			WHERE deposit_inbox.consumer_name = ?
			  AND deposit_inbox.event_id = chain_facts.event_id
			  AND deposit_inbox.status = ?
		)`, "deposit_fact_processor", models.MoneyEventInboxStatusDeadLetter).
		Where(`(
			deposits.id IS NULL
			OR (
				deposits.wallet_id IS NOT NULL
				AND (
					(deposits.status IN ? AND chain_facts.finalized = ?)
					OR deposits.status = ?
				)
			)
			OR (
				deposits.wallet_id IS NULL
				AND deposits.status = ?
			)
		)`,
			[]string{models.DepositStatusPending, models.DepositStatusConfirming},
			true,
			models.DepositStatusReorged,
			models.DepositStatusUnmatched,
		).
		// Oldest-first ordering guarantees that a continuous stream of new facts
		// cannot starve earlier retryable work. Dead-lettered poison facts are
		// excluded above and remain visible through inbox metrics/reconciliation.
		Order("chain_facts.created_at ASC, chain_facts.updated_at ASC").
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
		Where("event_id = ? OR (chain_id = ? AND tx_hash = ? AND log_index = ?)", eventID, txModel.ChainID, normalizeChainFactTransactionHash(txModel.ChainID, txModel.Hash), normalizeChainFactLogIndex(logIndex)).
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
	prepared.TxHash = normalizeChainFactTransactionHash(prepared.ChainID, trimChainFactDBString(prepared.TxHash))
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
