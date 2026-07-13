package chains

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"core/blockchain"
	"core/blockchain/walletcore"
	"core/constants"
)

func TestBitcoinSignP2WPKHUsesTrustWalletCore(t *testing.T) {
	chain := NewBitcoinChain()
	privateKeyHex := "13fcaabaf9e71ffaf915e242ec58a743d55f102cf836968e5bd4881135e0c52c"
	privateKeyBytes, err := hex.DecodeString(privateKeyHex)
	if err != nil {
		t.Fatal(err)
	}
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
		"bc1ptmsk7c2yut2xah4pgflpygh2s7fh0cpfkrza9cjj29awapv53mrslgd5cf",
		1_100,
		false,
		utxos,
	)
	if err != nil {
		if strings.Contains(err.Error(), "walletcorefallback") {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	if rawTxHex == "" {
		t.Fatal("raw tx is empty")
	}
	if txID == "" {
		t.Fatal("tx id is empty")
	}
}

func TestBitcoinSignTaprootUsesTrustWalletCore(t *testing.T) {
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
	wallet := blockchain.WalletDetails{PrivateKey: derived.PrivateKey, Address: derived.Address}
	utxos := []btcUTXO{{
		Txid:  strings.Repeat("11", 32),
		Vout:  0,
		Value: 100_000,
	}}

	rawTxHex, txID, err := chain.signBitcoinWithTrustWallet(
		wallet,
		privateKeyBytes,
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

func TestBitcoinSignTaprootSweepUsesTrustWalletCore(t *testing.T) {
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
	wallet := blockchain.WalletDetails{PrivateKey: derived.PrivateKey, Address: derived.Address}
	utxos := []btcUTXO{{
		Txid:  strings.Repeat("22", 32),
		Vout:  1,
		Value: 50_000,
	}}

	rawTxHex, txID, err := chain.signBitcoinWithTrustWallet(
		wallet,
		privateKeyBytes,
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
	outputs := decodeBitcoinOutputValues(t, rawTxHex)
	if len(outputs) != 1 {
		t.Fatalf("expected one sweep output, got %d", len(outputs))
	}
	if outputs[0] <= 0 || outputs[0] >= utxos[0].Value {
		t.Fatalf("unexpected sweep output value %d", outputs[0])
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

	_, err := chain.getBalance(context.Background(), first.Client(), "bc1qpjult34k9spjfym8hss2jrwjgf0xjf40ze0pp8")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), first.URL) {
		t.Fatalf("error %q does not include endpoint %q", err.Error(), first.URL)
	}
}

func TestBitcoinBatchBalancesNormalizesZeroWorkers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"chain_stats":{"funded_txo_sum":100,"spent_txo_sum":30},"mempool_stats":{"funded_txo_sum":5,"spent_txo_sum":2}}`))
	}))
	defer server.Close()

	chain := NewBitcoinChain()
	chain.RPCHttp = []string{server.URL}
	results := chain.BatchBalances(context.Background(), []string{"bc1qpjult34k9spjfym8hss2jrwjgf0xjf40ze0pp8"}, 0)
	if len(results) != 1 || results[0].Error != nil || results[0].Balance != "73" {
		t.Fatalf("results = %#v, want one successful balance", results)
	}
}

func decodeBitcoinOutputValues(t *testing.T, rawTxHex string) []int64 {
	t.Helper()
	raw, err := hex.DecodeString(rawTxHex)
	if err != nil {
		t.Fatal(err)
	}
	reader := bitcoinTestReader(raw)
	if _, err := reader.read(4); err != nil {
		t.Fatal(err)
	}
	marker, err := reader.peek()
	if err != nil {
		t.Fatal(err)
	}
	if marker == 0 {
		if _, err := reader.read(2); err != nil {
			t.Fatal(err)
		}
	}
	inputCount, err := reader.readVarInt()
	if err != nil {
		t.Fatal(err)
	}
	for i := uint64(0); i < inputCount; i++ {
		if _, err := reader.read(36); err != nil {
			t.Fatal(err)
		}
		scriptLen, err := reader.readVarInt()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := reader.read(int(scriptLen) + 4); err != nil {
			t.Fatal(err)
		}
	}
	outputCount, err := reader.readVarInt()
	if err != nil {
		t.Fatal(err)
	}
	values := make([]int64, 0, outputCount)
	for i := uint64(0); i < outputCount; i++ {
		valueBytes, err := reader.read(8)
		if err != nil {
			t.Fatal(err)
		}
		values = append(values, int64(binary.LittleEndian.Uint64(valueBytes)))
		scriptLen, err := reader.readVarInt()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := reader.read(int(scriptLen)); err != nil {
			t.Fatal(err)
		}
	}
	return values
}

type bitcoinTestReader []byte

func (r *bitcoinTestReader) read(n int) ([]byte, error) {
	if n < 0 || len(*r) < n {
		return nil, io.ErrUnexpectedEOF
	}
	out := (*r)[:n]
	*r = (*r)[n:]
	return out, nil
}

func (r *bitcoinTestReader) peek() (byte, error) {
	if len(*r) == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	return (*r)[0], nil
}

func (r *bitcoinTestReader) readVarInt() (uint64, error) {
	prefix, err := r.read(1)
	if err != nil {
		return 0, err
	}
	switch prefix[0] {
	case 0xfd:
		value, err := r.read(2)
		if err != nil {
			return 0, err
		}
		return uint64(binary.LittleEndian.Uint16(value)), nil
	case 0xfe:
		value, err := r.read(4)
		if err != nil {
			return 0, err
		}
		return uint64(binary.LittleEndian.Uint32(value)), nil
	case 0xff:
		value, err := r.read(8)
		if err != nil {
			return 0, err
		}
		return binary.LittleEndian.Uint64(value), nil
	default:
		return uint64(prefix[0]), nil
	}
}
