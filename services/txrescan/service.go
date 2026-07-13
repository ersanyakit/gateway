package txrescan

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
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
	ErrUnsupportedChain          = errors.New("unsupported rescan chain")
	ErrTransactionNotFound       = errors.New("transaction not found on chain")
	ErrUnauthorizedTx            = errors.New("transaction does not belong to merchant")
	ErrHistoricalScannerRequired = errors.New("historical range scanner is required")
)

const (
	erc20TransferTopic   = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
	solanaMemoProgram    = "MemoSq4gqABAXKb96qnH8TysNcWxMyWCqXgDLGmfcHr"
	solanaMemoProgramOld = "Memo1UhkJRfHyvLMcVucJwxXeuD728EqVDDwQDxFMNo"
)

type Service struct {
	Chains          *blockchain.ChainFactory
	Registry        *asset.Registry
	Bus             *dispatcher.Dispatcher
	ChainFactRepo   *repositories.ChainFactRepo
	ChainStateRepo  *repositories.ChainStateRepo
	DepositRepo     *repositories.DepositRepo
	TransactionRepo *repositories.TransactionRepo
	WalletRepo      *repositories.WalletRepo
	PaymentRepo     *repositories.PaymentRepo
	LedgerRepo      *repositories.LedgerRepo
	Confirmations   depositsvc.ConfirmationRequirementFunc
	client          *http.Client
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

type HistoricalRangeRequest struct {
	ChainID constants.ChainID
	From    int64
	To      int64
	Scanner HistoricalRangeScanner
}

type HistoricalEvent struct {
	Type                  string
	Tx                    types.TransactionParam
	Confirmations         uint
	ConfirmationsRequired uint
}

type HistoricalRangeResult struct {
	ChainID      constants.ChainID `json:"chain_id"`
	From         int64             `json:"from"`
	To           int64             `json:"to"`
	Blocks       int               `json:"blocks"`
	Events       int               `json:"events"`
	UniqueHashes []string          `json:"unique_hashes"`
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

	result := &HistoricalRangeResult{ChainID: req.ChainID, From: req.From, To: req.To}
	for blockNumber := req.From; blockNumber <= req.To; blockNumber++ {
		events, err := req.Scanner.ScanBlock(ctx, req.ChainID, blockNumber)
		if err != nil {
			return result, err
		}
		result.Blocks++
		for _, event := range events {
			candidate := eventCandidate{
				Type:                  event.Type,
				Tx:                    &event.Tx,
				Confirmations:         event.Confirmations,
				ConfirmationsRequired: event.ConfirmationsRequired,
			}
			if candidate.Tx == nil {
				continue
			}
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
	}
	return result, nil
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
	return service.ProcessFact(ctx, fact)
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
	return depositsvc.New(depositsvc.Dependencies{
		AssetRegistry:   s.Registry,
		ChainFactRepo:   s.ChainFactRepo,
		ChainStateRepo:  s.ChainStateRepo,
		DepositRepo:     s.DepositRepo,
		WalletRepo:      s.WalletRepo,
		TransactionRepo: s.TransactionRepo,
		PaymentRepo:     s.PaymentRepo,
		LedgerRepo:      s.LedgerRepo,
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
	var receipt evmReceipt
	if err := s.evmRPC(ctx, chain, "eth_getTransactionReceipt", []any{hash}, &receipt); err != nil {
		return nil, err
	}
	if receipt.TransactionHash == "" || receipt.BlockNumber == "" {
		return nil, ErrTransactionNotFound
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
	blockHash := receipt.BlockHash
	txIndex := hexToDec(receipt.TransactionIndex)
	confirmationsRequired := s.confirmationsRequired(chain.ChainID())
	confirmations := uint(0)
	if strings.EqualFold(status, models.TransactionStatusConfirmed) {
		if parsedBlock, parseErr := strconv.ParseInt(blockNumber, 10, 64); parseErr == nil && parsedBlock > 0 {
			if latest, latestErr := s.evmLatestBlockNumber(ctx, chain); latestErr == nil && latest >= parsedBlock {
				confirmations = uint(latest - parsedBlock + 1)
			}
		}
	}
	to := tx.To
	if to == "" {
		to = receipt.ContractAddress
	}
	if to == "" {
		to = "0x0000000000000000000000000000000000000000"
	}
	value := hexToBig(tx.Value)

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
			Context:   context.Background(),
			ChainID:   chain.ChainID(),
			Symbol:    helpers.StrPtr(native.GetSymbol()),
			Decimals:  native.GetDecimals(),
			Hash:      helpers.StrPtr(tx.Hash),
			Block:     helpers.StrPtr(blockNumber),
			BlockHash: helpers.StrPtr(blockHash),
			Token:     nil,
			From:      helpers.StrPtr(strings.ToLower(tx.From)),
			To:        helpers.StrPtr(strings.ToLower(to)),
			Amount:    helpers.StrPtr(value.String()),
			LogIndex:  helpers.StrPtr("tx:" + txIndex),
			Status:    helpers.StrPtr(status),
			GasUsed:   optionalHexBigString(receipt.GasUsed),
			GasPrice:  optionalHexBigString(receipt.EffectiveGasPrice),
		},
	}}

	for _, entry := range receipt.Logs {
		if len(entry.Topics) < 3 || !strings.EqualFold(entry.Topics[0], erc20TransferTopic) {
			continue
		}
		token := common.HexToAddress(entry.Address).Hex()
		assetInfo, ok := s.Registry.Get(chain.ChainID(), token)
		if !ok {
			continue
		}
		amount := hexToBig(entry.Data)
		if amount.Sign() <= 0 {
			continue
		}
		events = append(events, eventCandidate{
			Type:                  "token_transfer",
			Confirmations:         confirmations,
			ConfirmationsRequired: confirmationsRequired,
			Tx: &types.TransactionParam{
				Context:   context.Background(),
				ChainID:   chain.ChainID(),
				Symbol:    helpers.StrPtr(assetInfo.GetSymbol()),
				Decimals:  assetInfo.GetDecimals(),
				Hash:      helpers.StrPtr(tx.Hash),
				Block:     helpers.StrPtr(blockNumber),
				BlockHash: helpers.StrPtr(blockHash),
				Token:     helpers.StrPtr(token),
				From:      helpers.StrPtr(strings.ToLower(topicToEVMAddress(entry.Topics[1]))),
				To:        helpers.StrPtr(strings.ToLower(topicToEVMAddress(entry.Topics[2]))),
				Amount:    helpers.StrPtr(amount.String()),
				LogIndex:  helpers.StrPtr("log:" + hexToDec(entry.LogIndex)),
				Status:    helpers.StrPtr(status),
				GasUsed:   optionalHexBigString(receipt.GasUsed),
				GasPrice:  optionalHexBigString(receipt.EffectiveGasPrice),
			},
		})
	}
	return events, nil
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
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": time.Now().UnixNano(), "method": method, "params": params})
	if err != nil {
		return err
	}
	var lastErr error
	for _, rpcURL := range chain.RPCs() {
		rpcURL = strings.TrimSpace(rpcURL)
		if rpcURL == "" {
			continue
		}

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
		if string(rpcResp.Result) == "null" {
			return nil
		}
		return json.Unmarshal(rpcResp.Result, out)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("%s has no RPC endpoint configured", chain.Name())
	}
	return lastErr
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
	native, ok := s.Registry.GetNative(chain.ChainID())
	if !ok {
		return nil, errors.New("native asset is not registered")
	}
	from := "coinbase"
	fromAddresses := make([]string, 0, len(tx.Vin))
	for _, input := range tx.Vin {
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
	memo := bitcoinTxMemo(tx)
	events := make([]eventCandidate, 0, len(tx.Vout))
	for idx, output := range tx.Vout {
		if output.Address == "" || output.Value <= 0 {
			continue
		}
		events = append(events, eventCandidate{
			Type: "utxo_transfer",
			Tx: &types.TransactionParam{
				Context:       context.Background(),
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

func (s *Service) bitcoinGet(ctx context.Context, chain blockchain.Chain, path string) ([]byte, error) {
	var lastErr error
	for _, baseURL := range chain.RPCs() {
		baseURL = strings.TrimSpace(baseURL)
		if baseURL == "" {
			continue
		}

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
			return nil, ErrTransactionNotFound
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("%s %s returned HTTP %d: %s", chain.Name(), baseURL, resp.StatusCode, string(body))
			continue
		}
		return body, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("bitcoin has no API endpoint configured")
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
	native, ok := s.Registry.GetNative(chain.ChainID())
	if !ok {
		return nil, errors.New("native asset is not registered")
	}
	blockNumber := fmt.Sprintf("%d", blockTx.Slot)
	blockHash := blockTx.Blockhash
	if blockHash == "" {
		blockHash = blockNumber
	}
	status := "confirmed"
	if len(blockTx.Meta.Err) > 0 && string(blockTx.Meta.Err) != "null" {
		status = "failed"
	}
	signer := firstSolanaSigner(parseSolanaKeys(blockTx.Transaction.Message.AccountKeys))
	if signer == "" && len(blockTx.Transaction.Message.AccountKeys) > 0 {
		keys := parseSolanaKeys(blockTx.Transaction.Message.AccountKeys)
		if len(keys) > 0 {
			signer = keys[0].Pubkey
		}
	}
	if signer == "" {
		signer = "unknown_signer"
	}

	memo := solanaTransactionMemo(blockTx.Transaction.Message.Instructions, blockTx.Meta.InnerInstructions)
	tokenAccounts, tokenBalanceWarnings := solanaTokenAccountMetadataByAddress(blockTx.Transaction.Message.AccountKeys, blockTx.Meta)
	if len(tokenBalanceWarnings) > 0 {
		log.Printf("[txrescan:solana] invalid token balance metadata tx=%s: %s\n", hash, strings.Join(tokenBalanceWarnings, "; "))
	}
	var events []eventCandidate
	for idx, ix := range blockTx.Transaction.Message.Instructions {
		events = append(events, s.solanaInstructionEvent(chain.ChainID(), blockNumber, blockHash, hash, fmt.Sprintf("ix:%d", idx), native, status, signer, memo, ix, tokenAccounts)...)
	}
	for _, group := range blockTx.Meta.InnerInstructions {
		for idx, ix := range group.Instructions {
			events = append(events, s.solanaInstructionEvent(chain.ChainID(), blockNumber, blockHash, hash, fmt.Sprintf("inner:%d:%d", group.Index, idx), native, status, signer, memo, ix, tokenAccounts)...)
		}
	}
	return events, nil
}

func (s *Service) solanaInstructionEvent(chainID constants.ChainID, blockNumber, blockHash, hash, logIndex string, native asset.Asset, status, signer, memo string, ix solanaInstruction, tokenAccounts map[string]solanaTokenAccountMetadata) []eventCandidate {
	parsed, ok := parseSolanaInstruction(ix)
	if ok {
		program := strings.ToLower(ix.Program)
		ixType := strings.ToLower(parsed.Type)
		if program == "system" && ixType == "transfer" {
			source := rawString(parsed.Info, "source")
			destination := rawString(parsed.Info, "destination")
			lamports := rawString(parsed.Info, "lamports")
			if source != "" && destination != "" && lamports != "" {
				if !positiveRawAmount(lamports) {
					return nil
				}
				return []eventCandidate{{Type: "sol_transfer", Tx: &types.TransactionParam{
					Context: context.Background(), ChainID: chainID, Symbol: helpers.StrPtr(native.GetSymbol()), Decimals: native.GetDecimals(),
					Hash: helpers.StrPtr(hash), Block: helpers.StrPtr(blockNumber), BlockHash: helpers.StrPtr(blockHash), From: helpers.StrPtr(source),
					To: helpers.StrPtr(destination), Amount: helpers.StrPtr(lamports), LogIndex: helpers.StrPtr(logIndex), Status: helpers.StrPtr(status), Memo: optionalMemoPtr(memo),
				}}}
			}
		}
		if strings.HasPrefix(program, "spl-token") && (ixType == "transfer" || ixType == "transferchecked") {
			source := rawString(parsed.Info, "source")
			destination := rawString(parsed.Info, "destination")
			mint := rawString(parsed.Info, "mint")
			amount := rawString(parsed.Info, "amount")
			if tokenAmountRaw, ok := parsed.Info["tokenAmount"]; ok {
				var tokenAmount map[string]json.RawMessage
				if err := json.Unmarshal(tokenAmountRaw, &tokenAmount); err == nil {
					if amount == "" {
						amount = rawString(tokenAmount, "amount")
					}
				}
			}
			if source == "" || destination == "" || amount == "" || !positiveRawAmount(amount) {
				return nil
			}

			sourceMetadata, sourceOK := tokenAccounts[source]
			destinationMetadata, destinationOK := tokenAccounts[destination]
			if !sourceOK || !destinationOK ||
				sourceMetadata.Owner == "" || destinationMetadata.Owner == "" ||
				sourceMetadata.Mint == "" || destinationMetadata.Mint == "" ||
				sourceMetadata.Mint != destinationMetadata.Mint ||
				!sourceMetadata.HasDecimals || !destinationMetadata.HasDecimals ||
				sourceMetadata.Decimals != destinationMetadata.Decimals {
				return nil
			}
			if mint == "" {
				mint = destinationMetadata.Mint
			}
			if mint != destinationMetadata.Mint {
				return nil
			}
			if sourceMetadata.ProgramID != "" && destinationMetadata.ProgramID != "" && sourceMetadata.ProgramID != destinationMetadata.ProgramID {
				return nil
			}
			instructionProgramID := strings.TrimSpace(ix.ProgramID)
			if instructionProgramID != "" {
				if sourceMetadata.ProgramID != "" && sourceMetadata.ProgramID != instructionProgramID {
					return nil
				}
				if destinationMetadata.ProgramID != "" && destinationMetadata.ProgramID != instructionProgramID {
					return nil
				}
			}
			if s.Registry == nil {
				return nil
			}
			assetInfo, ok := s.Registry.Get(chainID, mint)
			if !ok || assetInfo.GetDecimals() != destinationMetadata.Decimals {
				return nil
			}
			return []eventCandidate{{Type: "spl_transfer", Tx: &types.TransactionParam{
				Context: context.Background(), ChainID: chainID, Symbol: helpers.StrPtr(assetInfo.GetSymbol()), Decimals: assetInfo.GetDecimals(),
				Hash: helpers.StrPtr(hash), Block: helpers.StrPtr(blockNumber), BlockHash: helpers.StrPtr(blockHash), Token: helpers.StrPtr(mint),
				From: helpers.StrPtr(sourceMetadata.Owner), To: helpers.StrPtr(destinationMetadata.Owner), Amount: helpers.StrPtr(amount), LogIndex: helpers.StrPtr(logIndex), Status: helpers.StrPtr(status), Memo: optionalMemoPtr(memo),
			}}}
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
		Context: context.Background(), ChainID: chainID, Symbol: helpers.StrPtr(native.GetSymbol()), Decimals: native.GetDecimals(),
		Hash: helpers.StrPtr(hash), Block: helpers.StrPtr(blockNumber), BlockHash: helpers.StrPtr(blockHash),
		From: helpers.StrPtr(signer), To: helpers.StrPtr(programID), Amount: helpers.StrPtr("0"), LogIndex: helpers.StrPtr(logIndex), Status: helpers.StrPtr(status), Memo: optionalMemoPtr(memo),
	}}}
}

func (s *Service) solanaRPC(ctx context.Context, chain blockchain.Chain, method string, params []any, out any) error {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": time.Now().UnixNano(), "method": method, "params": params})
	if err != nil {
		return err
	}
	var lastErr error
	for _, rpcURL := range chain.RPCs() {
		rpcURL = strings.TrimSpace(rpcURL)
		if rpcURL == "" {
			continue
		}

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
		if string(rpcResp.Result) == "null" {
			return nil
		}
		return json.Unmarshal(rpcResp.Result, out)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no solana RPC endpoint configured")
	}
	return lastErr
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
		return nil, err
	}
	var infoObj tronInfo
	if err := json.Unmarshal(info, &infoObj); err != nil {
		return nil, fmt.Errorf("decode TRON transaction info: %w", err)
	}

	blockNumber := fmt.Sprintf("%d", infoObj.BlockNumber)
	blockHash := fmt.Sprintf("%d", infoObj.BlockNumber)
	status := "confirmed"
	if strings.EqualFold(infoObj.Receipt.Result, "FAILED") || strings.EqualFold(infoObj.Result, "FAILED") {
		status = "failed"
	}
	confirmationsRequired := s.confirmationsRequired(chainID)
	confirmations := uint(0)
	if strings.EqualFold(status, "confirmed") && infoObj.BlockNumber > 0 {
		if latest, err := s.tronLatestBlockNumber(ctx, chain); err == nil && latest >= infoObj.BlockNumber {
			confirmations = uint(latest - infoObj.BlockNumber + 1)
		}
	}
	memo := tronRawDataMemo(txObj.RawData.Data)
	var events []eventCandidate
	for idx, contract := range txObj.RawData.Contract {
		if contract.Type != "TransferContract" {
			continue
		}
		if contract.Parameter.Value.Amount <= 0 {
			continue
		}
		events = append(events, eventCandidate{Type: "native_transfer", Confirmations: confirmations, ConfirmationsRequired: confirmationsRequired, Tx: &types.TransactionParam{
			Context: context.Background(), ChainID: chainID, Symbol: helpers.StrPtr(native.GetSymbol()), Decimals: native.GetDecimals(),
			Hash: helpers.StrPtr(hash), Block: helpers.StrPtr(blockNumber), BlockHash: helpers.StrPtr(blockHash), Token: nil,
			From: helpers.StrPtr(tronHexToBase58(contract.Parameter.Value.OwnerAddress)), To: helpers.StrPtr(tronHexToBase58(contract.Parameter.Value.ToAddress)),
			Amount: helpers.StrPtr(fmt.Sprintf("%d", contract.Parameter.Value.Amount)), LogIndex: helpers.StrPtr(fmt.Sprintf("tx:%d", idx)), Status: helpers.StrPtr(status), Memo: optionalMemoPtr(memo),
		}})
	}
	for idx, logEntry := range infoObj.Log {
		if len(logEntry.Topics) < 3 || !strings.EqualFold(logEntry.Topics[0], strings.TrimPrefix(erc20TransferTopic, "0x")) {
			continue
		}
		token := tronHexToBase58(logEntry.Address)
		assetInfo, ok := s.Registry.Get(chainID, token)
		if !ok {
			continue
		}
		amount, ok := new(big.Int).SetString(strings.TrimPrefix(logEntry.Data, "0x"), 16)
		if !ok || amount.Sign() <= 0 {
			continue
		}
		events = append(events, eventCandidate{Type: "token_transfer", Confirmations: confirmations, ConfirmationsRequired: confirmationsRequired, Tx: &types.TransactionParam{
			Context: context.Background(), ChainID: chainID, Symbol: helpers.StrPtr(assetInfo.GetSymbol()), Decimals: assetInfo.GetDecimals(),
			Hash: helpers.StrPtr(hash), Block: helpers.StrPtr(blockNumber), BlockHash: helpers.StrPtr(blockHash), Token: helpers.StrPtr(token),
			From: helpers.StrPtr(tronTopicToAddress(logEntry.Topics[1])), To: helpers.StrPtr(tronTopicToAddress(logEntry.Topics[2])),
			Amount: helpers.StrPtr(amount.String()), LogIndex: helpers.StrPtr(fmt.Sprintf("log:%d", idx)), Status: helpers.StrPtr(status), Memo: optionalMemoPtr(memo),
		}})
	}
	return events, nil
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
	for _, baseURL := range endpoints {
		baseURL = strings.TrimSpace(baseURL)
		if baseURL == "" {
			continue
		}

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
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("%s %s returned HTTP %d: %s", baseURL, path, resp.StatusCode, string(respBody))
			continue
		}
		return respBody, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no tron HTTP API endpoint configured")
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

func parseSolanaKeys(rawKeys []json.RawMessage) []solanaAccountKey {
	keys := make([]solanaAccountKey, 0, len(rawKeys))
	for _, rawKey := range rawKeys {
		var keyString string
		if err := json.Unmarshal(rawKey, &keyString); err == nil && keyString != "" {
			keys = append(keys, solanaAccountKey{Pubkey: keyString})
			continue
		}
		var keyObject struct {
			Pubkey string `json:"pubkey"`
			Signer bool   `json:"signer"`
		}
		if err := json.Unmarshal(rawKey, &keyObject); err == nil && keyObject.Pubkey != "" {
			keys = append(keys, solanaAccountKey{Pubkey: keyObject.Pubkey, Signer: keyObject.Signer})
		}
	}
	return keys
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
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type evmTx struct {
	Hash  string `json:"hash"`
	From  string `json:"from"`
	To    string `json:"to"`
	Value string `json:"value"`
	Input string `json:"input"`
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
	Address  string   `json:"address"`
	Topics   []string `json:"topics"`
	Data     string   `json:"data"`
	LogIndex string   `json:"logIndex"`
}

type btcTx struct {
	TxID   string `json:"txid"`
	Status struct {
		BlockHeight int64  `json:"block_height"`
		BlockHash   string `json:"block_hash"`
		Confirmed   bool   `json:"confirmed"`
	} `json:"status"`
	Vin  []btcVin  `json:"vin"`
	Vout []btcVout `json:"vout"`
}

type btcVin struct {
	Prevout struct {
		Address string `json:"scriptpubkey_address"`
	} `json:"prevout"`
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
	Meta        solanaTxMeta      `json:"meta"`
	Transaction solanaTransaction `json:"transaction"`
}

type solanaTxMeta struct {
	Err               json.RawMessage           `json:"err"`
	InnerInstructions []solanaInnerInstructions `json:"innerInstructions"`
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
}

type tronTx struct {
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
	BlockNumber int64  `json:"blockNumber"`
	Result      string `json:"result"`
	Receipt     struct {
		Result string `json:"result"`
	} `json:"receipt"`
	Log []struct {
		Address string   `json:"address"`
		Topics  []string `json:"topics"`
		Data    string   `json:"data"`
	} `json:"log"`
}
