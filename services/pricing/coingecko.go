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
	coinID, ok := CoinGeckoID(symbol)
	if !ok {
		return nil, fmt.Errorf("CoinGecko price id is not configured for %s", strings.ToUpper(strings.TrimSpace(symbol)))
	}

	vsCurrency := strings.ToLower(strings.TrimSpace(currency))
	if vsCurrency == "" {
		vsCurrency = "usd"
	}
	key := coinID + "|" + vsCurrency

	c.mu.RLock()
	cached, ok := c.cache[key]
	if ok && c.now().Before(cached.expiresAt) {
		price := new(big.Rat).Set(cached.price)
		c.mu.RUnlock()
		return price, nil
	}
	c.mu.RUnlock()

	endpoint, err := url.Parse(c.baseURL + "/simple/price")
	if err != nil {
		return nil, err
	}
	query := endpoint.Query()
	query.Set("ids", coinID)
	query.Set("vs_currencies", vsCurrency)
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	if c.apiKey != "" {
		req.Header.Set("x-cg-demo-api-key", c.apiKey)
		req.Header.Set("x-cg-pro-api-key", c.apiKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("CoinGecko returned HTTP %d", resp.StatusCode)
	}

	var payload map[string]map[string]json.Number
	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}

	number, ok := payload[coinID][vsCurrency]
	if !ok {
		return nil, fmt.Errorf("CoinGecko price not found for %s/%s", coinID, vsCurrency)
	}
	price, ok := new(big.Rat).SetString(number.String())
	if !ok || price.Sign() <= 0 {
		return nil, errors.New("CoinGecko returned invalid price")
	}

	c.mu.Lock()
	c.cache[key] = cachedPrice{
		price:     new(big.Rat).Set(price),
		expiresAt: c.now().Add(c.ttl),
	}
	c.mu.Unlock()

	return price, nil
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
