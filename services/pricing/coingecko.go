package pricing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

type PriceOracle interface {
	Price(ctx context.Context, symbol string, currency string) (*big.Rat, error)
}

type CoinGecko struct {
	client  *http.Client
	baseURL string
	apiKey  string
	ttl     time.Duration
	now     func() time.Time

	mu    sync.RWMutex
	cache map[string]cachedPrice
}

type cachedPrice struct {
	price     *big.Rat
	expiresAt time.Time
}

func NewCoinGecko() *CoinGecko {
	baseURL := strings.TrimRight(os.Getenv("COINGECKO_BASE_URL"), "/")
	if baseURL == "" {
		baseURL = "https://api.coingecko.com/api/v3"
	}

	ttl := 60 * time.Second
	if raw := os.Getenv("COINGECKO_CACHE_TTL"); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			ttl = parsed
		}
	}

	return &CoinGecko{
		client: &http.Client{
			Timeout: 8 * time.Second,
		},
		baseURL: baseURL,
		apiKey:  strings.TrimSpace(os.Getenv("COINGECKO_API_KEY")),
		ttl:     ttl,
		now:     time.Now,
		cache:   make(map[string]cachedPrice),
	}
}

func (c *CoinGecko) Price(ctx context.Context, symbol string, currency string) (*big.Rat, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	vsCurrency := strings.ToLower(strings.TrimSpace(currency))
	if vsCurrency == "" {
		vsCurrency = "usd"
	}

	if price, ok, err := configuredFallbackPrice(symbol, vsCurrency); ok || err != nil {
		return price, err
	}
	if price, ok := stablecoinFallbackPrice(symbol, vsCurrency); ok {
		return price, nil
	}

	coinID, ok := CoinGeckoID(symbol)
	if !ok {
		return nil, fmt.Errorf("CoinGecko price id is not configured for %s", symbol)
	}

	key := coinID + "|" + vsCurrency

	var stalePrice *big.Rat
	c.mu.RLock()
	cached, ok := c.cache[key]
	if ok && cached.price != nil {
		price := new(big.Rat).Set(cached.price)
		if c.now().Before(cached.expiresAt) {
			c.mu.RUnlock()
			return price, nil
		}
		stalePrice = price
	}
	c.mu.RUnlock()

	endpoint, err := url.Parse(c.baseURL + "/simple/price")
	if err != nil {
		return staleOrError(stalePrice, err)
	}
	query := endpoint.Query()
	query.Set("ids", coinID)
	query.Set("vs_currencies", vsCurrency)
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return staleOrError(stalePrice, err)
	}
	if c.apiKey != "" {
		req.Header.Set("x-cg-demo-api-key", c.apiKey)
		req.Header.Set("x-cg-pro-api-key", c.apiKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return staleOrError(stalePrice, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return staleOrError(stalePrice, fmt.Errorf("CoinGecko returned HTTP %d", resp.StatusCode))
	}

	var payload map[string]map[string]json.Number
	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return staleOrError(stalePrice, err)
	}

	number, ok := payload[coinID][vsCurrency]
	if !ok {
		return staleOrError(stalePrice, fmt.Errorf("CoinGecko price not found for %s/%s", coinID, vsCurrency))
	}
	price, ok := new(big.Rat).SetString(number.String())
	if !ok || price.Sign() <= 0 {
		return staleOrError(stalePrice, errors.New("CoinGecko returned invalid price"))
	}

	c.mu.Lock()
	c.cache[key] = cachedPrice{
		price:     new(big.Rat).Set(price),
		expiresAt: c.now().Add(c.ttl),
	}
	c.mu.Unlock()

	return price, nil
}

func staleOrError(stalePrice *big.Rat, err error) (*big.Rat, error) {
	if stalePrice != nil && stalePrice.Sign() > 0 {
		return new(big.Rat).Set(stalePrice), nil
	}
	return nil, err
}

func configuredFallbackPrice(symbol string, currency string) (*big.Rat, bool, error) {
	symbolPart := priceEnvPart(symbol)
	currencyPart := priceEnvPart(currency)
	if symbolPart == "" || currencyPart == "" {
		return nil, false, nil
	}
	for _, key := range []string{
		"PRICE_" + symbolPart + "_" + currencyPart,
		"GATEWAY_PRICE_" + symbolPart + "_" + currencyPart,
	} {
		raw := strings.TrimSpace(os.Getenv(key))
		if raw == "" {
			continue
		}
		price, ok := new(big.Rat).SetString(raw)
		if !ok || price.Sign() <= 0 {
			return nil, true, fmt.Errorf("%s must be a positive decimal price", key)
		}
		return price, true, nil
	}
	return nil, false, nil
}

func stablecoinFallbackPrice(symbol string, currency string) (*big.Rat, bool) {
	if strings.ToLower(strings.TrimSpace(currency)) != "usd" {
		return nil, false
	}
	switch strings.ToUpper(strings.TrimSpace(symbol)) {
	case "USDT", "USDC":
		return big.NewRat(1, 1), true
	default:
		return nil, false
	}
}

func priceEnvPart(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	var b strings.Builder
	lastUnderscore := false
	for _, char := range value {
		isAlpha := char >= 'A' && char <= 'Z'
		isDigit := char >= '0' && char <= '9'
		if isAlpha || isDigit {
			b.WriteRune(char)
			lastUnderscore = false
			continue
		}
		if b.Len() > 0 && !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func CoinGeckoID(symbol string) (string, bool) {
	switch strings.ToUpper(strings.TrimSpace(symbol)) {
	case "BTC", "WBTC":
		return "bitcoin", true
	case "ETH", "WETH":
		return "ethereum", true
	case "CHZ", "WCHZ":
		return "chiliz", true
	case "SOL":
		return "solana", true
	case "TRX":
		return "tron", true
	case "AVAX":
		return "avalanche-2", true
	case "BNB":
		return "binancecoin", true
	case "USDT":
		return "tether", true
	case "USDC":
		return "usd-coin", true
	default:
		return "", false
	}
}
