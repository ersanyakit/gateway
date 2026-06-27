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
		check := types.V1ReadinessCheck{Name: name, OK: ok, Details: details}
		if err != nil {
			check.Error = err.Error()
		}
		checks = append(checks, check)
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

	csrfOK, csrfDetails, csrfErr := v1PortalCSRFReadiness()
	add("portal.csrf_secret", csrfOK, csrfDetails, csrfErr)

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
		v1AppendChainReadinessChecks(ctx, &checks, chain)
	}

	return checks
}

func v1AppendOperationalReadinessChecks(ctx context.Context, checks *[]types.V1ReadinessCheck, deps V1APIDeps) {
	add := func(name string, ok bool, details string, err error) {
		check := types.V1ReadinessCheck{Name: name, OK: ok, Details: details}
		if err != nil {
			check.Error = err.Error()
		}
		*checks = append(*checks, check)
	}

	webhookStatuses := []string{
		models.WebhookDeliveryStatusPending,
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

func v1MigrationStrategyReadiness() (bool, string, error) {
	if strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV"))) != "production" {
		return true, "non-production AutoMigrate is enabled", nil
	}
	if v1ReadinessEnvBool("ALLOW_AUTOMIGRATE_IN_PRODUCTION") {
		return false, "APP_ENV=production and ALLOW_AUTOMIGRATE_IN_PRODUCTION=true", errors.New("production AutoMigrate override must be disabled before launch")
	}
	return true, "production AutoMigrate is disabled; schema must be externally migrated", nil
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
	if strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV"))) != "production" {
		return true, "non-production signer policy", nil
	}

	signerMode := strings.ToLower(strings.TrimSpace(os.Getenv("SIGNER_MODE")))
	if signerMode == "" {
		signerMode = "software"
	}

	switch signerMode {
	case "software":
		if v1ReadinessEnvBool("ALLOW_SOFTWARE_SIGNER_IN_PRODUCTION") {
			return false, "software signer override is enabled in production", errors.New("production software signer is not launch-ready")
		}
		return false, "software signer is blocked in production", errors.New("external KMS/HSM/MPC signer integration is required before production custody")
	case "kms", "hsm", "mpc":
		return false, fmt.Sprintf("SIGNER_MODE=%s is configured", signerMode), errors.New("external signer mode is declared but not implemented")
	default:
		return false, fmt.Sprintf("SIGNER_MODE=%s", signerMode), errors.New("unsupported signer mode")
	}
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

func v1PortalCSRFReadiness() (bool, string, error) {
	if strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV"))) != "production" {
		return true, "non-production CSRF secret policy", nil
	}
	for _, key := range []string{"CSRF_JWT_SECRET", "DEALER_SESSION_SECRET", "SESSION_SECRET", "MASTER_KEY"} {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			return true, key + " is configured", nil
		}
	}
	return false, "no stable CSRF/session secret is configured", errors.New("production portal CSRF signing requires a stable secret")
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

func v1AppendChainReadinessChecks(ctx context.Context, checks *[]types.V1ReadinessCheck, chain blockchain.Chain) {
	add := func(name string, ok bool, details string, err error) {
		check := types.V1ReadinessCheck{Name: name, OK: ok, Details: details}
		if err != nil {
			check.Error = err.Error()
		}
		*checks = append(*checks, check)
	}

	prefix := "chain." + chain.Name()
	if chain.ChainID() == constants.TRON {
		add(prefix+".rpc_config", len(v1TronGRPCEndpoints()) > 0, "TRON gRPC endpoint configured", nil)
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

	details, err := v1ProbeChainRPC(ctx, chain)
	if err != nil {
		add(prefix+".rpc_live", false, "", err)
		return
	}
	add(prefix+".rpc_live", true, details, nil)
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
	case constants.TRON:
		return v1ProbeTronGRPC(ctx)
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

func v1ProbeTronGRPC(ctx context.Context) (string, error) {
	apiKey := strings.TrimSpace(os.Getenv("TRON_PRO_API_KEY"))
	var lastErr error
	for _, endpoint := range v1TronGRPCEndpoints() {
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
	raw := strings.TrimSpace(os.Getenv("TRON_GRPC_ENDPOINTS"))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("TRON_GRPC_ENDPOINT"))
	}
	if raw == "" {
		return []string{"grpc.trongrid.io:50051"}
	}

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
