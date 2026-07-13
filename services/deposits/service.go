package deposits

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"core/asset"
	"core/blockchain"
	"core/constants"
	"core/models"
	"core/repositories"
	"core/types"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type ConfirmationRequirementFunc func(constants.ChainID) uint

type Dependencies struct {
	AssetRegistry         *asset.Registry
	ChainFactRepo         *repositories.ChainFactRepo
	ChainStateRepo        *repositories.ChainStateRepo
	DepositRepo           *repositories.DepositRepo
	WalletRepo            *repositories.WalletRepo
	TransactionRepo       *repositories.TransactionRepo
	PaymentRepo           *repositories.PaymentRepo
	LedgerRepo            *repositories.LedgerRepo
	SweepJobRepo          *repositories.SweepJobRepo
	MoneyEventInboxRepo   *repositories.MoneyEventInboxRepo
	SweepLifecycleEnqueue func(context.Context, models.SweepJob, *models.Transaction, string, string)
}

type Service struct {
	deps                    Dependencies
	confirmationRequirement ConfirmationRequirementFunc
}

type ProcessSummary struct {
	FactsProcessed       int
	DepositsCreated      int
	Unmatched            int
	Matched              int
	Finalized            int
	TransactionsRecorded int
	PaymentsSettled      int
}

func New(deps Dependencies, confirmationRequirement ConfirmationRequirementFunc) *Service {
	return &Service{
		deps:                    deps,
		confirmationRequirement: confirmationRequirement,
	}
}

func (s *Service) ProcessBatch(ctx context.Context, limit int) (ProcessSummary, error) {
	if err := s.validate(); err != nil {
		return ProcessSummary{}, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var summary ProcessSummary

	facts, err := s.deps.ChainFactRepo.ListForDepositProcessing(ctx, limit)
	if err != nil {
		return summary, err
	}
	for _, fact := range facts {
		row, err := s.processFactWithInbox(ctx, fact)
		if err != nil {
			return summary, err
		}
		summary.add(row)
	}

	pending, err := s.deps.DepositRepo.ListPendingFinality(ctx, limit)
	if err != nil {
		return summary, err
	}
	for _, deposit := range pending {
		row, err := s.ProcessPendingDeposit(ctx, deposit)
		if err != nil {
			return summary, err
		}
		summary.add(row)
	}
	return summary, nil
}

func (s *Service) processFactWithInbox(ctx context.Context, fact models.ChainFact) (ProcessSummary, error) {
	if s.deps.MoneyEventInboxRepo == nil {
		return s.ProcessFact(ctx, fact)
	}
	var summary ProcessSummary
	_, processed, err := s.deps.MoneyEventInboxRepo.ProcessWithDB(ctx, repositories.MoneyEventInboxConsumeParams{
		EventID:          fact.EventID,
		ConsumerName:     "deposit_fact_processor",
		IdempotencyScope: "deposit_fact_processor:" + fact.EventID,
		EventType:        strings.TrimSpace(fact.SourceEventType),
		ResourceType:     "chain_fact",
		ResourceID:       fact.ID.String(),
		LockFor:          2 * time.Minute,
		Evidence: map[string]any{
			"chain_id":         fact.ChainID,
			"tx_hash":          fact.TxHash,
			"log_index":        fact.LogIndex,
			"observed_address": fact.ObservedAddress,
		},
	}, func(tx *gorm.DB) error {
		row, err := s.withDB(tx).ProcessFact(ctx, fact)
		summary = row
		return err
	})
	if errors.Is(err, repositories.ErrMoneyEventInboxLocked) {
		return ProcessSummary{}, nil
	}
	if err != nil {
		return summary, err
	}
	if !processed {
		return ProcessSummary{}, nil
	}
	return summary, nil
}

func (s *Service) withDB(db *gorm.DB) *Service {
	deps := s.deps
	deps.ChainFactRepo = repositories.NewChainFactRepo(db)
	deps.ChainStateRepo = repositories.NewChainStateRepo(db)
	deps.DepositRepo = repositories.NewDepositRepo(db)
	deps.TransactionRepo = repositories.NewTransactionRepo(db)
	if s.deps.PaymentRepo != nil {
		deps.PaymentRepo = repositories.NewPaymentRepo(db)
	}
	if s.deps.LedgerRepo != nil {
		deps.LedgerRepo = repositories.NewLedgerRepo(db)
	}
	if s.deps.SweepJobRepo != nil {
		deps.SweepJobRepo = repositories.NewSweepJobRepo(db)
	}
	if s.deps.MoneyEventInboxRepo != nil {
		deps.MoneyEventInboxRepo = repositories.NewMoneyEventInboxRepo(db)
	}
	if s.deps.WalletRepo != nil {
		var factory *blockchain.ChainFactory
		if s.deps.WalletRepo.Domain() != nil && s.deps.WalletRepo.Domain().MerchantRepo() != nil {
			factory = s.deps.WalletRepo.Domain().MerchantRepo().Blockchains()
		}
		merchantRepo := repositories.NewMerchantRepo(db, factory)
		deps.WalletRepo = repositories.NewWalletRepo(repositories.NewDomainRepo(merchantRepo))
	}
	return New(deps, s.confirmationRequirement)
}

func (s *Service) ProcessFact(ctx context.Context, fact models.ChainFact) (ProcessSummary, error) {
	var summary ProcessSummary
	summary.FactsProcessed = 1
	if chainFactCorrected(fact) {
		return summary, nil
	}
	if chainFactFailed(fact) {
		if s.deps.ChainFactRepo == nil {
			return summary, nil
		}
		if err := s.deps.ChainFactRepo.MarkIgnored(ctx, fact.EventID, "chain transaction failed"); err != nil {
			return summary, err
		}
		return summary, nil
	}
	assetSupported, err := s.chainFactAssetSupported(fact)
	if err != nil {
		return summary, err
	}
	if !assetSupported {
		if s.deps.ChainFactRepo != nil {
			if err := s.deps.ChainFactRepo.MarkIgnored(ctx, fact.EventID, "chain fact asset is not supported"); err != nil {
				return summary, err
			}
		}
		return summary, nil
	}

	fact, err = s.factWithFinality(ctx, fact)
	if err != nil {
		return summary, err
	}
	wallet, err := s.matchDepositWallet(ctx, fact)
	if err != nil {
		return summary, err
	}
	if wallet == nil {
		summary.Unmatched = 1
		if err := s.deps.ChainFactRepo.MarkIgnored(ctx, fact.EventID, chainFactIgnoredReason(fact)); err != nil {
			return summary, err
		}
		return summary, nil
	}
	internalTransfer, err := s.sameMerchantInternalTransfer(ctx, fact, wallet)
	if err != nil {
		return summary, err
	}
	if internalTransfer {
		if err := s.deps.ChainFactRepo.MarkIgnored(ctx, fact.EventID, "same-merchant custody transfer"); err != nil {
			return summary, err
		}
		return summary, nil
	}
	deposit, created, err := s.deps.DepositRepo.ConsumeChainFact(ctx, fact, wallet)
	if err != nil {
		return summary, err
	}
	if created {
		summary.DepositsCreated = 1
	}
	summary.Matched = 1
	settlement, err := s.ensureDepositTransaction(ctx, fact, deposit, wallet)
	if err != nil {
		return summary, err
	}
	summary.add(settlement)
	return summary, nil
}

func (s *Service) ProcessPendingDeposit(ctx context.Context, deposit models.Deposit) (ProcessSummary, error) {
	var summary ProcessSummary
	if deposit.WalletID == nil {
		return summary, nil
	}
	fact, err := s.deps.ChainFactRepo.FindByEventID(ctx, deposit.ChainFactEventID)
	if err != nil {
		return summary, err
	}
	if chainFactCorrected(*fact) {
		return summary, nil
	}
	if chainFactFailed(*fact) {
		return summary, nil
	}
	assetSupported, err := s.chainFactAssetSupported(*fact)
	if err != nil {
		return summary, err
	}
	if !assetSupported {
		if err := s.deps.ChainFactRepo.MarkIgnored(ctx, fact.EventID, "chain fact asset is not supported"); err != nil {
			return summary, err
		}
		return summary, nil
	}
	wallet, err := s.deps.WalletRepo.FindByID(ctx, *deposit.WalletID)
	if err != nil {
		return summary, err
	}
	internalTransfer, err := s.sameMerchantInternalTransfer(ctx, *fact, wallet)
	if err != nil {
		return summary, err
	}
	if internalTransfer {
		if err := s.deps.ChainFactRepo.MarkIgnored(ctx, fact.EventID, "same-merchant custody transfer"); err != nil {
			return summary, err
		}
		return summary, nil
	}
	updatedFact, err := s.factWithFinality(ctx, *fact)
	if err != nil {
		return summary, err
	}
	updatedDeposit, _, err := s.deps.DepositRepo.ConsumeChainFact(ctx, updatedFact, wallet)
	if err != nil {
		return summary, err
	}
	return s.ensureDepositTransaction(ctx, updatedFact, updatedDeposit, wallet)
}

func (s *Service) validate() error {
	if s == nil ||
		s.deps.AssetRegistry == nil ||
		s.deps.ChainFactRepo == nil ||
		s.deps.ChainStateRepo == nil ||
		s.deps.DepositRepo == nil ||
		s.deps.WalletRepo == nil ||
		s.deps.TransactionRepo == nil {
		return errors.New("deposit service dependencies are not configured")
	}
	return nil
}

func (s *Service) chainFactAssetSupported(fact models.ChainFact) (bool, error) {
	if s == nil || s.deps.AssetRegistry == nil {
		return false, errors.New("deposit asset registry is not configured")
	}
	token := ""
	if fact.Token != nil {
		token = strings.TrimSpace(*fact.Token)
	}
	if token == "" {
		if chainFactEventRequiresToken(fact.SourceEventType) {
			return false, nil
		}
		_, ok := s.deps.AssetRegistry.GetNative(fact.ChainID)
		return ok, nil
	}
	_, ok := s.deps.AssetRegistry.Get(fact.ChainID, token)
	return ok, nil
}

func (s *Service) sameMerchantInternalTransfer(ctx context.Context, fact models.ChainFact, destination *models.Wallet) (bool, error) {
	if s == nil || s.deps.WalletRepo == nil || destination == nil || destination.MerchantID == uuid.Nil {
		return false, nil
	}
	for _, address := range chainFactSourceAddresses(fact) {
		source, err := s.deps.WalletRepo.FindByChainAddress(ctx, fact.ChainID, address)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return false, err
		}
		if source != nil && source.MerchantID == destination.MerchantID {
			return true, nil
		}
	}
	return false, nil
}

func chainFactEventRequiresToken(eventType string) bool {
	eventType = strings.ToLower(strings.TrimSpace(eventType))
	return strings.Contains(eventType, "token") || strings.HasPrefix(eventType, "spl_") || strings.HasPrefix(eventType, "erc20_") || strings.HasPrefix(eventType, "trc20_")
}

func (s *Service) matchDepositWallet(ctx context.Context, fact models.ChainFact) (*models.Wallet, error) {
	if fact.Direction != models.ChainFactDirectionTo || !positiveAmount(fact.AmountRaw) {
		return nil, nil
	}
	wallet, err := s.deps.WalletRepo.FindByChainAddress(ctx, fact.ChainID, fact.ObservedAddress)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return wallet, err
}

func (s *Service) ensureDepositTransaction(ctx context.Context, fact models.ChainFact, deposit *models.Deposit, wallet *models.Wallet) (ProcessSummary, error) {
	var summary ProcessSummary
	if deposit == nil || wallet == nil || deposit.WalletID == nil {
		return summary, nil
	}
	txParam := transactionParamFromChainFact(ctx, fact)
	uniqueHash, err := s.deps.TransactionRepo.UniqueHash(txParam)
	if err != nil {
		return summary, err
	}
	if err := s.deps.TransactionRepo.Create(txParam); err != nil {
		return summary, err
	}
	txModel, err := s.deps.TransactionRepo.BindWallet(ctx, uniqueHash, fact.SourceEventType, wallet)
	if err != nil {
		return summary, err
	}
	summary.TransactionsRecorded = 1
	if s.deps.LedgerRepo != nil {
		if err := s.deps.LedgerRepo.CreateDepositPending(ctx, *txModel); err != nil {
			return summary, err
		}
	}
	if err := s.markWalletAddressLifecycle(ctx, fact, models.WalletAddressStatusActive); err != nil {
		return summary, err
	}
	if !fact.Finalized {
		_, err := s.deps.TransactionRepo.MarkFinality(ctx, uniqueHash, fact.Confirmations, fact.ConfirmationsRequired, false)
		return summary, err
	}

	finalizedTx, err := s.deps.TransactionRepo.MarkFinality(ctx, uniqueHash, fact.Confirmations, fact.ConfirmationsRequired, true)
	if err != nil {
		return summary, err
	}
	summary.Finalized = 1
	if err := s.enqueueFinalizedSweepJob(ctx, finalizedTx, wallet); err != nil {
		return summary, err
	}
	settled, err := s.settleFinalizedTransaction(ctx, finalizedTx, deposit)
	if err != nil {
		return summary, err
	}
	if err := s.markWalletAddressLifecycle(ctx, fact, models.WalletAddressStatusUsed); err != nil {
		return summary, err
	}
	summary.add(settled)
	return summary, nil
}

func (s *Service) markWalletAddressLifecycle(ctx context.Context, fact models.ChainFact, status string) error {
	if s.deps.WalletRepo == nil || fact.ObservedAddress == "" {
		return nil
	}
	return repositories.NewWalletAddressRepo(s.deps.WalletRepo.DB()).
		TransitionWalletLifecycle(ctx, fact.ChainID, fact.ObservedAddress, status, time.Now().UTC())
}

func (s *Service) enqueueFinalizedSweepJob(ctx context.Context, txModel *models.Transaction, wallet *models.Wallet) error {
	if txModel == nil || wallet == nil || wallet.HDAddressId == 0 || s.deps.SweepJobRepo == nil {
		return nil
	}
	job, created, err := s.deps.SweepJobRepo.EnqueueForTransaction(ctx, *txModel)
	if err != nil {
		return err
	}
	if created && job != nil && s.deps.SweepLifecycleEnqueue != nil {
		s.deps.SweepLifecycleEnqueue(ctx, *job, txModel, constants.WebhookEventSweepRequestedV1, "")
	}
	return nil
}

func (s *Service) settleFinalizedTransaction(ctx context.Context, txModel *models.Transaction, deposit *models.Deposit) (ProcessSummary, error) {
	var summary ProcessSummary
	if txModel == nil || s.deps.PaymentRepo == nil {
		return summary, nil
	}
	matchResult, err := s.deps.PaymentRepo.MatchFinalizedDeposit(ctx, *txModel, deposit)
	if err != nil {
		return summary, err
	}
	if matchResult != nil && matchResult.Session != nil {
		if matchResult.Changed && matchResult.Status == models.PaymentStatusPaid {
			summary.PaymentsSettled = 1
		}
		if matchResult.LedgerEligible {
			if err := s.postFinalizedDepositAvailable(ctx, *txModel, matchResult.Session); err != nil {
				return summary, err
			}
		}
		return summary, nil
	}
	if err := s.postFinalizedDepositAvailable(ctx, *txModel, nil); err != nil {
		return summary, err
	}
	return summary, nil
}

func (s *Service) postFinalizedDepositAvailable(ctx context.Context, txModel models.Transaction, session *models.PaymentSession) error {
	if s.deps.LedgerRepo == nil || txModel.WalletID == nil {
		return nil
	}
	if session != nil {
		return s.deps.LedgerRepo.PostDepositAvailable(ctx, *session, txModel)
	}
	if s.deps.PaymentRepo != nil {
		matchedSession, err := s.deps.PaymentRepo.FindByTxUniqueHash(ctx, txModel.UniqueHash)
		if err == nil && matchedSession != nil {
			return s.deps.LedgerRepo.PostDepositAvailable(ctx, *matchedSession, txModel)
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	return s.deps.LedgerRepo.PostStandaloneDepositAvailable(ctx, txModel)
}

func (s *Service) factWithFinality(ctx context.Context, fact models.ChainFact) (models.ChainFact, error) {
	required := fact.ConfirmationsRequired
	if s.confirmationRequirement != nil {
		required = s.confirmationRequirement(fact.ChainID)
	}
	if required == 0 {
		required = 1
	}
	confirmations := fact.Confirmations
	state, err := s.deps.ChainStateRepo.Get(ctx, fact.ChainID)
	if err != nil {
		return fact, fmt.Errorf("load chain state for deposit finality: %w", err)
	}
	if state == nil {
		return fact, errors.New("load chain state for deposit finality: state is nil")
	}
	if calculated := confirmationsForBlock(fact.BlockNumber, state.LastProcessedBlock, state.LastConfirmedBlock); calculated > confirmations {
		confirmations = calculated
	}
	fact.ConfirmationsRequired = required
	fact.Confirmations = confirmations
	fact.Finalized = confirmations >= required
	return fact, nil
}

func confirmationsForBlock(blockNumber, lastProcessed, lastConfirmed int64) uint {
	if blockNumber <= 0 || lastProcessed < blockNumber {
		return 0
	}
	confirmedHead := lastConfirmed
	if confirmedHead < lastProcessed {
		confirmedHead = lastProcessed
	}
	if confirmedHead < blockNumber {
		return 0
	}
	return uint(confirmedHead - blockNumber + 1)
}

func chainFactCorrected(fact models.ChainFact) bool {
	status := strings.TrimSpace(fact.Status)
	return status == models.ChainFactStatusIgnored ||
		status == models.ChainFactStatusReorged ||
		status == models.ChainFactStatusSuperseded
}

func chainFactFailed(fact models.ChainFact) bool {
	return strings.EqualFold(strings.TrimSpace(chainFactMetadata(fact)["status"]), models.TransactionStatusFailed)
}

func chainFactIgnoredReason(fact models.ChainFact) string {
	if fact.Direction != models.ChainFactDirectionTo {
		return "chain fact is not inbound"
	}
	if !positiveAmount(fact.AmountRaw) {
		return "chain fact amount is not positive"
	}
	return "observed address is not owned by a wallet"
}

func transactionParamFromChainFact(ctx context.Context, fact models.ChainFact) types.TransactionParam {
	meta := chainFactMetadata(fact)
	fromAddresses := chainFactSourceAddresses(fact)
	from := firstNonEmpty(meta["from"], "unknown")
	to := firstNonEmpty(meta["to"], "unknown")
	if fact.Direction == models.ChainFactDirectionTo {
		to = firstNonEmpty(fact.ObservedAddress, to)
	} else if fact.Direction == models.ChainFactDirectionFrom {
		from = firstNonEmpty(fact.ObservedAddress, from)
	}
	block := fmt.Sprintf("%d", fact.BlockNumber)
	status := models.TransactionStatusPendingConfirmation
	if strings.EqualFold(strings.TrimSpace(meta["status"]), models.TransactionStatusFailed) {
		status = models.TransactionStatusFailed
	} else if fact.Finalized {
		status = models.TransactionStatusConfirmed
	}
	return types.TransactionParam{
		Context:       ctx,
		ChainID:       fact.ChainID,
		Hash:          stringPtr(fact.TxHash),
		Block:         &block,
		BlockHash:     stringPtr(fact.BlockHash),
		ParentHash:    stringPtr(firstNonEmpty(meta["parent_hash"], meta["parentHash"])),
		Token:         cloneOptional(fact.Token),
		Symbol:        stringPtr(fact.Symbol),
		Decimals:      fact.Decimals,
		From:          &from,
		FromAddresses: fromAddresses,
		To:            &to,
		Amount:        stringPtr(fact.AmountRaw),
		Memo:          stringPtr(firstNonEmpty(fact.Memo, meta["memo"], meta["tag"], meta["payment_id"], meta["paymentId"])),
		LogIndex:      stringPtr(fact.LogIndex),
		Status:        &status,
	}
}

func chainFactSourceAddresses(fact models.ChainFact) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, 2)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}

	var raw map[string]any
	if err := json.Unmarshal([]byte(fact.RawMetadataJSON), &raw); err != nil {
		return out
	}
	if value, ok := raw["from"].(string); ok {
		add(value)
	}
	if values, ok := raw["from_addresses"].([]any); ok {
		for _, value := range values {
			if address, ok := value.(string); ok {
				add(address)
			}
		}
	}
	return out
}

func chainFactMetadata(fact models.ChainFact) map[string]string {
	var raw map[string]any
	out := map[string]string{}
	if err := json.Unmarshal([]byte(fact.RawMetadataJSON), &raw); err != nil {
		return out
	}
	for _, key := range []string{"from", "to", "status", "parent_hash", "parentHash", "memo", "tag", "payment_id", "paymentId"} {
		if value, ok := raw[key].(string); ok {
			out[key] = strings.TrimSpace(value)
		}
	}
	return out
}

func positiveAmount(amount string) bool {
	value, ok := new(big.Int).SetString(strings.TrimSpace(amount), 10)
	return ok && value.Sign() > 0
}

func isStandaloneDepositWalletProduct(productID string) bool {
	productID = strings.TrimSpace(productID)
	return strings.HasPrefix(productID, "static:") || strings.HasPrefix(productID, "wallet:")
}

func (s *ProcessSummary) add(other ProcessSummary) {
	s.FactsProcessed += other.FactsProcessed
	s.DepositsCreated += other.DepositsCreated
	s.Unmatched += other.Unmatched
	s.Matched += other.Matched
	s.Finalized += other.Finalized
	s.TransactionsRecorded += other.TransactionsRecorded
	s.PaymentsSettled += other.PaymentsSettled
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func stringPtr(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func cloneOptional(value *string) *string {
	if value == nil {
		return nil
	}
	clone := strings.TrimSpace(*value)
	if clone == "" {
		return nil
	}
	return &clone
}
