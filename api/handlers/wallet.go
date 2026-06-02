package handlers

import (
	services "core/services/system"
	"core/types"

	"github.com/gofiber/fiber/v3"
)

type WalletHandler struct {
	service *services.WalletService
}

func NewWalletHandler(service *services.WalletService) *WalletHandler {
	return &WalletHandler{service: service}
}

// HandleWalletCreate creates or returns a product/user scoped wallet.
// @Summary Create user wallet
// @Description Creates or returns the deterministic multi-chain wallet for a merchant domain, product_id, and user_id.
// @Tags Users
// @Accept json
// @Produce json
// @Param payload body types.WalletParams true "User wallet create payload"
// @Success 201 {object} types.WalletCreateResponse
// @Failure 400 {object} types.ErrorResponse
// @Failure 500 {object} types.ErrorResponse
// @Router /merchant.wallet.create [post]
func HandleWalletCreate(s *services.WalletService) fiber.Handler {
	return func(c fiber.Ctx) error {
		var params types.WalletParams
		if err := c.Bind().Body(&params); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"success": false,
				"error":   "Invalid JSON body: " + err.Error(),
			})
		}

		params.Context = c.Context()

		if err := params.Validate(); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"success": false,
				"errors":  err,
			})
		}

		wallet, err := s.Create(params)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "Failed to create wallet: " + err.Error(),
			})
		}

		return c.Status(fiber.StatusCreated).JSON(wallet)
	}
}
