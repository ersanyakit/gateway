package handlers

import (
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

		domain, err := s.Create(params)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "Failed to create domain: " + err.Error(),
			})
		}

		return c.Status(fiber.StatusCreated).JSON(domain)
	}
}
