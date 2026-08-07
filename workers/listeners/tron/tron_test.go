package tron

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"core/asset"
	chainpkg "core/blockchain/chains"
	"core/constants"
	"core/models"
	"core/types"
	"core/workers/dispatcher"
	listenerconfig "core/workers/listeners"

	goproto "github.com/golang/protobuf/proto"
	anypb "github.com/golang/protobuf/ptypes/any"
	"github.com/okx/go-wallet-sdk/coins/tron/pb"
)

func TestLatestBlockNumberWithoutClientReturnsError(t *testing.T) {
	listener := &RpcListener{}

	_, err := listener.latestBlockNumber(context.Background())
	if !errors.Is(err, errTronClientNotConnected) {
		t.Fatalf("expected not connected error, got %v", err)
	}
}

func TestWalletClientNilReceiverReturnsError(t *testing.T) {
	var client *walletClient

	_, err := client.getNowBlock(context.Background())
	if !errors.Is(err, errTronClientNotConnected) {
		t.Fatalf("expected not connected error, got %v", err)
	}
}

func TestStartFailsBeforeConnectWhenProductionInternalSourceIsNotAttested(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("TRON_INTERNAL_TX_SOURCE_COMPLETE", "")
	listener := &RpcListener{}
	err := listener.Start()
	if err == nil || !strings.Contains(err.Error(), "TRON_INTERNAL_TX_SOURCE_COMPLETE=true") {
		t.Fatalf("Start error = %v, want production internal-source gate", err)
	}
	if listener.running || listener.conn != nil || listener.client != nil {
		t.Fatalf("listener mutated before startup config gate: %#v", listener)
	}
}

func TestProcessBlockHoldsCheckpointWhenTransactionInfoUnavailable(t *testing.T) {
	registry := asset.NewRegistry()
	registry.Register(asset.NewTRX(constants.TRON))

	owner := []byte{0x41, 0x63, 0xd0, 0x90, 0xb2, 0x10, 0x1f, 0x12, 0x5f, 0x65, 0xe8, 0xfa, 0xe5, 0xb9, 0x74, 0x4d, 0x0e, 0x74, 0xeb, 0x87, 0x46}
	to := []byte{0x41, 0x07, 0xcb, 0x66, 0xbc, 0x50, 0xd0, 0x9c, 0x78, 0x4a, 0x84, 0x3a, 0x6f, 0x2a, 0x0d, 0x94, 0x29, 0x95, 0xfa, 0xcb, 0x92}
	tx := tronNativeTransferTx(t, owner, to, 18_500_000)
	tx.RawData.Data = []byte("ORDER-42")

	listener := &RpcListener{
		chain:    chainpkg.NewTronChain(),
		registry: registry,
		client: fakeTronWalletClient{
			block: &pb.Block{
				BlockHeader:  &pb.BlockHeader{RawData: &pb.BlockHeaderRaw{Number: 100, ParentHash: tronTestHash(0x63)}},
				Transactions: []*pb.Transaction{tx},
			},
			infoErr: errors.New("transaction info missing"),
		},
		events: make(chan interface{}, 1),
	}

	err := listener.processBlock(context.Background(), 100)
	if err == nil || !strings.Contains(err.Error(), "checkpoint held to preserve TRC20 logs") {
		t.Fatalf("processBlock err = %v, want checkpoint-held transaction info error", err)
	}

	select {
	case event := <-listener.events:
		t.Fatalf("unexpected partial TRON event without transaction info: %#v", event)
	default:
	}
}

func TestProcessBlockFallsBackToHTTPTransactionInfo(t *testing.T) {
	t.Setenv("TRON_GRPC_ENDPOINTS", "bad:50051")

	token := tronTestAddress(0xaa)
	from := tronTestAddress(0x01)
	to := tronTestAddress(0x02)
	tokenID := tronAddress(token)

	registry := asset.NewRegistry()
	registry.Register(asset.NewTRX(constants.TRON))
	registry.Register(asset.NewDeploymentAsset(
		asset.AssetDefinition{Symbol: "USDT", Name: "Tether", Decimals: 6},
		asset.Deployment{ChainID: constants.TRON, Address: tokenID, Decimals: 6, Type: asset.AssetTRC20, Enabled: true},
	))

	tx := tronNativeTransferTx(t, from, to, 1)
	txID, err := tronTxID(tx)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wallet/gettransactioninfobyid" {
			http.NotFound(w, r)
			return
		}
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["value"] != txID {
			t.Fatalf("tx id = %q, want %q", payload["value"], txID)
		}
		_, _ = fmt.Fprintf(w, `{
			"id":%q,
			"blockNumber":100,
			"receipt":{"result":"SUCCESS"},
			"log":[{
				"address":%q,
				"topics":[%q,%q,%q],
				"data":%q
			}]
		}`,
			txID,
			hex.EncodeToString(token),
			hex.EncodeToString(transferEventHash),
			hex.EncodeToString(tronTopicAddress(from)),
			hex.EncodeToString(tronTopicAddress(to)),
			fmt.Sprintf("%064x", 12345),
		)
	}))
	defer server.Close()
	t.Setenv("TRON_HTTP_ENDPOINTS", server.URL)

	listener := &RpcListener{
		chain:      chainpkg.NewTronChain(),
		registry:   registry,
		client:     fakeTronWalletClient{block: &pb.Block{BlockHeader: &pb.BlockHeader{RawData: &pb.BlockHeaderRaw{Number: 100, ParentHash: tronTestHash(0x63)}}, Transactions: []*pb.Transaction{tx}}, infoErr: context.DeadlineExceeded},
		httpClient: server.Client(),
		endpoint:   "bad:50051",
		events:     make(chan interface{}, 4),
	}
	listener.reconnectFunc = func() error { return nil }

	if err := listener.processBlock(context.Background(), 100); err != nil {
		t.Fatal(err)
	}

	var tokenEvent *types.TransactionParam
	for i := 0; i < 2; i++ {
		raw := <-listener.events
		event, ok := raw.(dispatcher.Event)
		if !ok {
			t.Fatalf("event type = %T, want dispatcher.Event", raw)
		}
		if event.Type == "token_transfer" {
			tokenEvent = event.Transaction
		}
	}
	if tokenEvent == nil {
		t.Fatal("token_transfer event was not emitted from HTTP transaction info fallback")
	}
	if *tokenEvent.Token != tokenID || *tokenEvent.Amount != "12345" || *tokenEvent.From != tronAddress(from) || *tokenEvent.To != tronAddress(to) {
		t.Fatalf("token event = %#v", tokenEvent)
	}
	if tokenEvent.ParentHash == nil || *tokenEvent.ParentHash != hex.EncodeToString(tronTestHash(0x63)) {
		t.Fatalf("token event parent hash = %#v", tokenEvent.ParentHash)
	}
}

func TestProcessBlockCompletesTransactionInfoMissingFromSuccessfulGRPCList(t *testing.T) {
	token := tronTestAddress(0xaa)
	from := tronTestAddress(0x01)
	to := tronTestAddress(0x02)
	tokenID := tronAddress(token)

	registry := asset.NewRegistry()
	registry.Register(asset.NewTRX(constants.TRON))
	registry.Register(asset.NewDeploymentAsset(
		asset.AssetDefinition{Symbol: "USDT", Name: "Tether", Decimals: 6},
		asset.Deployment{ChainID: constants.TRON, Address: tokenID, Decimals: 6, Type: asset.AssetTRC20, Enabled: true},
	))
	tx := tronNativeTransferTx(t, from, to, 1)
	txID, err := tronTxID(tx)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{
			"id":%q,
			"blockNumber":100,
			"receipt":{"result":"SUCCESS"},
			"log":[{"address":%q,"topics":[%q,%q,%q],"data":%q}]
		}`,
			txID,
			hex.EncodeToString(token),
			hex.EncodeToString(transferEventHash),
			hex.EncodeToString(tronTopicAddress(from)),
			hex.EncodeToString(tronTopicAddress(to)),
			fmt.Sprintf("%064x", 12345),
		)
	}))
	defer server.Close()
	t.Setenv("TRON_HTTP_ENDPOINTS", server.URL)

	listener := &RpcListener{
		chain:      chainpkg.NewTronChain(),
		registry:   registry,
		client:     fakeTronWalletClient{block: &pb.Block{BlockHeader: &pb.BlockHeader{RawData: &pb.BlockHeaderRaw{Number: 100, ParentHash: tronTestHash(0x63)}}, Transactions: []*pb.Transaction{tx}}, info: &transactionInfoList{}},
		httpClient: server.Client(),
		events:     make(chan interface{}, 4),
	}
	if err := listener.processBlock(context.Background(), 100); err != nil {
		t.Fatal(err)
	}

	foundToken := false
	for i := 0; i < 2; i++ {
		event := (<-listener.events).(dispatcher.Event)
		if event.Type == "token_transfer" {
			foundToken = true
		}
	}
	if !foundToken {
		t.Fatal("missing gRPC transaction info was not completed through HTTP")
	}
}

func TestProcessBlockFailsClosedWhenMissingTransactionInfoCannotBeCompleted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	t.Setenv("TRON_HTTP_ENDPOINTS", server.URL)

	registry := asset.NewRegistry()
	registry.Register(asset.NewTRX(constants.TRON))
	tx := tronNativeTransferTx(t, tronTestAddress(0x01), tronTestAddress(0x02), 1)
	listener := &RpcListener{
		chain:      chainpkg.NewTronChain(),
		registry:   registry,
		client:     fakeTronWalletClient{block: &pb.Block{BlockHeader: &pb.BlockHeader{RawData: &pb.BlockHeaderRaw{Number: 100, ParentHash: tronTestHash(0x63)}}, Transactions: []*pb.Transaction{tx}}, info: &transactionInfoList{}},
		httpClient: server.Client(),
		events:     make(chan interface{}, 1),
	}

	err := listener.processBlock(context.Background(), 100)
	if err == nil || !strings.Contains(err.Error(), "HTTP completion failed") {
		t.Fatalf("processBlock err = %v, want HTTP completion failure", err)
	}
	select {
	case event := <-listener.events:
		t.Fatalf("partial block event emitted before receipt completeness: %#v", event)
	default:
	}
}

func TestProcessBlockRejectsWrongProviderBlockHeight(t *testing.T) {
	registry := asset.NewRegistry()
	registry.Register(asset.NewTRX(constants.TRON))
	listener := &RpcListener{
		chain:    chainpkg.NewTronChain(),
		registry: registry,
		client: fakeTronWalletClient{
			block: &pb.Block{BlockHeader: &pb.BlockHeader{RawData: &pb.BlockHeaderRaw{Number: 101}}},
		},
		events: make(chan interface{}, 1),
	}

	if err := listener.processBlock(context.Background(), 100); err == nil || !strings.Contains(err.Error(), "height mismatch") {
		t.Fatalf("processBlock err = %v, want height mismatch", err)
	}
}

func TestProcessBlockRejectsMissingTronParentHash(t *testing.T) {
	listener := &RpcListener{
		chain: chainpkg.NewTronChain(),
		client: fakeTronWalletClient{
			block: &pb.Block{BlockHeader: &pb.BlockHeader{RawData: &pb.BlockHeaderRaw{Number: 100}}},
		},
		events: make(chan interface{}, 1),
	}

	if err := listener.processBlock(context.Background(), 100); err == nil || !strings.Contains(err.Error(), "invalid parent hash") {
		t.Fatalf("processBlock err = %v, want parent hash validation error", err)
	}
}

func TestProcessBlockHoldsBeforeDispatchWhenTronCanonicalObserverFails(t *testing.T) {
	parentHashBytes := tronTestHash(0x63)
	block := &pb.Block{BlockHeader: &pb.BlockHeader{RawData: &pb.BlockHeaderRaw{Number: 100, ParentHash: parentHashBytes}}}
	blockHash := tronBlockID(block)
	parentHash := hex.EncodeToString(parentHashBytes)
	state := &models.ChainState{
		ChainID:            constants.TRON,
		LastProcessedBlock: 99,
		LastProcessedHash:  parentHash,
	}
	listener := &RpcListener{
		chain:      chainpkg.NewTronChain(),
		chainState: state,
		client:     fakeTronWalletClient{block: block},
		events:     make(chan interface{}, 1),
	}
	observerErr := errors.New("canonical block store unavailable")
	observerCalls := 0
	listener.SetCanonicalBlockObserver(func(_ context.Context, chainID constants.ChainID, blockNumber int64, observedHash, observedParentHash string) error {
		observerCalls++
		if chainID != constants.TRON || blockNumber != 100 || observedHash != blockHash || observedParentHash != parentHash {
			t.Fatalf("canonical observation = chain=%d block=%d hash=%s parent=%s", chainID, blockNumber, observedHash, observedParentHash)
		}
		return observerErr
	})

	err := listener.processBlock(context.Background(), 100)
	if !errors.Is(err, observerErr) {
		t.Fatalf("processBlock error = %v, want %v", err, observerErr)
	}
	if observerCalls != 1 {
		t.Fatalf("canonical observer calls = %d, want 1", observerCalls)
	}
	if state.LastProcessedBlock != 99 || listener.lastBlockHash != "" || listener.lastBlockParentHash != "" {
		t.Fatalf("checkpoint changed after observer failure: state=%#v hash=%q/%q", state, listener.lastBlockHash, listener.lastBlockParentHash)
	}
	select {
	case event := <-listener.events:
		t.Fatalf("unexpected event after observer failure: %#v", event)
	default:
	}
}

func TestProcessBlockRewindsTronCheckpointOnParentMismatch(t *testing.T) {
	t.Setenv(fmt.Sprintf("CHAIN_%d_REORG_REWIND_BLOCKS", constants.TRON), "3")
	parentHashBytes := tronTestHash(0x63)
	block := &pb.Block{BlockHeader: &pb.BlockHeader{RawData: &pb.BlockHeaderRaw{Number: 100, ParentHash: parentHashBytes}}}
	blockHash := tronBlockID(block)
	parentHash := hex.EncodeToString(parentHashBytes)
	state := &models.ChainState{
		ChainID:            constants.TRON,
		LastProcessedBlock: 99,
		LastProcessedHash:  hex.EncodeToString(tronTestHash(0x62)),
	}
	wroteRollback := false
	listener := &RpcListener{
		chain:      chainpkg.NewTronChain(),
		chainState: state,
		stateWriter: func(*models.ChainState) error {
			wroteRollback = true
			return nil
		},
		client: fakeTronWalletClient{block: block},
		events: make(chan interface{}, 1),
	}
	observedCanonical := false
	listener.SetCanonicalBlockObserver(func(_ context.Context, chainID constants.ChainID, blockNumber int64, observedHash, observedParentHash string) error {
		if wroteRollback {
			t.Fatal("canonical observer ran after rollback persistence")
		}
		observedCanonical = true
		if chainID != constants.TRON || blockNumber != 100 || observedHash != blockHash || observedParentHash != parentHash {
			t.Fatalf("canonical observation = chain=%d block=%d hash=%s parent=%s", chainID, blockNumber, observedHash, observedParentHash)
		}
		return nil
	})

	err := listener.processBlock(context.Background(), 100)
	if !errors.Is(err, listenerconfig.ErrParentContinuity) {
		t.Fatalf("processBlock error = %v, want ErrParentContinuity", err)
	}
	if !observedCanonical || !wroteRollback {
		t.Fatalf("canonical observation/rollback = %t/%t, want true/true", observedCanonical, wroteRollback)
	}
	if state.LastProcessedBlock != 96 || state.LastProcessedHash != "" || state.LastProcessedParentHash != "" {
		t.Fatalf("state after bounded rewind = %#v", state)
	}
	if state.ContinuityStatus != listenerconfig.ContinuityStatusRollback || state.ContinuityReason == "" {
		t.Fatalf("continuity evidence = %q/%q", state.ContinuityStatus, state.ContinuityReason)
	}
	select {
	case event := <-listener.events:
		t.Fatalf("unexpected event on parent mismatch: %#v", event)
	default:
	}
}

func TestWriteChainStateDoesNotAdvanceMemoryWhenWriterFails(t *testing.T) {
	state := &models.ChainState{ChainID: constants.TRON, LastProcessedBlock: 99, LastConfirmedBlock: 105}
	listener := &RpcListener{
		chainState: state,
		stateWriter: func(*models.ChainState) error {
			return errors.New("database unavailable")
		},
	}

	if err := listener.writeChainState(100, 106); err == nil {
		t.Fatal("expected checkpoint write error")
	}
	if state.LastProcessedBlock != 99 || state.LastConfirmedBlock != 105 {
		t.Fatalf("in-memory checkpoint advanced after failed write: %#v", state)
	}
}

func TestWriteChainStatePersistsTronBlockIdentity(t *testing.T) {
	state := &models.ChainState{ChainID: constants.TRON, LastProcessedBlock: 99}
	listener := &RpcListener{
		chainState:          state,
		lastBlockHash:       "block-100",
		lastBlockParentHash: "block-99",
	}

	if err := listener.writeChainState(100, 106); err != nil {
		t.Fatal(err)
	}
	if state.LastProcessedBlock != 100 || state.LastConfirmedBlock != 106 ||
		state.LastProcessedHash != "block-100" || state.LastProcessedParentHash != "block-99" {
		t.Fatalf("persisted checkpoint = %#v", state)
	}
}

func TestLatestBlockNumberFailsOverRetryableClientError(t *testing.T) {
	t.Setenv("TRON_GRPC_ENDPOINTS", "bad:50051,good:50051")

	listener := &RpcListener{
		chain:    chainpkg.NewTronChain(),
		client:   fakeTronWalletClient{nowErr: context.DeadlineExceeded},
		endpoint: "bad:50051",
	}
	reconnects := 0
	listener.reconnectFunc = func() error {
		reconnects++
		listener.setClient(nil, fakeTronWalletClient{
			block: &pb.Block{BlockHeader: &pb.BlockHeader{RawData: &pb.BlockHeaderRaw{Number: 321}}},
		}, "good:50051")
		return nil
	}

	got, err := listener.latestBlockNumber(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != 321 {
		t.Fatalf("latest block = %d, want 321", got)
	}
	if reconnects != 1 {
		t.Fatalf("reconnects = %d, want 1", reconnects)
	}
}

func TestTronTransactionMemoRejectsBinaryData(t *testing.T) {
	tx := &pb.Transaction{RawData: &pb.TransactionRaw{Data: []byte{0xff, 0xfe, 0xfd}}}

	if got := tronTransactionMemo(tx); got != "" {
		t.Fatalf("memo = %q, want empty", got)
	}
}

func TestDispatchUsesConfiguredTronChainID(t *testing.T) {
	listener := &RpcListener{
		chain:  chainpkg.NewTronTestnetChain(),
		events: make(chan interface{}, 1),
	}

	tx := &types.TransactionParam{Context: context.Background(), ChainID: constants.TRONTestnet}
	if err := listener.dispatch(context.Background(), "native_transfer", tx); err != nil {
		t.Fatal(err)
	}

	raw := <-listener.events
	event, ok := raw.(dispatcher.Event)
	if !ok {
		t.Fatalf("event type = %T, want dispatcher.Event", raw)
	}
	if event.Chain != constants.TRONTestnet {
		t.Fatalf("event chain = %d, want TRONTestnet", event.Chain)
	}
	if event.Transaction == nil || event.Transaction.ChainID != constants.TRONTestnet {
		t.Fatalf("event transaction = %#v, want TRONTestnet", event.Transaction)
	}
}

func TestHandleTRC20LogsSkipsZeroAmountTransfer(t *testing.T) {
	token := tronTestAddress(0xaa)
	from := tronTestAddress(0x01)
	to := tronTestAddress(0x02)
	tokenID := tronAddress(token)

	registry := asset.NewRegistry()
	registry.Register(asset.NewDeploymentAsset(
		asset.AssetDefinition{Symbol: "USDT", Name: "Tether", Decimals: 6},
		asset.Deployment{ChainID: constants.TRON, Address: tokenID, Decimals: 6, Type: asset.AssetTRC20, Enabled: true},
	))
	listener := &RpcListener{
		chain:    chainpkg.NewTronChain(),
		registry: registry,
		events:   make(chan interface{}, 1),
	}

	err := listener.handleTRC20Logs(context.Background(), &pb.TransactionInfo{
		Log: []*pb.TransactionInfo_Log{{
			Address: token,
			Topics:  [][]byte{transferEventHash, tronTopicAddress(from), tronTopicAddress(to)},
			Data:    make([]byte, 32),
		}},
	}, "tx-zero", "block-hash", "parent-hash", "100", "confirmed", "")
	if err != nil {
		t.Fatal(err)
	}

	select {
	case event := <-listener.events:
		t.Fatalf("unexpected zero amount token event: %#v", event)
	default:
	}
}

func TestHandleInternalTRXTransfersEmitsNativeValue(t *testing.T) {
	from := tronTestAddress(0x31)
	to := tronTestAddress(0x32)
	listener := &RpcListener{
		chain:  chainpkg.NewTronChain(),
		events: make(chan interface{}, 1),
	}
	info := &pb.TransactionInfo{InternalTransactions: []*pb.InternalTransaction{{
		Hash:              bytes.Repeat([]byte{0x91}, sha256.Size),
		CallerAddress:     from,
		TransferToAddress: to,
		Note:              []byte("call"),
		CallValueInfo: []*pb.InternalTransaction_CallValueInfo{{
			CallValue: 750000,
		}},
	}}}

	if err := listener.handleInternalTRXTransfers(
		context.Background(), info, "tx-internal", "block-hash", "parent-hash", "100", "confirmed", "ORDER-1", asset.NewTRX(constants.TRON),
	); err != nil {
		t.Fatal(err)
	}
	event := (<-listener.events).(dispatcher.Event)
	if event.Type != "internal_transfer" || event.Transaction == nil {
		t.Fatalf("internal transfer event = %#v", event)
	}
	tx := event.Transaction
	if tx.From == nil || *tx.From != tronAddress(from) || tx.To == nil || *tx.To != tronAddress(to) || tx.Amount == nil || *tx.Amount != "750000" {
		t.Fatalf("internal transfer transaction = %#v", tx)
	}
	wantIdentity := "internal:" + strings.Repeat("91", sha256.Size) + ":trx"
	if tx.LogIndex == nil || *tx.LogIndex != wantIdentity || tx.ParentHash == nil || *tx.ParentHash != "parent-hash" {
		t.Fatalf("internal transfer identity = %#v", tx)
	}
}

func TestHandleInternalTRXTransfersSkipsResourceInstructions(t *testing.T) {
	from := tronTestAddress(0x51)
	to := tronTestAddress(0x52)
	listener := &RpcListener{chain: chainpkg.NewTronChain(), events: make(chan interface{}, 1)}
	info := &pb.TransactionInfo{InternalTransactions: []*pb.InternalTransaction{{
		CallerAddress:     from,
		TransferToAddress: to,
		Note:              []byte("freezeBalanceV2ForEnergy"),
		CallValueInfo: []*pb.InternalTransaction_CallValueInfo{{
			CallValue: 750000,
		}},
	}}}
	if err := listener.handleInternalTRXTransfers(context.Background(), info, "tx-resource", "block", "parent", "100", "confirmed", "", asset.NewTRX(constants.TRON)); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-listener.events:
		t.Fatalf("resource instruction was mislabeled as TRX transfer: %#v", event)
	default:
	}
}

func TestHandleInternalTRXTransfersRejectsMissingStableIdentity(t *testing.T) {
	from := tronTestAddress(0x61)
	to := tronTestAddress(0x62)
	listener := &RpcListener{chain: chainpkg.NewTronChain(), events: make(chan interface{}, 1)}
	info := &pb.TransactionInfo{InternalTransactions: []*pb.InternalTransaction{{
		CallerAddress: from, TransferToAddress: to, Note: []byte("call"),
		CallValueInfo: []*pb.InternalTransaction_CallValueInfo{{CallValue: 1}},
	}}}
	if err := listener.handleInternalTRXTransfers(context.Background(), info, "tx-no-hash", "block", "parent", "100", "confirmed", "", asset.NewTRX(constants.TRON)); err == nil || !strings.Contains(err.Error(), "identity hash") {
		t.Fatalf("missing internal identity error = %v", err)
	}
	select {
	case event := <-listener.events:
		t.Fatalf("event emitted without stable internal identity: %#v", event)
	default:
	}
}

func TestTronTransactionStatusRequiresExplicitResultAndNeverUpgradesFailure(t *testing.T) {
	tx := tronNativeTransferTx(t, tronTestAddress(0x01), tronTestAddress(0x02), 1)
	tx.Ret = nil
	if _, err := tronTxStatus(tx); err == nil {
		t.Fatal("missing transaction result was accepted")
	}
	tx.Ret = []*pb.Transaction_Result{{ContractRet: pb.Transaction_Result_REVERT}}
	status, err := tronTxStatus(tx)
	if err != nil {
		t.Fatal(err)
	}
	if status != models.TransactionStatusFailed {
		t.Fatalf("reverted transaction status = %q", status)
	}
}

func TestTronHTTPInfoFailureParsingIsFailClosed(t *testing.T) {
	failed, err := tronHTTPInfoFailed(nil, "OUT_OF_ENERGY")
	if err != nil || !failed {
		t.Fatalf("OUT_OF_ENERGY failed=%v err=%v", failed, err)
	}
	if _, err := tronHTTPInfoFailed(json.RawMessage(`{"unexpected":true}`), ""); err == nil {
		t.Fatal("malformed HTTP execution result was accepted")
	}
}

func TestHTTPTransactionInfoPreservesInternalTransactions(t *testing.T) {
	from := tronTestAddress(0x41)
	to := tronTestAddress(0x42)
	var raw tronHTTPTransactionInfo
	payload := fmt.Sprintf(`{
		"id":"%s",
		"blockNumber":100,
		"internal_transactions":[{
			"hash":"aa",
			"caller_address":"%s",
			"transferTo_address":"%s",
			"callValueInfo":[{"callValue":9000,"tokenId":""}],
			"note":"63616c6c",
			"rejected":false
		}]
	}`, strings.Repeat("01", 32), hex.EncodeToString(from), hex.EncodeToString(to))
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		t.Fatal(err)
	}
	info, err := raw.toProto()
	if err != nil {
		t.Fatal(err)
	}
	if len(info.GetInternalTransactions()) != 1 {
		t.Fatalf("internal transactions = %d, want 1", len(info.GetInternalTransactions()))
	}
	internal := info.GetInternalTransactions()[0]
	if !equalBytes(internal.GetCallerAddress(), from) || !equalBytes(internal.GetTransferToAddress(), to) || len(internal.GetCallValueInfo()) != 1 || internal.GetCallValueInfo()[0].GetCallValue() != 9000 {
		t.Fatalf("decoded internal transaction = %#v", internal)
	}
}

func tronTestAddress(last byte) []byte {
	out := make([]byte, 21)
	out[0] = 0x41
	out[20] = last
	return out
}

func tronTestHash(fill byte) []byte {
	out := make([]byte, sha256.Size)
	for i := range out {
		out[i] = fill
	}
	return out
}

func tronTopicAddress(address []byte) []byte {
	out := make([]byte, 32)
	if len(address) >= 20 {
		copy(out[12:], address[len(address)-20:])
	}
	return out
}

type fakeTronWalletClient struct {
	block    *pb.Block
	info     *transactionInfoList
	nowErr   error
	blockErr error
	infoErr  error
}

func (f fakeTronWalletClient) getNowBlock(context.Context) (*pb.Block, error) {
	if f.nowErr != nil {
		return nil, f.nowErr
	}
	return f.block, nil
}

func (f fakeTronWalletClient) getBlockByNum(context.Context, int64) (*pb.Block, error) {
	if f.blockErr != nil {
		return nil, f.blockErr
	}
	return f.block, nil
}

func (f fakeTronWalletClient) getTransactionInfoByBlockNum(context.Context, int64) (*transactionInfoList, error) {
	if f.infoErr != nil {
		return nil, f.infoErr
	}
	if f.info != nil {
		return f.info, nil
	}
	return &transactionInfoList{}, nil
}

func tronNativeTransferTx(t *testing.T, owner, to []byte, amount int64) *pb.Transaction {
	t.Helper()
	transfer := &pb.TransferContract{
		OwnerAddress: owner,
		ToAddress:    to,
		Amount:       amount,
	}
	value, err := goproto.Marshal(transfer)
	if err != nil {
		t.Fatal(err)
	}
	return &pb.Transaction{
		RawData: &pb.TransactionRaw{
			Contract: []*pb.Transaction_Contract{
				{
					Type:      pb.Transaction_Contract_TransferContract,
					Parameter: &anypb.Any{Value: value},
				},
			},
		},
		Ret: []*pb.Transaction_Result{{ContractRet: pb.Transaction_Result_SUCCESS}},
	}
}
