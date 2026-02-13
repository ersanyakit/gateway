// routes/swagger_helpers.go
package routes

import (
	"core/api/router"
	"core/constants"
	"fmt"

	fiber "github.com/gofiber/fiber/v2"
)

// GenerateFakeActionRoutesSwagger, action bazlı router'ı Swagger UI için sahte endpoint olarak ekler
func GenerateFakeActionRoutesSwagger(app *fiber.App, ar *router.ActionRouter) {
	for _, cmd := range constants.AllCommands {
		// Swagger için GET ve POST ekliyoruz
		path := fmt.Sprintf("/%s", cmd)

		app.Get(path, func(c *fiber.Ctx) error {
			return c.SendString(fmt.Sprintf("Use /packet?action=%s with POST", cmd))
		})
		app.Post(path, func(c *fiber.Ctx) error {
			return c.SendString(fmt.Sprintf("Use /packet?action=%s with POST", cmd))
		})
	}
}
