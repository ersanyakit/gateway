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
	"fmt"
	"strings"

	fiber "github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	swagger "github.com/gofiber/swagger" // fiber için swagger handler
	"gorm.io/gorm"

	_ "core/docs"
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
	MerchantService *services.MerchantService
	WalletService   *services.WalletService
	DomainService   *services.DomainService
}

func NewRouter(db *gorm.DB) *Router {
	r := &Router{
		action: router.NewActionRouter(db),
		db:     db,
		fiber: fiber.New(fiber.Config{
			ReadBufferSize:  8192,
			WriteBufferSize: 8192,
		}),
		assetRegistry: configurations.NewAssetRegistry(),
		blockchains:   configurations.NewChainFactory(),
	}

	r.fiber.Use(cors.New(cors.Config{
		AllowOrigins:     "*",
		AllowCredentials: false,
		AllowMethods:     "POST,GET,OPTIONS,PUT,DELETE",
		AllowHeaders:     "Accept,Authorization,authorization,Content-Type,Content-Length,X-CSRF-Token,Token,session,Origin,Host,Connection,Accept-Encoding,Accept-Language,X-Requested-With",
	}))

	r.MerchantRepo = repositories.NewMerchantRepo(r.db, r.blockchains)
	r.MerchantService = services.NewMerchantService(r.MerchantRepo)

	r.DomainRepo = repositories.NewDomainRepo(r.MerchantRepo)
	r.DomainService = services.NewDomainService(r.DomainRepo)

	r.WalletRepo = repositories.NewWalletRepo(r.DomainRepo)
	r.WalletService = services.NewWalletService(r.WalletRepo)

	r.fiber.Post(constants.CMD_MERCHANT_CREATE.String(), handlers.HandleMerchantCreate(r.MerchantService))
	r.fiber.Post(constants.CMD_MERCHANT_FETCH.String(), handlers.HandleMerchantFetch(r.MerchantService))

	r.fiber.Post(constants.CMD_MERCHANT_CREATE.String(), handlers.HandleWalletCreate(r.WalletService))

	r.fiber.Post(constants.CMD_MERCHANT_FETCH_BY_ID.String(), handlers.HandleMerchantFindById(r.MerchantService))
	r.fiber.Post(constants.CMD_MERCHANT_FETCH_BY_EMAIL.String(), handlers.HandleMerchantFindByEmail(r.MerchantService))

	r.fiber.Post(constants.CMD_MERCHANT_FETCH_BY_ID.String(), handlers.HandleMerchantFindById(r.MerchantService))
	r.fiber.Post(constants.CMD_MERCHANT_FETCH_BY_EMAIL.String(), handlers.HandleMerchantFindByEmail(r.MerchantService))

	r.fiber.Post(constants.CMD_MERCHANT_DELETE_BY_ID.String(), handlers.HandleMerchantDeleteById(r.MerchantService))
	r.fiber.Post(constants.CMD_MERCHANT_DELETE_BY_EMAIL.String(), handlers.HandleMerchantDeleteByEmail(r.MerchantService))

	r.fiber.Post(constants.CMD_MERCHANT_WALLET_CREATE.String(), handlers.HandleWalletCreate(r.WalletService))

	r.fiber.All("/docs/*", swagger.HandlerDefault)     // http://localhost:3000/docs/index.html
	GenerateFakeActionRoutesSwagger(r.fiber, r.action) // Fake routes
	return r
}

func (r *Router) handlePacket(c *fiber.Ctx) error {
	var action string
	c.Set("Access-Control-Allow-Origin", "*")
	c.Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
	c.Set("Access-Control-Allow-Headers", "Accept,Authorization,Content-Type,X-CSRF-Token,Token,session,Origin,Host,Connection,Accept-Encoding,Accept-Language,X-Requested-With")
	if c.Method() == fiber.MethodOptions {
		return c.SendStatus(fiber.StatusNoContent)
	}
	if c.Method() == fiber.MethodOptions {
		return c.SendStatus(fiber.StatusNoContent)
	}

	switch c.Method() {
	case fiber.MethodGet:
		action = c.Query("action")

	case fiber.MethodPost:
		contentType := c.Get("Content-Type")
		if strings.Contains(contentType, "application/json") {
			var packet struct {
				Action string `json:"action"`
			}
			if err := c.BodyParser(&packet); err != nil {
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
		return c.SendString(fmt.Sprintf("%s DEFAULT HANDLER EXECUTED", constants.APPLICATION_NAME))
	}

	route, ok := r.action.GetHandler(action)
	if !ok {
		return c.Status(fiber.StatusBadRequest).SendString("Unknown action")
	}

	// Middleware zincirini uygula (Fiber middleware olduğu varsayımıyla)
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
