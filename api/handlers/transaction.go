package handlers

import (
	"core/repositories"
	"core/types"

	"github.com/gofiber/fiber/v3"
)

func HandleDomainDepositSummary(repo *repositories.TransactionRepo) fiber.Handler {
	return func(c fiber.Ctx) error {
		var params types.DepositSummaryParams
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
				"errors":  err.Error(),
			})
		}

		summaries, err := repo.DomainDepositSummary(params)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"success": false,
				"error":   "Failed to build deposit summary: " + err.Error(),
			})
		}

		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"success": true,
			"data":    summaries,
		})
	}
}
