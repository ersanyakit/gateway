package tron

import (
	"context"
	"errors"
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

func TestProcessBlockScansNativeTRXWhenTransactionInfoUnavailable(t *testing.T) {
	registry := asset.NewRegistry()
	registry.Register(asset.NewTRX(constants.TRON))

	owner := []byte{0x41, 0x63, 0xd0, 0x90, 0xb2, 0x10, 0x1f, 0x12, 0x5f, 0x65, 0xe8, 0xfa, 0xe5, 0xb9, 0x74, 0x4d, 0x0e, 0x74, 0xeb, 0x87, 0x46}
	to := []byte{0x41, 0x07, 0xcb, 0x66, 0xbc, 0x50, 0xd0, 0x9c, 0x78, 0x4a, 0x84, 0x3a, 0x6f, 0x2a, 0x0d, 0x94, 0x29, 0x95, 0xfa, 0xcb, 0x92}
	tx := tronNativeTransferTx(t, owner, to, 18_500_000)

	listener := &RpcListener{
		chain:    chainpkg.NewTronChain(),
		registry: registry,
		client: fakeTronWalletClient{
			block: &pb.Block{
				BlockHeader:  &pb.BlockHeader{RawData: &pb.BlockHeaderRaw{Number: 100}},
				Transactions: []*pb.Transaction{tx},
			},
			infoErr: errors.New("transaction info unavailable"),
		},
		events: make(chan interface{}, 1),
	}

	if err := listener.processBlock(context.Background(), 100); err != nil {
		t.Fatal(err)
	}

	raw := <-listener.events
	event, ok := raw.(dispatcher.Event)
	if !ok {
		t.Fatalf("event type = %T, want dispatcher.Event", raw)
	}
	if event.Chain != constants.TRON || event.Type != "native_transfer" {
		t.Fatalf("event identity = chain:%d type:%s", event.Chain, event.Type)
	}
	if event.Transaction == nil {
		t.Fatal("transaction is nil")
	}
	if got := *event.Transaction.Amount; got != "18500000" {
		t.Fatalf("amount = %q, want 18500000", got)
	}
	if got := *event.Transaction.Symbol; got != "TRX" {
		t.Fatalf("symbol = %q, want TRX", got)
	}
	if got := *event.Transaction.From; got != tronAddress(owner) {
		t.Fatalf("from = %q, want %q", got, tronAddress(owner))
	}
	if got := *event.Transaction.To; got != tronAddress(to) {
		t.Fatalf("to = %q, want %q", got, tronAddress(to))
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

type fakeTronWalletClient struct {
	block   *pb.Block
	infoErr error
}

func (f fakeTronWalletClient) getNowBlock(context.Context) (*pb.Block, error) {
	return f.block, nil
}

func (f fakeTronWalletClient) getBlockByNum(context.Context, int64) (*pb.Block, error) {
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
