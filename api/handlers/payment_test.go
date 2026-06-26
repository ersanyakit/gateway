package handlers

import (
	"context"
	"errors"
	"html/template"
	"math/big"
	"testing"

	"core/asset"
	"core/constants"
	"core/models"
)

type fakePriceOracle struct {
	prices map[string]string
}

func (f fakePriceOracle) Price(_ context.Context, symbol string, currency string) (*big.Rat, error) {
	price, ok := f.prices[symbol+"|"+currency]
	if !ok {
		price = f.prices[symbol+"|USD"]
	}
	rat, _ := new(big.Rat).SetString(price)
	return rat, nil
}

type failingPriceOracle struct{}

func (f failingPriceOracle) Price(context.Context, string, string) (*big.Rat, error) {
	return nil, errors.New("pricing unavailable")
}

type panicPriceOracle struct{}

func (p panicPriceOracle) Price(context.Context, string, string) (*big.Rat, error) {
	panic("checkout asset groups should not call the price oracle")
}

func TestCheckoutCanonicalSymbolAliases(t *testing.T) {
	tests := map[string]string{
		"WBTC": "BTC",
		"btc":  "BTC",
		"WETH": "ETH",
		"eth":  "ETH",
		"WCHZ": "CHZ",
		"chz":  "CHZ",
		"SOL":  "SOL",
	}
	for input, expected := range tests {
		if got := checkoutCanonicalSymbol(input); got != expected {
			t.Fatalf("checkoutCanonicalSymbol(%q) = %q, want %q", input, got, expected)
		}
	}
}

func TestCheckoutTemplateParses(t *testing.T) {
	if _, err := template.ParseFiles("../../views/gateway/checkout.html"); err != nil {
		t.Fatal(err)
	}
}

func TestCheckoutAssetSelectionDoesNotCallPriceOracle(t *testing.T) {
	registry := asset.NewRegistry()
	registry.Register(asset.NewERC20(
		constants.Ethereum,
		"0x1111111111111111111111111111111111111111",
		"PEPPER",
		"PEPPER",
		18,
	))
	session := models.PaymentSession{
		SessionToken: "checkout-token",
		Amount:       "10",
		Currency:     "USD",
		Wallet: models.Wallet{
			EthereumAddress: "0x2222222222222222222222222222222222222222",
		},
	}
	deps := PaymentHandlerDeps{
		AssetRegistry: registry,
		PriceOracle:   panicPriceOracle{},
	}

	options := checkoutAssetOptions(context.Background(), deps, session, "PEPPER")
	if len(options) != 1 {
		t.Fatalf("options = %d, want 1", len(options))
	}
	if !options[0].Available {
		t.Fatal("asset with an address should be selectable before the final quote step")
	}
	if options[0].QuoteAvailable {
		t.Fatal("asset options should not quote during route rendering")
	}
	if options[0].UnavailableReason != "" {
		t.Fatalf("unavailable reason = %q, want empty", options[0].UnavailableReason)
	}
	if options[0].AmountDisplay != "" {
		t.Fatalf("amount display = %q, want empty", options[0].AmountDisplay)
	}

	groupDeps := deps
	groupDeps.PriceOracle = panicPriceOracle{}
	groups := checkoutAssetGroups(context.Background(), groupDeps, session)
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(groups))
	}
	if groups[0].Symbol != "PEPPER" {
		t.Fatalf("group symbol = %q, want PEPPER", groups[0].Symbol)
	}
	if groups[0].QuoteAvailable {
		t.Fatal("group quote should be marked unavailable")
	}
	if groups[0].ChainCount != 1 {
		t.Fatalf("chain count = %d, want 1", groups[0].ChainCount)
	}
}

func TestPaymentDepositAddressForChainRejectsMismatchedAddressFamilies(t *testing.T) {
	wallet := models.Wallet{
		EthereumAddress: "0x1111111111111111111111111111111111111111",
		SolanaAddress:   "So11111111111111111111111111111111111111112",
		TronAddress:     "TLa2f6VPqDgRE67v1736s7bJ8Ray5wYjU7",
		BitcoinAddress:  "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kygt080",
	}
	if got := paymentDepositAddressForChain(wallet, constants.Solana); got != wallet.SolanaAddress {
		t.Fatalf("solana deposit address = %q, want %q", got, wallet.SolanaAddress)
	}
	wallet.SolanaAddress = wallet.EthereumAddress
	if got := paymentDepositAddressForChain(wallet, constants.Solana); got != "" {
		t.Fatalf("mismatched solana deposit address = %q, want empty", got)
	}
	if got := paymentDepositAddressForChain(wallet, constants.Ethereum); got != wallet.EthereumAddress {
		t.Fatalf("ethereum deposit address = %q, want %q", got, wallet.EthereumAddress)
	}
}

func TestCheckoutExpectedAmountRawUsesFiatPrice(t *testing.T) {
	session := models.PaymentSession{
		Amount:   "1",
		Currency: "USD",
	}
	eth := asset.NewEVMNative(constants.Ethereum, "ETH", "Ethereum", 18)
	amountRaw, err := checkoutExpectedAmountRaw(context.Background(), fakePriceOracle{
		prices: map[string]string{
			"ETH|USD": "2000",
		},
	}, session, eth)
	if err != nil {
		t.Fatal(err)
	}
	if amountRaw != "500000000000000" {
		t.Fatalf("amount raw = %s, want 500000000000000", amountRaw)
	}
	if got := formatPaymentAmount(amountRaw, eth.GetDecimals(), eth.GetSymbol()); got != "0.0005 ETH" {
		t.Fatalf("amount display = %q, want 0.0005 ETH", got)
	}
}

func TestCheckoutExpectedAmountRawRoundsUp(t *testing.T) {
	session := models.PaymentSession{
		Amount:   "1",
		Currency: "USD",
	}
	usdc := asset.NewERC20(constants.Ethereum, "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", "USDC", "USDC", 6)
	amountRaw, err := checkoutExpectedAmountRaw(context.Background(), fakePriceOracle{
		prices: map[string]string{
			"USDC|USD": "3",
		},
	}, session, usdc)
	if err != nil {
		t.Fatal(err)
	}
	if amountRaw != "333334" {
		t.Fatalf("amount raw = %s, want 333334", amountRaw)
	}
}
