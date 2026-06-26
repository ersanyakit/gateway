package handlers

import (
	"core/helpers"
	services "core/services/system"
	"core/types"

	"github.com/gofiber/fiber/v3"
)

type DomainHandler struct {
	service *services.DomainService
}

func NewDomainHandler(service *services.DomainService) *DomainHandler {
	return &DomainHandler{service: service}
}

// HandleDomainCreate creates a merchant domain configuration.
// @Summary Create merchant domain
// @Description Creates a merchant domain with webhook settings and returns the API key used by payment requests.
// @Tags Merchant Portal
// @Accept json
// @Produce json
// @Param payload body types.DomainParams true "Merchant domain create payload"
// @Success 201 {object} types.DomainCreateResponse
// @Failure 400 {object} types.ErrorResponse
// @Failure 500 {object} types.ErrorResponse
// @Router /merchant.domain.create [post]
func HandleDomainCreate(s *services.DomainService) fiber.Handler {
	return func(c fiber.Ctx) error {
		var params types.DomainParams
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
		if err := helpers.ValidateWebhookURL(*params.WebhookURL); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"success": false,
				"error":   "Invalid webhook URL: " + err.Error(),
			})
		}

		domain, err := s.Create(params)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "Failed to create domain: " + err.Error(),
			})
		}

		return c.Status(fiber.StatusCreated).JSON(domain)
	}
}
