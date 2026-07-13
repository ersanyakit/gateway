package x402

import (
	"context"
	"core/constants"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"
	x402sdk "github.com/x402-foundation/x402/go"
	x402http "github.com/x402-foundation/x402/go/http"
	nethttpmw "github.com/x402-foundation/x402/go/http/nethttp"
	evmserver "github.com/x402-foundation/x402/go/mechanisms/evm/exact/server"
	svmserver "github.com/x402-foundation/x402/go/mechanisms/svm/exact/server"
)

const (
	defaultFacilitatorURL = "https://x402.org/facilitator"
	defaultNetwork        = "eip155:84532"
	defaultPrice          = "$0.001"
	defaultTimeout        = 30 * time.Second
	defaultCheckoutRoute  = "GET /checkout/*/pay"

	CheckoutNetworkBase   x402sdk.Network = "eip155:8453"
	CheckoutNetworkSolana x402sdk.Network = "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp"
)

var (
	evmAddressPattern    = compileX402AddressPattern("EVM", `^0x[0-9a-fA-F]{40}$`)
	solanaAddressPattern = compileX402AddressPattern("Solana", `^[1-9A-HJ-NP-Za-km-z]{32,44}$`)
)

func compileX402AddressPattern(name, expression string) *regexp.Regexp {
	pattern, err := regexp.Compile(expression)
	if err != nil {
		log.Printf("x402 address validator=%s error=%v", name, err)
		return nil
	}
	return pattern
}

func x402AddressMatches(pattern *regexp.Regexp, address string) bool {
	return pattern != nil && pattern.MatchString(address)
}

// Config contains x402 infrastructure and generic static-resource settings.
// Payment-link x402 enablement lives on the Product and is copied into each
// PaymentSession; the selected checkout asset supplies the network.
type Config struct {
	Enabled                bool
	FacilitatorURL         string
	Price                  string
	Networks               []x402sdk.Network
	PayTo                  map[x402sdk.Network]string
	Routes                 x402http.RoutesConfig
	Timeout                time.Duration
	SyncFacilitatorOnStart bool
}

// CheckoutPayment is the payment tuple for one hosted checkout session.
// PayTo, Asset, and Amount are resolved per request so generated deposit
// addresses and selected assets cannot be reused across sessions.
type CheckoutPayment struct {
	Network x402sdk.Network
	PayTo   string
	Asset   string
	Amount  string
}

// CheckoutSessionResolver resolves the current payment session from a
// checkout URL token.
type CheckoutSessionResolver func(context.Context, string) (CheckoutPayment, error)

// ErrCheckoutNotEligible tells the x402 hook to leave the normal checkout
// page flow untouched (for example before asset selection or after expiry).
var ErrCheckoutNotEligible = errors.New("checkout is not eligible for x402")

// LoadConfigFromEnv loads facilitator infrastructure and optional generic
// static-resource configuration. Payment links do not require an x402 enable
// flag in the environment; their settings are stored with the link.
func LoadConfigFromEnv() (Config, error) {
	staticEnabled := envFlag("X402_ENABLED")
	config := Config{
		Enabled:                staticEnabled,
		FacilitatorURL:         strings.TrimSpace(os.Getenv("X402_FACILITATOR_URL")),
		Price:                  strings.TrimSpace(os.Getenv("X402_PRICE")),
		Timeout:                parseTimeout(os.Getenv("X402_TIMEOUT")),
		SyncFacilitatorOnStart: envFlagWithDefault("X402_SYNC_FACILITATOR_ON_START", true),
	}
	if config.FacilitatorURL == "" {
		config.FacilitatorURL = defaultFacilitatorURL
	}
	if err := validateFacilitatorURL(config.FacilitatorURL); err != nil {
		return Config{}, err
	}
	if !config.Enabled {
		return config, nil
	}

	staticRoutes := strings.TrimSpace(os.Getenv("X402_ROUTES"))
	if staticEnabled && staticRoutes != "" {
		if config.Price == "" {
			config.Price = defaultPrice
		}
		if err := validatePrice(config.Price); err != nil {
			return Config{}, fmt.Errorf("X402_PRICE: %w", err)
		}

		networks, err := parseNetworks(firstNonEmpty(os.Getenv("X402_NETWORKS"), os.Getenv("X402_NETWORK"), defaultNetwork))
		if err != nil {
			return Config{}, err
		}
		config.Networks = networks
		config.PayTo = make(map[x402sdk.Network]string, len(networks))
		for _, network := range networks {
			payTo := strings.TrimSpace(os.Getenv(payToEnvName(network)))
			if payTo == "" {
				payTo = strings.TrimSpace(os.Getenv("X402_PAY_TO"))
			}
			if err := validatePayTo(network, payTo); err != nil {
				return Config{}, err
			}
			config.PayTo[network] = payTo
		}

		routes, err := parseRoutes(staticRoutes, config.Price, config.Networks, config.PayTo)
		if err != nil {
			return Config{}, err
		}
		config.Routes = routes
	} else {
		return Config{}, errors.New("X402_ROUTES must contain at least one route pattern when X402_ENABLED is true")
	}

	return config, nil
}

// CheckoutNetworkForChain derives the x402 network from the asset's selected
// chain. The checkout UI already owns chain selection, so payment-link
// configuration must not duplicate it in the merchant panel.
func CheckoutNetworkForChain(chainID constants.ChainID) (x402sdk.Network, bool) {
	if chainID == constants.Solana {
		return CheckoutNetworkSolana, true
	}
	switch chainID {
	case constants.Ethereum, constants.Base, constants.Arbitrum, constants.Binance,
		constants.Unichain, constants.Avalanche, constants.Chiliz, constants.ChilizSpicy:
		return x402sdk.Network("eip155:" + strconv.FormatInt(int64(chainID), 10)), true
	default:
		return "", false
	}
}

// NewMiddleware creates a Fiber middleware backed by the official x402 Go
// net/http middleware. The Fiber adaptor preserves the normal Fiber routing
// chain while allowing the SDK to own the HTTP 402/verify/settle lifecycle.
func NewMiddleware(config Config) fiber.Handler {
	if !config.Enabled {
		return func(c fiber.Ctx) error {
			return c.Next()
		}
	}
	if config.Timeout <= 0 {
		config.Timeout = defaultTimeout
	}
	if strings.TrimSpace(config.FacilitatorURL) == "" {
		config.FacilitatorURL = defaultFacilitatorURL
	}

	facilitator := x402http.NewHTTPFacilitatorClient(&x402http.FacilitatorConfig{
		URL:     config.FacilitatorURL,
		Timeout: config.Timeout,
	})
	options := []nethttpmw.MiddlewareOption{
		nethttpmw.WithFacilitatorClient(facilitator),
		nethttpmw.WithSyncFacilitatorOnStart(config.SyncFacilitatorOnStart),
		nethttpmw.WithTimeout(config.Timeout),
	}
	for _, network := range config.Networks {
		switch networkNamespace(network) {
		case "eip155":
			options = append(options, nethttpmw.WithScheme(network, evmserver.NewExactEvmScheme()))
		case "solana":
			options = append(options, nethttpmw.WithScheme(network, svmserver.NewExactSvmScheme()))
		}
	}

	return adaptor.HTTPMiddleware(nethttpmw.PaymentMiddlewareFromConfig(config.Routes, options...))
}

// NewCheckoutMiddleware creates a session-driven x402 seller middleware for
// the hosted checkout route. Unlike generic static resources, it is mounted
// regardless of an environment flag and only activates for PaymentSessions
// whose merchant-created link has x402 enabled.
func NewCheckoutMiddleware(config Config, resolver CheckoutSessionResolver) fiber.Handler {
	if resolver == nil {
		return func(c fiber.Ctx) error {
			return c.Next()
		}
	}

	state := &checkoutMiddleware{
		config:   config,
		resolver: resolver,
		handlers: make(map[x402sdk.Network]fiber.Handler),
	}
	return state.handle
}

type checkoutMiddleware struct {
	config   Config
	resolver CheckoutSessionResolver

	mu       sync.Mutex
	handlers map[x402sdk.Network]fiber.Handler
}

func (m *checkoutMiddleware) handle(c fiber.Ctx) error {
	if c.Method() != http.MethodGet || !strings.HasSuffix(strings.TrimSuffix(c.Path(), "/"), "/pay") {
		return c.Next()
	}
	if checkoutPrefersHostedPage(c) {
		return c.Next()
	}
	payment, err := resolveCheckoutPayment(c.Context(), c.Path(), m.resolver)
	if errors.Is(err, ErrCheckoutNotEligible) {
		return c.Next()
	}
	if err != nil {
		return err
	}

	handler, err := m.handlerFor(payment.Network)
	if err != nil {
		return err
	}
	return handler(c)
}

// checkoutPrefersHostedPage keeps human browser navigation on the branded
// checkout page while leaving programmatic x402 requests on the 402 protocol
// path. A payment retry always wins over content negotiation.
func checkoutPrefersHostedPage(c fiber.Ctx) bool {
	if strings.TrimSpace(c.Get("PAYMENT-SIGNATURE")) != "" || strings.TrimSpace(c.Get("X-PAYMENT")) != "" {
		return false
	}

	if strings.EqualFold(strings.TrimSpace(c.Get("Sec-Fetch-Mode")), "navigate") ||
		strings.EqualFold(strings.TrimSpace(c.Get("Sec-Fetch-Dest")), "document") {
		return true
	}

	for _, mediaRange := range strings.Split(c.Get("Accept"), ",") {
		mediaType := strings.TrimSpace(strings.SplitN(mediaRange, ";", 2)[0])
		if strings.EqualFold(mediaType, "text/html") || strings.EqualFold(mediaType, "application/xhtml+xml") {
			return true
		}
	}
	return false
}

func (m *checkoutMiddleware) handlerFor(network x402sdk.Network) (fiber.Handler, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if handler, ok := m.handlers[network]; ok {
		return handler, nil
	}
	timeout := m.config.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	routes := x402http.RoutesConfig{
		defaultCheckoutRoute: {
			Accepts: x402http.PaymentOptions{
				{
					Scheme:  "exact",
					Network: network,
					PayTo: x402http.DynamicPayToFunc(func(ctx context.Context, reqCtx x402http.HTTPRequestContext) (string, error) {
						payment, err := resolveCheckoutPayment(ctx, reqCtx.Path, m.resolver)
						if err != nil {
							return "", err
						}
						if payment.Network != network {
							return "", ErrCheckoutNotEligible
						}
						return payment.PayTo, nil
					}),
					Price: x402http.DynamicPriceFunc(func(ctx context.Context, reqCtx x402http.HTTPRequestContext) (x402sdk.Price, error) {
						payment, err := resolveCheckoutPayment(ctx, reqCtx.Path, m.resolver)
						if err != nil {
							return nil, err
						}
						if payment.Network != network {
							return nil, ErrCheckoutNotEligible
						}
						return x402sdk.Price(map[string]interface{}{
							"amount": payment.Amount,
							"asset":  payment.Asset,
						}), nil
					}),
				},
			},
			Description: "Pay this checkout with x402",
			MimeType:    "text/html",
			ServiceName: "Gateway Checkout",
		},
	}
	facilitator := x402http.NewHTTPFacilitatorClient(&x402http.FacilitatorConfig{
		URL:     firstNonEmpty(m.config.FacilitatorURL, defaultFacilitatorURL),
		Timeout: timeout,
	})
	server := x402http.Newx402HTTPResourceServer(routes, x402sdk.WithFacilitatorClient(facilitator))
	switch networkNamespace(network) {
	case "eip155":
		server.Register(network, evmserver.NewExactEvmScheme())
	case "solana":
		server.Register(network, svmserver.NewExactSvmScheme())
	default:
		return nil, fmt.Errorf("unsupported x402 checkout network %s", network)
	}
	server.OnProtectedRequest(func(ctx context.Context, reqCtx x402http.HTTPRequestContext, _ x402http.RouteConfig) (*x402http.ProtectedRequestHookResult, error) {
		payment, err := resolveCheckoutPayment(ctx, reqCtx.Path, m.resolver)
		if errors.Is(err, ErrCheckoutNotEligible) || (err == nil && payment.Network != network) {
			return &x402http.ProtectedRequestHookResult{GrantAccess: true}, nil
		}
		if err != nil {
			return nil, err
		}
		return nil, nil
	})

	options := []nethttpmw.MiddlewareOption{
		nethttpmw.WithSyncFacilitatorOnStart(m.config.SyncFacilitatorOnStart),
		nethttpmw.WithTimeout(timeout),
	}
	handler := adaptor.HTTPMiddleware(nethttpmw.PaymentMiddlewareFromHTTPServer(server, options...))
	m.handlers[network] = handler
	return handler, nil
}

func resolveCheckoutPayment(ctx context.Context, path string, resolver CheckoutSessionResolver) (CheckoutPayment, error) {
	token := checkoutSessionToken(path)
	if token == "" {
		return CheckoutPayment{}, ErrCheckoutNotEligible
	}
	payment, err := resolver(ctx, token)
	if err != nil {
		return CheckoutPayment{}, err
	}
	if err := validateCheckoutPayment(payment, payment.Network); err != nil {
		return CheckoutPayment{}, err
	}
	return payment, nil
}

func validateCheckoutPayment(payment CheckoutPayment, network x402sdk.Network) error {
	if payment.Network != network || strings.TrimSpace(payment.PayTo) == "" || strings.TrimSpace(payment.Asset) == "" {
		return ErrCheckoutNotEligible
	}
	if err := validateAddress(network, payment.PayTo); err != nil {
		return fmt.Errorf("%w: invalid checkout payTo: %v", ErrCheckoutNotEligible, err)
	}
	if err := validateAddress(network, payment.Asset); err != nil {
		return fmt.Errorf("%w: invalid checkout asset: %v", ErrCheckoutNotEligible, err)
	}
	amount, ok := new(big.Int).SetString(strings.TrimSpace(payment.Amount), 10)
	if !ok || amount.Sign() <= 0 {
		return ErrCheckoutNotEligible
	}
	return nil
}

func validateAddress(network x402sdk.Network, address string) error {
	address = strings.TrimSpace(address)
	switch networkNamespace(network) {
	case "eip155":
		if !x402AddressMatches(evmAddressPattern, address) {
			return errors.New("must be a 20-byte 0x-prefixed EVM address")
		}
	case "solana":
		if !x402AddressMatches(solanaAddressPattern, address) {
			return errors.New("must be a base58 Solana address")
		}
	default:
		return fmt.Errorf("unsupported x402 network %s", network)
	}
	return nil
}

func checkoutSessionToken(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for index, part := range parts {
		if part == "checkout" && index+1 < len(parts) {
			token := strings.TrimSpace(parts[index+1])
			if token != "" {
				return token
			}
		}
	}
	return ""
}

func parseRoutes(raw, price string, networks []x402sdk.Network, payToByNetwork map[x402sdk.Network]string) (x402http.RoutesConfig, error) {
	items, err := parseRoutePatterns(raw, "")
	if err != nil {
		return nil, err
	}

	routes := make(x402http.RoutesConfig, len(items))
	for _, item := range items {
		parts := strings.Fields(item)
		method := parts[0]
		path := parts[1]
		routes[method+" "+path] = x402http.RouteConfig{
			Accepts: x402http.PaymentOptions{},
			Description: firstNonEmpty(
				strings.TrimSpace(os.Getenv("X402_ROUTE_DESCRIPTION")),
				"Gateway API resource",
			),
			MimeType:    "application/json",
			ServiceName: firstNonEmpty(strings.TrimSpace(os.Getenv("X402_SERVICE_NAME")), "Gateway"),
		}
	}

	for pattern, route := range routes {
		for _, network := range networks {
			payTo := payToByNetwork[network]
			route.Accepts = append(route.Accepts, x402http.PaymentOption{
				Scheme:  "exact",
				Price:   x402sdk.Price(price),
				Network: network,
				PayTo:   payTo,
			})
		}
		routes[pattern] = route
	}

	return routes, nil
}

func parseRoutePatterns(raw, fallback string) ([]string, error) {
	items := splitList(raw)
	if len(items) == 0 && strings.TrimSpace(fallback) != "" {
		items = splitList(fallback)
	}
	if len(items) == 0 {
		return nil, errors.New("route configuration must contain at least one route pattern")
	}

	result := make([]string, 0, len(items))
	for _, item := range items {
		parts := strings.Fields(item)
		if len(parts) == 1 && parts[0] == "*" {
			parts = []string{"*", "*"}
		}
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid x402 route %q; expected METHOD /path", item)
		}
		method := strings.ToUpper(strings.TrimSpace(parts[0]))
		path := strings.TrimSpace(parts[1])
		if !validHTTPMethod(method) {
			return nil, fmt.Errorf("invalid x402 method %q in route %q", method, item)
		}
		if path != "*" && (path == "" || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?\r\n")) {
			return nil, fmt.Errorf("invalid x402 path %q in route %q", path, item)
		}
		result = append(result, method+" "+path)
	}
	return result, nil
}

func parseNetworks(raw string) ([]x402sdk.Network, error) {
	networks := parseNetworksUnchecked(raw, "")
	if len(networks) == 0 {
		return nil, errors.New("X402_NETWORKS must contain at least one CAIP-2 network")
	}
	seen := make(map[x402sdk.Network]struct{}, len(networks))
	result := make([]x402sdk.Network, 0, len(networks))
	for _, network := range networks {
		namespace := networkNamespace(network)
		reference := networkReference(network)
		if reference == "" || strings.ContainsAny(reference, " \t\r\n") {
			return nil, fmt.Errorf("invalid x402 network %q; expected CAIP-2 namespace:reference", network)
		}
		if namespace != "eip155" && namespace != "solana" {
			return nil, fmt.Errorf("unsupported x402 network namespace %q; supported namespaces are eip155 and solana", namespace)
		}
		if namespace == "eip155" {
			if chainID, err := strconv.ParseUint(reference, 10, 64); err != nil || chainID == 0 {
				return nil, fmt.Errorf("invalid EVM x402 network %q", network)
			}
		}
		if _, ok := seen[network]; ok {
			continue
		}
		seen[network] = struct{}{}
		result = append(result, network)
	}
	return result, nil
}

func parseNetworksUnchecked(raw string, fallback string) []x402sdk.Network {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = strings.TrimSpace(fallback)
	}
	if raw == "" {
		raw = defaultNetwork
	}
	items := splitList(raw)
	result := make([]x402sdk.Network, 0, len(items))
	for _, item := range items {
		result = append(result, x402sdk.Network(strings.TrimSpace(item)))
	}
	return result
}

func validateFacilitatorURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return fmt.Errorf("X402_FACILITATOR_URL must be an http(s) URL")
	}
	return nil
}

func validatePrice(raw string) error {
	value := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "$"))
	amount, ok := new(big.Rat).SetString(value)
	if !ok || amount.Sign() <= 0 {
		return fmt.Errorf("must be a positive decimal price")
	}
	return nil
}

func validatePayTo(network x402sdk.Network, payTo string) error {
	if strings.TrimSpace(payTo) == "" {
		return fmt.Errorf("%s must be set for x402 network %s", payToEnvName(network), network)
	}
	switch networkNamespace(network) {
	case "eip155":
		if !x402AddressMatches(evmAddressPattern, payTo) {
			return fmt.Errorf("%s must be a 20-byte 0x-prefixed EVM address", payToEnvName(network))
		}
	case "solana":
		if !x402AddressMatches(solanaAddressPattern, payTo) {
			return fmt.Errorf("%s must be a base58 Solana address", payToEnvName(network))
		}
	}
	return nil
}

func payToEnvName(network x402sdk.Network) string {
	key := strings.NewReplacer(":", "_", "-", "_").Replace(string(network))
	return "X402_PAY_TO_" + strings.ToUpper(key)
}

func networkNamespace(network x402sdk.Network) string {
	value := string(network)
	if index := strings.IndexByte(value, ':'); index >= 0 {
		return strings.ToLower(strings.TrimSpace(value[:index]))
	}
	return strings.ToLower(strings.TrimSpace(value))
}

func networkReference(network x402sdk.Network) string {
	value := string(network)
	if index := strings.IndexByte(value, ':'); index >= 0 {
		return strings.TrimSpace(value[index+1:])
	}
	return ""
}

func splitList(raw string) []string {
	items := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	})
	result := make([]string, 0, len(items))
	for _, item := range items {
		if value := strings.TrimSpace(item); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func validHTTPMethod(method string) bool {
	switch method {
	case "*", http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return true
	default:
		return false
	}
}

func envFlag(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func envFlagWithDefault(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	return envFlag(key)
}

func parseTimeout(raw string) time.Duration {
	if strings.TrimSpace(raw) == "" {
		return defaultTimeout
	}
	timeout, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || timeout <= 0 {
		return defaultTimeout
	}
	return timeout
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
