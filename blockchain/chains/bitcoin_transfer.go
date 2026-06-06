package chains

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	blockchain "core/blockchain"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

type btcUTXO struct {
	Txid   string `json:"txid"`
	Vout   uint32 `json:"vout"`
	Value  int64  `json:"value"` // satoshis
	Status struct {
		Confirmed bool `json:"confirmed"`
	} `json:"status"`
}

func btcFetchUTXOs(ctx context.Context, rpcURLs []string, address string) ([]btcUTXO, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	var lastErr error
	for _, base := range rpcURLs {
		base = strings.TrimRight(strings.TrimSpace(base), "/")
		url := base + "/address/" + address + "/utxo"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			lastErr = err
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("bitcoin UTXO API HTTP %d: %s", resp.StatusCode, string(body))
			continue
		}
		var utxos []btcUTXO
		if err := json.Unmarshal(body, &utxos); err != nil {
			lastErr = err
			continue
		}
		return utxos, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no bitcoin API endpoint configured")
	}
	return nil, lastErr
}

func btcBroadcast(ctx context.Context, rpcURLs []string, rawTxHex string) (string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	var lastErr error
	for _, base := range rpcURLs {
		base = strings.TrimRight(strings.TrimSpace(base), "/")
		url := base + "/tx"
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(rawTxHex))
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("Content-Type", "text/plain")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("bitcoin broadcast HTTP %d: %s", resp.StatusCode, string(body))
			continue
		}
		return strings.TrimSpace(string(body)), nil
	}
	return "", lastErr
}

func btcEstimateFee(inputCount, outputCount int) int64 {
	// P2WPKH: ~68 vbytes per input, ~31 vbytes per output, ~10 vbytes overhead
	vsize := int64(10 + inputCount*68 + outputCount*31)
	const satPerVbyte int64 = 10 // conservative 10 sat/vbyte
	return vsize * satPerVbyte
}

func (b *BitcoinChain) SweepTo(ctx context.Context, wallet blockchain.WalletDetails, toAddress string) (*blockchain.TransactionResult, error) {
	privKeyBytes, err := hex.DecodeString(strings.TrimSpace(wallet.PrivateKey))
	if err != nil {
		return nil, fmt.Errorf("invalid bitcoin private key: %w", err)
	}
	privKey, pubKey := btcec.PrivKeyFromBytes(privKeyBytes)

	utxos, err := btcFetchUTXOs(ctx, b.RPCs(), wallet.Address)
	if err != nil {
		return nil, fmt.Errorf("bitcoin UTXO fetch: %w", err)
	}

	// Only use confirmed UTXOs
	confirmed := make([]btcUTXO, 0, len(utxos))
	var totalSat int64
	for _, u := range utxos {
		if u.Status.Confirmed {
			confirmed = append(confirmed, u)
			totalSat += u.Value
		}
	}
	if len(confirmed) == 0 || totalSat == 0 {
		return nil, fmt.Errorf("bitcoin no confirmed UTXOs for %s", wallet.Address)
	}

	fee := btcEstimateFee(len(confirmed), 1)
	sendSat := totalSat - fee
	if sendSat <= 0 {
		return nil, fmt.Errorf("bitcoin sweep balance not enough for fee: total=%d sat fee=%d sat", totalSat, fee)
	}

	// Build destination script
	destAddr, err := btcutil.DecodeAddress(toAddress, b.Params)
	if err != nil {
		return nil, fmt.Errorf("invalid bitcoin destination address: %w", err)
	}
	pkScript, err := txscript.PayToAddrScript(destAddr)
	if err != nil {
		return nil, fmt.Errorf("bitcoin dest pkScript: %w", err)
	}

	// Build P2WPKH script for signing
	pubKeyHash := btcutil.Hash160(pubKey.SerializeCompressed())
	fromScript, err := txscript.NewScriptBuilder().
		AddOp(txscript.OP_0).
		AddData(pubKeyHash).
		Script()
	if err != nil {
		return nil, fmt.Errorf("bitcoin p2wpkh script build: %w", err)
	}

	// Build transaction
	msgTx := wire.NewMsgTx(wire.TxVersion)
	msgTx.LockTime = 0

	for _, u := range confirmed {
		hash, err := chainhash.NewHashFromStr(u.Txid)
		if err != nil {
			return nil, fmt.Errorf("bitcoin txid parse: %w", err)
		}
		msgTx.AddTxIn(&wire.TxIn{
			PreviousOutPoint: wire.OutPoint{Hash: *hash, Index: u.Vout},
			Sequence:         math.MaxUint32 - 2,
		})
	}

	msgTx.AddTxOut(&wire.TxOut{Value: sendSat, PkScript: pkScript})

	// Build prevout fetcher for all inputs (needed by NewTxSigHashes for segwit)
	prevOutMap := make(map[wire.OutPoint]*wire.TxOut, len(confirmed))
	for _, u := range confirmed {
		hash, _ := chainhash.NewHashFromStr(u.Txid)
		op := wire.OutPoint{Hash: *hash, Index: u.Vout}
		prevOutMap[op] = &wire.TxOut{Value: u.Value, PkScript: fromScript}
	}
	fetcher := txscript.NewMultiPrevOutFetcher(prevOutMap)

	// Sign each input
	sigHashes := txscript.NewTxSigHashes(msgTx, fetcher)
	for i, u := range confirmed {
		sig, err := txscript.RawTxInWitnessSignature(
			msgTx, sigHashes, i, u.Value, fromScript,
			txscript.SigHashAll, privKey,
		)
		if err != nil {
			return nil, fmt.Errorf("bitcoin sign input %d: %w", i, err)
		}
		msgTx.TxIn[i].Witness = wire.TxWitness{sig, pubKey.SerializeCompressed()}
	}

	// Serialize
	var buf bytes.Buffer
	if err := msgTx.Serialize(&buf); err != nil {
		return nil, fmt.Errorf("bitcoin tx serialize: %w", err)
	}
	rawHex := hex.EncodeToString(buf.Bytes())

	txID, err := btcBroadcast(ctx, b.RPCs(), rawHex)
	if err != nil {
		return nil, err
	}
	return &blockchain.TransactionResult{TxHash: txID, Success: true}, nil
}

// SweepERC20To is not applicable for Bitcoin — Bitcoin has no ERC-20 tokens.
func (b *BitcoinChain) SweepERC20To(_ context.Context, _ blockchain.WalletDetails, _, _ string) (*blockchain.TransactionResult, error) {
	return nil, fmt.Errorf("bitcoin does not support token sweep")
}

// PrefundGas is not applicable for Bitcoin — transaction fees come from UTXOs.
func (b *BitcoinChain) PrefundGas(_ context.Context, _ blockchain.WalletDetails, _ string) (bool, error) {
	return false, nil
}
