// routes/router.go
package routes

import (
	"core/api/handlers"
	"core/api/middleware"
	"core/api/router"
	configurations "core/application/configuration"
	"core/asset"
	"core/blockchain"
	"core/constants"
	"core/repositories"
	"core/services/pricing"
	"core/services/realtime"
	services "core/services/system"
	webhooksvc "core/services/webhook"
	"fmt"
	"strings"

	"github.com/bytedance/sonic"
	fiber "github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/paginate"
	staticmw "github.com/gofiber/fiber/v3/middleware/static"
	"github.com/gofiber/template/html/v3"
	"gorm.io/gorm"

	_ "core/docs"

	"github.com/gofiber/contrib/v3/swaggo"
)

// swag init ile üretilen dosyalar

type Router struct {
	fiber         *fiber.App
	action        *router.ActionRouter
	db            *gorm.DB
	blockchains   *blockchain.ChainFactory
	assetRegistry *asset.Registry

	MerchantRepo        *repositories.MerchantRepo
	DomainRepo          *repositories.DomainRepo
	WalletRepo          *repositories.WalletRepo
	ChainStateRepo      *repositories.ChainStateRepo
	TransactionRepo     *repositories.TransactionRepo
	PaymentRepo         *repositories.PaymentRepo
	ProductRepo         *repositories.ProductRepo
	WithdrawalRepo      *repositories.WithdrawalRequestRepo
	LedgerRepo          *repositories.LedgerRepo
	IdempotencyRepo     *repositories.IdempotencyRepo
	WebhookDeliveryRepo *repositories.WebhookDeliveryRepo
	RefundRepo          *repositories.RefundRepo
	ActivityLogRepo     *repositories.ActivityLogRepo
	AdminRepo           *repositories.AdminRepo
	MerchantService     *services.MerchantService
	WalletService       *services.WalletService
	DomainService       *services.DomainService
	PaymentHub          *realtime.PaymentHub
}

func NewRouter(db *gorm.DB) *Router {

	var sonicAPI = sonic.Config{
		EscapeHTML: false,
	}.Froze()
	engine := html.New("./views", ".html")
	engine.Reload(false)
	engine.Debug(false)

	r := &Router{
		action: router.NewActionRouter(db),
		db:     db,
		fiber: fiber.New(fiber.Config{
			JSONEncoder:     sonicAPI.Marshal,   // use sonic for JSON encoding
			JSONDecoder:     sonicAPI.Unmarshal, // use sonic for JSON decoding
			ServerHeader:    constants.PRODUCT_NAME,
			AppName:         constants.PRODUCT_NAME + " " + constants.PRODUCT_VERSION,
			Views:           engine,
			ReadBufferSize:  8192,
			WriteBufferSize: 8192,
			CaseSensitive:   true,
			StrictRouting:   true,
			Immutable:       true,
			BodyLimit:       4 * 1024 * 1024, // 10MB
		}),
		assetRegistry: configurations.NewAssetRegistry(),
		blockchains:   configurations.NewChainFactory(),
	}

	r.fiber.Use(paginate.New(
		paginate.Config{
			PageKey:      "page",
			LimitKey:     "limit",
			DefaultPage:  1,
			DefaultLimit: constants.DEFAULT_LIMIT,
			MaxLimit:     constants.MAXIMUM_LIMIT,
		},
	))

	r.fiber.Use(middleware.SecurityHeaders())

	allowedOrigins := middleware.AllowedCORSOrigins()
	r.fiber.Use(cors.New(cors.Config{
		AllowOriginsFunc: func(origin string) bool {
			return middleware.IsOriginAllowed(origin, allowedOrigins)
		},
		AllowMethods: []string{"POST", "GET", "OPTIONS", "PUT", "DELETE"},
		AllowHeaders: []string{
			"Accept", "Authorization", "Content-Type", "Content-Length",
			"X-API-Key", "X-Gateway-Signature", "X-Gateway-Timestamp", "X-Gateway-Event",
			"X-CSRF-Token", "Token", "session", "Origin", "Host", "Connection",
			"Accept-Encoding", "Accept-Language", "X-Requested-With",
			"AMP-Redirect-To", "__amp_source_origin", "Access-Control-Allow-Origin",
		},
		ExposeHeaders: []string{
			"AMP-Redirect-To",
			"Access-Control-Allow-Origin",
			"AMP-Access-Control-Allow-Source-Origin",
			"Access-Control-Allow-Source-Origin",
		},
		AllowCredentials: true,
	}))

	r.fiber.Use("/api/v1", middleware.RateLimitAPIKey())

	r.ChainStateRepo = repositories.NewChainStateRepo(r.db)
	r.TransactionRepo = repositories.NewTransactionRepo(r.db)
	r.PaymentRepo = repositories.NewPaymentRepo(r.db)
	r.ProductRepo = repositories.NewProductRepo(r.db)
	r.WithdrawalRepo = repositories.NewWithdrawalRequestRepo(r.db)
	r.LedgerRepo = repositories.NewLedgerRepo(r.db)
	r.IdempotencyRepo = repositories.NewIdempotencyRepo(r.db)
	r.WebhookDeliveryRepo = repositories.NewWebhookDeliveryRepo(r.db)
	r.RefundRepo = repositories.NewRefundRepo(r.db)
	r.ActivityLogRepo = repositories.NewActivityLogRepo(r.db)
	r.AdminRepo = repositories.NewAdminRepo(r.db)
	r.PaymentHub = realtime.NewPaymentHub()
	r.MerchantRepo = repositories.NewMerchantRepo(r.db, r.blockchains)
	r.MerchantService = services.NewMerchantService(r.MerchantRepo)

	r.DomainRepo = repositories.NewDomainRepo(r.MerchantRepo)
	r.DomainService = services.NewDomainService(r.DomainRepo)

	r.WalletRepo = repositories.NewWalletRepo(r.DomainRepo)
	r.WalletService = services.NewWalletService(r.WalletRepo)

	r.fiber.Use("/assets", staticmw.New("./views/assets"))
	r.fiber.Use("/static", staticmw.New("./static"))

	r.fiber.Post(constants.CMD_MERCHANT_CREATE.String(), handlers.HandleMerchantCreate(r.MerchantService))
	r.fiber.Post(constants.CMD_MERCHANT_FETCH.String(), handlers.HandleMerchantFetch(r.MerchantService))

	r.fiber.Post(constants.CMD_MERCHANT_DOMAIN_CREATE.String(), handlers.HandleDomainCreate(r.DomainService))
	r.fiber.Post(constants.CMD_DOMAIN_DEPOSIT_SUMMARY.String(), handlers.HandleDomainDepositSummary(r.TransactionRepo))

	r.fiber.Post(constants.CMD_MERCHANT_FETCH_BY_ID.String(), handlers.HandleMerchantFindById(r.MerchantService))
	r.fiber.Post(constants.CMD_MERCHANT_FETCH_BY_EMAIL.String(), handlers.HandleMerchantFindByEmail(r.MerchantService))

	r.fiber.Post(constants.CMD_MERCHANT_DELETE_BY_ID.String(), handlers.HandleMerchantDeleteById(r.MerchantService))
	r.fiber.Post(constants.CMD_MERCHANT_DELETE_BY_EMAIL.String(), handlers.HandleMerchantDeleteByEmail(r.MerchantService))

	r.fiber.Post(constants.CMD_MERCHANT_WALLET_CREATE.String(), handlers.HandleWalletCreate(r.WalletService))
	r.fiber.Post(constants.CMD_WITHDRAW.String(), handlers.HandleWithdraw(r.WalletRepo, r.blockchains))
	r.fiber.Post(constants.CMD_SWEEP.String(), handlers.HandleSweep(r.WalletRepo, r.blockchains))

	r.fiber.Get("/", handlers.HandleDealerHome())
	r.fiber.Get("/dealer/login", handlers.HandleDealerLogin())
	r.fiber.Post("/dealer/login", handlers.HandleDealerLoginSubmit(r.MerchantService, r.ActivityLogRepo))
	r.fiber.Get("/dealer/register", handlers.HandleDealerRegister())
	dealerDeps := handlers.DealerDeps{
		MerchantService:     r.MerchantService,
		DomainService:       r.DomainService,
		WalletRepo:          r.WalletRepo,
		ProductRepo:         r.ProductRepo,
		PaymentRepo:         r.PaymentRepo,
		WithdrawalRepo:      r.WithdrawalRepo,
		RefundRepo:          r.RefundRepo,
		LedgerRepo:          r.LedgerRepo,
		TransactionRepo:     r.TransactionRepo,
		WebhookDeliveryRepo: r.WebhookDeliveryRepo,
		ActivityLogRepo:     r.ActivityLogRepo,
		AdminRepo:           r.AdminRepo,
		AssetRegistry:       r.assetRegistry,
		Blockchains:         r.blockchains,
		Notifier:            webhooksvc.NewNotifier(),
		PriceOracle:         pricing.NewCoinGecko(),
	}
	r.fiber.Post("/dealer/register", handlers.HandleDealerRegisterSubmit(dealerDeps))
	r.fiber.Get("/dealer", handlers.HandleDealerDashboard(dealerDeps))
	r.fiber.Get("/dealer/dashboard", handlers.HandleDealerDashboard(dealerDeps))
	r.fiber.Get("/dealer/dashboard/:section", handlers.HandleDealerDashboard(dealerDeps))
	r.fiber.Get("/dealer/domains", handlers.HandleDealerDashboard(dealerDeps))
	r.fiber.Post("/dealer/domains", handlers.HandleDealerDomainCreate(r.MerchantService, r.DomainService, r.ActivityLogRepo))
	r.fiber.Post("/dealer/products", handlers.HandleDealerProductCreate(dealerDeps))
	r.fiber.Post("/dealer/invoices", handlers.HandleDealerInvoiceCreate(dealerDeps))
	r.fiber.Post("/dealer/withdrawals", handlers.HandleDealerWithdrawalCreate(dealerDeps))
	r.fiber.Post("/dealer/wallets/:id/fill-address", handlers.HandleDealerFillWalletAddress(dealerDeps))
	r.fiber.Post("/dealer/domains/:id/test-webhook", handlers.HandleDealerWebhookTest(dealerDeps))
	r.fiber.Post("/dealer/domains/:id/update-webhook", handlers.HandleDealerDomainUpdateWebhook(dealerDeps))
	r.fiber.Post("/dealer/settings", handlers.HandleDealerSettingsUpdate(dealerDeps))
	r.fiber.Get("/dealer/onboarding", handlers.HandleDealerOnboarding())
	r.fiber.Get("/dealer/logout", handlers.HandleDealerLogout(r.MerchantService, r.ActivityLogRepo))
	r.fiber.Get("/auth/oidc/login", handlers.HandleOIDCLogin())
	r.fiber.Get("/auth/oidc/callback", handlers.HandleOIDCCallback(r.MerchantService, r.ActivityLogRepo, dealerDeps))
	r.fiber.Get("/admin/login", handlers.HandleAdminLogin())
	r.fiber.Post("/admin/login", handlers.HandleAdminLoginSubmit(r.AdminRepo))
	r.fiber.Get("/admin/logout", handlers.HandleAdminLogout())
	r.fiber.Get("/admin/2fa/setup", handlers.HandleAdminTOTPSetup(r.AdminRepo))
	r.fiber.Post("/admin/2fa/setup", handlers.HandleAdminTOTPSetupSubmit(r.AdminRepo))
	r.fiber.Get("/admin/2fa/verify", handlers.HandleAdminTOTPVerify(r.AdminRepo))
	r.fiber.Post("/admin/2fa/verify", handlers.HandleAdminTOTPVerifySubmit(r.AdminRepo))
	r.fiber.Get("/admin/admins", handlers.HandleAdminManageAdmins(dealerDeps))
	r.fiber.Post("/admin/admins", handlers.HandleAdminCreateAdmin(dealerDeps))
	r.fiber.Post("/admin/admins/:id/toggle", handlers.HandleAdminToggleAdmin(dealerDeps))
	r.fiber.Post("/admin/admins/:id/reset-totp", handlers.HandleAdminResetTOTP(dealerDeps))
	r.fiber.Post("/admin/merchants/:id/toggle", handlers.HandleAdminMerchantToggle(dealerDeps))
	r.fiber.Get("/admin/links", handlers.HandleAdminDashboard(dealerDeps))
	r.fiber.Get("/admin/security", handlers.HandleAdminDashboard(dealerDeps))
	r.fiber.Post("/admin/security/2fa/enable", handlers.HandleAdminTOTPEnable(dealerDeps))
	r.fiber.Get("/admin/security/2fa/disable", handlers.HandleAdminTOTPDisableConfirm(dealerDeps))
	r.fiber.Post("/admin/security/2fa/disable", handlers.HandleAdminTOTPDisableSubmit(dealerDeps))
	r.fiber.Post("/admin/withdrawals/:id/approve", handlers.HandleAdminWithdrawalApprove(dealerDeps))
	r.fiber.Post("/admin/withdrawals/:id/reject", handlers.HandleAdminWithdrawalReject(dealerDeps))
	r.fiber.Post("/admin/refunds/:id/approve", handlers.HandleAdminRefundApprove(dealerDeps))
	r.fiber.Post("/admin/refunds/:id/reject", handlers.HandleAdminRefundReject(dealerDeps))
	r.fiber.Post("/admin/webhooks/:id/replay", handlers.HandleAdminWebhookReplay(dealerDeps))
	r.fiber.Post("/admin/sweep", handlers.HandleAdminSweep(dealerDeps))
	r.fiber.Get("/admin", handlers.HandleAdminDashboard(dealerDeps))
	r.fiber.Get("/admin/:section", handlers.HandleAdminDashboard(dealerDeps))

	paymentDeps := handlers.PaymentHandlerDeps{
		DomainRepo:      r.DomainRepo,
		WalletRepo:      r.WalletRepo,
		PaymentRepo:     r.PaymentRepo,
		ProductRepo:     r.ProductRepo,
		AssetRegistry:   r.assetRegistry,
		PriceOracle:     pricing.NewCoinGecko(),
		Notifier:        webhooksvc.NewNotifier(),
		PaymentHub:      r.PaymentHub,
		IdempotencyRepo: r.IdempotencyRepo,
	}
	r.fiber.Post("/payments/create", middleware.RateLimitPaymentCreate(), handlers.HandlePaymentCreate(paymentDeps))
	r.fiber.Get("/payment-links/:token", handlers.HandlePaymentLink(dealerDeps))
	r.fiber.Get("/checkout/:token", middleware.RateLimitCheckout(), handlers.HandleCheckout(paymentDeps))
	r.fiber.Post("/checkout/:token/select", middleware.RateLimitCheckout(), handlers.HandleCheckoutSelectAsset(paymentDeps))
	r.fiber.Get("/checkout/:token/change", middleware.RateLimitCheckout(), handlers.HandleCheckoutChangeAsset(paymentDeps))
	r.fiber.Get("/checkout/:token/pay", middleware.RateLimitCheckout(), handlers.HandleCheckoutPay(paymentDeps))
	r.fiber.Get("/checkout/:token/ws", handlers.HandleCheckoutSocket(paymentDeps))
	r.fiber.Get("/checkout/:token/qr.png", handlers.HandleCheckoutQRCode(paymentDeps))
	r.fiber.Get("/checkout/:token/status.json", handlers.HandleCheckoutStatus(paymentDeps))
	r.fiber.Get("/checkout/:token/cancel", handlers.HandleCheckoutCancel(paymentDeps))
	r.fiber.Get("/checkout/:token/return/success", handlers.HandleCheckoutSuccessReturn(paymentDeps))
	r.fiber.Get("/invoice/:token", handlers.HandlePaymentInvoice(paymentDeps))

	v1Deps := handlers.V1APIDeps{
		DomainRepo:      r.DomainRepo,
		WalletRepo:      r.WalletRepo,
		PaymentRepo:     r.PaymentRepo,
		WithdrawalRepo:  r.WithdrawalRepo,
		RefundRepo:      r.RefundRepo,
		LedgerRepo:      r.LedgerRepo,
		TransactionRepo: r.TransactionRepo,
		AssetRegistry:   r.assetRegistry,
		PriceOracle:     pricing.NewCoinGecko(),
		Notifier:        webhooksvc.NewNotifier(),
		PaymentHub:      r.PaymentHub,
		IdempotencyRepo: r.IdempotencyRepo,
	}

	// ── Common API ───────────────────────────────────────────────────────────
	r.fiber.Get("/api/v1/common/status", handlers.HandleV1CommonStatus(v1Deps))
	r.fiber.Get("/api/v1/common/balance", handlers.HandleV1CommonBalance(v1Deps))
	r.fiber.Get("/api/v1/common/prices", handlers.HandleV1CommonPrices(v1Deps))
	r.fiber.Get("/api/v1/common/currencies", handlers.HandleV1CommonCurrencies(v1Deps))
	r.fiber.Get("/api/v1/common/fiat-currencies", handlers.HandleV1CommonFiatCurrencies(v1Deps))
	r.fiber.Get("/api/v1/common/networks", handlers.HandleV1CommonNetworks(v1Deps))

	// ── Payment API ──────────────────────────────────────────────────────────
	r.fiber.Post("/api/v1/payment/create", handlers.HandleV1PaymentCreate(v1Deps))
	r.fiber.Post("/api/v1/payment/white-label", handlers.HandleV1PaymentWhiteLabel(v1Deps))
	r.fiber.Post("/api/v1/payment/static-address", handlers.HandleV1PaymentStaticAddressCreate(v1Deps))
	r.fiber.Get("/api/v1/payment/static-addresses", handlers.HandleV1PaymentStaticAddressList(v1Deps))
	r.fiber.Get("/api/v1/payment/info", handlers.HandleV1PaymentInfo(v1Deps))
	r.fiber.Get("/api/v1/payment/history", handlers.HandleV1PaymentHistory(v1Deps))
	r.fiber.Get("/api/v1/payment/statistics", handlers.HandleV1PaymentStatistics(v1Deps))
	r.fiber.Get("/api/v1/payment/currencies", handlers.HandleV1PaymentCurrencies(v1Deps))
	r.fiber.Get("/api/v1/payment/status-table", handlers.HandleV1PaymentStatusTable(v1Deps))

	// ── Payout API ───────────────────────────────────────────────────────────
	r.fiber.Post("/api/v1/payout/create", handlers.HandleV1PayoutCreate(v1Deps))
	r.fiber.Get("/api/v1/payout/info", handlers.HandleV1PayoutInfo(v1Deps))
	r.fiber.Get("/api/v1/payout/history", handlers.HandleV1PayoutHistory(v1Deps))
	r.fiber.Get("/api/v1/payout/status-table", handlers.HandleV1PayoutStatusTable(v1Deps))

	// ── Refund API ───────────────────────────────────────────────────────────
	r.fiber.Post("/api/v1/refund/create", handlers.HandleV1RefundCreate(v1Deps))
	r.fiber.Get("/api/v1/refund/info", handlers.HandleV1RefundInfo(v1Deps))
	r.fiber.Get("/api/v1/refund/history", handlers.HandleV1RefundHistory(v1Deps))

	swaggerCfg := swaggo.Config{
		Title:                "Gateway API",
		DeepLinking:          true,
		PersistAuthorization: true,
		DocExpansion:         "list",
		WithCredentials:      false,
	}
	r.fiber.Get("/swagger/*", swaggo.New(swaggerCfg))
	r.fiber.Get("/docs/*", swaggo.New(swaggerCfg))
	GenerateFakeActionRoutesSwagger(r.fiber, r.action) // Fake routes
	return r
}

func (r *Router) handlePacket(c fiber.Ctx) error {
	var action string
	origin := strings.TrimRight(strings.TrimSpace(c.Get("Origin")), "/")
	if origin != "" && middleware.IsOriginAllowed(origin, middleware.AllowedCORSOrigins()) {
		c.Set("Access-Control-Allow-Origin", origin)
	}
	c.Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
	c.Set("Access-Control-Allow-Headers", "Accept,Authorization,Content-Type,X-CSRF-Token,Token,session,Origin,Host,Connection,Accept-Encoding,Accept-Language,X-Requested-With")

	if c.Method() == fiber.MethodOptions {
		return c.SendStatus(fiber.StatusNoContent)
	}

	switch c.Method() {
	case fiber.MethodGet:
		action = c.Query("action")

	case fiber.MethodOptions:
		return c.SendStatus(fiber.StatusNoContent)

	case fiber.MethodPost:
		contentType := c.Get("Content-Type")
		if strings.Contains(contentType, "application/json") {
			var packet struct {
				Action string `json:"action"`
			}

			if err := c.Bind().JSON(&packet); err != nil {
				return c.Status(fiber.StatusBadRequest).SendString("invalid JSON body")
			}
			action = packet.Action
		} else {
			action = c.FormValue("action")
		}

	default:
		return c.Status(fiber.StatusMethodNotAllowed).SendString("method not allowed")
	}

	if action == "" {
		fmt.Println("Default handler executed")
		return c.SendString("Default handler executed")
	}

	route, ok := r.action.GetHandler(action)
	if !ok {
		return c.Status(fiber.StatusBadRequest).SendString("Unknown action")
	}

	handler := route.Handler
	for i := len(route.Middlewares) - 1; i >= 0; i-- {
		handler = route.Middlewares[i](handler)
	}

	return handler(c)
}

func (r *Router) GetFiber() *fiber.App {
	return r.fiber
}

func (r *Router) Blockchains() *blockchain.ChainFactory {
	return r.blockchains
}

func (r *Router) AssetRegistry() *asset.Registry {
	return r.assetRegistry
}
