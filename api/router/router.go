package router

import (
	middleware "core/api/middleware"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

type Route struct {
	Handler     fiber.Handler
	Middlewares []middleware.Middleware
}

type ActionRouter struct {
	routes       map[string]Route
	defaultRoute fiber.Handler
	db           *gorm.DB
}

func NewActionRouter(db *gorm.DB) *ActionRouter {
	return &ActionRouter{
		routes: make(map[string]Route),
		db:     db,
	}
}

// Register
func (ar *ActionRouter) Register(action string, handler fiber.Handler, mws ...middleware.Middleware) {
	ar.routes[action] = Route{
		Handler:     handler,
		Middlewares: mws,
	}
}

// Resolve
func (ar *ActionRouter) Resolve(c *fiber.Ctx) error {
	action := c.FormValue("action")
	if action == "" {
		action = c.Query("action")
	}

	route, ok := ar.routes[action]
	if !ok {
		if ar.defaultRoute != nil {
			return ar.defaultRoute(c)
		}
		return c.Status(fiber.StatusBadRequest).SendString("Unknown action")
	}

	// Middleware zincirini uygula
	handler := route.Handler
	for i := len(route.Middlewares) - 1; i >= 0; i-- {
		handler = route.Middlewares[i](handler)
	}

	return handler(c)
}
func (ar *ActionRouter) GetHandler(action string) (Route, bool) {
	route, ok := ar.routes[action]
	return route, ok
}
