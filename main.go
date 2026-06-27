package main

import (
	"context"
	"core/api/routes"
	"core/blockchain"
	"core/constants"
	"core/models"
	depositsvc "core/services/deposits"
	"core/services/realtime"
	reconsvc "core/services/reconciliation"
	"core/services/txrescan"
	webhooksvc "core/services/webhook"
	"core/types"
	"core/workers/dispatcher"
	addressindex "core/workers/indexer"
	btcListener "core/workers/listeners/bitcoin"
	evmListener "core/workers/listeners/evm"
	solListener "core/workers/listeners/solana"
	tronListener "core/workers/listeners/tron"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/big"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	coreApplication "core/application"
	coreHelpers "core/helpers"
	"core/repositories"
	coreDB "core/services/database"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"gorm.io/gorm"
)

func ptrValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func envFlagEnabled(keys ...string) bool {
	for _, key := range keys {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
		case "1", "true", "yes", "on", "verbose":
			return true
		}
	}
	return false
}

func isPositiveAmount(value string) bool {
	amount, ok := new(big.Int).SetString(value, 10)
	return ok && amount.Sign() > 0
}

func webhookRetryInterval() time.Duration {
	raw := os.Getenv("WEBHOOK_RETRY_INTERVAL")
	if raw == "" {
		return 30 * time.Second
	}
	interval, err := time.ParseDuration(raw)
	if err != nil || interval <= 0 {
		return 30 * time.Second
	}
	return interval
}

func transactionFinalityInterval() time.Duration {
	raw := os.Getenv("TRANSACTION_FINALITY_INTERVAL")
	if raw == "" {
		return 20 * time.Second
	}
	interval, err := time.ParseDuration(raw)
	if err != nil || interval <= 0 {
		return 20 * time.Second
	}
	return interval
}

func depositFactInterval() time.Duration {
	raw := os.Getenv("DEPOSIT_FACT_INTERVAL")
	if raw == "" {
		return 10 * time.Second
	}
	interval, err := time.ParseDuration(raw)
	if err != nil || interval <= 0 {
		return 10 * time.Second
	}
	return interval
}

func sweepJobInterval() time.Duration {
	raw := os.Getenv("SWEEP_JOB_INTERVAL")
	if raw == "" {
		return 15 * time.Second
	}
	interval, err := time.ParseDuration(raw)
	if err != nil || interval <= 0 {
		return 15 * time.Second
	}
	return interval
}

func sweepJobLockTimeout() time.Duration {
	raw := os.Getenv("SWEEP_JOB_LOCK_TIMEOUT")
	if raw == "" {
		return 2 * time.Minute
	}
	timeout, err := time.ParseDuration(raw)
	if err != nil || timeout <= 0 {
		return 2 * time.Minute
	}
	return timeout
}

func sweepPrefundRetryAfter() time.Duration {
	raw := os.Getenv("SWEEP_PREFUND_RETRY_AFTER")
	if raw == "" {
		return 10 * time.Minute
	}
	interval, err := time.ParseDuration(raw)
	if err != nil || interval <= 0 {
		return 10 * time.Minute
	}
	return interval
}

func reconciliationInterval() time.Duration {
	raw := os.Getenv("RECONCILIATION_INTERVAL")
	if raw == "" {
		return 5 * time.Minute
	}
	interval, err := time.ParseDuration(raw)
	if err != nil || interval <= 0 {
		return 5 * time.Minute
	}
	return interval
}

func reserveReconciliationLimit() int {
	raw := os.Getenv("RESERVE_RECONCILIATION_LIMIT")
	if raw == "" {
		return 200
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return 200
	}
	if limit > 1000 {
		return 1000
	}
	return limit
}

func gatewayShutdownTimeout() time.Duration {
	raw := os.Getenv("GATEWAY_SHUTDOWN_TIMEOUT")
	if raw == "" {
		return 5 * time.Second
	}
	timeout, err := time.ParseDuration(raw)
	if err != nil || timeout <= 0 {
		return 5 * time.Second
	}
	return timeout
}

func chainConfirmationRequirement(chainID constants.ChainID) uint {
	envKeys := []string{fmt.Sprintf("CHAIN_%d_CONFIRMATIONS", chainID)}
	if chainName := constants.ChainName(chainID); chainName != "" {
		envKeys = append(envKeys, strings.ToUpper(strings.ReplaceAll(chainName, "-", "_"))+"_CONFIRMATIONS")
	}
	envKeys = append(envKeys, "FINALITY_CONFIRMATIONS_DEFAULT")
	for _, key := range envKeys {
		if key == "" {
			continue
		}
		if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
			value, err := strconv.ParseUint(raw, 10, 32)
			if err == nil && value > 0 {
				return uint(value)
			}
		}
	}
	switch chainID {
	case constants.Bitcoin:
		return 3
	case constants.Solana:
		return 1
	case constants.TRON:
		return 20
	default:
		return 12
	}
}

func transactionConfirmations(blockNumber string, state *models.ChainState) uint {
	block, err := strconv.ParseInt(strings.TrimSpace(blockNumber), 10, 64)
	if err != nil || block <= 0 || state == nil || state.LastProcessedBlock < block {
		return 0
	}
	confirmedHead := state.LastConfirmedBlock
	if confirmedHead < state.LastProcessedBlock {
		confirmedHead = state.LastProcessedBlock
	}
	if confirmedHead < block {
		return 0
	}
	return uint(confirmedHead - block + 1)
}

var addrIndex *addressindex.AddressIndex

func walletForAddress(ctx context.Context, chainID constants.ChainID, address string) (*models.Wallet, bool, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return nil, false, nil
	}

	if addrIndex != nil {
		if info, ok := addrIndex.Get(chainID, address); ok {
			wallet, err := coreApplication.CORE.Router.WalletRepo.FindByID(ctx, info.WalletID)
			if err == nil {
				return wallet, true, nil
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, false, err
			}
		}
	}

	wallet, err := coreApplication.CORE.Router.WalletRepo.FindByChainAddress(ctx, chainID, address)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if addrIndex != nil {
		addrIndex.Add(chainID, address, addressindex.WalletInfo{
			WalletID:   wallet.ID,
			MerchantID: wallet.MerchantID,
			DomainID:   wallet.DomainID,
			ProductID:  wallet.ProductID,
			UserID:     wallet.UserID,
		})
	}
	return wallet, true, nil
}

func transactionWalletMatch(ctx context.Context, txParam types.TransactionParam) (*models.Wallet, bool, bool, error) {
	if txParam.To != nil {
		wallet, ok, err := walletForAddress(ctx, txParam.ChainID, *txParam.To)
		if err != nil {
			return nil, false, false, err
		}
		if ok {
			return wallet, true, true, nil
		}
	}
	if txParam.From != nil {
		wallet, ok, err := walletForAddress(ctx, txParam.ChainID, *txParam.From)
		if err != nil {
			return nil, false, false, err
		}
		if ok {
			return wallet, false, true, nil
		}
	}
	return nil, false, false, nil
}

func handleChainIndexerEvent(ctx context.Context, event dispatcher.Event) error {
	if event.Transaction == nil {
		return nil
	}
	_, _, err := recordChainFactObservation(ctx, event.Type, *event.Transaction)
	return err
}

func recordChainFactObservation(ctx context.Context, eventType string, txParam types.TransactionParam) (*models.ChainFact, bool, error) {
	if coreApplication.CORE == nil || coreApplication.CORE.Router == nil || coreApplication.CORE.Router.ChainFactRepo == nil {
		return nil, false, errors.New("chain fact repository is not configured")
	}
	fact, err := repositories.BuildChainFact(repositories.ChainFactBuildParams{
		EventType:             eventType,
		Transaction:           txParam,
		ConfirmationsRequired: uint(chainConfirmationRequirement(txParam.ChainID)),
	})
	if err != nil {
		return nil, false, err
	}
	return coreApplication.CORE.Router.ChainFactRepo.Record(ctx, &fact)
}

func bindTransactionWallet(ctx context.Context, eventType string, txParam types.TransactionParam, wallet *models.Wallet) (*models.Transaction, error) {
	uniqueHash, err := coreApplication.CORE.Router.TransactionRepo.UniqueHash(txParam)
	if err != nil {
		return nil, err
	}
	return coreApplication.CORE.Router.TransactionRepo.BindWallet(ctx, uniqueHash, eventType, wallet)
}

func handleDepositWebhook(ctx context.Context, notifier *webhooksvc.Notifier, eventType string, txParam types.TransactionParam) (*models.Transaction, error) {
	if txParam.To == nil || txParam.Amount == nil || !isPositiveAmount(*txParam.Amount) {
		return nil, nil
	}

	wallet, ok, err := walletForAddress(ctx, txParam.ChainID, *txParam.To)
	if err != nil || !ok {
		return nil, err
	}

	uniqueHash, err := coreApplication.CORE.Router.TransactionRepo.UniqueHash(txParam)
	if err != nil {
		return nil, err
	}

	txModel, err := coreApplication.CORE.Router.TransactionRepo.BindWallet(ctx, uniqueHash, eventType, wallet)
	if err != nil {
		return nil, err
	}
	if coreApplication.CORE.Router.LedgerRepo != nil {
		if err := coreApplication.CORE.Router.LedgerRepo.CreateDepositPending(ctx, *txModel); err != nil {
			log.Println("Ledger pending deposit error:", err)
		}
	}
	if txModel.Status != models.TransactionStatusConfirmed || txModel.FinalizedAt == nil {
		return txModel, nil
	}
	if txModel.WebhookSentAt != nil {
		return txModel, nil
	}

	createTransactionWebhookDelivery(ctx, wallet.Domain, *txModel)

	enqueueSweepJob(ctx, txModel)

	return txModel, nil
}

func applyTransactionFinality(ctx context.Context, txParam types.TransactionParam) (*models.Transaction, error) {
	uniqueHash, err := coreApplication.CORE.Router.TransactionRepo.UniqueHash(txParam)
	if err != nil {
		return nil, err
	}
	if txParam.Status != nil && strings.EqualFold(*txParam.Status, models.TransactionStatusFailed) {
		return coreApplication.CORE.Router.TransactionRepo.MarkFailed(ctx, uniqueHash)
	}
	state, err := coreApplication.CORE.Router.ChainStateRepo.Get(ctx, txParam.ChainID)
	if err != nil {
		return nil, err
	}
	required := chainConfirmationRequirement(txParam.ChainID)
	txModel, err := coreApplication.CORE.Router.TransactionRepo.FindByUniqueHash(ctx, uniqueHash)
	if err != nil {
		return nil, err
	}
	confirmations := transactionConfirmations(txModel.BlockNumber, state)
	finalized := confirmations >= required
	return coreApplication.CORE.Router.TransactionRepo.MarkFinality(ctx, uniqueHash, confirmations, required, finalized)
}

func finalizePendingTransactions(ctx context.Context, notifier *webhooksvc.Notifier) {
	rows, err := coreApplication.CORE.Router.TransactionRepo.ListPendingFinality(ctx, 500)
	if err != nil {
		log.Println("Pending finality query error:", err)
		return
	}
	for _, row := range rows {
		state, err := coreApplication.CORE.Router.ChainStateRepo.Get(ctx, row.ChainID)
		if err != nil {
			log.Println("Finality chain state error:", err)
			continue
		}
		required := row.ConfirmationsRequired
		if required == 0 {
			required = chainConfirmationRequirement(row.ChainID)
		}
		confirmations := transactionConfirmations(row.BlockNumber, state)
		if confirmations < required {
			_, _ = coreApplication.CORE.Router.TransactionRepo.MarkFinality(ctx, row.UniqueHash, confirmations, required, false)
			continue
		}
		finalized, err := coreApplication.CORE.Router.TransactionRepo.MarkFinality(ctx, row.UniqueHash, confirmations, required, true)
		if err != nil {
			log.Println("Finality update error:", err)
			continue
		}
		handlePaymentDeposit(ctx, notifier, finalized)
		enqueueSweepJob(ctx, finalized)
	}
}

func processDepositFacts(ctx context.Context) {
	if coreApplication.CORE == nil || coreApplication.CORE.Router == nil {
		return
	}
	router := coreApplication.CORE.Router
	if router.ChainFactRepo == nil ||
		router.ChainStateRepo == nil ||
		router.DepositRepo == nil ||
		router.WalletRepo == nil ||
		router.TransactionRepo == nil {
		return
	}
	service := depositsvc.New(depositsvc.Dependencies{
		ChainFactRepo:   router.ChainFactRepo,
		ChainStateRepo:  router.ChainStateRepo,
		DepositRepo:     router.DepositRepo,
		WalletRepo:      router.WalletRepo,
		TransactionRepo: router.TransactionRepo,
		PaymentRepo:     router.PaymentRepo,
		LedgerRepo:      router.LedgerRepo,
	}, chainConfirmationRequirement)
	summary, err := service.ProcessBatch(ctx, 200)
	if err != nil {
		log.Println("Deposit fact processing error:", err)
		return
	}
	if summary.FactsProcessed > 0 || summary.Finalized > 0 || summary.PaymentsSettled > 0 {
		log.Printf(
			"Deposit facts processed=%d created=%d matched=%d unmatched=%d finalized=%d transactions=%d payments=%d\n",
			summary.FactsProcessed,
			summary.DepositsCreated,
			summary.Matched,
			summary.Unmatched,
			summary.Finalized,
			summary.TransactionsRecorded,
			summary.PaymentsSettled,
		)
	}
}

func startDepositFactWorker(ctx context.Context) {
	processDepositFacts(ctx)
	ticker := time.NewTicker(depositFactInterval())
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				processDepositFacts(ctx)
			}
		}
	}()
}

func ensureMerchantReserveWallet(ctx context.Context, merchantID uuid.UUID) (*models.Wallet, error) {
	wallet, err := coreApplication.CORE.Router.WalletRepo.FindReserveWallet(ctx, merchantID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		domain, createErr := coreApplication.CORE.Router.DomainService.CreateReserve(ctx, merchantID)
		if createErr != nil {
			return nil, fmt.Errorf("reserve domain: %w", createErr)
		}
		wallet, err = coreApplication.CORE.Router.WalletRepo.CreateReserveWallet(ctx, merchantID, domain.ID, domain.HDAccountID)
	}
	if err != nil {
		return nil, err
	}
	if err := coreApplication.CORE.Router.WalletRepo.EnsureAllAddresses(ctx, wallet.ID, coreApplication.CORE.Router.Blockchains()); err != nil {
		return nil, err
	}
	return coreApplication.CORE.Router.WalletRepo.FindByID(ctx, wallet.ID)
}

func enqueueSweepJob(ctx context.Context, txModel *models.Transaction) {
	if txModel == nil || txModel.MerchantID == nil || txModel.WalletID == nil {
		return
	}
	userWallet, err := coreApplication.CORE.Router.WalletRepo.FindByID(ctx, *txModel.WalletID)
	if err != nil || userWallet == nil {
		log.Printf("sweep enqueue: wallet %s not found: %v", txModel.WalletID, err)
		return
	}
	if userWallet.HDAddressId == 0 {
		return
	}
	if coreApplication.CORE.Router.SweepJobRepo == nil {
		go autoSweepDeposit(txModel)
		return
	}
	job, created, err := coreApplication.CORE.Router.SweepJobRepo.EnqueueForTransaction(ctx, *txModel)
	if err != nil {
		log.Printf("sweep enqueue: tx=%s error=%v", txModel.UniqueHash, err)
		return
	}
	if created && job != nil {
		log.Printf("sweep enqueue: job=%s tx=%s chain=%d", job.ID, txModel.UniqueHash, txModel.ChainID)
		enqueueSweepLifecycleWebhook(ctx, *job, txModel, constants.WebhookEventSweepRequestedV1, "")
	}
}

// autoSweepDeposit is the legacy fire-and-forget fallback used only if the sweep job repo is unavailable.
func autoSweepDeposit(txModel *models.Transaction) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	result, err := executeAutoSweepDeposit(ctx, txModel)
	if err != nil {
		log.Printf("auto-sweep: %v", err)
		return
	}
	if result != nil {
		log.Printf("auto-sweep: swept to reserve tx=%s", result.TxHash)
	}
}

// executeAutoSweepDeposit moves funds from a user wallet (HDAddressId > 0) to the merchant reserve wallet.
func executeAutoSweepDeposit(ctx context.Context, txModel *models.Transaction) (*blockchain.TransactionResult, error) {
	return executeAutoSweepDepositWithJob(ctx, txModel, nil)
}

func executeSweepJob(ctx context.Context, job models.SweepJob, txModel *models.Transaction) (*blockchain.TransactionResult, error) {
	return executeAutoSweepDepositWithJob(ctx, txModel, &job)
}

func executeAutoSweepDepositWithJob(ctx context.Context, txModel *models.Transaction, job *models.SweepJob) (*blockchain.TransactionResult, error) {
	if txModel == nil || txModel.MerchantID == nil || txModel.WalletID == nil {
		return nil, nil
	}

	userWallet, err := coreApplication.CORE.Router.WalletRepo.FindByID(ctx, *txModel.WalletID)
	if err != nil || userWallet == nil {
		return nil, fmt.Errorf("wallet %s not found: %w", txModel.WalletID, err)
	}
	if userWallet.HDAddressId == 0 {
		return nil, nil
	}

	chain, err := coreApplication.CORE.Router.Blockchains().GetChainByID(txModel.ChainID)
	if err != nil {
		return nil, fmt.Errorf("chain %d not found: %w", txModel.ChainID, err)
	}

	reserveWallet, err := ensureMerchantReserveWallet(ctx, *txModel.MerchantID)
	if err != nil {
		return nil, fmt.Errorf("no reserve wallet for merchant %s: %w", txModel.MerchantID, err)
	}

	reserveAddr := repositories.WalletAddressForChainID(*reserveWallet, txModel.ChainID)
	if reserveAddr == "" {
		return nil, fmt.Errorf("reserve wallet has no address for chain %d", txModel.ChainID)
	}

	userDetails, err := chain.CreateHDWallet(ctx, int(userWallet.HDAccountID), int(userWallet.HDAddressId))
	if err != nil {
		return nil, fmt.Errorf("re-derive wallet [acct=%d idx=%d] failed: %w", userWallet.HDAccountID, userWallet.HDAddressId, err)
	}

	if txModel.Token != nil && *txModel.Token != "" {
		reserveDetails, err := chain.CreateHDWallet(ctx, int(reserveWallet.HDAccountID), int(reserveWallet.HDAddressId))
		if err != nil {
			return nil, fmt.Errorf("re-derive reserve wallet failed: %w", err)
		}
		if shouldAttemptSweepPrefund(job) {
			prefunded, err := chain.PrefundGas(ctx, *reserveDetails, userDetails.Address)
			if err != nil {
				markSweepPrefundFailed(ctx, job, err)
				log.Printf("auto-sweep: gas prefund [chain=%d addr=%s]: %v", txModel.ChainID, userDetails.Address, err)
			} else if prefunded {
				markSweepPrefunded(ctx, job)
				log.Printf("auto-sweep: gas prefunded to %s on chain %d", userDetails.Address, txModel.ChainID)
				time.Sleep(5 * time.Second)
			}
		}
		result, err := chain.SweepERC20To(ctx, *userDetails, *txModel.Token, reserveAddr)
		if err != nil {
			return nil, fmt.Errorf("sweep token [chain=%d token=%s]: %w", txModel.ChainID, *txModel.Token, err)
		}
		return result, nil
	}

	result, err := chain.SweepTo(ctx, *userDetails, reserveAddr)
	if err != nil {
		return nil, fmt.Errorf("sweep native [chain=%d]: %w", txModel.ChainID, err)
	}
	return result, nil
}

func shouldAttemptSweepPrefund(job *models.SweepJob) bool {
	if job == nil || job.PrefundedAt == nil {
		return true
	}
	return time.Since(*job.PrefundedAt) >= sweepPrefundRetryAfter()
}

func markSweepPrefunded(ctx context.Context, job *models.SweepJob) {
	if job == nil || coreApplication.CORE.Router.SweepJobRepo == nil {
		return
	}
	if err := coreApplication.CORE.Router.SweepJobRepo.MarkPrefunded(ctx, job.ID); err != nil {
		log.Printf("auto-sweep: mark prefunded job=%s: %v", job.ID, err)
	}
}

func markSweepPrefundFailed(ctx context.Context, job *models.SweepJob, prefundErr error) {
	if job == nil || coreApplication.CORE.Router.SweepJobRepo == nil {
		return
	}
	if err := coreApplication.CORE.Router.SweepJobRepo.MarkPrefundFailed(ctx, job.ID, prefundErr); err != nil {
		log.Printf("auto-sweep: mark prefund failed job=%s: %v", job.ID, err)
	}
}

func handlePaymentDeposit(ctx context.Context, notifier *webhooksvc.Notifier, txModel *models.Transaction) {
	if txModel == nil || coreApplication.CORE.Router.PaymentRepo == nil {
		return
	}

	session, changed, err := coreApplication.CORE.Router.PaymentRepo.MarkPaidByTransaction(ctx, *txModel)
	if err != nil {
		log.Println("Payment match error:", err)
		return
	}
	if !changed || session == nil {
		postStaticAddressDepositAvailable(ctx, txModel)
		return
	}
	if coreApplication.CORE.Router.LedgerRepo != nil {
		if err := coreApplication.CORE.Router.LedgerRepo.PostDepositAvailable(ctx, *session, *txModel); err != nil {
			log.Println("Ledger available deposit error:", err)
		}
	}
	publishPaymentUpdate(session)
	if session.WebhookSentAt != nil {
		return
	}

	createPaymentWebhookDelivery(ctx, session.Domain, *session)
}

func postStaticAddressDepositAvailable(ctx context.Context, txModel *models.Transaction) {
	if txModel == nil || txModel.WalletID == nil || coreApplication.CORE.Router.LedgerRepo == nil {
		return
	}
	wallet, err := coreApplication.CORE.Router.WalletRepo.FindByID(ctx, *txModel.WalletID)
	if err != nil || wallet == nil {
		return
	}
	if !isStandaloneDepositWalletProduct(wallet.ProductID) {
		return
	}
	if err := coreApplication.CORE.Router.LedgerRepo.PostStandaloneDepositAvailable(ctx, *txModel); err != nil {
		log.Println("Ledger standalone available deposit error:", err)
	}
}

func isStandaloneDepositWalletProduct(productID string) bool {
	productID = strings.TrimSpace(productID)
	return strings.HasPrefix(productID, "static:") || strings.HasPrefix(productID, "wallet:")
}

func createTransactionWebhookDelivery(ctx context.Context, domain models.Domain, txModel models.Transaction) uuid.UUID {
	if coreApplication.CORE == nil || coreApplication.CORE.Router == nil || coreApplication.CORE.Router.WebhookDeliveryRepo == nil {
		return uuid.Nil
	}
	if txModel.MerchantID == nil || txModel.DomainID == nil {
		return uuid.Nil
	}
	delivery, _, err := coreApplication.CORE.Router.WebhookDeliveryRepo.EnqueueTransaction(ctx, domain, txModel)
	if err != nil {
		log.Println("Webhook delivery log create error:", err)
		return uuid.Nil
	}
	if delivery == nil {
		return uuid.Nil
	}
	return delivery.ID
}

func createPaymentWebhookDelivery(ctx context.Context, domain models.Domain, session models.PaymentSession) uuid.UUID {
	if coreApplication.CORE == nil || coreApplication.CORE.Router == nil || coreApplication.CORE.Router.WebhookDeliveryRepo == nil {
		return uuid.Nil
	}
	delivery, _, err := coreApplication.CORE.Router.WebhookDeliveryRepo.EnqueuePayment(ctx, domain, session)
	if err != nil {
		log.Println("Payment webhook delivery log create error:", err)
		return uuid.Nil
	}
	if delivery == nil {
		return uuid.Nil
	}
	return delivery.ID
}

func enqueueLifecycleWebhook(ctx context.Context, domain models.Domain, payload webhooksvc.LifecyclePayload) uuid.UUID {
	if coreApplication.CORE == nil || coreApplication.CORE.Router == nil || coreApplication.CORE.Router.WebhookDeliveryRepo == nil {
		return uuid.Nil
	}
	delivery, _, err := coreApplication.CORE.Router.WebhookDeliveryRepo.EnqueueLifecycle(ctx, domain, payload)
	if err != nil {
		log.Printf("Lifecycle webhook enqueue error event=%s id=%s: %v\n", payload.EventType, payload.EventID, err)
		return uuid.Nil
	}
	if delivery == nil {
		return uuid.Nil
	}
	return delivery.ID
}

func findDomainForLifecycle(ctx context.Context, domainID uuid.UUID) (*models.Domain, error) {
	if coreApplication.CORE == nil || coreApplication.CORE.Router == nil || coreApplication.CORE.Router.DomainRepo == nil {
		return nil, errors.New("domain repo is unavailable")
	}
	domainIDString := domainID.String()
	return coreApplication.CORE.Router.DomainRepo.FindByID(types.DomainParams{
		Context:  ctx,
		DomainID: &domainIDString,
	})
}

func enqueueSweepLifecycleWebhook(ctx context.Context, job models.SweepJob, txModel *models.Transaction, eventType string, errText string) uuid.UUID {
	if txModel == nil || txModel.DomainID == nil {
		return uuid.Nil
	}
	domain, err := findDomainForLifecycle(ctx, *txModel.DomainID)
	if err != nil {
		log.Printf("Sweep lifecycle domain lookup error job=%s domain=%s: %v\n", job.ID, txModel.DomainID.String(), err)
		return uuid.Nil
	}
	payload := webhooksvc.NewSweepPayload(eventType, job, txModel, errText)
	return enqueueLifecycleWebhook(ctx, *domain, payload)
}

func enqueuePayoutLifecycleWebhook(ctx context.Context, request models.WithdrawalRequest, eventType string) uuid.UUID {
	if request.DomainID == nil {
		return uuid.Nil
	}
	domain, err := findDomainForLifecycle(ctx, *request.DomainID)
	if err != nil {
		log.Printf("Payout lifecycle domain lookup error payout=%s domain=%s: %v\n", request.ID, request.DomainID.String(), err)
		return uuid.Nil
	}
	payload := webhooksvc.NewPayoutPayload(eventType, request)
	return enqueueLifecycleWebhook(ctx, *domain, payload)
}

func enqueueRefundLifecycleWebhook(ctx context.Context, refund models.Refund, eventType string) uuid.UUID {
	domain, err := findDomainForLifecycle(ctx, refund.DomainID)
	if err != nil {
		log.Printf("Refund lifecycle domain lookup error refund=%s domain=%s: %v\n", refund.ID, refund.DomainID.String(), err)
		return uuid.Nil
	}
	payload := webhooksvc.NewRefundPayload(eventType, refund)
	return enqueueLifecycleWebhook(ctx, *domain, payload)
}

func markWebhookDeliveryAttempt(ctx context.Context, deliveryID uuid.UUID, delivered bool, lastErr error) {
	if deliveryID == uuid.Nil || coreApplication.CORE == nil || coreApplication.CORE.Router == nil || coreApplication.CORE.Router.WebhookDeliveryRepo == nil {
		return
	}
	if err := coreApplication.CORE.Router.WebhookDeliveryRepo.MarkAttempt(ctx, deliveryID, delivered, lastErr); err != nil {
		log.Println("Webhook delivery log update error:", err)
	}
}

func publishPaymentUpdate(session *models.PaymentSession) {
	if session == nil || coreApplication.CORE == nil || coreApplication.CORE.Router == nil || coreApplication.CORE.Router.PaymentHub == nil {
		return
	}
	coreApplication.CORE.Router.PaymentHub.Broadcast(session.SessionToken, paymentRealtimeBroadcastEvent(session))
}

func paymentRealtimeBroadcastEvent(session *models.PaymentSession) realtime.PaymentEvent {
	if session == nil {
		return realtime.PaymentEvent{}
	}
	paid := session.Status == models.PaymentStatusPaid
	status := session.Status
	payable := false
	terminal := false
	switch session.Status {
	case models.PaymentStatusPaid:
		terminal = true
	case models.PaymentStatusExpired, models.PaymentStatusCanceled, models.PaymentStatusFailed, models.PaymentStatusUnderpaid:
		terminal = true
	case models.PaymentStatusAwaitingPayment:
		payable = true
		status = "active"
		if ptrValue(session.TxHash) != "" || session.ConfirmedAt != nil {
			status = "confirming"
		}
	case models.PaymentStatusPending:
		status = "pending"
	}
	return realtime.PaymentEvent{
		Event:       "payment.updated",
		Status:      status,
		Paid:        paid,
		Payable:     payable,
		Terminal:    terminal,
		PaymentID:   session.ID.String(),
		TxHash:      ptrValue(session.TxHash),
		SuccessPath: "/checkout/" + session.SessionToken + "/return/success",
		CancelPath:  "/checkout/" + session.SessionToken + "/cancel",
		UpdatedAt:   time.Now().UnixMilli(),
	}
}

func retryPendingWebhooks(ctx context.Context, notifier *webhooksvc.Notifier) {
	bridgePendingTransactionWebhookDeliveries(ctx)
	bridgePendingPaymentWebhookDeliveries(ctx)

	if coreApplication.CORE == nil || coreApplication.CORE.Router == nil || coreApplication.CORE.Router.WebhookDeliveryRepo == nil || coreApplication.CORE.Router.DomainRepo == nil || notifier == nil {
		return
	}
	router := coreApplication.CORE.Router
	processor := webhooksvc.DeliveryProcessor{
		DeliveryRepo: router.WebhookDeliveryRepo,
		Notifier:     notifier,
		DomainLookup: func(ctx context.Context, id uuid.UUID) (*models.Domain, error) {
			idString := id.String()
			return router.DomainRepo.FindByID(types.DomainParams{
				Context:  ctx,
				DomainID: &idString,
			})
		},
	}
	if router.TransactionRepo != nil {
		processor.TransactionLookup = router.TransactionRepo.FindByID
		processor.MarkTransactionAttempt = router.TransactionRepo.MarkWebhookAttempt
	}
	if router.PaymentRepo != nil {
		processor.PaymentLookup = router.PaymentRepo.FindByID
		processor.MarkPaymentAttempt = router.PaymentRepo.MarkWebhookAttempt
	}
	summary, err := processor.ProcessDue(ctx, 100)
	if err != nil {
		log.Println("Webhook boundary delivery error:", err)
		return
	}
	if summary.Claimed > 0 {
		log.Printf("Webhook boundary delivered=%d failed=%d claimed=%d\n", summary.Delivered, summary.Failed, summary.Claimed)
	}
}

func bridgePendingTransactionWebhookDeliveries(ctx context.Context) {
	if coreApplication.CORE == nil || coreApplication.CORE.Router == nil || coreApplication.CORE.Router.TransactionRepo == nil || coreApplication.CORE.Router.WebhookDeliveryRepo == nil || coreApplication.CORE.Router.WalletRepo == nil {
		return
	}
	transactions, err := coreApplication.CORE.Router.TransactionRepo.ListPendingWebhooks(ctx, 100)
	if err != nil {
		log.Println("Pending webhook query error:", err)
		return
	}

	for _, txModel := range transactions {
		if txModel.WalletID == nil || txModel.WebhookSentAt != nil {
			continue
		}

		wallet, err := coreApplication.CORE.Router.WalletRepo.FindByID(ctx, *txModel.WalletID)
		if err != nil {
			log.Println("Webhook wallet lookup error:", err)
			_ = coreApplication.CORE.Router.TransactionRepo.MarkWebhookAttempt(ctx, txModel.UniqueHash, false, err)
			continue
		}

		createTransactionWebhookDelivery(ctx, wallet.Domain, txModel)
	}
}

func bridgePendingPaymentWebhookDeliveries(ctx context.Context) {
	if coreApplication.CORE == nil || coreApplication.CORE.Router == nil || coreApplication.CORE.Router.PaymentRepo == nil || coreApplication.CORE.Router.WebhookDeliveryRepo == nil {
		return
	}
	sessions, err := coreApplication.CORE.Router.PaymentRepo.ListPendingWebhooks(ctx, 100)
	if err != nil {
		log.Println("Pending payment webhook query error:", err)
		return
	}
	for _, session := range sessions {
		createPaymentWebhookDelivery(ctx, session.Domain, session)
	}
}

func bootstrapAdminAccount(ctx context.Context) {
	email := strings.TrimSpace(os.Getenv("ADMIN_EMAIL"))
	password := os.Getenv("ADMIN_PASSWORD")
	name := strings.TrimSpace(os.Getenv("ADMIN_NAME"))
	if email == "" {
		email = "admin@gateway.local"
	}
	if name == "" {
		name = "Admin"
	}
	if password == "" {
		// No hardcoded default — generate a strong one-time random password.
		rnd, err := coreHelpers.GenerateBcryptSafeSecret()
		if err != nil {
			log.Printf("Bootstrap admin: failed to generate random password: %v\n", err)
			return
		}
		password = rnd
		passwordFile := ".bootstrap_admin_password"
		if err := os.WriteFile(passwordFile, []byte(password+"\n"), 0600); err != nil {
			log.Printf("[BOOTSTRAP] ADMIN_PASSWORD not set and generated password file could not be written: %v\n", err)
			return
		}
		log.Printf("[BOOTSTRAP] ADMIN_PASSWORD not set in .env; generated first-run password file: %s\n", passwordFile)
		log.Printf("[BOOTSTRAP] Set ADMIN_PASSWORD in .env before restarting to use a fixed password.\n")
	}
	created, err := coreApplication.CORE.Router.AdminRepo.EnsureBootstrapAdmin(ctx, email, name, password)
	if err != nil {
		log.Printf("Bootstrap admin error: %v\n", err)
		return
	}
	if created != nil {
		log.Printf("Bootstrap admin created: %s (2FA not yet configured — login to set up)\n", email)
	}
}

func backfillMissingAddresses(ctx context.Context) {
	wallets, err := coreApplication.CORE.Router.WalletRepo.List(ctx, 10000)
	if err != nil {
		log.Printf("Backfill: wallet list error: %v\n", err)
		return
	}
	filled := 0
	for _, wallet := range wallets {
		if err := coreApplication.CORE.Router.WalletRepo.EnsureAllAddresses(
			ctx,
			wallet.ID,
			coreApplication.CORE.Router.Blockchains(),
		); err != nil {
			log.Printf("Backfill: wallet %s error: %v\n", wallet.ID, err)
		} else {
			filled++
		}
	}
	if filled > 0 {
		log.Printf("Backfill: ensured addresses for %d wallets\n", filled)
		if err := addrIndex.Load(); err != nil {
			log.Printf("Backfill: address index reload error: %v\n", err)
		}
	}
}

func startSessionExpiryWorker(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := coreApplication.CORE.Router.PaymentRepo.MarkExpiredSessions(ctx)
			if err != nil {
				log.Println("Session expiry sweep error:", err)
			} else if n > 0 {
				log.Printf("Expired %d payment sessions\n", n)
			}
		}
	}
}

func startWebhookRetryWorker(ctx context.Context, notifier *webhooksvc.Notifier) {
	ticker := time.NewTicker(webhookRetryInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			retryPendingWebhooks(ctx, notifier)
		}
	}
}

func startTransactionFinalityWorker(ctx context.Context, notifier *webhooksvc.Notifier) {
	ticker := time.NewTicker(transactionFinalityInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			finalizePendingTransactions(ctx, notifier)
		}
	}
}

func processSweepJobs(ctx context.Context) {
	router := coreApplication.CORE.Router
	if router.SweepJobRepo == nil || router.TransactionRepo == nil {
		return
	}
	jobs, err := router.SweepJobRepo.ClaimDue(ctx, 25, sweepJobLockTimeout())
	if err != nil {
		log.Println("Sweep job claim error:", err)
		return
	}
	for _, job := range jobs {
		txModel, err := router.TransactionRepo.FindByUniqueHash(ctx, job.TransactionUniqueHash)
		if err != nil {
			log.Printf("Sweep job transaction lookup error job=%s tx=%s: %v\n", job.ID, job.TransactionUniqueHash, err)
			_ = router.SweepJobRepo.MarkFailed(ctx, job.ID, err)
			if updated, findErr := router.SweepJobRepo.Find(ctx, job.ID); findErr == nil {
				eventType := constants.WebhookEventSweepFailedV1
				if updated.Status == models.SweepJobStatusDeadLetter {
					eventType = constants.WebhookEventSweepDeadLetteredV1
				}
				enqueueSweepLifecycleWebhook(ctx, *updated, nil, eventType, err.Error())
			}
			continue
		}
		jobCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		result, err := executeSweepJob(jobCtx, job, txModel)
		cancel()
		if err != nil {
			log.Printf("Sweep job failed job=%s tx=%s: %v\n", job.ID, job.TransactionUniqueHash, err)
			_ = router.SweepJobRepo.MarkFailed(ctx, job.ID, err)
			if updated, findErr := router.SweepJobRepo.Find(ctx, job.ID); findErr == nil {
				eventType := constants.WebhookEventSweepFailedV1
				if updated.Status == models.SweepJobStatusDeadLetter {
					eventType = constants.WebhookEventSweepDeadLetteredV1
				}
				enqueueSweepLifecycleWebhook(ctx, *updated, txModel, eventType, err.Error())
			}
			continue
		}
		txHash := ""
		if result != nil {
			txHash = result.TxHash
		}
		if err := router.SweepJobRepo.MarkSucceeded(ctx, job.ID, txHash); err != nil {
			log.Printf("Sweep job mark succeeded error job=%s: %v\n", job.ID, err)
			continue
		}
		job.Status = models.SweepJobStatusSucceeded
		job.SweepTxHash = txHash
		job.LastError = ""
		enqueueSweepLifecycleWebhook(ctx, job, txModel, constants.WebhookEventSweepSucceededV1, "")
		log.Printf("Sweep job succeeded job=%s tx=%s sweep_tx=%s\n", job.ID, job.TransactionUniqueHash, txHash)
	}
}

func startSweepJobWorker(ctx context.Context) {
	ticker := time.NewTicker(sweepJobInterval())
	defer ticker.Stop()
	processSweepJobs(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			processSweepJobs(ctx)
		}
	}
}

func runLedgerInvariantReconciliation(ctx context.Context) {
	router := coreApplication.CORE.Router
	if router.LedgerRepo == nil || router.ReconciliationRepo == nil {
		return
	}
	issues, err := router.LedgerRepo.FindInvariantIssues(ctx, 200)
	if err != nil {
		log.Println("Ledger invariant reconciliation error:", err)
		return
	}
	if len(issues) == 0 {
		return
	}
	for _, issue := range issues {
		domainID := ledgerInvariantDomainID(issue.DomainID)
		correlationID := ledgerInvariantCorrelationID(issue)
		reason := ledgerInvariantReason(issue)
		if _, created, err := router.ReconciliationRepo.CreateOpenIfMissing(ctx, constants.ChainID(issue.ChainID), 0, 0, reason); err != nil {
			log.Printf("Reconciliation job create error correlation_id=%s merchant=%s domain=%s chain=%d token=%s symbol=%s net=%s: %v\n", correlationID, issue.MerchantID, domainID, issue.ChainID, ptrValue(issue.Token), issue.Symbol, issue.NetRaw, err)
		} else if created {
			log.Printf("Reconciliation job opened correlation_id=%s merchant=%s domain=%s chain=%d token=%s symbol=%s net=%s\n", correlationID, issue.MerchantID, domainID, issue.ChainID, ptrValue(issue.Token), issue.Symbol, issue.NetRaw)
		}
	}
}

func ledgerInvariantCorrelationID(issue repositories.LedgerInvariantIssue) string {
	return "ledger_invariant:" + issue.IdempotencyKey
}

func ledgerInvariantReason(issue repositories.LedgerInvariantIssue) string {
	reason := fmt.Sprintf("ledger_invariant:%s:%s:%d:%s", issue.MerchantID.String(), ledgerInvariantDomainID(issue.DomainID), issue.ChainID, issue.IdempotencyKey)
	if len(reason) > 120 {
		return reason[:120]
	}
	return reason
}

func ledgerInvariantDomainID(domainID *uuid.UUID) string {
	if domainID == nil {
		return "none"
	}
	return domainID.String()
}

func runReserveBalanceReconciliation(ctx context.Context) {
	router := coreApplication.CORE.Router
	if router.WalletRepo == nil || router.LedgerRepo == nil || router.ReconciliationRepo == nil || router.Blockchains() == nil {
		return
	}
	service := reconsvc.NewReserveService(
		router.WalletRepo,
		router.LedgerRepo,
		router.ReconciliationRepo,
		router.Blockchains(),
	)
	report, err := service.RunOnce(ctx, reserveReconciliationLimit())
	if err != nil {
		log.Println("Reserve balance reconciliation error:", err)
	}
	if report.JobsOpened > 0 {
		log.Printf(
			"Reserve balance reconciliation opened %d jobs wallets=%d queries=%d deficits=%d missing=%d unreadable=%d query_errors=%d missing_addresses=%d unavailable_chains=%d\n",
			report.JobsOpened,
			report.WalletsChecked,
			report.BalanceQueries,
			report.Deficits,
			report.MissingComponents,
			report.UnreadableBalances,
			report.QueryErrors,
			report.MissingAddresses,
			report.UnavailableChains,
		)
	}
}

func startReconciliationWorker(ctx context.Context) {
	ticker := time.NewTicker(reconciliationInterval())
	defer ticker.Stop()
	runLedgerInvariantReconciliation(ctx)
	runReserveBalanceReconciliation(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runLedgerInvariantReconciliation(ctx)
			runReserveBalanceReconciliation(ctx)
		}
	}
}

func finalizeProcessingTransfers(ctx context.Context) {
	router := coreApplication.CORE.Router
	if router.WithdrawalRepo != nil {
		withdrawals, err := router.WithdrawalRepo.ListProcessingWithTxHash(ctx, 100)
		if err != nil {
			log.Println("Processing withdrawal query error:", err)
		} else {
			for _, request := range withdrawals {
				if err := router.WithdrawalRepo.FinalizeProcessingWithLedger(ctx, request.ID, router.LedgerRepo); err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					log.Printf("Processing withdrawal finalize error %s: %v\n", request.ID, err)
				} else if err == nil {
					request.Status = models.WithdrawalStatusApproved
					request.Error = ""
					enqueuePayoutLifecycleWebhook(ctx, request, constants.WebhookEventPayoutFinalizedV1)
				}
			}
		}
	}
	if router.RefundRepo != nil && router.PaymentRepo != nil {
		refunds, err := router.RefundRepo.ListProcessingWithTxHash(ctx, 100)
		if err != nil {
			log.Println("Processing refund query error:", err)
		} else {
			for _, refund := range refunds {
				session, err := router.PaymentRepo.FindByID(ctx, refund.PaymentID)
				if err != nil {
					log.Printf("Processing refund payment lookup error %s: %v\n", refund.ID, err)
					continue
				}
				if err := router.RefundRepo.MarkSucceededWithLedger(ctx, refund.ID, refund.ReviewedBy, refund.TxHash, *session, router.LedgerRepo); err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					log.Printf("Processing refund finalize error %s: %v\n", refund.ID, err)
				} else if err == nil {
					refund.Status = models.RefundStatusSucceeded
					refund.Error = ""
					enqueueRefundLifecycleWebhook(ctx, refund, constants.WebhookEventRefundSucceededV1)
				}
			}
		}
	}
}

func startTransferFinalizationWorker(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	finalizeProcessingTransfers(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			finalizeProcessingTransfers(ctx)
		}
	}
}

func NewApp() (*coreApplication.App, error) {
	if coreApplication.CORE == nil {

		err := coreDB.InitDB()
		if err != nil {
			return nil, err
		}

		coreApplication.CORE = &coreApplication.App{
			DB:     coreDB.DB,
			Router: routes.NewRouter(coreDB.DB),
		}

		migrateFlag := flag.Bool("migrate", false, "Run DB migrations (also runs automatically on startup)")
		seedFlag := flag.Bool("seed", false, "Run DB seed")
		installFlag := flag.Bool("install", false, "Run DB migrate & seed")

		flag.Parse()

		if *installFlag {
			*seedFlag = true
			*migrateFlag = true
		}

		if *migrateFlag {
			log.Println("Migration flag detected; startup migration check is enabled by default")
		}
		log.Println("Migration:BEGIN")
		if err = coreDB.Migrate(coreApplication.CORE); err != nil {
			return nil, err
		}
		log.Println("Migration:END")

		if *seedFlag {
			err = coreDB.Seed(coreApplication.CORE)
			if err != nil {
				return nil, err
			}
		}
	}

	return coreApplication.CORE, nil
}

func GetApp() (*coreApplication.App, error) {
	return NewApp()
}

// @title Gateway API
// @version 1.0
// @description Multi-chain merchant payment gateway API.
// @BasePath /
// @securityDefinitions.apikey ApiKeyAuth
// @in header
// @name X-API-Key
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
func main() {
	mainCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = godotenv.Load()
	verboseTxLogging := envFlagEnabled("GATEWAY_VERBOSE_TX", "VERBOSE")

	var err error
	coreApplication.CORE, err = NewApp()
	if err != nil {
		log.Fatal(err)
	}

	log.Println("Registered chains:", coreApplication.CORE.Router.Blockchains().ListChains())
	deletedChainStates, err := coreApplication.CORE.Router.ChainStateRepo.DeleteUnsupported(
		mainCtx,
		coreApplication.CORE.Router.Blockchains().ListChainIDs(),
	)
	if err != nil {
		log.Fatal(err)
	}
	if deletedChainStates > 0 {
		log.Printf("Deleted %d unsupported chain state rows\n", deletedChainStates)
	}

	addrIndex = addressindex.NewAddressIndex(mainCtx, coreDB.DB)
	if err := addrIndex.Load(); err != nil {
		log.Printf("Address index load error: %v\n", err)
	} else {
		log.Println("Address index loaded")
	}

	go backfillMissingAddresses(mainCtx)
	go bootstrapAdminAccount(mainCtx)

	bus := dispatcher.NewDispatcher()
	assetRegistry := coreApplication.CORE.Router.AssetRegistry()
	coreApplication.CORE.Router.TxRescanService = txrescan.New(
		coreApplication.CORE.Router.Blockchains(),
		assetRegistry,
		bus,
		coreApplication.CORE.Router.TransactionRepo,
		coreApplication.CORE.Router.WalletRepo,
	)
	webhookNotifier := webhooksvc.NewNotifier()
	go startWebhookRetryWorker(mainCtx, webhookNotifier)
	go startSessionExpiryWorker(mainCtx)
	go startTransactionFinalityWorker(mainCtx, webhookNotifier)
	go startDepositFactWorker(mainCtx)
	go startSweepJobWorker(mainCtx)
	go startReconciliationWorker(mainCtx)
	go startTransferFinalizationWorker(mainCtx)

	var isEnabled = true

	if isEnabled {
		subscribeBus := func(chain blockchain.Chain) {
			events := bus.Subscribe(chain.ChainID(), 1000)
			go func() {
				for {
					select {
					case <-mainCtx.Done():
						return
					case event, ok := <-events:
						if !ok {
							return
						}
						if event.Transaction == nil {
							if event.Ack != nil {
								event.Ack <- nil
							}
							continue
						}

						tx := event.Transaction
						if verboseTxLogging {
							fmt.Printf(
								"[BUS] chain=%s chain_id=%d type=%s hash=%s block=%s from=%s to=%s amount=%s symbol=%s log=%s\n",
								chain.Name(),
								tx.ChainID,
								event.Type,
								ptrValue(tx.Hash),
								ptrValue(tx.Block),
								ptrValue(tx.From),
								ptrValue(tx.To),
								ptrValue(tx.Amount),
								ptrValue(tx.Symbol),
								ptrValue(tx.LogIndex),
							)
						}

						err := handleChainIndexerEvent(mainCtx, event)
						if err != nil {
							fmt.Println("Chain fact record error:", err)
						}
						if event.Ack != nil {
							event.Ack <- err
						}
					}
				}
			}()
		}

		for _, chainName := range coreApplication.CORE.Router.Blockchains().ListChains() {
			chain, err := coreApplication.CORE.Router.MerchantRepo.Blockchains().GetChain(chainName)
			if err != nil {
				log.Printf("[%s] chain not found: %v\n", chainName, err)
				continue
			}

			state, err := coreApplication.CORE.Router.ChainStateRepo.Get(mainCtx, chain.ChainID())
			if err != nil {
				log.Printf("[%s] chain state error: %v\n", chainName, err)
				continue
			}

			var worker blockchain.Worker
			switch chain.ChainID() {
			case constants.Bitcoin:
				worker = btcListener.NewRpcListener(
					chain,
					assetRegistry,
					state,
					bus,
					func(s *models.ChainState) error {
						return coreApplication.CORE.Router.ChainStateRepo.Update(mainCtx, s)
					},
				)
			case constants.Solana:
				worker = solListener.NewRpcListener(
					chain,
					assetRegistry,
					state,
					bus,
					func(s *models.ChainState) error {
						return coreApplication.CORE.Router.ChainStateRepo.Update(mainCtx, s)
					},
				)
			case constants.TRON:
				worker = tronListener.NewRpcListener(
					chain,
					assetRegistry,
					state,
					bus,
					func(s *models.ChainState) error {
						return coreApplication.CORE.Router.ChainStateRepo.Update(mainCtx, s)
					},
				)
			default:
				worker = evmListener.NewRpcListener(
					chain,
					assetRegistry,
					state,
					bus,
					func(s *models.ChainState) error {
						return coreApplication.CORE.Router.ChainStateRepo.Update(mainCtx, s)
					},
				)
			}

			if err := chain.AddWorker(worker); err != nil {
				log.Printf("[%s] add worker error: %v\n", chain.Name(), err)
				continue
			}
			subscribeBus(chain)
		}

		for chainName, startErr := range coreApplication.CORE.Router.Blockchains().StartAllWorkers(mainCtx) {
			log.Printf("[%s] worker start error: %v\n", chainName, startErr)
		}

	}
	fiberApp := coreApplication.CORE.Router.GetFiber()
	port := os.Getenv("PORT")
	log.Println("App running on", port)
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- fiberApp.Listen(port)
	}()

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(c)
	select {
	case sig := <-c:
		log.Println("Shutting down...", sig)
		go func() {
			sig := <-c
			log.Println("Force shutdown requested...", sig)
			os.Exit(1)
		}()
	case err := <-serverErr:
		log.Fatal(err)
	}

	shutdownTimeout := gatewayShutdownTimeout()
	cancel()
	if err := fiberApp.ShutdownWithTimeout(shutdownTimeout); err != nil {
		log.Println("Fiber shutdown error:", err)
	}
	workerCtx, workerCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	for chainName, stopErr := range coreApplication.CORE.Router.Blockchains().StopAllWorkers(workerCtx) {
		log.Printf("[%s] worker stop error: %v\n", chainName, stopErr)
	}
	workerCancel()
	bus.Shutdown()
}
