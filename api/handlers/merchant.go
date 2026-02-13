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
		var params types.MerchantParams
		if err := c.BodyParser(&params); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"success": false,
				"error":   "Invalid JSON body: " + err.Error(),
			})
		}

		params.Context = c.Context()

		if params.Name != nil {
			name := strings.ToLower(*params.Name)
			params.Name = &name
		}
		if params.Email != nil {
			email := strings.ToLower(*params.Email)
			params.Email = &email
		}
		if params.EmailRepeat != nil {
			emailRepeat := strings.ToLower(*params.EmailRepeat)
			params.EmailRepeat = &emailRepeat
		}
		if params.Password != nil {
			password := strings.ToLower(*params.Password)
			params.Password = &password
		}
		if params.PasswordRepeat != nil {
			passwordRepeat := strings.ToLower(*params.PasswordRepeat)
			params.PasswordRepeat = &passwordRepeat
		}

		if err := params.Validate(); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
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

func HandleMerchantFindById(s *services.MerchantService) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var params types.MerchantParams
		if err := c.BodyParser(&params); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"success": false,
				"error":   "Invalid JSON body: " + err.Error(),
			})
		}

		params.Context = c.Context()
		if err := params.ValidateID(); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"success": false,
				"errors":  err,
			})
		}

		merchant, err := s.FindByID(params)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "Failed to find merchant by id: " + err.Error(),
			})
		}

		return c.Status(fiber.StatusCreated).JSON(merchant)
	}
}

func HandleMerchantFindByEmail(s *services.MerchantService) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var params types.MerchantParams
		if err := c.BodyParser(&params); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"success": false,
				"error":   "Invalid JSON body: " + err.Error(),
			})
		}

		params.Context = c.Context()
		if err := params.ValidateEmail(); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"success": false,
				"errors":  err,
			})
		}

		merchant, err := s.FindByID(params)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "Failed to find merchant by id: " + err.Error(),
			})
		}

		return c.Status(fiber.StatusCreated).JSON(merchant)
	}
}

func HandleMerchantDeleteById(s *services.MerchantService) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var params types.MerchantParams
		if err := c.BodyParser(&params); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"success": false,
				"error":   "Invalid JSON body: " + err.Error(),
			})
		}

		params.Context = c.Context()
		if err := params.ValidateID(); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"success": false,
				"errors":  err,
			})
		}

		err := s.DeleteByID(params)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "Failed to delete merchant by id: " + err.Error(),
			})
		}

		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"success": true,
		})
	}
}

func HandleMerchantDeleteByEmail(s *services.MerchantService) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var params types.MerchantParams
		if err := c.BodyParser(&params); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"success": false,
				"error":   "Invalid JSON body: " + err.Error(),
			})
		}

		params.Context = c.Context()
		if err := params.ValidateEmail(); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"success": false,
				"errors":  err,
			})
		}

		err := s.DeleteByEmail(params)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "Failed to delete merchant by email: " + err.Error(),
			})
		}

		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"success": true,
		})
	}
}

func HandleMerchantFetch(s *services.MerchantService) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var params types.MerchantParams
		if err := c.BodyParser(&params); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"success": false,
				"error":   "Invalid JSON body: " + err.Error(),
			})
		}

		params.Context = c.Context()

		merchants, cursor, err := s.Fetch(params)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "Failed to fetch merchants: " + err.Error(),
			})
		}

		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"success":   false,
			"merchants": merchants,
			"cursor":    cursor,
		})
	}
}
