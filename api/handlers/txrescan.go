package handlers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"core/constants"
	"core/models"
	"core/services/txrescan"

	"github.com/gofiber/fiber/v3"
)

const txRescanRequestTimeout = 90 * time.Second

func HandleDealerTxRescan(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		merchant, ok := requireDealerMerchant(c, deps.MerchantService)
		if !ok {
			return redirectWithError(c, "/merchant/login", "Devam etmek için giriş yapmalısın.")
		}
		chainID, txHash, err := parseTxRescanInput(c)
		if err != nil {
			return redirectWithError(c, "/merchant/dashboard/rescan", err.Error())
		}
		service := txRescanServiceFromDealerDeps(deps)
		if service == nil {
			return redirectWithError(c, "/merchant/dashboard/rescan", "Tx rescan servisi hazır değil.")
		}
		ctx, cancel := requestTimeout(c, txRescanRequestTimeout)
		defer cancel()
		result, err := service.RescanForMerchant(ctx, chainID, txHash, merchant.ID)
		if err != nil {
			return redirectWithError(c, "/merchant/dashboard/rescan", txRescanErrorMessage(err))
		}
		logDealerActivity(c, deps.ActivityLogRepo, &merchant.ID, "dealer", merchant.Email, "tx.rescan", "success", "transaction", txHash, txRescanSuccessMessage(result))
		return redirectWithSuccess(c, "/merchant/dashboard/rescan", txRescanSuccessMessage(result))
	}
}

func HandleAdminTxRescan(deps DealerDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		adminEmail, ok := requireAdmin(c)
		if !ok {
			return redirectWithError(c, "/admin/login", "Admin girişi gerekli.")
		}
		chainID, txHash, err := parseTxRescanInput(c)
		if err != nil {
			return redirectWithError(c, "/admin/rescan", err.Error())
		}
		service := txRescanServiceFromDealerDeps(deps)
		if service == nil {
			return redirectWithError(c, "/admin/rescan", "Tx rescan servisi hazır değil.")
		}
		ctx, cancel := requestTimeout(c, txRescanRequestTimeout)
		defer cancel()
		result, err := service.Rescan(ctx, chainID, txHash)
		if err != nil {
			return redirectWithError(c, "/admin/rescan", txRescanErrorMessage(err))
		}
		logDealerActivity(c, deps.ActivityLogRepo, nil, "admin", adminEmail, "tx.rescan", "success", "transaction", txHash, txRescanSuccessMessage(result))
		return redirectWithSuccess(c, "/admin/rescan", txRescanSuccessMessage(result))
	}
}

// HandleV1TransactionRescan godoc
// @Summary Rescan transaction
// @Description Re-fetches a transaction from the selected blockchain and replays it through wallet matching, payment, ledger, and webhook processing.
// @Tags Transaction
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param X-API-Secret header string true "API secret returned when the domain was created or rotated"
// @Param X-Gateway-Timestamp header string true "Unix timestamp used in HMAC signature"
// @Param X-Gateway-Signature header string true "HMAC-SHA256 over method + path/query + timestamp + raw body, optionally prefixed with sha256="
// @Param payload body map[string]string true "chain and tx_hash"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} types.V1ErrorResponse
// @Failure 401 {object} types.V1ErrorResponse
// @Failure 404 {object} types.V1ErrorResponse
// @Router /api/v1/transaction/rescan [post]
func HandleV1TransactionRescan(deps V1APIDeps) fiber.Handler {
	return func(c fiber.Ctx) error {
		domain, err := v1ResolveSignedDomainForScope(c, deps.DomainRepo, models.DomainAPIScopeTransactionRescan)
		if err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}
		var body struct {
			Chain   string `json:"chain"`
			ChainID string `json:"chain_id"`
			TxHash  string `json:"tx_hash"`
			Hash    string `json:"hash"`
		}
		if err := c.Bind().Body(&body); err != nil {
			return v1Err(c, fiber.StatusBadRequest, "invalid request body")
		}
		chainValue := strings.TrimSpace(body.Chain)
		if chainValue == "" {
			chainValue = strings.TrimSpace(body.ChainID)
		}
		chainID, err := parseTxRescanChain(chainValue)
		if err != nil {
			return v1Err(c, fiber.StatusBadRequest, err.Error())
		}
		txHash := strings.TrimSpace(body.TxHash)
		if txHash == "" {
			txHash = strings.TrimSpace(body.Hash)
		}
		if txHash == "" {
			return v1Err(c, fiber.StatusBadRequest, "tx_hash is required")
		}
		service := txRescanServiceFromV1Deps(deps)
		if service == nil {
			return v1Err(c, fiber.StatusServiceUnavailable, "tx rescan service is not ready")
		}
		ctx, cancel := requestTimeout(c, txRescanRequestTimeout)
		defer cancel()
		result, err := service.RescanForMerchant(ctx, chainID, txHash, domain.MerchantID)
		if err != nil {
			status := fiber.StatusBadRequest
			if errors.Is(err, txrescan.ErrTransactionNotFound) {
				status = fiber.StatusNotFound
			}
			if errors.Is(err, txrescan.ErrUnauthorizedTx) {
				status = fiber.StatusForbidden
			}
			if txRescanTimedOut(err) {
				status = fiber.StatusGatewayTimeout
			}
			return v1Err(c, status, txRescanErrorMessage(err))
		}
		return v1OK(c, fiber.Map{
			"chain_id":              result.ChainID,
			"chain":                 result.Chain,
			"tx_hash":               result.Hash,
			"events":                result.Events,
			"deposits_created":      result.DepositsCreated,
			"deposits_matched":      result.DepositsMatched,
			"deposits_unmatched":    result.DepositsUnmatched,
			"deposits_finalized":    result.DepositsFinalized,
			"transactions_recorded": result.TransactionsRecorded,
			"payments_settled":      result.PaymentsSettled,
			"unique_hashes":         result.UniqueHashes,
		})
	}
}

func txRescanServiceFromDealerDeps(deps DealerDeps) *txrescan.Service {
	if deps.TxRescanService == nil {
		return nil
	}
	return deps.TxRescanService()
}

func txRescanServiceFromV1Deps(deps V1APIDeps) *txrescan.Service {
	if deps.TxRescanService == nil {
		return nil
	}
	return deps.TxRescanService()
}

func parseTxRescanInput(c fiber.Ctx) (constants.ChainID, string, error) {
	chainID, err := parseTxRescanChain(firstNonEmpty(c.FormValue("chain"), c.FormValue("chain_id")))
	if err != nil {
		return 0, "", err
	}
	txHash := strings.TrimSpace(firstNonEmpty(c.FormValue("tx_hash"), c.FormValue("hash")))
	if txHash == "" {
		return 0, "", errors.New("Tx hash zorunlu.")
	}
	return chainID, txHash, nil
}

func parseTxRescanChain(value string) (constants.ChainID, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New("Blockchain seçmelisin.")
	}
	if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
		chainID := constants.ChainID(parsed)
		if constants.IsSupportedChainID(chainID) {
			return chainID, nil
		}
	}
	if chainID := chainSlugToID(value); constants.IsSupportedChainID(chainID) {
		return chainID, nil
	}
	return 0, fmt.Errorf("Desteklenmeyen blockchain: %s", value)
}

func txRescanSuccessMessage(result *txrescan.Result) string {
	if result == nil {
		return "Tx yeniden tarandı."
	}
	parts := []string{fmt.Sprintf("%s tx yeniden tarandı: %d event işlendi", result.Chain, result.Events)}
	if result.DepositsMatched > 0 {
		parts = append(parts, fmt.Sprintf("%d deposit eşleşti", result.DepositsMatched))
	}
	if result.DepositsUnmatched > 0 {
		parts = append(parts, fmt.Sprintf("%d tx wallet eşleşmedi; deposit yazılmadı", result.DepositsUnmatched))
	}
	if result.TransactionsRecorded > 0 {
		parts = append(parts, fmt.Sprintf("%d transaction kaydedildi", result.TransactionsRecorded))
	}
	if result.DepositsFinalized > 0 {
		parts = append(parts, fmt.Sprintf("%d deposit finalize oldu", result.DepositsFinalized))
	}
	if result.PaymentsSettled > 0 {
		parts = append(parts, fmt.Sprintf("%d ödeme kapandı", result.PaymentsSettled))
	}
	return strings.Join(parts, ", ") + "."
}

func txRescanErrorMessage(err error) string {
	switch {
	case errors.Is(err, txrescan.ErrTransactionNotFound):
		return "Tx blockchain üzerinde bulunamadı."
	case errors.Is(err, txrescan.ErrUnauthorizedTx):
		return "Bu tx üye işyeri wallet adresleriyle eşleşmiyor."
	case errors.Is(err, txrescan.ErrUnsupportedChain):
		return "Bu blockchain için rescan desteklenmiyor."
	case txRescanTimedOut(err):
		return "Rescan zaman aşımına uğradı. İstek gönderildi ancak blockchain RPC yanıtı süresi doldu; tamamlandığı doğrulanamadı. Biraz sonra tekrar dene."
	case errors.Is(err, context.Canceled):
		return "Rescan tamamlanmadan iptal edildi. İşlemin tamamlandığı doğrulanamadı."
	default:
		return err.Error()
	}
}

func txRescanTimedOut(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "context deadline exceeded") ||
		strings.Contains(message, "client.timeout") ||
		strings.Contains(message, "timeout awaiting response headers")
}

func requestTimeout(c fiber.Ctx, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Context(), timeout)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
