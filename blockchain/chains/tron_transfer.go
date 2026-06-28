package chains

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	blockchain "core/blockchain"
	"core/services/chainresource"

	"github.com/btcsuite/btcd/btcec/v2"
	tronSDK "github.com/okx/go-wallet-sdk/coins/tron"
)

// tronAPIBase derives the TRON full-node HTTP API base URL from the JSON-RPC URL list.
func tronAPIBase(rpcURLs []string) string {
	for _, u := range normalizeTronHTTPAPIEndpoints(rpcURLs) {
		return u
	}
	return "https://api.trongrid.io"
}

func normalizeTronHTTPAPIEndpoints(rawEndpoints []string) []string {
	out := make([]string, 0, len(rawEndpoints))
	seen := make(map[string]struct{}, len(rawEndpoints))
	for _, endpoint := range rawEndpoints {
		endpoint = strings.TrimSuffix(strings.TrimSpace(endpoint), "/")
		endpoint = strings.TrimSuffix(endpoint, "/jsonrpc")
		if endpoint == "" {
			continue
		}
		if _, ok := seen[endpoint]; ok {
			continue
		}
		seen[endpoint] = struct{}{}
		out = append(out, endpoint)
	}
	return out
}

func (s *TronChain) httpAPIEndpoints() []string {
	return tronHTTPAPIEndpointsForChain(s.Name(), s.RPCs())
}

type tronBlockRef struct {
	refBlockBytes string
	refBlockHash  string
	expiration    int64
	timestamp     int64
}

func tronGetBlockRef(ctx context.Context, apiBase string) (*tronBlockRef, error) {
	reqBody := []byte(`{}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+"/wallet/getnowblock", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tron getnowblock: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("tron getnowblock HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result struct {
		BlockID     string `json:"blockID"`
		BlockHeader struct {
			RawData struct {
				Number int64 `json:"number"`
			} `json:"raw_data"`
		} `json:"block_header"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("tron block parse: %w", err)
	}

	blockNum := result.BlockHeader.RawData.Number
	blockHashBytes, err := hex.DecodeString(result.BlockID)
	if err != nil {
		return nil, fmt.Errorf("tron blockID decode: %w", err)
	}

	numBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(numBytes, uint64(blockNum))

	now := time.Now().UnixMilli()
	return &tronBlockRef{
		refBlockBytes: hex.EncodeToString(numBytes[6:8]),
		refBlockHash:  hex.EncodeToString(blockHashBytes[8:16]),
		expiration:    now + 3600_000, // 1 hour
		timestamp:     now,
	}, nil
}

func tronGetTRXBalance(ctx context.Context, rpcURL, address string) (int64, error) {
	apiBase := strings.TrimRight(strings.TrimSpace(rpcURL), "/")
	apiBase = strings.TrimSuffix(apiBase, "/jsonrpc")
	if apiBase == "" {
		return 0, fmt.Errorf("empty tron HTTP API endpoint")
	}
	addressHash, err := tronSDK.GetAddressHash(address)
	if err != nil {
		return 0, fmt.Errorf("tron address hash: %w", err)
	}
	reqBody, _ := json.Marshal(map[string]interface{}{
		"address": hex.EncodeToString(addressHash),
	})

	endpoint := apiBase + "/wallet/getaccount"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey := strings.TrimSpace(os.Getenv("TRON_PRO_API_KEY")); apiKey != "" {
		req.Header.Set("TRON-PRO-API-KEY", apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("tron getaccount: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("tron getaccount HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var res struct {
		Balance  json.Number     `json:"balance"`
		Error    json.RawMessage `json:"Error"`
		RPCError json.RawMessage `json:"error"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&res); err != nil {
		return 0, err
	}
	if len(res.Error) > 0 && string(res.Error) != "null" {
		return 0, fmt.Errorf("tron getaccount error: %s", strings.TrimSpace(string(res.Error)))
	}
	if len(res.RPCError) > 0 && string(res.RPCError) != "null" {
		return 0, fmt.Errorf("tron getaccount error: %s", strings.TrimSpace(string(res.RPCError)))
	}
	balance := strings.TrimSpace(res.Balance.String())
	if balance == "" {
		return 0, nil
	}
	v, err := strconv.ParseInt(balance, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("tron parse balance SUN: %w", err)
	}
	return v, nil
}

func tronGetTRXBalanceFromRPCs(ctx context.Context, rpcURLs []string, address string) (int64, error) {
	var lastErr error
	for _, rpcURL := range normalizeTronHTTPAPIEndpoints(rpcURLs) {
		rpcURL = strings.TrimSpace(rpcURL)
		if rpcURL == "" {
			continue
		}

		balance, err := tronGetTRXBalance(ctx, rpcURL, address)
		if err == nil {
			return balance, nil
		}
		lastErr = fmt.Errorf("%s tron TRX balance failed: %w", rpcURL, err)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no tron RPC endpoint configured")
	}
	return 0, lastErr
}

type tronAccountResource struct {
	FreeNetLimit json.Number `json:"freeNetLimit"`
	FreeNetUsed  json.Number `json:"freeNetUsed"`
	NetLimit     json.Number `json:"NetLimit"`
	NetUsed      json.Number `json:"NetUsed"`
}

func tronJSONNumberInt64(value json.Number) int64 {
	raw := strings.TrimSpace(value.String())
	if raw == "" {
		return 0
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

func (r tronAccountResource) availableBandwidth() int64 {
	free := tronJSONNumberInt64(r.FreeNetLimit) - tronJSONNumberInt64(r.FreeNetUsed)
	staked := tronJSONNumberInt64(r.NetLimit) - tronJSONNumberInt64(r.NetUsed)
	if free < 0 {
		free = 0
	}
	if staked < 0 {
		staked = 0
	}
	return free + staked
}

func tronGetAccountResource(ctx context.Context, rpcURL, address string) (*tronAccountResource, error) {
	apiBase := strings.TrimRight(strings.TrimSpace(rpcURL), "/")
	apiBase = strings.TrimSuffix(apiBase, "/jsonrpc")
	if apiBase == "" {
		return nil, fmt.Errorf("empty tron HTTP API endpoint")
	}
	addressHash, err := tronSDK.GetAddressHash(address)
	if err != nil {
		return nil, fmt.Errorf("tron address hash: %w", err)
	}
	reqBody, _ := json.Marshal(map[string]interface{}{
		"address": hex.EncodeToString(addressHash),
	})

	endpoint := apiBase + "/wallet/getaccountresource"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey := strings.TrimSpace(os.Getenv("TRON_PRO_API_KEY")); apiKey != "" {
		req.Header.Set("TRON-PRO-API-KEY", apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tron getaccountresource: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("tron getaccountresource HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var res tronAccountResource
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&res); err != nil {
		return nil, err
	}
	return &res, nil
}

func tronGetAccountResourceFromRPCs(ctx context.Context, rpcURLs []string, address string) (*tronAccountResource, error) {
	var lastErr error
	for _, rpcURL := range normalizeTronHTTPAPIEndpoints(rpcURLs) {
		rpcURL = strings.TrimSpace(rpcURL)
		if rpcURL == "" {
			continue
		}
		resource, err := tronGetAccountResource(ctx, rpcURL, address)
		if err == nil {
			return resource, nil
		}
		lastErr = fmt.Errorf("%s tron account resource failed: %w", rpcURL, err)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no tron RPC endpoint configured")
	}
	return nil, lastErr
}

func tronEstimateBandwidthFeeSUN(ctx context.Context, rpcURLs []string, ownerAddress string, signedTxHex string) (int64, error) {
	txHex := strings.TrimPrefix(strings.TrimSpace(signedTxHex), "0x")
	if txHex == "" || len(txHex)%2 != 0 {
		return 0, fmt.Errorf("invalid signed tron transaction hex")
	}
	resource, err := tronGetAccountResourceFromRPCs(ctx, rpcURLs, ownerAddress)
	if err != nil {
		return 0, err
	}
	txBytes := int64(len(txHex) / 2)
	paidBandwidth := txBytes - resource.availableBandwidth()
	if paidBandwidth <= 0 {
		return 0, nil
	}
	return paidBandwidth * 1000, nil
}

func tronGetTRC20Balance(ctx context.Context, rpcURL, contractAddr, ownerAddr string) (*big.Int, error) {
	// Use eth_call with balanceOf(address) ABI encoding
	// keccak256("balanceOf(address)") = 0x70a08231
	ownerHash, err := tronSDK.GetAddressHash(ownerAddr)
	if err != nil {
		return nil, fmt.Errorf("tron address hash: %w", err)
	}
	// Pad to 32 bytes
	padded := make([]byte, 32)
	copy(padded[12:], ownerHash[1:]) // strip leading 0x41 TRON prefix, use last 20 bytes

	// eth_call data: 0x70a08231 + padded address
	callData := "0x70a08231" + hex.EncodeToString(padded)

	// Convert TRON base58 contract address to hex EVM format
	contractHash, err := tronSDK.GetAddressHash(contractAddr)
	if err != nil {
		return nil, fmt.Errorf("tron contract address hash: %w", err)
	}
	contractHex := "0x" + hex.EncodeToString(contractHash[1:]) // strip 0x41 prefix

	reqBody, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "eth_call",
		"params": []interface{}{
			map[string]string{"to": contractHex, "data": callData},
			"latest",
		},
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("tron eth_call HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var res struct {
		Result string          `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, err
	}
	if len(res.Error) > 0 && string(res.Error) != "null" {
		return nil, fmt.Errorf("tron eth_call rpc error: %s", strings.TrimSpace(string(res.Error)))
	}

	hexStr := strings.TrimPrefix(strings.TrimSpace(res.Result), "0x")
	if hexStr == "" || hexStr == "0" {
		return big.NewInt(0), nil
	}
	v, ok := new(big.Int).SetString(hexStr, 16)
	if !ok {
		return nil, fmt.Errorf("tron parse TRC20 balance hex: %s", res.Result)
	}
	return v, nil
}

func tronGetTRC20BalanceFromRPCs(ctx context.Context, rpcURLs []string, contractAddr, ownerAddr string) (*big.Int, error) {
	var lastErr error
	for _, rpcURL := range rpcURLs {
		rpcURL = strings.TrimSpace(rpcURL)
		if rpcURL == "" {
			continue
		}

		balance, err := tronGetTRC20Balance(ctx, rpcURL, contractAddr, ownerAddr)
		if err == nil {
			return balance, nil
		}
		lastErr = fmt.Errorf("%s tron TRC20 balance failed: %w", rpcURL, err)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no tron RPC endpoint configured")
	}
	return nil, lastErr
}

func tronBroadcast(ctx context.Context, apiBase, signedTxHex string) (string, error) {
	reqBody, _ := json.Marshal(map[string]string{"transaction": signedTxHex})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+"/wallet/broadcasthex", bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("tron broadcast: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("tron broadcast HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var res struct {
		Result  bool   `json:"result"`
		Txid    string `json:"txid"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return "", fmt.Errorf("tron broadcast response parse: %w", err)
	}
	if !res.Result {
		return "", fmt.Errorf("tron broadcast failed: %s", res.Message)
	}
	return res.Txid, nil
}

func tronPrivateKey(wallet blockchain.WalletDetails) (*btcec.PrivateKey, error) {
	privKeyBytes, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(wallet.PrivateKey), "0x"))
	if err != nil {
		return nil, fmt.Errorf("invalid tron private key hex: %w", err)
	}
	privKey, pubKey := btcec.PrivKeyFromBytes(privKeyBytes)
	if privKey == nil {
		return nil, fmt.Errorf("failed to parse tron private key")
	}
	if wallet.Address != "" {
		derivedAddress := tronSDK.GetAddress(pubKey)
		if derivedAddress != wallet.Address {
			return nil, fmt.Errorf("private key does not match wallet address: expected %s got %s", wallet.Address, derivedAddress)
		}
	}
	return privKey, nil
}

func tronSignRawTransaction(rawTxHex string, privKey *btcec.PrivateKey) (string, error) {
	dataToSign, err := tronSDK.SignStart(rawTxHex)
	if err != nil {
		return "", fmt.Errorf("tron sign start: %w", err)
	}
	sigStr, err := tronSDK.Sign(dataToSign, privKey)
	if err != nil {
		return "", fmt.Errorf("tron sign: %w", err)
	}
	signedTx, err := tronSDK.SignEnd(rawTxHex, sigStr)
	if err != nil {
		return "", fmt.Errorf("tron sign end: %w", err)
	}
	return signedTx, nil
}

func tronSignAndBroadcast(ctx context.Context, apiBase, rawTxHex string, privKey *btcec.PrivateKey) (string, error) {
	signedTx, err := tronSignRawTransaction(rawTxHex, privKey)
	if err != nil {
		return "", err
	}
	return tronBroadcast(ctx, apiBase, signedTx)
}

func tronSignBroadcastWithLease(ctx context.Context, apiBase, rawTxHex string, privKey *btcec.PrivateKey, lease *chainresource.SequenceLease) (string, error) {
	signedTx, err := tronSignRawTransaction(rawTxHex, privKey)
	if err != nil {
		_ = lease.Release()
		return "", err
	}
	txID, err := tronBroadcast(ctx, apiBase, signedTx)
	if err != nil {
		_ = lease.Consume("")
		return "", err
	}
	_ = lease.Consume(txID)
	return txID, nil
}

func tronBroadcastSignedWithLease(ctx context.Context, apiBase, signedTxHex string, lease *chainresource.SequenceLease) (string, error) {
	txID, err := tronBroadcast(ctx, apiBase, signedTxHex)
	if err != nil {
		_ = lease.Consume("")
		return "", err
	}
	_ = lease.Consume(txID)
	return txID, nil
}

func tronGasThresholdSUN() int64 {
	if raw := strings.TrimSpace(os.Getenv("TRON_GAS_THRESHOLD_SUN")); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			return v
		}
	}
	if feeLimit, err := chainresource.TronTRC20FeeLimitSUN(); err == nil && feeLimit > 0 {
		return feeLimit
	}
	return 50_000_000 // 50 TRX in SUN
}

func tronGasPrefundSUN() int64 {
	if raw := strings.TrimSpace(os.Getenv("TRON_GAS_PREFUND_SUN")); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			return v
		}
	}
	return tronGasThresholdSUN()
}

func (s *TronChain) sendTRX(ctx context.Context, wallet blockchain.WalletDetails, amountRaw string, toAddress string) (*blockchain.TransactionResult, error) {
	if !s.ValidateAddress(toAddress) {
		return nil, fmt.Errorf("invalid tron address: %s", toAddress)
	}
	if !s.ValidateAddress(wallet.Address) {
		return nil, fmt.Errorf("invalid tron wallet address: %s", wallet.Address)
	}
	amount, err := nativeAmountRaw(amountRaw)
	if err != nil {
		return nil, err
	}
	if !amount.IsInt64() {
		return nil, fmt.Errorf("tron amount_raw exceeds int64 SUN")
	}
	sendAmount := amount.Int64()
	if err := authorizeWalletSigning(ctx, s.Name(), s.ChainID(), wallet, "transfer.native", amount.String(), toAddress); err != nil {
		return nil, err
	}

	rpcs := s.httpAPIEndpoints()
	if len(rpcs) == 0 {
		return nil, fmt.Errorf("no tron RPC endpoint configured")
	}
	apiBase := tronAPIBase(rpcs)

	lease, err := chainResourceSequenceLease(ctx, s.Name(), wallet, "transfer.native")
	if err != nil {
		return nil, fmt.Errorf("tron resource reservation failed: %w", err)
	}

	blockRef, err := tronGetBlockRef(ctx, apiBase)
	if err != nil {
		_ = lease.Release()
		return nil, err
	}
	rawTx, err := tronSDK.NewTransfer(wallet.Address, toAddress, sendAmount,
		blockRef.refBlockBytes, blockRef.refBlockHash,
		blockRef.expiration, blockRef.timestamp)
	if err != nil {
		_ = lease.Release()
		return nil, fmt.Errorf("tron build transfer tx: %w", err)
	}
	privKey, err := tronPrivateKey(wallet)
	if err != nil {
		_ = lease.Release()
		return nil, err
	}
	txID, err := tronSignBroadcastWithLease(ctx, apiBase, rawTx, privKey, lease)
	if err != nil {
		return nil, err
	}
	return &blockchain.TransactionResult{TxHash: txID, Success: true}, nil
}

func (s *TronChain) sendTRC20(ctx context.Context, wallet blockchain.WalletDetails, contractAddr, amountRaw, toAddress string) (*blockchain.TransactionResult, error) {
	if !s.ValidateAddress(wallet.Address) {
		return nil, fmt.Errorf("invalid tron wallet address: %s", wallet.Address)
	}
	if !s.ValidateAddress(contractAddr) {
		return nil, fmt.Errorf("invalid TRC-20 contract address: %s", contractAddr)
	}
	if !s.ValidateAddress(toAddress) {
		return nil, fmt.Errorf("invalid tron destination address: %s", toAddress)
	}
	amount, err := nativeAmountRaw(amountRaw)
	if err != nil {
		return nil, err
	}
	if err := authorizeWalletSigning(ctx, s.Name(), s.ChainID(), wallet, "transfer.token", amount.String(), toAddress); err != nil {
		return nil, err
	}

	rpcs := s.httpAPIEndpoints()
	if len(rpcs) == 0 {
		return nil, fmt.Errorf("no tron RPC endpoint configured")
	}
	apiBase := tronAPIBase(rpcs)

	balance, err := tronGetTRC20BalanceFromRPCs(ctx, rpcs, contractAddr, wallet.Address)
	if err != nil {
		return nil, fmt.Errorf("tron TRC-20 balance: %w", err)
	}
	if balance == nil || balance.Cmp(amount) < 0 {
		return nil, fmt.Errorf("tron TRC-20 balance is not enough: balance=%s amount=%s", balance.String(), amount.String())
	}

	lease, err := chainResourceSequenceLease(ctx, s.Name(), wallet, "transfer.token")
	if err != nil {
		return nil, fmt.Errorf("tron resource reservation failed: %w", err)
	}

	blockRef, err := tronGetBlockRef(ctx, apiBase)
	if err != nil {
		_ = lease.Release()
		return nil, err
	}

	trc20FeeLimit, err := chainresource.TronTRC20FeeLimitSUN()
	if err != nil {
		_ = lease.Release()
		return nil, err
	}
	rawTx, err := tronSDK.NewTRC20TokenTransfer(
		wallet.Address, toAddress, contractAddr,
		amount, trc20FeeLimit,
		blockRef.refBlockBytes, blockRef.refBlockHash,
		blockRef.expiration, blockRef.timestamp)
	if err != nil {
		_ = lease.Release()
		return nil, fmt.Errorf("tron build TRC-20 tx: %w", err)
	}

	privKey, err := tronPrivateKey(wallet)
	if err != nil {
		_ = lease.Release()
		return nil, err
	}
	txID, err := tronSignBroadcastWithLease(ctx, apiBase, rawTx, privKey, lease)
	if err != nil {
		return nil, err
	}
	return &blockchain.TransactionResult{TxHash: txID, Success: true}, nil
}

func (s *TronChain) SweepTo(ctx context.Context, wallet blockchain.WalletDetails, toAddress string) (*blockchain.TransactionResult, error) {
	if !s.ValidateAddress(wallet.Address) {
		return nil, fmt.Errorf("invalid tron wallet address: %s", wallet.Address)
	}
	if !s.ValidateAddress(toAddress) {
		return nil, fmt.Errorf("invalid tron address: %s", toAddress)
	}
	if err := authorizeWalletSigning(ctx, s.Name(), s.ChainID(), wallet, "sweep.native", "max", toAddress); err != nil {
		return nil, err
	}

	rpcs := s.httpAPIEndpoints()
	if len(rpcs) == 0 {
		return nil, fmt.Errorf("no tron RPC endpoint configured")
	}
	apiBase := tronAPIBase(rpcs)

	balance, err := tronGetTRXBalanceFromRPCs(ctx, rpcs, wallet.Address)
	if err != nil {
		return nil, fmt.Errorf("tron TRX balance: %w", err)
	}

	fallbackFeeSUN, err := chainresource.TronNativeSweepFeeSUN()
	if err != nil {
		return nil, err
	}

	lease, err := chainResourceSequenceLease(ctx, s.Name(), wallet, "sweep.native")
	if err != nil {
		return nil, fmt.Errorf("tron resource reservation failed: %w", err)
	}

	blockRef, err := tronGetBlockRef(ctx, apiBase)
	if err != nil {
		_ = lease.Release()
		return nil, err
	}
	privKey, err := tronPrivateKey(wallet)
	if err != nil {
		_ = lease.Release()
		return nil, err
	}

	feeSUN := int64(0)
	var signedTx string
	var sendAmount int64
	for attempt := 0; attempt < 4; attempt++ {
		sendAmount = balance - feeSUN
		if sendAmount <= 0 {
			_ = lease.Release()
			return nil, fmt.Errorf("tron sweep balance not enough: balance=%d sun fee=%d sun", balance, feeSUN)
		}
		rawTx, err := tronSDK.NewTransfer(wallet.Address, toAddress, sendAmount,
			blockRef.refBlockBytes, blockRef.refBlockHash,
			blockRef.expiration, blockRef.timestamp)
		if err != nil {
			_ = lease.Release()
			return nil, fmt.Errorf("tron build transfer tx: %w", err)
		}
		signedTx, err = tronSignRawTransaction(rawTx, privKey)
		if err != nil {
			_ = lease.Release()
			return nil, err
		}
		estimatedFeeSUN, err := tronEstimateBandwidthFeeSUN(ctx, rpcs, wallet.Address, signedTx)
		if err != nil {
			if feeSUN == fallbackFeeSUN {
				break
			}
			feeSUN = fallbackFeeSUN
			continue
		}
		if estimatedFeeSUN == feeSUN {
			break
		}
		feeSUN = estimatedFeeSUN
	}

	txID, err := tronBroadcastSignedWithLease(ctx, apiBase, signedTx, lease)
	if err != nil {
		return nil, err
	}
	return &blockchain.TransactionResult{TxHash: txID, Success: true}, nil
}

func (s *TronChain) SweepERC20To(ctx context.Context, wallet blockchain.WalletDetails, contractAddr, toAddress string) (*blockchain.TransactionResult, error) {
	if !s.ValidateAddress(wallet.Address) {
		return nil, fmt.Errorf("invalid tron wallet address: %s", wallet.Address)
	}
	if !s.ValidateAddress(contractAddr) {
		return nil, fmt.Errorf("invalid TRC-20 contract address: %s", contractAddr)
	}
	if !s.ValidateAddress(toAddress) {
		return nil, fmt.Errorf("invalid tron destination address: %s", toAddress)
	}
	if err := authorizeWalletSigning(ctx, s.Name(), s.ChainID(), wallet, "sweep.token", "max", toAddress); err != nil {
		return nil, err
	}

	rpcs := s.httpAPIEndpoints()
	if len(rpcs) == 0 {
		return nil, fmt.Errorf("no tron RPC endpoint configured")
	}
	apiBase := tronAPIBase(rpcs)

	balance, err := tronGetTRC20BalanceFromRPCs(ctx, rpcs, contractAddr, wallet.Address)
	if err != nil {
		return nil, fmt.Errorf("tron TRC-20 balance: %w", err)
	}
	if balance == nil || balance.Sign() <= 0 {
		return nil, fmt.Errorf("tron TRC-20 balance is zero for %s", contractAddr)
	}

	lease, err := chainResourceSequenceLease(ctx, s.Name(), wallet, "sweep.token")
	if err != nil {
		return nil, fmt.Errorf("tron resource reservation failed: %w", err)
	}

	blockRef, err := tronGetBlockRef(ctx, apiBase)
	if err != nil {
		_ = lease.Release()
		return nil, err
	}

	trc20FeeLimit, err := chainresource.TronTRC20FeeLimitSUN()
	if err != nil {
		_ = lease.Release()
		return nil, err
	}
	rawTx, err := tronSDK.NewTRC20TokenTransfer(
		wallet.Address, toAddress, contractAddr,
		balance, trc20FeeLimit,
		blockRef.refBlockBytes, blockRef.refBlockHash,
		blockRef.expiration, blockRef.timestamp)
	if err != nil {
		_ = lease.Release()
		return nil, fmt.Errorf("tron build TRC-20 tx: %w", err)
	}

	privKey, err := tronPrivateKey(wallet)
	if err != nil {
		_ = lease.Release()
		return nil, err
	}

	txID, err := tronSignBroadcastWithLease(ctx, apiBase, rawTx, privKey, lease)
	if err != nil {
		return nil, err
	}
	return &blockchain.TransactionResult{TxHash: txID, Success: true}, nil
}

func (s *TronChain) PrefundGas(ctx context.Context, reserveWallet blockchain.WalletDetails, userAddress string) (bool, error) {
	if !s.ValidateAddress(reserveWallet.Address) {
		return false, fmt.Errorf("invalid tron reserve wallet address: %s", reserveWallet.Address)
	}
	if !s.ValidateAddress(userAddress) {
		return false, fmt.Errorf("invalid tron user address: %s", userAddress)
	}
	rpcs := s.httpAPIEndpoints()
	if len(rpcs) == 0 {
		return false, fmt.Errorf("no tron RPC endpoint configured")
	}
	apiBase := tronAPIBase(rpcs)

	balance, err := tronGetTRXBalanceFromRPCs(ctx, rpcs, userAddress)
	if err != nil {
		return false, fmt.Errorf("tron prefund balance check: %w", err)
	}

	thresholdSUN := tronGasThresholdSUN()
	if balance >= thresholdSUN {
		return false, nil
	}
	prefundAmountSUN := tronGasPrefundSUN() - balance
	if prefundAmountSUN <= 0 {
		prefundAmountSUN = thresholdSUN - balance
	}
	if prefundAmountSUN <= 0 {
		return false, nil
	}
	if err := authorizeWalletSigning(ctx, s.Name(), s.ChainID(), reserveWallet, "prefund.native", fmt.Sprintf("%d", prefundAmountSUN), userAddress); err != nil {
		return false, err
	}
	lease, err := chainResourceSequenceLease(ctx, s.Name(), reserveWallet, "prefund.native")
	if err != nil {
		return false, fmt.Errorf("tron resource reservation failed: %w", err)
	}

	blockRef, err := tronGetBlockRef(ctx, apiBase)
	if err != nil {
		_ = lease.Release()
		return false, err
	}

	rawTx, err := tronSDK.NewTransfer(
		reserveWallet.Address, userAddress, prefundAmountSUN,
		blockRef.refBlockBytes, blockRef.refBlockHash,
		blockRef.expiration, blockRef.timestamp)
	if err != nil {
		_ = lease.Release()
		return false, fmt.Errorf("tron prefund tx build: %w", err)
	}

	privKey, err := tronPrivateKey(reserveWallet)
	if err != nil {
		_ = lease.Release()
		return false, err
	}

	_, err = tronSignBroadcastWithLease(ctx, apiBase, rawTx, privKey, lease)
	if err != nil {
		return false, fmt.Errorf("tron prefund broadcast: %w", err)
	}
	return true, nil
}
