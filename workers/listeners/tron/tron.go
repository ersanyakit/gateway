package tron

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"core/asset"
	"core/blockchain"
	"core/blockchain/addrutil"
	"core/helpers"
	"core/models"
	"core/types"
	"core/workers/dispatcher"
	listenerconfig "core/workers/listeners"
	"core/workers/listeners/rpcutil"

	"github.com/ethereum/go-ethereum/crypto"
	goproto "github.com/golang/protobuf/proto"
	"github.com/okx/go-wallet-sdk/coins/tron/pb"
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

	conn            *grpc.ClientConn
	client          tronWalletClient
	httpClient      *http.Client
	endpoint        string
	apiKey          string
	connMu          sync.RWMutex
	endpointCircuit *rpcutil.EndpointCircuit
	reconnectFunc   func() error

	mu      sync.Mutex
	quit    chan struct{}
	running bool
	events  chan interface{}

	throttleErrors       int
	lastRetryableWarning time.Time
}

type walletClient struct {
	cc grpc.ClientConnInterface
}

var errTronClientNotConnected = errors.New("tron grpc client is not connected")

type tronWalletClient interface {
	getNowBlock(ctx context.Context) (*pb.Block, error)
	getBlockByNum(ctx context.Context, num int64) (*pb.Block, error)
	getTransactionInfoByBlockNum(ctx context.Context, num int64) (*transactionInfoList, error)
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

type tronHTTPTransactionInfo struct {
	ID          string          `json:"id"`
	Result      json.RawMessage `json:"result"`
	BlockNumber int64           `json:"blockNumber"`
	Receipt     struct {
		Result string `json:"result"`
	} `json:"receipt"`
	Log []tronHTTPLog `json:"log"`
}

type tronHTTPLog struct {
	Address string   `json:"address"`
	Topics  []string `json:"topics"`
	Data    string   `json:"data"`
}

func (t tronHTTPTransactionInfo) toProto() (*pb.TransactionInfo, error) {
	id, err := tronDecodeHex(t.ID)
	if err != nil {
		return nil, fmt.Errorf("decode tron transaction info id: %w", err)
	}
	info := &pb.TransactionInfo{
		Id:          id,
		BlockNumber: t.BlockNumber,
		Result:      pb.TransactionInfo_SUCESS,
		Log:         make([]*pb.TransactionInfo_Log, 0, len(t.Log)),
	}
	if tronHTTPInfoFailed(t.Result, t.Receipt.Result) {
		info.Result = pb.TransactionInfo_FAILED
	}
	for idx, entry := range t.Log {
		logEntry, err := entry.toProto()
		if err != nil {
			return nil, fmt.Errorf("decode tron transaction info log %d: %w", idx, err)
		}
		info.Log = append(info.Log, logEntry)
	}
	return info, nil
}

func (l tronHTTPLog) toProto() (*pb.TransactionInfo_Log, error) {
	address, err := tronDecodeHex(l.Address)
	if err != nil {
		return nil, fmt.Errorf("decode address: %w", err)
	}
	out := &pb.TransactionInfo_Log{
		Address: address,
		Topics:  make([][]byte, 0, len(l.Topics)),
	}
	for _, topic := range l.Topics {
		decoded, err := tronDecodeHex(topic)
		if err != nil {
			return nil, fmt.Errorf("decode topic: %w", err)
		}
		out.Topics = append(out.Topics, decoded)
	}
	if strings.TrimSpace(l.Data) != "" {
		data, err := tronDecodeHex(l.Data)
		if err != nil {
			return nil, fmt.Errorf("decode data: %w", err)
		}
		out.Data = data
	}
	return out, nil
}

func tronHTTPInfoFailed(raw json.RawMessage, receiptResult string) bool {
	if strings.EqualFold(strings.TrimSpace(receiptResult), "FAILED") {
		return true
	}
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.EqualFold(strings.TrimSpace(text), "FAILED")
	}
	var numeric int
	if err := json.Unmarshal(raw, &numeric); err == nil {
		return numeric == int(pb.TransactionInfo_FAILED)
	}
	return false
}

func tronDecodeHex(value string) ([]byte, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "0x")
	if value == "" {
		return nil, nil
	}
	if len(value)%2 != 0 {
		value = "0" + value
	}
	return hex.DecodeString(value)
}

func newWalletClient(cc grpc.ClientConnInterface) *walletClient {
	return &walletClient{cc: cc}
}

func (c *walletClient) getNowBlock(ctx context.Context) (*pb.Block, error) {
	if c == nil || c.cc == nil {
		return nil, errTronClientNotConnected
	}
	out := new(pb.Block)
	err := c.cc.Invoke(ctx, "/protocol.Wallet/GetNowBlock", &emptyMessage{}, out, grpc.MaxCallRecvMsgSize(32*1024*1024))
	return out, err
}

func (c *walletClient) getBlockByNum(ctx context.Context, num int64) (*pb.Block, error) {
	if c == nil || c.cc == nil {
		return nil, errTronClientNotConnected
	}
	out := new(pb.Block)
	err := c.cc.Invoke(ctx, "/protocol.Wallet/GetBlockByNum", &numberMessage{Num: num}, out, grpc.MaxCallRecvMsgSize(32*1024*1024))
	return out, err
}

func (c *walletClient) getTransactionInfoByBlockNum(ctx context.Context, num int64) (*transactionInfoList, error) {
	if c == nil || c.cc == nil {
		return nil, errTronClientNotConnected
	}
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
		chain:           chain,
		registry:        registry,
		chainState:      state,
		bus:             bus,
		stateWriter:     stateWriter,
		apiKey:          strings.TrimSpace(os.Getenv("TRON_PRO_API_KEY")),
		httpClient:      &http.Client{Timeout: 30 * time.Second},
		endpointCircuit: rpcutil.NewEndpointCircuit(),
		quit:            make(chan struct{}),
		events:          make(chan interface{}, 1000),
	}
}

func (r *RpcListener) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running {
		return fmt.Errorf("listener already running")
	}
	r.quit = make(chan struct{})
	if err := r.connect(); err != nil {
		return err
	}

	r.running = true
	helpers.GoSafely("listener.tron."+r.chain.Name(), r.pollLoop)
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
	return r.closeClient()
}

func (r *RpcListener) Events() <-chan interface{} {
	return r.events
}

func (r *RpcListener) connect() error {
	var lastErr error
	for _, endpoint := range r.grpcEndpoints() {
		conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			lastErr = err
			r.recordEndpointFailure(endpoint, err)
			continue
		}

		client := newWalletClient(conn)
		ctx, cancel := r.grpcContext(context.Background(), 15*time.Second)
		_, err = client.getNowBlock(ctx)
		cancel()
		if err != nil {
			_ = conn.Close()
			lastErr = err
			r.recordEndpointFailure(endpoint, err)
			continue
		}

		r.recordEndpointSuccess(endpoint)
		r.setClient(conn, client, endpoint)
		log.Printf("[tron] connected to fullnode grpc %s", endpoint)
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no tron gRPC endpoints configured")
	}
	return lastErr
}

func (r *RpcListener) currentClient() (tronWalletClient, string, error) {
	r.connMu.RLock()
	client := r.client
	endpoint := r.endpoint
	r.connMu.RUnlock()
	if client == nil {
		return nil, "", errTronClientNotConnected
	}
	return client, endpoint, nil
}

func (r *RpcListener) setClient(conn *grpc.ClientConn, client tronWalletClient, endpoint string) {
	r.connMu.Lock()
	r.conn = conn
	r.client = client
	r.endpoint = endpoint
	r.connMu.Unlock()
}

func (r *RpcListener) closeClient() error {
	r.connMu.Lock()
	conn := r.conn
	r.conn = nil
	r.client = nil
	r.endpoint = ""
	r.connMu.Unlock()
	if conn != nil {
		return conn.Close()
	}
	return nil
}

func (r *RpcListener) grpcContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	if r.apiKey != "" {
		ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("TRON-PRO-API-KEY", r.apiKey))
	}
	return ctx, cancel
}

func tronGRPCEndpoints() []string {
	return tronGRPCEndpointsForChain("tron")
}

func tronGRPCEndpointsForChain(chainName string) []string {
	if strings.EqualFold(strings.TrimSpace(chainName), "tron-testnet") {
		raw := strings.TrimSpace(os.Getenv("TRON_TESTNET_GRPC_ENDPOINTS"))
		if raw == "" {
			raw = strings.TrimSpace(os.Getenv("TRON_TESTNET_GRPC_ENDPOINT"))
		}
		if raw == "" {
			return []string{"grpc.nile.trongrid.io:50051"}
		}
		return splitTronGRPCEndpoints(raw)
	}

	raw := strings.TrimSpace(os.Getenv("TRON_GRPC_ENDPOINTS"))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("TRON_GRPC_ENDPOINT"))
	}
	if raw == "" {
		return []string{"grpc.trongrid.io:50051"}
	}

	return splitTronGRPCEndpoints(raw)
}

func (r *RpcListener) grpcEndpoints() []string {
	chainName := "tron"
	if r != nil && r.chain != nil {
		chainName = r.chain.Name()
	}
	endpoints := tronGRPCEndpointsForChain(chainName)
	if r == nil || r.endpointCircuit == nil {
		return endpoints
	}
	return r.endpointCircuit.Rank(endpoints)
}

func (r *RpcListener) httpEndpoints() []string {
	chainName := "tron"
	var rpcs []string
	if r != nil && r.chain != nil {
		chainName = r.chain.Name()
		rpcs = r.chain.RPCs()
	}
	endpoints := tronHTTPEndpointsForChain(chainName, rpcs)
	if r == nil || r.endpointCircuit == nil {
		return endpoints
	}
	return r.endpointCircuit.Rank(endpoints)
}

func (r *RpcListener) recordEndpointSuccess(endpoint string) {
	if r != nil && r.endpointCircuit != nil {
		r.endpointCircuit.RecordSuccess(endpoint)
	}
}

func (r *RpcListener) recordEndpointFailure(endpoint string, err error) {
	if r != nil && r.endpointCircuit != nil {
		r.endpointCircuit.RecordFailure(endpoint, err)
	}
}

func (r *RpcListener) withClientFailover(ctx context.Context, op func(tronWalletClient) error) error {
	attempts := len(r.grpcEndpoints())
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	for i := 0; i < attempts; i++ {
		client, endpoint, err := r.currentClient()
		if err != nil {
			lastErr = err
			if errors.Is(err, errTronClientNotConnected) {
				return err
			}
		} else if err := op(client); err != nil {
			lastErr = err
			r.recordEndpointFailure(endpoint, err)
			if !rpcutil.IsRetryable(err) {
				return err
			}
		} else {
			r.recordEndpointSuccess(endpoint)
			return nil
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := r.reconnectOnce(); err != nil {
			if lastErr != nil {
				return errors.Join(lastErr, err)
			}
			return err
		}
	}

	if lastErr == nil {
		lastErr = errTronClientNotConnected
	}
	return lastErr
}

func (r *RpcListener) reconnectOnce() error {
	if r.reconnectFunc != nil {
		return r.reconnectFunc()
	}
	_ = r.closeClient()
	return r.connect()
}

func splitTronGRPCEndpoints(raw string) []string {
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

func tronHTTPEndpointsForChain(chainName string, rpcs []string) []string {
	raw := ""
	if strings.EqualFold(strings.TrimSpace(chainName), "tron-testnet") {
		raw = strings.TrimSpace(os.Getenv("TRON_TESTNET_HTTP_ENDPOINTS"))
		if raw == "" {
			raw = strings.TrimSpace(os.Getenv("TRON_TESTNET_HTTP_ENDPOINT"))
		}
		if raw == "" {
			raw = strings.Join(rpcs, ",")
		}
		if raw == "" {
			return []string{"https://nile.trongrid.io"}
		}
		return splitTronHTTPEndpoints(raw)
	}

	raw = strings.TrimSpace(os.Getenv("TRON_HTTP_ENDPOINTS"))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("TRON_HTTP_ENDPOINT"))
	}
	if raw == "" {
		raw = strings.Join(rpcs, ",")
	}
	if raw == "" {
		return []string{"https://api.trongrid.io"}
	}
	return splitTronHTTPEndpoints(raw)
}

func splitTronHTTPEndpoints(raw string) []string {
	parts := strings.Split(raw, ",")
	endpoints := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		endpoint := strings.TrimRight(strings.TrimSpace(part), "/")
		endpoint = strings.TrimSuffix(endpoint, "/jsonrpc")
		if endpoint == "" {
			continue
		}
		if _, ok := seen[endpoint]; ok {
			continue
		}
		seen[endpoint] = struct{}{}
		endpoints = append(endpoints, endpoint)
	}
	return endpoints
}

func (r *RpcListener) pollLoop() {
	for {
		delay := pollInterval
		if err := r.catchUp(); err != nil {
			if rpcutil.IsRetryable(err) {
				r.throttleErrors++
				delay = rpcutil.ThrottleDelay(err, r.throttleErrors, pollInterval)
				if time.Since(r.lastRetryableWarning) >= time.Minute {
					r.lastRetryableWarning = time.Now()
					log.Printf("[tron] listener transient RPC; checkpoint held; retrying in %s: %v", delay.Round(time.Second), err)
				}
				if !rpcutil.IsThrottle(err) {
					r.reconnect()
				}
			} else {
				log.Printf("[tron] listener catch-up error: %v", err)
				r.throttleErrors = 0
				r.reconnect()
			}
		} else {
			r.throttleErrors = 0
		}

		timer := time.NewTimer(delay)
		select {
		case <-r.quit:
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (r *RpcListener) reconnect() {
	_ = r.closeClient()
	for {
		select {
		case <-r.quit:
			return
		default:
			if err := r.connect(); err == nil {
				return
			}
			timer := time.NewTimer(3 * time.Second)
			select {
			case <-r.quit:
				timer.Stop()
				return
			case <-timer.C:
			}
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
	confirmedHead := latest
	safeLatest := latest - safeBlockConfirmations
	if safeLatest <= 0 {
		return nil
	}

	decision, err := listenerconfig.ResolveStartBlock(r.chain, r.chainState.LastProcessedBlock, safeLatest)
	if err != nil {
		return err
	}
	if decision.Warning != "" {
		log.Printf("[%s] scanner start policy: %s\n", r.chain.Name(), decision.Warning)
	}
	listenerconfig.ApplyStartBlockDecision(r.chainState, decision)
	from := decision.From
	if from > safeLatest {
		return nil
	}

	to := from + maxBlocksPerPoll - 1
	if to > safeLatest {
		to = safeLatest
	}

	for blockNumber := from; blockNumber <= to; blockNumber++ {
		if err := r.processBlock(ctx, blockNumber); err != nil {
			return err
		}

		r.chainState.LastProcessedBlock = blockNumber
		r.chainState.LastConfirmedBlock = confirmedHead
		if r.stateWriter != nil {
			if err := r.stateWriter(r.chainState); err != nil {
				return fmt.Errorf("write chain state: %w", err)
			}
		}
	}
	return nil
}

func (r *RpcListener) latestBlockNumber(ctx context.Context) (int64, error) {
	var block *pb.Block
	err := r.withClientFailover(ctx, func(client tronWalletClient) error {
		var err error
		block, err = client.getNowBlock(ctx)
		return err
	})
	if err != nil {
		return 0, err
	}
	return block.GetBlockHeader().GetRawData().GetNumber(), nil
}

func (r *RpcListener) processBlock(ctx context.Context, blockNumber int64) error {
	var block *pb.Block
	err := r.withClientFailover(ctx, func(client tronWalletClient) error {
		var err error
		block, err = client.getBlockByNum(ctx, blockNumber)
		return err
	})
	if err != nil {
		return err
	}

	var infoList *transactionInfoList
	err = r.withClientFailover(ctx, func(client tronWalletClient) error {
		var err error
		infoList, err = client.getTransactionInfoByBlockNum(ctx, blockNumber)
		return err
	})
	if err != nil && rpcutil.IsRetryable(err) {
		if fallbackInfo, fallbackErr := r.httpTransactionInfoForBlock(ctx, block); fallbackErr == nil {
			infoList = fallbackInfo
			err = nil
		} else {
			err = errors.Join(err, fallbackErr)
		}
	}
	infoByTxID := make(map[string]*pb.TransactionInfo)
	if err != nil {
		return fmt.Errorf("transaction info fetch failed for block %d; checkpoint held to preserve TRC20 logs: %w", blockNumber, err)
	} else {
		for _, info := range infoList.GetTransactionInfo() {
			infoByTxID[hex.EncodeToString(info.GetId())] = info
		}
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

		memo := tronTransactionMemo(tx)
		if err := r.handleNativeTransfers(ctx, tx, txID, blockHash, readableBlock, txIndex, status, memo, nativeAsset); err != nil {
			return err
		}
		if info := infoByTxID[txID]; info != nil {
			if err := r.handleTRC20Logs(ctx, info, txID, blockHash, readableBlock, status, memo); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *RpcListener) httpTransactionInfoForBlock(ctx context.Context, block *pb.Block) (*transactionInfoList, error) {
	if block == nil {
		return nil, fmt.Errorf("tron HTTP transaction info fallback requires block")
	}
	out := &transactionInfoList{TransactionInfo: make([]*pb.TransactionInfo, 0, len(block.GetTransactions()))}
	for _, tx := range block.GetTransactions() {
		txID, err := tronTxID(tx)
		if err != nil {
			return nil, err
		}
		info, err := r.httpTransactionInfoByID(ctx, txID)
		if err != nil {
			return nil, fmt.Errorf("tron HTTP transaction info %s: %w", txID, err)
		}
		out.TransactionInfo = append(out.TransactionInfo, info)
	}
	return out, nil
}

func (r *RpcListener) httpTransactionInfoByID(ctx context.Context, txID string) (*pb.TransactionInfo, error) {
	body, err := r.tronHTTPPost(ctx, "/wallet/gettransactioninfobyid", map[string]string{"value": txID})
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(body)) == 0 || string(bytes.TrimSpace(body)) == "{}" {
		return nil, fmt.Errorf("empty tron transaction info response")
	}

	var raw tronHTTPTransactionInfo
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	if strings.TrimSpace(raw.ID) == "" {
		raw.ID = txID
	}
	info, err := raw.toProto()
	if err != nil {
		return nil, err
	}
	return info, nil
}

func (r *RpcListener) tronHTTPPost(ctx context.Context, path string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	client := r.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	var lastErr error
	for _, endpoint := range r.httpEndpoints() {
		endpointCtx, cancel := rpcutil.WithEndpointTimeout(ctx)
		req, err := http.NewRequestWithContext(endpointCtx, http.MethodPost, strings.TrimRight(endpoint, "/")+path, bytes.NewReader(body))
		if err != nil {
			cancel()
			lastErr = err
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		if r.apiKey != "" {
			req.Header.Set("TRON-PRO-API-KEY", r.apiKey)
		}

		resp, err := client.Do(req)
		if err != nil {
			cancel()
			lastErr = err
			r.recordEndpointFailure(endpoint, err)
			continue
		}
		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		cancel()
		if readErr != nil {
			lastErr = readErr
			r.recordEndpointFailure(endpoint, readErr)
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			err := fmt.Errorf("tron HTTP %s returned HTTP %d: %s", endpoint, resp.StatusCode, strings.TrimSpace(string(respBody)))
			if rpcutil.StatusThrottled(resp.StatusCode) {
				lastErr = rpcutil.NewThrottleError(err, rpcutil.RetryAfter(resp.Header))
			} else {
				lastErr = err
			}
			r.recordEndpointFailure(endpoint, lastErr)
			continue
		}

		r.recordEndpointSuccess(endpoint)
		return respBody, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no tron HTTP endpoint configured")
	}
	return nil, lastErr
}

func (r *RpcListener) handleNativeTransfers(ctx context.Context, tx *pb.Transaction, txID, blockHash, blockNumber string, txIndex int, status, memo string, nativeAsset asset.Asset) error {
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
			LogIndex:  helpers.StrPtr(fmt.Sprintf("tx:%d", contractIndex)),
			Status:    helpers.StrPtr(status),
			Memo:      optionalMemoPtr(memo),
		}
		if err := r.dispatch(ctx, "native_transfer", txParam); err != nil {
			return err
		}
	}
	return nil
}

func (r *RpcListener) handleTRC20Logs(ctx context.Context, info *pb.TransactionInfo, txID, blockHash, blockNumber, status, memo string) error {
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
		if amount.Sign() <= 0 {
			continue
		}
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
			Memo:      optionalMemoPtr(memo),
		}
		if err := r.dispatch(ctx, "token_transfer", txParam); err != nil {
			return err
		}
	}
	return nil
}

func tronTransactionMemo(tx *pb.Transaction) string {
	if tx == nil || tx.GetRawData() == nil {
		return ""
	}
	return readableMemoBytes(tx.GetRawData().GetData())
}

func readableMemoBytes(payload []byte) string {
	if len(payload) == 0 || !utf8.Valid(payload) {
		return ""
	}
	memo := strings.TrimSpace(string(payload))
	if memo == "" {
		return ""
	}
	for _, r := range memo {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			return ""
		}
	}
	return memo
}

func optionalMemoPtr(memo string) *string {
	memo = strings.TrimSpace(memo)
	if memo == "" {
		return nil
	}
	return helpers.StrPtr(memo)
}

func (r *RpcListener) dispatch(ctx context.Context, eventType string, txParam *types.TransactionParam) error {
	event := dispatcher.Event{
		Chain:       r.chain.ChainID(),
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
	rawData := block.GetBlockHeader().GetRawData()
	if rawData == nil || rawData.GetNumber() < 0 {
		return ""
	}
	raw, err := goproto.Marshal(rawData)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	id := sum
	binary.BigEndian.PutUint64(id[:8], uint64(rawData.GetNumber()))
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
	address, err := addrutil.TronAddressFromHash(raw)
	if err == nil {
		return address
	}
	return hex.EncodeToString(raw)
}
