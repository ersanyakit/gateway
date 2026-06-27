package deposits

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"core/constants"
	"core/models"
	"core/repositories"
	"core/types"

	"gorm.io/gorm"
)

type ConfirmationRequirementFunc func(constants.ChainID) uint

type Dependencies struct {
	ChainFactRepo   *repositories.ChainFactRepo
	ChainStateRepo  *repositories.ChainStateRepo
	DepositRepo     *repositories.DepositRepo
	WalletRepo      *repositories.WalletRepo
	TransactionRepo *repositories.TransactionRepo
	PaymentRepo     *repositories.PaymentRepo
	LedgerRepo      *repositories.LedgerRepo
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
		row, err := s.ProcessFact(ctx, fact)
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

func (s *Service) ProcessFact(ctx context.Context, fact models.ChainFact) (ProcessSummary, error) {
	var summary ProcessSummary
	summary.FactsProcessed = 1

	fact = s.factWithFinality(ctx, fact)
	wallet, err := s.matchDepositWallet(ctx, fact)
	if err != nil {
		return summary, err
	}
	deposit, created, err := s.deps.DepositRepo.ConsumeChainFact(ctx, fact, wallet)
	if err != nil {
		return summary, err
	}
	if created {
		summary.DepositsCreated = 1
	}
	if deposit.WalletID == nil {
		summary.Unmatched = 1
		return summary, nil
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
	wallet, err := s.deps.WalletRepo.FindByID(ctx, *deposit.WalletID)
	if err != nil {
		return summary, err
	}
	updatedFact := s.factWithFinality(ctx, *fact)
	updatedDeposit, _, err := s.deps.DepositRepo.ConsumeChainFact(ctx, updatedFact, wallet)
	if err != nil {
		return summary, err
	}
	return s.ensureDepositTransaction(ctx, updatedFact, updatedDeposit, wallet)
}

func (s *Service) validate() error {
	if s == nil ||
		s.deps.ChainFactRepo == nil ||
		s.deps.ChainStateRepo == nil ||
		s.deps.DepositRepo == nil ||
		s.deps.WalletRepo == nil ||
		s.deps.TransactionRepo == nil {
		return errors.New("deposit service dependencies are not configured")
	}
	return nil
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
	if !fact.Finalized {
		_, err := s.deps.TransactionRepo.MarkFinality(ctx, uniqueHash, fact.Confirmations, fact.ConfirmationsRequired, false)
		return summary, err
	}

	finalizedTx, err := s.deps.TransactionRepo.MarkFinality(ctx, uniqueHash, fact.Confirmations, fact.ConfirmationsRequired, true)
	if err != nil {
		return summary, err
	}
	summary.Finalized = 1
	settled, err := s.settleFinalizedTransaction(ctx, finalizedTx)
	if err != nil {
		return summary, err
	}
	summary.add(settled)
	return summary, nil
}

func (s *Service) settleFinalizedTransaction(ctx context.Context, txModel *models.Transaction) (ProcessSummary, error) {
	var summary ProcessSummary
	if txModel == nil || s.deps.PaymentRepo == nil {
		return summary, nil
	}
	session, changed, err := s.deps.PaymentRepo.MarkPaidByTransaction(ctx, *txModel)
	if err != nil {
		return summary, err
	}
	if changed && session != nil {
		summary.PaymentsSettled = 1
		if s.deps.LedgerRepo != nil {
			if err := s.deps.LedgerRepo.PostDepositAvailable(ctx, *session, *txModel); err != nil {
				return summary, err
			}
		}
		return summary, nil
	}
	if s.deps.LedgerRepo != nil && txModel.WalletID != nil {
		wallet, err := s.deps.WalletRepo.FindByID(ctx, *txModel.WalletID)
		if err == nil && isStandaloneDepositWalletProduct(wallet.ProductID) {
			return summary, s.deps.LedgerRepo.PostStandaloneDepositAvailable(ctx, *txModel)
		}
	}
	return summary, nil
}

func (s *Service) factWithFinality(ctx context.Context, fact models.ChainFact) models.ChainFact {
	required := fact.ConfirmationsRequired
	if s.confirmationRequirement != nil {
		required = s.confirmationRequirement(fact.ChainID)
	}
	if required == 0 {
		required = 1
	}
	confirmations := fact.Confirmations
	if state, err := s.deps.ChainStateRepo.Get(ctx, fact.ChainID); err == nil {
		if calculated := confirmationsForBlock(fact.BlockNumber, state.LastProcessedBlock, state.LastConfirmedBlock); calculated > confirmations {
			confirmations = calculated
		}
	}
	fact.ConfirmationsRequired = required
	fact.Confirmations = confirmations
	fact.Finalized = confirmations >= required
	return fact
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

func transactionParamFromChainFact(ctx context.Context, fact models.ChainFact) types.TransactionParam {
	meta := chainFactMetadata(fact)
	from := firstNonEmpty(meta["from"], "unknown")
	to := firstNonEmpty(meta["to"], "unknown")
	if fact.Direction == models.ChainFactDirectionTo {
		to = firstNonEmpty(fact.ObservedAddress, to)
	} else if fact.Direction == models.ChainFactDirectionFrom {
		from = firstNonEmpty(fact.ObservedAddress, from)
	}
	block := fmt.Sprintf("%d", fact.BlockNumber)
	status := models.TransactionStatusPendingConfirmation
	if fact.Finalized {
		status = models.TransactionStatusConfirmed
	}
	return types.TransactionParam{
		Context:   ctx,
		ChainID:   fact.ChainID,
		Hash:      stringPtr(fact.TxHash),
		Block:     &block,
		BlockHash: stringPtr(fact.BlockHash),
		Token:     cloneOptional(fact.Token),
		Symbol:    stringPtr(fact.Symbol),
		Decimals:  fact.Decimals,
		From:      &from,
		To:        &to,
		Amount:    stringPtr(fact.AmountRaw),
		LogIndex:  stringPtr(fact.LogIndex),
		Status:    &status,
	}
}

func chainFactMetadata(fact models.ChainFact) map[string]string {
	var raw map[string]any
	out := map[string]string{}
	if err := json.Unmarshal([]byte(fact.RawMetadataJSON), &raw); err != nil {
		return out
	}
	for _, key := range []string{"from", "to"} {
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
