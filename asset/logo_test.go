package asset

import "testing"

func TestCoinLogoURLFromSlugFormatsConfiguredSlug(t *testing.T) {
	tests := map[string]string{
		"tbt":      "/static/coins/tbt.svg",
		"LGBT":     "/static/coins/lgbt.svg",
		" pepper ": "/static/coins/pepper.svg",
	}

	for slug, want := range tests {
		t.Run(slug, func(t *testing.T) {
			if got := CoinLogoURLFromSlug(slug); got != want {
				t.Fatalf("CoinLogoURLFromSlug(%q) = %q, want %q", slug, got, want)
			}
		})
	}
}
