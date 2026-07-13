package repositories

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func FuzzCanonicalMoneyEventOutboxJSON(f *testing.F) {
	f.Add(`{"event_type":"payment.succeeded.v1","resource_id":"payment-1"}`)
	f.Add(`{}`)
	f.Add(`[]`)
	f.Add(`{"amount_raw":"100"} trailing`)
	f.Add(`{"nested":{"b":2,"a":1}}`)

	f.Fuzz(func(t *testing.T, raw string) {
		canonical, err := canonicalMoneyEventOutboxJSON(limitFuzzString(raw, 2048))
		if err != nil {
			if !errors.Is(err, ErrMoneyEventOutboxInvalid) {
				t.Fatalf("unexpected error: %v", err)
			}
			return
		}
		if !strings.HasPrefix(canonical, "{") || !strings.HasSuffix(canonical, "}") {
			t.Fatalf("canonical payload is not an object: %q", canonical)
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(canonical), &decoded); err != nil {
			t.Fatalf("canonical payload is invalid JSON: %v", err)
		}
	})
}
