package middleware

import "github.com/gofiber/fiber/v3"

type Middleware func(fiber.Handler) fiber.Handler
