package txrescan

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	configurations "core/application/configuration"
	"core/asset"
	chainpkg "core/blockchain/chains"
	"core/constants"
	"core/models"
	"core/repositories"
	"core/types"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
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

func TestSolanaInstructionEventSkipsZeroAmountTransfer(t *testing.T) {
	svc := &Service{Registry: asset.NewRegistry()}
	native := asset.NewSOL(constants.Solana)

	cases := []struct {
		name        string
		instruction solanaInstruction
	}{
		{
			name: "system transfer",
			instruction: solanaInstruction{
				Program: "system",
				Parsed:  json.RawMessage(`{"type":"transfer","info":{"source":"source","destination":"destination","lamports":"0"}}`),
			},
		},
		{
			name: "spl transfer",
			instruction: solanaInstruction{
				Program: "spl-token",
				Parsed:  json.RawMessage(`{"type":"transfer","info":{"source":"source","destination":"destination","amount":"0","mint":"mint"}}`),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := svc.solanaInstructionEvent(constants.Solana, "1", "block-hash", "tx", "ix:0", native, "confirmed", "signer", "", tc.instruction, nil)
			if len(events) != 0 {
				t.Fatalf("events = %#v, want none for zero amount transfer", events)
			}
		})
	}
}

func TestSolanaInstructionEventResolvesOrdinaryTransferMintAndOwners(t *testing.T) {
	const mint = "OrdinaryTransferMint111111111111111111111111"
	registry := asset.NewRegistry()
	registry.Register(asset.NewSPL(constants.Solana, mint, "USDT", "Tether", 6))
	svc := &Service{Registry: registry}

	events := svc.solanaInstructionEvent(
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
			events := svc.solanaInstructionEvent(
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
			if len(events) != 0 {
				t.Fatalf("events = %#v, want fail-closed skip", events)
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
			result = `{"hash":"` + txHash + `","from":"0x0000000000000000000000000000000000000001","to":"0x0000000000000000000000000000000000000002","value":"0xde0b6b3a7640000","input":"0x"}`
		case "eth_getTransactionReceipt":
			result = `{"transactionHash":"` + txHash + `","transactionIndex":"0x0","blockNumber":"0x64","blockHash":"0xblock","status":"0x1","gasUsed":"0x5208","effectiveGasPrice":"0x1","logs":[]}`
		case "eth_blockNumber":
			result = `"0x6f"`
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
}

func (f fakeHistoricalRangeScanner) ScanBlock(ctx context.Context, chainID constants.ChainID, blockNumber int64) ([]HistoricalEvent, error) {
	out := make([]HistoricalEvent, len(f.eventsByBlock[blockNumber]))
	copy(out, f.eventsByBlock[blockNumber])
	return out, nil
}

func TestReplayHistoricalRangeRecordsFactsIdempotently(t *testing.T) {
	db := openTxRescanPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.ChainFact{}); err != nil {
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
		ChainFactRepo: repositories.NewChainFactRepo(db),
		WalletRepo:    repositories.NewWalletRepo(repositories.NewDomainRepo(repositories.NewMerchantRepo(db, nil))),
		Registry:      registry,
	}
	hash := "0x" + strings.ReplaceAll(uuid.NewString(), "-", "")
	unownedHash := "0x" + strings.ReplaceAll(uuid.NewString(), "-", "")
	unknownTokenHash := "0x" + strings.ReplaceAll(uuid.NewString(), "-", "")
	block := "100"
	logIndex := "tx:0"
	event := HistoricalEvent{
		Type: "native_transfer",
		Tx: types.TransactionParam{
			Context:  context.Background(),
			ChainID:  constants.Ethereum,
			Symbol:   stringPtr("ETH"),
			Decimals: 18,
			Hash:     &hash,
			Block:    &block,
			From:     stringPtr("0xfrom"),
			To:       stringPtr(wallet.EthereumAddress),
			Amount:   stringPtr("1"),
			LogIndex: &logIndex,
			Status:   stringPtr(models.TransactionStatusConfirmed),
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
			_, _ = w.Write([]byte(`{"blockNumber": 123456, "receipt": {"result": "SUCCESS"}}`))
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
			_, _ = w.Write([]byte(`{"id":"` + txHash + `","fee":1000000,"blockNumber":83954659,"blockTimeStamp":1782533604000,"receipt":{"net_usage":274}}`))
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
			_, _ = w.Write([]byte(`{"blockNumber": 123456, "receipt": {"result": "SUCCESS"}}`))
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
