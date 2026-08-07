package txrescan

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"core/asset"
	"core/blockchain"
	"core/blockchain/addrutil"
	"core/constants"
	"core/helpers"
	"core/models"
	"core/repositories"
	depositsvc "core/services/deposits"
	"core/types"
	"core/workers/dispatcher"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrUnsupportedChain           = errors.New("unsupported rescan chain")
	ErrTransactionNotFound        = errors.New("transaction not found on chain")
	ErrUnauthorizedTx             = errors.New("transaction does not belong to merchant")
	ErrHistoricalScannerRequired  = errors.New("historical range scanner is required")
	ErrIncompleteProviderResponse = errors.New("incomplete or inconsistent provider response")
)

const (
	erc20TransferTopic   = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
	solanaMemoProgram    = "MemoSq4gqABAXKb96qnH8TysNcWxMyWCqXgDLGmfcHr"
	solanaMemoProgramOld = "Memo1UhkJRfHyvLMcVucJwxXeuD728EqVDDwQDxFMNo"
	solanaSystemProgram  = "11111111111111111111111111111111"
	solanaTokenProgram   = "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
	solanaToken2022      = "TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb"
)

type Service struct {
	Chains              *blockchain.ChainFactory
	Registry            *asset.Registry
	Bus                 *dispatcher.Dispatcher
	ChainFactRepo       *repositories.ChainFactRepo
	ChainStateRepo      *repositories.ChainStateRepo
	DepositRepo         *repositories.DepositRepo
	TransactionRepo     *repositories.TransactionRepo
	WalletRepo          *repositories.WalletRepo
	PaymentRepo         *repositories.PaymentRepo
	LedgerRepo          *repositories.LedgerRepo
	MoneyEventInboxRepo *repositories.MoneyEventInboxRepo
	Confirmations       depositsvc.ConfirmationRequirementFunc
	client              *http.Client
}

type Result struct {
	ChainID              constants.ChainID `json:"chain_id"`
	Chain                string            `json:"chain"`
	Hash                 string            `json:"hash"`
	Events               int               `json:"events"`
	DepositsCreated      int               `json:"deposits_created"`
	DepositsMatched      int               `json:"deposits_matched"`
	DepositsUnmatched    int               `json:"deposits_unmatched"`
	DepositsFinalized    int               `json:"deposits_finalized"`
	TransactionsRecorded int               `json:"transactions_recorded"`
	PaymentsSettled      int               `json:"payments_settled"`
	UniqueHashes         []string          `json:"unique_hashes"`
}

type HistoricalRangeScanner interface {
	ScanBlock(context.Context, constants.ChainID, int64) ([]HistoricalEvent, error)
}

// HistoricalRangeAttestationProvider supplies independently checkable evidence
// that ScanBlock inspected the complete canonical block. TransactionIDs are the
// identities advertised by the block header/body; ScannedTransactionIDs are
// the identities for which the scanner completed event extraction. Both slices
// must be non-nil (an explicit empty slice is a legitimate empty block).
type HistoricalRangeAttestationProvider interface {
	AttestBlock(context.Context, constants.ChainID, int64) (HistoricalBlockAttestation, error)
}

type HistoricalBlockAttestation struct {
	ChainID               constants.ChainID
	BlockNumber           int64
	BlockHash             string
	ParentHash            string
	TransactionCount      int
	TransactionIDs        []string
	ScannedTransactionIDs []string
	Complete              bool
}

// HistoricalRangeCheckpointStore persists the last block that was processed in
// full. Implementations must make StoreHistoricalRangeCheckpoint durable before
// returning. A failed store deliberately causes the block to be replayed; all
// downstream writes in this service are idempotent.
type HistoricalRangeCheckpointStore interface {
	LoadHistoricalRangeCheckpoint(context.Context, string, constants.ChainID) (int64, error)
	StoreHistoricalRangeCheckpoint(context.Context, string, constants.ChainID, int64) error
}

type HistoricalRangeRequest struct {
	ChainID constants.ChainID
	From    int64
	To      int64
	Scanner HistoricalRangeScanner
	// CheckpointKey and CheckpointStore are optional, but must be supplied
	// together. The key should identify one logical replay job.
	CheckpointKey   string
	CheckpointStore HistoricalRangeCheckpointStore
}

type HistoricalEvent struct {
	Type                  string
	Tx                    types.TransactionParam
	Confirmations         uint
	ConfirmationsRequired uint
}

type HistoricalRangeResult struct {
	ChainID            constants.ChainID `json:"chain_id"`
	From               int64             `json:"from"`
	To                 int64             `json:"to"`
	Blocks             int               `json:"blocks"`
	Events             int               `json:"events"`
	UniqueHashes       []string          `json:"unique_hashes"`
	LastCompletedBlock int64             `json:"last_completed_block"`
	NextBlock          int64             `json:"next_block"`
}

type eventCandidate struct {
	Type                  string
	Tx                    *types.TransactionParam
	Confirmations         uint
	ConfirmationsRequired uint
}

func New(chains *blockchain.ChainFactory, registry *asset.Registry, bus *dispatcher.Dispatcher, txRepo *repositories.TransactionRepo, walletRepo *repositories.WalletRepo) *Service {
	return &Service{
		Chains:          chains,
		Registry:        registry,
		Bus:             bus,
		TransactionRepo: txRepo,
		WalletRepo:      walletRepo,
		client:          &http.Client{Timeout: 30 * time.Second},
	}
}

func (s *Service) Rescan(ctx context.Context, chainID constants.ChainID, hash string) (*Result, error) {
	return s.rescan(ctx, chainID, hash, nil)
}

func (s *Service) RescanForMerchant(ctx context.Context, chainID constants.ChainID, hash string, merchantID uuid.UUID) (*Result, error) {
	return s.rescan(ctx, chainID, hash, &merchantID)
}

func (s *Service) ReplayHistoricalRange(ctx context.Context, req HistoricalRangeRequest) (*HistoricalRangeResult, error) {
	if !constants.IsSupportedChainID(req.ChainID) {
		return nil, ErrUnsupportedChain
	}
	if req.From <= 0 {
		return nil, errors.New("historical range from block must be positive")
	}
	if req.To < req.From {
		return nil, errors.New("historical range to block must be greater than or equal to from block")
	}
	if req.Scanner == nil {
		return nil, ErrHistoricalScannerRequired
	}
	attester, ok := req.Scanner.(HistoricalRangeAttestationProvider)
	if !ok {
		return nil, fmt.Errorf("%w: historical scanner does not provide block coverage attestation", ErrIncompleteProviderResponse)
	}
	checkpointKey := strings.TrimSpace(req.CheckpointKey)
	if (checkpointKey == "") != (req.CheckpointStore == nil) {
		return nil, errors.New("historical range checkpoint key and store must be supplied together")
	}

	result := &HistoricalRangeResult{
		ChainID:            req.ChainID,
		From:               req.From,
		To:                 req.To,
		LastCompletedBlock: req.From - 1,
		NextBlock:          req.From,
	}
	startBlock := req.From
	if req.CheckpointStore != nil {
		completed, err := req.CheckpointStore.LoadHistoricalRangeCheckpoint(ctx, checkpointKey, req.ChainID)
		if err != nil {
			return result, fmt.Errorf("load historical range checkpoint: %w", err)
		}
		if completed >= req.From {
			result.LastCompletedBlock = completed
			startBlock = completed + 1
			result.NextBlock = startBlock
		}
	}
	if startBlock > req.To {
		return result, nil
	}

	for blockNumber := startBlock; blockNumber <= req.To; blockNumber++ {
		events, err := req.Scanner.ScanBlock(ctx, req.ChainID, blockNumber)
		if err != nil {
			return result, err
		}
		attestation, err := attester.AttestBlock(ctx, req.ChainID, blockNumber)
		if err != nil {
			return result, err
		}
		if err := validateHistoricalBlockEvents(req.ChainID, blockNumber, events, attestation); err != nil {
			return result, err
		}
		if err := s.observeHistoricalCanonicalBlock(ctx, attestation); err != nil {
			return result, err
		}
		for _, event := range events {
			// Carry the attested parent through TransactionRepo.Create so the
			// per-event canonical observation cannot erase continuity evidence.
			if event.Tx.ParentHash == nil || strings.TrimSpace(*event.Tx.ParentHash) == "" {
				parentHash := attestation.ParentHash
				event.Tx.ParentHash = &parentHash
			}
			candidate := eventCandidate{
				Type:                  event.Type,
				Tx:                    &event.Tx,
				Confirmations:         event.Confirmations,
				ConfirmationsRequired: event.ConfirmationsRequired,
			}
			s.ensureCandidateFinalityMetadata(&candidate)
			if candidate.Tx.Context == nil {
				candidate.Tx.Context = ctx
			}
			fact, err := s.recordRescanFact(ctx, candidate)
			if err != nil {
				return result, err
			}
			if fact == nil {
				continue
			}
			if s != nil && s.Bus != nil {
				if err := s.Bus.DispatchAndWait(ctx, dispatcher.Event{
					Chain:       req.ChainID,
					Type:        candidate.Type,
					Transaction: candidate.Tx,
				}); err != nil && !(errors.Is(err, dispatcher.ErrNoSubscribers) && s.ChainFactRepo != nil) {
					return result, err
				}
			}
			result.Events++
			if s != nil && s.TransactionRepo != nil {
				if unique, err := s.TransactionRepo.UniqueHash(*candidate.Tx); err == nil {
					result.UniqueHashes = append(result.UniqueHashes, unique)
				}
			}
			if fact != nil {
				if _, err := s.processDepositFact(ctx, *fact); err != nil {
					return result, err
				}
			}
			if err := s.applyTransactionFinality(ctx, candidate); err != nil {
				return result, err
			}
		}
		if req.CheckpointStore != nil {
			if err := req.CheckpointStore.StoreHistoricalRangeCheckpoint(ctx, checkpointKey, req.ChainID, blockNumber); err != nil {
				return result, fmt.Errorf("store historical range checkpoint at block %d: %w", blockNumber, err)
			}
		}
		result.Blocks++
		result.LastCompletedBlock = blockNumber
		result.NextBlock = blockNumber + 1
	}
	return result, nil
}

func validateHistoricalBlockEvents(chainID constants.ChainID, blockNumber int64, events []HistoricalEvent, attestation HistoricalBlockAttestation) error {
	if !attestation.Complete || attestation.ChainID != chainID || attestation.BlockNumber != blockNumber {
		return fmt.Errorf("%w: historical block %d has invalid coverage attestation", ErrIncompleteProviderResponse, blockNumber)
	}
	if strings.TrimSpace(attestation.BlockHash) == "" {
		return fmt.Errorf("%w: historical block %d attestation has no block hash", ErrIncompleteProviderResponse, blockNumber)
	}
	if blockNumber > 1 && strings.TrimSpace(attestation.ParentHash) == "" {
		return fmt.Errorf("%w: historical block %d attestation has no parent hash", ErrIncompleteProviderResponse, blockNumber)
	}
	if attestation.TransactionIDs == nil || attestation.ScannedTransactionIDs == nil || attestation.TransactionCount < 0 {
		return fmt.Errorf("%w: historical block %d has no explicit transaction coverage", ErrIncompleteProviderResponse, blockNumber)
	}
	advertised, err := historicalTransactionIDSet(chainID, blockNumber, "advertised", attestation.TransactionIDs)
	if err != nil {
		return err
	}
	scanned, err := historicalTransactionIDSet(chainID, blockNumber, "scanned", attestation.ScannedTransactionIDs)
	if err != nil {
		return err
	}
	if attestation.TransactionCount != len(attestation.TransactionIDs) || len(advertised) != len(scanned) {
		return fmt.Errorf("%w: historical block %d transaction coverage count mismatch", ErrIncompleteProviderResponse, blockNumber)
	}
	for transactionID := range advertised {
		if _, ok := scanned[transactionID]; !ok {
			return fmt.Errorf("%w: historical block %d did not scan transaction %s", ErrIncompleteProviderResponse, blockNumber, transactionID)
		}
	}

	seen := make(map[string]struct{}, len(events))
	for index := range events {
		tx := &events[index].Tx
		if tx.ChainID != chainID {
			return fmt.Errorf("%w: historical block %d event %d has chain %d, want %d", ErrIncompleteProviderResponse, blockNumber, index, tx.ChainID, chainID)
		}
		if tx.Block == nil || strings.TrimSpace(*tx.Block) == "" {
			return fmt.Errorf("%w: historical block %d event %d has no block number", ErrIncompleteProviderResponse, blockNumber, index)
		}
		eventBlock, err := strconv.ParseInt(strings.TrimSpace(*tx.Block), 10, 64)
		if err != nil || eventBlock != blockNumber {
			return fmt.Errorf("%w: historical block %d event %d claims block %q", ErrIncompleteProviderResponse, blockNumber, index, *tx.Block)
		}
		if tx.Hash == nil || strings.TrimSpace(*tx.Hash) == "" || tx.LogIndex == nil || strings.TrimSpace(*tx.LogIndex) == "" {
			return fmt.Errorf("%w: historical block %d event %d has no transaction identity", ErrIncompleteProviderResponse, blockNumber, index)
		}
		if tx.BlockHash == nil || !historicalIdentifierEqual(chainID, *tx.BlockHash, attestation.BlockHash) {
			return fmt.Errorf("%w: historical block %d event %d has block hash %q, want %q", ErrIncompleteProviderResponse, blockNumber, index, txRescanPtrString(tx.BlockHash), attestation.BlockHash)
		}
		transactionID := historicalIdentifier(chainID, *tx.Hash)
		if _, ok := scanned[transactionID]; !ok {
			return fmt.Errorf("%w: historical block %d event %d references unattested transaction %s", ErrIncompleteProviderResponse, blockNumber, index, transactionID)
		}
		identity := transactionID + ":" + strings.ToLower(strings.TrimSpace(*tx.LogIndex))
		if _, duplicate := seen[identity]; duplicate {
			return fmt.Errorf("%w: historical block %d contains duplicate event %s", ErrIncompleteProviderResponse, blockNumber, identity)
		}
		seen[identity] = struct{}{}
	}
	return nil
}

func historicalTransactionIDSet(chainID constants.ChainID, blockNumber int64, label string, ids []string) (map[string]struct{}, error) {
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		normalized := historicalIdentifier(chainID, id)
		if normalized == "" {
			return nil, fmt.Errorf("%w: historical block %d %s transaction identity is empty", ErrIncompleteProviderResponse, blockNumber, label)
		}
		if _, duplicate := set[normalized]; duplicate {
			return nil, fmt.Errorf("%w: historical block %d %s transaction identity %s is duplicated", ErrIncompleteProviderResponse, blockNumber, label, normalized)
		}
		set[normalized] = struct{}{}
	}
	return set, nil
}

func historicalIdentifier(chainID constants.ChainID, value string) string {
	value = strings.TrimSpace(value)
	if chainID == constants.Solana {
		return value
	}
	return strings.ToLower(value)
}

func historicalIdentifierEqual(chainID constants.ChainID, left, right string) bool {
	return historicalIdentifier(chainID, left) == historicalIdentifier(chainID, right)
}

func (s *Service) observeHistoricalCanonicalBlock(ctx context.Context, attestation HistoricalBlockAttestation) error {
	if s == nil || s.TransactionRepo == nil {
		if s == nil || s.ChainFactRepo == nil {
			return nil
		}
		return errors.New("transaction repository is required for canonical historical observation")
	}
	if err := s.TransactionRepo.ObserveCanonicalBlock(
		ctx,
		attestation.ChainID,
		attestation.BlockNumber,
		attestation.BlockHash,
		attestation.ParentHash,
	); err != nil {
		return fmt.Errorf("observe historical canonical block %d: %w", attestation.BlockNumber, err)
	}
	return nil
}

func (s *Service) rescan(ctx context.Context, chainID constants.ChainID, hash string, merchantID *uuid.UUID) (*Result, error) {
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return nil, errors.New("tx hash is required")
	}
	if !constants.IsSupportedChainID(chainID) {
		return nil, ErrUnsupportedChain
	}
	if s == nil || s.Bus == nil || s.Chains == nil || s.Registry == nil {
		return nil, errors.New("rescan service is not ready")
	}
	chain, err := s.Chains.GetChainByID(chainID)
	if err != nil {
		return nil, err
	}

	var events []eventCandidate
	switch chainID {
	case constants.Bitcoin:
		events, err = s.scanBitcoin(ctx, chain, hash)
	case constants.Solana:
		events, err = s.scanSolana(ctx, chain, hash)
	case constants.TRON, constants.TRONTestnet:
		events, err = s.scanTron(ctx, chain, hash)
	default:
		events, err = s.scanEVM(ctx, chain, hash)
	}
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, ErrTransactionNotFound
	}
	if merchantID != nil {
		events, err = s.filterMerchantEvents(ctx, chainID, events, *merchantID)
		if err != nil {
			return nil, err
		}
	} else {
		events, err = s.filterOwnedInboundEvents(ctx, chainID, events)
		if err != nil {
			return nil, err
		}
	}

	result := &Result{ChainID: chainID, Chain: constants.ChainName(chainID), Hash: hash}
	for _, candidate := range events {
		if candidate.Tx == nil {
			continue
		}
		s.ensureCandidateFinalityMetadata(&candidate)
		if err := s.observeCandidateCanonicalBlock(ctx, candidate); err != nil {
			return result, err
		}
		fact, err := s.recordRescanFact(ctx, candidate)
		if err != nil {
			return result, err
		}
		if fact == nil {
			continue
		}
		if err := s.Bus.DispatchAndWait(ctx, dispatcher.Event{
			Chain:       chainID,
			Type:        candidate.Type,
			Transaction: candidate.Tx,
		}); err != nil && !(errors.Is(err, dispatcher.ErrNoSubscribers) && s.ChainFactRepo != nil) {
			return result, err
		}
		result.Events++
		if s.TransactionRepo != nil {
			if unique, err := s.TransactionRepo.UniqueHash(*candidate.Tx); err == nil {
				result.UniqueHashes = append(result.UniqueHashes, unique)
			}
		}
		if fact != nil {
			summary, err := s.processDepositFact(ctx, *fact)
			if err != nil {
				return result, err
			}
			result.addDepositSummary(summary)
		}
		if err := s.applyTransactionFinality(ctx, candidate); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (s *Service) observeCandidateCanonicalBlock(ctx context.Context, candidate eventCandidate) error {
	if candidate.Tx == nil {
		return fmt.Errorf("%w: rescan candidate has no transaction", ErrIncompleteProviderResponse)
	}
	if s == nil || s.TransactionRepo == nil {
		if s != nil && s.ChainFactRepo == nil {
			return nil
		}
		return errors.New("transaction repository is required for canonical rescan observation")
	}
	if candidate.Tx.Block == nil || strings.TrimSpace(*candidate.Tx.Block) == "" ||
		candidate.Tx.BlockHash == nil || strings.TrimSpace(*candidate.Tx.BlockHash) == "" {
		return fmt.Errorf("%w: rescan candidate has no canonical block identity", ErrIncompleteProviderResponse)
	}
	blockNumber, err := strconv.ParseInt(strings.TrimSpace(*candidate.Tx.Block), 10, 64)
	if err != nil || blockNumber <= 0 {
		return fmt.Errorf("%w: rescan candidate has invalid block %q", ErrIncompleteProviderResponse, *candidate.Tx.Block)
	}
	parentHash := strings.TrimSpace(txRescanPtrString(candidate.Tx.ParentHash))
	if err := s.TransactionRepo.ObserveCanonicalBlock(
		ctx,
		candidate.Tx.ChainID,
		blockNumber,
		strings.TrimSpace(*candidate.Tx.BlockHash),
		parentHash,
	); err != nil {
		return fmt.Errorf("observe rescan canonical block %d: %w", blockNumber, err)
	}
	return nil
}

func (s *Service) ensureCandidateFinalityMetadata(candidate *eventCandidate) {
	if candidate == nil || candidate.Tx == nil || candidate.ConfirmationsRequired > 0 {
		return
	}
	candidate.ConfirmationsRequired = s.confirmationsRequired(candidate.Tx.ChainID)
	if candidate.ConfirmationsRequired == 0 {
		candidate.ConfirmationsRequired = 1
	}
}

func (s *Service) recordRescanFact(ctx context.Context, candidate eventCandidate) (*models.ChainFact, error) {
	if s == nil || s.ChainFactRepo == nil || candidate.Tx == nil {
		return nil, nil
	}
	assetSupported, err := s.candidateAssetSupported(candidate)
	if err != nil {
		return nil, err
	}
	if !assetSupported {
		return nil, nil
	}
	wallet, err := s.ownedInboundWallet(ctx, candidate.Tx.ChainID, candidate)
	if err != nil {
		return nil, err
	}
	if wallet == nil {
		return nil, nil
	}
	if s.TransactionRepo == nil {
		return nil, errors.New("transaction repository is required for durable rescan facts")
	}
	// Reconcile an existing transaction generation before recording/reviving its
	// fact. For a moved finalized transaction this atomically performs orphan
	// reversal followed by exact canonical lifecycle reset; RecordOrUpdate can
	// then revive the associated fact and reset its deposit inbox.
	if err := s.TransactionRepo.Create(*candidate.Tx); err != nil {
		return nil, err
	}
	fact, err := repositories.BuildChainFact(repositories.ChainFactBuildParams{
		EventType:             candidate.Type,
		Transaction:           *candidate.Tx,
		Confirmations:         candidate.Confirmations,
		ConfirmationsRequired: candidate.ConfirmationsRequired,
	})
	if err != nil {
		return nil, err
	}
	if candidate.ConfirmationsRequired > 0 {
		failed := candidate.Tx.Status != nil && strings.EqualFold(*candidate.Tx.Status, models.TransactionStatusFailed)
		fact.Finalized = !failed && candidate.Confirmations >= candidate.ConfirmationsRequired
	}
	stored, _, err := s.ChainFactRepo.RecordOrUpdate(ctx, &fact)
	if err != nil {
		return nil, err
	}
	return stored, nil
}

func (s *Service) candidateAssetSupported(candidate eventCandidate) (bool, error) {
	if candidate.Tx == nil {
		return false, nil
	}
	if s == nil || s.Registry == nil {
		return false, errors.New("asset registry is not ready")
	}
	if candidate.Tx.Token == nil || strings.TrimSpace(*candidate.Tx.Token) == "" {
		if candidateEventRequiresToken(candidate.Type) {
			return false, nil
		}
		_, ok := s.Registry.GetNative(candidate.Tx.ChainID)
		return ok, nil
	}
	_, ok := s.Registry.Get(candidate.Tx.ChainID, *candidate.Tx.Token)
	return ok, nil
}

func candidateEventRequiresToken(eventType string) bool {
	eventType = strings.ToLower(strings.TrimSpace(eventType))
	return strings.Contains(eventType, "token") || strings.HasPrefix(eventType, "spl_") || strings.HasPrefix(eventType, "erc20_") || strings.HasPrefix(eventType, "trc20_")
}

func (s *Service) processDeposits(ctx context.Context) error {
	service := s.depositProcessor()
	if service == nil {
		return nil
	}
	_, err := service.ProcessBatch(ctx, 200)
	return err
}

func (s *Service) processDepositFact(ctx context.Context, fact models.ChainFact) (depositsvc.ProcessSummary, error) {
	service := s.depositProcessor()
	if service == nil {
		return depositsvc.ProcessSummary{}, nil
	}
	return service.ProcessFactSafely(ctx, fact)
}

func (s *Service) applyTransactionFinality(ctx context.Context, candidate eventCandidate) error {
	if s == nil || s.TransactionRepo == nil || candidate.Tx == nil {
		return nil
	}
	uniqueHash, err := s.TransactionRepo.UniqueHash(*candidate.Tx)
	if err != nil {
		return err
	}
	status := strings.TrimSpace(txRescanPtrString(candidate.Tx.Status))
	if strings.EqualFold(status, models.TransactionStatusFailed) {
		if _, err := s.TransactionRepo.MarkFailed(ctx, uniqueHash); err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return nil
	}
	if !strings.EqualFold(status, models.TransactionStatusConfirmed) {
		return nil
	}
	required := candidate.ConfirmationsRequired
	if required == 0 {
		required = s.confirmationsRequired(candidate.Tx.ChainID)
	}
	if required == 0 {
		required = 1
	}
	finalized := candidate.Confirmations >= required
	if _, err := s.TransactionRepo.MarkFinality(ctx, uniqueHash, candidate.Confirmations, required, finalized); err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return nil
}

func txRescanPtrString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s *Service) depositProcessor() *depositsvc.Service {
	if s == nil ||
		s.Registry == nil ||
		s.ChainFactRepo == nil ||
		s.ChainStateRepo == nil ||
		s.DepositRepo == nil ||
		s.WalletRepo == nil ||
		s.TransactionRepo == nil {
		return nil
	}
	inboxRepo := s.MoneyEventInboxRepo
	if inboxRepo == nil && s.ChainFactRepo.DB() != nil {
		// Rescan used to bypass the durable inbox because the API constructor did
		// not expose it. Derive it from the same database so settlement always uses
		// the locked/reloaded ChainFact transaction boundary.
		inboxRepo = repositories.NewMoneyEventInboxRepo(s.ChainFactRepo.DB())
	}
	return depositsvc.New(depositsvc.Dependencies{
		AssetRegistry:       s.Registry,
		ChainFactRepo:       s.ChainFactRepo,
		ChainStateRepo:      s.ChainStateRepo,
		DepositRepo:         s.DepositRepo,
		WalletRepo:          s.WalletRepo,
		TransactionRepo:     s.TransactionRepo,
		PaymentRepo:         s.PaymentRepo,
		LedgerRepo:          s.LedgerRepo,
		MoneyEventInboxRepo: inboxRepo,
	}, s.Confirmations)
}

func (r *Result) addDepositSummary(summary depositsvc.ProcessSummary) {
	if r == nil {
		return
	}
	r.DepositsCreated += summary.DepositsCreated
	r.DepositsMatched += summary.Matched
	r.DepositsUnmatched += summary.Unmatched
	r.DepositsFinalized += summary.Finalized
	r.TransactionsRecorded += summary.TransactionsRecorded
	r.PaymentsSettled += summary.PaymentsSettled
}

func (s *Service) authorizeMerchantEvents(ctx context.Context, chainID constants.ChainID, events []eventCandidate, merchantID uuid.UUID) error {
	_, err := s.filterMerchantEvents(ctx, chainID, events, merchantID)
	return err
}

func (s *Service) filterMerchantEvents(ctx context.Context, chainID constants.ChainID, events []eventCandidate, merchantID uuid.UUID) ([]eventCandidate, error) {
	filtered := make([]eventCandidate, 0, len(events))
	for _, candidate := range events {
		wallet, err := s.ownedInboundWallet(ctx, chainID, candidate)
		if err != nil {
			return nil, err
		}
		if wallet != nil && wallet.MerchantID == merchantID {
			filtered = append(filtered, candidate)
		}
	}
	if len(filtered) == 0 {
		return nil, ErrUnauthorizedTx
	}
	return filtered, nil
}

func (s *Service) filterOwnedInboundEvents(ctx context.Context, chainID constants.ChainID, events []eventCandidate) ([]eventCandidate, error) {
	filtered := make([]eventCandidate, 0, len(events))
	for _, candidate := range events {
		wallet, err := s.ownedInboundWallet(ctx, chainID, candidate)
		if err != nil {
			return nil, err
		}
		if wallet != nil {
			filtered = append(filtered, candidate)
		}
	}
	if len(filtered) == 0 {
		return nil, ErrUnauthorizedTx
	}
	return filtered, nil
}

func (s *Service) ownedInboundWallet(ctx context.Context, chainID constants.ChainID, candidate eventCandidate) (*models.Wallet, error) {
	if candidate.Tx == nil || candidate.Tx.To == nil || strings.TrimSpace(*candidate.Tx.To) == "" ||
		candidate.Tx.Amount == nil || !positiveRawAmount(*candidate.Tx.Amount) {
		return nil, nil
	}
	if candidate.Tx.Status == nil || !strings.EqualFold(strings.TrimSpace(*candidate.Tx.Status), models.TransactionStatusConfirmed) {
		return nil, nil
	}
	if s == nil || s.WalletRepo == nil {
		return nil, errors.New("wallet repository is not ready")
	}
	wallet, err := s.WalletRepo.FindByChainAddress(ctx, chainID, *candidate.Tx.To)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for _, fromAddress := range candidateSourceAddresses(candidate) {
		fromWallet, fromErr := s.WalletRepo.FindByChainAddress(ctx, chainID, fromAddress)
		if fromErr == nil && fromWallet != nil && wallet != nil && fromWallet.MerchantID == wallet.MerchantID {
			return nil, nil
		}
		if fromErr != nil && !errors.Is(fromErr, gorm.ErrRecordNotFound) {
			return nil, fromErr
		}
	}
	return wallet, nil
}

func candidateSourceAddresses(candidate eventCandidate) []string {
	if candidate.Tx == nil {
		return nil
	}
	seen := make(map[string]struct{})
	out := make([]string, 0, len(candidate.Tx.FromAddresses)+1)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, exists := seen[value]; exists {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	for _, address := range candidate.Tx.FromAddresses {
		add(address)
	}
	if candidate.Tx.From != nil {
		add(*candidate.Tx.From)
	}
	return out
}

func (s *Service) scanEVM(ctx context.Context, chain blockchain.Chain, hash string) ([]eventCandidate, error) {
	var tx evmTx
	if err := s.evmRPC(ctx, chain, "eth_getTransactionByHash", []any{hash}, &tx); err != nil {
		return nil, err
	}
	if tx.Hash == "" {
		return nil, ErrTransactionNotFound
	}
	if !strings.EqualFold(strings.TrimSpace(tx.Hash), strings.TrimSpace(hash)) {
		return nil, fmt.Errorf("%w: EVM transaction hash %q does not match requested hash %q", ErrIncompleteProviderResponse, tx.Hash, hash)
	}
	var receipt evmReceipt
	if err := s.evmRPC(ctx, chain, "eth_getTransactionReceipt", []any{hash}, &receipt); err != nil {
		return nil, err
	}
	if receipt.TransactionHash == "" || receipt.BlockNumber == "" {
		return nil, ErrTransactionNotFound
	}
	if !strings.EqualFold(strings.TrimSpace(receipt.TransactionHash), strings.TrimSpace(tx.Hash)) {
		return nil, fmt.Errorf("%w: EVM receipt transaction hash %q does not match transaction %q", ErrIncompleteProviderResponse, receipt.TransactionHash, tx.Hash)
	}
	if strings.TrimSpace(receipt.BlockHash) == "" || strings.TrimSpace(tx.BlockHash) == "" ||
		!strings.EqualFold(strings.TrimSpace(receipt.BlockHash), strings.TrimSpace(tx.BlockHash)) {
		return nil, fmt.Errorf("%w: EVM transaction and receipt block hashes do not match", ErrIncompleteProviderResponse)
	}
	if strings.TrimSpace(tx.BlockNumber) == "" || hexToDec(tx.BlockNumber) == "" || hexToDec(tx.BlockNumber) != hexToDec(receipt.BlockNumber) {
		return nil, fmt.Errorf("%w: EVM transaction and receipt block numbers do not match", ErrIncompleteProviderResponse)
	}
	if strings.TrimSpace(tx.TransactionIndex) == "" || hexToDec(tx.TransactionIndex) == "" || hexToDec(tx.TransactionIndex) != hexToDec(receipt.TransactionIndex) {
		return nil, fmt.Errorf("%w: EVM transaction and receipt indexes do not match", ErrIncompleteProviderResponse)
	}
	parentHash, err := s.verifyEVMCanonicalBlock(ctx, chain, tx.Hash, receipt.BlockNumber, receipt.BlockHash)
	if err != nil {
		return nil, err
	}
	native, ok := s.Registry.GetNative(chain.ChainID())
	if !ok {
		return nil, errors.New("native asset is not registered")
	}

	status := receiptStatus(receipt.Status)
	if status == "" {
		return nil, fmt.Errorf("unsupported EVM receipt status %q for transaction %s", receipt.Status, tx.Hash)
	}
	blockNumber := hexToDec(receipt.BlockNumber)
	if blockNumber == "" || blockNumber == "0" {
		return nil, fmt.Errorf("%w: EVM receipt has invalid block number %q", ErrIncompleteProviderResponse, receipt.BlockNumber)
	}
	blockHash := receipt.BlockHash
	txIndex := hexToDec(receipt.TransactionIndex)
	confirmationsRequired := s.confirmationsRequired(chain.ChainID())
	confirmations := uint(0)
	if strings.EqualFold(status, models.TransactionStatusConfirmed) {
		parsedBlock, parseErr := strconv.ParseInt(blockNumber, 10, 64)
		if parseErr != nil || parsedBlock <= 0 {
			return nil, fmt.Errorf("%w: EVM receipt block number %q is invalid", ErrIncompleteProviderResponse, receipt.BlockNumber)
		}
		latest, latestErr := s.evmLatestBlockNumber(ctx, chain)
		if latestErr != nil {
			return nil, fmt.Errorf("load latest EVM block for finality: %w", latestErr)
		}
		if latest < parsedBlock {
			return nil, fmt.Errorf("%w: latest EVM block %d is behind transaction block %d", ErrIncompleteProviderResponse, latest, parsedBlock)
		}
		confirmations = uint(latest - parsedBlock + 1)
	}
	to := tx.To
	if to == "" {
		to = receipt.ContractAddress
	}
	if to == "" {
		to = "0x0000000000000000000000000000000000000000"
	}
	if !common.IsHexAddress(strings.TrimSpace(tx.From)) || !common.IsHexAddress(strings.TrimSpace(to)) ||
		(strings.TrimSpace(receipt.ContractAddress) != "" && !common.IsHexAddress(strings.TrimSpace(receipt.ContractAddress))) {
		return nil, fmt.Errorf("%w: EVM transaction contains malformed account addresses", ErrIncompleteProviderResponse)
	}
	value, err := parseHexBigStrict(tx.Value)
	if err != nil {
		return nil, fmt.Errorf("%w: EVM transaction value: %v", ErrIncompleteProviderResponse, err)
	}

	eventType := "transaction"
	if value.Sign() > 0 {
		eventType = "native_transfer"
	} else if isContractInput(tx.Input) || receipt.ContractAddress != "" {
		eventType = "contract_transaction"
	}

	events := []eventCandidate{{
		Type:                  eventType,
		Confirmations:         confirmations,
		ConfirmationsRequired: confirmationsRequired,
		Tx: &types.TransactionParam{
			Context:    ctx,
			ChainID:    chain.ChainID(),
			Symbol:     helpers.StrPtr(native.GetSymbol()),
			Decimals:   native.GetDecimals(),
			Hash:       helpers.StrPtr(tx.Hash),
			Block:      helpers.StrPtr(blockNumber),
			BlockHash:  helpers.StrPtr(blockHash),
			ParentHash: helpers.StrPtr(parentHash),
			Token:      nil,
			From:       helpers.StrPtr(strings.ToLower(tx.From)),
			To:         helpers.StrPtr(strings.ToLower(to)),
			Amount:     helpers.StrPtr(value.String()),
			LogIndex:   helpers.StrPtr("tx:" + txIndex),
			Status:     helpers.StrPtr(status),
			GasUsed:    optionalHexBigString(receipt.GasUsed),
			GasPrice:   optionalHexBigString(receipt.EffectiveGasPrice),
		},
	}}

	seenLogIndexes := make(map[string]struct{}, len(receipt.Logs))
	for _, entry := range receipt.Logs {
		logIndex := hexToDec(entry.LogIndex)
		if logIndex == "" {
			return nil, fmt.Errorf("%w: EVM receipt contains a log with invalid index %q", ErrIncompleteProviderResponse, entry.LogIndex)
		}
		if _, duplicate := seenLogIndexes[logIndex]; duplicate {
			return nil, fmt.Errorf("%w: EVM receipt contains duplicate log index %s", ErrIncompleteProviderResponse, logIndex)
		}
		seenLogIndexes[logIndex] = struct{}{}
		if !strings.EqualFold(strings.TrimSpace(entry.TransactionHash), strings.TrimSpace(tx.Hash)) ||
			!strings.EqualFold(strings.TrimSpace(entry.BlockHash), strings.TrimSpace(blockHash)) ||
			hexToDec(entry.BlockNumber) != blockNumber || hexToDec(entry.TransactionIndex) != txIndex {
			return nil, fmt.Errorf("%w: EVM log %s does not belong to the requested receipt", ErrIncompleteProviderResponse, logIndex)
		}
		if len(entry.Topics) == 0 || !strings.EqualFold(entry.Topics[0], erc20TransferTopic) {
			continue
		}
		if len(entry.Topics) < 3 || !common.IsHexAddress(strings.TrimSpace(entry.Address)) || !validEVMTopic(entry.Topics[1]) || !validEVMTopic(entry.Topics[2]) {
			return nil, fmt.Errorf("%w: EVM transfer log %s contains malformed address topics", ErrIncompleteProviderResponse, logIndex)
		}
		token := common.HexToAddress(entry.Address).Hex()
		assetInfo, ok := s.Registry.Get(chain.ChainID(), token)
		if !ok {
			continue
		}
		amount, err := parseHexBigStrict(entry.Data)
		if err != nil {
			return nil, fmt.Errorf("%w: EVM transfer log %s amount: %v", ErrIncompleteProviderResponse, logIndex, err)
		}
		if amount.Sign() <= 0 {
			continue
		}
		events = append(events, eventCandidate{
			Type:                  "token_transfer",
			Confirmations:         confirmations,
			ConfirmationsRequired: confirmationsRequired,
			Tx: &types.TransactionParam{
				Context:    ctx,
				ChainID:    chain.ChainID(),
				Symbol:     helpers.StrPtr(assetInfo.GetSymbol()),
				Decimals:   assetInfo.GetDecimals(),
				Hash:       helpers.StrPtr(tx.Hash),
				Block:      helpers.StrPtr(blockNumber),
				BlockHash:  helpers.StrPtr(blockHash),
				ParentHash: helpers.StrPtr(parentHash),
				Token:      helpers.StrPtr(token),
				From:       helpers.StrPtr(strings.ToLower(topicToEVMAddress(entry.Topics[1]))),
				To:         helpers.StrPtr(strings.ToLower(topicToEVMAddress(entry.Topics[2]))),
				Amount:     helpers.StrPtr(amount.String()),
				LogIndex:   helpers.StrPtr("log:" + logIndex),
				Status:     helpers.StrPtr(status),
				GasUsed:    optionalHexBigString(receipt.GasUsed),
				GasPrice:   optionalHexBigString(receipt.EffectiveGasPrice),
			},
		})
	}
	internalEvents, err := s.scanEVMInternalTransfers(ctx, chain, tx, receipt, blockNumber, blockHash, parentHash, status, confirmations, confirmationsRequired, native.GetSymbol(), native.GetDecimals())
	if err != nil {
		return nil, err
	}
	events = append(events, internalEvents...)
	return events, nil
}

func validEVMTopic(value string) bool {
	value = strings.TrimPrefix(strings.TrimSpace(value), "0x")
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == common.HashLength
}

func (s *Service) verifyEVMCanonicalBlock(ctx context.Context, chain blockchain.Chain, txHash, blockNumber, blockHash string) (string, error) {
	var block evmBlock
	if err := s.evmRPC(ctx, chain, "eth_getBlockByNumber", []any{blockNumber, false}, &block); err != nil {
		return "", fmt.Errorf("load canonical EVM block %s: %w", blockNumber, err)
	}
	if hexToDec(block.Number) == "" || hexToDec(block.Number) != hexToDec(blockNumber) ||
		strings.TrimSpace(block.Hash) == "" || !strings.EqualFold(strings.TrimSpace(block.Hash), strings.TrimSpace(blockHash)) {
		return "", fmt.Errorf("%w: canonical EVM block identity does not match transaction receipt", ErrIncompleteProviderResponse)
	}
	if strings.TrimSpace(block.ParentHash) == "" || strings.EqualFold(strings.TrimSpace(block.ParentHash), strings.TrimSpace(block.Hash)) {
		return "", fmt.Errorf("%w: canonical EVM block has invalid parent identity", ErrIncompleteProviderResponse)
	}
	for _, candidate := range block.Transactions {
		if strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(txHash)) {
			return strings.TrimSpace(block.ParentHash), nil
		}
	}
	return "", fmt.Errorf("%w: canonical EVM block %s does not contain transaction %s", ErrIncompleteProviderResponse, blockNumber, txHash)
}

func (s *Service) scanEVMInternalTransfers(
	ctx context.Context,
	chain blockchain.Chain,
	tx evmTx,
	receipt evmReceipt,
	blockNumber string,
	blockHash string,
	parentHash string,
	status string,
	confirmations uint,
	confirmationsRequired uint,
	symbol string,
	decimals uint8,
) ([]eventCandidate, error) {
	var traces []evmTrace
	if err := s.evmRPC(ctx, chain, "trace_transaction", []any{tx.Hash}, &traces); err != nil {
		return nil, fmt.Errorf("%w: EVM trace_transaction failed for %s: %v", ErrIncompleteProviderResponse, tx.Hash, err)
	}
	if len(traces) == 0 {
		return nil, fmt.Errorf("%w: EVM trace_transaction returned no execution trace for %s", ErrIncompleteProviderResponse, tx.Hash)
	}

	seen := make(map[string]struct{}, len(traces))
	failed := make(map[string]struct{})
	rootCount := 0
	for index, trace := range traces {
		if strings.TrimSpace(trace.TransactionHash) == "" || !strings.EqualFold(strings.TrimSpace(trace.TransactionHash), strings.TrimSpace(tx.Hash)) {
			return nil, fmt.Errorf("%w: EVM trace %d transaction identity does not match %s", ErrIncompleteProviderResponse, index, tx.Hash)
		}
		for _, component := range trace.TraceAddress {
			if component < 0 {
				return nil, fmt.Errorf("%w: EVM trace %d has a negative trace path", ErrIncompleteProviderResponse, index)
			}
		}
		path := evmTraceAddressKey(trace.TraceAddress)
		if _, duplicate := seen[path]; duplicate {
			return nil, fmt.Errorf("%w: EVM trace_transaction returned duplicate path %q", ErrIncompleteProviderResponse, path)
		}
		seen[path] = struct{}{}
		traceType := strings.ToLower(strings.TrimSpace(trace.Type))
		if len(trace.TraceAddress) == 0 {
			rootCount++
			expectedRootType := "call"
			if strings.TrimSpace(tx.To) == "" {
				expectedRootType = "create"
			}
			if traceType != expectedRootType {
				return nil, fmt.Errorf("%w: EVM root trace type %q does not match transaction shape", ErrIncompleteProviderResponse, trace.Type)
			}
		}
		if strings.TrimSpace(trace.Error) != "" {
			failed[path] = struct{}{}
		}
	}
	if rootCount != 1 {
		return nil, fmt.Errorf("%w: EVM trace_transaction returned %d root traces; want exactly 1", ErrIncompleteProviderResponse, rootCount)
	}
	for path := range seen {
		if path == "" || !strings.Contains(path, ".") {
			continue
		}
		parentPath := path[:strings.LastIndex(path, ".")]
		if _, ok := seen[parentPath]; !ok {
			return nil, fmt.Errorf("%w: EVM trace path %q has no parent trace", ErrIncompleteProviderResponse, path)
		}
	}

	out := make([]eventCandidate, 0, len(traces)-1)
	for _, trace := range traces {
		if len(trace.TraceAddress) == 0 || !strings.EqualFold(status, models.TransactionStatusConfirmed) || evmTracePathFailed(trace.TraceAddress, failed) {
			continue
		}
		from := strings.TrimSpace(trace.Action.From)
		to := strings.TrimSpace(trace.Action.To)
		valueRaw := strings.TrimSpace(trace.Action.Value)
		switch strings.ToLower(strings.TrimSpace(trace.Type)) {
		case "call":
			switch strings.ToLower(strings.TrimSpace(trace.Action.CallType)) {
			case "call":
			case "delegatecall", "staticcall", "callcode":
				continue
			default:
				return nil, fmt.Errorf("%w: EVM trace %s has unsupported callType %q", ErrIncompleteProviderResponse, evmInternalTraceLogIndex(trace.TraceAddress), trace.Action.CallType)
			}
		case "create":
			if to == "" {
				to = strings.TrimSpace(trace.Result.Address)
			}
		case "suicide", "selfdestruct":
			from = strings.TrimSpace(trace.Action.Address)
			to = strings.TrimSpace(trace.Action.RefundAddress)
			valueRaw = strings.TrimSpace(trace.Action.Balance)
		default:
			return nil, fmt.Errorf("%w: EVM trace %s has unsupported type %q", ErrIncompleteProviderResponse, evmInternalTraceLogIndex(trace.TraceAddress), trace.Type)
		}
		value, err := parseHexBigStrict(valueRaw)
		if err != nil {
			return nil, fmt.Errorf("%w: EVM trace %s value: %v", ErrIncompleteProviderResponse, evmInternalTraceLogIndex(trace.TraceAddress), err)
		}
		if value.Sign() == 0 {
			continue
		}
		if !common.IsHexAddress(from) || !common.IsHexAddress(to) {
			return nil, fmt.Errorf("%w: EVM trace %s contains malformed account addresses", ErrIncompleteProviderResponse, evmInternalTraceLogIndex(trace.TraceAddress))
		}
		out = append(out, eventCandidate{
			Type:                  "internal_transfer",
			Confirmations:         confirmations,
			ConfirmationsRequired: confirmationsRequired,
			Tx: &types.TransactionParam{
				Context:    ctx,
				ChainID:    chain.ChainID(),
				Symbol:     helpers.StrPtr(symbol),
				Decimals:   decimals,
				Hash:       helpers.StrPtr(tx.Hash),
				Block:      helpers.StrPtr(blockNumber),
				BlockHash:  helpers.StrPtr(blockHash),
				ParentHash: helpers.StrPtr(parentHash),
				From:       helpers.StrPtr(strings.ToLower(from)),
				To:         helpers.StrPtr(strings.ToLower(to)),
				Amount:     helpers.StrPtr(value.String()),
				LogIndex:   helpers.StrPtr(evmInternalTraceLogIndex(trace.TraceAddress)),
				Status:     helpers.StrPtr(status),
				GasUsed:    optionalHexBigString(receipt.GasUsed),
				GasPrice:   optionalHexBigString(receipt.EffectiveGasPrice),
			},
		})
	}
	return out, nil
}

func evmTraceAddressKey(traceAddress []int) string {
	parts := make([]string, len(traceAddress))
	for index, value := range traceAddress {
		parts[index] = strconv.Itoa(value)
	}
	return strings.Join(parts, ".")
}

func evmInternalTraceLogIndex(traceAddress []int) string {
	return "internal:" + evmTraceAddressKey(traceAddress)
}

func evmTracePathFailed(traceAddress []int, failed map[string]struct{}) bool {
	if _, rootFailed := failed[""]; rootFailed {
		return true
	}
	for length := 1; length <= len(traceAddress); length++ {
		if _, failedAncestor := failed[evmTraceAddressKey(traceAddress[:length])]; failedAncestor {
			return true
		}
	}
	return false
}

func (s *Service) evmLatestBlockNumber(ctx context.Context, chain blockchain.Chain) (int64, error) {
	var result string
	if err := s.evmRPC(ctx, chain, "eth_blockNumber", []any{}, &result); err != nil {
		return 0, err
	}
	blockNumber := hexToDec(result)
	if blockNumber == "" {
		return 0, ErrTransactionNotFound
	}
	parsed, err := strconv.ParseInt(blockNumber, 10, 64)
	if err != nil {
		return 0, err
	}
	return parsed, nil
}

func (s *Service) evmRPC(ctx context.Context, chain blockchain.Chain, method string, params []any, out any) error {
	return s.chainJSONRPC(ctx, chain, method, params, out, fmt.Sprintf("%s has no RPC endpoint configured", chain.Name()))
}

func (s *Service) chainJSONRPC(ctx context.Context, chain blockchain.Chain, method string, params []any, out any, noEndpointError string) error {
	requestID := time.Now().UnixNano()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": requestID, "method": method, "params": params})
	if err != nil {
		return err
	}
	if err := validateRPCOutput(out); err != nil {
		return err
	}
	var lastErr error
	attempted := 0
	notFound := 0
	for _, rpcURL := range chain.RPCs() {
		rpcURL = strings.TrimSpace(rpcURL)
		if rpcURL == "" {
			continue
		}
		attempted++

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, bytes.NewReader(body))
		if err != nil {
			lastErr = fmt.Errorf("%s %s request build failed: %w", chain.Name(), rpcURL, err)
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := s.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("%s %s request failed: %w", chain.Name(), rpcURL, err)
			continue
		}
		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("%s %s response read failed: %w", chain.Name(), rpcURL, readErr)
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("%s %s returned HTTP %d: %s", chain.Name(), rpcURL, resp.StatusCode, string(respBody))
			continue
		}
		var rpcResp rpcResponse
		if err := json.Unmarshal(respBody, &rpcResp); err != nil {
			lastErr = fmt.Errorf("%s %s response decode failed: %w", chain.Name(), rpcURL, err)
			continue
		}
		if rpcResp.Error != nil {
			lastErr = fmt.Errorf("%s %s RPC %s error %d: %s", chain.Name(), rpcURL, method, rpcResp.Error.Code, rpcResp.Error.Message)
			continue
		}
		if rpcResp.JSONRPC != "2.0" {
			lastErr = fmt.Errorf("%w: %s %s RPC %s returned jsonrpc %q", ErrIncompleteProviderResponse, chain.Name(), rpcURL, method, rpcResp.JSONRPC)
			continue
		}
		if !rpcResponseIDMatches(rpcResp.ID, requestID) {
			lastErr = fmt.Errorf("%w: %s %s RPC %s response id does not match request id %d", ErrIncompleteProviderResponse, chain.Name(), rpcURL, method, requestID)
			continue
		}
		result := bytes.TrimSpace(rpcResp.Result)
		if len(result) == 0 {
			lastErr = fmt.Errorf("%w: %s %s RPC %s omitted result", ErrIncompleteProviderResponse, chain.Name(), rpcURL, method)
			continue
		}
		if bytes.Equal(result, []byte("null")) {
			notFound++
			continue
		}
		if err := decodeRPCResult(result, out); err != nil {
			lastErr = fmt.Errorf("%s %s RPC %s result decode failed: %w", chain.Name(), rpcURL, method, err)
			continue
		}
		return nil
	}
	if attempted > 0 && notFound == attempted {
		return fmt.Errorf("%w: %s RPC %s returned null from every endpoint", ErrTransactionNotFound, chain.Name(), method)
	}
	if lastErr == nil {
		if notFound > 0 {
			lastErr = fmt.Errorf("%w: %s RPC %s returned no result", ErrTransactionNotFound, chain.Name(), method)
		} else {
			lastErr = errors.New(noEndpointError)
		}
	}
	return lastErr
}

func validateRPCOutput(out any) error {
	value := reflect.ValueOf(out)
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() {
		return errors.New("RPC output must be a non-nil pointer")
	}
	return nil
}

func decodeRPCResult(result json.RawMessage, out any) error {
	value := reflect.ValueOf(out)
	temporary := reflect.New(value.Elem().Type())
	if err := json.Unmarshal(result, temporary.Interface()); err != nil {
		return err
	}
	value.Elem().Set(temporary.Elem())
	return nil
}

func rpcResponseIDMatches(raw json.RawMessage, requestID int64) bool {
	value := strings.TrimSpace(string(raw))
	expected := strconv.FormatInt(requestID, 10)
	return value == expected || value == strconv.Quote(expected)
}

func (s *Service) scanBitcoin(ctx context.Context, chain blockchain.Chain, hash string) ([]eventCandidate, error) {
	body, err := s.bitcoinGet(ctx, chain, "/tx/"+hash)
	if err != nil {
		return nil, err
	}
	var tx btcTx
	if err := json.Unmarshal(body, &tx); err != nil {
		return nil, err
	}
	if tx.TxID == "" {
		return nil, ErrTransactionNotFound
	}
	if !strings.EqualFold(strings.TrimSpace(tx.TxID), strings.TrimSpace(hash)) {
		return nil, fmt.Errorf("%w: Bitcoin transaction id %q does not match requested id %q", ErrIncompleteProviderResponse, tx.TxID, hash)
	}
	if tx.Status == nil {
		return nil, fmt.Errorf("%w: Bitcoin transaction %s omitted status", ErrIncompleteProviderResponse, tx.TxID)
	}
	if len(tx.Vin) == 0 || len(tx.Vout) == 0 {
		return nil, fmt.Errorf("%w: Bitcoin transaction %s omitted inputs or outputs", ErrIncompleteProviderResponse, tx.TxID)
	}
	native, ok := s.Registry.GetNative(chain.ChainID())
	if !ok {
		return nil, errors.New("native asset is not registered")
	}
	from := "coinbase"
	fromAddresses := make([]string, 0, len(tx.Vin))
	for _, input := range tx.Vin {
		if input.IsCoinbase {
			continue
		}
		if input.Prevout == nil || strings.TrimSpace(input.Prevout.Address) == "" {
			return nil, fmt.Errorf("%w: Bitcoin transaction %s has an input without prevout ownership", ErrIncompleteProviderResponse, tx.TxID)
		}
		if input.Prevout.Address != "" {
			fromAddresses = append(fromAddresses, input.Prevout.Address)
			if from == "coinbase" {
				from = input.Prevout.Address
			}
		}
	}
	status := "confirmed"
	if !tx.Status.Confirmed {
		status = "pending"
	}
	block := fmt.Sprintf("%d", tx.Status.BlockHeight)
	confirmationsRequired := s.confirmationsRequired(chain.ChainID())
	confirmations := uint(0)
	if tx.Status.Confirmed {
		if tx.Status.BlockHeight <= 0 || strings.TrimSpace(tx.Status.BlockHash) == "" {
			return nil, fmt.Errorf("%w: confirmed Bitcoin transaction %s has no canonical block identity", ErrIncompleteProviderResponse, tx.TxID)
		}
		canonicalHash, err := s.bitcoinCanonicalBlockHash(ctx, chain, tx.Status.BlockHeight)
		if err != nil {
			return nil, fmt.Errorf("load canonical Bitcoin block %d: %w", tx.Status.BlockHeight, err)
		}
		if !strings.EqualFold(canonicalHash, strings.TrimSpace(tx.Status.BlockHash)) {
			return nil, fmt.Errorf("%w: canonical Bitcoin block %d hash %q does not match transaction block hash %q", ErrIncompleteProviderResponse, tx.Status.BlockHeight, canonicalHash, tx.Status.BlockHash)
		}
		latest, err := s.bitcoinLatestBlockHeight(ctx, chain)
		if err != nil {
			return nil, fmt.Errorf("load latest Bitcoin block for finality: %w", err)
		}
		if latest < tx.Status.BlockHeight {
			return nil, fmt.Errorf("%w: latest Bitcoin block %d is behind transaction block %d", ErrIncompleteProviderResponse, latest, tx.Status.BlockHeight)
		}
		confirmations = uint(latest - tx.Status.BlockHeight + 1)
	}
	memo := bitcoinTxMemo(tx)
	events := make([]eventCandidate, 0, len(tx.Vout))
	for idx, output := range tx.Vout {
		if output.Address == "" || output.Value <= 0 {
			continue
		}
		events = append(events, eventCandidate{
			Type:                  "utxo_transfer",
			Confirmations:         confirmations,
			ConfirmationsRequired: confirmationsRequired,
			Tx: &types.TransactionParam{
				Context:       ctx,
				ChainID:       chain.ChainID(),
				Symbol:        helpers.StrPtr(native.GetSymbol()),
				Decimals:      native.GetDecimals(),
				Hash:          helpers.StrPtr(tx.TxID),
				Block:         helpers.StrPtr(block),
				BlockHash:     helpers.StrPtr(tx.Status.BlockHash),
				Token:         nil,
				From:          helpers.StrPtr(from),
				FromAddresses: append([]string(nil), fromAddresses...),
				To:            helpers.StrPtr(output.Address),
				Amount:        helpers.StrPtr(fmt.Sprintf("%d", output.Value)),
				LogIndex:      helpers.StrPtr(fmt.Sprintf("vout:%d", idx)),
				Status:        helpers.StrPtr(status),
				Memo:          optionalMemoPtr(memo),
			},
		})
	}
	return events, nil
}

func (s *Service) bitcoinCanonicalBlockHash(ctx context.Context, chain blockchain.Chain, blockHeight int64) (string, error) {
	body, err := s.bitcoinGet(ctx, chain, fmt.Sprintf("/block-height/%d", blockHeight))
	if err != nil {
		return "", err
	}
	hash := strings.TrimSpace(string(body))
	if hash == "" || strings.ContainsAny(hash, " \t\r\n") {
		return "", fmt.Errorf("%w: Bitcoin block %d hash %q is invalid", ErrIncompleteProviderResponse, blockHeight, hash)
	}
	return hash, nil
}

func (s *Service) bitcoinLatestBlockHeight(ctx context.Context, chain blockchain.Chain) (int64, error) {
	body, err := s.bitcoinGet(ctx, chain, "/blocks/tip/height")
	if err != nil {
		return 0, err
	}
	height, err := strconv.ParseInt(strings.TrimSpace(string(body)), 10, 64)
	if err != nil || height <= 0 {
		return 0, fmt.Errorf("%w: Bitcoin tip height %q is invalid", ErrIncompleteProviderResponse, strings.TrimSpace(string(body)))
	}
	return height, nil
}

func (s *Service) bitcoinGet(ctx context.Context, chain blockchain.Chain, path string) ([]byte, error) {
	var lastErr error
	attempted := 0
	notFound := 0
	for _, baseURL := range chain.RPCs() {
		baseURL = strings.TrimSpace(baseURL)
		if baseURL == "" {
			continue
		}
		attempted++

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+path, nil)
		if err != nil {
			lastErr = fmt.Errorf("%s %s request build failed: %w", chain.Name(), baseURL, err)
			continue
		}
		resp, err := s.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("%s %s request failed: %w", chain.Name(), baseURL, err)
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("%s %s response read failed: %w", chain.Name(), baseURL, readErr)
			continue
		}
		if resp.StatusCode == http.StatusNotFound {
			notFound++
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("%s %s returned HTTP %d: %s", chain.Name(), baseURL, resp.StatusCode, string(body))
			continue
		}
		return body, nil
	}
	if attempted > 0 && notFound == attempted {
		return nil, ErrTransactionNotFound
	}
	if lastErr == nil {
		if notFound > 0 {
			lastErr = fmt.Errorf("%w: Bitcoin endpoint results were inconclusive", ErrIncompleteProviderResponse)
		} else {
			lastErr = fmt.Errorf("bitcoin has no API endpoint configured")
		}
	}
	return nil, lastErr
}

func (s *Service) scanSolana(ctx context.Context, chain blockchain.Chain, hash string) ([]eventCandidate, error) {
	var blockTx *solanaBlockTx
	if err := s.solanaRPC(ctx, chain, "getTransaction", []any{
		hash,
		map[string]any{"encoding": "jsonParsed", "commitment": "finalized", "maxSupportedTransactionVersion": 0},
	}, &blockTx); err != nil {
		return nil, err
	}
	if blockTx == nil || len(blockTx.Transaction.Signatures) == 0 {
		return nil, ErrTransactionNotFound
	}
	if blockTx.Transaction.Signatures[0] != strings.TrimSpace(hash) {
		return nil, fmt.Errorf("%w: Solana signature %q does not match requested signature %q", ErrIncompleteProviderResponse, blockTx.Transaction.Signatures[0], hash)
	}
	if blockTx.Meta == nil {
		return nil, fmt.Errorf("%w: Solana transaction %s omitted metadata", ErrIncompleteProviderResponse, hash)
	}
	executionStatus := bytes.TrimSpace(blockTx.Meta.Err)
	if len(executionStatus) == 0 || !json.Valid(executionStatus) {
		return nil, fmt.Errorf("%w: Solana transaction %s metadata omitted execution status", ErrIncompleteProviderResponse, hash)
	}
	if blockTx.Meta.PreBalances == nil || blockTx.Meta.PostBalances == nil {
		return nil, fmt.Errorf("%w: Solana transaction %s metadata omitted preBalances or postBalances", ErrIncompleteProviderResponse, hash)
	}
	if len(blockTx.Meta.PreBalances) != len(blockTx.Meta.PostBalances) {
		return nil, fmt.Errorf(
			"%w: Solana transaction %s balance vector length mismatch: pre=%d post=%d",
			ErrIncompleteProviderResponse,
			hash,
			len(blockTx.Meta.PreBalances),
			len(blockTx.Meta.PostBalances),
		)
	}
	if blockTx.Slot <= 0 {
		return nil, fmt.Errorf("%w: Solana transaction %s has no finalized slot identity", ErrIncompleteProviderResponse, hash)
	}
	parsedKeys, err := solanaTransactionAccountKeys(
		blockTx.Transaction.Message.AccountKeys,
		blockTx.Meta.LoadedAddresses,
		len(blockTx.Meta.PreBalances),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: Solana transaction %s account keys: %v", ErrIncompleteProviderResponse, hash, err)
	}
	blockHash, err := s.solanaCanonicalBlockHash(ctx, chain, blockTx.Slot, hash)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(blockTx.Blockhash) != "" && blockTx.Blockhash != blockHash {
		return nil, fmt.Errorf("%w: Solana transaction and canonical slot blockhashes do not match", ErrIncompleteProviderResponse)
	}
	native, ok := s.Registry.GetNative(chain.ChainID())
	if !ok {
		return nil, errors.New("native asset is not registered")
	}
	blockNumber := fmt.Sprintf("%d", blockTx.Slot)
	status := "confirmed"
	if !bytes.Equal(executionStatus, []byte("null")) {
		status = "failed"
	}
	confirmationsRequired := s.confirmationsRequired(chain.ChainID())
	confirmations := confirmationsRequired
	if status == "failed" {
		confirmations = 0
	}
	signer := firstSolanaSigner(parsedKeys)
	if signer == "" && len(blockTx.Transaction.Message.AccountKeys) > 0 {
		if len(parsedKeys) > 0 {
			signer = parsedKeys[0].Pubkey
		}
	}
	if signer == "" {
		signer = "unknown_signer"
	}

	memo := solanaTransactionMemo(blockTx.Transaction.Message.Instructions, blockTx.Meta.InnerInstructions)
	tokenAccounts, tokenBalanceWarnings := solanaTokenAccountMetadataByAddress(blockTx.Transaction.Message.AccountKeys, *blockTx.Meta)
	if len(tokenBalanceWarnings) > 0 {
		return nil, fmt.Errorf("%w: Solana transaction %s token metadata: %s", ErrIncompleteProviderResponse, hash, strings.Join(tokenBalanceWarnings, "; "))
	}
	seenInnerIndexes := make(map[int]struct{}, len(blockTx.Meta.InnerInstructions))
	for _, group := range blockTx.Meta.InnerInstructions {
		if group.Index < 0 || group.Index >= len(blockTx.Transaction.Message.Instructions) {
			return nil, fmt.Errorf("%w: Solana transaction %s has inner instruction group index %d outside message", ErrIncompleteProviderResponse, hash, group.Index)
		}
		if _, duplicate := seenInnerIndexes[group.Index]; duplicate {
			return nil, fmt.Errorf("%w: Solana transaction %s has duplicate inner instruction group index %d", ErrIncompleteProviderResponse, hash, group.Index)
		}
		seenInnerIndexes[group.Index] = struct{}{}
	}
	nativeEvents, err := solanaNativeBalanceEvents(
		ctx,
		chain.ChainID(),
		blockNumber,
		blockHash,
		hash,
		native,
		status,
		signer,
		memo,
		parsedKeys,
		blockTx.Meta.PreBalances,
		blockTx.Meta.PostBalances,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIncompleteProviderResponse, err)
	}
	events := withCandidateFinality(nativeEvents, confirmations, confirmationsRequired)
	for idx, ix := range blockTx.Transaction.Message.Instructions {
		instructionEvents, err := s.solanaInstructionEventValidated(ctx, chain.ChainID(), blockNumber, blockHash, hash, fmt.Sprintf("ix:%d", idx), native, status, signer, memo, ix, tokenAccounts)
		if err != nil {
			return nil, err
		}
		events = append(events, withCandidateFinality(instructionEvents, confirmations, confirmationsRequired)...)
	}
	for _, group := range blockTx.Meta.InnerInstructions {
		for idx, ix := range group.Instructions {
			instructionEvents, err := s.solanaInstructionEventValidated(ctx, chain.ChainID(), blockNumber, blockHash, hash, fmt.Sprintf("inner:%d:%d", group.Index, idx), native, status, signer, memo, ix, tokenAccounts)
			if err != nil {
				return nil, err
			}
			events = append(events, withCandidateFinality(instructionEvents, confirmations, confirmationsRequired)...)
		}
	}
	return events, nil
}

func (s *Service) solanaCanonicalBlockHash(ctx context.Context, chain blockchain.Chain, slot int64, signature string) (string, error) {
	var block solanaBlockIdentity
	if err := s.solanaRPC(ctx, chain, "getBlock", []any{
		slot,
		map[string]any{
			"commitment":                     "finalized",
			"transactionDetails":             "signatures",
			"rewards":                        false,
			"maxSupportedTransactionVersion": 0,
		},
	}, &block); err != nil {
		return "", fmt.Errorf("load canonical Solana slot %d: %w", slot, err)
	}
	if strings.TrimSpace(block.Blockhash) == "" {
		return "", fmt.Errorf("%w: Solana slot %d omitted blockhash", ErrIncompleteProviderResponse, slot)
	}
	for _, candidate := range block.Signatures {
		if strings.TrimSpace(candidate) == strings.TrimSpace(signature) {
			return block.Blockhash, nil
		}
	}
	return "", fmt.Errorf("%w: Solana slot %d does not contain signature %s", ErrIncompleteProviderResponse, slot, signature)
}

func withCandidateFinality(events []eventCandidate, confirmations, required uint) []eventCandidate {
	for index := range events {
		events[index].Confirmations = confirmations
		events[index].ConfirmationsRequired = required
	}
	return events
}

func solanaNativeBalanceEvents(
	ctx context.Context,
	chainID constants.ChainID,
	blockNumber string,
	blockHash string,
	hash string,
	native asset.Asset,
	status string,
	signer string,
	memo string,
	accountKeys []solanaAccountKey,
	preBalances []uint64,
	postBalances []uint64,
) ([]eventCandidate, error) {
	if len(preBalances) != len(postBalances) || len(accountKeys) != len(preBalances) {
		return nil, fmt.Errorf(
			"Solana transaction %s native balance shape mismatch: accounts=%d pre=%d post=%d",
			hash,
			len(accountKeys),
			len(preBalances),
			len(postBalances),
		)
	}
	// Failed transactions roll back instruction state changes. The transaction
	// fee may reduce a payer balance, but a failed transaction cannot produce a
	// merchant receipt.
	if status != "confirmed" {
		return nil, nil
	}
	debitAddresses := make([]string, 0)
	for index, postBalance := range postBalances {
		if postBalance < preBalances[index] {
			debitAddresses = append(debitAddresses, accountKeys[index].Pubkey)
		}
	}

	events := make([]eventCandidate, 0)
	for index, postBalance := range postBalances {
		preBalance := preBalances[index]
		if postBalance <= preBalance {
			continue
		}
		destination := accountKeys[index].Pubkey
		amount := strconv.FormatUint(postBalance-preBalance, 10)
		logIndex := fmt.Sprintf("balance:%d", index)
		events = append(events, eventCandidate{
			Type: "sol_transfer",
			Tx: &types.TransactionParam{
				Context:       ctx,
				ChainID:       chainID,
				Symbol:        helpers.StrPtr(native.GetSymbol()),
				Decimals:      native.GetDecimals(),
				Hash:          helpers.StrPtr(hash),
				Block:         helpers.StrPtr(blockNumber),
				BlockHash:     helpers.StrPtr(blockHash),
				From:          helpers.StrPtr(signer),
				FromAddresses: append([]string(nil), debitAddresses...),
				To:            helpers.StrPtr(destination),
				Amount:        helpers.StrPtr(amount),
				LogIndex:      helpers.StrPtr(logIndex),
				Status:        helpers.StrPtr(status),
				Memo:          optionalMemoPtr(memo),
			},
		})
	}
	return events, nil
}

func (s *Service) solanaInstructionEventValidated(ctx context.Context, chainID constants.ChainID, blockNumber, blockHash, hash, logIndex string, native asset.Asset, status, signer, memo string, ix solanaInstruction, tokenAccounts map[string]solanaTokenAccountMetadata) ([]eventCandidate, error) {
	parsed, ok := parseSolanaInstruction(ix)
	if ok {
		program := normalizedSolanaInstructionProgram(ix)
		ixType := strings.ToLower(parsed.Type)
		if program == "system" && (ixType == "transfer" || ixType == "transferwithseed") {
			source := rawString(parsed.Info, "source")
			destination := rawString(parsed.Info, "destination")
			lamports := rawString(parsed.Info, "lamports")
			if source == "" || destination == "" || lamports == "" {
				return nil, fmt.Errorf("%w: Solana system transfer %s is missing source, destination, or lamports", ErrIncompleteProviderResponse, logIndex)
			}
			lamportAmount, valid := new(big.Int).SetString(strings.TrimSpace(lamports), 10)
			if !valid || lamportAmount.Sign() < 0 {
				return nil, fmt.Errorf("%w: Solana system transfer %s has invalid lamports %q", ErrIncompleteProviderResponse, logIndex, lamports)
			}
			// Native SOL is emitted once from the authoritative transaction balance
			// vector. Parsed system instructions are validation-only to avoid
			// double-crediting the same economic receipt.
			return nil, nil
		}
		if strings.HasPrefix(program, "spl-token") && (ixType == "transfer" || ixType == "transferchecked") {
			source := rawString(parsed.Info, "source")
			destination := rawString(parsed.Info, "destination")
			mint := rawString(parsed.Info, "mint")
			amount := rawString(parsed.Info, "amount")
			if tokenAmountRaw, ok := parsed.Info["tokenAmount"]; ok {
				var tokenAmount map[string]json.RawMessage
				if err := json.Unmarshal(tokenAmountRaw, &tokenAmount); err != nil {
					return nil, fmt.Errorf("%w: Solana token transfer %s has malformed tokenAmount", ErrIncompleteProviderResponse, logIndex)
				}
				if amount == "" {
					amount = rawString(tokenAmount, "amount")
				}
			}
			if source == "" || destination == "" || amount == "" || !positiveRawAmount(amount) {
				return nil, fmt.Errorf("%w: Solana token transfer %s is missing source, destination, or positive amount", ErrIncompleteProviderResponse, logIndex)
			}

			sourceMetadata, sourceOK := tokenAccounts[source]
			destinationMetadata, destinationOK := tokenAccounts[destination]
			if !sourceOK || !destinationOK ||
				sourceMetadata.Owner == "" || destinationMetadata.Owner == "" ||
				sourceMetadata.Mint == "" || destinationMetadata.Mint == "" ||
				sourceMetadata.Mint != destinationMetadata.Mint ||
				!sourceMetadata.HasDecimals || !destinationMetadata.HasDecimals ||
				sourceMetadata.Decimals != destinationMetadata.Decimals {
				return nil, fmt.Errorf("%w: Solana token transfer %s has incomplete account ownership metadata", ErrIncompleteProviderResponse, logIndex)
			}
			if mint == "" {
				mint = destinationMetadata.Mint
			}
			if mint != destinationMetadata.Mint {
				return nil, fmt.Errorf("%w: Solana token transfer %s mint does not match account metadata", ErrIncompleteProviderResponse, logIndex)
			}
			if sourceMetadata.ProgramID != "" && destinationMetadata.ProgramID != "" && sourceMetadata.ProgramID != destinationMetadata.ProgramID {
				return nil, fmt.Errorf("%w: Solana token transfer %s account programs do not match", ErrIncompleteProviderResponse, logIndex)
			}
			instructionProgramID := strings.TrimSpace(ix.ProgramID)
			if instructionProgramID != "" {
				if sourceMetadata.ProgramID != "" && sourceMetadata.ProgramID != instructionProgramID {
					return nil, fmt.Errorf("%w: Solana token transfer %s source program does not match instruction", ErrIncompleteProviderResponse, logIndex)
				}
				if destinationMetadata.ProgramID != "" && destinationMetadata.ProgramID != instructionProgramID {
					return nil, fmt.Errorf("%w: Solana token transfer %s destination program does not match instruction", ErrIncompleteProviderResponse, logIndex)
				}
			}
			if s.Registry == nil {
				return nil, errors.New("asset registry is not ready")
			}
			assetInfo, ok := s.Registry.Get(chainID, mint)
			if !ok {
				return nil, nil
			}
			if assetInfo.GetDecimals() != destinationMetadata.Decimals {
				return nil, fmt.Errorf("%w: Solana token transfer %s decimals do not match registry", ErrIncompleteProviderResponse, logIndex)
			}
			return []eventCandidate{{Type: "spl_transfer", Tx: &types.TransactionParam{
				Context: ctx, ChainID: chainID, Symbol: helpers.StrPtr(assetInfo.GetSymbol()), Decimals: assetInfo.GetDecimals(),
				Hash: helpers.StrPtr(hash), Block: helpers.StrPtr(blockNumber), BlockHash: helpers.StrPtr(blockHash), Token: helpers.StrPtr(mint),
				From: helpers.StrPtr(sourceMetadata.Owner), To: helpers.StrPtr(destinationMetadata.Owner), Amount: helpers.StrPtr(amount), LogIndex: helpers.StrPtr(logIndex), Status: helpers.StrPtr(status), Memo: optionalMemoPtr(memo),
			}}}, nil
		}
	} else {
		program := normalizedSolanaInstructionProgram(ix)
		if strings.HasPrefix(program, "spl-token") {
			return nil, fmt.Errorf("%w: Solana transfer-capable instruction %s was not parsed", ErrIncompleteProviderResponse, logIndex)
		}
	}

	programID := ix.ProgramID
	if programID == "" {
		programID = ix.Program
	}
	if programID == "" {
		programID = "unknown_program"
	}
	return []eventCandidate{{Type: "program_call", Tx: &types.TransactionParam{
		Context: ctx, ChainID: chainID, Symbol: helpers.StrPtr(native.GetSymbol()), Decimals: native.GetDecimals(),
		Hash: helpers.StrPtr(hash), Block: helpers.StrPtr(blockNumber), BlockHash: helpers.StrPtr(blockHash),
		From: helpers.StrPtr(signer), To: helpers.StrPtr(programID), Amount: helpers.StrPtr("0"), LogIndex: helpers.StrPtr(logIndex), Status: helpers.StrPtr(status), Memo: optionalMemoPtr(memo),
	}}}, nil
}

func normalizedSolanaInstructionProgram(ix solanaInstruction) string {
	program := strings.ToLower(strings.TrimSpace(ix.Program))
	if program != "" {
		return program
	}
	switch strings.TrimSpace(ix.ProgramID) {
	case solanaSystemProgram:
		return "system"
	case solanaTokenProgram:
		return "spl-token"
	case solanaToken2022:
		return "spl-token-2022"
	default:
		return ""
	}
}

func (s *Service) solanaRPC(ctx context.Context, chain blockchain.Chain, method string, params []any, out any) error {
	return s.chainJSONRPC(ctx, chain, method, params, out, "no solana RPC endpoint configured")
}

func (s *Service) scanTron(ctx context.Context, chain blockchain.Chain, hash string) ([]eventCandidate, error) {
	chainID := chain.ChainID()
	tx, err := s.tronPost(ctx, chain, "/wallet/gettransactionbyid", map[string]string{"value": hash})
	if err != nil {
		return nil, err
	}
	if len(tx) == 0 || string(tx) == "{}" {
		return nil, ErrTransactionNotFound
	}
	info, err := s.tronPost(ctx, chain, "/wallet/gettransactioninfobyid", map[string]string{"value": hash})
	if err != nil {
		return nil, err
	}
	native, ok := s.Registry.GetNative(chainID)
	if !ok {
		return nil, errors.New("native tron asset is not registered")
	}

	var txObj tronTx
	if err := json.Unmarshal(tx, &txObj); err != nil {
		return nil, fmt.Errorf("decode TRON transaction: %w", err)
	}
	var infoObj tronInfo
	if err := json.Unmarshal(info, &infoObj); err != nil {
		return nil, fmt.Errorf("decode TRON transaction info: %w", err)
	}
	if infoObj.InternalTransactions == nil {
		return nil, fmt.Errorf("%w: TRON transaction info omitted internal_transactions completeness evidence", ErrIncompleteProviderResponse)
	}
	if strings.TrimSpace(txObj.TxID) == "" || !strings.EqualFold(strings.TrimSpace(txObj.TxID), strings.TrimSpace(hash)) {
		return nil, fmt.Errorf("%w: TRON transaction id %q does not match requested id %q", ErrIncompleteProviderResponse, txObj.TxID, hash)
	}
	if strings.TrimSpace(infoObj.ID) == "" || !strings.EqualFold(strings.TrimSpace(infoObj.ID), strings.TrimSpace(hash)) {
		return nil, fmt.Errorf("%w: TRON transaction-info id %q does not match requested id %q", ErrIncompleteProviderResponse, infoObj.ID, hash)
	}
	if infoObj.BlockNumber <= 0 {
		return nil, fmt.Errorf("%w: TRON transaction %s has no confirmed block number", ErrIncompleteProviderResponse, hash)
	}
	blockID, err := s.tronBlockID(ctx, chain, infoObj.BlockNumber, txObj.TxID)
	if err != nil {
		return nil, err
	}

	blockNumber := fmt.Sprintf("%d", infoObj.BlockNumber)
	blockHash := blockID
	status := "confirmed"
	executionOutcomeKnown := false
	for _, result := range txObj.Ret {
		switch strings.ToUpper(strings.TrimSpace(result.ContractRet)) {
		case "SUCCESS":
			executionOutcomeKnown = true
		case "FAILED", "REVERT", "OUT_OF_ENERGY", "OUT_OF_TIME", "BAD_JUMP_DESTINATION", "OUT_OF_MEMORY", "ILLEGAL_OPERATION", "STACK_TOO_SMALL", "STACK_TOO_LARGE", "JVM_STACK_OVER_FLOW", "UNKNOWN":
			executionOutcomeKnown = true
			status = "failed"
		}
	}
	for _, result := range []string{infoObj.Receipt.Result, infoObj.Result} {
		switch strings.ToUpper(strings.TrimSpace(result)) {
		case "SUCCESS":
			executionOutcomeKnown = true
		case "FAILED", "REVERT":
			executionOutcomeKnown = true
			status = "failed"
		}
	}
	if !executionOutcomeKnown {
		return nil, fmt.Errorf("%w: TRON transaction %s omitted execution outcome", ErrIncompleteProviderResponse, hash)
	}
	confirmationsRequired := s.confirmationsRequired(chainID)
	confirmations := uint(0)
	if strings.EqualFold(status, "confirmed") {
		latest, err := s.tronLatestBlockNumber(ctx, chain)
		if err != nil {
			return nil, fmt.Errorf("load latest TRON block for finality: %w", err)
		}
		if latest < infoObj.BlockNumber {
			return nil, fmt.Errorf("%w: latest TRON block %d is behind transaction block %d", ErrIncompleteProviderResponse, latest, infoObj.BlockNumber)
		}
		confirmations = uint(latest - infoObj.BlockNumber + 1)
	}
	memo := tronRawDataMemo(txObj.RawData.Data)
	var events []eventCandidate
	for idx, contract := range txObj.RawData.Contract {
		if contract.Type != "TransferContract" {
			continue
		}
		if contract.Parameter.Value.Amount <= 0 || !validTronHexAddress(contract.Parameter.Value.OwnerAddress) || !validTronHexAddress(contract.Parameter.Value.ToAddress) {
			return nil, fmt.Errorf("%w: TRON native transfer %d has invalid amount or addresses", ErrIncompleteProviderResponse, idx)
		}
		events = append(events, eventCandidate{Type: "native_transfer", Confirmations: confirmations, ConfirmationsRequired: confirmationsRequired, Tx: &types.TransactionParam{
			Context: ctx, ChainID: chainID, Symbol: helpers.StrPtr(native.GetSymbol()), Decimals: native.GetDecimals(),
			Hash: helpers.StrPtr(txObj.TxID), Block: helpers.StrPtr(blockNumber), BlockHash: helpers.StrPtr(blockHash), Token: nil,
			From: helpers.StrPtr(tronHexToBase58(contract.Parameter.Value.OwnerAddress)), To: helpers.StrPtr(tronHexToBase58(contract.Parameter.Value.ToAddress)),
			Amount: helpers.StrPtr(fmt.Sprintf("%d", contract.Parameter.Value.Amount)), LogIndex: helpers.StrPtr(fmt.Sprintf("tx:%d", idx)), Status: helpers.StrPtr(status), Memo: optionalMemoPtr(memo),
		}})
	}
	for idx, logEntry := range infoObj.Log {
		if len(logEntry.Topics) == 0 || !strings.EqualFold(strings.TrimPrefix(logEntry.Topics[0], "0x"), strings.TrimPrefix(erc20TransferTopic, "0x")) {
			continue
		}
		if len(logEntry.Topics) < 3 || !validTronLogAddress(logEntry.Address) || !validTronTopicAddress(logEntry.Topics[1]) || !validTronTopicAddress(logEntry.Topics[2]) {
			return nil, fmt.Errorf("%w: TRON transfer log %d has invalid address topics", ErrIncompleteProviderResponse, idx)
		}
		token := tronHexToBase58(logEntry.Address)
		assetInfo, ok := s.Registry.Get(chainID, token)
		if !ok {
			continue
		}
		amount, ok := new(big.Int).SetString(strings.TrimPrefix(logEntry.Data, "0x"), 16)
		if !ok || amount.Sign() <= 0 {
			return nil, fmt.Errorf("%w: TRON transfer log %d has invalid amount", ErrIncompleteProviderResponse, idx)
		}
		events = append(events, eventCandidate{Type: "token_transfer", Confirmations: confirmations, ConfirmationsRequired: confirmationsRequired, Tx: &types.TransactionParam{
			Context: ctx, ChainID: chainID, Symbol: helpers.StrPtr(assetInfo.GetSymbol()), Decimals: assetInfo.GetDecimals(),
			Hash: helpers.StrPtr(txObj.TxID), Block: helpers.StrPtr(blockNumber), BlockHash: helpers.StrPtr(blockHash), Token: helpers.StrPtr(token),
			From: helpers.StrPtr(tronTopicToAddress(logEntry.Topics[1])), To: helpers.StrPtr(tronTopicToAddress(logEntry.Topics[2])),
			Amount: helpers.StrPtr(amount.String()), LogIndex: helpers.StrPtr(fmt.Sprintf("log:%d", idx)), Status: helpers.StrPtr(status), Memo: optionalMemoPtr(memo),
		}})
	}
	seenInternalHashes := make(map[string]struct{}, len(infoObj.InternalTransactions))
	for internalIndex, internal := range infoObj.InternalTransactions {
		if internal.Rejected {
			continue
		}
		note, err := tronInternalTransactionNote(internal.Note)
		if err != nil {
			return nil, fmt.Errorf("%w: TRON internal transaction %d note: %v", ErrIncompleteProviderResponse, internalIndex, err)
		}
		switch note {
		case "call", "create", "suicide":
		case "":
			for _, value := range internal.CallValueInfo {
				if value.CallValue > 0 && strings.TrimSpace(value.TokenID) == "" {
					return nil, fmt.Errorf("%w: TRON internal transaction %d has positive native value without an instruction note", ErrIncompleteProviderResponse, internalIndex)
				}
			}
			continue
		default:
			// Resource, staking and delegation instructions are not native
			// merchant transfers even when callValueInfo is populated.
			continue
		}
		internalHash, err := tronInternalTransactionHash(internal.Hash)
		if err != nil {
			return nil, fmt.Errorf("%w: TRON internal transaction %d identity: %v", ErrIncompleteProviderResponse, internalIndex, err)
		}
		if _, duplicate := seenInternalHashes[internalHash]; duplicate {
			return nil, fmt.Errorf("%w: TRON transaction contains duplicate internal identity %s", ErrIncompleteProviderResponse, internalHash)
		}
		seenInternalHashes[internalHash] = struct{}{}
		if !validTronHexAddress(internal.CallerAddress) || !validTronHexAddress(internal.TransferToAddress) {
			return nil, fmt.Errorf("%w: TRON internal transaction %d contains malformed account addresses", ErrIncompleteProviderResponse, internalIndex)
		}
		nativeValueCount := 0
		for _, value := range internal.CallValueInfo {
			if value.CallValue <= 0 || strings.TrimSpace(value.TokenID) != "" {
				continue
			}
			nativeValueCount++
			if nativeValueCount > 1 {
				return nil, fmt.Errorf("%w: TRON internal transaction %d has ambiguous multiple native values", ErrIncompleteProviderResponse, internalIndex)
			}
			events = append(events, eventCandidate{Type: "internal_transfer", Confirmations: confirmations, ConfirmationsRequired: confirmationsRequired, Tx: &types.TransactionParam{
				Context: ctx, ChainID: chainID, Symbol: helpers.StrPtr(native.GetSymbol()), Decimals: native.GetDecimals(),
				Hash: helpers.StrPtr(txObj.TxID), Block: helpers.StrPtr(blockNumber), BlockHash: helpers.StrPtr(blockHash), Token: nil,
				From: helpers.StrPtr(tronHexToBase58(internal.CallerAddress)), To: helpers.StrPtr(tronHexToBase58(internal.TransferToAddress)),
				Amount: helpers.StrPtr(strconv.FormatInt(value.CallValue, 10)), LogIndex: helpers.StrPtr("internal:" + internalHash + ":trx"), Status: helpers.StrPtr(status), Memo: optionalMemoPtr(memo),
			}})
		}
	}
	return events, nil
}

func tronInternalTransactionNote(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(raw, "0x"))
	if err == nil {
		if !utf8.Valid(decoded) {
			return "", errors.New("hex note is not valid UTF-8")
		}
		return strings.ToLower(strings.TrimSpace(string(decoded))), nil
	}
	if !utf8.ValidString(raw) {
		return "", errors.New("note is not valid UTF-8")
	}
	return strings.ToLower(raw), nil
}

func tronInternalTransactionHash(raw string) (string, error) {
	raw = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(raw, "0x")))
	decoded, err := hex.DecodeString(raw)
	if err != nil || len(decoded) != 32 {
		return "", fmt.Errorf("hash must be exactly 32 bytes")
	}
	return raw, nil
}

func (s *Service) tronBlockID(ctx context.Context, chain blockchain.Chain, blockNumber int64, txID string) (string, error) {
	body, err := s.tronPost(ctx, chain, "/wallet/getblockbynum", map[string]int64{"num": blockNumber})
	if err != nil {
		return "", fmt.Errorf("load TRON block %d identity: %w", blockNumber, err)
	}
	var block tronBlock
	if err := json.Unmarshal(body, &block); err != nil {
		return "", fmt.Errorf("decode TRON block %d identity: %w", blockNumber, err)
	}
	if block.BlockHeader.RawData.Number != blockNumber || strings.TrimSpace(block.BlockID) == "" {
		return "", fmt.Errorf("%w: TRON block response claims number %d and id %q, want %d", ErrIncompleteProviderResponse, block.BlockHeader.RawData.Number, block.BlockID, blockNumber)
	}
	for _, transaction := range block.Transactions {
		if strings.EqualFold(strings.TrimSpace(transaction.TxID), strings.TrimSpace(txID)) {
			return block.BlockID, nil
		}
	}
	return "", fmt.Errorf("%w: TRON block %d does not contain transaction %s", ErrIncompleteProviderResponse, blockNumber, txID)
}

func (s *Service) confirmationsRequired(chainID constants.ChainID) uint {
	if s != nil && s.Confirmations != nil {
		if required := s.Confirmations(chainID); required > 0 {
			return required
		}
	}
	return 1
}

func (s *Service) tronLatestBlockNumber(ctx context.Context, chain blockchain.Chain) (int64, error) {
	body, err := s.tronPost(ctx, chain, "/wallet/getnowblock", map[string]any{})
	if err != nil {
		return 0, err
	}
	var result struct {
		BlockHeader struct {
			RawData struct {
				Number int64 `json:"number"`
			} `json:"raw_data"`
		} `json:"block_header"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, err
	}
	if result.BlockHeader.RawData.Number <= 0 {
		return 0, ErrTransactionNotFound
	}
	return result.BlockHeader.RawData.Number, nil
}

func (s *Service) tronPost(ctx context.Context, chain blockchain.Chain, path string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	endpoints := tronHTTPEndpointsForChain(chain.Name(), chain.RPCs())
	var lastErr error
	attempted := 0
	notFound := 0
	for _, baseURL := range endpoints {
		baseURL = strings.TrimSpace(baseURL)
		if baseURL == "" {
			continue
		}
		attempted++

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+path, bytes.NewReader(body))
		if err != nil {
			lastErr = fmt.Errorf("%s %s request build failed: %w", baseURL, path, err)
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		if apiKey := strings.TrimSpace(os.Getenv("TRON_PRO_API_KEY")); apiKey != "" {
			req.Header.Set("TRON-PRO-API-KEY", apiKey)
		}
		resp, err := s.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("%s %s request failed: %w", baseURL, path, err)
			continue
		}
		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("%s %s response read failed: %w", baseURL, path, readErr)
			continue
		}
		if resp.StatusCode == http.StatusNotFound {
			notFound++
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("%s %s returned HTTP %d: %s", baseURL, path, resp.StatusCode, string(respBody))
			continue
		}
		trimmed := bytes.TrimSpace(respBody)
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("{}")) || bytes.Equal(trimmed, []byte("null")) {
			notFound++
			continue
		}
		return respBody, nil
	}
	if attempted > 0 && notFound == attempted {
		return nil, ErrTransactionNotFound
	}
	if lastErr == nil {
		if notFound > 0 {
			lastErr = fmt.Errorf("%w: TRON endpoint results were inconclusive for %s", ErrIncompleteProviderResponse, path)
		} else {
			lastErr = fmt.Errorf("no tron HTTP API endpoint configured")
		}
	}
	return nil, lastErr
}

func tronHTTPEndpoints() []string {
	return tronHTTPEndpointsForChain("tron", nil)
}

func tronHTTPEndpointsForChain(chainName string, rpcs []string) []string {
	if strings.EqualFold(strings.TrimSpace(chainName), "tron-testnet") {
		raw := strings.TrimSpace(os.Getenv("TRON_TESTNET_HTTP_ENDPOINTS"))
		if raw == "" {
			raw = strings.TrimSpace(os.Getenv("TRON_TESTNET_HTTP_ENDPOINT"))
		}
		if raw == "" {
			raw = strings.Join(rpcs, ",")
		}
		if raw == "" {
			return []string{"https://nile.trongrid.io"}
		}
		return splitTronHTTPEndpoints(raw)
	}

	raw := strings.TrimSpace(os.Getenv("TRON_HTTP_ENDPOINTS"))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("TRON_HTTP_ENDPOINT"))
	}
	if raw == "" {
		raw = strings.Join(rpcs, ",")
	}
	if raw == "" {
		return []string{"https://api.trongrid.io"}
	}
	return splitTronHTTPEndpoints(raw)
}

func splitTronHTTPEndpoints(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		value := strings.TrimSuffix(strings.TrimSpace(part), "/")
		value = strings.TrimSuffix(value, "/jsonrpc")
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func receiptStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "0x0", "0", "failed":
		return models.TransactionStatusFailed
	case "0x1", "1", "confirmed":
		return models.TransactionStatusConfirmed
	default:
		return ""
	}
}

func hexToDec(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.TrimPrefix(value, "0x")
	if value == "" {
		return "0"
	}
	parsed, ok := new(big.Int).SetString(value, 16)
	if !ok {
		return ""
	}
	return parsed.String()
}

func hexToBig(value string) *big.Int {
	value = strings.TrimSpace(strings.TrimPrefix(value, "0x"))
	if value == "" {
		return big.NewInt(0)
	}
	parsed, ok := new(big.Int).SetString(value, 16)
	if !ok {
		return big.NewInt(0)
	}
	return parsed
}

func parseHexBigStrict(value string) (*big.Int, error) {
	original := strings.TrimSpace(value)
	if original == "" {
		return nil, errors.New("hex value is empty")
	}
	value = strings.TrimPrefix(strings.ToLower(original), "0x")
	if value == "" {
		return nil, fmt.Errorf("hex value %q has no digits", original)
	}
	parsed, ok := new(big.Int).SetString(value, 16)
	if !ok {
		return nil, fmt.Errorf("hex value %q is invalid", original)
	}
	return parsed, nil
}

func positiveRawAmount(value string) bool {
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

func optionalHexBigString(value string) *string {
	if value == "" {
		return nil
	}
	converted := hexToBig(value).String()
	return &converted
}

func isContractInput(input string) bool {
	input = strings.ToLower(strings.TrimSpace(input))
	return input != "" && input != "0x" && input != "0x0"
}

func topicToEVMAddress(topic string) string {
	return common.BytesToAddress(common.HexToHash(topic).Bytes()[12:]).Hex()
}

func tronHexToBase58(value string) string {
	value = strings.TrimPrefix(strings.TrimSpace(value), "0x")
	raw, err := hex.DecodeString(value)
	if err != nil {
		return value
	}
	address, err := addrutil.TronAddressFromHash(raw)
	if err == nil {
		return address
	}
	return value
}

func validTronHexAddress(value string) bool {
	raw, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(value), "0x"))
	return err == nil && len(raw) == 21 && raw[0] == 0x41
}

func validTronLogAddress(value string) bool {
	raw, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(value), "0x"))
	if err != nil {
		return false
	}
	return len(raw) == 20 || (len(raw) == 21 && raw[0] == 0x41)
}

func validTronTopicAddress(value string) bool {
	raw, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(value), "0x"))
	return err == nil && len(raw) == 32
}

func tronTopicToAddress(topic string) string {
	raw, err := hex.DecodeString(strings.TrimPrefix(topic, "0x"))
	if err != nil {
		return topic
	}
	if len(raw) >= 20 {
		return tronHexToBase58("41" + hex.EncodeToString(raw[len(raw)-20:]))
	}
	return tronHexToBase58(hex.EncodeToString(raw))
}

func rawString(info map[string]json.RawMessage, key string) string {
	raw, ok := info[key]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		return fmt.Sprintf("%.0f", v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func rawUint8(info map[string]json.RawMessage, key string) uint8 {
	value := rawString(info, key)
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseUint(value, 10, 8)
	if err != nil {
		return 0
	}
	return uint8(parsed)
}

func parseSolanaInstruction(ix solanaInstruction) (solanaParsedInstruction, bool) {
	if len(ix.Parsed) == 0 || string(ix.Parsed) == "null" {
		return solanaParsedInstruction{}, false
	}
	var parsed solanaParsedInstruction
	if err := json.Unmarshal(ix.Parsed, &parsed); err != nil {
		return solanaParsedInstruction{}, false
	}
	return parsed, true
}

func solanaTransactionMemo(instructions []solanaInstruction, innerGroups []solanaInnerInstructions) string {
	for _, instruction := range instructions {
		if memo := solanaInstructionMemo(instruction); memo != "" {
			return memo
		}
	}
	for _, group := range innerGroups {
		for _, instruction := range group.Instructions {
			if memo := solanaInstructionMemo(instruction); memo != "" {
				return memo
			}
		}
	}
	return ""
}

func solanaInstructionMemo(instruction solanaInstruction) string {
	if !isSolanaMemoInstruction(instruction) {
		return ""
	}
	return solanaParsedMemo(instruction.Parsed)
}

func isSolanaMemoInstruction(instruction solanaInstruction) bool {
	programID := strings.TrimSpace(instruction.ProgramID)
	if programID == solanaMemoProgram || programID == solanaMemoProgramOld {
		return true
	}
	lowerProgram := strings.ToLower(strings.TrimSpace(instruction.Program))
	lowerProgramID := strings.ToLower(programID)
	return strings.Contains(lowerProgram, "memo") || strings.Contains(lowerProgramID, "memo")
}

func solanaParsedMemo(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}

	if parsed, ok := parseSolanaInstruction(solanaInstruction{Parsed: raw}); ok && parsed.Info != nil {
		if memo := firstRawString(parsed.Info, "memo", "data", "text"); memo != "" {
			return memo
		}
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return ""
	}
	if memo := firstRawString(object, "memo", "data", "text"); memo != "" {
		return memo
	}
	if infoRaw, ok := object["info"]; ok {
		var info map[string]json.RawMessage
		if err := json.Unmarshal(infoRaw, &info); err == nil {
			return firstRawString(info, "memo", "data", "text")
		}
	}
	return ""
}

func firstRawString(info map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(rawString(info, key)); value != "" {
			return value
		}
	}
	return ""
}

func bitcoinTxMemo(tx btcTx) string {
	for _, output := range tx.Vout {
		if memo := bitcoinOutputMemo(output); memo != "" {
			return memo
		}
	}
	return ""
}

func bitcoinOutputMemo(output btcVout) string {
	outputType := strings.ToLower(strings.TrimSpace(output.ScriptPubKeyType))
	asm := strings.TrimSpace(output.ScriptPubKeyASM)
	if outputType != "op_return" && !strings.HasPrefix(strings.ToUpper(asm), "OP_RETURN") {
		return ""
	}
	if memo := bitcoinASMOpReturnMemo(asm); memo != "" {
		return memo
	}
	return bitcoinScriptOpReturnMemo(output.ScriptPubKey)
}

func bitcoinASMOpReturnMemo(asm string) string {
	parts := strings.Fields(asm)
	if len(parts) < 2 || strings.ToUpper(parts[0]) != "OP_RETURN" {
		return ""
	}
	var payload []byte
	for _, part := range parts[1:] {
		if strings.HasPrefix(strings.ToUpper(part), "OP_PUSHBYTES_") {
			continue
		}
		decoded, err := hex.DecodeString(strings.TrimPrefix(part, "0x"))
		if err != nil {
			continue
		}
		payload = append(payload, decoded...)
	}
	return readableMemoBytes(payload)
}

func bitcoinScriptOpReturnMemo(scriptHex string) string {
	scriptHex = strings.TrimPrefix(strings.TrimSpace(scriptHex), "0x")
	script, err := hex.DecodeString(scriptHex)
	if err != nil || len(script) < 2 || script[0] != 0x6a {
		return ""
	}

	offset := 1
	var payload []byte
	for offset < len(script) {
		opcode := script[offset]
		offset++
		if opcode == 0x4c {
			if offset >= len(script) {
				return ""
			}
			opcode = script[offset]
			offset++
		}
		if opcode == 0 || opcode > 0x4b {
			return ""
		}
		size := int(opcode)
		if offset+size > len(script) {
			return ""
		}
		payload = append(payload, script[offset:offset+size]...)
		offset += size
	}
	return readableMemoBytes(payload)
}

func tronRawDataMemo(data string) string {
	data = strings.TrimSpace(data)
	if data == "" {
		return ""
	}
	if decoded, err := hex.DecodeString(strings.TrimPrefix(data, "0x")); err == nil {
		if memo := readableMemoBytes(decoded); memo != "" {
			return memo
		}
	}
	return readableMemoBytes([]byte(data))
}

func readableMemoBytes(payload []byte) string {
	if len(payload) == 0 || !utf8.Valid(payload) {
		return ""
	}
	memo := strings.TrimSpace(string(payload))
	if memo == "" {
		return ""
	}
	for _, r := range memo {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			return ""
		}
	}
	return memo
}

func optionalMemoPtr(memo string) *string {
	memo = strings.TrimSpace(memo)
	if memo == "" {
		return nil
	}
	return helpers.StrPtr(memo)
}

func solanaTransactionAccountKeys(rawKeys []json.RawMessage, loaded solanaLoadedAddresses, expectedCount int) ([]solanaAccountKey, error) {
	if rawKeys == nil {
		return nil, errors.New("message is missing accountKeys")
	}
	if expectedCount <= 0 {
		return nil, fmt.Errorf("invalid balance account count %d", expectedCount)
	}

	keys := make([]solanaAccountKey, 0, len(rawKeys)+len(loaded.Writable)+len(loaded.Readonly))
	seen := make(map[string]struct{}, cap(keys))
	for index, rawKey := range rawKeys {
		key, err := parseSolanaAccountKey(rawKey)
		if err != nil {
			return nil, fmt.Errorf("accountKeys[%d]: %w", index, err)
		}
		if _, duplicate := seen[key.Pubkey]; duplicate {
			return nil, fmt.Errorf("accountKeys[%d] duplicates pubkey %s", index, key.Pubkey)
		}
		seen[key.Pubkey] = struct{}{}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil, errors.New("message contains no account keys")
	}

	loadedKeys := make([]solanaAccountKey, 0, len(loaded.Writable)+len(loaded.Readonly))
	appendLoaded := func(address string, writable bool, index int) error {
		address = strings.TrimSpace(address)
		if address == "" {
			kind := "readonly"
			if writable {
				kind = "writable"
			}
			return fmt.Errorf("loadedAddresses.%s[%d] is empty", kind, index)
		}
		loadedKeys = append(loadedKeys, solanaAccountKey{Pubkey: address, Source: "lookupTable"})
		return nil
	}
	for index, address := range loaded.Writable {
		if err := appendLoaded(address, true, index); err != nil {
			return nil, err
		}
	}
	for index, address := range loaded.Readonly {
		if err := appendLoaded(address, false, index); err != nil {
			return nil, err
		}
	}

	switch {
	case expectedCount == len(keys):
		// jsonParsed normally includes lookup-table keys in message.accountKeys.
		// Providers that also return loadedAddresses must agree with that tail.
		if len(loadedKeys) > len(keys) {
			return nil, fmt.Errorf(
				"loaded address count %d exceeds complete account key count %d",
				len(loadedKeys),
				len(keys),
			)
		}
		tailStart := len(keys) - len(loadedKeys)
		for index, loadedKey := range loadedKeys {
			if keys[tailStart+index].Pubkey != loadedKey.Pubkey {
				return nil, fmt.Errorf(
					"loaded address %d (%s) does not match accountKeys[%d] (%s)",
					index,
					loadedKey.Pubkey,
					tailStart+index,
					keys[tailStart+index].Pubkey,
				)
			}
		}
		return keys, nil

	case expectedCount == len(keys)+len(loadedKeys):
		// Raw message encodings expose static keys in the message and append
		// loaded writable then loaded readonly keys from transaction metadata.
		for index, loadedKey := range loadedKeys {
			if _, duplicate := seen[loadedKey.Pubkey]; duplicate {
				return nil, fmt.Errorf("loaded address %d duplicates pubkey %s", index, loadedKey.Pubkey)
			}
			seen[loadedKey.Pubkey] = struct{}{}
			keys = append(keys, loadedKey)
		}
		return keys, nil

	default:
		return nil, fmt.Errorf(
			"account/balance length mismatch: static_or_parsed=%d loaded=%d balances=%d",
			len(keys),
			len(loadedKeys),
			expectedCount,
		)
	}
}

func parseSolanaAccountKey(rawKey json.RawMessage) (solanaAccountKey, error) {
	trimmed := bytes.TrimSpace(rawKey)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return solanaAccountKey{}, errors.New("account key is empty")
	}

	var keyString string
	if err := json.Unmarshal(rawKey, &keyString); err == nil {
		keyString = strings.TrimSpace(keyString)
		if keyString == "" {
			return solanaAccountKey{}, errors.New("account pubkey is empty")
		}
		return solanaAccountKey{Pubkey: keyString}, nil
	}

	var keyObject struct {
		Pubkey string `json:"pubkey"`
		Signer bool   `json:"signer"`
		Source string `json:"source"`
	}
	if err := json.Unmarshal(rawKey, &keyObject); err != nil {
		return solanaAccountKey{}, fmt.Errorf("decode account key: %w", err)
	}
	keyObject.Pubkey = strings.TrimSpace(keyObject.Pubkey)
	if keyObject.Pubkey == "" {
		return solanaAccountKey{}, errors.New("account pubkey is empty")
	}
	return solanaAccountKey{
		Pubkey: keyObject.Pubkey,
		Signer: keyObject.Signer,
		Source: strings.TrimSpace(keyObject.Source),
	}, nil
}

func solanaTokenAccountMetadataByAddress(rawKeys []json.RawMessage, meta solanaTxMeta) (map[string]solanaTokenAccountMetadata, []string) {
	accountKeys := indexedSolanaAccountKeys(rawKeys, meta.LoadedAddresses)
	accounts := make(map[string]solanaTokenAccountMetadata)
	invalid := make(map[string]struct{})
	warnings := make([]string, 0)

	merge := func(balance solanaTokenBalance) {
		index := int(balance.AccountIndex)
		if index >= len(accountKeys) {
			warnings = append(warnings, fmt.Sprintf("account_index=%d is out of range", balance.AccountIndex))
			return
		}
		address := strings.TrimSpace(accountKeys[index])
		if address == "" {
			warnings = append(warnings, fmt.Sprintf("account_index=%d has no parsed pubkey", balance.AccountIndex))
			return
		}
		if _, conflicted := invalid[address]; conflicted {
			return
		}

		next := solanaTokenAccountMetadata{
			Owner:     strings.TrimSpace(balance.Owner),
			Mint:      strings.TrimSpace(balance.Mint),
			ProgramID: strings.TrimSpace(balance.ProgramID),
		}
		if balance.UITokenAmount != nil {
			next.Decimals = balance.UITokenAmount.Decimals
			next.HasDecimals = true
		}

		current, exists := accounts[address]
		if !exists {
			accounts[address] = next
			return
		}
		if solanaTokenAccountMetadataConflicts(current, next) {
			delete(accounts, address)
			invalid[address] = struct{}{}
			warnings = append(warnings, fmt.Sprintf("token account %s has conflicting pre/post metadata", address))
			return
		}
		accounts[address] = mergeSolanaTokenAccountMetadata(current, next)
	}

	for _, balance := range meta.PreTokenBalances {
		merge(balance)
	}
	for _, balance := range meta.PostTokenBalances {
		merge(balance)
	}
	for address, account := range accounts {
		if account.Owner == "" || account.Mint == "" || !account.HasDecimals {
			delete(accounts, address)
			warnings = append(warnings, fmt.Sprintf("token account %s is missing owner, mint, or decimals", address))
		}
	}
	return accounts, warnings
}

func indexedSolanaAccountKeys(rawKeys []json.RawMessage, loaded solanaLoadedAddresses) []string {
	keys := make([]string, len(rawKeys), len(rawKeys)+len(loaded.Writable)+len(loaded.Readonly))
	for index, rawKey := range rawKeys {
		var keyString string
		if err := json.Unmarshal(rawKey, &keyString); err == nil {
			keys[index] = strings.TrimSpace(keyString)
			continue
		}
		var keyObject struct {
			Pubkey string `json:"pubkey"`
		}
		if err := json.Unmarshal(rawKey, &keyObject); err == nil {
			keys[index] = strings.TrimSpace(keyObject.Pubkey)
		}
	}
	for _, address := range loaded.Writable {
		keys = append(keys, strings.TrimSpace(address))
	}
	for _, address := range loaded.Readonly {
		keys = append(keys, strings.TrimSpace(address))
	}
	return keys
}

func solanaTokenAccountMetadataConflicts(current, next solanaTokenAccountMetadata) bool {
	return conflictingSolanaMetadataString(current.Owner, next.Owner) ||
		conflictingSolanaMetadataString(current.Mint, next.Mint) ||
		conflictingSolanaMetadataString(current.ProgramID, next.ProgramID) ||
		(current.HasDecimals && next.HasDecimals && current.Decimals != next.Decimals)
}

func conflictingSolanaMetadataString(left, right string) bool {
	return left != "" && right != "" && left != right
}

func mergeSolanaTokenAccountMetadata(current, next solanaTokenAccountMetadata) solanaTokenAccountMetadata {
	if current.Owner == "" {
		current.Owner = next.Owner
	}
	if current.Mint == "" {
		current.Mint = next.Mint
	}
	if current.ProgramID == "" {
		current.ProgramID = next.ProgramID
	}
	if !current.HasDecimals && next.HasDecimals {
		current.Decimals = next.Decimals
		current.HasDecimals = true
	}
	return current
}

func firstSolanaSigner(keys []solanaAccountKey) string {
	for _, key := range keys {
		if key.Signer {
			return key.Pubkey
		}
	}
	return ""
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type evmTx struct {
	Hash             string `json:"hash"`
	From             string `json:"from"`
	To               string `json:"to"`
	Value            string `json:"value"`
	Input            string `json:"input"`
	BlockHash        string `json:"blockHash"`
	BlockNumber      string `json:"blockNumber"`
	TransactionIndex string `json:"transactionIndex"`
}

type evmBlock struct {
	Number       string   `json:"number"`
	Hash         string   `json:"hash"`
	ParentHash   string   `json:"parentHash"`
	Transactions []string `json:"transactions"`
}

type evmTrace struct {
	Type   string `json:"type"`
	Action struct {
		From          string `json:"from"`
		To            string `json:"to"`
		Value         string `json:"value"`
		Address       string `json:"address"`
		RefundAddress string `json:"refundAddress"`
		Balance       string `json:"balance"`
		CallType      string `json:"callType"`
	} `json:"action"`
	Result struct {
		Address string `json:"address"`
	} `json:"result"`
	Error           string `json:"error"`
	TransactionHash string `json:"transactionHash"`
	TraceAddress    []int  `json:"traceAddress"`
}

type evmReceipt struct {
	TransactionHash   string   `json:"transactionHash"`
	TransactionIndex  string   `json:"transactionIndex"`
	BlockNumber       string   `json:"blockNumber"`
	BlockHash         string   `json:"blockHash"`
	Status            string   `json:"status"`
	ContractAddress   string   `json:"contractAddress"`
	GasUsed           string   `json:"gasUsed"`
	EffectiveGasPrice string   `json:"effectiveGasPrice"`
	Logs              []evmLog `json:"logs"`
}

type evmLog struct {
	Address          string   `json:"address"`
	Topics           []string `json:"topics"`
	Data             string   `json:"data"`
	LogIndex         string   `json:"logIndex"`
	TransactionHash  string   `json:"transactionHash"`
	TransactionIndex string   `json:"transactionIndex"`
	BlockHash        string   `json:"blockHash"`
	BlockNumber      string   `json:"blockNumber"`
}

type btcTx struct {
	TxID   string     `json:"txid"`
	Status *btcStatus `json:"status"`
	Vin    []btcVin   `json:"vin"`
	Vout   []btcVout  `json:"vout"`
}

type btcStatus struct {
	BlockHeight int64  `json:"block_height"`
	BlockHash   string `json:"block_hash"`
	Confirmed   bool   `json:"confirmed"`
}

type btcVin struct {
	IsCoinbase bool        `json:"is_coinbase"`
	Prevout    *btcPrevout `json:"prevout"`
}

type btcPrevout struct {
	Address string `json:"scriptpubkey_address"`
}

type btcVout struct {
	Address          string `json:"scriptpubkey_address"`
	Value            int64  `json:"value"`
	ScriptPubKey     string `json:"scriptpubkey"`
	ScriptPubKeyASM  string `json:"scriptpubkey_asm"`
	ScriptPubKeyType string `json:"scriptpubkey_type"`
}

type solanaBlockTx struct {
	Slot        int64             `json:"slot"`
	Blockhash   string            `json:"blockhash"`
	Meta        *solanaTxMeta     `json:"meta"`
	Transaction solanaTransaction `json:"transaction"`
}

type solanaBlockIdentity struct {
	Blockhash  string   `json:"blockhash"`
	Signatures []string `json:"signatures"`
}

type solanaTxMeta struct {
	Err               json.RawMessage           `json:"err"`
	InnerInstructions []solanaInnerInstructions `json:"innerInstructions"`
	PreBalances       []uint64                  `json:"preBalances"`
	PostBalances      []uint64                  `json:"postBalances"`
	PreTokenBalances  []solanaTokenBalance      `json:"preTokenBalances"`
	PostTokenBalances []solanaTokenBalance      `json:"postTokenBalances"`
	LoadedAddresses   solanaLoadedAddresses     `json:"loadedAddresses"`
}

type solanaTokenBalance struct {
	AccountIndex  uint16               `json:"accountIndex"`
	Mint          string               `json:"mint"`
	Owner         string               `json:"owner"`
	ProgramID     string               `json:"programId"`
	UITokenAmount *solanaUITokenAmount `json:"uiTokenAmount"`
}

type solanaUITokenAmount struct {
	Amount   string `json:"amount"`
	Decimals uint8  `json:"decimals"`
}

type solanaLoadedAddresses struct {
	Writable []string `json:"writable"`
	Readonly []string `json:"readonly"`
}

type solanaTokenAccountMetadata struct {
	Owner       string
	Mint        string
	ProgramID   string
	Decimals    uint8
	HasDecimals bool
}

type solanaInnerInstructions struct {
	Index        int                 `json:"index"`
	Instructions []solanaInstruction `json:"instructions"`
}

type solanaTransaction struct {
	Signatures []string      `json:"signatures"`
	Message    solanaMessage `json:"message"`
}

type solanaMessage struct {
	AccountKeys  []json.RawMessage   `json:"accountKeys"`
	Instructions []solanaInstruction `json:"instructions"`
}

type solanaInstruction struct {
	Program   string          `json:"program"`
	ProgramID string          `json:"programId"`
	Parsed    json.RawMessage `json:"parsed"`
}

type solanaParsedInstruction struct {
	Type string                     `json:"type"`
	Info map[string]json.RawMessage `json:"info"`
}

type solanaAccountKey struct {
	Pubkey string
	Signer bool
	Source string
}

type tronTx struct {
	TxID string `json:"txID"`
	Ret  []struct {
		ContractRet string `json:"contractRet"`
	} `json:"ret"`
	RawData struct {
		Data     string `json:"data"`
		Contract []struct {
			Type      string `json:"type"`
			Parameter struct {
				Value struct {
					Amount       int64  `json:"amount"`
					OwnerAddress string `json:"owner_address"`
					ToAddress    string `json:"to_address"`
				} `json:"value"`
			} `json:"parameter"`
		} `json:"contract"`
	} `json:"raw_data"`
}

type tronInfo struct {
	ID                   string                    `json:"id"`
	BlockNumber          int64                     `json:"blockNumber"`
	Result               string                    `json:"result"`
	InternalTransactions []tronInternalTransaction `json:"internal_transactions"`
	Receipt              struct {
		Result string `json:"result"`
	} `json:"receipt"`
	Log []struct {
		Address string   `json:"address"`
		Topics  []string `json:"topics"`
		Data    string   `json:"data"`
	} `json:"log"`
}

type tronInternalTransaction struct {
	Hash              string                      `json:"hash"`
	CallerAddress     string                      `json:"caller_address"`
	TransferToAddress string                      `json:"transferTo_address"`
	CallValueInfo     []tronInternalCallValueInfo `json:"callValueInfo"`
	Note              string                      `json:"note"`
	Rejected          bool                        `json:"rejected"`
}

type tronInternalCallValueInfo struct {
	CallValue int64  `json:"callValue"`
	TokenID   string `json:"tokenId"`
}

type tronBlock struct {
	BlockID      string `json:"blockID"`
	Transactions []struct {
		TxID string `json:"txID"`
	} `json:"transactions"`
	BlockHeader struct {
		RawData struct {
			Number int64 `json:"number"`
		} `json:"raw_data"`
	} `json:"block_header"`
}
