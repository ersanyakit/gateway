package solana

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"core/asset"
	chainpkg "core/blockchain/chains"
	"core/constants"
	"core/workers/dispatcher"
)

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
	if err != nil {
		t.Fatal(err)
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
		_ = json.NewEncoder(w).Encode(map[string]any{"result": nil, "error": nil})
	}))
	defer server.Close()

	chain := chainpkg.NewSolanaChain()
	chain.RPCHttp = []string{server.URL}
	listener := NewRpcListener(chain, asset.NewRegistry(), nil, nil, nil)
	listener.client = server.Client()

	err := listener.processSlot(context.Background(), 100)
	if err == nil || !strings.Contains(err.Error(), "empty block result for solana slot 100") {
		t.Fatalf("processSlot err = %v, want empty block error", err)
	}
}
