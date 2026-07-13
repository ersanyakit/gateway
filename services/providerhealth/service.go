package providerhealth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"core/blockchain"
	"core/constants"
	"core/models"

	"github.com/okx/go-wallet-sdk/coins/tron/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

const (
	ErrorNone          = ""
	ErrorTimeout       = "timeout"
	ErrorHTTPStatus    = "http_status"
	ErrorRPC           = "rpc_error"
	ErrorDecode        = "decode_error"
	ErrorUnconfigured  = "unconfigured"
	ErrorUnreachable   = "unreachable"
	ErrorStaleHead     = "stale_head"
	ErrorInconsistent  = "inconsistent_head"
	StrategyObserve    = "observe"
	StrategyPreferLive = "prefer_healthy"
	StrategyFailClosed = "fail_closed"
)

type Config struct {
	Timeout       time.Duration
	Interval      time.Duration
	StaleLag      int64
	Strategy      string
	RequireHashes bool
}

type SnapshotStore interface {
	UpsertLatest(context.Context, []models.ProviderHealthSnapshot) error
}

type Service struct {
	Blockchains   *blockchain.ChainFactory
	Store         SnapshotStore
	HTTPClient    *http.Client
	Now           func() time.Time
	Config        Config
	TronEndpoints func(chainName string) []string
}

type target struct {
	ChainID   constants.ChainID
	ChainName string
	Family    string
	URL       string
	Label     string
}

type jsonRPCResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *jsonRPCError   `json:"error"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type ConsistencyReport struct {
	ChainID          constants.ChainID
	ChainName        string
	ReferenceHeight  int64
	MaxLagBlocks     int64
	InconsistentHead bool
	DriftExceeded    bool
	SelectedProvider string
	Evidence         map[string]string
}

type tronEmptyMessage struct{}

func (*tronEmptyMessage) Reset()         {}
func (*tronEmptyMessage) String() string { return "{}" }
func (*tronEmptyMessage) ProtoMessage()  {}

func LoadConfigFromEnv() Config {
	return Config{
		Timeout:       envDuration("PROVIDER_HEALTH_TIMEOUT", 8*time.Second),
		Interval:      envDuration("PROVIDER_HEALTH_INTERVAL", time.Minute),
		StaleLag:      envInt64("PROVIDER_HEALTH_STALE_LAG_BLOCKS", 3),
		Strategy:      normalizeStrategy(os.Getenv("PROVIDER_FAILOVER_STRATEGY")),
		RequireHashes: envBool("PROVIDER_HEALTH_REQUIRE_HASH"),
	}
}

func DefaultTRONEndpoints(chainName string) []string {
	if strings.EqualFold(strings.TrimSpace(chainName), "tron-testnet") {
		raw := strings.TrimSpace(os.Getenv("TRON_TESTNET_GRPC_ENDPOINTS"))
		if raw == "" {
			raw = strings.TrimSpace(os.Getenv("TRON_TESTNET_GRPC_ENDPOINT"))
		}
		if raw == "" {
			return []string{"grpc.nile.trongrid.io:50051"}
		}
		return splitCSV(raw)
	}
	raw := strings.TrimSpace(os.Getenv("TRON_GRPC_ENDPOINTS"))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("TRON_GRPC_ENDPOINT"))
	}
	if raw == "" {
		return []string{"grpc.trongrid.io:50051"}
	}
	return splitCSV(raw)
}

func New(blockchains *blockchain.ChainFactory, store SnapshotStore, cfg Config) *Service {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 8 * time.Second
	}
	if cfg.Interval <= 0 {
		cfg.Interval = time.Minute
	}
	if cfg.StaleLag <= 0 {
		cfg.StaleLag = 3
	}
	cfg.Strategy = normalizeStrategy(cfg.Strategy)
	return &Service{
		Blockchains:   blockchains,
		Store:         store,
		HTTPClient:    &http.Client{Timeout: cfg.Timeout},
		Now:           func() time.Time { return time.Now().UTC() },
		Config:        cfg,
		TronEndpoints: DefaultTRONEndpoints,
	}
}

func (s *Service) RunOnce(ctx context.Context) ([]models.ProviderHealthSnapshot, error) {
	if s == nil || s.Blockchains == nil {
		return nil, errors.New("blockchain factory is not configured")
	}
	targets := s.discoverTargets()
	snapshots := make([]models.ProviderHealthSnapshot, 0, len(targets))
	for _, t := range targets {
		snapshots = append(snapshots, s.probe(ctx, t))
	}
	snapshots = FinalizeSnapshots(snapshots, s.Config)
	if s.Store != nil {
		if err := s.Store.UpsertLatest(ctx, snapshots); err != nil {
			return snapshots, err
		}
	}
	return snapshots, nil
}

func (s *Service) discoverTargets() []target {
	names := s.Blockchains.ListChains()
	targets := make([]target, 0, len(names)*2)
	for _, name := range names {
		chain, err := s.Blockchains.GetChain(name)
		if err != nil {
			continue
		}
		family := ChainFamily(chain.ChainID())
		urls := chain.RPCs()
		if constants.IsTRONChain(chain.ChainID()) && s.TronEndpoints != nil {
			urls = s.TronEndpoints(chain.Name())
		}
		if len(urls) == 0 {
			targets = append(targets, target{ChainID: chain.ChainID(), ChainName: chain.Name(), Family: family, URL: "", Label: chain.Name() + ":unconfigured"})
			continue
		}
		for i, raw := range urls {
			targets = append(targets, target{
				ChainID:   chain.ChainID(),
				ChainName: chain.Name(),
				Family:    family,
				URL:       strings.TrimSpace(raw),
				Label:     fmt.Sprintf("%s:%s:%d", chain.Name(), family, i+1),
			})
		}
	}
	return targets
}

func ChainFamily(chainID constants.ChainID) string {
	switch chainID {
	case constants.Bitcoin:
		return "bitcoin"
	case constants.Solana:
		return "solana"
	case constants.TRON, constants.TRONTestnet:
		return "tron"
	default:
		return "evm"
	}
}

func (s *Service) probe(ctx context.Context, t target) models.ProviderHealthSnapshot {
	now := s.now()
	start := now
	snapshot := models.ProviderHealthSnapshot{
		ChainID:         t.ChainID,
		ChainName:       t.ChainName,
		ProviderLabel:   RedactedEndpointLabel(t.URL, t.Label),
		ProviderURLHash: URLHash(t.URL),
		Status:          models.ProviderHealthStatusUnknown,
		CheckedAt:       now,
	}
	if strings.TrimSpace(t.URL) == "" {
		snapshot.ErrorCategory = ErrorUnconfigured
		snapshot.ErrorDetail = "no provider endpoint configured"
		snapshot.Status = models.ProviderHealthStatusUnhealthy
		snapshot.ConsecutiveFailures = 1
		return snapshot
	}
	probeCtx, cancel := context.WithTimeout(ctx, s.Config.Timeout)
	defer cancel()
	var height int64
	var hash string
	var err error
	switch t.Family {
	case "bitcoin":
		height, err = s.probeBitcoin(probeCtx, t.URL)
	case "solana":
		height, err = s.probeSolana(probeCtx, t.URL)
	case "tron":
		height, err = s.probeTRON(probeCtx, t.URL)
	default:
		height, hash, err = s.probeEVM(probeCtx, t.URL)
	}
	snapshot.ResponseLatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		snapshot.ErrorCategory = ClassifyError(err)
		snapshot.ErrorDetail = RedactError(err.Error())
		snapshot.Status = models.ProviderHealthStatusUnhealthy
		snapshot.ConsecutiveFailures = 1
		return snapshot
	}
	snapshot.Reachable = true
	snapshot.Status = models.ProviderHealthStatusHealthy
	snapshot.LatestHeight = height
	snapshot.HeadHash = hash
	return snapshot
}

func (s *Service) probeEVM(ctx context.Context, endpoint string) (int64, string, error) {
	var block struct {
		Number string `json:"number"`
		Hash   string `json:"hash"`
	}
	if err := s.jsonRPC(ctx, endpoint, "eth_getBlockByNumber", []any{"latest", false}, &block); err != nil {
		return 0, "", err
	}
	height, err := parseHexInt(block.Number)
	if err != nil {
		return 0, "", err
	}
	if height <= 0 {
		return 0, "", fmt.Errorf("latest block is not positive: %d", height)
	}
	return height, strings.TrimSpace(block.Hash), nil
}

func (s *Service) probeSolana(ctx context.Context, endpoint string) (int64, error) {
	var slot int64
	if err := s.jsonRPC(ctx, endpoint, "getSlot", []any{map[string]any{"commitment": "finalized"}}, &slot); err != nil {
		return 0, err
	}
	if slot <= 0 {
		return 0, fmt.Errorf("latest slot is not positive: %d", slot)
	}
	return slot, nil
}

func (s *Service) probeBitcoin(ctx context.Context, endpoint string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/blocks/tip/height", nil)
	if err != nil {
		return 0, err
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return 0, err
	}
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		return 0, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("provider returned HTTP %d", resp.StatusCode)
	}
	height, err := strconv.ParseInt(strings.TrimSpace(string(body)), 10, 64)
	if err != nil {
		return 0, err
	}
	if height <= 0 {
		return 0, fmt.Errorf("latest bitcoin height is not positive: %d", height)
	}
	return height, nil
}

func (s *Service) probeTRON(ctx context.Context, endpoint string) (int64, error) {
	endpoint = strings.TrimPrefix(strings.TrimSpace(endpoint), "grpc://")
	apiKey := strings.TrimSpace(os.Getenv("TRON_PRO_API_KEY"))
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return 0, err
	}
	defer func() { _ = conn.Close() }()
	callCtx := ctx
	if apiKey != "" {
		callCtx = metadata.NewOutgoingContext(callCtx, metadata.Pairs("TRON-PRO-API-KEY", apiKey))
	}
	out := new(pb.Block)
	if err := conn.Invoke(callCtx, "/protocol.Wallet/GetNowBlock", &tronEmptyMessage{}, out, grpc.MaxCallRecvMsgSize(32*1024*1024)); err != nil {
		return 0, err
	}
	height := out.GetBlockHeader().GetRawData().GetNumber()
	if height <= 0 {
		return 0, fmt.Errorf("latest tron block is not positive: %d", height)
	}
	return height, nil
}

func (s *Service) jsonRPC(ctx context.Context, endpoint string, method string, params []any, out any) error {
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      time.Now().UnixNano(),
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client().Do(req)
	if err != nil {
		return err
	}
	respBody, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		return readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("provider returned HTTP %d", resp.StatusCode)
	}
	var rpcResp jsonRPCResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return err
	}
	if rpcResp.Error != nil {
		return fmt.Errorf("RPC %s error %d: %s", method, rpcResp.Error.Code, rpcResp.Error.Message)
	}
	if out == nil || string(rpcResp.Result) == "null" {
		return nil
	}
	return json.Unmarshal(rpcResp.Result, out)
}

func FinalizeSnapshots(snapshots []models.ProviderHealthSnapshot, cfg Config) []models.ProviderHealthSnapshot {
	if cfg.StaleLag <= 0 {
		cfg.StaleLag = 3
	}
	cfg.Strategy = normalizeStrategy(cfg.Strategy)
	byChain := make(map[constants.ChainID][]int)
	for i := range snapshots {
		byChain[snapshots[i].ChainID] = append(byChain[snapshots[i].ChainID], i)
	}
	for _, indexes := range byChain {
		reference := int64(0)
		hashesByHeight := make(map[int64]map[string]int)
		for _, idx := range indexes {
			s := snapshots[idx]
			if s.Reachable && s.LatestHeight > reference {
				reference = s.LatestHeight
			}
			if s.Reachable && s.LatestHeight > 0 && strings.TrimSpace(s.HeadHash) != "" {
				if hashesByHeight[s.LatestHeight] == nil {
					hashesByHeight[s.LatestHeight] = map[string]int{}
				}
				hashesByHeight[s.LatestHeight][s.HeadHash]++
			}
		}
		for _, idx := range indexes {
			s := &snapshots[idx]
			if !s.Reachable {
				s.Status = models.ProviderHealthStatusUnhealthy
				continue
			}
			s.LagFromReference = reference - s.LatestHeight
			if s.LagFromReference < 0 {
				s.LagFromReference = 0
			}
			switch {
			case len(hashesByHeight[s.LatestHeight]) > 1:
				s.Status = models.ProviderHealthStatusUnhealthy
				s.ErrorCategory = ErrorInconsistent
				s.ErrorDetail = "providers disagree on same-height head hash"
				s.ConsecutiveFailures = 1
			case s.LagFromReference > cfg.StaleLag:
				s.Status = models.ProviderHealthStatusDegraded
				s.ErrorCategory = ErrorStaleHead
				s.ErrorDetail = "provider head lags reference"
			case cfg.RequireHashes && s.HeadHash == "":
				s.Status = models.ProviderHealthStatusDegraded
				s.ErrorCategory = ErrorInconsistent
				s.ErrorDetail = "head hash evidence unavailable"
			default:
				s.Status = models.ProviderHealthStatusHealthy
				s.ErrorCategory = ""
				s.ErrorDetail = ""
				s.ConsecutiveFailures = 0
			}
		}
		selected := selectProviderIndex(snapshots, indexes)
		for pos, idx := range indexes {
			snapshots[idx].Selected = idx == selected
			if idx == selected && pos > 0 && cfg.Strategy != StrategyObserve {
				snapshots[idx].FailoverReason = "primary_not_selected"
			} else if idx == selected {
				snapshots[idx].FailoverReason = "primary"
			} else if snapshots[idx].Status != models.ProviderHealthStatusHealthy {
				snapshots[idx].FailoverReason = snapshots[idx].ErrorCategory
			}
		}
	}
	return snapshots
}

func selectProviderIndex(snapshots []models.ProviderHealthSnapshot, indexes []int) int {
	if len(indexes) == 0 {
		return -1
	}
	best := indexes[0]
	for _, idx := range indexes[1:] {
		if snapshotLess(snapshots[idx], snapshots[best]) {
			best = idx
		}
	}
	return best
}

func snapshotLess(left, right models.ProviderHealthSnapshot) bool {
	leftRank := statusRank(left.Status)
	rightRank := statusRank(right.Status)
	if leftRank != rightRank {
		return leftRank < rightRank
	}
	if left.LatestHeight != right.LatestHeight {
		return left.LatestHeight > right.LatestHeight
	}
	if left.ResponseLatencyMS != right.ResponseLatencyMS {
		return left.ResponseLatencyMS < right.ResponseLatencyMS
	}
	return left.ProviderLabel < right.ProviderLabel
}

func statusRank(status string) int {
	switch status {
	case models.ProviderHealthStatusHealthy:
		return 0
	case models.ProviderHealthStatusDegraded:
		return 1
	case models.ProviderHealthStatusUnknown:
		return 2
	default:
		return 3
	}
}

func RankURLs(chainID constants.ChainID, chainName string, urls []string, snapshots []models.ProviderHealthSnapshot, strategy string) []string {
	if normalizeStrategy(strategy) == StrategyObserve || len(urls) <= 1 {
		return cloneStrings(urls)
	}
	byHash := make(map[string]models.ProviderHealthSnapshot, len(snapshots))
	for _, snapshot := range snapshots {
		if snapshot.ChainID == chainID || strings.EqualFold(snapshot.ChainName, chainName) {
			byHash[snapshot.ProviderURLHash] = snapshot
		}
	}
	type ranked struct {
		url      string
		snapshot models.ProviderHealthSnapshot
		has      bool
		order    int
	}
	items := make([]ranked, 0, len(urls))
	for i, raw := range urls {
		snapshot, ok := byHash[URLHash(raw)]
		items = append(items, ranked{url: raw, snapshot: snapshot, has: ok, order: i})
	}
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if left.has != right.has {
			return left.has
		}
		if !left.has {
			return left.order < right.order
		}
		if snapshotLess(left.snapshot, right.snapshot) {
			return true
		}
		if snapshotLess(right.snapshot, left.snapshot) {
			return false
		}
		return left.order < right.order
	})
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.url)
	}
	return out
}

func ConsistencyReports(snapshots []models.ProviderHealthSnapshot, cfg Config) []ConsistencyReport {
	if cfg.StaleLag <= 0 {
		cfg.StaleLag = 3
	}
	byChain := make(map[constants.ChainID][]models.ProviderHealthSnapshot)
	for _, snapshot := range snapshots {
		byChain[snapshot.ChainID] = append(byChain[snapshot.ChainID], snapshot)
	}
	ids := make([]constants.ChainID, 0, len(byChain))
	for chainID := range byChain {
		ids = append(ids, chainID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	reports := make([]ConsistencyReport, 0, len(ids))
	for _, chainID := range ids {
		group := byChain[chainID]
		report := ConsistencyReport{
			ChainID:  chainID,
			Evidence: map[string]string{},
		}
		for _, snapshot := range group {
			if report.ChainName == "" {
				report.ChainName = snapshot.ChainName
			}
			if snapshot.Reachable && snapshot.LatestHeight > report.ReferenceHeight {
				report.ReferenceHeight = snapshot.LatestHeight
			}
			if snapshot.Selected {
				report.SelectedProvider = snapshot.ProviderLabel
			}
			if snapshot.ErrorCategory == ErrorInconsistent {
				report.InconsistentHead = true
				report.Evidence["error_category"] = ErrorInconsistent
				report.Evidence["error_detail"] = snapshot.ErrorDetail
			}
		}
		for _, snapshot := range group {
			if !snapshot.Reachable {
				continue
			}
			lag := report.ReferenceHeight - snapshot.LatestHeight
			if lag > report.MaxLagBlocks {
				report.MaxLagBlocks = lag
			}
		}
		if report.MaxLagBlocks > cfg.StaleLag {
			report.DriftExceeded = true
			if report.Evidence["error_category"] == "" {
				report.Evidence["error_category"] = ErrorStaleHead
				report.Evidence["error_detail"] = "provider head lags reference"
			}
		}
		reports = append(reports, report)
	}
	return reports
}

func URLHash(raw string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	return hex.EncodeToString(sum[:])
}

func RedactedEndpointLabel(raw string, fallback string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return strings.TrimSpace(fallback)
	}
	parsed, err := url.Parse(raw)
	if err == nil && parsed.Host != "" {
		host := parsed.Hostname()
		port := parsed.Port()
		if host != "" && port != "" {
			return net.JoinHostPort(host, port)
		}
		if host != "" {
			return host
		}
	}
	if host, _, err := net.SplitHostPort(strings.TrimPrefix(raw, "grpc://")); err == nil && host != "" {
		return host
	}
	if strings.Contains(raw, "?") {
		raw = strings.Split(raw, "?")[0]
	}
	raw = strings.TrimPrefix(raw, "grpc://")
	raw = strings.Trim(raw, "/")
	if raw == "" {
		return strings.TrimSpace(fallback)
	}
	return raw
}

func RedactError(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) > 480 {
		value = value[:480] + "..."
	}
	if strings.Contains(value, "://") {
		return "provider request failed"
	}
	return value
}

func ClassifyError(err error) string {
	if err == nil {
		return ErrorNone
	}
	if errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err) {
		return ErrorTimeout
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "http "):
		return ErrorHTTPStatus
	case strings.Contains(msg, "rpc "):
		return ErrorRPC
	case strings.Contains(msg, "json") || strings.Contains(msg, "decode") || strings.Contains(msg, "parse"):
		return ErrorDecode
	case strings.Contains(msg, "no provider") || strings.Contains(msg, "not configured"):
		return ErrorUnconfigured
	default:
		return ErrorUnreachable
	}
}

func (s *Service) client() *http.Client {
	if s.HTTPClient != nil {
		return s.HTTPClient
	}
	return &http.Client{Timeout: s.Config.Timeout}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func parseHexInt(raw string) (int64, error) {
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "0x"))
	if raw == "" {
		return 0, errors.New("empty hex number")
	}
	return strconv.ParseInt(raw, 16, 64)
}

func envDuration(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
		return parsed
	}
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return fallback
}

func envInt64(key string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func envBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func normalizeStrategy(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", StrategyPreferLive:
		return StrategyPreferLive
	case StrategyObserve:
		return StrategyObserve
	case StrategyFailClosed:
		return StrategyFailClosed
	default:
		return StrategyPreferLive
	}
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		part = strings.TrimPrefix(part, "grpc://")
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func cloneStrings(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	return out
}
