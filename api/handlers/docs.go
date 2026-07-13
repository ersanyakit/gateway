package handlers

import (
	"os"

	"github.com/gofiber/fiber/v3"
)

// HandleIntegrationGuide serves the AI/developer integration guide as Markdown.
// @Summary Integration guide
// @Description Returns the payment gateway integration guide as plain Markdown for developers and AI agents.
// @Tags Documentation
// @Produce text/markdown
// @Success 200 {string} string "Markdown integration guide"
// @Failure 404 {string} string "integration guide not found"
// @Router /docs/integration-guide.md [get]
func HandleIntegrationGuide() fiber.Handler {
	return func(c fiber.Ctx) error {
		body, err := os.ReadFile("docs/integration-guide.md")
		if err != nil {
			return c.Status(fiber.StatusNotFound).SendString("integration guide not found")
		}
		c.Set("Content-Type", "text/markdown; charset=utf-8")
		return c.SendString(string(body))
	}
}

// HandleMoneyEventCatalog serves the versioned money event catalog as Markdown.
// @Summary Money event catalog
// @Description Returns the canonical and compatibility webhook event catalog as plain Markdown.
// @Tags Documentation
// @Produce text/markdown
// @Success 200 {string} string "Markdown money event catalog"
// @Failure 404 {string} string "money event catalog not found"
// @Router /docs/money-event-catalog.md [get]
func HandleMoneyEventCatalog() fiber.Handler {
	return func(c fiber.Ctx) error {
		body, err := os.ReadFile("docs/money-event-catalog.md")
		if err != nil {
			return c.Status(fiber.StatusNotFound).SendString("money event catalog not found")
		}
		c.Set("Content-Type", "text/markdown; charset=utf-8")
		return c.SendString(string(body))
	}
}
