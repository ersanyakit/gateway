package repositories

import (
	"context"
	"core/constants"
	"core/models"
	webhooksvc "core/services/webhook"
	"core/types"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type TransactionRepo struct {
	db *gorm.DB
}

func (r *TransactionRepo) DB() *gorm.DB {
	return r.db
}

func NewTransactionRepo(db *gorm.DB) *TransactionRepo {
	return &TransactionRepo{db: db}
}

func (r *TransactionRepo) UniqueHash(params types.TransactionParam) (string, error) {
	hash := normalizeTransactionHash(params.Hash)
	if hash == "" {
		return "", errors.New("hash is required")
	}
	return fmt.Sprintf("%d-%s-%s", params.ChainID, hash, normalizeTransactionLogIndex(params.LogIndex)), nil
}

func (r *TransactionRepo) Create(params types.TransactionParam) error {
	ctx := params.Context
	if ctx == nil {
		ctx = context.Background()
	}
	uniqueHash, err := r.UniqueHash(params)
	if err != nil {
		return err
	}
	if params.Block == nil {
		return errors.New("block number is required")
	}
	if params.From == nil || params.To == nil {
		return errors.New("from/to required")
	}
	if params.Symbol == nil {
		return errors.New("symbol is required")
	}
	if params.Amount == nil {
		return errors.New("amount is required")
	}
	hash := normalizeTransactionHash(params.Hash)
	logIndex := normalizeTransactionLogIndex(params.LogIndex)
	logIndexPtr := normalizedTransactionLogIndexPtr(logIndex)

	return r.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		status := transactionInitialStatus(params.Status)
		blockHash := ""
		if params.BlockHash != nil {
			blockHash = normalizeBlockIdentifier(*params.BlockHash)
		}
		parentHash := ""
		if params.ParentHash != nil {
			parentHash = normalizeBlockIdentifier(*params.ParentHash)
		}
		var token interface{}
		if params.Token != nil {
			token = *params.Token
		}

		now := time.Now()
		// Canonical identity is authoritative and must be persisted before taking
		// the transaction row lock. Reorg correction uses block -> money-row lock
		// order; following the same order here avoids an inverse-lock deadlock and
		// lets an already-reorged transaction prove exact canonical reappearance in
		// this same transaction.
		if err := r.observeCanonicalBlockWithDB(ctx, tx, params.ChainID, *params.Block, blockHash, parentHash, uniqueHash, now); err != nil {
			return err
		}
		existing, found, err := r.findExistingForCreateWithDB(ctx, tx, uniqueHash)
		if err != nil {
			return err
		}
		reviving := false
		if found {
			identityChanged := transactionBlockIdentityChanged(existing, *params.Block, blockHash)
			if existing.Status == models.TransactionStatusFailed {
				return nil
			}
			if existing.Status == models.TransactionStatusReorged {
				canonical, err := canonicalBlockMatchesWithDB(ctx, tx, params.ChainID, parseBlockNumber(*params.Block), blockHash)
				if err != nil {
					return err
				}
				if !canonical || !transactionEconomicIdentityEqual(existing, params, hash, logIndex) {
					return recordTransactionReappearanceReview(ctx, tx, existing, params, blockHash, canonical)
				}
				reviving = true
			} else if existing.FinalizedAt != nil && identityChanged {
				// The same economic transaction moved to another authoritative block.
				// Reverse the orphaned generation first, then reload the corrected row
				// and continue through the exact canonical-reappearance path instead of
				// returning with a permanently reorged transaction.
				fromBlock := parseBlockNumber(existing.BlockNumber)
				reason := transactionReorgReason("tx_block_identity_changed", params.ChainID, existing.BlockNumber)
				if err := r.markTransactionsReorgedWithDB(ctx, tx, []models.Transaction{existing}, now, params.ChainID, fromBlock, fromBlock, reason); err != nil {
					return err
				}
				existing, found, err = r.findExistingForCreateWithDB(ctx, tx, uniqueHash)
				if err != nil {
					return err
				}
				if !found {
					return gorm.ErrRecordNotFound
				}
				canonical, err := canonicalBlockMatchesWithDB(ctx, tx, params.ChainID, parseBlockNumber(*params.Block), blockHash)
				if err != nil {
					return err
				}
				if !canonical || !transactionEconomicIdentityEqual(existing, params, hash, logIndex) {
					return recordTransactionReappearanceReview(ctx, tx, existing, params, blockHash, canonical)
				}
				reviving = true
			} else if existing.FinalizedAt != nil {
				return nil
			}
		}
		identityChanged := found && transactionBlockIdentityChanged(existing, *params.Block, blockHash)
		resetLifecycle := identityChanged || reviving
		assignments := map[string]interface{}{
			"hash":          hash,
			"log_index":     logIndexPtr,
			"block_number":  *params.Block,
			"block_hash":    blockHash,
			"token":         token,
			"symbol":        *params.Symbol,
			"decimals":      params.Decimals,
			"from_address":  *params.From,
			"to_address":    *params.To,
			"amount":        *params.Amount,
			"status":        status,
			"confirmations": uint(0),
			"updated_at":    now,
		}
		if found && !resetLifecycle {
			delete(assignments, "status")
			delete(assignments, "confirmations")
		}
		if resetLifecycle {
			// Canonical reappearance starts a fresh finality generation even when
			// the provider already labels the transaction confirmed. Restored money
			// state and merchant notification are committed only by MarkFinality.
			assignments["status"] = models.TransactionStatusPendingConfirmation
			assignments["confirmations_required"] = uint(1)
			assignments["finalized_at"] = nil
			assignments["reorged_at"] = nil
			assignments["correction_reason"] = transactionReappearanceMarker(existing.ReorgedAt, *params.Block, blockHash)
			assignments["webhook_sent_at"] = nil
			assignments["webhook_attempts"] = uint(0)
			assignments["webhook_last_error"] = ""
			assignments["webhook_locked_until"] = nil
		}
		txModel := &models.Transaction{
			ID:          uuid.New(),
			ChainID:     params.ChainID,
			Hash:        hash,
			LogIndex:    logIndexPtr,
			BlockNumber: *params.Block,
			Symbol:      *params.Symbol,
			Decimals:    params.Decimals,
			BlockHash:   blockHash,
			Token:       params.Token,
			FromAddress: *params.From,
			ToAddress:   *params.To,
			Amount:      *params.Amount,
			UniqueHash:  uniqueHash,
			Status:      status,
			CreatedAt:   now,
			UpdatedAt:   now,
		}

		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "unique_hash"}},
			DoUpdates: clause.Assignments(assignments),
		}).Create(txModel).Error; err != nil {
			return err
		}
		return nil
	})
}

const transactionReappearanceMarkerPrefix = "canonical_reappearance:"

func transactionReappearanceMarker(reorgedAt *time.Time, blockNumber, blockHash string) string {
	epoch := moneyEventGenerationUnixNano(time.Now())
	if reorgedAt != nil && !reorgedAt.IsZero() {
		epoch = moneyEventGenerationUnixNano(*reorgedAt)
	}
	return boundedCorrectionReason(fmt.Sprintf("%s%d:%s:%s", transactionReappearanceMarkerPrefix, epoch, strings.TrimSpace(blockNumber), normalizeBlockIdentifier(blockHash)))
}

// PostgreSQL timestamps have microsecond precision. Event generations derived
// from a persisted ReorgedAt must use that same precision or a restoration
// reconstructed later can reference a nanosecond-qualified correction ID that
// never existed.
func moneyEventGenerationUnixNano(value time.Time) int64 {
	return value.UTC().Truncate(time.Microsecond).UnixNano()
}

func transactionIsCanonicalReappearance(txModel models.Transaction) bool {
	return strings.HasPrefix(strings.TrimSpace(txModel.CorrectionReason), transactionReappearanceMarkerPrefix)
}

func transactionEconomicIdentityEqual(existing models.Transaction, params types.TransactionParam, hash, logIndex string) bool {
	if existing.ChainID != params.ChainID ||
		!chainFactTransactionHashesEqual(existing.ChainID, existing.Hash, hash) ||
		normalizeTransactionLogIndex(existing.LogIndex) != logIndex ||
		!chainAssetIdentityEqual(existing.ChainID, existing.Token, params.Token) ||
		!strings.EqualFold(strings.TrimSpace(existing.Symbol), strings.TrimSpace(ptrString(params.Symbol))) ||
		existing.Decimals != params.Decimals ||
		NormalizeWalletLookupAddress(existing.ChainID, existing.FromAddress) != NormalizeWalletLookupAddress(params.ChainID, ptrString(params.From)) ||
		NormalizeWalletLookupAddress(existing.ChainID, existing.ToAddress) != NormalizeWalletLookupAddress(params.ChainID, ptrString(params.To)) ||
		strings.TrimSpace(existing.Amount) != strings.TrimSpace(ptrString(params.Amount)) {
		return false
	}
	return chainFactPositiveRaw(existing.Amount)
}

func recordTransactionReappearanceReview(ctx context.Context, tx *gorm.DB, existing models.Transaction, params types.TransactionParam, blockHash string, canonical bool) error {
	reason := "tx_reappearance_not_canonical"
	if canonical {
		reason = "tx_reappearance_payload_mismatch"
	}
	fromBlock := parseBlockNumber(existing.BlockNumber)
	toBlock := parseBlockNumber(ptrString(params.Block))
	_, _, err := NewReconciliationRepo(tx).CreateScopedOpenIfMissing(ctx, ReconciliationScope{
		ChainID:             params.ChainID,
		FromBlock:           fromBlock,
		ToBlock:             toBlock,
		Reason:              reason,
		ScopeKey:            fmt.Sprintf("%s:%s:%s:%s", reason, existing.UniqueHash, strings.TrimSpace(ptrString(params.Block)), normalizeBlockIdentifier(blockHash)),
		ResourceType:        "transaction_reappeared",
		ResourceID:          existing.UniqueHash,
		AffectedResourceIDs: []string{existing.UniqueHash},
		Evidence: map[string]any{
			"canonical_match":       canonical,
			"original_block_number": existing.BlockNumber,
			"original_block_hash":   existing.BlockHash,
			"new_block_number":      ptrString(params.Block),
			"new_block_hash":        blockHash,
		},
	})
	return err
}

func recordTransactionRestoredEvent(ctx context.Context, tx *gorm.DB, restored models.Transaction, occurredAt time.Time) error {
	if restored.MerchantID == nil || restored.DomainID == nil || restored.ID == uuid.Nil {
		return nil
	}
	generation, ok := transactionReappearanceGeneration(restored.CorrectionReason)
	if !ok {
		return errors.New("canonical reappearance transaction has no restoration generation")
	}
	eventName := constants.WebhookEventTransactionRestored
	eventID := fmt.Sprintf("%s:%s:%d", restored.ID.String(), eventName, generation)
	reorgEventID := fmt.Sprintf("%s:%s:%d", restored.ID.String(), constants.WebhookEventTransactionReorged, generation)
	var reorgEvent models.MoneyEventOutbox
	reorgPayload := make(map[string]any)
	if err := tx.WithContext(ctx).First(&reorgEvent, "event_id = ?", reorgEventID).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("load transaction reorg event for restoration: %w", err)
	} else if err == nil {
		if err := json.Unmarshal([]byte(reorgEvent.PayloadJSON), &reorgPayload); err != nil {
			return fmt.Errorf("decode transaction reorg event for restoration: %w", err)
		}
	}
	payload := map[string]any{
		"event_id":                   eventID,
		"event_type":                 eventName,
		"event_version":              constants.WebhookEventVersionV1,
		"occurred_at":                occurredAt.UTC().Format(time.RFC3339Nano),
		"merchant_id":                restored.MerchantID.String(),
		"domain_id":                  restored.DomainID.String(),
		"resource_type":              "transaction",
		"resource_id":                restored.ID.String(),
		"resource_status":            restored.Status,
		"idempotency_key":            eventID,
		"correlation_id":             "transaction:" + restored.UniqueHash,
		"restoration":                true,
		"restoration_reason":         "transaction reappeared in an exact canonical block",
		"reorg_event_id":             reorgEventID,
		"restored_from_event_id":     reorgEventID,
		"transaction_id":             restored.ID.String(),
		"tx_hash":                    restored.Hash,
		"tx_unique_hash":             restored.UniqueHash,
		"chain_id":                   int64(restored.ChainID),
		"log_index":                  normalizeTransactionLogIndex(restored.LogIndex),
		"amount_raw":                 restored.Amount,
		"symbol":                     restored.Symbol,
		"token":                      restored.Token,
		"orphaned_block_number":      reorgPayload["orphaned_block_number"],
		"orphaned_block_hash":        reorgPayload["orphaned_block_hash"],
		"canonical_block_number":     restored.BlockNumber,
		"canonical_block_hash":       normalizeBlockIdentifier(restored.BlockHash),
		"original_event_id":          restored.OriginalEventID,
		"original_resource_id":       restored.OriginalResourceID,
		"previous_correction_reason": reorgPayload["correction_reason"],
		"confirmations":              restored.Confirmations,
		"confirmations_required":     restored.ConfirmationsRequired,
		"finalized_at":               restored.FinalizedAt,
	}
	event, err := BuildMoneyEventOutbox(MoneyEventOutboxBuildParams{
		EventName:      eventName,
		EventID:        eventID,
		AggregateType:  "transaction",
		AggregateID:    restored.ID.String(),
		MerchantID:     *restored.MerchantID,
		DomainID:       *restored.DomainID,
		IdempotencyKey: eventID,
		Payload:        payload,
	})
	if err != nil {
		return err
	}
	_, _, err = NewMoneyEventOutboxRepo(tx).RecordWithDB(ctx, tx, &event)
	return err
}

func transactionReappearanceGeneration(reason string) (int64, bool) {
	reason = strings.TrimSpace(reason)
	if !strings.HasPrefix(reason, transactionReappearanceMarkerPrefix) {
		return 0, false
	}
	remainder := strings.TrimPrefix(reason, transactionReappearanceMarkerPrefix)
	separator := strings.IndexByte(remainder, ':')
	if separator <= 0 {
		return 0, false
	}
	generation, err := strconv.ParseInt(remainder[:separator], 10, 64)
	return generation, err == nil && generation > 0
}

func normalizeTransactionHash(hash *string) string {
	if hash == nil {
		return ""
	}
	value := strings.TrimSpace(*hash)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(value), "0x") {
		return strings.ToLower(value)
	}
	return value
}

func normalizeTransactionLogIndex(logIndex *string) string {
	if logIndex == nil {
		return ""
	}
	value := strings.TrimSpace(*logIndex)
	if value == "" {
		return ""
	}
	prefix, suffix, ok := strings.Cut(value, ":")
	if !ok {
		return normalizeTransactionLogIndexNumber(value)
	}
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	suffix = normalizeTransactionLogIndexNumber(suffix)
	if prefix == "" || suffix == "" {
		return strings.TrimSpace(value)
	}
	return prefix + ":" + suffix
}

func normalizeTransactionLogIndexNumber(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(value), "0x") {
		parsed, ok := new(big.Int).SetString(value[2:], 16)
		if ok {
			return parsed.String()
		}
	}
	return value
}

func normalizedTransactionLogIndexPtr(logIndex string) *string {
	if logIndex == "" {
		return nil
	}
	return &logIndex
}

func normalizeBlockIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if hasHexPrefix(value) {
		return strings.ToLower(value)
	}
	return value
}

func hasHexPrefix(value string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "0x")
}

func transactionInitialStatus(status *string) string {
	if status == nil {
		return models.TransactionStatusPending
	}
	switch strings.ToLower(strings.TrimSpace(*status)) {
	case models.TransactionStatusFailed:
		return models.TransactionStatusFailed
	case models.TransactionStatusConfirmed:
		return models.TransactionStatusPendingConfirmation
	case models.TransactionStatusPendingConfirmation:
		return models.TransactionStatusPendingConfirmation
	case models.TransactionStatusReorged:
		return models.TransactionStatusReorged
	default:
		return models.TransactionStatusPending
	}
}

func transactionBlockIdentityChanged(existing models.Transaction, blockNumber string, blockHash string) bool {
	if strings.TrimSpace(existing.BlockNumber) != strings.TrimSpace(blockNumber) {
		return true
	}
	existingHash := strings.TrimSpace(existing.BlockHash)
	nextHash := strings.TrimSpace(blockHash)
	return existingHash != "" && nextHash != "" && !historicalBlockHashesEqual(existing.ChainID, existingHash, nextHash)
}

func parseBlockNumber(blockNumber string) int64 {
	block, err := strconv.ParseInt(strings.TrimSpace(blockNumber), 10, 64)
	if err != nil || block < 0 {
		return 0
	}
	return block
}

func transactionReorgReason(prefix string, chainID constants.ChainID, blockNumber string) string {
	reason := fmt.Sprintf("%s:%d:%s", prefix, chainID, strings.TrimSpace(blockNumber))
	return boundedCorrectionReason(reason)
}

func boundedCorrectionReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if len(reason) > 120 {
		reason = reason[:120]
	}
	return reason
}

func (r *TransactionRepo) findExistingForCreateWithDB(ctx context.Context, tx *gorm.DB, uniqueHash string) (models.Transaction, bool, error) {
	var existing models.Transaction
	err := tx.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&existing, "unique_hash = ?", uniqueHash).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.Transaction{}, false, nil
	}
	if err != nil {
		return models.Transaction{}, false, err
	}
	return existing, true, nil
}

func (r *TransactionRepo) markBlockHashConflictsWithDB(ctx context.Context, tx *gorm.DB, chainID constants.ChainID, blockNumber string, blockHash string, currentUniqueHash string, now time.Time) error {
	blockNumber = strings.TrimSpace(blockNumber)
	blockHash = normalizeBlockIdentifier(blockHash)
	if blockNumber == "" || blockHash == "" {
		return nil
	}

	var conflicts []models.Transaction
	query := tx.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("chain_id = ?", chainID).
		Where("block_number = ?", blockNumber).
		Where("block_hash <> ''").
		Where("unique_hash <> ?", currentUniqueHash).
		Where("status <> ?", models.TransactionStatusReorged)
	if hasHexPrefix(blockHash) {
		query = query.Where("LOWER(block_hash) <> ?", strings.ToLower(blockHash))
	} else {
		query = query.Where("block_hash <> ?", blockHash)
	}
	if err := query.Find(&conflicts).Error; err != nil {
		return err
	}
	if len(conflicts) == 0 {
		return nil
	}

	fromBlock := parseBlockNumber(blockNumber)
	reason := transactionReorgReason("reorg_detected", chainID, blockNumber)
	return r.markTransactionsReorgedWithDB(ctx, tx, conflicts, now, chainID, fromBlock, fromBlock, reason)
}

func (r *TransactionRepo) observeCanonicalBlockWithDB(ctx context.Context, tx *gorm.DB, chainID constants.ChainID, blockNumberRaw string, blockHash string, parentHash string, currentUniqueHash string, now time.Time) error {
	blockNumber := parseBlockNumber(blockNumberRaw)
	blockHash = normalizeBlockIdentifier(blockHash)
	parentHash = normalizeBlockIdentifier(parentHash)
	if blockNumber <= 0 || blockHash == "" {
		return nil
	}
	// Serialize competing scanners/rescans before reading or replacing the
	// canonical row. The database partial unique index is the final invariant;
	// this lock also makes the reorg correction sequence deterministic.
	if err := AcquireCanonicalBlockLockWithDB(ctx, tx, chainID, blockNumber); err != nil {
		return err
	}

	if parentHash != "" && blockNumber > 1 {
		if err := r.markParentHashConflictsWithDB(ctx, tx, chainID, blockNumber, parentHash, currentUniqueHash, now); err != nil {
			return err
		}
	}

	reason := transactionReorgReason("reorg_detected", chainID, strconv.FormatInt(blockNumber, 10))
	heightConflict, err := canonicalBlockHeightConflictExistsWithDB(ctx, tx, chainID, blockNumber, blockHash)
	if err != nil {
		return err
	}
	// A legacy repair/migration can already have selected the provider's block
	// as the sole canonical row while transactions from a discarded duplicate
	// still exist. Reconcile transaction economics on every authoritative block
	// observation, not only when the block table itself still shows a conflict.
	if err := r.markBlockHashConflictsWithDB(ctx, tx, chainID, blockNumberRaw, blockHash, currentUniqueHash, now); err != nil {
		return err
	}
	if heightConflict {
		// Correct money state before replacing the canonical block record. Looking
		// only at the new block's parent catches the tip of a fork but can leave
		// transactions near the fork's common ancestor incorrectly credited.
		if err := r.markBlockRangeTransactionsReorgedWithDB(ctx, tx, chainID, blockNumber, currentUniqueHash, now, reason); err != nil {
			return err
		}
	}
	if err := markCanonicalBlockHeightConflictsWithDB(ctx, tx, chainID, blockNumber, blockHash, reason, now); err != nil {
		return err
	}

	return upsertCanonicalBlockWithDB(ctx, tx, chainID, blockNumber, blockHash, parentHash, now)
}

func CanonicalBlockLockKey(chainID constants.ChainID, blockNumber int64) string {
	return fmt.Sprintf("canonical-block:%d:%d", chainID, blockNumber)
}

// AcquireCanonicalBlockLockWithDB establishes the global lock order used by
// canonical observers and money-state consumers: canonical height first, then
// ChainFact/Transaction/Deposit/inbox rows. PostgreSQL advisory locks are held
// until the surrounding transaction completes.
func AcquireCanonicalBlockLockWithDB(ctx context.Context, tx *gorm.DB, chainID constants.ChainID, blockNumber int64) error {
	if tx == nil {
		return gorm.ErrInvalidDB
	}
	if blockNumber <= 0 {
		return nil
	}
	if tx.Dialector == nil || tx.Dialector.Name() != "postgres" {
		return nil
	}
	return tx.WithContext(ctx).Exec("SELECT pg_advisory_xact_lock(hashtext(?))", CanonicalBlockLockKey(chainID, blockNumber)).Error
}

func canonicalBlockHeightConflictExistsWithDB(ctx context.Context, tx *gorm.DB, chainID constants.ChainID, blockNumber int64, blockHash string) (bool, error) {
	var count int64
	err := tx.WithContext(ctx).
		Model(&models.Block{}).
		Where("chain_id = ?", chainID).
		Where("number = ?", blockNumber).
		Where("canonical = ?", true).
		Where("hash <> ?", normalizeBlockIdentifier(blockHash)).
		Count(&count).Error
	return count > 0, err
}

func (r *TransactionRepo) ObserveCanonicalBlock(ctx context.Context, chainID constants.ChainID, blockNumber int64, blockHash string, parentHash string) error {
	if r == nil || r.db == nil {
		return gorm.ErrInvalidDB
	}
	if blockNumber <= 0 {
		return nil
	}
	now := time.Now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return r.observeCanonicalBlockWithDB(ctx, tx, chainID, strconv.FormatInt(blockNumber, 10), blockHash, parentHash, "", now)
	})
}

func (r *TransactionRepo) markParentHashConflictsWithDB(ctx context.Context, tx *gorm.DB, chainID constants.ChainID, blockNumber int64, parentHash string, currentUniqueHash string, now time.Time) error {
	var parent models.Block
	err := tx.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("chain_id = ?", chainID).
		Where("number = ?", blockNumber-1).
		Where("canonical = ?", true).
		Where("hash <> ?", parentHash).
		Order("updated_at DESC").
		First(&parent).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	reason := transactionReorgReason("parent_mismatch", chainID, strconv.FormatInt(parent.Number, 10))
	if err := r.markBlockRangeTransactionsReorgedWithDB(ctx, tx, chainID, parent.Number, currentUniqueHash, now, reason); err != nil {
		return err
	}
	return markCanonicalBlockRangeReorgedWithDB(ctx, tx, chainID, parent.Number, parentHash, reason, now)
}

func (r *TransactionRepo) markBlockRangeTransactionsReorgedWithDB(ctx context.Context, tx *gorm.DB, chainID constants.ChainID, fromBlock int64, currentUniqueHash string, now time.Time, reason string) error {
	if fromBlock <= 0 {
		return nil
	}

	var blocks []models.Block
	if err := tx.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("chain_id = ?", chainID).
		Where("number >= ?", fromBlock).
		Where("canonical = ?", true).
		Where("status <> ?", models.BlockStatusReorged).
		Find(&blocks).Error; err != nil {
		return err
	}
	if len(blocks) == 0 {
		return nil
	}

	hashes := make([]string, 0, len(blocks))
	hexHashes := make([]string, 0, len(blocks))
	toBlock := fromBlock
	for _, block := range blocks {
		if hash := normalizeBlockIdentifier(block.Hash); hash != "" {
			if hasHexPrefix(hash) {
				hexHashes = append(hexHashes, strings.ToLower(hash))
			} else {
				hashes = append(hashes, hash)
			}
		}
		if block.Number > toBlock {
			toBlock = block.Number
		}
	}
	if len(hashes) == 0 && len(hexHashes) == 0 {
		return nil
	}
	if err := markChainFactsReorgedByBlockHashesWithDB(ctx, tx, chainID, hashes, hexHashes, reason, now); err != nil {
		return err
	}

	var conflicts []models.Transaction
	query := tx.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("chain_id = ?", chainID).
		Where("unique_hash <> ?", currentUniqueHash).
		Where("status <> ?", models.TransactionStatusReorged)
	switch {
	case len(hashes) > 0 && len(hexHashes) > 0:
		query = query.Where("(block_hash IN ? OR LOWER(block_hash) IN ?)", hashes, hexHashes)
	case len(hashes) > 0:
		query = query.Where("block_hash IN ?", hashes)
	default:
		query = query.Where("LOWER(block_hash) IN ?", hexHashes)
	}
	if err := query.Find(&conflicts).Error; err != nil {
		return err
	}
	return r.markTransactionsReorgedWithDB(ctx, tx, conflicts, now, chainID, fromBlock, toBlock, reason)
}

func markChainFactsReorgedByBlockHashesWithDB(ctx context.Context, tx *gorm.DB, chainID constants.ChainID, hashes, hexHashes []string, reason string, now time.Time) error {
	query := tx.WithContext(ctx).
		Model(&models.ChainFact{}).
		Where("chain_id = ?", chainID).
		Where("status NOT IN ?", []string{models.ChainFactStatusReorged, models.ChainFactStatusSuperseded})
	switch {
	case len(hashes) > 0 && len(hexHashes) > 0:
		query = query.Where("(block_hash IN ? OR LOWER(block_hash) IN ?)", hashes, hexHashes)
	case len(hashes) > 0:
		query = query.Where("block_hash IN ?", hashes)
	case len(hexHashes) > 0:
		query = query.Where("LOWER(block_hash) IN ?", hexHashes)
	default:
		return nil
	}
	return query.Updates(map[string]any{
		"status":            models.ChainFactStatusReorged,
		"reorged_at":        &now,
		"correction_reason": boundedCorrectionReason(reason),
		"updated_at":        now,
	}).Error
}

func markCanonicalBlockHeightConflictsWithDB(ctx context.Context, tx *gorm.DB, chainID constants.ChainID, blockNumber int64, blockHash string, reason string, now time.Time) error {
	return tx.WithContext(ctx).
		Model(&models.Block{}).
		Where("chain_id = ?", chainID).
		Where("number = ?", blockNumber).
		Where("canonical = ?", true).
		Where("hash <> ?", blockHash).
		Updates(reorgedBlockAssignments(blockHash, reason, now)).Error
}

func markCanonicalBlockRangeReorgedWithDB(ctx context.Context, tx *gorm.DB, chainID constants.ChainID, fromBlock int64, supersededByHash string, reason string, now time.Time) error {
	return tx.WithContext(ctx).
		Model(&models.Block{}).
		Where("chain_id = ?", chainID).
		Where("number >= ?", fromBlock).
		Where("canonical = ?", true).
		Updates(reorgedBlockAssignments(supersededByHash, reason, now)).Error
}

func reorgedBlockAssignments(supersededByHash string, reason string, now time.Time) map[string]any {
	return map[string]any{
		"canonical":          false,
		"status":             models.BlockStatusReorged,
		"reorged_at":         &now,
		"superseded_by_hash": normalizeBlockIdentifier(supersededByHash),
		"correction_reason":  boundedCorrectionReason(reason),
		"updated_at":         now,
	}
}

func upsertCanonicalBlockWithDB(ctx context.Context, tx *gorm.DB, chainID constants.ChainID, blockNumber int64, blockHash string, parentHash string, now time.Time) error {
	blockHash = normalizeBlockIdentifier(blockHash)
	parentHash = normalizeBlockIdentifier(parentHash)
	block := models.Block{
		ID:         uuid.New(),
		ChainID:    chainID,
		Number:     blockNumber,
		Hash:       blockHash,
		ParentHash: parentHash,
		Timestamp:  now,
		Processed:  true,
		Canonical:  true,
		Status:     models.BlockStatusCanonical,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	return tx.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "chain_id"}, {Name: "hash"}},
		DoUpdates: clause.Assignments(map[string]any{
			"number":             blockNumber,
			"parent_hash":        parentHash,
			"timestamp":          now,
			"processed":          true,
			"canonical":          true,
			"status":             models.BlockStatusCanonical,
			"reorged_at":         nil,
			"superseded_by_hash": "",
			"correction_reason":  "",
			"updated_at":         now,
		}),
	}).Create(&block).Error
}

func (r *TransactionRepo) markTransactionsReorgedWithDB(ctx context.Context, tx *gorm.DB, conflicts []models.Transaction, now time.Time, chainID constants.ChainID, fromBlock int64, toBlock int64, reason string) error {
	if len(conflicts) == 0 {
		return nil
	}
	uniqueHashes := make([]string, 0, len(conflicts))
	for _, conflict := range conflicts {
		if conflict.Status == models.TransactionStatusReorged {
			continue
		}
		uniqueHashes = append(uniqueHashes, conflict.UniqueHash)
		if err := NewLedgerRepo(tx).PostTransactionReversalWithDB(ctx, tx, conflict); err != nil {
			return err
		}
		if err := NewPaymentRepo(tx).MarkReorgedByTransactionWithDB(ctx, tx, conflict.UniqueHash); err != nil {
			return err
		}
		if err := NewChainFactRepo(tx).MarkReorgedByTransactionWithDB(ctx, tx, conflict, reason); err != nil {
			return err
		}
		if err := NewDepositRepo(tx).MarkReorgedByTransactionWithDB(ctx, tx, conflict, reason); err != nil {
			return err
		}
		originalEventID := webhooksvc.TransactionEventID(conflict)
		updateResult := tx.WithContext(ctx).
			Model(&models.Transaction{}).
			Where("id = ?", conflict.ID).
			Where("status <> ?", models.TransactionStatusReorged).
			Updates(map[string]any{
				"status":               models.TransactionStatusReorged,
				"event_type":           constants.WebhookEventTransactionReorged,
				"reorged_at":           &now,
				"original_event_id":    originalEventID,
				"original_resource_id": conflict.ID.String(),
				"correction_reason":    boundedCorrectionReason(reason),
				"webhook_sent_at":      nil,
				"webhook_attempts":     0,
				"webhook_last_error":   "",
				"webhook_locked_until": nil,
				"updated_at":           now,
			})
		if updateResult.Error != nil {
			return updateResult.Error
		}
		if updateResult.RowsAffected == 1 {
			if err := recordTransactionReorgEvent(ctx, tx, conflict, originalEventID, reason, now); err != nil {
				return err
			}
		}
	}
	if len(uniqueHashes) == 0 {
		return nil
	}

	if strings.TrimSpace(reason) == "" {
		reason = transactionReorgReason("reorg_detected", chainID, strconv.FormatInt(fromBlock, 10))
	}
	if toBlock < fromBlock {
		toBlock = fromBlock
	}
	_, _, err := NewReconciliationRepo(tx).CreateScopedOpenIfMissing(ctx, ReconciliationScope{
		ChainID:             chainID,
		FromBlock:           fromBlock,
		ToBlock:             toBlock,
		Reason:              reason,
		ScopeKey:            fmt.Sprintf("transaction_reorg:%d:%d:%d:%s", chainID, fromBlock, toBlock, reason),
		ResourceType:        "transaction_reorg",
		ResourceID:          reason,
		AffectedResourceIDs: uniqueHashes,
		Evidence: map[string]any{
			"chain_id":          chainID,
			"from_block":        fromBlock,
			"to_block":          toBlock,
			"transaction_count": len(uniqueHashes),
			"unique_hashes":     uniqueHashes,
		},
	})
	return err
}

func recordTransactionReorgEvent(ctx context.Context, tx *gorm.DB, txModel models.Transaction, originalEventID, reason string, occurredAt time.Time) error {
	if txModel.MerchantID == nil || txModel.DomainID == nil || txModel.ID == uuid.Nil {
		return nil
	}
	eventName := "transaction.reorged.v1"
	eventID := fmt.Sprintf("%s:%s:%d", txModel.ID.String(), eventName, moneyEventGenerationUnixNano(occurredAt))
	payload := map[string]any{
		"event_id":              eventID,
		"event_type":            eventName,
		"event_version":         constants.WebhookEventVersionV1,
		"occurred_at":           occurredAt.UTC().Format(time.RFC3339Nano),
		"merchant_id":           txModel.MerchantID.String(),
		"domain_id":             txModel.DomainID.String(),
		"resource_type":         "transaction",
		"resource_id":           txModel.ID.String(),
		"resource_status":       models.TransactionStatusReorged,
		"idempotency_key":       eventID,
		"correlation_id":        "transaction:" + txModel.UniqueHash,
		"correction":            true,
		"transaction_id":        txModel.ID.String(),
		"tx_hash":               txModel.Hash,
		"tx_unique_hash":        txModel.UniqueHash,
		"chain_id":              int64(txModel.ChainID),
		"orphaned_block_number": txModel.BlockNumber,
		"orphaned_block_hash":   txModel.BlockHash,
		"original_event_id":     originalEventID,
		"original_resource_id":  txModel.ID.String(),
		"correction_reason":     boundedCorrectionReason(reason),
	}
	event, err := BuildMoneyEventOutbox(MoneyEventOutboxBuildParams{
		EventName:      eventName,
		EventID:        eventID,
		AggregateType:  "transaction",
		AggregateID:    txModel.ID.String(),
		MerchantID:     *txModel.MerchantID,
		DomainID:       *txModel.DomainID,
		IdempotencyKey: eventID,
		Payload:        payload,
	})
	if err != nil {
		return err
	}
	_, _, err = NewMoneyEventOutboxRepo(tx).RecordWithDB(ctx, tx, &event)
	return err
}

func (r *TransactionRepo) FindByUniqueHash(ctx context.Context, uniqueHash string) (*models.Transaction, error) {
	var txModel models.Transaction
	err := r.DB().WithContext(ctx).
		First(&txModel, "unique_hash = ?", uniqueHash).Error
	if err != nil {
		return nil, err
	}
	return &txModel, nil
}

func (r *TransactionRepo) FindFinalizedByHash(ctx context.Context, chainID constants.ChainID, txHash string) (*models.Transaction, error) {
	hash := normalizeTransactionHash(&txHash)
	if hash == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var txModel models.Transaction
	query := r.DB().WithContext(ctx).
		Where("chain_id = ?", chainID).
		Where("status = ?", models.TransactionStatusConfirmed).
		Where("finalized_at IS NOT NULL")
	if hasHexPrefix(hash) {
		query = query.Where("LOWER(hash) = ?", strings.ToLower(hash))
	} else {
		query = query.Where("hash = ?", hash)
	}
	err := query.Order("finalized_at DESC").First(&txModel).Error
	if err != nil {
		return nil, err
	}
	return &txModel, nil
}

func (r *TransactionRepo) FindFailedByHash(ctx context.Context, chainID constants.ChainID, txHash string) (*models.Transaction, error) {
	hash := normalizeTransactionHash(&txHash)
	if hash == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var txModel models.Transaction
	query := r.DB().WithContext(ctx).
		Where("chain_id = ?", chainID).
		Where("status = ?", models.TransactionStatusFailed)
	if hasHexPrefix(hash) {
		query = query.Where("LOWER(hash) = ?", strings.ToLower(hash))
	} else {
		query = query.Where("hash = ?", hash)
	}
	err := query.Order("updated_at DESC").First(&txModel).Error
	if err != nil {
		return nil, err
	}
	return &txModel, nil
}

func (r *TransactionRepo) FindTerminalByHash(ctx context.Context, chainID constants.ChainID, txHash string) (*models.Transaction, error) {
	hash := normalizeTransactionHash(&txHash)
	if hash == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var txModel models.Transaction
	query := r.DB().WithContext(ctx).
		Where("chain_id = ?", chainID).
		Where("((status = ? AND finalized_at IS NOT NULL) OR status = ?)", models.TransactionStatusConfirmed, models.TransactionStatusFailed)
	if hasHexPrefix(hash) {
		query = query.Where("LOWER(hash) = ?", strings.ToLower(hash))
	} else {
		query = query.Where("hash = ?", hash)
	}
	err := query.Order("updated_at DESC").First(&txModel).Error
	if err != nil {
		return nil, err
	}
	return &txModel, nil
}

func (r *TransactionRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.Transaction, error) {
	var txModel models.Transaction
	err := r.DB().WithContext(ctx).First(&txModel, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &txModel, nil
}

func (r *TransactionRepo) BindWallet(ctx context.Context, uniqueHash, eventType string, wallet *models.Wallet) (*models.Transaction, error) {
	if wallet == nil {
		return nil, errors.New("wallet is required")
	}
	if r == nil || r.DB() == nil {
		return nil, gorm.ErrInvalidDB
	}
	merchantID := wallet.MerchantID
	domainID := wallet.DomainID
	walletID := wallet.ID

	updates := map[string]interface{}{
		"event_type":  eventType,
		"wallet_id":   &walletID,
		"merchant_id": &merchantID,
		"domain_id":   &domainID,
		"product_id":  wallet.ProductID,
		"user_id":     wallet.UserID,
		"updated_at":  time.Now(),
	}

	if err := r.DB().WithContext(ctx).
		Model(&models.Transaction{}).
		Where("unique_hash = ?", uniqueHash).
		Updates(updates).Error; err != nil {
		return nil, err
	}

	return r.FindByUniqueHash(ctx, uniqueHash)
}

func (r *TransactionRepo) MarkWebhookAttempt(ctx context.Context, uniqueHash string, delivered bool, lastErr error) error {
	var current models.Transaction
	if err := r.DB().WithContext(ctx).Select("webhook_attempts").First(&current, "unique_hash = ?", uniqueHash).Error; err != nil {
		return err
	}
	attempts := current.WebhookAttempts + 1
	updates := map[string]interface{}{
		"webhook_attempts":     gorm.Expr("webhook_attempts + 1"),
		"webhook_locked_until": nil,
		"updated_at":           time.Now(),
	}

	if delivered {
		now := time.Now()
		updates["webhook_sent_at"] = &now
		updates["webhook_last_error"] = ""
	} else if lastErr != nil {
		updates["webhook_last_error"] = webhooksvc.SanitizeDeliveryError(lastErr)
		if attempts < webhookMaxAttempts() {
			lockUntil := time.Now().Add(webhookRetryBackoff(attempts))
			updates["webhook_locked_until"] = &lockUntil
		}
	}

	return r.DB().WithContext(ctx).
		Model(&models.Transaction{}).
		Where("unique_hash = ?", uniqueHash).
		Updates(updates).Error
}

func (r *TransactionRepo) MarkFinality(ctx context.Context, uniqueHash string, confirmations, required uint, finalized bool) (*models.Transaction, error) {
	if r == nil || r.DB() == nil {
		return nil, gorm.ErrInvalidDB
	}
	var out models.Transaction
	err := r.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current models.Transaction
		if err := tx.WithContext(ctx).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&current, "unique_hash = ?", uniqueHash).Error; err != nil {
			return err
		}
		if current.Status == models.TransactionStatusReorged || current.Status == models.TransactionStatusFailed {
			out = current
			return nil
		}
		if !finalized && current.FinalizedAt != nil {
			out = current
			return nil
		}

		now := time.Now()
		updates := map[string]interface{}{
			"confirmations":          confirmations,
			"confirmations_required": required,
			"updated_at":             now,
		}
		if finalized {
			updates["status"] = models.TransactionStatusConfirmed
			if current.FinalizedAt == nil {
				updates["finalized_at"] = &now
			}
			if transactionIsCanonicalReappearance(current) {
				updates["event_type"] = constants.WebhookEventTransactionRestored
			}
		} else {
			updates["status"] = models.TransactionStatusPendingConfirmation
		}
		if err := tx.WithContext(ctx).
			Model(&models.Transaction{}).
			Where("id = ?", current.ID).
			Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.WithContext(ctx).First(&out, "id = ?", current.ID).Error; err != nil {
			return err
		}
		if finalized && current.FinalizedAt == nil && transactionIsCanonicalReappearance(out) {
			if _, err := NewLedgerRepo(tx).PostTransactionRestorationWithDB(ctx, tx, out); err != nil {
				return err
			}
			if err := recordTransactionRestoredEvent(ctx, tx, out, now); err != nil {
				return err
			}
		} else if finalized && current.FinalizedAt == nil {
			if err := recordTransactionDetectedEvent(ctx, tx, out); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func recordTransactionDetectedEvent(ctx context.Context, tx *gorm.DB, txModel models.Transaction) error {
	if txModel.ID == uuid.Nil || txModel.MerchantID == nil || txModel.DomainID == nil || strings.TrimSpace(txModel.EventType) == "" {
		return nil
	}
	sourceEventType := strings.TrimSpace(txModel.EventType)
	eventSnapshot := txModel
	if _, _, cataloged := webhooksvc.MoneyEventCatalogEntryForEmittedEvent(sourceEventType); !cataloged {
		// Historical/admin rows may carry application-specific event labels. A
		// catalog miss must not roll back finality forever: emit the canonical
		// transaction event while retaining the original label as provenance.
		eventSnapshot.EventType = constants.WebhookEventTransactionDetected
	}
	eventID := webhooksvc.TransactionEventID(eventSnapshot)
	payload, err := webhooksvc.TransactionPayloadJSON(eventSnapshot, webhooksvc.DeliveryMetadata{})
	if err != nil {
		return err
	}
	if eventSnapshot.EventType != sourceEventType {
		var envelope map[string]any
		if err := json.Unmarshal(payload, &envelope); err != nil {
			return err
		}
		envelope["source_event_type"] = sourceEventType
		payload, err = json.Marshal(envelope)
		if err != nil {
			return err
		}
	}
	event, err := BuildMoneyEventOutbox(MoneyEventOutboxBuildParams{
		EventName:      eventSnapshot.EventType,
		EventID:        eventID,
		AggregateType:  "transaction",
		AggregateID:    txModel.ID.String(),
		MerchantID:     *txModel.MerchantID,
		DomainID:       *txModel.DomainID,
		IdempotencyKey: eventID,
		Payload:        payload,
	})
	if err != nil {
		return err
	}
	_, _, err = NewMoneyEventOutboxRepo(tx).RecordWithDB(ctx, tx, &event)
	return err
}

func (r *TransactionRepo) MarkFailed(ctx context.Context, uniqueHash string) (*models.Transaction, error) {
	if err := r.DB().WithContext(ctx).
		Model(&models.Transaction{}).
		Where("unique_hash = ?", uniqueHash).
		Where("status <> ?", models.TransactionStatusReorged).
		Where("finalized_at IS NULL").
		Updates(map[string]interface{}{
			"status":     models.TransactionStatusFailed,
			"updated_at": time.Now(),
		}).Error; err != nil {
		return nil, err
	}
	return r.FindByUniqueHash(ctx, uniqueHash)
}

func (r *TransactionRepo) ListPendingFinality(ctx context.Context, limit int) ([]models.Transaction, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	var rows []models.Transaction
	err := r.DB().WithContext(ctx).
		Where("wallet_id IS NOT NULL").
		Where("(status = ? OR (status = ? AND finalized_at IS NULL))", models.TransactionStatusPendingConfirmation, models.TransactionStatusConfirmed).
		Order("created_at ASC").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}

func (r *TransactionRepo) ListPendingWebhooks(ctx context.Context, limit int) ([]models.Transaction, error) {
	if limit <= 0 {
		limit = 100
	}

	var transactions []models.Transaction
	now := time.Now()
	lockUntil := now.Add(2 * time.Minute)
	err := r.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("wallet_id IS NOT NULL").
			Joins("JOIN domains ON domains.id = transactions.domain_id").
			Scopes(whereConfiguredDomainNotificationTarget).
			Where("status IN ?", []string{models.TransactionStatusConfirmed, models.TransactionStatusReorged}).
			Where("webhook_sent_at IS NULL").
			Where("webhook_attempts < ?", webhookMaxAttempts()).
			Where("webhook_locked_until IS NULL OR webhook_locked_until < ?", now).
			Order("transactions.created_at ASC").
			Limit(limit).
			Find(&transactions).Error; err != nil {
			return err
		}
		if len(transactions) == 0 {
			return nil
		}
		ids := make([]uuid.UUID, 0, len(transactions))
		for _, row := range transactions {
			ids = append(ids, row.ID)
		}
		return tx.Model(&models.Transaction{}).
			Where("id IN ?", ids).
			Update("webhook_locked_until", &lockUntil).Error
	})
	return transactions, err
}

func (r *TransactionRepo) ListByMerchant(ctx context.Context, merchantID uuid.UUID, limit int) ([]models.Transaction, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	var transactions []models.Transaction
	err := r.DB().WithContext(ctx).
		Where("merchant_id = ?", merchantID).
		Order("created_at DESC").
		Limit(limit).
		Find(&transactions).Error
	return transactions, err
}

func (r *TransactionRepo) ListByMerchantPage(ctx context.Context, merchantID uuid.UUID, page, limit int) ([]models.Transaction, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 20
	}
	q := r.DB().WithContext(ctx).Model(&models.Transaction{}).Where("merchant_id = ?", merchantID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var transactions []models.Transaction
	err := q.Order("created_at DESC").
		Limit(limit).Offset((page - 1) * limit).
		Find(&transactions).Error
	return transactions, total, err
}

func (r *TransactionRepo) List(ctx context.Context, limit int) ([]models.Transaction, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var transactions []models.Transaction
	err := r.DB().WithContext(ctx).
		Order("created_at DESC").
		Limit(limit).
		Find(&transactions).Error
	return transactions, err
}

func (r *TransactionRepo) ListPage(ctx context.Context, page, limit int) ([]models.Transaction, int64, error) {
	return r.ListPageFiltered(ctx, page, limit, "", "", "")
}

// ListPageFiltered lists transactions with optional filters: from address, to address, tx hash.
// Empty string = no filter for that field.
func (r *TransactionRepo) ListPageFiltered(ctx context.Context, page, limit int, from, to, hash string) ([]models.Transaction, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	q := r.DB().WithContext(ctx).Model(&models.Transaction{})
	if from != "" {
		q = q.Where("LOWER(from_address) = LOWER(?)", from)
	}
	if to != "" {
		q = q.Where("LOWER(to_address) = LOWER(?)", to)
	}
	if hash != "" {
		q = q.Where("LOWER(hash) = LOWER(?)", hash)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []models.Transaction
	err := q.Order("created_at DESC").
		Limit(limit).Offset((page - 1) * limit).
		Find(&rows).Error
	return rows, total, err
}

type WalletBalanceRow struct {
	WalletID  uuid.UUID
	ChainID   int64
	Symbol    string
	Decimals  uint8
	Deposited string
	TxCount   int64
}

type WalletLockedRow struct {
	WalletID uuid.UUID
	Locked   string
}

func (r *TransactionRepo) AllWalletDeposits(ctx context.Context) ([]WalletBalanceRow, error) {
	var rows []WalletBalanceRow
	err := r.DB().WithContext(ctx).Raw(`
		SELECT wallet_id,
		       chain_id,
		       symbol,
		       decimals,
		       SUM(amount::numeric)::text AS deposited,
		       COUNT(*) AS tx_count
		FROM transactions
		WHERE wallet_id IS NOT NULL
		  AND status = 'confirmed'
		  AND amount ~ '^[0-9]+$'
		  AND amount::numeric > 0
		GROUP BY wallet_id, chain_id, symbol, decimals
		ORDER BY wallet_id, chain_id, symbol
	`).Scan(&rows).Error
	return rows, err
}

func (r *TransactionRepo) MerchantDepositSummary(ctx context.Context, merchantID uuid.UUID) ([]models.DepositSummary, error) {
	var summaries []models.DepositSummary
	err := r.DB().WithContext(ctx).Raw(`
		SELECT
			chain_id,
			token,
			symbol,
			decimals,
			SUM(amount::numeric)::text AS amount_raw,
			COUNT(*) AS transaction_count,
			COUNT(DISTINCT user_id) AS user_count,
			MIN(created_at) AS first_deposit_at,
			MAX(created_at) AS last_deposit_at
		FROM transactions
		WHERE merchant_id = ?
			AND wallet_id IS NOT NULL
			AND status = ?
			AND amount ~ '^[0-9]+$'
			AND amount::numeric > 0
		GROUP BY chain_id, token, symbol, decimals
		ORDER BY chain_id ASC, symbol ASC
	`, merchantID, "confirmed").Scan(&summaries).Error
	return summaries, err
}

func (r *TransactionRepo) DomainDepositSummary(params types.DepositSummaryParams) ([]models.DepositSummary, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}

	selectParts := []string{
		"domain_id",
		"chain_id",
		"token",
		"symbol",
		"decimals",
		"SUM(amount::numeric)::text AS amount_raw",
		"COUNT(*) AS transaction_count",
		"COUNT(DISTINCT user_id) AS user_count",
		"MIN(created_at) AS first_deposit_at",
		"MAX(created_at) AS last_deposit_at",
	}
	groupParts := []string{"domain_id", "chain_id", "token", "symbol", "decimals"}
	orderParts := []string{"chain_id ASC", "symbol ASC"}

	if params.ShouldGroupByUser() {
		selectParts = append([]string{"product_id", "user_id"}, selectParts...)
		groupParts = append([]string{"product_id", "user_id"}, groupParts...)
		orderParts = append(orderParts, "product_id ASC", "user_id ASC")
	}

	whereParts := []string{
		"domain_id = ?",
		"wallet_id IS NOT NULL",
		"status = ?",
		"amount ~ '^[0-9]+$'",
		"amount::numeric > 0",
	}
	args := []interface{}{*params.DomainID, "confirmed"}

	if params.MerchantID != nil {
		whereParts = append(whereParts, "merchant_id = ?")
		args = append(args, *params.MerchantID)
	}
	if params.ProductID != nil {
		whereParts = append(whereParts, "product_id = ?")
		args = append(args, *params.ProductID)
	}
	if params.UserID != nil {
		whereParts = append(whereParts, "user_id = ?")
		args = append(args, *params.UserID)
	}
	if params.ChainID != nil {
		whereParts = append(whereParts, "chain_id = ?")
		args = append(args, *params.ChainID)
	}
	if params.Symbol != nil {
		whereParts = append(whereParts, "LOWER(symbol) = LOWER(?)")
		args = append(args, *params.Symbol)
	}

	query := fmt.Sprintf(
		"SELECT %s FROM transactions WHERE %s GROUP BY %s ORDER BY %s",
		strings.Join(selectParts, ", "),
		strings.Join(whereParts, " AND "),
		strings.Join(groupParts, ", "),
		strings.Join(orderParts, ", "),
	)

	var summaries []models.DepositSummary
	if err := r.DB().WithContext(params.Context).Raw(query, args...).Scan(&summaries).Error; err != nil {
		return nil, err
	}

	return summaries, nil
}
