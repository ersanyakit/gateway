package pricing

import (
	"bytes"
	"context"
	"math/big"
	"net/http"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestCoinGeckoIDAliases(t *testing.T) {
	tests := map[string]string{
		"BTC":  "bitcoin",
		"WBTC": "bitcoin",
		"eth":  "ethereum",
		"WETH": "ethereum",
		"USDT": "tether",
		"USDC": "usd-coin",
		"SOL":  "solana",
	}
	for symbol, expected := range tests {
		got, ok := CoinGeckoID(symbol)
		if !ok {
			t.Fatalf("CoinGeckoID(%q) missing", symbol)
		}
		if got != expected {
			t.Fatalf("CoinGeckoID(%q) = %q, want %q", symbol, got, expected)
		}
	}
	if _, ok := CoinGeckoID("UNKNOWN"); ok {
		t.Fatal("unknown symbol should not resolve")
	}
}

func TestCoinGeckoPriceFetchesAndCaches(t *testing.T) {
	clearPriceFallbacks(t, "ETH", "WETH")
	var requests int
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if got := r.URL.Query().Get("ids"); got != "ethereum" {
			t.Fatalf("ids = %q", got)
		}
		if got := r.URL.Query().Get("vs_currencies"); got != "usd" {
			t.Fatalf("vs_currencies = %q", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       ioNopCloser(`{"ethereum":{"usd":2000.5}}`),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})}

	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	oracle := &CoinGecko{
		client:  client,
		baseURL: "https://coingecko.test",
		ttl:     time.Minute,
		now:     func() time.Time { return now },
		cache:   make(map[string]cachedPrice),
	}

	price, err := oracle.Price(context.Background(), "ETH", "USD")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := new(big.Rat).SetString("2000.5")
	if price.Cmp(want) != 0 {
		t.Fatalf("price = %s, want %s", price, want)
	}

	price, err = oracle.Price(context.Background(), "WETH", "USD")
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want cache hit after first request", requests)
	}
}

func TestCoinGeckoPriceUsesStablecoinUSDFallbackWithoutHTTP(t *testing.T) {
	clearPriceFallbacks(t, "USDC")
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("stablecoin fallback should not call HTTP: %s", r.URL.String())
		return nil, nil
	})}
	oracle := &CoinGecko{
		client:  client,
		baseURL: "https://coingecko.test",
		ttl:     time.Minute,
		now:     time.Now,
		cache:   make(map[string]cachedPrice),
	}

	price, err := oracle.Price(context.Background(), "USDC", "USD")
	if err != nil {
		t.Fatal(err)
	}
	if price.Cmp(big.NewRat(1, 1)) != 0 {
		t.Fatalf("price = %s, want 1", price)
	}
}

func TestCoinGeckoPriceUsesConfiguredFallbackWithoutHTTP(t *testing.T) {
	t.Setenv("PRICE_PEPPER_USD", "0.0001")
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("configured fallback should not call HTTP: %s", r.URL.String())
		return nil, nil
	})}
	oracle := &CoinGecko{
		client:  client,
		baseURL: "https://coingecko.test",
		ttl:     time.Minute,
		now:     time.Now,
		cache:   make(map[string]cachedPrice),
	}

	price, err := oracle.Price(context.Background(), "PEPPER", "USD")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := new(big.Rat).SetString("0.0001")
	if price.Cmp(want) != 0 {
		t.Fatalf("price = %s, want %s", price, want)
	}
}

func TestCoinGeckoPriceReturnsStaleCacheOnFetchError(t *testing.T) {
	clearPriceFallbacks(t, "ETH")
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Body:       ioNopCloser(`{"error":"down"}`),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})}
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	stale, _ := new(big.Rat).SetString("1999.25")
	oracle := &CoinGecko{
		client:  client,
		baseURL: "https://coingecko.test",
		ttl:     time.Minute,
		now:     func() time.Time { return now },
		cache: map[string]cachedPrice{
			"ethereum|usd": {
				price:     stale,
				expiresAt: now.Add(-time.Minute),
			},
		},
	}

	price, err := oracle.Price(context.Background(), "ETH", "USD")
	if err != nil {
		t.Fatal(err)
	}
	if price.Cmp(stale) != 0 {
		t.Fatalf("price = %s, want stale %s", price, stale)
	}
}

func TestCoinGeckoPriceRejectsInvalidResponses(t *testing.T) {
	clearPriceFallbacks(t, "ETH")
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       ioNopCloser(`{"ethereum":{"usd":0}}`),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})}
	oracle := &CoinGecko{
		client:  client,
		baseURL: "https://coingecko.test",
		ttl:     time.Minute,
		now:     time.Now,
		cache:   make(map[string]cachedPrice),
	}
	if _, err := oracle.Price(context.Background(), "ETH", "USD"); err == nil {
		t.Fatal("zero price should fail")
	}
}

func ioNopCloser(body string) *nopCloser {
	return &nopCloser{bytes.NewBufferString(body)}
}

type nopCloser struct {
	*bytes.Buffer
}

func (n *nopCloser) Close() error { return nil }

func clearPriceFallbacks(t *testing.T, symbols ...string) {
	t.Helper()
	for _, symbol := range symbols {
		for _, prefix := range []string{"PRICE_", "GATEWAY_PRICE_"} {
			t.Setenv(prefix+symbol+"_USD", "")
		}
	}
}
