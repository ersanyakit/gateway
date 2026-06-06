package tron

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"
	"sync"
	"time"

	"core/asset"
	"core/blockchain"
	"core/constants"
	"core/helpers"
	"core/models"
	"core/types"
	"core/workers/dispatcher"

	"github.com/ethereum/go-ethereum/crypto"
	goproto "github.com/golang/protobuf/proto"
	"github.com/okx/go-wallet-sdk/coins/tron/pb"
	"github.com/okx/go-wallet-sdk/crypto/base58"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

var transferEventHash = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)")).Bytes()

const (
	pollInterval           = 3 * time.Second
	maxBlocksPerPoll       = int64(10)
	safeBlockConfirmations = int64(2)
)

type RpcListener struct {
	chain       blockchain.Chain
	registry    *asset.Registry
	chainState  *models.ChainState
	stateWriter func(*models.ChainState) error
	bus         *dispatcher.Dispatcher

	conn   *grpc.ClientConn
	client *walletClient
	apiKey string

	mu      sync.Mutex
	quit    chan struct{}
	running bool
	events  chan interface{}
}

type walletClient struct {
	cc grpc.ClientConnInterface
}

type emptyMessage struct{}

func (*emptyMessage) Reset()         {}
func (*emptyMessage) String() string { return "empty" }
func (*emptyMessage) ProtoMessage()  {}

type numberMessage struct {
	Num int64 `protobuf:"varint,1,opt,name=num,proto3" json:"num,omitempty"`
}

func (m *numberMessage) Reset()         { *m = numberMessage{} }
func (m *numberMessage) String() string { return goproto.CompactTextString(m) }
func (*numberMessage) ProtoMessage()    {}

type transactionInfoList struct {
	TransactionInfo []*pb.TransactionInfo `protobuf:"bytes,1,rep,name=transactionInfo,proto3" json:"transactionInfo,omitempty"`
}

func (m *transactionInfoList) Reset()         { *m = transactionInfoList{} }
func (m *transactionInfoList) String() string { return goproto.CompactTextString(m) }
func (*transactionInfoList) ProtoMessage()    {}

func (m *transactionInfoList) GetTransactionInfo() []*pb.TransactionInfo {
	if m != nil {
		return m.TransactionInfo
	}
	return nil
}

func newWalletClient(cc grpc.ClientConnInterface) *walletClient {
	return &walletClient{cc: cc}
}

func (c *walletClient) getNowBlock(ctx context.Context) (*pb.Block, error) {
	out := new(pb.Block)
	err := c.cc.Invoke(ctx, "/protocol.Wallet/GetNowBlock", &emptyMessage{}, out, grpc.MaxCallRecvMsgSize(32*1024*1024))
	return out, err
}

func (c *walletClient) getBlockByNum(ctx context.Context, num int64) (*pb.Block, error) {
	out := new(pb.Block)
	err := c.cc.Invoke(ctx, "/protocol.Wallet/GetBlockByNum", &numberMessage{Num: num}, out, grpc.MaxCallRecvMsgSize(32*1024*1024))
	return out, err
}

func (c *walletClient) getTransactionInfoByBlockNum(ctx context.Context, num int64) (*transactionInfoList, error) {
	out := new(transactionInfoList)
	err := c.cc.Invoke(ctx, "/protocol.Wallet/GetTransactionInfoByBlockNum", &numberMessage{Num: num}, out, grpc.MaxCallRecvMsgSize(32*1024*1024))
	return out, err
}

func NewRpcListener(
	chain blockchain.Chain,
	registry *asset.Registry,
	state *models.ChainState,
	bus *dispatcher.Dispatcher,
	stateWriter func(*models.ChainState) error,
) *RpcListener {
	if state == nil {
		state = &models.ChainState{ChainID: chain.ChainID()}
	}
	return &RpcListener{
		chain:       chain,
		registry:    registry,
		chainState:  state,
		bus:         bus,
		stateWriter: stateWriter,
		apiKey:      strings.TrimSpace(os.Getenv("TRON_PRO_API_KEY")),
		quit:        make(chan struct{}),
		events:      make(chan interface{}, 1000),
	}
}

func (r *RpcListener) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running {
		return fmt.Errorf("listener already running")
	}
	if err := r.connect(); err != nil {
		return err
	}

	r.running = true
	go r.pollLoop()
	return nil
}

func (r *RpcListener) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.running {
		return nil
	}
	close(r.quit)
	r.running = false
	if r.conn != nil {
		return r.conn.Close()
	}
	return nil
}

func (r *RpcListener) Events() <-chan interface{} {
	return r.events
}

func (r *RpcListener) connect() error {
	var lastErr error
	for _, endpoint := range tronGRPCEndpoints() {
		conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			lastErr = err
			continue
		}

		client := newWalletClient(conn)
		ctx, cancel := r.grpcContext(context.Background(), 15*time.Second)
		_, err = client.getNowBlock(ctx)
		cancel()
		if err != nil {
			_ = conn.Close()
			lastErr = err
			continue
		}

		r.conn = conn
		r.client = client
		log.Printf("[tron] connected to fullnode grpc %s", endpoint)
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no tron gRPC endpoints configured")
	}
	return lastErr
}

func (r *RpcListener) grpcContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	if r.apiKey != "" {
		ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("TRON-PRO-API-KEY", r.apiKey))
	}
	return ctx, cancel
}

func tronGRPCEndpoints() []string {
	raw := strings.TrimSpace(os.Getenv("TRON_GRPC_ENDPOINTS"))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("TRON_GRPC_ENDPOINT"))
	}
	if raw == "" {
		return []string{"grpc.trongrid.io:50051"}
	}

	parts := strings.Split(raw, ",")
	endpoints := make([]string, 0, len(parts))
	for _, part := range parts {
		endpoint := strings.TrimSpace(part)
		endpoint = strings.TrimPrefix(endpoint, "grpc://")
		if endpoint != "" {
			endpoints = append(endpoints, endpoint)
		}
	}
	return endpoints
}

func (r *RpcListener) pollLoop() {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		if err := r.catchUp(); err != nil {
			log.Printf("[tron] listener catch-up error: %v", err)
			r.reconnect()
		}

		select {
		case <-r.quit:
			return
		case <-ticker.C:
		}
	}
}

func (r *RpcListener) reconnect() {
	if r.conn != nil {
		_ = r.conn.Close()
		r.conn = nil
		r.client = nil
	}
	for {
		select {
		case <-r.quit:
			return
		default:
			if err := r.connect(); err == nil {
				return
			}
			time.Sleep(3 * time.Second)
		}
	}
}

func (r *RpcListener) catchUp() error {
	ctx, cancel := r.grpcContext(context.Background(), 2*time.Minute)
	defer cancel()

	latest, err := r.latestBlockNumber(ctx)
	if err != nil {
		return err
	}
	latest -= safeBlockConfirmations
	if latest <= 0 {
		return nil
	}

	from := r.chainState.LastProcessedBlock + 1
	if from <= 1 {
		from = latest
	}
	if from > latest {
		return nil
	}

	to := from + maxBlocksPerPoll - 1
	if to > latest {
		to = latest
	}

	for blockNumber := from; blockNumber <= to; blockNumber++ {
		if err := r.processBlock(ctx, blockNumber); err != nil {
			return err
		}

		r.chainState.LastProcessedBlock = blockNumber
		r.chainState.LastConfirmedBlock = blockNumber
		if r.stateWriter != nil {
			if err := r.stateWriter(r.chainState); err != nil {
				return fmt.Errorf("write chain state: %w", err)
			}
		}
	}
	return nil
}

func (r *RpcListener) latestBlockNumber(ctx context.Context) (int64, error) {
	block, err := r.client.getNowBlock(ctx)
	if err != nil {
		return 0, err
	}
	return block.GetBlockHeader().GetRawData().GetNumber(), nil
}

func (r *RpcListener) processBlock(ctx context.Context, blockNumber int64) error {
	block, err := r.client.getBlockByNum(ctx, blockNumber)
	if err != nil {
		return err
	}
	infoList, err := r.client.getTransactionInfoByBlockNum(ctx, blockNumber)
	if err != nil {
		return err
	}

	infoByTxID := make(map[string]*pb.TransactionInfo)
	for _, info := range infoList.GetTransactionInfo() {
		infoByTxID[hex.EncodeToString(info.GetId())] = info
	}

	nativeAsset, ok := r.registry.GetNative(r.chain.ChainID())
	if !ok {
		return fmt.Errorf("native tron asset is not registered")
	}

	readableBlock := fmt.Sprintf("%d", blockNumber)
	blockHash := tronBlockID(block)
	for txIndex, tx := range block.GetTransactions() {
		txID, err := tronTxID(tx)
		if err != nil {
			return err
		}

		status := tronTxStatus(tx)
		if info := infoByTxID[txID]; info != nil {
			status = tronInfoStatus(info)
		}

		if err := r.handleNativeTransfers(ctx, tx, txID, blockHash, readableBlock, txIndex, status, nativeAsset); err != nil {
			return err
		}
		if info := infoByTxID[txID]; info != nil {
			if err := r.handleTRC20Logs(ctx, info, txID, blockHash, readableBlock, status); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *RpcListener) handleNativeTransfers(ctx context.Context, tx *pb.Transaction, txID, blockHash, blockNumber string, txIndex int, status string, nativeAsset asset.Asset) error {
	if tx == nil || tx.GetRawData() == nil {
		return nil
	}
	for contractIndex, contract := range tx.GetRawData().GetContract() {
		if contract.GetType() != pb.Transaction_Contract_TransferContract || contract.GetParameter() == nil {
			continue
		}

		var transfer pb.TransferContract
		if err := goproto.Unmarshal(contract.GetParameter().GetValue(), &transfer); err != nil {
			return fmt.Errorf("decode tron transfer contract %s: %w", txID, err)
		}
		if transfer.GetAmount() <= 0 {
			continue
		}

		txParam := &types.TransactionParam{
			Context:   context.Background(),
			ChainID:   r.chain.ChainID(),
			Symbol:    helpers.StrPtr(nativeAsset.GetSymbol()),
			Decimals:  nativeAsset.GetDecimals(),
			Hash:      helpers.StrPtr(txID),
			Block:     helpers.StrPtr(blockNumber),
			BlockHash: helpers.StrPtr(blockHash),
			Token:     nil,
			From:      helpers.StrPtr(tronAddress(transfer.GetOwnerAddress())),
			To:        helpers.StrPtr(tronAddress(transfer.GetToAddress())),
			Amount:    helpers.StrPtr(fmt.Sprintf("%d", transfer.GetAmount())),
			LogIndex:  helpers.StrPtr(fmt.Sprintf("tx:%d:%d", txIndex, contractIndex)),
			Status:    helpers.StrPtr(status),
		}
		if err := r.dispatch(ctx, "native_transfer", txParam); err != nil {
			return err
		}
	}
	return nil
}

func (r *RpcListener) handleTRC20Logs(ctx context.Context, info *pb.TransactionInfo, txID, blockHash, blockNumber, status string) error {
	for idx, entry := range info.GetLog() {
		topics := entry.GetTopics()
		if len(topics) < 3 || !equalBytes(topics[0], transferEventHash) {
			continue
		}

		tokenID := tronAddress(entry.GetAddress())
		assetInfo, isRegistered := r.registry.Get(r.chain.ChainID(), tokenID)
		if !isRegistered {
			continue
		}

		amount := new(big.Int).SetBytes(entry.GetData())
		txParam := &types.TransactionParam{
			Context:   context.Background(),
			ChainID:   r.chain.ChainID(),
			Symbol:    helpers.StrPtr(assetInfo.GetSymbol()),
			Decimals:  assetInfo.GetDecimals(),
			Hash:      helpers.StrPtr(txID),
			Block:     helpers.StrPtr(blockNumber),
			BlockHash: helpers.StrPtr(blockHash),
			Token:     helpers.StrPtr(tokenID),
			From:      helpers.StrPtr(topicToTronAddress(topics[1])),
			To:        helpers.StrPtr(topicToTronAddress(topics[2])),
			Amount:    helpers.StrPtr(amount.String()),
			LogIndex:  helpers.StrPtr(fmt.Sprintf("log:%d", idx)),
			Status:    helpers.StrPtr(status),
		}
		if err := r.dispatch(ctx, "token_transfer", txParam); err != nil {
			return err
		}
	}
	return nil
}

func (r *RpcListener) dispatch(ctx context.Context, eventType string, txParam *types.TransactionParam) error {
	event := dispatcher.Event{
		Chain:       constants.TRON,
		Type:        eventType,
		Transaction: txParam,
	}

	if r.bus != nil {
		if err := r.bus.DispatchAndWait(ctx, event); err != nil {
			return err
		}
	}

	select {
	case r.events <- event:
	default:
	}
	return nil
}

func tronTxID(tx *pb.Transaction) (string, error) {
	raw, err := goproto.Marshal(tx.GetRawData())
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func tronBlockID(block *pb.Block) string {
	raw, err := goproto.Marshal(block.GetBlockHeader().GetRawData())
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	id := sum
	binary.BigEndian.PutUint64(id[:8], uint64(block.GetBlockHeader().GetRawData().GetNumber()))
	return hex.EncodeToString(id[:])
}

func tronTxStatus(tx *pb.Transaction) string {
	if tx == nil {
		return "confirmed"
	}
	for _, result := range tx.GetRet() {
		if result.GetContractRet() != pb.Transaction_Result_SUCCESS && result.GetContractRet() != pb.Transaction_Result_DEFAULT {
			return "failed"
		}
	}
	return "confirmed"
}

func tronInfoStatus(info *pb.TransactionInfo) string {
	if info != nil && info.GetResult() == pb.TransactionInfo_FAILED {
		return "failed"
	}
	return "confirmed"
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func topicToTronAddress(topic []byte) string {
	if len(topic) >= 20 {
		return tronAddress(append([]byte{0x41}, topic[len(topic)-20:]...))
	}
	return tronAddress(topic)
}

func tronAddress(raw []byte) string {
	if len(raw) == 20 {
		return base58.CheckEncode(raw, 0x41)
	}
	if len(raw) == 21 && raw[0] == 0x41 {
		return base58.CheckEncode(raw[1:], raw[0])
	}
	return hex.EncodeToString(raw)
}
