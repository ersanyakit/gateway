package asset

import "testing"

func TestCoinLogoURLKnownLocalLogos(t *testing.T) {
	tests := map[string]string{
		"TBT":    "/static/coins/tbt.svg",
		"LGBT":   "/static/coins/lgbt.svg",
		"PEPPER": "/static/coins/pepper.svg",
		"CHZINU": "/static/coins/chzinu.svg",
	}

	for symbol, want := range tests {
		t.Run(symbol, func(t *testing.T) {
			if got := CoinLogoURL(symbol); got != want {
				t.Fatalf("CoinLogoURL(%q) = %q, want %q", symbol, got, want)
			}
		})
	}
}
