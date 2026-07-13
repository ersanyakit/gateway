package repositories

import "testing"

func FuzzIdempotencyRequestHashStable(f *testing.F) {
	f.Add("ethereum", "ETH", "100", "0xabc", "memo")
	f.Add("solana", "SOL", "0", "", "")
	f.Add("tron", "USDT", "not-a-number", "contract", "checkout-1")

	f.Fuzz(func(t *testing.T, chain, symbol, amountRaw, token, memo string) {
		payload := map[string]any{
			"chain":      limitFuzzString(chain, 64),
			"symbol":     limitFuzzString(symbol, 32),
			"amount_raw": limitFuzzString(amountRaw, 128),
			"token":      limitFuzzString(token, 180),
			"memo":       limitFuzzString(memo, 180),
		}
		repo := &IdempotencyRepo{}
		first, err := repo.RequestHash(payload)
		if err != nil {
			t.Fatal(err)
		}
		second, err := repo.RequestHash(payload)
		if err != nil {
			t.Fatal(err)
		}
		if first != second || len(first) != 64 {
			t.Fatalf("request hash = %q/%q", first, second)
		}
	})
}
