package docs

import (
	"os"
	"strings"
	"testing"
)

func TestControlledLaunchReadinessSeparatesReadinessLevels(t *testing.T) {
	docBytes, err := os.ReadFile("controlled-launch-readiness.md")
	if err != nil {
		t.Fatalf("read controlled launch readiness: %v", err)
	}
	doc := string(docBytes)

	for _, token := range []string{
		"Gateway readiness levels are intentionally separated",
		"## Level 1: Controlled Merchant/Dealer Beta",
		"## Level 2: Production Payment Gateway",
		"## Level 3: Wallet-Provider Custody",
		"## Level 4: Exchange-Grade Tracking",
		"API auth, HMAC signing, idempotent payment creation, hosted checkout, and static address contracts are tested",
		"Durable money event outbox and webhook delivery retry/replay are enabled",
		"Ledger-derived balances are the only authoritative balance source",
		"Deposit finality gates settlement",
		"/api/v1/common/readiness",
		"External signer mode is implemented and tested",
		"AML/KYT, sanctions screening, travel-rule obligations",
		"Archive/quorum provider strategy is defined",
		"Unsupported scale claims are explicitly rejected",
		"Production payment gateway, wallet-provider custody, and exchange-grade tracking remain blocked",
	} {
		requireContains(t, doc, token)
	}

	level1 := markdownSection(doc, "## Level 1: Controlled Merchant/Dealer Beta", "## Level 2: Production Payment Gateway")
	for _, forbidden := range []string{
		"archive/quorum",
		"AML/KYT",
		"Signer quorum",
		"Exchange-Grade",
	} {
		if strings.Contains(level1, forbidden) {
			t.Fatalf("controlled beta section must not imply higher readiness gate %q", forbidden)
		}
	}
}

func TestProductReadinessAuditKeepsCustodyAndExchangeClaimsBlocked(t *testing.T) {
	docBytes, err := os.ReadFile("product-readiness-audit.md")
	if err != nil {
		t.Fatalf("read product readiness audit: %v", err)
	}
	doc := string(docBytes)

	for _, token := range []string{
		"not yet a production-grade wallet provider",
		"not ready for Binance-level exchange wallet tracking",
		"Controlled beta / internal pilot: yes",
		"Wallet-provider-as-a-service: partial",
		"Binance-scale exchange wallet tracking: no",
		"Key custody is still not backed by a real production provider",
		"Listener first start skips history",
		"Single-process architecture is a scaling ceiling",
		"archive/quorum exchange infrastructure",
		"Large wallet-set benchmark evidence",
	} {
		requireContains(t, doc, token)
	}
}
