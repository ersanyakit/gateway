package handlers

import (
	services "core/services/system"
	"core/types"
	"strings"

	"github.com/gofiber/fiber/v2"
)

type MerchantHandler struct {
	service *services.MerchantService
}

func NewMerchantHandler(service *services.MerchantService) *MerchantHandler {
	return &MerchantHandler{service: service}
}

func HandleMerchantCreate(s *services.MerchantService) fiber.Handler {
	return func(c *fiber.Ctx) error {

		name := strings.ToLower(c.Params("name"))
		email := strings.ToLower(c.Params("email"))
		emailRepeat := strings.ToLower(c.Params("email_repeat"))
		password := strings.ToLower(c.Params("password"))
		passwordRepeat := strings.ToLower(c.Params("password_repeat"))

		params := types.MerchantParams{
			Context:        c.Context(),
			Name:           &name,
			Email:          &email,
			EmailRepeat:    &emailRepeat,
			Password:       &password,
			PasswordRepeat: &passwordRepeat,
		}

		if err := params.Validate(); err != nil {
			return c.Status(400).JSON(fiber.Map{
				"success": false,
				"errors":  err,
			})
		}
		merchant, err := s.Create(params)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "Failed to create merchant: " + err.Error(),
			})
		}

		return c.Status(fiber.StatusCreated).JSON(merchant)
	}
}
