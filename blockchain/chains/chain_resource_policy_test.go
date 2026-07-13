package chains

import (
	"os"
	"strings"
	"testing"
)

func TestEVMTransfersReserveNonceAndValidateGasBeforePrivateKeySigning(t *testing.T) {
	source := readChainSource(t, "evm_transfer.go")
	for _, functionName := range []string{"evmSendNativeWithClient", "evmSendERC20WithClient"} {
		body := extractChainFunctionBody(t, source, functionName)
		for _, token := range []string{
			"chainresource.ValidateEVMGasPolicy",
			"requireDatabaseResourceReservation",
			"chainResources.ReserveNonce",
			"chainResourceOwnerID(ctx, wallet",
			"nonceReservation.Release()",
			"nonceReservation.Consume(txHash)",
		} {
			if !strings.Contains(body, token) {
				t.Fatalf("%s missing %q", functionName, token)
			}
		}
		reserveIndex := strings.Index(body, "chainResources.ReserveNonce")
		privateKeyIndex := strings.Index(body, "evmPrivateKeyAndAddress(wallet)")
		if reserveIndex == -1 || privateKeyIndex == -1 || reserveIndex > privateKeyIndex {
			t.Fatalf("%s must reserve nonce before private key access", functionName)
		}
	}
}

func TestBitcoinTransfersReserveUTXOsBeforePrivateKeySigning(t *testing.T) {
	source := readChainSource(t, "bitcoin_transfer.go")
	if !strings.Contains(source, "chainresource.BitcoinFeeRateSatPerVByte") {
		t.Fatal("bitcoin_transfer.go must use chainresource fee policy")
	}
	for _, functionName := range []string{"sendTo", "SweepTo"} {
		body := extractChainFunctionBody(t, source, functionName)
		for _, token := range []string{
			"chainResources.ReserveUTXOs",
			"requireDatabaseResourceReservation",
			"btcChainResourceUTXOs",
			"utxoReservation.Release()",
			"utxoReservation.Consume",
		} {
			if !strings.Contains(body, token) {
				t.Fatalf("%s missing %q", functionName, token)
			}
		}
		reserveIndex := strings.Index(body, "chainResources.ReserveUTXOs")
		privateKeyIndex := strings.Index(body, "bitcoinPrivateKeyBytes(wallet)")
		if reserveIndex == -1 || privateKeyIndex == -1 || reserveIndex > privateKeyIndex {
			t.Fatalf("%s must reserve UTXOs before private key access", functionName)
		}
	}
}

func TestSolanaAndTronUseSequenceLeaseAndResourceFeePolicies(t *testing.T) {
	solanaNative := readChainSource(t, "solana.go")
	if body := extractChainFunctionBody(t, solanaNative, "sendLamportsWithClient"); !strings.Contains(body, "chainResourceSequenceLease") ||
		!strings.Contains(body, "requireDatabaseResourceReservation") ||
		!strings.Contains(body, "solanaTransferFeeLamports()") ||
		!strings.Contains(body, "lease.Consume(signature.String())") {
		t.Fatalf("sendLamportsWithClient missing sequence lease or fee policy")
	}

	solanaToken := readChainSource(t, "solana_transfer.go")
	for _, functionName := range []string{"SweepERC20To", "sendSPL"} {
		body := extractChainFunctionBody(t, solanaToken, functionName)
		if !strings.Contains(body, "chainResourceSequenceLease") ||
			!strings.Contains(body, "requireDatabaseResourceReservation") ||
			!strings.Contains(body, "lease.Release()") ||
			!strings.Contains(body, "lease.Consume") {
			t.Fatalf("%s missing sequence lease guard", functionName)
		}
	}

	tron := readChainSource(t, "tron_transfer.go")
	for _, functionName := range []string{"sendTRX", "sendTRC20", "SweepTo", "SweepERC20To", "PrefundGas"} {
		body := extractChainFunctionBody(t, tron, functionName)
		if !strings.Contains(body, "chainResourceSequenceLease") ||
			!strings.Contains(body, "requireDatabaseResourceReservation") ||
			!strings.Contains(body, "lease.Release()") ||
			(!strings.Contains(body, "tronSignBroadcastWithLease") && !strings.Contains(body, "tronBroadcastSignedWithLease")) {
			t.Fatalf("%s missing TRON sequence lease guard", functionName)
		}
	}
	for _, functionName := range []string{"sendTRX", "sendTRC20", "SweepERC20To", "PrefundGas"} {
		body := extractChainFunctionBody(t, tron, functionName)
		if !strings.Contains(body, "broadcastErr") || !strings.Contains(body, "if broadcastErr != nil") {
			t.Fatalf("%s must propagate TRON broadcast failures", functionName)
		}
	}
	if helper := extractChainFunctionBody(t, tron, "tronSignBroadcastWithLease"); !strings.Contains(helper, "lease.Release()") ||
		!strings.Contains(helper, "lease.Consume") {
		t.Fatal("tronSignBroadcastWithLease must release before broadcast and consume after broadcast attempt")
	}
	for _, token := range []string{
		"chainresource.TronTRC20FeeLimitSUN()",
		"chainresource.TronNativeSweepFeeSUN()",
	} {
		if !strings.Contains(tron, token) {
			t.Fatalf("tron_transfer.go missing %q", token)
		}
	}
}

func readChainSource(t *testing.T, filename string) string {
	t.Helper()
	source, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	return string(source)
}

func extractChainFunctionBody(t *testing.T, source, functionName string) string {
	t.Helper()
	start := strings.Index(source, "func ")
	for start != -1 {
		remaining := source[start:]
		open := strings.Index(remaining, "{")
		if open == -1 {
			t.Fatalf("function %s has no opening brace", functionName)
		}
		signature := remaining[:open]
		if strings.Contains(signature, " "+functionName+"(") || strings.Contains(signature, ") "+functionName+"(") {
			index := start + open
			depth := 0
			for i := index; i < len(source); i++ {
				switch source[i] {
				case '{':
					depth++
				case '}':
					depth--
					if depth == 0 {
						return source[index : i+1]
					}
				}
			}
			t.Fatalf("function %s has no closing brace", functionName)
		}
		next := strings.Index(remaining[5:], "func ")
		if next == -1 {
			break
		}
		start += 5 + next
	}
	t.Fatalf("function %s not found", functionName)
	return ""
}
