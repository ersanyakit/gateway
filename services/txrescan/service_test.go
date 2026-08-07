package txrescan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	configurations "core/application/configuration"
	"core/asset"
	chainpkg "core/blockchain/chains"
	"core/constants"
	"core/models"
	"core/repositories"
	depositsvc "core/services/deposits"
	"core/types"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

func TestBitcoinGetAnnotatesEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("bad gateway"))
	}))
	defer server.Close()

	svc := &Service{client: server.Client()}
	chain := chainpkg.NewBitcoinChain()
	chain.RPCHttp = []string{server.URL}

	_, err := svc.bitcoinGet(context.Background(), chain, "/tx/abc")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), server.URL) {
		t.Fatalf("error %q does not include endpoint %q", err.Error(), server.URL)
	}
}

func TestSolanaRPCAnnotatesEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("bad gateway"))
	}))
	defer server.Close()

	svc := &Service{client: server.Client()}
	chain := chainpkg.NewSolanaChain()
	chain.RPCHttp = []string{server.URL}

	var out any
	err := svc.solanaRPC(context.Background(), chain, "getTransaction", []any{"abc"}, &out)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), server.URL) {
		t.Fatalf("error %q does not include endpoint %q", err.Error(), server.URL)
	}
}

func TestJSONRPCFailoverSkipsNullAndMismatchedResponses(t *testing.T) {
	requestCount := 0
	serverA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		var req struct {
			ID int64 `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode first request: %v", err)
		}
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":null}`, req.ID)
	}))
	defer serverA.Close()
	serverB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		var req struct {
			ID int64 `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode second request: %v", err)
		}
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":"0x2a"}`, req.ID)
	}))
	defer serverB.Close()

	chain := chainpkg.NewChilizChain()
	chain.RPCHttp = []string{serverA.URL, serverB.URL}
	svc := &Service{client: serverA.Client()}
	var out string
	if err := svc.evmRPC(context.Background(), chain, "eth_blockNumber", nil, &out); err != nil {
		t.Fatal(err)
	}
	if out != "0x2a" || requestCount != 2 {
		t.Fatalf("result/count = %q/%d, want 0x2a/2", out, requestCount)
	}
}

func TestJSONRPCRejectsResponseIDMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":"0x2a"}`, req.ID+1)
	}))
	defer server.Close()

	chain := chainpkg.NewChilizChain()
	chain.RPCHttp = []string{server.URL}
	svc := &Service{client: server.Client()}
	var out string
	err := svc.evmRPC(context.Background(), chain, "eth_blockNumber", nil, &out)
	if !errors.Is(err, ErrIncompleteProviderResponse) {
		t.Fatalf("error = %v, want incomplete provider response", err)
	}
}

func TestTronPostAnnotatesEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("bad gateway"))
	}))
	defer server.Close()

	t.Setenv("TRON_HTTP_ENDPOINTS", server.URL)

	svc := &Service{client: server.Client()}
	_, err := svc.tronPost(context.Background(), chainpkg.NewTronChain(), "/wallet/gettransactionbyid", map[string]string{"value": "abc"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), server.URL) {
		t.Fatalf("error %q does not include endpoint %q", err.Error(), server.URL)
	}
}

func TestTronPostFailsOverEmptyProviderResponse(t *testing.T) {
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer empty.Close()
	complete := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"txID":"abc"}`))
	}))
	defer complete.Close()
	t.Setenv("TRON_HTTP_ENDPOINTS", empty.URL+","+complete.URL)
	body, err := (&Service{client: complete.Client()}).tronPost(context.Background(), chainpkg.NewTronChain(), "/wallet/gettransactionbyid", map[string]string{"value": "abc"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"txID":"abc"`) {
		t.Fatalf("body = %s, want complete second-provider response", body)
	}
}

func TestTronHTTPEndpointsForChainNormalizesJSONRPCFallback(t *testing.T) {
	t.Setenv("TRON_TESTNET_HTTP_ENDPOINTS", "")
	t.Setenv("TRON_TESTNET_HTTP_ENDPOINT", "")

	got := tronHTTPEndpointsForChain("tron-testnet", []string{"https://shasta.example/jsonrpc", "https://shasta.example/jsonrpc"})
	if len(got) != 1 {
		t.Fatalf("endpoints = %#v, want one normalized endpoint", got)
	}
	if got[0] != "https://shasta.example" {
		t.Fatalf("endpoint = %q, want normalized full node base", got[0])
	}
}

func TestRescanMemoExtractionHelpers(t *testing.T) {
	btc := btcTx{Vout: []btcVout{{
		ScriptPubKeyType: "op_return",
		ScriptPubKeyASM:  "OP_RETURN 4f524445522d3432",
	}}}
	if got := bitcoinTxMemo(btc); got != "ORDER-42" {
		t.Fatalf("bitcoin memo = %q, want ORDER-42", got)
	}

	solanaMemoRaw, err := json.Marshal("ORDER-42")
	if err != nil {
		t.Fatal(err)
	}
	if got := solanaTransactionMemo([]solanaInstruction{{
		Program:   "spl-memo",
		ProgramID: solanaMemoProgram,
		Parsed:    solanaMemoRaw,
	}}, nil); got != "ORDER-42" {
		t.Fatalf("solana memo = %q, want ORDER-42", got)
	}

	if got := tronRawDataMemo(fmt.Sprintf("%x", "ORDER-42")); got != "ORDER-42" {
		t.Fatalf("tron memo = %q, want ORDER-42", got)
	}
}

func TestSolanaInstructionEventHandlesZeroAmountTransfer(t *testing.T) {
	svc := &Service{Registry: asset.NewRegistry()}
	native := asset.NewSOL(constants.Solana)

	cases := []struct {
		name        string
		instruction solanaInstruction
		wantError   bool
	}{
		{
			name: "system transfer",
			instruction: solanaInstruction{
				Program: "system",
				Parsed:  json.RawMessage(`{"type":"transfer","info":{"source":"source","destination":"destination","lamports":"0"}}`),
			},
		},
		{
			name:      "spl transfer",
			wantError: true,
			instruction: solanaInstruction{
				Program: "spl-token",
				Parsed:  json.RawMessage(`{"type":"transfer","info":{"source":"source","destination":"destination","amount":"0","mint":"mint"}}`),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events, err := svc.solanaInstructionEventValidated(context.Background(), constants.Solana, "1", "block-hash", "tx", "ix:0", native, "confirmed", "signer", "", tc.instruction, nil)
			if tc.wantError && !errors.Is(err, ErrIncompleteProviderResponse) {
				t.Fatalf("error = %v, want fail-closed rejection", err)
			}
			if !tc.wantError && err != nil {
				t.Fatalf("error = %v, want validation-only native skip", err)
			}
			if len(events) != 0 {
				t.Fatalf("events = %#v, want no zero-amount transfer", events)
			}
		})
	}
}

func TestSolanaInstructionEventResolvesOrdinaryTransferMintAndOwners(t *testing.T) {
	const mint = "OrdinaryTransferMint111111111111111111111111"
	registry := asset.NewRegistry()
	registry.Register(asset.NewSPL(constants.Solana, mint, "USDT", "Tether", 6))
	svc := &Service{Registry: registry}

	events, err := svc.solanaInstructionEventValidated(
		context.Background(),
		constants.Solana,
		"1",
		"block-hash",
		"tx",
		"ix:0",
		asset.NewSOL(constants.Solana),
		"confirmed",
		"signer",
		"",
		solanaInstruction{
			Program: "spl-token",
			Parsed: json.RawMessage(`{
				"type":"transfer",
				"info":{
					"source":"source-token-account",
					"destination":"destination-token-account",
					"amount":"75"
				}
			}`),
		},
		map[string]solanaTokenAccountMetadata{
			"source-token-account": {
				Owner: "source-owner", Mint: mint, Decimals: 6, HasDecimals: true,
			},
			"destination-token-account": {
				Owner: "destination-owner", Mint: mint, Decimals: 6, HasDecimals: true,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Tx == nil {
		t.Fatalf("events = %#v, want one SPL transfer", events)
	}
	tx := events[0].Tx
	if tx.Token == nil || *tx.Token != mint || tx.Symbol == nil || *tx.Symbol != "USDT" || tx.Decimals != 6 {
		t.Fatalf("asset metadata = %#v", tx)
	}
	if tx.From == nil || *tx.From != "source-owner" || tx.To == nil || *tx.To != "destination-owner" {
		t.Fatalf("owner mapping = %#v", tx)
	}
}

func TestSolanaInstructionEventFailsClosedOnMissingOwnerOrMintConflict(t *testing.T) {
	const mint = "StrictMetadataMint1111111111111111111111111111"
	registry := asset.NewRegistry()
	registry.Register(asset.NewSPL(constants.Solana, mint, "USDC", "USD Coin", 6))
	svc := &Service{Registry: registry}
	instruction := solanaInstruction{
		Program: "spl-token",
		Parsed: json.RawMessage(`{
			"type":"transferChecked",
			"info":{
				"source":"source-token-account",
				"destination":"destination-token-account",
				"mint":"StrictMetadataMint1111111111111111111111111111",
				"tokenAmount":{"amount":"75","decimals":6}
			}
		}`),
	}

	tests := []struct {
		name     string
		accounts map[string]solanaTokenAccountMetadata
	}{
		{
			name: "missing destination owner",
			accounts: map[string]solanaTokenAccountMetadata{
				"source-token-account": {
					Owner: "source-owner", Mint: mint, Decimals: 6, HasDecimals: true,
				},
			},
		},
		{
			name: "source destination mint conflict",
			accounts: map[string]solanaTokenAccountMetadata{
				"source-token-account": {
					Owner: "source-owner", Mint: "different-mint", Decimals: 6, HasDecimals: true,
				},
				"destination-token-account": {
					Owner: "destination-owner", Mint: mint, Decimals: 6, HasDecimals: true,
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events, err := svc.solanaInstructionEventValidated(
				context.Background(),
				constants.Solana,
				"1",
				"block-hash",
				"tx",
				"ix:0",
				asset.NewSOL(constants.Solana),
				"confirmed",
				"signer",
				"",
				instruction,
				test.accounts,
			)
			if !errors.Is(err, ErrIncompleteProviderResponse) || len(events) != 0 {
				t.Fatalf("events/error = %#v/%v, want fail-closed rejection", events, err)
			}
		})
	}
}

func TestSolanaTokenAccountMetadataByAddressMergesPreAndPostStrictly(t *testing.T) {
	rawKeys := []json.RawMessage{
		json.RawMessage(`{"pubkey":"source-token-account","signer":false}`),
		json.RawMessage(`{"pubkey":"destination-token-account","signer":false}`),
	}
	meta := solanaTxMeta{
		PreTokenBalances: []solanaTokenBalance{{
			AccountIndex: 0,
			Mint:         "mint",
			Owner:        "source-owner",
			ProgramID:    "token-program",
			UITokenAmount: &solanaUITokenAmount{
				Amount: "100", Decimals: 6,
			},
		}},
		PostTokenBalances: []solanaTokenBalance{{
			AccountIndex: 1,
			Mint:         "mint",
			Owner:        "destination-owner",
			ProgramID:    "token-program",
			UITokenAmount: &solanaUITokenAmount{
				Amount: "100", Decimals: 6,
			},
		}},
	}

	accounts, warnings := solanaTokenAccountMetadataByAddress(rawKeys, meta)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v", warnings)
	}
	if got := accounts["source-token-account"]; got.Owner != "source-owner" || got.Mint != "mint" || got.Decimals != 6 {
		t.Fatalf("source metadata = %#v", got)
	}
	if got := accounts["destination-token-account"]; got.Owner != "destination-owner" || got.Mint != "mint" || got.Decimals != 6 {
		t.Fatalf("destination metadata = %#v", got)
	}

	meta.PostTokenBalances = append(meta.PostTokenBalances, solanaTokenBalance{
		AccountIndex: 0,
		Mint:         "different-mint",
		Owner:        "source-owner",
		UITokenAmount: &solanaUITokenAmount{
			Amount: "100", Decimals: 6,
		},
	})
	accounts, warnings = solanaTokenAccountMetadataByAddress(rawKeys, meta)
	if _, exists := accounts["source-token-account"]; exists {
		t.Fatal("conflicting source metadata must be removed")
	}
	if len(warnings) == 0 {
		t.Fatal("conflicting metadata should produce a warning for logging")
	}
}

func TestScanSolanaValidatesIdentityAndCarriesFinality(t *testing.T) {
	const signature = "solana-signature"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		switch req.Method {
		case "getTransaction":
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{
				"slot":321,"meta":{"err":null,"innerInstructions":[],"preBalances":[100,0],"postBalances":[53,42],"preTokenBalances":[],"postTokenBalances":[]},
				"transaction":{"signatures":[%q],"message":{"accountKeys":[{"pubkey":"source","signer":true},{"pubkey":"destination","signer":false}],"instructions":[{"program":"system","programId":"11111111111111111111111111111111","parsed":{"type":"transfer","info":{"source":"source","destination":"destination","lamports":42}}}]}}
			}}`, req.ID, signature)
		case "getBlock":
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"blockhash":"solana-block-hash","signatures":[%q]}}`, req.ID, signature)
		default:
			t.Fatalf("unexpected Solana method %q", req.Method)
		}
	}))
	defer server.Close()
	chain := chainpkg.NewSolanaChain()
	chain.RPCHttp = []string{server.URL}
	registry := asset.NewRegistry()
	registry.Register(asset.NewSOL(constants.Solana))
	svc := &Service{
		Registry: registry,
		client:   server.Client(),
		Confirmations: func(constants.ChainID) uint {
			return 7
		},
	}
	events, err := svc.scanSolana(context.Background(), chain, signature)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Tx == nil {
		t.Fatalf("events = %#v, want one transfer", events)
	}
	if events[0].Confirmations != 7 || events[0].ConfirmationsRequired != 7 {
		t.Fatalf("confirmations = %d/%d, want 7/7", events[0].Confirmations, events[0].ConfirmationsRequired)
	}
	if *events[0].Tx.Hash != signature || *events[0].Tx.Block != "321" || *events[0].Tx.BlockHash != "solana-block-hash" {
		t.Fatalf("unexpected Solana identity: %#v", events[0].Tx)
	}
	if events[0].Tx.Amount == nil || *events[0].Tx.Amount != "42" ||
		events[0].Tx.LogIndex == nil || *events[0].Tx.LogIndex != "balance:1" {
		t.Fatalf("unexpected authoritative Solana balance event: %#v", events[0].Tx)
	}
}

func TestScanSolanaUsesLoadedBalanceDeltaWithoutParsedDoubleCredit(t *testing.T) {
	const signature = "solana-loaded-signature"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		switch req.Method {
		case "getTransaction":
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{
				"slot":654,"meta":{
					"err":null,
					"innerInstructions":[],
					"preBalances":[1000,10],
					"postBalances":[870,130],
					"loadedAddresses":{"writable":["loaded-merchant"],"readonly":[]},
					"preTokenBalances":[],"postTokenBalances":[]
				},
				"transaction":{"signatures":[%q],"message":{
					"accountKeys":["static-signer"],
					"instructions":[
						{"program":"system","programId":"11111111111111111111111111111111","parsed":{"type":"transfer","info":{"source":"static-signer","destination":"loaded-merchant","lamports":25}}},
						{"program":"system","programId":"11111111111111111111111111111111","parsed":{"type":"transferWithSeed","info":{"source":"static-signer","sourceBase":"static-signer","destination":"loaded-merchant","lamports":95}}}
					]
				}}
			}}`, req.ID, signature)
		case "getBlock":
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"blockhash":"loaded-block-hash","signatures":[%q]}}`, req.ID, signature)
		default:
			t.Fatalf("unexpected Solana method %q", req.Method)
		}
	}))
	defer server.Close()

	chain := chainpkg.NewSolanaChain()
	chain.RPCHttp = []string{server.URL}
	registry := asset.NewRegistry()
	registry.Register(asset.NewSOL(constants.Solana))
	events, err := (&Service{Registry: registry, client: server.Client()}).scanSolana(context.Background(), chain, signature)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Tx == nil {
		t.Fatalf("events = %#v, want one authoritative native receipt", events)
	}
	tx := events[0].Tx
	if tx.From == nil || *tx.From != "static-signer" ||
		len(tx.FromAddresses) != 1 || tx.FromAddresses[0] != "static-signer" ||
		tx.To == nil || *tx.To != "loaded-merchant" ||
		tx.Amount == nil || *tx.Amount != "120" ||
		tx.LogIndex == nil || *tx.LogIndex != "balance:1" {
		t.Fatalf("loaded balance event = %#v", tx)
	}

	replayed, err := (&Service{Registry: registry, client: server.Client()}).scanSolana(context.Background(), chain, signature)
	if err != nil {
		t.Fatal(err)
	}
	if len(replayed) != 1 || replayed[0].Tx == nil || replayed[0].Tx.LogIndex == nil || *replayed[0].Tx.LogIndex != "balance:1" {
		t.Fatalf("replayed identity = %#v, want balance:1", replayed)
	}
}

func TestSolanaTransactionAccountKeysAcceptsParsedLoadedTailAndRejectsMismatch(t *testing.T) {
	rawKeys := []json.RawMessage{
		json.RawMessage(`{"pubkey":"static-signer","signer":true,"source":"transaction"}`),
		json.RawMessage(`{"pubkey":"loaded-write","signer":false,"source":"lookupTable"}`),
		json.RawMessage(`{"pubkey":"loaded-read","signer":false,"source":"lookupTable"}`),
	}
	loaded := solanaLoadedAddresses{Writable: []string{"loaded-write"}, Readonly: []string{"loaded-read"}}
	keys, err := solanaTransactionAccountKeys(rawKeys, loaded, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{keys[0].Pubkey, keys[1].Pubkey, keys[2].Pubkey}; !reflect.DeepEqual(got, []string{"static-signer", "loaded-write", "loaded-read"}) {
		t.Fatalf("account keys = %v", got)
	}

	loaded.Readonly[0] = "different-loaded-read"
	if _, err := solanaTransactionAccountKeys(rawKeys, loaded, 3); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("loaded address mismatch error = %v", err)
	}
}

func TestScanSolanaRejectsMissingOrMismatchedBalanceMetadata(t *testing.T) {
	const signature = "solana-invalid-balance"
	tests := []struct {
		name string
		meta string
	}{
		{
			name: "missing pre balances",
			meta: `{"err":null,"postBalances":[1],"innerInstructions":[],"preTokenBalances":[],"postTokenBalances":[]}`,
		},
		{
			name: "missing post balances",
			meta: `{"err":null,"preBalances":[1],"innerInstructions":[],"preTokenBalances":[],"postTokenBalances":[]}`,
		},
		{
			name: "pre post mismatch",
			meta: `{"err":null,"preBalances":[1],"postBalances":[1,2],"innerInstructions":[],"preTokenBalances":[],"postTokenBalances":[]}`,
		},
		{
			name: "account balance mismatch",
			meta: `{"err":null,"preBalances":[1,0],"postBalances":[0,1],"innerInstructions":[],"preTokenBalances":[],"postTokenBalances":[]}`,
		},
		{
			name: "loaded tail mismatch",
			meta: `{"err":null,"preBalances":[1],"postBalances":[1],"loadedAddresses":{"writable":["different-key"],"readonly":[]},"innerInstructions":[],"preTokenBalances":[],"postTokenBalances":[]}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					ID     int64  `json:"id"`
					Method string `json:"method"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Fatalf("decode request: %v", err)
				}
				if req.Method != "getTransaction" {
					t.Fatalf("canonical block lookup must not run after invalid transaction metadata: %s", req.Method)
				}
				_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"slot":321,"meta":%s,"transaction":{"signatures":[%q],"message":{"accountKeys":["only-key"],"instructions":[]}}}}`, req.ID, tc.meta, signature)
			}))
			defer server.Close()

			chain := chainpkg.NewSolanaChain()
			chain.RPCHttp = []string{server.URL}
			registry := asset.NewRegistry()
			registry.Register(asset.NewSOL(constants.Solana))
			_, err := (&Service{Registry: registry, client: server.Client()}).scanSolana(context.Background(), chain, signature)
			if !errors.Is(err, ErrIncompleteProviderResponse) {
				t.Fatalf("error = %v, want incomplete provider response", err)
			}
		})
	}
}

func TestSolanaNativeBalanceEventsSkipsFailedTransaction(t *testing.T) {
	events, err := solanaNativeBalanceEvents(
		context.Background(),
		constants.Solana,
		"1",
		"block-hash",
		"failed-signature",
		asset.NewSOL(constants.Solana),
		"failed",
		"source",
		"",
		[]solanaAccountKey{{Pubkey: "source", Signer: true}, {Pubkey: "merchant"}},
		[]uint64{100, 0},
		[]uint64{90, 10},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("failed transaction events = %#v, want none", events)
	}
}

func TestScanSolanaRejectsNullMetadata(t *testing.T) {
	const signature = "solana-signature"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID int64 `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"slot":321,"blockhash":"block","meta":null,"transaction":{"signatures":[%q],"message":{"accountKeys":["source"],"instructions":[]}}}}`, req.ID, signature)
	}))
	defer server.Close()
	chain := chainpkg.NewSolanaChain()
	chain.RPCHttp = []string{server.URL}
	registry := asset.NewRegistry()
	registry.Register(asset.NewSOL(constants.Solana))
	_, err := (&Service{Registry: registry, client: server.Client()}).scanSolana(context.Background(), chain, signature)
	if !errors.Is(err, ErrIncompleteProviderResponse) {
		t.Fatalf("error = %v, want incomplete provider response", err)
	}
}

func TestScanEVMCarriesConfirmationMetadata(t *testing.T) {
	const txHash = "0xabc"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var req struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		var result string
		switch req.Method {
		case "eth_getTransactionByHash":
			result = `{"hash":"` + txHash + `","from":"0x0000000000000000000000000000000000000001","to":"0x0000000000000000000000000000000000000002","value":"0xde0b6b3a7640000","input":"0x","blockNumber":"0x64","blockHash":"0xblock","transactionIndex":"0x0"}`
		case "eth_getTransactionReceipt":
			result = `{"transactionHash":"` + txHash + `","transactionIndex":"0x0","blockNumber":"0x64","blockHash":"0xblock","status":"0x1","gasUsed":"0x5208","effectiveGasPrice":"0x1","logs":[]}`
		case "eth_getBlockByNumber":
			result = `{"number":"0x64","hash":"0xblock","parentHash":"0xparent","transactions":["` + txHash + `"]}`
		case "eth_blockNumber":
			result = `"0x6f"`
		case "trace_transaction":
			result = `[{"type":"call","action":{"callType":"call","from":"0x0000000000000000000000000000000000000001","to":"0x0000000000000000000000000000000000000002","value":"0xde0b6b3a7640000"},"transactionHash":"` + txHash + `","traceAddress":[]}]`
		default:
			t.Fatalf("unexpected method: %s", req.Method)
		}
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, req.ID, result)
	}))
	defer server.Close()

	chain := chainpkg.NewChilizChain()
	chain.RPCHttp = []string{server.URL}
	svc := &Service{
		Registry: configurations.NewAssetRegistry(),
		client:   server.Client(),
		Confirmations: func(chainID constants.ChainID) uint {
			if chainID != constants.Chiliz {
				t.Fatalf("chainID = %d, want Chiliz", chainID)
			}
			return 12
		},
	}

	events, err := svc.scanEVM(context.Background(), chain, txHash)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].Confirmations != 12 || events[0].ConfirmationsRequired != 12 {
		t.Fatalf("confirmations = %d/%d, want 12/12", events[0].Confirmations, events[0].ConfirmationsRequired)
	}
	if events[0].Tx == nil || *events[0].Tx.Symbol != "CHZ" || *events[0].Tx.Amount != "1000000000000000000" {
		t.Fatalf("unexpected tx event: %#v", events[0].Tx)
	}
}

func TestScanEVMIncludesAttestedInternalNativeTransfer(t *testing.T) {
	const txHash = "0xabc"
	server := newEVMInternalRescanServer(t, txHash, false)
	defer server.Close()
	chain := chainpkg.NewChilizChain()
	chain.RPCHttp = []string{server.URL}
	events, err := (&Service{Registry: configurations.NewAssetRegistry(), client: server.Client()}).scanEVM(context.Background(), chain, txHash)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want root and internal transfer", len(events))
	}
	internal := events[1]
	if internal.Type != "internal_transfer" || internal.Tx == nil || *internal.Tx.LogIndex != "internal:0" || *internal.Tx.Amount != "2" ||
		*internal.Tx.From != "0x0000000000000000000000000000000000000002" || *internal.Tx.To != "0x0000000000000000000000000000000000000003" ||
		internal.Tx.ParentHash == nil || *internal.Tx.ParentHash != "0xparent" {
		t.Fatalf("internal event = %#v", internal)
	}
}

func TestScanEVMFailsClosedWhenInternalTraceSourceUnavailable(t *testing.T) {
	const txHash = "0xabc"
	server := newEVMInternalRescanServer(t, txHash, true)
	defer server.Close()
	chain := chainpkg.NewChilizChain()
	chain.RPCHttp = []string{server.URL}
	_, err := (&Service{Registry: configurations.NewAssetRegistry(), client: server.Client()}).scanEVM(context.Background(), chain, txHash)
	if !errors.Is(err, ErrIncompleteProviderResponse) {
		t.Fatalf("error = %v, want incomplete provider response", err)
	}
}

func newEVMInternalRescanServer(t *testing.T, txHash string, traceUnavailable bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode EVM request: %v", err)
			return
		}
		var result string
		switch req.Method {
		case "eth_getTransactionByHash":
			result = `{"hash":"` + txHash + `","from":"0x0000000000000000000000000000000000000001","to":"0x0000000000000000000000000000000000000002","value":"0x0","input":"0x1234","blockNumber":"0x64","blockHash":"0xblock","transactionIndex":"0x0"}`
		case "eth_getTransactionReceipt":
			result = `{"transactionHash":"` + txHash + `","transactionIndex":"0x0","blockNumber":"0x64","blockHash":"0xblock","status":"0x1","gasUsed":"0x5208","effectiveGasPrice":"0x1","logs":[]}`
		case "eth_getBlockByNumber":
			result = `{"number":"0x64","hash":"0xblock","parentHash":"0xparent","transactions":["` + txHash + `"]}`
		case "eth_blockNumber":
			result = `"0x64"`
		case "trace_transaction":
			if traceUnavailable {
				_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"error":{"code":-32601,"message":"method trace_transaction not found"}}`, req.ID)
				return
			}
			result = `[
				{"type":"call","action":{"callType":"call","from":"0x0000000000000000000000000000000000000001","to":"0x0000000000000000000000000000000000000002","value":"0x0"},"transactionHash":"` + txHash + `","traceAddress":[]},
				{"type":"call","action":{"callType":"call","from":"0x0000000000000000000000000000000000000002","to":"0x0000000000000000000000000000000000000003","value":"0x2"},"transactionHash":"` + txHash + `","traceAddress":[0]}
			]`
		default:
			t.Errorf("unexpected EVM method: %s", req.Method)
			return
		}
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, req.ID, result)
	}))
}

func TestScanEVMRejectsTransactionReceiptIdentityMismatch(t *testing.T) {
	const txHash = "0xabc"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		var result string
		switch req.Method {
		case "eth_getTransactionByHash":
			result = `{"hash":"0xabc","from":"0x1","to":"0x2","value":"0x1","input":"0x","blockNumber":"0x64","blockHash":"0xblock","transactionIndex":"0x0"}`
		case "eth_getTransactionReceipt":
			result = `{"transactionHash":"0xdifferent","transactionIndex":"0x0","blockNumber":"0x64","blockHash":"0xblock","status":"0x1","logs":[]}`
		default:
			t.Fatalf("unexpected method %s", req.Method)
		}
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, req.ID, result)
	}))
	defer server.Close()
	chain := chainpkg.NewChilizChain()
	chain.RPCHttp = []string{server.URL}
	_, err := (&Service{Registry: configurations.NewAssetRegistry(), client: server.Client()}).scanEVM(context.Background(), chain, txHash)
	if !errors.Is(err, ErrIncompleteProviderResponse) {
		t.Fatalf("error = %v, want incomplete provider response", err)
	}
}

func TestScanBitcoinFailsOverNotFoundAndCalculatesFinality(t *testing.T) {
	hash := strings.Repeat("a", 64)
	missing := httptest.NewServer(http.NotFoundHandler())
	defer missing.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tx/" + hash:
			_, _ = fmt.Fprintf(w, `{
				"txid":%q,
				"status":{"confirmed":true,"block_height":100,"block_hash":"block-100"},
				"vin":[{"is_coinbase":false,"prevout":{"scriptpubkey_address":"bc1-from"}}],
				"vout":[{"scriptpubkey_address":"bc1-to","value":2500}]
			}`, hash)
		case "/blocks/tip/height":
			_, _ = w.Write([]byte("102"))
		case "/block-height/100":
			_, _ = w.Write([]byte("block-100"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()

	chain := chainpkg.NewBitcoinChain()
	chain.RPCHttp = []string{missing.URL, provider.URL}
	registry := asset.NewRegistry()
	registry.Register(asset.NewBTC())
	svc := &Service{
		Registry: registry,
		client:   provider.Client(),
		Confirmations: func(constants.ChainID) uint {
			return 2
		},
	}
	events, err := svc.scanBitcoin(context.Background(), chain, hash)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].Confirmations != 3 || events[0].ConfirmationsRequired != 2 {
		t.Fatalf("confirmations = %d/%d, want 3/2", events[0].Confirmations, events[0].ConfirmationsRequired)
	}
	if events[0].Tx == nil || *events[0].Tx.BlockHash != "block-100" || *events[0].Tx.From != "bc1-from" {
		t.Fatalf("unexpected Bitcoin event: %#v", events[0].Tx)
	}
}

func TestScanBitcoinRejectsMissingPrevoutOwnership(t *testing.T) {
	hash := strings.Repeat("b", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tx/"+hash {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprintf(w, `{"txid":%q,"status":{"confirmed":true,"block_height":100,"block_hash":"block-100"},"vin":[{"is_coinbase":false,"prevout":null}],"vout":[{"scriptpubkey_address":"bc1-to","value":1}]}`, hash)
	}))
	defer server.Close()
	chain := chainpkg.NewBitcoinChain()
	chain.RPCHttp = []string{server.URL}
	registry := asset.NewRegistry()
	registry.Register(asset.NewBTC())
	_, err := (&Service{Registry: registry, client: server.Client()}).scanBitcoin(context.Background(), chain, hash)
	if !errors.Is(err, ErrIncompleteProviderResponse) {
		t.Fatalf("error = %v, want incomplete provider response", err)
	}
}

func TestRescanAppliesCandidateFinalityToExistingTransactions(t *testing.T) {
	source, err := os.ReadFile("service.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(source)
	for _, token := range []string{
		"applyTransactionFinality(ctx, candidate)",
		"func (s *Service) applyTransactionFinality",
		"s.TransactionRepo.MarkFinality(ctx, uniqueHash, candidate.Confirmations, required, finalized)",
		"s.TransactionRepo.MarkFailed(ctx, uniqueHash)",
		"errors.Is(err, gorm.ErrRecordNotFound)",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("txrescan finality integration missing %q", token)
		}
	}
}

func TestFilterMerchantEventsDropsOtherMerchantEventsInMixedTransaction(t *testing.T) {
	db := openTxRescanPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}); err != nil {
		t.Fatalf("automigrate wallet ownership tables: %v", err)
	}
	ctx := context.Background()
	merchantA := models.Merchant{ID: uuid.New(), Name: "merchant-a", Email: "a@example.com", Password: "x", IsActive: true}
	merchantB := models.Merchant{ID: uuid.New(), Name: "merchant-b", Email: "b@example.com", Password: "x", IsActive: true}
	if err := db.WithContext(ctx).Create(&[]models.Merchant{merchantA, merchantB}).Error; err != nil {
		t.Fatalf("seed merchants: %v", err)
	}
	domainA := models.Domain{ID: uuid.New(), MerchantID: merchantA.ID, DomainURL: "a.example.com", APIKey: "key-a", APISecret: "secret-a", HDAccountID: 1}
	domainB := models.Domain{ID: uuid.New(), MerchantID: merchantB.ID, DomainURL: "b.example.com", APIKey: "key-b", APISecret: "secret-b", HDAccountID: 2}
	if err := db.WithContext(ctx).Create(&[]models.Domain{domainA, domainB}).Error; err != nil {
		t.Fatalf("seed domains: %v", err)
	}
	addressA := "0x1111111111111111111111111111111111111111"
	addressB := "0x2222222222222222222222222222222222222222"
	walletA := models.Wallet{ID: uuid.New(), MerchantID: merchantA.ID, DomainID: domainA.ID, HDAccountID: 1, HDAddressId: 1, ProductID: "wallet-a", UserID: "user-a", EthereumAddress: addressA}
	walletB := models.Wallet{ID: uuid.New(), MerchantID: merchantB.ID, DomainID: domainB.ID, HDAccountID: 2, HDAddressId: 1, ProductID: "wallet-b", UserID: "user-b", EthereumAddress: addressB}
	for _, wallet := range []*models.Wallet{&walletA, &walletB} {
		suffix := strings.ReplaceAll(wallet.ID.String(), "-", "")
		wallet.BitcoinAddress = "btc-" + suffix
		wallet.AvalancheAddress = "avax-" + suffix
		wallet.BinanceAddress = "bnb-" + suffix
		wallet.BaseAddress = "base-" + suffix
		wallet.ArbitrumAddress = "arb-" + suffix
		wallet.UnichainAddress = "uni-" + suffix
		wallet.TronAddress = "tron-" + suffix
		wallet.SolanaAddress = "sol-" + suffix
		wallet.ChilizAddress = "chz-" + suffix
		wallet.ChilizSpicyAddress = "spicy-" + suffix
	}
	if err := db.WithContext(ctx).Create(&[]models.Wallet{walletA, walletB}).Error; err != nil {
		t.Fatalf("seed wallets: %v", err)
	}

	from := "0x9999999999999999999999999999999999999999"
	external := "0x3333333333333333333333333333333333333333"
	amount := "100"
	confirmed := models.TransactionStatusConfirmed
	pending := models.TransactionStatusPending
	failed := models.TransactionStatusFailed
	filtered, err := (&Service{
		WalletRepo: repositories.NewWalletRepo(repositories.NewDomainRepo(repositories.NewMerchantRepo(db, nil))),
	}).filterMerchantEvents(ctx, constants.Ethereum, []eventCandidate{
		{Type: "deposit", Tx: &types.TransactionParam{ChainID: constants.Ethereum, Hash: stringPtr("0xmixed"), From: &from, To: &addressA, Amount: &amount, Status: &confirmed}},
		{Type: "deposit", Tx: &types.TransactionParam{ChainID: constants.Ethereum, Hash: stringPtr("0xmixed"), From: &from, To: &addressB, Amount: &amount, Status: &confirmed}},
		{Type: "outgoing", Tx: &types.TransactionParam{ChainID: constants.Ethereum, Hash: stringPtr("0xoutgoing"), From: &addressA, To: &external, Amount: &amount, Status: &confirmed}},
		{Type: "internal", Tx: &types.TransactionParam{ChainID: constants.Ethereum, Hash: stringPtr("0xinternal"), From: &addressA, To: &addressA, Amount: &amount, Status: &confirmed}},
		{Type: "multi_input_internal", Tx: &types.TransactionParam{ChainID: constants.Ethereum, Hash: stringPtr("0xmulti"), From: &from, FromAddresses: []string{from, addressA}, To: &addressA, Amount: &amount, Status: &confirmed}},
		{Type: "pending", Tx: &types.TransactionParam{ChainID: constants.Ethereum, Hash: stringPtr("0xpending"), From: &from, To: &addressA, Amount: &amount, Status: &pending}},
		{Type: "missing_status", Tx: &types.TransactionParam{ChainID: constants.Ethereum, Hash: stringPtr("0xmissing"), From: &from, To: &addressA, Amount: &amount}},
		{Type: "failed", Tx: &types.TransactionParam{ChainID: constants.Ethereum, Hash: stringPtr("0xfailed"), From: &from, To: &addressA, Amount: &amount, Status: &failed}},
	}, merchantA.ID)
	if err != nil {
		t.Fatalf("filter merchant events: %v", err)
	}
	if len(filtered) != 1 || filtered[0].Tx == nil || filtered[0].Tx.To == nil || *filtered[0].Tx.To != addressA {
		t.Fatalf("filtered events = %#v, want only merchant A address", filtered)
	}
}

func TestCandidateAssetSupportedRejectsTokenEventWithoutIdentity(t *testing.T) {
	registry := asset.NewRegistry()
	registry.Register(asset.NewEVMNative(constants.Ethereum, "ETH", "Ethereum", 18))
	svc := &Service{Registry: registry}

	supported, err := svc.candidateAssetSupported(eventCandidate{
		Type: "token_transfer",
		Tx:   &types.TransactionParam{ChainID: constants.Ethereum},
	})
	if err != nil {
		t.Fatal(err)
	}
	if supported {
		t.Fatal("token event without token identity must not be treated as native")
	}
}

func TestCandidateSourceAddressesIncludesEveryInput(t *testing.T) {
	first := "bc1first"
	candidate := eventCandidate{Tx: &types.TransactionParam{
		From:          &first,
		FromAddresses: []string{"bc1first", "bc1platform", " ", "bc1platform"},
	}}
	got := candidateSourceAddresses(candidate)
	if strings.Join(got, ",") != "bc1first,bc1platform" {
		t.Fatalf("source addresses = %#v", got)
	}
}

func TestReceiptStatusFailsClosedForUnknownValues(t *testing.T) {
	if got := receiptStatus("0x1"); got != models.TransactionStatusConfirmed {
		t.Fatalf("confirmed status = %q", got)
	}
	if got := receiptStatus("0x0"); got != models.TransactionStatusFailed {
		t.Fatalf("failed status = %q", got)
	}
	if got := receiptStatus(""); got != "" {
		t.Fatalf("missing status = %q, want fail-closed empty", got)
	}
}

type fakeHistoricalRangeScanner struct {
	eventsByBlock map[int64][]HistoricalEvent
	attestations  map[int64]HistoricalBlockAttestation
}

func (f fakeHistoricalRangeScanner) ScanBlock(ctx context.Context, chainID constants.ChainID, blockNumber int64) ([]HistoricalEvent, error) {
	out := make([]HistoricalEvent, len(f.eventsByBlock[blockNumber]))
	copy(out, f.eventsByBlock[blockNumber])
	return out, nil
}

func (f fakeHistoricalRangeScanner) AttestBlock(_ context.Context, chainID constants.ChainID, blockNumber int64) (HistoricalBlockAttestation, error) {
	if attestation, ok := f.attestations[blockNumber]; ok {
		return attestation, nil
	}
	events := f.eventsByBlock[blockNumber]
	transactionIDs := make([]string, 0, len(events))
	seen := make(map[string]struct{}, len(events))
	blockHash := fmt.Sprintf("block-hash-%d", blockNumber)
	parentHash := fmt.Sprintf("block-hash-%d", blockNumber-1)
	for _, event := range events {
		if event.Tx.BlockHash != nil && strings.TrimSpace(*event.Tx.BlockHash) != "" {
			blockHash = *event.Tx.BlockHash
		}
		if event.Tx.ParentHash != nil && strings.TrimSpace(*event.Tx.ParentHash) != "" {
			parentHash = *event.Tx.ParentHash
		}
		if event.Tx.Hash == nil {
			continue
		}
		id := historicalIdentifier(chainID, *event.Tx.Hash)
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		transactionIDs = append(transactionIDs, *event.Tx.Hash)
	}
	return HistoricalBlockAttestation{
		ChainID:               chainID,
		BlockNumber:           blockNumber,
		BlockHash:             blockHash,
		ParentHash:            parentHash,
		TransactionCount:      len(transactionIDs),
		TransactionIDs:        transactionIDs,
		ScannedTransactionIDs: append([]string{}, transactionIDs...),
		Complete:              true,
	}, nil
}

type recordingHistoricalScanner struct {
	calls []int64
}

func (f *recordingHistoricalScanner) ScanBlock(_ context.Context, chainID constants.ChainID, blockNumber int64) ([]HistoricalEvent, error) {
	f.calls = append(f.calls, blockNumber)
	block := fmt.Sprintf("%d", blockNumber)
	hash := fmt.Sprintf("tx-%d", blockNumber)
	blockHash := fmt.Sprintf("block-hash-%d", blockNumber)
	parentHash := fmt.Sprintf("block-hash-%d", blockNumber-1)
	logIndex := "tx:0"
	return []HistoricalEvent{{
		Type: "native_transfer",
		Tx: types.TransactionParam{
			ChainID:    chainID,
			Hash:       &hash,
			Block:      &block,
			BlockHash:  &blockHash,
			ParentHash: &parentHash,
			LogIndex:   &logIndex,
		},
	}}, nil
}

func (f *recordingHistoricalScanner) AttestBlock(_ context.Context, chainID constants.ChainID, blockNumber int64) (HistoricalBlockAttestation, error) {
	txID := fmt.Sprintf("tx-%d", blockNumber)
	return HistoricalBlockAttestation{
		ChainID:               chainID,
		BlockNumber:           blockNumber,
		BlockHash:             fmt.Sprintf("block-hash-%d", blockNumber),
		ParentHash:            fmt.Sprintf("block-hash-%d", blockNumber-1),
		TransactionCount:      1,
		TransactionIDs:        []string{txID},
		ScannedTransactionIDs: []string{txID},
		Complete:              true,
	}, nil
}

type memoryHistoricalCheckpointStore struct {
	completed int64
	failAt    int64
	stores    []int64
}

func (s *memoryHistoricalCheckpointStore) LoadHistoricalRangeCheckpoint(context.Context, string, constants.ChainID) (int64, error) {
	return s.completed, nil
}

func (s *memoryHistoricalCheckpointStore) StoreHistoricalRangeCheckpoint(_ context.Context, _ string, _ constants.ChainID, block int64) error {
	s.stores = append(s.stores, block)
	if block == s.failAt {
		return errors.New("checkpoint unavailable")
	}
	s.completed = block
	return nil
}

func TestReplayHistoricalRangeResumesFromLastDurableCheckpoint(t *testing.T) {
	store := &memoryHistoricalCheckpointStore{failAt: 101}
	firstScanner := &recordingHistoricalScanner{}
	service := &Service{}
	first, err := service.ReplayHistoricalRange(context.Background(), HistoricalRangeRequest{
		ChainID:         constants.Ethereum,
		From:            100,
		To:              102,
		Scanner:         firstScanner,
		CheckpointKey:   "repair-job",
		CheckpointStore: store,
	})
	if err == nil || !strings.Contains(err.Error(), "store historical range checkpoint at block 101") {
		t.Fatalf("error = %v, want checkpoint failure", err)
	}
	if store.completed != 100 || first.LastCompletedBlock != 100 || first.NextBlock != 101 || first.Blocks != 1 {
		t.Fatalf("first checkpoint/result = %d/%#v, want durable block 100", store.completed, first)
	}
	if !reflect.DeepEqual(firstScanner.calls, []int64{100, 101}) {
		t.Fatalf("first scanner calls = %#v", firstScanner.calls)
	}

	store.failAt = 0
	secondScanner := &recordingHistoricalScanner{}
	second, err := service.ReplayHistoricalRange(context.Background(), HistoricalRangeRequest{
		ChainID:         constants.Ethereum,
		From:            100,
		To:              102,
		Scanner:         secondScanner,
		CheckpointKey:   "repair-job",
		CheckpointStore: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(secondScanner.calls, []int64{101, 102}) {
		t.Fatalf("resume scanner calls = %#v, want [101 102]", secondScanner.calls)
	}
	if store.completed != 102 || second.LastCompletedBlock != 102 || second.NextBlock != 103 || second.Blocks != 2 {
		t.Fatalf("second checkpoint/result = %d/%#v, want durable block 102", store.completed, second)
	}
}

func TestReplayHistoricalRangeRejectsBlockParityBeforeCheckpoint(t *testing.T) {
	claimedBlock := "99"
	hash := "wrong-block"
	blockHash := "block-hash-100"
	logIndex := "tx:0"
	store := &memoryHistoricalCheckpointStore{}
	result, err := (&Service{}).ReplayHistoricalRange(context.Background(), HistoricalRangeRequest{
		ChainID: constants.Ethereum,
		From:    100,
		To:      100,
		Scanner: fakeHistoricalRangeScanner{eventsByBlock: map[int64][]HistoricalEvent{100: {{
			Type: "native_transfer",
			Tx:   types.TransactionParam{ChainID: constants.Ethereum, Hash: &hash, Block: &claimedBlock, BlockHash: &blockHash, LogIndex: &logIndex},
		}}}},
		CheckpointKey:   "parity-job",
		CheckpointStore: store,
	})
	if !errors.Is(err, ErrIncompleteProviderResponse) {
		t.Fatalf("error = %v, want incomplete provider response", err)
	}
	if result.LastCompletedBlock != 99 || store.completed != 0 || len(store.stores) != 0 {
		t.Fatalf("checkpoint advanced on parity failure: result=%#v store=%#v", result, store)
	}
}

type historicalScannerWithoutAttestation struct{}

func (historicalScannerWithoutAttestation) ScanBlock(context.Context, constants.ChainID, int64) ([]HistoricalEvent, error) {
	return []HistoricalEvent{}, nil
}

func TestReplayHistoricalRangeRequiresCoverageAttestationBeforeCheckpoint(t *testing.T) {
	store := &memoryHistoricalCheckpointStore{}
	_, err := (&Service{}).ReplayHistoricalRange(context.Background(), HistoricalRangeRequest{
		ChainID:         constants.Ethereum,
		From:            100,
		To:              100,
		Scanner:         historicalScannerWithoutAttestation{},
		CheckpointKey:   "missing-attestation",
		CheckpointStore: store,
	})
	if !errors.Is(err, ErrIncompleteProviderResponse) {
		t.Fatalf("error = %v, want incomplete provider response", err)
	}
	if store.completed != 0 || len(store.stores) != 0 {
		t.Fatalf("checkpoint advanced without attestation: %#v", store)
	}
}

func TestReplayHistoricalRangeRejectsTruncatedTransactionCoverageBeforeCheckpoint(t *testing.T) {
	store := &memoryHistoricalCheckpointStore{}
	scanner := fakeHistoricalRangeScanner{
		eventsByBlock: map[int64][]HistoricalEvent{100: {}},
		attestations: map[int64]HistoricalBlockAttestation{100: {
			ChainID:               constants.Ethereum,
			BlockNumber:           100,
			BlockHash:             "block-hash-100",
			ParentHash:            "block-hash-99",
			TransactionCount:      2,
			TransactionIDs:        []string{"0xone", "0xtwo"},
			ScannedTransactionIDs: []string{"0xone"},
			Complete:              true,
		}},
	}
	result, err := (&Service{}).ReplayHistoricalRange(context.Background(), HistoricalRangeRequest{
		ChainID:         constants.Ethereum,
		From:            100,
		To:              100,
		Scanner:         scanner,
		CheckpointKey:   "truncated-coverage",
		CheckpointStore: store,
	})
	if !errors.Is(err, ErrIncompleteProviderResponse) {
		t.Fatalf("error = %v, want incomplete provider response", err)
	}
	if result.LastCompletedBlock != 99 || store.completed != 0 || len(store.stores) != 0 {
		t.Fatalf("checkpoint advanced on truncated coverage: result=%#v store=%#v", result, store)
	}
}

func TestReplayHistoricalRangeAdvancesForAttestedEmptyBlock(t *testing.T) {
	store := &memoryHistoricalCheckpointStore{}
	result, err := (&Service{}).ReplayHistoricalRange(context.Background(), HistoricalRangeRequest{
		ChainID:         constants.Ethereum,
		From:            100,
		To:              100,
		Scanner:         fakeHistoricalRangeScanner{eventsByBlock: map[int64][]HistoricalEvent{100: {}}},
		CheckpointKey:   "attested-empty",
		CheckpointStore: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Blocks != 1 || result.Events != 0 || result.LastCompletedBlock != 100 || store.completed != 100 {
		t.Fatalf("attested empty result/store = %#v/%#v", result, store)
	}
}

func TestReplayHistoricalRangeRecordsFactsIdempotently(t *testing.T) {
	db := openTxRescanPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.Block{}, &models.Transaction{}, &models.ChainFact{}); err != nil {
		t.Fatalf("automigrate chain facts: %v", err)
	}
	merchant := models.Merchant{ID: uuid.New(), Name: "range-merchant", Email: "range@example.com", Password: "x", IsActive: true}
	domain := models.Domain{ID: uuid.New(), MerchantID: merchant.ID, DomainURL: "range.example.com", APIKey: "range-key", APISecret: "range-secret", HDAccountID: 1}
	wallet := models.Wallet{
		ID: uuid.New(), MerchantID: merchant.ID, DomainID: domain.ID, HDAccountID: 1, HDAddressId: 1,
		ProductID: "range-wallet", UserID: "range-user", BitcoinAddress: "btc-range", EthereumAddress: "0x1111111111111111111111111111111111111111",
		AvalancheAddress: "avax-range", BinanceAddress: "bnb-range", BaseAddress: "base-range", ArbitrumAddress: "arb-range",
		UnichainAddress: "uni-range", TronAddress: "tron-range", SolanaAddress: "sol-range", ChilizAddress: "chz-range", ChilizSpicyAddress: "spicy-range",
	}
	if err := db.Create(&merchant).Error; err != nil {
		t.Fatalf("seed range merchant: %v", err)
	}
	if err := db.Create(&domain).Error; err != nil {
		t.Fatalf("seed range domain: %v", err)
	}
	if err := db.Create(&wallet).Error; err != nil {
		t.Fatalf("seed range wallet: %v", err)
	}
	registry := asset.NewRegistry()
	registry.Register(asset.NewEVMNative(constants.Ethereum, "ETH", "Ethereum", 18))
	service := &Service{
		ChainFactRepo:   repositories.NewChainFactRepo(db),
		TransactionRepo: repositories.NewTransactionRepo(db),
		WalletRepo:      repositories.NewWalletRepo(repositories.NewDomainRepo(repositories.NewMerchantRepo(db, nil))),
		Registry:        registry,
	}
	hash := "0x" + strings.ReplaceAll(uuid.NewString(), "-", "")
	unownedHash := "0x" + strings.ReplaceAll(uuid.NewString(), "-", "")
	unknownTokenHash := "0x" + strings.ReplaceAll(uuid.NewString(), "-", "")
	block := "100"
	blockHash := "block-hash-100"
	parentHash := "block-hash-99"
	logIndex := "tx:0"
	event := HistoricalEvent{
		Type: "native_transfer",
		Tx: types.TransactionParam{
			Context:    context.Background(),
			ChainID:    constants.Ethereum,
			Symbol:     stringPtr("ETH"),
			Decimals:   18,
			Hash:       &hash,
			Block:      &block,
			BlockHash:  &blockHash,
			ParentHash: &parentHash,
			From:       stringPtr("0xfrom"),
			To:         stringPtr(wallet.EthereumAddress),
			Amount:     stringPtr("1"),
			LogIndex:   &logIndex,
			Status:     stringPtr(models.TransactionStatusConfirmed),
		},
		Confirmations:         12,
		ConfirmationsRequired: 12,
	}
	unownedEvent := event
	unownedEvent.Tx.Hash = &unownedHash
	unownedEvent.Tx.To = stringPtr("0x9999999999999999999999999999999999999999")
	unknownTokenEvent := event
	unknownTokenEvent.Tx.Hash = &unknownTokenHash
	unknownTokenEvent.Tx.Token = stringPtr("0x2222222222222222222222222222222222222222")
	scanner := fakeHistoricalRangeScanner{eventsByBlock: map[int64][]HistoricalEvent{100: {event, unownedEvent, unknownTokenEvent}}}

	first, err := service.ReplayHistoricalRange(context.Background(), HistoricalRangeRequest{
		ChainID: constants.Ethereum,
		From:    100,
		To:      100,
		Scanner: scanner,
	})
	if err != nil {
		t.Fatalf("first replay: %v", err)
	}
	second, err := service.ReplayHistoricalRange(context.Background(), HistoricalRangeRequest{
		ChainID: constants.Ethereum,
		From:    100,
		To:      100,
		Scanner: scanner,
	})
	if err != nil {
		t.Fatalf("second replay: %v", err)
	}
	if first.Events != 1 || second.Events != 1 {
		t.Fatalf("events = %d/%d, want replay attempt counted once each run", first.Events, second.Events)
	}
	var count int64
	if err := db.Model(&models.ChainFact{}).Where("event_id = ?", repositories.ChainFactEventID(constants.Ethereum, hash, logIndex)).Count(&count).Error; err != nil {
		t.Fatalf("count chain facts: %v", err)
	}
	if count != 1 {
		t.Fatalf("chain fact count = %d, want 1 after duplicate range replay", count)
	}
	if err := db.Model(&models.ChainFact{}).Count(&count).Error; err != nil {
		t.Fatalf("count all chain facts: %v", err)
	}
	if count != 1 {
		t.Fatalf("all chain fact count = %d, want only the owned inbound event", count)
	}
}

func TestDepositSettlementReloadsLockedChainFactAfterConcurrentReorg(t *testing.T) {
	db := openTxRescanPostgresTestDB(t)
	if err := db.AutoMigrate(&models.ChainFact{}, &models.MoneyEventInbox{}, &models.Deposit{}); err != nil {
		t.Fatalf("automigrate stale fact race models: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(4)

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	fact := models.ChainFact{
		ID: uuid.New(), EventID: "stale-fact:" + uuid.NewString(), ChainID: constants.Ethereum,
		BlockNumber: 100, BlockHash: "0xorphan", TxHash: "0xstale", LogIndex: "tx:0",
		ObservedAddress: "0x0000000000000000000000000000000000000001", Direction: models.ChainFactDirectionTo,
		ObservationStatus: models.ChainFactObservationConfirmed, Symbol: "ETH", Decimals: 18, AmountRaw: "100",
		Confirmations: 12, ConfirmationsRequired: 12, Finalized: true, Status: models.ChainFactStatusObserved,
		SourceEventType: "native_transfer", RawMetadataJSON: `{}`, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.WithContext(ctx).Create(&fact).Error; err != nil {
		t.Fatalf("seed stale fact: %v", err)
	}

	correctionTx := db.WithContext(ctx).Begin()
	if correctionTx.Error != nil {
		t.Fatal(correctionTx.Error)
	}
	defer correctionTx.Rollback()
	if err := repositories.AcquireCanonicalBlockLockWithDB(ctx, correctionTx, fact.ChainID, fact.BlockNumber); err != nil {
		t.Fatalf("lock canonical height for correction: %v", err)
	}
	var locked models.ChainFact
	if err := correctionTx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, "event_id = ?", fact.EventID).Error; err != nil {
		t.Fatalf("lock fact for correction: %v", err)
	}

	attemptedCanonicalLock := make(chan struct{}, 1)
	callbackName := "test:stale-canonical-lock:" + uuid.NewString()
	if err := db.Callback().Raw().Before("gorm:raw").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement != nil && strings.Contains(tx.Statement.SQL.String(), "pg_advisory_xact_lock") {
			select {
			case attemptedCanonicalLock <- struct{}{}:
			default:
			}
		}
	}); err != nil {
		t.Fatalf("register canonical-lock callback: %v", err)
	}
	defer db.Callback().Raw().Remove(callbackName)

	processor := depositsvc.New(depositsvc.Dependencies{
		ChainFactRepo:       repositories.NewChainFactRepo(db),
		MoneyEventInboxRepo: repositories.NewMoneyEventInboxRepo(db),
	}, nil)
	type processResult struct {
		summary depositsvc.ProcessSummary
		err     error
	}
	processed := make(chan processResult, 1)
	go func() {
		summary, err := processor.ProcessFactSafely(ctx, fact)
		processed <- processResult{summary: summary, err: err}
	}()

	select {
	case <-attemptedCanonicalLock:
	case <-time.After(5 * time.Second):
		t.Fatal("settlement did not wait on the canonical correction lock")
	}
	reorgedAt := time.Now().UTC().Truncate(time.Microsecond)
	if err := correctionTx.Model(&models.ChainFact{}).Where("event_id = ?", fact.EventID).Updates(map[string]any{
		"status": models.ChainFactStatusReorged, "reorged_at": &reorgedAt, "correction_reason": "concurrent canonical correction",
	}).Error; err != nil {
		t.Fatalf("apply concurrent correction: %v", err)
	}
	if err := correctionTx.Commit().Error; err != nil {
		t.Fatalf("commit concurrent correction: %v", err)
	}

	select {
	case result := <-processed:
		if result.err != nil {
			t.Fatalf("process stale fact: %v", result.err)
		}
		if result.summary.FactsProcessed != 1 || result.summary.DepositsCreated != 0 || result.summary.TransactionsRecorded != 0 {
			t.Fatalf("stale fact summary = %#v", result.summary)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("settlement remained blocked after correction commit")
	}
	var depositCount int64
	if err := db.Model(&models.Deposit{}).Count(&depositCount).Error; err != nil {
		t.Fatal(err)
	}
	if depositCount != 0 {
		t.Fatalf("stale finalized snapshot created %d deposits", depositCount)
	}
}

func TestDepositSettlementRejectsFinalizedNonCanonicalChainFact(t *testing.T) {
	db := openTxRescanPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Block{}, &models.ChainState{}, &models.ChainFact{}, &models.MoneyEventInbox{}, &models.Deposit{}); err != nil {
		t.Fatalf("automigrate canonical settlement models: %v", err)
	}
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	fact := models.ChainFact{
		ID: uuid.New(), EventID: "noncanonical-fact:" + uuid.NewString(), ChainID: constants.Ethereum,
		BlockNumber: 100, BlockHash: "0xorphan", TxHash: "0xnoncanonical", LogIndex: "tx:0",
		ObservedAddress: "0x0000000000000000000000000000000000000001", Direction: models.ChainFactDirectionTo,
		ObservationStatus: models.ChainFactObservationConfirmed, Symbol: "ETH", Decimals: 18, AmountRaw: "100",
		Confirmations: 12, ConfirmationsRequired: 12, Finalized: true, Status: models.ChainFactStatusObserved,
		SourceEventType: "native_transfer", RawMetadataJSON: `{}`, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&fact).Error; err != nil {
		t.Fatalf("seed noncanonical fact: %v", err)
	}
	if err := db.Create(&models.Block{
		ID: uuid.New(), ChainID: constants.Ethereum, Number: 100, Hash: "0xcanonical", ParentHash: "0xparent",
		Canonical: true, Status: models.BlockStatusCanonical, Processed: true, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed canonical block: %v", err)
	}
	if err := db.Create(&models.ChainState{ChainID: constants.Ethereum, LastProcessedBlock: 111, LastConfirmedBlock: 111, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("seed chain state: %v", err)
	}
	processor := depositsvc.New(depositsvc.Dependencies{
		ChainFactRepo:       repositories.NewChainFactRepo(db),
		ChainStateRepo:      repositories.NewChainStateRepo(db),
		MoneyEventInboxRepo: repositories.NewMoneyEventInboxRepo(db),
	}, nil)
	_, err := processor.ProcessFactSafely(ctx, fact)
	if !errors.Is(err, depositsvc.ErrFinalizedChainFactNotCanonical) {
		t.Fatalf("error = %v, want noncanonical finalized fact rejection", err)
	}
	var depositCount int64
	if err := db.Model(&models.Deposit{}).Count(&depositCount).Error; err != nil {
		t.Fatal(err)
	}
	if depositCount != 0 {
		t.Fatalf("noncanonical finalized fact created %d deposits", depositCount)
	}
}

func TestPendingDepositSettlementWaitsForCanonicalCorrectionBeforeFactLock(t *testing.T) {
	db := openTxRescanPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Block{}, &models.ChainFact{}, &models.Deposit{}, &models.Transaction{}); err != nil {
		t.Fatalf("automigrate pending correction models: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(4)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	fact := models.ChainFact{
		ID: uuid.New(), EventID: "pending-race:" + uuid.NewString(), ChainID: constants.Ethereum,
		BlockNumber: 100, BlockHash: "0xorphan", TxHash: "0xpending-race", LogIndex: "tx:0",
		ObservedAddress: "0x0000000000000000000000000000000000000001", Direction: models.ChainFactDirectionTo,
		ObservationStatus: models.ChainFactObservationConfirmed, Symbol: "ETH", Decimals: 18, AmountRaw: "100",
		Confirmations: 1, ConfirmationsRequired: 12, Status: models.ChainFactStatusObserved,
		SourceEventType: "native_transfer", RawMetadataJSON: `{}`, CreatedAt: now, UpdatedAt: now,
	}
	deposit := models.Deposit{
		ID: uuid.New(), ChainFactID: fact.ID, ChainFactEventID: fact.EventID, Status: models.DepositStatusConfirming,
		ChainID: fact.ChainID, BlockNumber: fact.BlockNumber, BlockHash: fact.BlockHash, TxHash: fact.TxHash, LogIndex: fact.LogIndex,
		ObservedAddress: fact.ObservedAddress, Direction: fact.Direction, ObservationStatus: fact.ObservationStatus,
		MemoStatus: models.DepositMemoStatusNotRequired, Symbol: fact.Symbol, Decimals: fact.Decimals, AmountRaw: fact.AmountRaw,
		Confirmations: fact.Confirmations, ConfirmationsRequired: fact.ConfirmationsRequired, SourceEventType: fact.SourceEventType,
		DetectedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&fact).Error; err != nil {
		t.Fatalf("seed pending fact: %v", err)
	}
	if err := db.Create(&deposit).Error; err != nil {
		t.Fatalf("seed pending deposit: %v", err)
	}
	if err := db.Create(&models.Block{ID: uuid.New(), ChainID: fact.ChainID, Number: fact.BlockNumber, Hash: fact.BlockHash, ParentHash: "0xparent", Canonical: true, Status: models.BlockStatusCanonical, Processed: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("seed orphan-before-correction block: %v", err)
	}

	correctionTx := db.WithContext(ctx).Begin()
	if correctionTx.Error != nil {
		t.Fatal(correctionTx.Error)
	}
	defer correctionTx.Rollback()
	if err := repositories.AcquireCanonicalBlockLockWithDB(ctx, correctionTx, fact.ChainID, fact.BlockNumber); err != nil {
		t.Fatalf("lock pending canonical height: %v", err)
	}
	var lockedFact models.ChainFact
	if err := correctionTx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedFact, "id = ?", fact.ID).Error; err != nil {
		t.Fatalf("lock pending fact: %v", err)
	}

	attemptedCanonicalLock := make(chan struct{}, 1)
	callbackName := "test:pending-canonical-lock:" + uuid.NewString()
	if err := db.Callback().Raw().Before("gorm:raw").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement != nil && strings.Contains(tx.Statement.SQL.String(), "pg_advisory_xact_lock") {
			select {
			case attemptedCanonicalLock <- struct{}{}:
			default:
			}
		}
	}); err != nil {
		t.Fatalf("register pending canonical-lock callback: %v", err)
	}
	defer db.Callback().Raw().Remove(callbackName)

	processor := depositsvc.New(depositsvc.Dependencies{
		ChainFactRepo: repositories.NewChainFactRepo(db),
		DepositRepo:   repositories.NewDepositRepo(db),
	}, nil)
	type pendingResult struct {
		summary depositsvc.ProcessSummary
		err     error
	}
	done := make(chan pendingResult, 1)
	go func() {
		summary, err := processor.ProcessPendingDepositSafely(ctx, deposit)
		done <- pendingResult{summary: summary, err: err}
	}()
	select {
	case <-attemptedCanonicalLock:
	case <-time.After(5 * time.Second):
		t.Fatal("pending settlement did not wait on canonical correction")
	}
	reorgedAt := time.Now().UTC().Truncate(time.Microsecond)
	if err := correctionTx.Model(&models.ChainFact{}).Where("id = ?", fact.ID).Updates(map[string]any{"status": models.ChainFactStatusReorged, "reorged_at": &reorgedAt}).Error; err != nil {
		t.Fatal(err)
	}
	if err := correctionTx.Model(&models.Deposit{}).Where("id = ?", deposit.ID).Updates(map[string]any{"status": models.DepositStatusReorged, "reorged_at": &reorgedAt}).Error; err != nil {
		t.Fatal(err)
	}
	if err := correctionTx.Model(&models.Block{}).Where("chain_id = ? AND number = ?", fact.ChainID, fact.BlockNumber).Updates(map[string]any{"canonical": false, "status": models.BlockStatusReorged, "reorged_at": &reorgedAt}).Error; err != nil {
		t.Fatal(err)
	}
	if err := correctionTx.Commit().Error; err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.err != nil || result.summary.TransactionsRecorded != 0 || result.summary.Finalized != 0 {
			t.Fatalf("pending result = %#v, error = %v", result.summary, result.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending settlement remained blocked after correction")
	}
	var transactionCount int64
	if err := db.Model(&models.Transaction{}).Count(&transactionCount).Error; err != nil {
		t.Fatal(err)
	}
	if transactionCount != 0 {
		t.Fatalf("pending stale snapshot created %d transactions", transactionCount)
	}
}

func TestScanTronProducesNativeTRXTransfer(t *testing.T) {
	const txHash = "abc123"
	ownerHex := "410000000000000000000000000000000000000001"
	toHex := "410000000000000000000000000000000000000002"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/wallet/gettransactionbyid":
			_, _ = fmt.Fprintf(w, `{
				"txID": %q,
				"raw_data": {
					"contract": [{
						"type": "TransferContract",
						"parameter": {
							"value": {
								"amount": 1000000,
								"owner_address": %q,
								"to_address": %q
							}
						}
					}]
				}
			}`, txHash, ownerHex, toHex)
		case "/wallet/gettransactioninfobyid":
			_, _ = w.Write([]byte(`{"id":"abc123","blockNumber":123456,"internal_transactions":[],"receipt":{"result":"SUCCESS"}}`))
		case "/wallet/getblockbynum":
			_, _ = w.Write([]byte(`{"blockID":"block-123456","transactions":[{"txID":"abc123"}],"block_header":{"raw_data":{"number":123456}}}`))
		case "/wallet/getnowblock":
			_, _ = w.Write([]byte(`{"block_header":{"raw_data":{"number":123460}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("TRON_HTTP_ENDPOINTS", server.URL)

	registry := asset.NewRegistry()
	registry.Register(asset.NewTRX(constants.TRON))
	svc := &Service{Registry: registry, client: server.Client()}
	chain := chainpkg.NewTronChain()

	events, err := svc.scanTron(context.Background(), chain, txHash)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	event := events[0]
	if event.Type != "native_transfer" {
		t.Fatalf("type = %q, want native_transfer", event.Type)
	}
	if event.Tx == nil {
		t.Fatal("event tx is nil")
	}
	if got := *event.Tx.Symbol; got != "TRX" {
		t.Fatalf("symbol = %q, want TRX", got)
	}
	if event.Tx.ChainID != constants.TRON {
		t.Fatalf("chainID = %d, want TRON", event.Tx.ChainID)
	}
	if got := *event.Tx.Amount; got != "1000000" {
		t.Fatalf("amount = %q, want 1000000", got)
	}
	if got := *event.Tx.LogIndex; got != "tx:0" {
		t.Fatalf("log index = %q, want tx:0", got)
	}
	if got := *event.Tx.To; got != tronHexToBase58(toHex) {
		t.Fatalf("to = %q, want %q", got, tronHexToBase58(toHex))
	}
	if got := *event.Tx.From; got != tronHexToBase58(ownerHex) {
		t.Fatalf("from = %q, want %q", got, tronHexToBase58(ownerHex))
	}
	if event.Tx.Decimals != 6 {
		t.Fatalf("decimals = %d, want 6", event.Tx.Decimals)
	}
}

func TestScanTronRejectsMalformedTransactionInfo(t *testing.T) {
	const txHash = "malformed-info"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/wallet/gettransactionbyid":
			_, _ = w.Write([]byte(`{"txID":"malformed-info","raw_data":{"contract":[]}}`))
		case "/wallet/gettransactioninfobyid":
			_, _ = w.Write([]byte(`{"blockNumber":`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("TRON_HTTP_ENDPOINTS", server.URL)

	registry := asset.NewRegistry()
	registry.Register(asset.NewTRX(constants.TRON))
	svc := &Service{Registry: registry, client: server.Client()}
	_, err := svc.scanTron(context.Background(), chainpkg.NewTronChain(), txHash)
	if err == nil || !strings.Contains(err.Error(), "decode TRON transaction info") {
		t.Fatalf("error = %v, want transaction-info decode error", err)
	}
}

func TestScanTronIncludesAttestedInternalNativeTransfer(t *testing.T) {
	const txHash = "8fef7c128e5db6d32eb375c18dc5a21cc2baff15fd30e149f0ced74cfe63d0ee"
	const internalHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const callerHex = "4163d090b2101f125f65e8fae5b9744d0e74eb8746"
	const toHex = "4107cb66bc50d09c784a843a6f2a0d942995facb92"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wallet/gettransactionbyid":
			_, _ = fmt.Fprintf(w, `{"txID":%q,"ret":[{"contractRet":"SUCCESS"}],"raw_data":{"contract":[]}}`, txHash)
		case "/wallet/gettransactioninfobyid":
			_, _ = fmt.Fprintf(w, `{"id":%q,"blockNumber":123,"receipt":{"result":"SUCCESS"},"internal_transactions":[{"hash":%q,"caller_address":%q,"transferTo_address":%q,"callValueInfo":[{"callValue":2500000,"tokenId":""}],"note":"63616c6c","rejected":false}]}`, txHash, internalHash, callerHex, toHex)
		case "/wallet/getblockbynum":
			_, _ = fmt.Fprintf(w, `{"blockID":"block-123","transactions":[{"txID":%q}],"block_header":{"raw_data":{"number":123}}}`, txHash)
		case "/wallet/getnowblock":
			_, _ = w.Write([]byte(`{"block_header":{"raw_data":{"number":123}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("TRON_HTTP_ENDPOINTS", server.URL)
	registry := asset.NewRegistry()
	registry.Register(asset.NewTRX(constants.TRON))
	events, err := (&Service{Registry: registry, client: server.Client()}).scanTron(context.Background(), chainpkg.NewTronChain(), txHash)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != "internal_transfer" || events[0].Tx == nil ||
		*events[0].Tx.LogIndex != "internal:"+internalHash+":trx" || *events[0].Tx.Amount != "2500000" ||
		*events[0].Tx.From != tronHexToBase58(callerHex) || *events[0].Tx.To != tronHexToBase58(toHex) {
		t.Fatalf("internal TRX event = %#v", events)
	}
}

func TestScanTronFailsClosedWhenInternalTransactionEvidenceIsMissing(t *testing.T) {
	const txHash = "8fef7c128e5db6d32eb375c18dc5a21cc2baff15fd30e149f0ced74cfe63d0ee"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wallet/gettransactionbyid":
			_, _ = fmt.Fprintf(w, `{"txID":%q,"ret":[{"contractRet":"SUCCESS"}],"raw_data":{"contract":[]}}`, txHash)
		case "/wallet/gettransactioninfobyid":
			_, _ = fmt.Fprintf(w, `{"id":%q,"blockNumber":123,"receipt":{"result":"SUCCESS"}}`, txHash)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("TRON_HTTP_ENDPOINTS", server.URL)
	registry := asset.NewRegistry()
	registry.Register(asset.NewTRX(constants.TRON))
	_, err := (&Service{Registry: registry, client: server.Client()}).scanTron(context.Background(), chainpkg.NewTronChain(), txHash)
	if !errors.Is(err, ErrIncompleteProviderResponse) {
		t.Fatalf("error = %v, want incomplete provider response", err)
	}
}

func TestScanTronRejectsBlockIdentityMismatch(t *testing.T) {
	const txHash = "tron-identity"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/wallet/gettransactionbyid":
			_, _ = w.Write([]byte(`{"txID":"tron-identity","ret":[{"contractRet":"SUCCESS"}],"raw_data":{"contract":[]}}`))
		case "/wallet/gettransactioninfobyid":
			_, _ = w.Write([]byte(`{"id":"tron-identity","blockNumber":123,"receipt":{"result":"SUCCESS"}}`))
		case "/wallet/getblockbynum":
			_, _ = w.Write([]byte(`{"blockID":"wrong-block","block_header":{"raw_data":{"number":124}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("TRON_HTTP_ENDPOINTS", server.URL)
	registry := asset.NewRegistry()
	registry.Register(asset.NewTRX(constants.TRON))
	_, err := (&Service{Registry: registry, client: server.Client()}).scanTron(context.Background(), chainpkg.NewTronChain(), txHash)
	if !errors.Is(err, ErrIncompleteProviderResponse) {
		t.Fatalf("error = %v, want incomplete provider response", err)
	}
}

func TestScanTronProducesNativeTRXTransferFromFullNodeResponse(t *testing.T) {
	const txHash = "8fef7c128e5db6d32eb375c18dc5a21cc2baff15fd30e149f0ced74cfe63d0ee"
	const ownerHex = "4163d090b2101f125f65e8fae5b9744d0e74eb8746"
	const toHex = "4107cb66bc50d09c784a843a6f2a0d942995facb92"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/wallet/gettransactionbyid":
			_, _ = fmt.Fprintf(w, `{
				"ret":[{"contractRet":"SUCCESS"}],
				"txID":%q,
				"raw_data":{"contract":[{
					"parameter":{"value":{"amount":18500000,"owner_address":%q,"to_address":%q},"type_url":"type.googleapis.com/protocol.TransferContract"},
					"type":"TransferContract"
				}]}
			}`, txHash, ownerHex, toHex)
		case "/wallet/gettransactioninfobyid":
			_, _ = w.Write([]byte(`{"id":"` + txHash + `","fee":1000000,"blockNumber":83954659,"blockTimeStamp":1782533604000,"internal_transactions":[],"receipt":{"net_usage":274}}`))
		case "/wallet/getblockbynum":
			_, _ = w.Write([]byte(`{"blockID":"block-83954659","transactions":[{"txID":"` + txHash + `"}],"block_header":{"raw_data":{"number":83954659}}}`))
		case "/wallet/getnowblock":
			_, _ = w.Write([]byte(`{"block_header":{"raw_data":{"number":83954670}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("TRON_HTTP_ENDPOINTS", server.URL)

	registry := asset.NewRegistry()
	registry.Register(asset.NewTRX(constants.TRON))
	chain := chainpkg.NewTronChain()
	svc := &Service{
		Registry: registry,
		client:   server.Client(),
		Confirmations: func(chainID constants.ChainID) uint {
			if chainID != constants.TRON {
				t.Fatalf("chainID = %d, want TRON", chainID)
			}
			return 2
		},
	}

	events, err := svc.scanTron(context.Background(), chain, txHash)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	event := events[0]
	if event.Type != "native_transfer" {
		t.Fatalf("type = %q, want native_transfer", event.Type)
	}
	if event.Confirmations != 12 || event.ConfirmationsRequired != 2 {
		t.Fatalf("confirmations = %d/%d, want 12/2", event.Confirmations, event.ConfirmationsRequired)
	}
	if got := *event.Tx.Amount; got != "18500000" {
		t.Fatalf("amount = %q, want 18500000", got)
	}
	if got := *event.Tx.To; got != tronHexToBase58(toHex) {
		t.Fatalf("to = %q, want %q", got, tronHexToBase58(toHex))
	}
	if got := *event.Tx.From; got != tronHexToBase58(ownerHex) {
		t.Fatalf("from = %q, want %q", got, tronHexToBase58(ownerHex))
	}
	if got := *event.Tx.Status; got != "confirmed" {
		t.Fatalf("status = %q, want confirmed", got)
	}
}

func TestScanTronTestnetUsesTestnetChainIDAndEndpoints(t *testing.T) {
	const txHash = "abc123"
	ownerHex := "410000000000000000000000000000000000000001"
	toHex := "410000000000000000000000000000000000000002"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/wallet/gettransactionbyid":
			_, _ = fmt.Fprintf(w, `{
				"txID": %q,
				"raw_data": {
					"contract": [{
						"type": "TransferContract",
						"parameter": {
							"value": {
								"amount": 2500000,
								"owner_address": %q,
								"to_address": %q
							}
						}
					}]
				}
			}`, txHash, ownerHex, toHex)
		case "/wallet/gettransactioninfobyid":
			_, _ = w.Write([]byte(`{"id":"abc123","blockNumber":123456,"internal_transactions":[],"receipt":{"result":"SUCCESS"}}`))
		case "/wallet/getblockbynum":
			_, _ = w.Write([]byte(`{"blockID":"block-123456","transactions":[{"txID":"abc123"}],"block_header":{"raw_data":{"number":123456}}}`))
		case "/wallet/getnowblock":
			_, _ = w.Write([]byte(`{"block_header":{"raw_data":{"number":123460}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("TRON_TESTNET_HTTP_ENDPOINTS", server.URL)
	t.Setenv("TRON_HTTP_ENDPOINTS", "http://mainnet.invalid")

	registry := asset.NewRegistry()
	registry.Register(asset.NewTRX(constants.TRONTestnet))
	svc := &Service{Registry: registry, client: server.Client()}
	chain := chainpkg.NewTronTestnetChain()

	events, err := svc.scanTron(context.Background(), chain, txHash)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	event := events[0]
	if event.Tx == nil {
		t.Fatal("event tx is nil")
	}
	if event.Tx.ChainID != constants.TRONTestnet {
		t.Fatalf("chainID = %d, want TRONTestnet", event.Tx.ChainID)
	}
	if got := *event.Tx.Amount; got != "2500000" {
		t.Fatalf("amount = %q, want 2500000", got)
	}
}

func stringPtr(value string) *string {
	return &value
}

func openTxRescanPostgresTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TXRESCAN_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("TEST_DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("set TXRESCAN_TEST_DATABASE_URL to run txrescan Postgres tests")
	}

	adminDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("connect test postgres: %v", err)
	}
	if err := adminDB.Exec(`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`).Error; err != nil {
		t.Fatalf("enable uuid extension: %v", err)
	}
	schemaName := "txrescan_test_" + strings.ReplaceAll(uuid.NewString(), "-", "_")
	quotedSchema := quoteTxRescanPostgresIdentifier(schemaName)
	if err := adminDB.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	if err := adminDB.Exec(
		"CREATE FUNCTION " + quotedSchema + ".uuid_generate_v4() RETURNS uuid LANGUAGE SQL VOLATILE PARALLEL SAFE AS 'SELECT public.uuid_generate_v4()'",
	).Error; err != nil {
		t.Fatalf("create schema-local uuid function: %v", err)
	}

	db, err := gorm.Open(postgres.Open(txRescanPostgresDSNWithSearchPath(dsn, schemaName)), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("connect schema-scoped test postgres: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get test sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = sqlDB.Close()
		_ = adminDB.Exec("DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE").Error
		if adminSQL, err := adminDB.DB(); err == nil {
			_ = adminSQL.Close()
		}
	})
	return db
}

func txRescanPostgresDSNWithSearchPath(dsn string, schemaName string) string {
	searchPath := schemaName
	if strings.Contains(dsn, "://") {
		parsed, err := url.Parse(dsn)
		if err == nil {
			query := parsed.Query()
			query.Set("search_path", searchPath)
			parsed.RawQuery = query.Encode()
			return parsed.String()
		}
	}
	return strings.TrimSpace(dsn) + " search_path=" + searchPath
}

func quoteTxRescanPostgresIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}
