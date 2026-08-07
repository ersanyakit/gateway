package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"core/blockchain"
	"core/constants"
	"core/models"
	"core/services/dbmigrations"
	"core/services/signer"
	"core/types"

	"github.com/gofiber/fiber/v3"
	"github.com/okx/go-wallet-sdk/coins/tron/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

const v1ReadinessTimeout = 20 * time.Second

type v1JSONRPCResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *v1JSONRPCError `json:"error"`
}

type v1JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type v1TronEmptyMessage struct{}

func (*v1TronEmptyMessage) Reset()         {}
func (*v1TronEmptyMessage) String() string { return "{}" }
func (*v1TronEmptyMessage) ProtoMessage()  {}

// HandleV1CommonReadiness godoc
// @Summary Production readiness
// @Description Validates database access, chain registration, listener registration, live RPC access, and Trust Wallet Core HD wallet derivation.
// @Tags Common
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} types.V1ReadinessResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Failure 503 {object} types.V1ReadinessResponse
// @Router /api/v1/common/readiness [get]
func HandleV1CommonReadiness(deps V1APIDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		if deps.DomainRepo == nil {
			return v1Err(c, fiber.StatusServiceUnavailable, "domain repository is not configured")
		}
		if _, err := v1ResolveDomain(c, deps.DomainRepo); err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}

		ctx, cancel := context.WithTimeout(c.Context(), v1ReadinessTimeout)
		defer cancel()

		checks := v1RunReadinessChecks(ctx, deps)
		ready := v1ReadinessOK(checks)
		status := fiber.StatusOK
		if !ready {
			status = fiber.StatusServiceUnavailable
		}

		return c.Status(status).JSON(fiber.Map{
			"result": "ok",
			"data": types.V1ReadinessData{
				Ready:     ready,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
				Checks:    checks,
			},
		})
	}
}

func v1RunReadinessChecks(ctx context.Context, deps V1APIDeps) []types.V1ReadinessCheck {
	checks := make([]types.V1ReadinessCheck, 0, 64)
	add := func(name string, ok bool, details string, err error) {
		v1AppendReadinessCheck(&checks, name, ok, details, err)
	}

	if deps.DomainRepo == nil || deps.DomainRepo.DB() == nil {
		add("database", false, "", errors.New("database is not configured"))
	} else if err := deps.DomainRepo.DB().WithContext(ctx).Exec("SELECT 1").Error; err != nil {
		add("database", false, "", err)
	} else {
		add("database", true, "database query succeeded", nil)
	}

	migrationOK, migrationDetails, migrationErr := v1MigrationStrategyReadiness()
	add("migration.strategy", migrationOK, migrationDetails, migrationErr)

	signerOK, signerDetails, signerErr := v1ProductionSignerReadiness()
	add("signer.production", signerOK, signerDetails, signerErr)

	metricsOK, metricsDetails, metricsErr := v1MetricsAccessReadiness()
	add("metrics.access", metricsOK, metricsDetails, metricsErr)

	portalJWTOK, portalJWTDetails, portalJWTErr := v1PortalJWTReadiness()
	add("portal.jwt_secret", portalJWTOK, portalJWTDetails, portalJWTErr)

	v1AppendOperationalReadinessChecks(ctx, &checks, deps)

	if deps.Blockchains == nil {
		add("chain.registry", false, "", errors.New("blockchain factory is not configured"))
		return checks
	}

	chainNames := deps.Blockchains.ListChains()
	if len(chainNames) == 0 {
		add("chain.registry", false, "", errors.New("no chains are registered"))
		return checks
	}

	missing := make([]string, 0)
	for _, chainID := range constants.AllChainIDs() {
		if _, err := deps.Blockchains.GetChainByID(chainID); err != nil {
			missing = append(missing, fmt.Sprintf("%s(%d)", constants.ChainName(chainID), chainID))
		}
	}
	if len(missing) > 0 {
		add("chain.registry", false, "registered chains: "+strings.Join(chainNames, ", "), fmt.Errorf("missing chains: %s", strings.Join(missing, ", ")))
	} else {
		add("chain.registry", true, fmt.Sprintf("%d chains registered", len(chainNames)), nil)
	}

	for _, chainName := range chainNames {
		chain, err := deps.Blockchains.GetChain(chainName)
		if err != nil {
			add("chain."+chainName+".registered", false, "", err)
			continue
		}
		v1AppendChainReadinessChecks(ctx, &checks, chain, deps.ProviderHealthRepo)
	}

	return checks
}

func v1AppendOperationalReadinessChecks(ctx context.Context, checks *[]types.V1ReadinessCheck, deps V1APIDeps) {
	add := func(name string, ok bool, details string, err error) {
		v1AppendReadinessCheck(checks, name, ok, details, err)
	}

	if deps.MoneyEventOutboxRepo == nil {
		add("money_event.outbox_backlog", false, "", errors.New("money event outbox repository is not configured"))
	} else {
		v1AppendMoneyEventOutboxReadiness(ctx, checks, deps.MoneyEventOutboxRepo)
	}

	webhookStatuses := []string{
		models.WebhookDeliveryStatusPending,
		models.WebhookDeliveryStatusProcessing,
		models.WebhookDeliveryStatusFailed,
		models.WebhookDeliveryStatusDeadLetter,
	}
	if deps.WebhookDeliveryRepo == nil {
		add("webhook.delivery_backlog", false, "", errors.New("webhook delivery repository is not configured"))
	} else if counts, err := deps.WebhookDeliveryRepo.CountByStatus(ctx, webhookStatuses...); err != nil {
		add("webhook.delivery_backlog", false, "", err)
	} else {
		add(
			"webhook.delivery_backlog",
			counts[models.WebhookDeliveryStatusDeadLetter] == 0,
			v1ReadinessCountDetails(counts, webhookStatuses...),
			nil,
		)
	}

	sweepStatuses := []string{
		models.SweepJobStatusPending,
		models.SweepJobStatusProcessing,
		models.SweepJobStatusFailed,
		models.SweepJobStatusDeadLetter,
	}
	if deps.SweepJobRepo == nil {
		add("sweep.job_backlog", false, "", errors.New("sweep job repository is not configured"))
	} else if counts, err := deps.SweepJobRepo.CountByStatus(ctx, sweepStatuses...); err != nil {
		add("sweep.job_backlog", false, "", err)
	} else {
		add(
			"sweep.job_backlog",
			counts[models.SweepJobStatusDeadLetter] == 0,
			v1ReadinessCountDetails(counts, sweepStatuses...),
			nil,
		)
	}

	reconciliationStatuses := []string{
		models.ReconciliationStatusOpen,
		models.ReconciliationStatusProcessing,
		models.ReconciliationStatusNeedsOperatorAction,
		models.ReconciliationStatusRetryScheduled,
		models.ReconciliationStatusFailed,
	}
	if deps.ReconciliationRepo == nil {
		add("reconciliation.drift", false, "", errors.New("reconciliation repository is not configured"))
	} else if counts, err := deps.ReconciliationRepo.CountByStatus(ctx, reconciliationStatuses...); err != nil {
		add("reconciliation.drift", false, "", err)
	} else {
		total := v1ReadinessCountTotal(counts, reconciliationStatuses...)
		add(
			"reconciliation.drift",
			total == 0,
			v1ReadinessCountDetails(counts, reconciliationStatuses...),
			nil,
		)
	}
}

type v1ReadinessStatusCounter interface {
	CountByStatus(context.Context, ...string) (map[string]int64, error)
}

func v1AppendMoneyEventOutboxReadiness(ctx context.Context, checks *[]types.V1ReadinessCheck, counter v1ReadinessStatusCounter) {
	statuses := []string{
		models.MoneyEventOutboxStatusPending,
		models.MoneyEventOutboxStatusProcessing,
		models.MoneyEventOutboxStatusFailed,
		models.MoneyEventOutboxStatusDeadLetter,
	}
	if counter == nil {
		v1AppendReadinessCheck(checks, "money_event.outbox_backlog", false, "", errors.New("money event outbox repository is not configured"))
		return
	}
	counts, err := counter.CountByStatus(ctx, statuses...)
	if err != nil {
		v1AppendReadinessCheck(checks, "money_event.outbox_backlog", false, "", err)
		return
	}
	v1AppendReadinessCheck(
		checks,
		"money_event.outbox_backlog",
		counts[models.MoneyEventOutboxStatusDeadLetter] == 0,
		v1ReadinessCountDetails(counts, statuses...),
		nil,
	)
}

func v1MigrationStrategyReadiness() (bool, string, error) {
	latestMigrationID, err := dbmigrations.LatestID()
	if err != nil {
		return false, "versioned migration artifacts are invalid", err
	}
	if strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV"))) != "production" {
		return true, "non-production AutoMigrate is enabled; latest migration artifact " + latestMigrationID, nil
	}
	if v1ReadinessEnvBool("ALLOW_AUTOMIGRATE_IN_PRODUCTION") {
		return false, "APP_ENV=production and ALLOW_AUTOMIGRATE_IN_PRODUCTION=true", errors.New("production AutoMigrate override must be disabled before launch")
	}
	appliedMigrationID := strings.TrimSpace(os.Getenv("GATEWAY_DB_MIGRATION_VERSION"))
	if appliedMigrationID == "" {
		return false, "production AutoMigrate is disabled but GATEWAY_DB_MIGRATION_VERSION is empty", fmt.Errorf("production schema migration evidence must reference latest migration %s", latestMigrationID)
	}
	if appliedMigrationID != latestMigrationID {
		return false, "production migration evidence is stale: " + appliedMigrationID, fmt.Errorf("latest required migration is %s", latestMigrationID)
	}
	return true, "production AutoMigrate is disabled; versioned migration evidence " + appliedMigrationID, nil
}

func v1ReadinessEnvBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func v1ProductionSignerReadiness() (bool, string, error) {
	return signer.ProductionReadiness()
}

func v1MetricsAccessReadiness() (bool, string, error) {
	if strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV"))) != "production" {
		return true, "non-production metrics access policy", nil
	}
	if strings.TrimSpace(os.Getenv("METRICS_BEARER_TOKEN")) == "" {
		return false, "METRICS_BEARER_TOKEN is empty in production", errors.New("production metrics endpoint must be protected")
	}
	return true, "production metrics bearer token is configured", nil
}

func v1PortalJWTReadiness() (bool, string, error) {
	if strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV"))) != "production" {
		return true, "non-production portal JWT secret policy", nil
	}
	for _, key := range []string{"PORTAL_JWT_SECRET", "DEALER_SESSION_SECRET", "SESSION_SECRET", "MASTER_KEY"} {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			return true, key + " is configured", nil
		}
	}
	return false, "no stable portal JWT/session secret is configured", errors.New("production portal JWT signing requires a stable secret")
}

func v1ReadinessCountDetails(counts map[string]int64, statuses ...string) string {
	parts := make([]string, 0, len(statuses))
	for _, status := range statuses {
		parts = append(parts, fmt.Sprintf("%s=%d", status, counts[status]))
	}
	return strings.Join(parts, ", ")
}

func v1ReadinessCountTotal(counts map[string]int64, statuses ...string) int64 {
	var total int64
	for _, status := range statuses {
		total += counts[status]
	}
	return total
}

func v1AppendReadinessCheck(checks *[]types.V1ReadinessCheck, name string, ok bool, details string, err error) {
	check := types.V1ReadinessCheck{Name: name, OK: ok, Details: v1RedactReadinessText(details)}
	if err != nil {
		check.Error = v1RedactReadinessText(err.Error())
	}
	*checks = append(*checks, check)
}

func v1RedactReadinessText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	for _, marker := range []string{
		"api_secret",
		"webhook_secret",
		"x-api-secret",
		"authorization",
		"raw_signature",
		"signature=",
		"mnemonic",
		"private_key",
		"raw_tx",
		"signed_tx",
	} {
		if strings.Contains(lower, marker) {
			return "[redacted]"
		}
	}
	return value
}

type v1ProviderHealthLister interface {
	ListByChain(ctx context.Context, chainID constants.ChainID) ([]models.ProviderHealthSnapshot, error)
}

func v1AppendChainReadinessChecks(ctx context.Context, checks *[]types.V1ReadinessCheck, chain blockchain.Chain, providerHealth v1ProviderHealthLister) {
	add := func(name string, ok bool, details string, err error) {
		v1AppendReadinessCheck(checks, name, ok, details, err)
	}

	prefix := "chain." + chain.Name()
	if constants.IsTRONChain(chain.ChainID()) {
		add(prefix+".rpc_config", len(v1TronGRPCEndpointsForChain(chain.Name())) > 0, "TRON gRPC endpoint configured", nil)
	} else {
		rpcCount := len(chain.RPCs())
		if rpcCount == 0 {
			add(prefix+".rpc_config", false, "", errors.New("no RPC endpoint configured"))
		} else {
			add(prefix+".rpc_config", true, fmt.Sprintf("%d RPC endpoint(s) configured", rpcCount), nil)
		}
	}

	workerCount := chain.WorkerCount()
	if workerCount == 0 {
		add(prefix+".worker", false, "", errors.New("no listener worker registered"))
	} else {
		add(prefix+".worker", true, fmt.Sprintf("%d listener worker(s) registered", workerCount), nil)
	}

	wallet, err := chain.CreateHDWallet(ctx, 0, 0)
	switch {
	case err != nil:
		add(prefix+".wallet_derivation", false, "", err)
	case wallet == nil || strings.TrimSpace(wallet.Address) == "":
		add(prefix+".wallet_derivation", false, "", errors.New("derived wallet address is empty"))
	case !chain.ValidateAddress(wallet.Address):
		add(prefix+".wallet_derivation", false, "", fmt.Errorf("derived wallet address is invalid for %s", chain.Name()))
	default:
		add(prefix+".wallet_derivation", true, "Trust Wallet Core HD derivation succeeded", nil)
	}

	v1AppendProviderHealthReadiness(ctx, add, prefix, chain, providerHealth)
}

func v1AppendProviderHealthReadiness(ctx context.Context, add func(string, bool, string, error), prefix string, chain blockchain.Chain, providerHealth v1ProviderHealthLister) {
	if providerHealth == nil {
		add(prefix+".provider_health", false, "", errors.New("provider health repository is not configured"))
		return
	}
	snapshots, err := providerHealth.ListByChain(ctx, chain.ChainID())
	if err != nil {
		add(prefix+".provider_health", false, "", err)
		return
	}
	if len(snapshots) == 0 {
		details := "provider health snapshot unavailable"
		if strings.EqualFold(strings.TrimSpace(os.Getenv("APP_ENV")), "production") {
			add(prefix+".provider_health", false, details, errors.New("provider health snapshot is required in production"))
			return
		}
		add(prefix+".provider_health", true, details+"; non-production observe mode", nil)
		return
	}
	total := len(snapshots)
	healthy := 0
	degraded := 0
	unhealthy := 0
	selected := ""
	var worstErr error
	for _, snapshot := range snapshots {
		switch snapshot.Status {
		case models.ProviderHealthStatusHealthy:
			healthy++
		case models.ProviderHealthStatusDegraded:
			degraded++
			if worstErr == nil && snapshot.ErrorCategory != "" {
				worstErr = errors.New(snapshot.ErrorCategory)
			}
		default:
			unhealthy++
			if worstErr == nil && snapshot.ErrorCategory != "" {
				worstErr = errors.New(snapshot.ErrorCategory)
			}
		}
		if snapshot.Selected {
			selected = snapshot.ProviderLabel
		}
	}
	details := fmt.Sprintf("providers=%d healthy=%d degraded=%d unhealthy=%d selected=%s", total, healthy, degraded, unhealthy, emptyReadinessValue(selected))
	ok := healthy > 0 && unhealthy == 0
	if strings.EqualFold(strings.TrimSpace(os.Getenv("APP_ENV")), "production") {
		ok = healthy > 0 && degraded == 0 && unhealthy == 0
	}
	add(prefix+".provider_health", ok, details, worstErr)
}

func emptyReadinessValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}

func v1ReadinessOK(checks []types.V1ReadinessCheck) bool {
	if len(checks) == 0 {
		return false
	}
	for _, check := range checks {
		if !check.OK {
			return false
		}
	}
	return true
}

func v1ProbeChainRPC(ctx context.Context, chain blockchain.Chain) (string, error) {
	switch chain.ChainID() {
	case constants.Bitcoin:
		return v1ProbeBitcoinREST(ctx, chain.RPCs())
	case constants.Solana:
		return v1ProbeSolanaRPC(ctx, chain.RPCs())
	case constants.TRON, constants.TRONTestnet:
		return v1ProbeTronGRPC(ctx, chain.Name())
	default:
		return v1ProbeEVMRPC(ctx, chain.RPCs())
	}
}

func v1ProbeEVMRPC(ctx context.Context, rpcs []string) (string, error) {
	var result string
	if err := v1JSONRPCCall(ctx, rpcs, "eth_blockNumber", []interface{}{}, &result); err != nil {
		return "", err
	}
	block, err := v1ParseHexBlockNumber(result)
	if err != nil {
		return "", err
	}
	if block <= 0 {
		return "", fmt.Errorf("latest block is not positive: %d", block)
	}
	return fmt.Sprintf("latest block %d", block), nil
}

func v1ProbeSolanaRPC(ctx context.Context, rpcs []string) (string, error) {
	var slot int64
	if err := v1JSONRPCCall(ctx, rpcs, "getSlot", []interface{}{map[string]interface{}{"commitment": "finalized"}}, &slot); err != nil {
		return "", err
	}
	if slot <= 0 {
		return "", fmt.Errorf("latest slot is not positive: %d", slot)
	}
	return fmt.Sprintf("latest slot %d", slot), nil
}

func v1ProbeBitcoinREST(ctx context.Context, rpcs []string) (string, error) {
	client := &http.Client{Timeout: 8 * time.Second}
	var lastErr error
	for _, rpcURL := range rpcs {
		rpcURL = strings.TrimSpace(rpcURL)
		if rpcURL == "" {
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(rpcURL, "/")+"/blocks/tip/height", nil)
		if err != nil {
			lastErr = err
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("bitcoin API returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
			continue
		}
		height, err := strconv.ParseInt(strings.TrimSpace(string(body)), 10, 64)
		if err != nil {
			lastErr = fmt.Errorf("parse latest bitcoin height: %w", err)
			continue
		}
		if height <= 0 {
			lastErr = fmt.Errorf("latest bitcoin height is not positive: %d", height)
			continue
		}
		return fmt.Sprintf("latest height %d", height), nil
	}
	if lastErr == nil {
		lastErr = errors.New("no bitcoin API endpoint configured")
	}
	return "", lastErr
}

func v1ProbeTronGRPC(ctx context.Context, chainName string) (string, error) {
	apiKey := strings.TrimSpace(os.Getenv("TRON_PRO_API_KEY"))
	var lastErr error
	for _, endpoint := range v1TronGRPCEndpointsForChain(chainName) {
		conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			lastErr = err
			continue
		}

		callCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		if apiKey != "" {
			callCtx = metadata.NewOutgoingContext(callCtx, metadata.Pairs("TRON-PRO-API-KEY", apiKey))
		}
		out := new(pb.Block)
		err = conn.Invoke(callCtx, "/protocol.Wallet/GetNowBlock", &v1TronEmptyMessage{}, out, grpc.MaxCallRecvMsgSize(32*1024*1024))
		cancel()
		_ = conn.Close()
		if err != nil {
			lastErr = err
			continue
		}

		block := out.GetBlockHeader().GetRawData().GetNumber()
		if block <= 0 {
			lastErr = fmt.Errorf("latest tron block is not positive: %d", block)
			continue
		}
		return fmt.Sprintf("latest block %d via %s", block, endpoint), nil
	}
	if lastErr == nil {
		lastErr = errors.New("no tron gRPC endpoint configured")
	}
	return "", lastErr
}

func v1JSONRPCCall(ctx context.Context, rpcs []string, method string, params []interface{}, out interface{}) error {
	body, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      time.Now().UnixNano(),
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 8 * time.Second}
	var lastErr error
	for _, rpcURL := range rpcs {
		rpcURL = strings.TrimSpace(rpcURL)
		if rpcURL == "" {
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, bytes.NewReader(body))
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("RPC returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
			continue
		}

		var rpcResp v1JSONRPCResponse
		if err := json.Unmarshal(respBody, &rpcResp); err != nil {
			lastErr = err
			continue
		}
		if rpcResp.Error != nil {
			lastErr = fmt.Errorf("RPC %s error %d: %s", method, rpcResp.Error.Code, rpcResp.Error.Message)
			continue
		}
		if out == nil || string(rpcResp.Result) == "null" {
			return nil
		}
		if err := json.Unmarshal(rpcResp.Result, out); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr == nil {
		lastErr = errors.New("no RPC endpoint configured")
	}
	return lastErr
}

func v1ParseHexBlockNumber(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "0x")
	if raw == "" {
		return 0, errors.New("empty hex block number")
	}
	return strconv.ParseInt(raw, 16, 64)
}

func v1TronGRPCEndpoints() []string {
	return v1TronGRPCEndpointsForChain("tron")
}

func v1TronGRPCEndpointsForChain(chainName string) []string {
	if strings.EqualFold(strings.TrimSpace(chainName), "tron-testnet") {
		raw := strings.TrimSpace(os.Getenv("TRON_TESTNET_GRPC_ENDPOINTS"))
		if raw == "" {
			raw = strings.TrimSpace(os.Getenv("TRON_TESTNET_GRPC_ENDPOINT"))
		}
		if raw == "" {
			return []string{"grpc.nile.trongrid.io:50051"}
		}
		return splitV1TronGRPCEndpoints(raw)
	}

	raw := strings.TrimSpace(os.Getenv("TRON_GRPC_ENDPOINTS"))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("TRON_GRPC_ENDPOINT"))
	}
	if raw == "" {
		return []string{"grpc.trongrid.io:50051"}
	}

	return splitV1TronGRPCEndpoints(raw)
}

func splitV1TronGRPCEndpoints(raw string) []string {
	parts := strings.Split(raw, ",")
	endpoints := make([]string, 0, len(parts))
	for _, part := range parts {
		endpoint := strings.TrimSpace(part)
		endpoint = strings.TrimPrefix(endpoint, "grpc://")
		if endpoint != "" {
			endpoints = append(endpoints, endpoint)
		}
	}
	return endpoints
}
