package repositories

import (
	"context"
	"core/constants"
	"core/models"
	webhooksvc "core/services/webhook"
	"core/types"
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
		existing, found, err := r.findExistingForCreateWithDB(ctx, tx, uniqueHash)
		if err != nil {
			return err
		}
		if found {
			if existing.Status == models.TransactionStatusReorged {
				if transactionBlockIdentityChanged(existing, *params.Block, blockHash) {
					fromBlock := parseBlockNumber(existing.BlockNumber)
					reason := transactionReorgReason("tx_reappeared", params.ChainID, existing.BlockNumber)
					_, _, err := NewReconciliationRepo(tx).CreateScopedOpenIfMissing(ctx, ReconciliationScope{
						ChainID:             params.ChainID,
						FromBlock:           fromBlock,
						ToBlock:             fromBlock,
						Reason:              reason,
						ScopeKey:            fmt.Sprintf("tx_reappeared:%s:%s", existing.UniqueHash, reason),
						ResourceType:        "transaction_reappeared",
						ResourceID:          existing.UniqueHash,
						AffectedResourceIDs: []string{existing.UniqueHash},
						Evidence: map[string]any{
							"original_block_number": existing.BlockNumber,
							"original_block_hash":   existing.BlockHash,
							"new_block_number":      *params.Block,
							"new_block_hash":        blockHash,
						},
					})
					return err
				}
				return nil
			}
			identityChanged := transactionBlockIdentityChanged(existing, *params.Block, blockHash)
			if existing.Status == models.TransactionStatusFailed {
				return nil
			}
			if existing.FinalizedAt != nil && identityChanged {
				fromBlock := parseBlockNumber(existing.BlockNumber)
				reason := transactionReorgReason("tx_block_identity_changed", params.ChainID, existing.BlockNumber)
				return r.markTransactionsReorgedWithDB(ctx, tx, []models.Transaction{existing}, now, params.ChainID, fromBlock, fromBlock, reason)
			}
			if existing.FinalizedAt != nil {
				return nil
			}
		}
		if err := r.observeCanonicalBlockWithDB(ctx, tx, params.ChainID, *params.Block, blockHash, parentHash, uniqueHash, now); err != nil {
			return err
		}
		if err := r.markBlockHashConflictsWithDB(ctx, tx, params.ChainID, *params.Block, blockHash, uniqueHash, now); err != nil {
			return err
		}
		identityChanged := found && transactionBlockIdentityChanged(existing, *params.Block, blockHash)
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
		if found && !identityChanged {
			delete(assignments, "status")
			delete(assignments, "confirmations")
		}
		if identityChanged {
			assignments["confirmations_required"] = uint(1)
			assignments["finalized_at"] = nil
			assignments["reorged_at"] = nil
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
	return existingHash != "" && nextHash != "" && !strings.EqualFold(existingHash, nextHash)
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

	if parentHash != "" && blockNumber > 1 {
		if err := r.markParentHashConflictsWithDB(ctx, tx, chainID, blockNumber, parentHash, currentUniqueHash, now); err != nil {
			return err
		}
	}

	reason := transactionReorgReason("reorg_detected", chainID, strconv.FormatInt(blockNumber, 10))
	if err := markCanonicalBlockHeightConflictsWithDB(ctx, tx, chainID, blockNumber, blockHash, reason, now); err != nil {
		return err
	}

	return upsertCanonicalBlockWithDB(ctx, tx, chainID, blockNumber, blockHash, parentHash, now)
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
		if err := tx.WithContext(ctx).
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
			}).Error; err != nil {
			return err
		}
	}
	if len(uniqueHashes) == 0 {
		return nil
	}

	if err := tx.WithContext(ctx).
		Model(&models.SweepJob{}).
		Where("transaction_unique_hash IN ?", uniqueHashes).
		Where("status IN ?", []string{models.SweepJobStatusPending, models.SweepJobStatusProcessing, models.SweepJobStatusFailed}).
		Updates(map[string]any{
			"status":       models.SweepJobStatusDeadLetter,
			"last_error":   "source transaction was reorged",
			"next_run_at":  nil,
			"locked_until": nil,
			"updated_at":   now,
		}).Error; err != nil {
		return err
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
	updates := map[string]interface{}{
		"confirmations":          confirmations,
		"confirmations_required": required,
		"updated_at":             time.Now(),
	}
	if finalized {
		now := time.Now()
		updates["status"] = models.TransactionStatusConfirmed
		updates["finalized_at"] = &now
	} else {
		updates["status"] = models.TransactionStatusPendingConfirmation
	}
	query := r.DB().WithContext(ctx).
		Model(&models.Transaction{}).
		Where("unique_hash = ?", uniqueHash).
		Where("status NOT IN ?", []string{models.TransactionStatusReorged, models.TransactionStatusFailed})
	if !finalized {
		query = query.Where("finalized_at IS NULL")
	}
	if err := query.Updates(updates).Error; err != nil {
		return nil, err
	}
	return r.FindByUniqueHash(ctx, uniqueHash)
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
