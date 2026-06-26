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

	"github.com/btcsuite/btcd/btcec/v2"
	tronSDK "github.com/okx/go-wallet-sdk/coins/tron"
)

// tronAPIBase derives the TRON full-node HTTP API base URL from the JSON-RPC URL list.
func tronAPIBase(rpcURLs []string) string {
	for _, u := range rpcURLs {
		u = strings.TrimSuffix(strings.TrimSpace(u), "/")
		u = strings.TrimSuffix(u, "/jsonrpc")
		if u != "" {
			return u
		}
	}
	return "https://api.trongrid.io"
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
	reqBody, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "eth_getBalance",
		"params":  []interface{}{address, "latest"},
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, bytes.NewReader(reqBody))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("tron eth_getBalance: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("tron eth_getBalance HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var res struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return 0, err
	}

	hexStr := strings.TrimPrefix(strings.TrimSpace(res.Result), "0x")
	if hexStr == "" {
		return 0, nil
	}
	v, ok := new(big.Int).SetString(hexStr, 16)
	if !ok {
		return 0, fmt.Errorf("tron parse balance hex: %s", res.Result)
	}
	if !v.IsInt64() {
		return 0, fmt.Errorf("tron balance exceeds int64 SUN: %s", v.String())
	}
	return v.Int64(), nil
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
		Result string `json:"result"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, err
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

func tronSignAndBroadcast(ctx context.Context, apiBase, rawTxHex string, privKey *btcec.PrivateKey) (string, error) {
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
	return tronBroadcast(ctx, apiBase, signedTx)
}

func tronGasThresholdSUN() int64 {
	if raw := strings.TrimSpace(os.Getenv("TRON_GAS_THRESHOLD_SUN")); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			return v
		}
	}
	return 10_000_000 // 10 TRX in SUN
}

func tronGasPrefundSUN() int64 {
	if raw := strings.TrimSpace(os.Getenv("TRON_GAS_PREFUND_SUN")); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			return v
		}
	}
	return 30_000_000 // 30 TRX in SUN
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

	rpcs := s.RPCs()
	if len(rpcs) == 0 {
		return nil, fmt.Errorf("no tron RPC endpoint configured")
	}
	apiBase := tronAPIBase(rpcs)

	blockRef, err := tronGetBlockRef(ctx, apiBase)
	if err != nil {
		return nil, err
	}
	rawTx, err := tronSDK.NewTransfer(wallet.Address, toAddress, sendAmount,
		blockRef.refBlockBytes, blockRef.refBlockHash,
		blockRef.expiration, blockRef.timestamp)
	if err != nil {
		return nil, fmt.Errorf("tron build transfer tx: %w", err)
	}
	privKey, err := tronPrivateKey(wallet)
	if err != nil {
		return nil, err
	}
	txID, err := tronSignAndBroadcast(ctx, apiBase, rawTx, privKey)
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

	rpcs := s.RPCs()
	if len(rpcs) == 0 {
		return nil, fmt.Errorf("no tron RPC endpoint configured")
	}
	rpcURL := rpcs[0]
	apiBase := tronAPIBase(rpcs)

	balance, err := tronGetTRC20Balance(ctx, rpcURL, contractAddr, wallet.Address)
	if err != nil {
		return nil, fmt.Errorf("tron TRC-20 balance: %w", err)
	}
	if balance == nil || balance.Cmp(amount) < 0 {
		return nil, fmt.Errorf("tron TRC-20 balance is not enough: balance=%s amount=%s", balance.String(), amount.String())
	}

	blockRef, err := tronGetBlockRef(ctx, apiBase)
	if err != nil {
		return nil, err
	}

	const trc20FeeLimit int64 = 50_000_000
	rawTx, err := tronSDK.NewTRC20TokenTransfer(
		wallet.Address, toAddress, contractAddr,
		amount, trc20FeeLimit,
		blockRef.refBlockBytes, blockRef.refBlockHash,
		blockRef.expiration, blockRef.timestamp)
	if err != nil {
		return nil, fmt.Errorf("tron build TRC-20 tx: %w", err)
	}

	privKey, err := tronPrivateKey(wallet)
	if err != nil {
		return nil, err
	}
	txID, err := tronSignAndBroadcast(ctx, apiBase, rawTx, privKey)
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

	rpcs := s.RPCs()
	if len(rpcs) == 0 {
		return nil, fmt.Errorf("no tron RPC endpoint configured")
	}
	rpcURL := rpcs[0]
	apiBase := tronAPIBase(rpcs)

	balance, err := tronGetTRXBalance(ctx, rpcURL, wallet.Address)
	if err != nil {
		return nil, fmt.Errorf("tron TRX balance: %w", err)
	}

	const feeSUN int64 = 1_100_000 // ~1.1 TRX (safe margin for bandwidth)
	sendAmount := balance - feeSUN
	if sendAmount <= 0 {
		return nil, fmt.Errorf("tron sweep balance not enough: balance=%d sun fee=%d sun", balance, feeSUN)
	}

	blockRef, err := tronGetBlockRef(ctx, apiBase)
	if err != nil {
		return nil, err
	}

	rawTx, err := tronSDK.NewTransfer(wallet.Address, toAddress, sendAmount,
		blockRef.refBlockBytes, blockRef.refBlockHash,
		blockRef.expiration, blockRef.timestamp)
	if err != nil {
		return nil, fmt.Errorf("tron build transfer tx: %w", err)
	}

	privKey, err := tronPrivateKey(wallet)
	if err != nil {
		return nil, err
	}

	txID, err := tronSignAndBroadcast(ctx, apiBase, rawTx, privKey)
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

	rpcs := s.RPCs()
	if len(rpcs) == 0 {
		return nil, fmt.Errorf("no tron RPC endpoint configured")
	}
	rpcURL := rpcs[0]
	apiBase := tronAPIBase(rpcs)

	balance, err := tronGetTRC20Balance(ctx, rpcURL, contractAddr, wallet.Address)
	if err != nil {
		return nil, fmt.Errorf("tron TRC-20 balance: %w", err)
	}
	if balance == nil || balance.Sign() <= 0 {
		return nil, fmt.Errorf("tron TRC-20 balance is zero for %s", contractAddr)
	}

	blockRef, err := tronGetBlockRef(ctx, apiBase)
	if err != nil {
		return nil, err
	}

	const trc20FeeLimit int64 = 50_000_000 // 50 TRX energy limit
	rawTx, err := tronSDK.NewTRC20TokenTransfer(
		wallet.Address, toAddress, contractAddr,
		balance, trc20FeeLimit,
		blockRef.refBlockBytes, blockRef.refBlockHash,
		blockRef.expiration, blockRef.timestamp)
	if err != nil {
		return nil, fmt.Errorf("tron build TRC-20 tx: %w", err)
	}

	privKey, err := tronPrivateKey(wallet)
	if err != nil {
		return nil, err
	}

	txID, err := tronSignAndBroadcast(ctx, apiBase, rawTx, privKey)
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
	rpcs := s.RPCs()
	if len(rpcs) == 0 {
		return false, fmt.Errorf("no tron RPC endpoint configured")
	}
	rpcURL := rpcs[0]
	apiBase := tronAPIBase(rpcs)

	balance, err := tronGetTRXBalance(ctx, rpcURL, userAddress)
	if err != nil {
		return false, fmt.Errorf("tron prefund balance check: %w", err)
	}

	if balance >= tronGasThresholdSUN() {
		return false, nil
	}

	blockRef, err := tronGetBlockRef(ctx, apiBase)
	if err != nil {
		return false, err
	}

	rawTx, err := tronSDK.NewTransfer(
		reserveWallet.Address, userAddress, tronGasPrefundSUN(),
		blockRef.refBlockBytes, blockRef.refBlockHash,
		blockRef.expiration, blockRef.timestamp)
	if err != nil {
		return false, fmt.Errorf("tron prefund tx build: %w", err)
	}

	privKey, err := tronPrivateKey(reserveWallet)
	if err != nil {
		return false, err
	}

	if _, err := tronSignAndBroadcast(ctx, apiBase, rawTx, privKey); err != nil {
		return false, fmt.Errorf("tron prefund broadcast: %w", err)
	}
	return true, nil
}
