package solana

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"core/asset"
	chainpkg "core/blockchain/chains"
	"core/constants"
	"core/models"
	"core/workers/dispatcher"
	listenerconfig "core/workers/listeners"
)

type testJSONRPCRequest struct {
	ID     int64             `json:"id"`
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
}

func readTestJSONRPCRequest(t *testing.T, r *http.Request) testJSONRPCRequest {
	t.Helper()
	var request testJSONRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		t.Errorf("decode JSON-RPC request: %v", err)
	}
	return request
}

func writeTestJSONRPCResponse(t *testing.T, w http.ResponseWriter, requestID int64, result interface{}, rpcErr *jsonRPCError) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      requestID,
		"result":  result,
		"error":   rpcErr,
	}); err != nil {
		t.Errorf("encode JSON-RPC response: %v", err)
	}
}

func TestSolanaTransactionMemoFromParsedMemoInstruction(t *testing.T) {
	memoRaw, err := json.Marshal("ORDER-42")
	if err != nil {
		t.Fatal(err)
	}

	got := solanaTransactionMemo([]Instruction{
		{Program: "system", Parsed: json.RawMessage(`{"type":"transfer"}`)},
		{Program: "spl-memo", ProgramID: solanaMemoProgram, Parsed: memoRaw},
	}, nil)

	if got != "ORDER-42" {
		t.Fatalf("memo = %q, want ORDER-42", got)
	}
}

func TestSolanaTransactionMemoFromInnerMemoInfo(t *testing.T) {
	got := solanaTransactionMemo(nil, []InnerInstructions{{
		Index: 2,
		Instructions: []Instruction{{
			ProgramID: solanaMemoProgram,
			Parsed:    json.RawMessage(`{"type":"memo","info":{"memo":" INV-900 "}}`),
		}},
	}})

	if got != "INV-900" {
		t.Fatalf("memo = %q, want INV-900", got)
	}
}

func TestHandleParsedTransferSkipsZeroAmountTransfer(t *testing.T) {
	listener := &RpcListener{
		chain:  chainpkg.NewSolanaChain(),
		events: make(chan interface{}, 1),
	}

	cases := []struct {
		name        string
		instruction Instruction
	}{
		{
			name: "system transfer",
			instruction: Instruction{
				Program: "system",
				Parsed:  json.RawMessage(`{"type":"transfer","info":{"source":"source","destination":"destination","lamports":"0"}}`),
			},
		},
		{
			name: "spl transfer",
			instruction: Instruction{
				Program: "spl-token",
				Parsed:  json.RawMessage(`{"type":"transfer","info":{"source":"source","destination":"destination","amount":"0","mint":"mint"}}`),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handled, err := listener.handleParsedTransfer("1", "block-hash", "tx", "ix:0", asset.NewSOL(constants.Solana), "confirmed", "", tc.instruction, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !handled {
				t.Fatal("zero amount parsed transfer should be handled without falling back to program_call")
			}
			select {
			case event := <-listener.events:
				t.Fatalf("unexpected zero amount transfer event: %#v", event)
			default:
			}
		})
	}
}

func TestHandleParsedTransferRejectsMalformedParsedInstruction(t *testing.T) {
	listener := &RpcListener{
		chain:  chainpkg.NewSolanaChain(),
		events: make(chan interface{}, 1),
	}

	handled, err := listener.handleParsedTransfer(
		"1",
		"block-hash",
		"tx",
		"ix:0",
		asset.NewSOL(constants.Solana),
		"confirmed",
		"",
		Instruction{Program: "system", Parsed: json.RawMessage(`{"type":`)},
		nil,
	)
	if err == nil {
		t.Fatal("malformed parsed instruction returned nil error")
	}
	if handled {
		t.Fatal("malformed parsed instruction must not be marked handled")
	}
}

func TestHandleParsedTransferSkipsScalarParsedInstruction(t *testing.T) {
	listener := &RpcListener{
		chain:  chainpkg.NewSolanaChain(),
		events: make(chan interface{}, 1),
	}

	handled, err := listener.handleParsedTransfer(
		"1",
		"block-hash",
		"tx",
		"ix:0",
		asset.NewSOL(constants.Solana),
		"confirmed",
		"",
		Instruction{Program: "spl-memo", Parsed: json.RawMessage(`"ORDER-42"`)},
		nil,
	)
	if err != nil {
		t.Fatalf("scalar parsed instruction returned error: %v", err)
	}
	if handled {
		t.Fatal("scalar parsed instruction must not be marked as a transfer")
	}
}

func TestHandleParsedTransferSupportsToken2022TransferChecked(t *testing.T) {
	const mint = "Token2022Mint111111111111111111111111111111"
	const token2022Program = "TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb"

	registry := asset.NewRegistry()
	registry.Register(asset.NewSPL(constants.Solana, mint, "USDC", "USD Coin", 6))
	listener := &RpcListener{
		chain:    chainpkg.NewSolanaChain(),
		registry: registry,
		events:   make(chan interface{}, 1),
	}

	handled, err := listener.handleParsedTransfer(
		"123",
		"block-hash",
		"tx-sig",
		"ix:1",
		asset.NewSOL(constants.Solana),
		"confirmed",
		"ORDER-42",
		Instruction{
			Program:   "spl-token-2022",
			ProgramID: token2022Program,
			Parsed: json.RawMessage(`{
				"type":"transferChecked",
				"info":{
					"source":"source-token-account",
					"destination":"destination-token-account",
					"mint":"Token2022Mint111111111111111111111111111111",
					"tokenAmount":{"amount":"2500000","decimals":6}
				}
			}`),
		},
		map[string]tokenAccountMetadata{
			"source-token-account": {
				Owner: "source-owner", Mint: mint, ProgramID: token2022Program, Decimals: 6, HasDecimals: true,
			},
			"destination-token-account": {
				Owner: "destination-owner", Mint: mint, ProgramID: token2022Program, Decimals: 6, HasDecimals: true,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatal("Token-2022 transferChecked should be handled as an SPL transfer")
	}

	raw := <-listener.events
	event, ok := raw.(dispatcher.Event)
	if !ok {
		t.Fatalf("event type = %T, want dispatcher.Event", raw)
	}
	if event.Type != "spl_transfer" {
		t.Fatalf("event type = %q, want spl_transfer", event.Type)
	}
	tx := event.Transaction
	if tx == nil {
		t.Fatal("event transaction is nil")
	}
	if *tx.Symbol != "USDC" || tx.Decimals != 6 || *tx.Token != mint || *tx.Amount != "2500000" {
		t.Fatalf("transaction = %#v", tx)
	}
	if tx.From == nil || *tx.From != "source-owner" || tx.To == nil || *tx.To != "destination-owner" {
		t.Fatalf("owners = from:%#v to:%#v", tx.From, tx.To)
	}
	if tx.Memo == nil || *tx.Memo != "ORDER-42" {
		t.Fatalf("memo = %#v, want ORDER-42", tx.Memo)
	}
}

func TestHandleParsedTransferResolvesOrdinaryTransferMintAndOwners(t *testing.T) {
	const mint = "OrdinaryTransferMint111111111111111111111111"
	registry := asset.NewRegistry()
	registry.Register(asset.NewSPL(constants.Solana, mint, "USDT", "Tether", 6))
	listener := &RpcListener{
		chain:    chainpkg.NewSolanaChain(),
		registry: registry,
		events:   make(chan interface{}, 1),
	}

	handled, err := listener.handleParsedTransfer(
		"123",
		"block-hash",
		"tx-sig",
		"ix:2",
		asset.NewSOL(constants.Solana),
		"confirmed",
		"",
		Instruction{
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
		map[string]tokenAccountMetadata{
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
	if !handled {
		t.Fatal("ordinary SPL transfer should be handled")
	}

	raw := <-listener.events
	event := raw.(dispatcher.Event)
	if event.Transaction == nil || event.Transaction.Token == nil || *event.Transaction.Token != mint {
		t.Fatalf("transaction token = %#v, want metadata-derived mint", event.Transaction)
	}
	if *event.Transaction.From != "source-owner" || *event.Transaction.To != "destination-owner" {
		t.Fatalf("transaction owners = %#v", event.Transaction)
	}
}

func TestHandleParsedTransferFailsClosedWithoutCompleteTokenOwners(t *testing.T) {
	const mint = "MissingOwnerMint11111111111111111111111111111"
	registry := asset.NewRegistry()
	registry.Register(asset.NewSPL(constants.Solana, mint, "USDC", "USD Coin", 6))
	listener := &RpcListener{
		chain:    chainpkg.NewSolanaChain(),
		registry: registry,
		events:   make(chan interface{}, 1),
	}

	handled, err := listener.handleParsedTransfer(
		"123",
		"block-hash",
		"tx-sig",
		"ix:3",
		asset.NewSOL(constants.Solana),
		"confirmed",
		"",
		Instruction{Program: "spl-token", Parsed: json.RawMessage(`{
			"type":"transfer",
			"info":{"source":"source-token-account","destination":"destination-token-account","amount":"75"}
		}`)},
		map[string]tokenAccountMetadata{
			"source-token-account": {
				Owner: "source-owner", Mint: mint, Decimals: 6, HasDecimals: true,
			},
		},
	)
	if err == nil {
		t.Fatal("incomplete registered SPL transfer metadata must hold the slot checkpoint")
	}
	if !handled {
		t.Fatal("unresolved SPL transfer must be handled without program_call fallback")
	}
	select {
	case event := <-listener.events:
		t.Fatalf("unexpected event with missing destination owner: %#v", event)
	default:
	}
}

func TestTokenAccountMetadataByAddressMergesPreAndPostStrictly(t *testing.T) {
	rawKeys := []json.RawMessage{
		json.RawMessage(`{"pubkey":"source-token-account","signer":false}`),
		json.RawMessage(`{"pubkey":"destination-token-account","signer":false}`),
	}
	meta := TxMeta{
		PreTokenBalances: []TokenBalance{{
			AccountIndex: 0,
			Mint:         "mint",
			Owner:        "source-owner",
			ProgramID:    "token-program",
			UITokenAmount: &UITokenAmount{
				Amount: "100", Decimals: 6,
			},
		}},
		PostTokenBalances: []TokenBalance{{
			AccountIndex: 1,
			Mint:         "mint",
			Owner:        "destination-owner",
			ProgramID:    "token-program",
			UITokenAmount: &UITokenAmount{
				Amount: "100", Decimals: 6,
			},
		}},
	}

	accounts, warnings := tokenAccountMetadataByAddress(rawKeys, meta)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v", warnings)
	}
	if got := accounts["source-token-account"]; got.Owner != "source-owner" || got.Mint != "mint" || got.Decimals != 6 {
		t.Fatalf("source metadata = %#v", got)
	}
	if got := accounts["destination-token-account"]; got.Owner != "destination-owner" || got.Mint != "mint" || got.Decimals != 6 {
		t.Fatalf("destination metadata = %#v", got)
	}

	meta.PostTokenBalances = append(meta.PostTokenBalances, TokenBalance{
		AccountIndex: 0,
		Mint:         "different-mint",
		Owner:        "source-owner",
		UITokenAmount: &UITokenAmount{
			Amount: "100", Decimals: 6,
		},
	})
	accounts, warnings = tokenAccountMetadataByAddress(rawKeys, meta)
	if _, exists := accounts["source-token-account"]; exists {
		t.Fatal("conflicting source metadata must be removed")
	}
	if len(warnings) == 0 {
		t.Fatal("conflicting metadata should produce a warning for logging")
	}
}

func TestProcessSlotReturnsErrorOnNullBlock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := readTestJSONRPCRequest(t, r)
		writeTestJSONRPCResponse(t, w, request.ID, nil, nil)
	}))
	defer server.Close()

	chain := chainpkg.NewSolanaChain()
	chain.RPCHttp = []string{server.URL}
	listener := NewRpcListener(chain, asset.NewRegistry(), nil, nil, nil)
	listener.client = server.Client()

	err := listener.processSlot(context.Background(), 100)
	if err == nil || !strings.Contains(err.Error(), "null result") {
		t.Fatalf("processSlot err = %v, want null block error", err)
	}
}

func TestProcessSlotObservesCanonicalSlotBeforeDispatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := readTestJSONRPCRequest(t, r)
		writeTestJSONRPCResponse(t, w, request.ID, map[string]interface{}{
			"blockhash":         "slot-hash",
			"previousBlockhash": "parent-hash",
			"transactions":      []interface{}{},
		}, nil)
	}))
	defer server.Close()

	chain := chainpkg.NewSolanaChain()
	chain.RPCHttp = []string{server.URL}
	registry := asset.NewRegistry()
	registry.Register(asset.NewSOL(constants.Solana))
	listener := NewRpcListener(chain, registry, nil, nil, nil)
	listener.client = server.Client()

	called := false
	listener.SetCanonicalBlockObserver(func(_ context.Context, chainID constants.ChainID, slot int64, blockHash, parentHash string) error {
		called = true
		if chainID != constants.Solana || slot != 100 || blockHash != "slot-hash" || parentHash != "parent-hash" {
			t.Fatalf("canonical observation chain=%d slot=%d hash=%q parent=%q", chainID, slot, blockHash, parentHash)
		}
		return nil
	})

	if err := listener.processSlot(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("canonical slot observer was not called")
	}

	listener.SetCanonicalBlockObserver(func(context.Context, constants.ChainID, int64, string, string) error {
		return errors.New("canonical store unavailable")
	})
	if err := listener.processSlot(context.Background(), 100); err == nil || !strings.Contains(err.Error(), "canonical store unavailable") {
		t.Fatalf("observer failure = %v, want checkpoint-holding error", err)
	}
}

func TestWriteChainStateDoesNotAdvanceMemoryWhenWriterFails(t *testing.T) {
	state := &models.ChainState{ChainID: constants.Solana, LastProcessedBlock: 100, LastConfirmedBlock: 100}
	listener := &RpcListener{
		chainState: state,
		stateWriter: func(*models.ChainState) error {
			return errors.New("database unavailable")
		},
	}

	if err := listener.writeChainState(101); err == nil {
		t.Fatal("expected checkpoint write error")
	}
	if state.LastProcessedBlock != 100 || state.LastConfirmedBlock != 100 {
		t.Fatalf("in-memory checkpoint advanced after failed write: %#v", state)
	}
}

func TestWriteChainStateStoresRealBlockHashAndPreservesItAcrossSkippedSlot(t *testing.T) {
	state := &models.ChainState{ChainID: constants.Solana, LastProcessedBlock: 99, LastProcessedHash: "slot-hash-99"}
	listener := &RpcListener{
		chainState:          state,
		lastBlockSlot:       100,
		lastBlockHash:       "slot-hash-100",
		lastBlockParentHash: "slot-hash-99",
		stateWriter:         func(*models.ChainState) error { return nil },
	}
	if err := listener.writeChainState(100); err != nil {
		t.Fatal(err)
	}
	if state.LastProcessedBlock != 100 || state.LastProcessedHash != "slot-hash-100" || state.LastProcessedParentHash != "slot-hash-99" || state.ContinuityStatus != listenerconfig.ContinuityStatusOK {
		t.Fatalf("real block checkpoint = %#v", state)
	}
	if err := listener.writeChainState(101); err != nil {
		t.Fatal(err)
	}
	if state.LastProcessedBlock != 101 || state.LastProcessedHash != "slot-hash-100" || state.LastProcessedParentHash != "slot-hash-99" {
		t.Fatalf("skipped slot discarded last real block identity: %#v", state)
	}
}

func TestProcessSlotParentMismatchObservesCanonicalAndPersistsRewind(t *testing.T) {
	t.Setenv("SCANNER_REORG_REWIND_BLOCKS", "2")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := readTestJSONRPCRequest(t, r)
		writeTestJSONRPCResponse(t, w, request.ID, map[string]interface{}{
			"blockhash":         "slot-hash-100-new",
			"previousBlockhash": "different-parent",
			"transactions":      []interface{}{},
		}, nil)
	}))
	defer server.Close()

	chain := chainpkg.NewSolanaChain()
	chain.RPCHttp = []string{server.URL}
	registry := asset.NewRegistry()
	registry.Register(asset.NewSOL(constants.Solana))
	state := &models.ChainState{
		ChainID: constants.Solana, LastProcessedBlock: 99, LastProcessedHash: "slot-hash-99", LastProcessedParentHash: "slot-hash-98",
	}
	var persisted *models.ChainState
	listener := NewRpcListener(chain, registry, state, nil, func(next *models.ChainState) error {
		copy := *next
		persisted = &copy
		return nil
	})
	listener.client = server.Client()
	observed := false
	listener.SetCanonicalBlockObserver(func(_ context.Context, chainID constants.ChainID, slot int64, hash, parent string) error {
		observed = true
		if chainID != constants.Solana || slot != 100 || hash != "slot-hash-100-new" || parent != "different-parent" {
			t.Fatalf("canonical mismatch observation = %d/%d/%s/%s", chainID, slot, hash, parent)
		}
		return nil
	})
	err := listener.processSlot(context.Background(), 100)
	if !errors.Is(err, listenerconfig.ErrParentContinuity) {
		t.Fatalf("error = %v, want parent continuity failure", err)
	}
	if !observed || persisted == nil {
		t.Fatalf("canonical correction/persist missing: observed=%v persisted=%#v", observed, persisted)
	}
	if state.LastProcessedBlock != 97 || state.LastProcessedHash != "" || state.LastProcessedParentHash != "" || state.ContinuityStatus != listenerconfig.ContinuityStatusRollback || state.ContinuityReason == "" {
		t.Fatalf("rewound state = %#v", state)
	}
}

func TestCatchUpDoesNotAdvanceOnNullGetBlocksResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := readTestJSONRPCRequest(t, r)
		switch request.Method {
		case "getSlot":
			writeTestJSONRPCResponse(t, w, request.ID, int64(108), nil)
		case "getBlocks":
			writeTestJSONRPCResponse(t, w, request.ID, nil, nil)
		default:
			http.Error(w, "unexpected method "+request.Method, http.StatusBadRequest)
		}
	}))
	defer server.Close()

	chain := chainpkg.NewSolanaChain()
	chain.RPCHttp = []string{server.URL}
	state := &models.ChainState{ChainID: constants.Solana, LastProcessedBlock: 100, LastConfirmedBlock: 100}
	listener := NewRpcListener(chain, asset.NewRegistry(), state, nil, func(*models.ChainState) error {
		t.Fatal("state writer must not run for a null getBlocks result")
		return nil
	})
	listener.client = server.Client()

	if err := listener.catchUp(); err == nil || !strings.Contains(err.Error(), "null result") {
		t.Fatalf("catchUp err = %v, want null getBlocks error", err)
	}
	if state.LastProcessedBlock != 100 {
		t.Fatalf("checkpoint = %d, want 100", state.LastProcessedBlock)
	}
}

func TestRPCCallRejectsMismatchedResponseID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := readTestJSONRPCRequest(t, r)
		writeTestJSONRPCResponse(t, w, request.ID+1, int64(123), nil)
	}))
	defer server.Close()

	chain := chainpkg.NewSolanaChain()
	chain.RPCHttp = []string{server.URL}
	listener := NewRpcListener(chain, asset.NewRegistry(), nil, nil, nil)
	listener.client = server.Client()

	_, err := listener.latestSlot(context.Background())
	if err == nil || !strings.Contains(err.Error(), "response id mismatch") {
		t.Fatalf("latestSlot err = %v, want response id mismatch", err)
	}
}

func TestBlocksInRangeRecoversSlotOmittedByPartialResponse(t *testing.T) {
	var probedSlots []int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := readTestJSONRPCRequest(t, r)
		switch request.Method {
		case "getBlocks":
			writeTestJSONRPCResponse(t, w, request.ID, []int64{101, 103}, nil)
		case "getBlock":
			var slot int64
			if len(request.Params) == 0 || json.Unmarshal(request.Params[0], &slot) != nil {
				http.Error(w, "missing slot", http.StatusBadRequest)
				return
			}
			probedSlots = append(probedSlots, slot)
			writeTestJSONRPCResponse(t, w, request.ID, map[string]interface{}{"blockhash": "slot-102"}, nil)
		default:
			http.Error(w, "unexpected method "+request.Method, http.StatusBadRequest)
		}
	}))
	defer server.Close()

	chain := chainpkg.NewSolanaChain()
	chain.RPCHttp = []string{server.URL}
	listener := NewRpcListener(chain, asset.NewRegistry(), nil, nil, nil)
	listener.client = server.Client()

	slots, err := listener.blocksInRange(context.Background(), 101, 103)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{101, 102, 103}; !reflect.DeepEqual(slots, want) {
		t.Fatalf("slots = %v, want %v", slots, want)
	}
	if want := []int64{102}; !reflect.DeepEqual(probedSlots, want) {
		t.Fatalf("probed slots = %v, want %v", probedSlots, want)
	}
}

func TestBlocksInRangeAcceptsOnlyExplicitSkippedSlot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := readTestJSONRPCRequest(t, r)
		switch request.Method {
		case "getBlocks":
			writeTestJSONRPCResponse(t, w, request.ID, []int64{}, nil)
		case "getBlock":
			writeTestJSONRPCResponse(t, w, request.ID, nil, &jsonRPCError{Code: -32007, Message: "Slot 101 was skipped"})
		default:
			http.Error(w, "unexpected method "+request.Method, http.StatusBadRequest)
		}
	}))
	defer server.Close()

	chain := chainpkg.NewSolanaChain()
	chain.RPCHttp = []string{server.URL}
	listener := NewRpcListener(chain, asset.NewRegistry(), nil, nil, nil)
	listener.client = server.Client()

	slots, err := listener.blocksInRange(context.Background(), 101, 101)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 0 {
		t.Fatalf("slots = %v, want verified skipped slot", slots)
	}
}

func TestCatchUpDoesNotAdvanceOnEmptyPartialResponseWithUnavailableSlot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := readTestJSONRPCRequest(t, r)
		switch request.Method {
		case "getSlot":
			writeTestJSONRPCResponse(t, w, request.ID, int64(102), nil)
		case "getBlocks":
			writeTestJSONRPCResponse(t, w, request.ID, []int64{}, nil)
		case "getBlock":
			writeTestJSONRPCResponse(t, w, request.ID, nil, &jsonRPCError{Code: -32004, Message: "Block not available for slot 101"})
		default:
			http.Error(w, "unexpected method "+request.Method, http.StatusBadRequest)
		}
	}))
	defer server.Close()

	chain := chainpkg.NewSolanaChain()
	chain.RPCHttp = []string{server.URL}
	state := &models.ChainState{ChainID: constants.Solana, LastProcessedBlock: 100, LastConfirmedBlock: 100}
	listener := NewRpcListener(chain, asset.NewRegistry(), state, nil, func(*models.ChainState) error {
		t.Fatal("state writer must not run while an omitted slot is unavailable")
		return nil
	})
	listener.client = server.Client()

	err := listener.catchUp()
	if err == nil || !strings.Contains(err.Error(), "verify omitted solana slot 101") {
		t.Fatalf("catchUp err = %v, want omitted-slot verification error", err)
	}
	if state.LastProcessedBlock != 100 {
		t.Fatalf("checkpoint = %d, want 100", state.LastProcessedBlock)
	}
}

func TestCatchUpScansLargeBacklogInBatchesWithoutPollDelay(t *testing.T) {
	t.Setenv("SOLANA_SCAN_BATCH_SLOTS", "8")
	t.Setenv("SOLANA_MAX_SLOTS_PER_CATCH_UP", "32")

	getBlocksCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := readTestJSONRPCRequest(t, r)
		switch request.Method {
		case "getSlot":
			writeTestJSONRPCResponse(t, w, request.ID, int64(132), nil)
		case "getBlocks":
			if len(request.Params) < 2 {
				http.Error(w, "missing getBlocks range", http.StatusBadRequest)
				return
			}
			var from, to int64
			if json.Unmarshal(request.Params[0], &from) != nil || json.Unmarshal(request.Params[1], &to) != nil {
				http.Error(w, "invalid getBlocks range", http.StatusBadRequest)
				return
			}
			slots := make([]int64, 0, to-from+1)
			for slot := from; slot <= to; slot++ {
				slots = append(slots, slot)
			}
			getBlocksCalls++
			writeTestJSONRPCResponse(t, w, request.ID, slots, nil)
		case "getBlock":
			var slot int64
			if len(request.Params) == 0 || json.Unmarshal(request.Params[0], &slot) != nil {
				http.Error(w, "missing block slot", http.StatusBadRequest)
				return
			}
			writeTestJSONRPCResponse(t, w, request.ID, map[string]interface{}{
				"blockhash":         "block-hash-" + strconv.FormatInt(slot, 10),
				"previousBlockhash": "block-hash-" + strconv.FormatInt(slot-1, 10),
				"transactions":      []interface{}{},
			}, nil)
		default:
			http.Error(w, "unexpected method "+request.Method, http.StatusBadRequest)
		}
	}))
	defer server.Close()

	chain := chainpkg.NewSolanaChain()
	chain.RPCHttp = []string{server.URL}
	registry := asset.NewRegistry()
	registry.Register(asset.NewSOL(constants.Solana))
	state := &models.ChainState{ChainID: constants.Solana, LastProcessedBlock: 100, LastConfirmedBlock: 100}
	listener := NewRpcListener(chain, registry, state, nil, func(*models.ChainState) error { return nil })
	listener.client = server.Client()

	if err := listener.catchUp(); err != nil {
		t.Fatal(err)
	}
	if state.LastProcessedBlock != 132 {
		t.Fatalf("checkpoint = %d, want 132", state.LastProcessedBlock)
	}
	if getBlocksCalls != 4 {
		t.Fatalf("getBlocks calls = %d, want 4 batches", getBlocksCalls)
	}
	if listener.backlogRemaining {
		t.Fatal("backlog should be drained in one catch-up pass")
	}
}

func TestProcessSlotUsesSlotAsTransactionBlock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := readTestJSONRPCRequest(t, r)
		writeTestJSONRPCResponse(t, w, request.ID, json.RawMessage(`{
				"blockhash": "block-hash",
				"previousBlockhash": "parent-hash",
				"blockHeight": 90,
				"transactions": [{
					"meta": {
						"err": null,
						"innerInstructions": [],
						"preBalances": [100, 0],
						"postBalances": [70, 25],
						"preTokenBalances": [],
						"postTokenBalances": []
					},
					"transaction": {
						"signatures": ["tx-signature"],
						"message": {
							"accountKeys": [{"pubkey":"source","signer":true},{"pubkey":"destination","signer":false}],
							"instructions": [{
								"program": "system",
								"programId": "11111111111111111111111111111111",
								"parsed": {"type":"transfer","info":{"source":"source","destination":"destination","lamports":25}}
							}]
						}
					}
				}]
			}`), nil)
	}))
	defer server.Close()

	chain := chainpkg.NewSolanaChain()
	chain.RPCHttp = []string{server.URL}
	registry := asset.NewRegistry()
	registry.Register(asset.NewSOL(constants.Solana))
	listener := NewRpcListener(chain, registry, nil, nil, nil)
	listener.client = server.Client()

	if err := listener.processSlot(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	event := (<-listener.events).(dispatcher.Event)
	if event.Transaction == nil || event.Transaction.Block == nil || *event.Transaction.Block != "100" {
		t.Fatalf("transaction block = %#v, want slot 100", event.Transaction)
	}
	if event.Transaction.Amount == nil || *event.Transaction.Amount != "25" ||
		event.Transaction.LogIndex == nil || *event.Transaction.LogIndex != "balance:1" {
		t.Fatalf("native balance event = %#v, want authoritative balance:1 delta 25", event.Transaction)
	}
}

func TestHandleTransactionUsesNativeBalanceDeltaWithoutParsedDoubleCredit(t *testing.T) {
	listener := &RpcListener{
		chain:  chainpkg.NewSolanaChain(),
		events: make(chan interface{}, 4),
	}
	tx := BlockTx{
		Meta: &TxMeta{
			Err:          json.RawMessage(`null`),
			PreBalances:  []uint64{1_000, 10},
			PostBalances: []uint64{870, 130},
		},
		Transaction: Transaction{
			Signatures: []string{"tx-signature"},
			Message: Message{
				AccountKeys: []json.RawMessage{
					json.RawMessage(`{"pubkey":"source","signer":true,"source":"transaction"}`),
					json.RawMessage(`{"pubkey":"merchant","signer":false,"source":"transaction"}`),
				},
				Instructions: []Instruction{
					{
						Program: "system",
						Parsed:  json.RawMessage(`{"type":"transfer","info":{"source":"source","destination":"merchant","lamports":25}}`),
					},
					{
						Program: "system",
						Parsed:  json.RawMessage(`{"type":"transferWithSeed","info":{"source":"source","sourceBase":"source","destination":"merchant","lamports":95}}`),
					},
				},
			},
		},
	}

	if err := listener.handleTransaction("100", "block-hash", 0, asset.NewSOL(constants.Solana), tx); err != nil {
		t.Fatal(err)
	}
	if got := len(listener.events); got != 1 {
		t.Fatalf("native event count = %d, want one net balance increase", got)
	}
	event := (<-listener.events).(dispatcher.Event)
	if event.Type != "sol_transfer" || event.Transaction == nil {
		t.Fatalf("event = %#v, want sol_transfer", event)
	}
	if event.Transaction.To == nil || *event.Transaction.To != "merchant" ||
		event.Transaction.From == nil || *event.Transaction.From != "source" ||
		len(event.Transaction.FromAddresses) != 1 || event.Transaction.FromAddresses[0] != "source" ||
		event.Transaction.Amount == nil || *event.Transaction.Amount != "120" ||
		event.Transaction.LogIndex == nil || *event.Transaction.LogIndex != "balance:1" {
		t.Fatalf("native balance event = %#v", event.Transaction)
	}

	// A replay derives the same identity; downstream durable idempotency can
	// safely collapse it even if a crash happened before checkpoint storage.
	if err := listener.handleTransaction("100", "block-hash", 0, asset.NewSOL(constants.Solana), tx); err != nil {
		t.Fatal(err)
	}
	replayed := (<-listener.events).(dispatcher.Event)
	if replayed.Transaction == nil || replayed.Transaction.LogIndex == nil ||
		*replayed.Transaction.LogIndex != "balance:1" {
		t.Fatalf("replayed identity = %#v, want balance:1", replayed.Transaction)
	}
}

func TestHandleTransactionIncludesLoadedAccountKeysInNativeBalanceDelta(t *testing.T) {
	listener := &RpcListener{
		chain:  chainpkg.NewSolanaChain(),
		events: make(chan interface{}, 1),
	}
	tx := BlockTx{
		Meta: &TxMeta{
			Err:             json.RawMessage(`null`),
			PreBalances:     []uint64{1_000, 0},
			PostBalances:    []uint64{895, 100},
			LoadedAddresses: LoadedAddresses{Writable: []string{"loaded-merchant"}},
		},
		Transaction: Transaction{
			Signatures: []string{"versioned-tx-signature"},
			Message: Message{
				// Raw-message providers return only static keys here. The lookup-table
				// destination must be appended in consensus account-index order.
				AccountKeys: []json.RawMessage{json.RawMessage(`"static-signer"`)},
			},
		},
	}

	if err := listener.handleTransaction("101", "block-hash", 0, asset.NewSOL(constants.Solana), tx); err != nil {
		t.Fatal(err)
	}
	event := (<-listener.events).(dispatcher.Event)
	if event.Transaction == nil || event.Transaction.To == nil || *event.Transaction.To != "loaded-merchant" ||
		event.Transaction.Amount == nil || *event.Transaction.Amount != "100" ||
		event.Transaction.LogIndex == nil || *event.Transaction.LogIndex != "balance:1" {
		t.Fatalf("loaded-address native event = %#v", event.Transaction)
	}
}

func TestTransactionAccountKeysAcceptsParsedLoadedTailAndRejectsProviderMismatch(t *testing.T) {
	rawKeys := []json.RawMessage{
		json.RawMessage(`{"pubkey":"static-signer","signer":true,"source":"transaction"}`),
		json.RawMessage(`{"pubkey":"loaded-write","signer":false,"source":"lookupTable"}`),
		json.RawMessage(`{"pubkey":"loaded-read","signer":false,"source":"lookupTable"}`),
	}
	loaded := LoadedAddresses{Writable: []string{"loaded-write"}, Readonly: []string{"loaded-read"}}
	keys, err := transactionAccountKeys(rawKeys, loaded, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{keys[0].Pubkey, keys[1].Pubkey, keys[2].Pubkey}; !reflect.DeepEqual(got, []string{"static-signer", "loaded-write", "loaded-read"}) {
		t.Fatalf("account keys = %v", got)
	}

	loaded.Readonly[0] = "different-loaded-read"
	if _, err := transactionAccountKeys(rawKeys, loaded, 3); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("loaded-address mismatch error = %v", err)
	}
}

func TestHandleTransactionFailsClosedOnMissingOrMismatchedBalanceMetadata(t *testing.T) {
	tests := []struct {
		name string
		meta *TxMeta
	}{
		{
			name: "missing pre balances",
			meta: &TxMeta{Err: json.RawMessage(`null`), PostBalances: []uint64{1}},
		},
		{
			name: "missing post balances",
			meta: &TxMeta{Err: json.RawMessage(`null`), PreBalances: []uint64{1}},
		},
		{
			name: "pre post mismatch",
			meta: &TxMeta{Err: json.RawMessage(`null`), PreBalances: []uint64{1}, PostBalances: []uint64{1, 2}},
		},
		{
			name: "account balance mismatch",
			meta: &TxMeta{Err: json.RawMessage(`null`), PreBalances: []uint64{1, 0}, PostBalances: []uint64{0, 1}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			listener := &RpcListener{
				chain:  chainpkg.NewSolanaChain(),
				events: make(chan interface{}, 1),
			}
			tx := BlockTx{
				Meta: tc.meta,
				Transaction: Transaction{
					Signatures: []string{"tx-signature"},
					Message:    Message{AccountKeys: []json.RawMessage{json.RawMessage(`"only-account"`)}},
				},
			}
			if err := listener.handleTransaction("100", "block-hash", 0, asset.NewSOL(constants.Solana), tx); err == nil {
				t.Fatal("invalid balance metadata must hold the slot checkpoint")
			}
			if got := len(listener.events); got != 0 {
				t.Fatalf("events = %d, want none after validation failure", got)
			}
		})
	}
}

func TestHandleTransactionDoesNotEmitFailedNativeBalanceIncrease(t *testing.T) {
	listener := &RpcListener{
		chain:  chainpkg.NewSolanaChain(),
		events: make(chan interface{}, 1),
	}
	tx := BlockTx{
		Meta: &TxMeta{
			Err:          json.RawMessage(`{"InstructionError":[0,"Custom"]}`),
			PreBalances:  []uint64{100, 0},
			PostBalances: []uint64{90, 10},
		},
		Transaction: Transaction{
			Signatures: []string{"failed-tx"},
			Message: Message{AccountKeys: []json.RawMessage{
				json.RawMessage(`{"pubkey":"source","signer":true}`),
				json.RawMessage(`{"pubkey":"merchant","signer":false}`),
			}},
		},
	}
	if err := listener.handleTransaction("100", "block-hash", 0, asset.NewSOL(constants.Solana), tx); err != nil {
		t.Fatal(err)
	}
	if got := len(listener.events); got != 0 {
		t.Fatalf("failed native event count = %d, want zero", got)
	}
}

func TestProcessSlotRejectsNullTransactionMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := readTestJSONRPCRequest(t, r)
		writeTestJSONRPCResponse(t, w, request.ID, json.RawMessage(`{"blockhash":"block-hash","previousBlockhash":"parent-hash","transactions":[{"meta":null,"transaction":{"signatures":["tx-signature"],"message":{"accountKeys":[],"instructions":[]}}}]}`), nil)
	}))
	defer server.Close()

	chain := chainpkg.NewSolanaChain()
	chain.RPCHttp = []string{server.URL}
	registry := asset.NewRegistry()
	registry.Register(asset.NewSOL(constants.Solana))
	listener := NewRpcListener(chain, registry, nil, nil, nil)
	listener.client = server.Client()

	if err := listener.processSlot(context.Background(), 100); err == nil || !strings.Contains(err.Error(), "null metadata") {
		t.Fatalf("processSlot err = %v, want null metadata error", err)
	}
}

func TestProcessSlotRejectsMetadataWithoutExecutionStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := readTestJSONRPCRequest(t, r)
		writeTestJSONRPCResponse(t, w, request.ID, json.RawMessage(`{"blockhash":"block-hash","previousBlockhash":"parent-hash","transactions":[{"meta":{},"transaction":{"signatures":["tx-signature"],"message":{"accountKeys":[],"instructions":[]}}}]}`), nil)
	}))
	defer server.Close()

	chain := chainpkg.NewSolanaChain()
	chain.RPCHttp = []string{server.URL}
	registry := asset.NewRegistry()
	registry.Register(asset.NewSOL(constants.Solana))
	listener := NewRpcListener(chain, registry, nil, nil, nil)
	listener.client = server.Client()

	if err := listener.processSlot(context.Background(), 100); err == nil || !strings.Contains(err.Error(), "execution status") {
		t.Fatalf("processSlot err = %v, want missing execution status error", err)
	}
}
