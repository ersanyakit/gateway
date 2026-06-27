package chains

import (
	"bytes"
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"core/blockchain"
	"core/blockchain/walletcore"
	"core/constants"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/wire"
)

func TestBitcoinSignP2WPKHUsesTrustWalletCore(t *testing.T) {
	chain := NewBitcoinChain()
	privateKeyHex := "13fcaabaf9e71ffaf915e242ec58a743d55f102cf836968e5bd4881135e0c52c"
	privateKeyBytes, err := hex.DecodeString(privateKeyHex)
	if err != nil {
		t.Fatal(err)
	}
	_, pubKey := btcec.PrivKeyFromBytes(privateKeyBytes)
	wallet := blockchain.WalletDetails{
		PrivateKey: privateKeyHex,
		Address:    "bc1qpjult34k9spjfym8hss2jrwjgf0xjf40ze0pp8",
	}
	utxos := []btcUTXO{{
		Txid:  "c24bd72e3eaea797bd5c879480a0db90980297bc7085efda97df2bf7d31413fb",
		Vout:  1,
		Value: 49_429,
	}}

	rawTxHex, txID, err := chain.signBitcoinWithTrustWallet(
		wallet,
		privateKeyBytes,
		pubKey,
		"bc1ptmsk7c2yut2xah4pgflpygh2s7fh0cpfkrza9cjj29awapv53mrslgd5cf",
		1_100,
		false,
		utxos,
	)
	if err != nil {
		t.Fatal(err)
	}
	if rawTxHex == "" {
		t.Fatal("raw tx is empty")
	}
	if txID == "" {
		t.Fatal("tx id is empty")
	}
}

func TestBitcoinSignTaprootUsesManualFallback(t *testing.T) {
	chain := NewBitcoinChain()
	derived, err := walletcore.DeriveWallet(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		"m/86'/0'/0'/0/0",
		constants.Bitcoin,
	)
	if err != nil {
		if strings.Contains(err.Error(), "walletcorefallback") {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	privateKeyBytes, err := hex.DecodeString(derived.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	_, pubKey := btcec.PrivKeyFromBytes(privateKeyBytes)
	wallet := blockchain.WalletDetails{PrivateKey: derived.PrivateKey, Address: derived.Address}
	utxos := []btcUTXO{{
		Txid:  strings.Repeat("11", 32),
		Vout:  0,
		Value: 100_000,
	}}

	rawTxHex, txID, err := chain.signBitcoinWithTrustWallet(
		wallet,
		privateKeyBytes,
		pubKey,
		"bc1qpjult34k9spjfym8hss2jrwjgf0xjf40ze0pp8",
		10_000,
		false,
		utxos,
	)
	if err != nil {
		t.Fatal(err)
	}
	if rawTxHex == "" {
		t.Fatal("raw tx is empty")
	}
	if txID == "" {
		t.Fatal("tx id is empty")
	}
}

func TestBitcoinSignTaprootSweepUsesManualFallback(t *testing.T) {
	chain := NewBitcoinChain()
	derived, err := walletcore.DeriveWallet(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		"m/86'/0'/0'/0/0",
		constants.Bitcoin,
	)
	if err != nil {
		if strings.Contains(err.Error(), "walletcorefallback") {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	privateKeyBytes, err := hex.DecodeString(derived.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	_, pubKey := btcec.PrivKeyFromBytes(privateKeyBytes)
	wallet := blockchain.WalletDetails{PrivateKey: derived.PrivateKey, Address: derived.Address}
	utxos := []btcUTXO{{
		Txid:  strings.Repeat("22", 32),
		Vout:  1,
		Value: 50_000,
	}}

	rawTxHex, txID, err := chain.signBitcoinWithTrustWallet(
		wallet,
		privateKeyBytes,
		pubKey,
		"bc1qpjult34k9spjfym8hss2jrwjgf0xjf40ze0pp8",
		50_000,
		true,
		utxos,
	)
	if err != nil {
		t.Fatal(err)
	}
	if txID == "" {
		t.Fatal("tx id is empty")
	}
	tx := decodeBitcoinRawTx(t, rawTxHex)
	if len(tx.TxOut) != 1 {
		t.Fatalf("expected one sweep output, got %d", len(tx.TxOut))
	}
	if tx.TxOut[0].Value <= 0 || tx.TxOut[0].Value >= utxos[0].Value {
		t.Fatalf("unexpected sweep output value %d", tx.TxOut[0].Value)
	}
}

func TestBitcoinBroadcastRequiresEndpoint(t *testing.T) {
	txID, err := btcBroadcast(context.Background(), nil, "00")
	if err == nil {
		t.Fatal("expected missing endpoint error")
	}
	if txID != "" {
		t.Fatalf("expected empty tx id, got %q", txID)
	}
}

func TestBitcoinGetBalanceAnnotatesFailingRPC(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`bad gateway`))
	}))
	defer first.Close()

	chain := NewBitcoinChain()
	chain.RPCHttp = []string{first.URL}

	_, err := chain.getBalance(first.Client(), "bc1qpjult34k9spjfym8hss2jrwjgf0xjf40ze0pp8")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), first.URL) {
		t.Fatalf("error %q does not include endpoint %q", err.Error(), first.URL)
	}
}

func decodeBitcoinRawTx(t *testing.T, rawTxHex string) *wire.MsgTx {
	t.Helper()
	raw, err := hex.DecodeString(rawTxHex)
	if err != nil {
		t.Fatal(err)
	}
	var tx wire.MsgTx
	if err := tx.Deserialize(bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	return &tx
}
