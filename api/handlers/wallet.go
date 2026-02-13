package handlers

import (
	services "core/services/system"
	"core/types"

	"github.com/gofiber/fiber/v2"
)

type WalletHandler struct {
	service *services.WalletService
}

func NewWalletHandler(service *services.WalletService) *WalletHandler {
	return &WalletHandler{service: service}
}

func HandleWalletCreate(s *services.WalletService) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var params types.WalletParams
		if err := c.BodyParser(&params); err != nil {
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
				"error": "Failed to create merchant: " + err.Error(),
			})
		}

		return c.Status(fiber.StatusCreated).JSON(wallet)
	}
}
