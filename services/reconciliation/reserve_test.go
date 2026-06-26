package reconciliation

import (
	"math/big"
	"testing"

	"core/constants"
)

func TestParseBalanceComponents(t *testing.T) {
	components := parseBalanceComponents("ETH:1.500000000000000000 | WETH:0.000000000000000001")
	if components["ETH"] != "1.500000000000000000" {
		t.Fatalf("ETH component = %q", components["ETH"])
	}
	if components["WETH"] != "0.000000000000000001" {
		t.Fatalf("WETH component = %q", components["WETH"])
	}

	raw := parseBalanceComponents("0x10")
	if raw[""] != "0x10" {
		t.Fatalf("raw component = %q, want 0x10", raw[""])
	}
}

func TestAmountToRawConvertsDecimalUnits(t *testing.T) {
	amount, ok := amountToRaw("1.500000000000000000", 18)
	if !ok {
		t.Fatal("decimal amount should be readable")
	}
	if amount.String() != "1500000000000000000" {
		t.Fatalf("amount = %s, want 1500000000000000000", amount)
	}
}

func TestAmountToRawRejectsPrecisionThatCannotBeRepresented(t *testing.T) {
	if _, ok := amountToRaw("0.000000000001000000", 6); ok {
		t.Fatal("18-decimal formatted token amount should not be coerced into a 6-decimal raw value")
	}
}

func TestEvaluateExpectedReserveDetectsDeficit(t *testing.T) {
	expected := reserveExpectedBalance{
		Symbol:     "ETH",
		Decimals:   18,
		BalanceRaw: mustBigInt("2000000000000000000"),
	}
	components := parseBalanceComponents("ETH:1.500000000000000000")
	if got := evaluateExpectedReserve(components, expected, constants.Ethereum); got != reserveIssueDeficit {
		t.Fatalf("issue = %q, want %q", got, reserveIssueDeficit)
	}
}

func TestEvaluateExpectedReserveAcceptsSufficientBalance(t *testing.T) {
	expected := reserveExpectedBalance{
		Symbol:     "ETH",
		Decimals:   18,
		BalanceRaw: mustBigInt("1500000000000000000"),
	}
	components := parseBalanceComponents("ETH:2.000000000000000000")
	if got := evaluateExpectedReserve(components, expected, constants.Ethereum); got != reserveIssueNone {
		t.Fatalf("issue = %q, want none", got)
	}
}

func TestEvaluateExpectedReserveUsesRawNativeComponent(t *testing.T) {
	expected := reserveExpectedBalance{
		Symbol:     "TRX",
		Decimals:   6,
		BalanceRaw: mustBigInt("1000000"),
	}
	components := parseBalanceComponents("0x200000")
	if got := evaluateExpectedReserve(components, expected, constants.TRON); got != reserveIssueNone {
		t.Fatalf("issue = %q, want none", got)
	}
}

func TestEvaluateExpectedReserveRejectsUnreadableComponent(t *testing.T) {
	token := "0x1111111111111111111111111111111111111111"
	expected := reserveExpectedBalance{
		Token:      &token,
		Symbol:     "USDT",
		Decimals:   6,
		BalanceRaw: mustBigInt("1000000"),
	}
	components := parseBalanceComponents("USDT:0.000000000001000000")
	if got := evaluateExpectedReserve(components, expected, constants.Ethereum); got != reserveIssueUnreadable {
		t.Fatalf("issue = %q, want %q", got, reserveIssueUnreadable)
	}
}

func mustBigInt(raw string) *big.Int {
	value, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		panic(raw)
	}
	return value
}
