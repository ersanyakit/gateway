package bitcoin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"core/asset"
	chainpkg "core/blockchain/chains"
	"core/constants"
	"core/models"
	"core/workers/dispatcher"
	listenerconfig "core/workers/listeners"
)

func TestIsBitcoinTxPageEOF(t *testing.T) {
	err := &bitcoinAPIError{statusCode: http.StatusNotFound, body: "start index out of range"}
	if !isBitcoinTxPageEOF(err) {
		t.Fatal("start-index 404 should be treated as end of block tx pages")
	}
}

func TestIsBitcoinTxPageEOFRejectsOtherErrors(t *testing.T) {
	cases := []error{
		&bitcoinAPIError{statusCode: http.StatusNotFound, body: "block not found"},
		&bitcoinAPIError{statusCode: http.StatusInternalServerError, body: "start index out of range"},
		errors.New("start index out of range"),
	}

	for _, err := range cases {
		if isBitcoinTxPageEOF(err) {
			t.Fatalf("isBitcoinTxPageEOF(%v) = true, want false", err)
		}
	}
}

func TestBitcoinTxMemoFromOpReturnASM(t *testing.T) {
	tx := Tx{Vout: []Vout{{
		ScriptPubKeyType: "op_return",
		ScriptPubKeyASM:  "OP_RETURN 4f524445522d3432",
	}}}

	got := bitcoinTxMemo(tx)
	if got != "ORDER-42" {
		t.Fatalf("memo = %q, want ORDER-42", got)
	}
}

func TestBitcoinTxMemoFromOpReturnScript(t *testing.T) {
	tx := Tx{Vout: []Vout{{
		ScriptPubKeyType: "op_return",
		ScriptPubKey:     "6a07494e562d393030",
	}}}

	got := bitcoinTxMemo(tx)
	if got != "INV-900" {
		t.Fatalf("memo = %q, want INV-900", got)
	}
}

func TestBitcoinValueToSats(t *testing.T) {
	cases := map[string]int64{
		"0":          0,
		"0.00000001": 1,
		"1.23456789": 123456789,
		"21":         2100000000,
	}
	for raw, want := range cases {
		got, err := bitcoinValueToSats(json.Number(raw))
		if err != nil {
			t.Fatalf("bitcoinValueToSats(%q) error = %v", raw, err)
		}
		if got != want {
			t.Fatalf("bitcoinValueToSats(%q) = %d, want %d", raw, got, want)
		}
	}

	if _, err := bitcoinValueToSats(json.Number("0.000000001")); err == nil {
		t.Fatal("bitcoinValueToSats accepted sub-satoshi precision")
	}
}

func TestBitcoinEndpointFromURLClassifiesChainRPCs(t *testing.T) {
	core, ok := bitcoinEndpointFromURL("http://alice:secret@127.0.0.1:8332")
	if !ok {
		t.Fatal("core endpoint was not parsed")
	}
	if core.Kind != bitcoinEndpointCore || core.URL != "http://127.0.0.1:8332" || core.Username != "alice" || core.Password != "secret" {
		t.Fatalf("core endpoint = %#v", core)
	}

	unisat, ok := bitcoinEndpointFromURL("https://token@open-api.unisat.io")
	if !ok {
		t.Fatal("unisat endpoint was not parsed")
	}
	if unisat.Kind != bitcoinEndpointUniSat || unisat.URL != "https://open-api.unisat.io" || unisat.BearerToken != "token" {
		t.Fatalf("unisat endpoint = %#v", unisat)
	}

	esplora, ok := bitcoinEndpointFromURL("https://mempool.space/api")
	if !ok {
		t.Fatal("esplora endpoint was not parsed")
	}
	if esplora.Kind != bitcoinEndpointEsplora {
		t.Fatalf("esplora kind = %q", esplora.Kind)
	}
}

func TestBitcoinCoreBlockConvertsTransactions(t *testing.T) {
	server := newBitcoinCoreTestServer(t)
	defer server.Close()

	listener := &RpcListener{
		client: server.Client(),
	}
	endpoint, _ := bitcoinEndpointFromURL(bitcoinCoreTestURL(server.URL))

	blockHash, parentHash, txs, err := listener.coreBlock(context.Background(), endpoint, 100)
	if err != nil {
		t.Fatal(err)
	}
	if blockHash != "0000000000000000000000000000000000000000000000000000000000000100" {
		t.Fatalf("blockHash = %q", blockHash)
	}
	if parentHash != "0000000000000000000000000000000000000000000000000000000000000099" {
		t.Fatalf("parentHash = %q", parentHash)
	}
	if len(txs) != 1 {
		t.Fatalf("txs len = %d, want 1", len(txs))
	}
	tx := txs[0]
	if tx.TxID != "core-tx-1" || !tx.Status.Confirmed || tx.Status.BlockHeight != 100 {
		t.Fatalf("tx status = %#v", tx)
	}
	if got := tx.Vin[0].Prevout.Value; got != 50000000 {
		t.Fatalf("vin prevout value = %d, want 50000000", got)
	}
	if got := tx.Vout[0].Value; got != 123456789 {
		t.Fatalf("vout value = %d, want 123456789", got)
	}
}

func TestProcessBlockUsesBitcoinCoreRPC(t *testing.T) {
	server := newBitcoinCoreTestServer(t)
	defer server.Close()

	registry := asset.NewRegistry()
	registry.Register(asset.NewBTC())
	chain := chainpkg.NewBitcoinChain()
	chain.RPCHttp = []string{bitcoinCoreTestURL(server.URL)}
	listener := &RpcListener{
		chain:    chain,
		registry: registry,
		client:   server.Client(),
		events:   make(chan interface{}, 2),
	}

	if err := listener.processBlock(100); err != nil {
		t.Fatal(err)
	}

	raw := <-listener.events
	event, ok := raw.(dispatcher.Event)
	if !ok {
		t.Fatalf("event type = %T, want dispatcher.Event", raw)
	}
	if event.Type != "utxo_transfer" {
		t.Fatalf("event type = %q, want utxo_transfer", event.Type)
	}
	if event.Transaction == nil {
		t.Fatal("event transaction is nil")
	}
	if got := *event.Transaction.Hash; got != "core-tx-1" {
		t.Fatalf("hash = %q", got)
	}
	if got := *event.Transaction.From; got != "bc1qsource" {
		t.Fatalf("from = %q", got)
	}
	if got := *event.Transaction.To; got != "bc1qrecipient" {
		t.Fatalf("to = %q", got)
	}
	if got := *event.Transaction.Amount; got != "123456789" {
		t.Fatalf("amount = %q", got)
	}
	if listener.lastBlockHash != "0000000000000000000000000000000000000000000000000000000000000100" ||
		listener.lastBlockParentHash != "0000000000000000000000000000000000000000000000000000000000000099" {
		t.Fatalf("last block checkpoint hash = %q/%q", listener.lastBlockHash, listener.lastBlockParentHash)
	}
}

func TestHandleTxPreservesEveryBitcoinInputAddress(t *testing.T) {
	chain := chainpkg.NewBitcoinChain()
	listener := &RpcListener{chain: chain, events: make(chan interface{}, 1)}
	tx := Tx{
		TxID: "multi-input",
		Vin: []Vin{
			{Prevout: Prevout{Address: "bc1external"}},
			{Prevout: Prevout{Address: "bc1platform"}},
		},
		Vout: []Vout{{Address: "bc1destination", Value: 100}},
	}
	tx.Status.Confirmed = true

	if err := listener.handleTx(100, "block-hash", asset.NewBTC(), tx); err != nil {
		t.Fatal(err)
	}
	event := (<-listener.events).(dispatcher.Event)
	got := event.Transaction.FromAddresses
	if len(got) != 2 || got[0] != "bc1external" || got[1] != "bc1platform" {
		t.Fatalf("from addresses = %#v", got)
	}
}

func TestProcessBlockRewindsBitcoinCheckpointOnParentMismatch(t *testing.T) {
	server := newBitcoinCoreTestServer(t)
	defer server.Close()

	registry := asset.NewRegistry()
	registry.Register(asset.NewBTC())
	chain := chainpkg.NewBitcoinChain()
	chain.RPCHttp = []string{bitcoinCoreTestURL(server.URL)}
	state := &models.ChainState{
		ChainID:            constants.Bitcoin,
		LastProcessedBlock: 99,
		LastProcessedHash:  "different-parent",
	}
	var wroteRollback bool
	listener := &RpcListener{
		chain:       chain,
		registry:    registry,
		chainState:  state,
		stateWriter: func(*models.ChainState) error { wroteRollback = true; return nil },
		client:      server.Client(),
		events:      make(chan interface{}, 1),
	}

	err := listener.processBlock(100)
	if !errors.Is(err, listenerconfig.ErrParentContinuity) {
		t.Fatalf("processBlock err = %v, want ErrParentContinuity", err)
	}
	if !wroteRollback {
		t.Fatal("rollback state was not persisted")
	}
	if state.LastProcessedBlock != 98 || state.LastProcessedHash != "" || state.LastProcessedParentHash != "" {
		t.Fatalf("state after rewind = %#v", state)
	}
	select {
	case event := <-listener.events:
		t.Fatalf("unexpected event on parent mismatch: %#v", event)
	default:
	}
}

func TestUniSatBlockConvertsTransactions(t *testing.T) {
	server := newUniSatBitcoinTestServer(t)
	defer server.Close()

	listener := &RpcListener{client: server.Client()}
	endpoint := bitcoinEndpoint{URL: server.URL, Key: server.URL, Kind: bitcoinEndpointUniSat}

	blockHash, parentHash, txs, err := listener.unisatBlock(context.Background(), endpoint, 100)
	if err != nil {
		t.Fatal(err)
	}
	if blockHash != "0000000000000000000000000000000000000000000000000000000000000100" {
		t.Fatalf("blockHash = %q", blockHash)
	}
	if parentHash != "0000000000000000000000000000000000000000000000000000000000000099" {
		t.Fatalf("parentHash = %q", parentHash)
	}
	if len(txs) != 1 {
		t.Fatalf("tx count = %d, want 1", len(txs))
	}
	if txs[0].TxID != "unisat-tx-1" || txs[0].Vin[0].Prevout.Address != "bc1qsource" || txs[0].Vout[0].Address != "bc1qrecipient" {
		t.Fatalf("tx = %#v", txs[0])
	}
	if txs[0].Vin[0].Prevout.Value != 50000000 || txs[0].Vout[0].Value != 12345 {
		t.Fatalf("tx values = %#v/%#v", txs[0].Vin[0].Prevout, txs[0].Vout[0])
	}
}

func bitcoinCoreTestURL(raw string) string {
	return strings.Replace(raw, "http://", "http://alice:secret@", 1)
}

func newBitcoinCoreTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	const blockHash = "0000000000000000000000000000000000000000000000000000000000000100"

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req bitcoinCoreRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "getblockhash":
			_ = json.NewEncoder(w).Encode(map[string]any{"result": blockHash, "error": nil})
		case "getblock":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{
					"hash":              blockHash,
					"previousblockhash": "0000000000000000000000000000000000000000000000000000000000000099",
					"height":            100,
					"tx": []map[string]any{{
						"txid": "core-tx-1",
						"vin": []map[string]any{{
							"txid": "prev-tx",
							"vout": 0,
							"prevout": map[string]any{
								"value": 0.5,
								"scriptPubKey": map[string]any{
									"type":    "witness_v0_keyhash",
									"address": "bc1qsource",
								},
							},
						}},
						"vout": []map[string]any{{
							"n":     0,
							"value": 1.23456789,
							"scriptPubKey": map[string]any{
								"asm":     "0 abcd",
								"hex":     "0014abcd",
								"type":    "witness_v0_keyhash",
								"address": "bc1qrecipient",
							},
						}},
					}},
				},
				"error": nil,
			})
		default:
			t.Fatalf("unexpected bitcoin core method %q", req.Method)
		}
	}))
}

func newUniSatBitcoinTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	const blockHash = "0000000000000000000000000000000000000000000000000000000000000100"
	const parentHash = "0000000000000000000000000000000000000000000000000000000000000099"

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/indexer/height/100/block":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"hash":              blockHash,
					"previousBlockHash": parentHash,
					"height":            100,
				},
			})
		case "/v1/indexer/block/100/txs":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"total": 1,
					"list": []map[string]any{{
						"txid": "unisat-tx-1",
						"inputs": []map[string]any{{
							"address": "bc1qsource",
							"satoshi": 50000000,
						}},
						"outputs": []map[string]any{{
							"address":  "bc1qrecipient",
							"satoshi":  12345,
							"scriptPk": "0014abcd",
						}},
					}},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}
