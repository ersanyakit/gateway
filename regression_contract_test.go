package main

import (
	"os"
	"strings"
	"testing"
)

func TestRegressionHarnessDocumentsTrustWalletCoreModes(t *testing.T) {
	script := readRegressionContractFile(t, "scripts/regression.sh")
	workflow := readRegressionContractFile(t, ".github/workflows/regression.yml")

	for _, token := range []string{
		"GATEWAY_REGRESSION_MODE",
		"scripts/build_wallet_core.sh",
		"walletcorefallback",
		"third_party/trustwallet/wallet-core",
		"regression dependency failure",
	} {
		if !strings.Contains(script, token) {
			t.Fatalf("scripts/regression.sh missing %q", token)
		}
	}
	for _, token := range []string{
		"submodules: recursive",
		"git submodule update --init --recursive third_party/trustwallet/wallet-core",
		"GATEWAY_REGRESSION_MODE=native scripts/regression.sh",
		"GATEWAY_REGRESSION_MODE=fallback scripts/regression.sh",
	} {
		if !strings.Contains(workflow, token) {
			t.Fatalf(".github/workflows/regression.yml missing %q", token)
		}
	}
}

func readRegressionContractFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
