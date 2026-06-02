// routes/router.go
package routes

import (
	"core/api/handlers"
	"core/api/router"
	configurations "core/application/configuration"
	"core/asset"
	"core/blockchain"
	"core/constants"
	"core/repositories"
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

	MerchantRepo    *repositories.MerchantRepo
	DomainRepo      *repositories.DomainRepo
	WalletRepo      *repositories.WalletRepo
	ChainStateRepo  *repositories.ChainStateRepo
	TransactionRepo *repositories.TransactionRepo
	PaymentRepo     *repositories.PaymentRepo
	WithdrawalRepo  *repositories.WithdrawalRequestRepo
	ActivityLogRepo *repositories.ActivityLogRepo
	MerchantService *services.MerchantService
	WalletService   *services.WalletService
	DomainService   *services.DomainService
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

	r.fiber.Use(cors.New(cors.Config{
		AllowOriginsFunc: func(origin string) bool {
			return true
		},
		AllowMethods: []string{"POST", "GET", "OPTIONS", "PUT", "DELETE"},
		AllowHeaders: []string{
			"Accept", "Authorization", "Content-Type", "Content-Length",
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

	r.ChainStateRepo = repositories.NewChainStateRepo(r.db)
	r.TransactionRepo = repositories.NewTransactionRepo(r.db)
	r.PaymentRepo = repositories.NewPaymentRepo(r.db)
	r.WithdrawalRepo = repositories.NewWithdrawalRequestRepo(r.db)
	r.ActivityLogRepo = repositories.NewActivityLogRepo(r.db)
	r.MerchantRepo = repositories.NewMerchantRepo(r.db, r.blockchains)
	r.MerchantService = services.NewMerchantService(r.MerchantRepo)

	r.DomainRepo = repositories.NewDomainRepo(r.MerchantRepo)
	r.DomainService = services.NewDomainService(r.DomainRepo)

	r.WalletRepo = repositories.NewWalletRepo(r.DomainRepo)
	r.WalletService = services.NewWalletService(r.WalletRepo)

	r.fiber.Use("/assets", staticmw.New("./views/assets"))

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
	r.fiber.Post("/dealer/register", handlers.HandleDealerRegisterSubmit(r.MerchantService, r.ActivityLogRepo))
	dealerDeps := handlers.DealerDeps{
		MerchantService: r.MerchantService,
		DomainService:   r.DomainService,
		WalletRepo:      r.WalletRepo,
		WithdrawalRepo:  r.WithdrawalRepo,
		TransactionRepo: r.TransactionRepo,
		ActivityLogRepo: r.ActivityLogRepo,
		Blockchains:     r.blockchains,
	}
	r.fiber.Get("/dealer", handlers.HandleDealerDashboard(dealerDeps))
	r.fiber.Get("/dealer/dashboard", handlers.HandleDealerDashboard(dealerDeps))
	r.fiber.Get("/dealer/domains", handlers.HandleDealerDashboard(dealerDeps))
	r.fiber.Post("/dealer/domains", handlers.HandleDealerDomainCreate(r.MerchantService, r.DomainService, r.ActivityLogRepo))
	r.fiber.Post("/dealer/withdrawals", handlers.HandleDealerWithdrawalCreate(dealerDeps))
	r.fiber.Get("/dealer/onboarding", handlers.HandleDealerOnboarding())
	r.fiber.Get("/dealer/logout", handlers.HandleDealerLogout(r.MerchantService, r.ActivityLogRepo))
	r.fiber.Get("/auth/oidc/login", handlers.HandleOIDCLogin())
	r.fiber.Get("/auth/oidc/callback", handlers.HandleOIDCCallback(r.MerchantService, r.ActivityLogRepo))
	r.fiber.Get("/admin/login", handlers.HandleAdminLogin())
	r.fiber.Post("/admin/login", handlers.HandleAdminLoginSubmit())
	r.fiber.Get("/admin", handlers.HandleAdminDashboard(dealerDeps))
	r.fiber.Get("/admin/withdrawals", handlers.HandleAdminDashboard(dealerDeps))
	r.fiber.Post("/admin/withdrawals/:id/approve", handlers.HandleAdminWithdrawalApprove(dealerDeps))
	r.fiber.Post("/admin/withdrawals/:id/reject", handlers.HandleAdminWithdrawalReject(dealerDeps))
	r.fiber.Get("/admin/logout", handlers.HandleAdminLogout())

	paymentDeps := handlers.PaymentHandlerDeps{
		DomainRepo:    r.DomainRepo,
		WalletRepo:    r.WalletRepo,
		PaymentRepo:   r.PaymentRepo,
		AssetRegistry: r.assetRegistry,
		Notifier:      webhooksvc.NewNotifier(),
	}
	r.fiber.Post("/payments/create", handlers.HandlePaymentCreate(paymentDeps))
	r.fiber.Get("/checkout/:token", handlers.HandleCheckout(paymentDeps))
	r.fiber.Post("/checkout/:token/select", handlers.HandleCheckoutSelectAsset(paymentDeps))
	r.fiber.Get("/checkout/:token/pay", handlers.HandleCheckoutPay(paymentDeps))
	r.fiber.Get("/checkout/:token/qr.png", handlers.HandleCheckoutQRCode(paymentDeps))
	r.fiber.Get("/checkout/:token/status.json", handlers.HandleCheckoutStatus(paymentDeps))
	r.fiber.Get("/checkout/:token/cancel", handlers.HandleCheckoutCancel(paymentDeps))
	r.fiber.Get("/checkout/:token/return/success", handlers.HandleCheckoutSuccessReturn(paymentDeps))

	r.fiber.Get("/swagger/*", swaggo.New())
	r.fiber.Get("/docs/*", swaggo.New())
	GenerateFakeActionRoutesSwagger(r.fiber, r.action) // Fake routes
	return r
}

func (r *Router) handlePacket(c fiber.Ctx) error {
	var action string
	c.Set("Access-Control-Allow-Origin", "*")
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
