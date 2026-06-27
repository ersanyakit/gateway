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
	"strconv"
	"strings"
	"time"

	"core/asset"
	"core/blockchain"
	"core/constants"
	"core/helpers"
	"core/models"
	"core/repositories"
	depositsvc "core/services/deposits"
	"core/types"
	"core/workers/dispatcher"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/okx/go-wallet-sdk/crypto/base58"
	"gorm.io/gorm"
)

var (
	ErrUnsupportedChain    = errors.New("unsupported rescan chain")
	ErrTransactionNotFound = errors.New("transaction not found on chain")
	ErrUnauthorizedTx      = errors.New("transaction does not belong to merchant")
)

const (
	erc20TransferTopic = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
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
	case constants.TRON:
		events, err = s.scanTron(ctx, hash)
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
		if err := s.authorizeMerchantEvents(ctx, chainID, events, *merchantID); err != nil {
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
	}
	return result, nil
}

func (s *Service) recordRescanFact(ctx context.Context, candidate eventCandidate) (*models.ChainFact, error) {
	if s == nil || s.ChainFactRepo == nil || candidate.Tx == nil {
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

func (s *Service) depositProcessor() *depositsvc.Service {
	if s == nil ||
		s.ChainFactRepo == nil ||
		s.ChainStateRepo == nil ||
		s.DepositRepo == nil ||
		s.WalletRepo == nil ||
		s.TransactionRepo == nil {
		return nil
	}
	return depositsvc.New(depositsvc.Dependencies{
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
	if s.WalletRepo == nil {
		return errors.New("wallet repository is not ready")
	}
	for _, candidate := range events {
		if candidate.Tx == nil {
			continue
		}
		addresses := []string{}
		if candidate.Tx.To != nil {
			addresses = append(addresses, *candidate.Tx.To)
		}
		if candidate.Tx.From != nil {
			addresses = append(addresses, *candidate.Tx.From)
		}
		for _, address := range addresses {
			wallet, err := s.WalletRepo.FindByChainAddress(ctx, chainID, address)
			if err == nil && wallet.MerchantID == merchantID {
				return nil
			}
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
	}
	return ErrUnauthorizedTx
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
	blockNumber := hexToDec(receipt.BlockNumber)
	blockHash := receipt.BlockHash
	txIndex := hexToDec(receipt.TransactionIndex)
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
		Type: eventType,
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
		events = append(events, eventCandidate{
			Type: "token_transfer",
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
	for _, input := range tx.Vin {
		if input.Prevout.Address != "" {
			from = input.Prevout.Address
			break
		}
	}
	status := "confirmed"
	if !tx.Status.Confirmed {
		status = "pending"
	}
	block := fmt.Sprintf("%d", tx.Status.BlockHeight)
	events := make([]eventCandidate, 0, len(tx.Vout))
	for idx, output := range tx.Vout {
		if output.Address == "" || output.Value <= 0 {
			continue
		}
		events = append(events, eventCandidate{
			Type: "utxo_transfer",
			Tx: &types.TransactionParam{
				Context:   context.Background(),
				ChainID:   chain.ChainID(),
				Symbol:    helpers.StrPtr(native.GetSymbol()),
				Decimals:  native.GetDecimals(),
				Hash:      helpers.StrPtr(tx.TxID),
				Block:     helpers.StrPtr(block),
				BlockHash: helpers.StrPtr(tx.Status.BlockHash),
				Token:     nil,
				From:      helpers.StrPtr(from),
				To:        helpers.StrPtr(output.Address),
				Amount:    helpers.StrPtr(fmt.Sprintf("%d", output.Value)),
				LogIndex:  helpers.StrPtr(fmt.Sprintf("vout:%d", idx)),
				Status:    helpers.StrPtr(status),
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

	var events []eventCandidate
	for idx, ix := range blockTx.Transaction.Message.Instructions {
		events = append(events, s.solanaInstructionEvent(chain.ChainID(), blockNumber, blockHash, hash, fmt.Sprintf("ix:%d", idx), native, status, signer, ix)...)
	}
	for _, group := range blockTx.Meta.InnerInstructions {
		for idx, ix := range group.Instructions {
			events = append(events, s.solanaInstructionEvent(chain.ChainID(), blockNumber, blockHash, hash, fmt.Sprintf("inner:%d:%d", group.Index, idx), native, status, signer, ix)...)
		}
	}
	return events, nil
}

func (s *Service) solanaInstructionEvent(chainID constants.ChainID, blockNumber, blockHash, hash, logIndex string, native asset.Asset, status, signer string, ix solanaInstruction) []eventCandidate {
	parsed, ok := parseSolanaInstruction(ix)
	if ok {
		program := strings.ToLower(ix.Program)
		ixType := strings.ToLower(parsed.Type)
		if program == "system" && ixType == "transfer" {
			source := rawString(parsed.Info, "source")
			destination := rawString(parsed.Info, "destination")
			lamports := rawString(parsed.Info, "lamports")
			if source != "" && destination != "" && lamports != "" {
				return []eventCandidate{{Type: "sol_transfer", Tx: &types.TransactionParam{
					Context: context.Background(), ChainID: chainID, Symbol: helpers.StrPtr(native.GetSymbol()), Decimals: native.GetDecimals(),
					Hash: helpers.StrPtr(hash), Block: helpers.StrPtr(blockNumber), BlockHash: helpers.StrPtr(blockHash), From: helpers.StrPtr(source),
					To: helpers.StrPtr(destination), Amount: helpers.StrPtr(lamports), LogIndex: helpers.StrPtr(logIndex), Status: helpers.StrPtr(status),
				}}}
			}
		}
		if strings.HasPrefix(program, "spl-token") && (ixType == "transfer" || ixType == "transferchecked") {
			source := rawString(parsed.Info, "source")
			destination := rawString(parsed.Info, "destination")
			mint := rawString(parsed.Info, "mint")
			amount := rawString(parsed.Info, "amount")
			decimals := uint8(0)
			if tokenAmountRaw, ok := parsed.Info["tokenAmount"]; ok {
				var tokenAmount map[string]json.RawMessage
				if err := json.Unmarshal(tokenAmountRaw, &tokenAmount); err == nil {
					if amount == "" {
						amount = rawString(tokenAmount, "amount")
					}
					decimals = rawUint8(tokenAmount, "decimals")
				}
			}
			symbol := "SPL"
			if mint != "" && s.Registry != nil {
				if assetInfo, ok := s.Registry.Get(chainID, mint); ok {
					symbol = assetInfo.GetSymbol()
					decimals = assetInfo.GetDecimals()
				}
			}
			if source != "" && destination != "" && amount != "" {
				var token *string
				if mint != "" {
					token = helpers.StrPtr(mint)
				}
				return []eventCandidate{{Type: "spl_transfer", Tx: &types.TransactionParam{
					Context: context.Background(), ChainID: chainID, Symbol: helpers.StrPtr(symbol), Decimals: decimals,
					Hash: helpers.StrPtr(hash), Block: helpers.StrPtr(blockNumber), BlockHash: helpers.StrPtr(blockHash), Token: token,
					From: helpers.StrPtr(source), To: helpers.StrPtr(destination), Amount: helpers.StrPtr(amount), LogIndex: helpers.StrPtr(logIndex), Status: helpers.StrPtr(status),
				}}}
			}
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
		From: helpers.StrPtr(signer), To: helpers.StrPtr(programID), Amount: helpers.StrPtr("0"), LogIndex: helpers.StrPtr(logIndex), Status: helpers.StrPtr(status),
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

func (s *Service) scanTron(ctx context.Context, hash string) ([]eventCandidate, error) {
	tx, err := s.tronPost(ctx, "/wallet/gettransactionbyid", map[string]string{"value": hash})
	if err != nil {
		return nil, err
	}
	if len(tx) == 0 || string(tx) == "{}" {
		return nil, ErrTransactionNotFound
	}
	info, err := s.tronPost(ctx, "/wallet/gettransactioninfobyid", map[string]string{"value": hash})
	if err != nil {
		return nil, err
	}
	native, ok := s.Registry.GetNative(constants.TRON)
	if !ok {
		return nil, errors.New("native tron asset is not registered")
	}

	var txObj tronTx
	if err := json.Unmarshal(tx, &txObj); err != nil {
		return nil, err
	}
	var infoObj tronInfo
	_ = json.Unmarshal(info, &infoObj)

	blockNumber := fmt.Sprintf("%d", infoObj.BlockNumber)
	blockHash := fmt.Sprintf("%d", infoObj.BlockNumber)
	status := "confirmed"
	if strings.EqualFold(infoObj.Receipt.Result, "FAILED") || strings.EqualFold(infoObj.Result, "FAILED") {
		status = "failed"
	}
	confirmationsRequired := s.confirmationsRequired(constants.TRON)
	confirmations := uint(0)
	if strings.EqualFold(status, "confirmed") && infoObj.BlockNumber > 0 {
		if latest, err := s.tronLatestBlockNumber(ctx); err == nil && latest >= infoObj.BlockNumber {
			confirmations = uint(latest - infoObj.BlockNumber + 1)
		}
	}
	var events []eventCandidate
	for idx, contract := range txObj.RawData.Contract {
		if contract.Type != "TransferContract" {
			continue
		}
		if contract.Parameter.Value.Amount <= 0 {
			continue
		}
		events = append(events, eventCandidate{Type: "native_transfer", Confirmations: confirmations, ConfirmationsRequired: confirmationsRequired, Tx: &types.TransactionParam{
			Context: context.Background(), ChainID: constants.TRON, Symbol: helpers.StrPtr(native.GetSymbol()), Decimals: native.GetDecimals(),
			Hash: helpers.StrPtr(hash), Block: helpers.StrPtr(blockNumber), BlockHash: helpers.StrPtr(blockHash), Token: nil,
			From: helpers.StrPtr(tronHexToBase58(contract.Parameter.Value.OwnerAddress)), To: helpers.StrPtr(tronHexToBase58(contract.Parameter.Value.ToAddress)),
			Amount: helpers.StrPtr(fmt.Sprintf("%d", contract.Parameter.Value.Amount)), LogIndex: helpers.StrPtr(fmt.Sprintf("tx:%d", idx)), Status: helpers.StrPtr(status),
		}})
	}
	for idx, logEntry := range infoObj.Log {
		if len(logEntry.Topics) < 3 || !strings.EqualFold(logEntry.Topics[0], strings.TrimPrefix(erc20TransferTopic, "0x")) {
			continue
		}
		token := tronHexToBase58(logEntry.Address)
		assetInfo, ok := s.Registry.Get(constants.TRON, token)
		if !ok {
			continue
		}
		amount := new(big.Int)
		amount.SetString(strings.TrimPrefix(logEntry.Data, "0x"), 16)
		events = append(events, eventCandidate{Type: "token_transfer", Confirmations: confirmations, ConfirmationsRequired: confirmationsRequired, Tx: &types.TransactionParam{
			Context: context.Background(), ChainID: constants.TRON, Symbol: helpers.StrPtr(assetInfo.GetSymbol()), Decimals: assetInfo.GetDecimals(),
			Hash: helpers.StrPtr(hash), Block: helpers.StrPtr(blockNumber), BlockHash: helpers.StrPtr(blockHash), Token: helpers.StrPtr(token),
			From: helpers.StrPtr(tronTopicToAddress(logEntry.Topics[1])), To: helpers.StrPtr(tronTopicToAddress(logEntry.Topics[2])),
			Amount: helpers.StrPtr(amount.String()), LogIndex: helpers.StrPtr(fmt.Sprintf("log:%d", idx)), Status: helpers.StrPtr(status),
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

func (s *Service) tronLatestBlockNumber(ctx context.Context) (int64, error) {
	body, err := s.tronPost(ctx, "/wallet/getnowblock", map[string]any{})
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

func (s *Service) tronPost(ctx context.Context, path string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	endpoints := tronHTTPEndpoints()
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
	raw := strings.TrimSpace(os.Getenv("TRON_HTTP_ENDPOINTS"))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("TRON_HTTP_ENDPOINT"))
	}
	if raw == "" {
		return []string{"https://api.trongrid.io"}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func receiptStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "0x0", "0", "failed":
		return models.TransactionStatusFailed
	default:
		return models.TransactionStatusConfirmed
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
	if len(raw) == 20 {
		return base58.CheckEncode(raw, 0x41)
	}
	if len(raw) == 21 && raw[0] == 0x41 {
		return base58.CheckEncode(raw[1:], raw[0])
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
	Address string `json:"scriptpubkey_address"`
	Value   int64  `json:"value"`
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
