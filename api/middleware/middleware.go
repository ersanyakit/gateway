package middleware

import "github.com/gofiber/fiber/v2"

type Middleware func(fiber.Handler) fiber.Handler
