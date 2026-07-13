package tron

import (
	"context"
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
	"core/types"
	"core/workers/dispatcher"

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
				BlockHeader:  &pb.BlockHeader{RawData: &pb.BlockHeaderRaw{Number: 100}},
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
		client:     fakeTronWalletClient{block: &pb.Block{BlockHeader: &pb.BlockHeader{RawData: &pb.BlockHeaderRaw{Number: 100}}, Transactions: []*pb.Transaction{tx}}, infoErr: context.DeadlineExceeded},
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
	}, "tx-zero", "block-hash", "100", "confirmed", "")
	if err != nil {
		t.Fatal(err)
	}

	select {
	case event := <-listener.events:
		t.Fatalf("unexpected zero amount token event: %#v", event)
	default:
	}
}

func tronTestAddress(last byte) []byte {
	out := make([]byte, 21)
	out[0] = 0x41
	out[20] = last
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
	}
}
