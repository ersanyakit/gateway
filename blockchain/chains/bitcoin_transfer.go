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
	"core/blockchain/walletcore"
	"core/constants"
	"core/services/chainresource"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	twbitcoin "tw/protos/bitcoin"
	twcommon "tw/protos/common"
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
	if lastErr == nil {
		lastErr = fmt.Errorf("no bitcoin API endpoint configured")
	}
	return "", lastErr
}

func btcEstimateFee(inputCount, outputCount int) (int64, error) {
	feeRate, err := chainresource.BitcoinFeeRateSatPerVByte()
	if err != nil {
		return 0, err
	}
	// P2WPKH: ~68 vbytes per input, ~31 vbytes per output, ~10 vbytes overhead
	vsize := int64(10 + inputCount*68 + outputCount*31)
	return vsize * feeRate, nil
}

const (
	trustWalletCoinTypeBitcoin uint32 = 0
)

func (b *BitcoinChain) sendTo(ctx context.Context, wallet blockchain.WalletDetails, toAddress string, sendSat int64) (*blockchain.TransactionResult, error) {
	if sendSat <= 0 {
		return nil, fmt.Errorf("bitcoin amount must be greater than zero")
	}
	if err := authorizeWalletSigning(ctx, b.Name(), b.ChainID(), wallet, "transfer.native", fmt.Sprintf("%d", sendSat), toAddress); err != nil {
		return nil, err
	}

	utxos, err := btcFetchUTXOs(ctx, b.RPCs(), wallet.Address)
	if err != nil {
		return nil, fmt.Errorf("bitcoin UTXO fetch: %w", err)
	}

	selected := make([]btcUTXO, 0, len(utxos))
	var totalSat int64
	for _, u := range utxos {
		if !u.Status.Confirmed || u.Value <= 0 {
			continue
		}
		selected = append(selected, u)
		totalSat += u.Value
		estimatedFee, err := btcEstimateFee(len(selected), 2)
		if err != nil {
			return nil, err
		}
		if totalSat >= sendSat+estimatedFee {
			break
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("bitcoin no confirmed UTXOs for %s", wallet.Address)
	}

	const dustSat int64 = 546
	fee, err := btcEstimateFee(len(selected), 2)
	if err != nil {
		return nil, err
	}
	changeSat := totalSat - sendSat - fee
	if changeSat < 0 {
		return nil, fmt.Errorf("bitcoin balance not enough: total=%d sat amount=%d sat fee=%d sat", totalSat, sendSat, fee)
	}
	includeChange := changeSat >= dustSat
	if !includeChange {
		fee = totalSat - sendSat
		minFee, err := btcEstimateFee(len(selected), 1)
		if err != nil {
			return nil, err
		}
		if fee < minFee {
			return nil, fmt.Errorf("bitcoin balance not enough: total=%d sat amount=%d sat fee=%d sat", totalSat, sendSat, minFee)
		}
		changeSat = 0
	}

	destAddr, err := btcutil.DecodeAddress(toAddress, b.Params)
	if err != nil {
		return nil, fmt.Errorf("invalid bitcoin destination address: %w", err)
	}
	if destAddr == nil {
		return nil, fmt.Errorf("invalid bitcoin destination address")
	}
	utxoReservation, err := chainResources.ReserveUTXOs(ctx, chainresource.UTXORequest{
		Chain:   b.Name(),
		Wallet:  wallet.Address,
		Intent:  "transfer.native",
		OwnerID: chainResourceOwnerID(ctx, wallet, "transfer.native"),
		UTXOs:   btcChainResourceUTXOs(selected),
	})
	if err != nil {
		return nil, fmt.Errorf("bitcoin UTXO reservation failed: %w", err)
	}

	privKeyBytes, err := hex.DecodeString(strings.TrimSpace(wallet.PrivateKey))
	if err != nil {
		_ = utxoReservation.Release()
		return nil, fmt.Errorf("invalid bitcoin private key: %w", err)
	}
	_, pubKey := btcec.PrivKeyFromBytes(privKeyBytes)
	if _, _, err := b.bitcoinSourceScript(wallet, pubKey); err != nil {
		_ = utxoReservation.Release()
		return nil, err
	}
	rawTxHex, fallbackTxID, err := b.signBitcoinWithTrustWallet(wallet, privKeyBytes, pubKey, toAddress, sendSat, false, selected)
	if err != nil {
		_ = utxoReservation.Release()
		return nil, err
	}
	txID, err := btcBroadcast(ctx, b.RPCs(), rawTxHex)
	if err != nil {
		_ = utxoReservation.Consume(fallbackTxID)
		return nil, err
	}
	if txID == "" {
		txID = fallbackTxID
	}
	_ = utxoReservation.Consume(txID)
	return &blockchain.TransactionResult{TxHash: txID, Success: true}, nil
}

func (b *BitcoinChain) SweepTo(ctx context.Context, wallet blockchain.WalletDetails, toAddress string) (*blockchain.TransactionResult, error) {
	if err := authorizeWalletSigning(ctx, b.Name(), b.ChainID(), wallet, "sweep.native", "max", toAddress); err != nil {
		return nil, err
	}

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

	fee, err := btcEstimateFee(len(confirmed), 1)
	if err != nil {
		return nil, err
	}
	sendSat := totalSat - fee
	if sendSat <= 0 {
		return nil, fmt.Errorf("bitcoin sweep balance not enough for fee: total=%d sat fee=%d sat", totalSat, fee)
	}

	// Build destination script
	destAddr, err := btcutil.DecodeAddress(toAddress, b.Params)
	if err != nil {
		return nil, fmt.Errorf("invalid bitcoin destination address: %w", err)
	}
	if destAddr == nil {
		return nil, fmt.Errorf("invalid bitcoin destination address")
	}
	utxoReservation, err := chainResources.ReserveUTXOs(ctx, chainresource.UTXORequest{
		Chain:   b.Name(),
		Wallet:  wallet.Address,
		Intent:  "sweep.native",
		OwnerID: chainResourceOwnerID(ctx, wallet, "sweep.native"),
		UTXOs:   btcChainResourceUTXOs(confirmed),
	})
	if err != nil {
		return nil, fmt.Errorf("bitcoin UTXO reservation failed: %w", err)
	}

	privKeyBytes, err := hex.DecodeString(strings.TrimSpace(wallet.PrivateKey))
	if err != nil {
		_ = utxoReservation.Release()
		return nil, fmt.Errorf("invalid bitcoin private key: %w", err)
	}
	_, pubKey := btcec.PrivKeyFromBytes(privKeyBytes)
	if _, _, err := b.bitcoinSourceScript(wallet, pubKey); err != nil {
		_ = utxoReservation.Release()
		return nil, err
	}
	rawTxHex, fallbackTxID, err := b.signBitcoinWithTrustWallet(wallet, privKeyBytes, pubKey, toAddress, totalSat, true, confirmed)
	if err != nil {
		_ = utxoReservation.Release()
		return nil, err
	}
	txID, err := btcBroadcast(ctx, b.RPCs(), rawTxHex)
	if err != nil {
		_ = utxoReservation.Consume(fallbackTxID)
		return nil, err
	}
	if txID == "" {
		txID = fallbackTxID
	}
	_ = utxoReservation.Consume(txID)
	return &blockchain.TransactionResult{TxHash: txID, Success: true}, nil
}

// SweepERC20To is not applicable for Bitcoin — Bitcoin has no ERC-20 tokens.
func (b *BitcoinChain) SweepERC20To(_ context.Context, _ blockchain.WalletDetails, _, _ string) (*blockchain.TransactionResult, error) {
	return nil, fmt.Errorf("bitcoin does not support token sweep")
}

func (b *BitcoinChain) bitcoinSourceScript(wallet blockchain.WalletDetails, pubKey *btcec.PublicKey) ([]byte, bool, error) {
	sourceAddr, err := btcutil.DecodeAddress(wallet.Address, b.Params)
	if err != nil {
		return nil, false, fmt.Errorf("invalid bitcoin source address: %w", err)
	}
	sourceScript, err := txscript.PayToAddrScript(sourceAddr)
	if err != nil {
		return nil, false, fmt.Errorf("bitcoin source pkScript: %w", err)
	}

	switch sourceAddr.(type) {
	case *btcutil.AddressWitnessPubKeyHash:
		expectedAddr, err := btcutil.NewAddressWitnessPubKeyHash(btcutil.Hash160(pubKey.SerializeCompressed()), b.Params)
		if err != nil {
			return nil, false, fmt.Errorf("bitcoin derive p2wpkh address: %w", err)
		}
		if expectedAddr.EncodeAddress() != sourceAddr.EncodeAddress() {
			return nil, false, fmt.Errorf("private key does not match bitcoin wallet address: expected %s got %s", wallet.Address, expectedAddr.EncodeAddress())
		}
		return sourceScript, false, nil
	case *btcutil.AddressTaproot:
		taprootKey := txscript.ComputeTaprootKeyNoScript(pubKey)
		expectedAddr, err := btcutil.NewAddressTaproot(schnorr.SerializePubKey(taprootKey), b.Params)
		if err != nil {
			return nil, false, fmt.Errorf("bitcoin derive taproot address: %w", err)
		}
		if expectedAddr.EncodeAddress() != sourceAddr.EncodeAddress() {
			return nil, false, fmt.Errorf("private key does not match bitcoin wallet address: expected %s got %s", wallet.Address, expectedAddr.EncodeAddress())
		}
		return sourceScript, true, nil
	default:
		return nil, false, fmt.Errorf("unsupported bitcoin source address type %T; only native segwit and taproot are supported", sourceAddr)
	}
}

func (b *BitcoinChain) signBitcoinWithTrustWallet(wallet blockchain.WalletDetails, privateKey []byte, pubKey *btcec.PublicKey, toAddress string, amountSat int64, useMax bool, utxos []btcUTXO) (string, string, error) {
	if amountSat <= 0 {
		return "", "", fmt.Errorf("bitcoin amount must be greater than zero")
	}
	sourceScript, taprootSpend, err := b.bitcoinSourceScript(wallet, pubKey)
	if err != nil {
		return "", "", err
	}
	variant := twbitcoin.TransactionVariant_P2WPKH
	if taprootSpend {
		return signBitcoinManually(b.Params, wallet, privateKey, pubKey, toAddress, amountSat, useMax, utxos, sourceScript, taprootSpend)
	}

	twUTXOs := make([]*twbitcoin.UnspentTransaction, 0, len(utxos))
	for _, utxo := range utxos {
		twUTXO, err := bitcoinTrustWalletUTXO(utxo, sourceScript, variant)
		if err != nil {
			return "", "", err
		}
		twUTXOs = append(twUTXOs, twUTXO)
	}
	if len(twUTXOs) == 0 {
		return "", "", fmt.Errorf("bitcoin no confirmed UTXOs for %s", wallet.Address)
	}
	feeRate, err := chainresource.BitcoinFeeRateSatPerVByte()
	if err != nil {
		return "", "", err
	}

	input := &twbitcoin.SigningInput{
		HashType:      uint32(txscript.SigHashAll),
		Amount:        amountSat,
		ByteFee:       feeRate,
		ToAddress:     toAddress,
		ChangeAddress: wallet.Address,
		PrivateKey:    [][]byte{privateKey},
		Utxo:          twUTXOs,
		UseMaxAmount:  useMax,
		CoinType:      trustWalletCoinTypeBitcoin,
	}

	var output twbitcoin.SigningOutput
	if err := walletcore.Sign(input, &output, constants.Bitcoin); err != nil {
		return "", "", fmt.Errorf("bitcoin Trust Wallet Core signing failed: %w", err)
	}
	if output.GetError() != twcommon.SigningError_OK {
		msg := strings.TrimSpace(output.GetErrorMessage())
		if msg == "" {
			msg = output.GetError().String()
		}
		return "", "", fmt.Errorf("bitcoin Trust Wallet Core signing error: %s", msg)
	}
	encoded := output.GetEncoded()
	if len(encoded) == 0 {
		return "", "", fmt.Errorf("bitcoin Trust Wallet Core signing returned empty transaction")
	}
	txID := strings.TrimSpace(output.GetTransactionId())
	if txID == "" {
		txID = bitcoinTxIDFromRaw(encoded)
	}
	return hex.EncodeToString(encoded), txID, nil
}

func bitcoinTrustWalletUTXO(utxo btcUTXO, script []byte, variant twbitcoin.TransactionVariant) (*twbitcoin.UnspentTransaction, error) {
	if utxo.Value <= 0 {
		return nil, fmt.Errorf("bitcoin UTXO amount must be positive")
	}
	hash, err := bitcoinTrustWalletOutPointHash(utxo.Txid)
	if err != nil {
		return nil, err
	}
	return &twbitcoin.UnspentTransaction{
		OutPoint: &twbitcoin.OutPoint{
			Hash:     hash,
			Index:    utxo.Vout,
			Sequence: math.MaxUint32 - 2,
		},
		Script:  script,
		Amount:  utxo.Value,
		Variant: variant,
	}, nil
}

func bitcoinTrustWalletOutPointHash(txid string) ([]byte, error) {
	hash, err := chainhash.NewHashFromStr(strings.TrimSpace(txid))
	if err != nil {
		return nil, fmt.Errorf("bitcoin txid parse: %w", err)
	}
	return hash.CloneBytes(), nil
}

func signBitcoinManually(params *chaincfg.Params, wallet blockchain.WalletDetails, privateKey []byte, pubKey *btcec.PublicKey, toAddress string, amountSat int64, useMax bool, utxos []btcUTXO, sourceScript []byte, taprootSpend bool) (string, string, error) {
	if len(utxos) == 0 {
		return "", "", fmt.Errorf("bitcoin no confirmed UTXOs for %s", wallet.Address)
	}
	privKey, _ := btcec.PrivKeyFromBytes(privateKey)

	totalSat := int64(0)
	for _, utxo := range utxos {
		if utxo.Value <= 0 {
			return "", "", fmt.Errorf("bitcoin UTXO amount must be positive")
		}
		totalSat += utxo.Value
	}

	const dustSat int64 = 546
	outputCount := 2
	if useMax {
		outputCount = 1
	}
	fee, err := btcEstimateFee(len(utxos), outputCount)
	if err != nil {
		return "", "", err
	}
	sendSat := amountSat
	changeSat := totalSat - sendSat - fee
	includeChange := !useMax && changeSat >= dustSat
	if useMax {
		sendSat = totalSat - fee
		changeSat = 0
	} else if !includeChange {
		fee = totalSat - sendSat
		changeSat = 0
	}
	if sendSat <= 0 || totalSat < sendSat+fee {
		return "", "", fmt.Errorf("bitcoin balance not enough: total=%d sat amount=%d sat fee=%d sat", totalSat, sendSat, fee)
	}

	destAddr, err := btcutil.DecodeAddress(toAddress, params)
	if err != nil {
		return "", "", fmt.Errorf("invalid bitcoin destination address: %w", err)
	}
	destScript, err := txscript.PayToAddrScript(destAddr)
	if err != nil {
		return "", "", fmt.Errorf("bitcoin dest pkScript: %w", err)
	}

	var changeScript []byte
	if includeChange {
		changeAddr, err := btcutil.DecodeAddress(wallet.Address, params)
		if err != nil {
			return "", "", fmt.Errorf("invalid bitcoin change address: %w", err)
		}
		changeScript, err = txscript.PayToAddrScript(changeAddr)
		if err != nil {
			return "", "", fmt.Errorf("bitcoin change pkScript: %w", err)
		}
	}

	msgTx := wire.NewMsgTx(wire.TxVersion)
	for _, utxo := range utxos {
		hash, err := chainhash.NewHashFromStr(utxo.Txid)
		if err != nil {
			return "", "", fmt.Errorf("bitcoin txid parse: %w", err)
		}
		msgTx.AddTxIn(&wire.TxIn{
			PreviousOutPoint: wire.OutPoint{Hash: *hash, Index: utxo.Vout},
			Sequence:         math.MaxUint32 - 2,
		})
	}
	msgTx.AddTxOut(&wire.TxOut{Value: sendSat, PkScript: destScript})
	if includeChange {
		msgTx.AddTxOut(&wire.TxOut{Value: changeSat, PkScript: changeScript})
	}

	prevOutMap := make(map[wire.OutPoint]*wire.TxOut, len(utxos))
	for _, utxo := range utxos {
		hash, _ := chainhash.NewHashFromStr(utxo.Txid)
		op := wire.OutPoint{Hash: *hash, Index: utxo.Vout}
		prevOutMap[op] = &wire.TxOut{Value: utxo.Value, PkScript: sourceScript}
	}
	fetcher := txscript.NewMultiPrevOutFetcher(prevOutMap)
	sigHashes := txscript.NewTxSigHashes(msgTx, fetcher)
	for i, utxo := range utxos {
		witness, err := bitcoinWitness(msgTx, sigHashes, i, utxo.Value, sourceScript, privKey, pubKey, taprootSpend)
		if err != nil {
			return "", "", fmt.Errorf("bitcoin sign input %d: %w", i, err)
		}
		msgTx.TxIn[i].Witness = witness
	}

	var buf bytes.Buffer
	if err := msgTx.Serialize(&buf); err != nil {
		return "", "", fmt.Errorf("bitcoin tx serialize: %w", err)
	}
	raw := buf.Bytes()
	return hex.EncodeToString(raw), bitcoinTxIDFromRaw(raw), nil
}

func bitcoinWitness(msgTx *wire.MsgTx, sigHashes *txscript.TxSigHashes, inputIndex int, value int64, pkScript []byte, privKey *btcec.PrivateKey, pubKey *btcec.PublicKey, taprootSpend bool) (wire.TxWitness, error) {
	if taprootSpend {
		sig, err := txscript.RawTxInTaprootSignature(
			msgTx, sigHashes, inputIndex, value, pkScript, nil,
			txscript.SigHashDefault, privKey,
		)
		if err != nil {
			return nil, err
		}
		return wire.TxWitness{sig}, nil
	}

	sig, err := txscript.RawTxInWitnessSignature(
		msgTx, sigHashes, inputIndex, value, pkScript,
		txscript.SigHashAll, privKey,
	)
	if err != nil {
		return nil, err
	}
	return wire.TxWitness{sig, pubKey.SerializeCompressed()}, nil
}

func bitcoinTxIDFromRaw(raw []byte) string {
	var tx wire.MsgTx
	if err := tx.Deserialize(bytes.NewReader(raw)); err != nil {
		return ""
	}
	return tx.TxHash().String()
}

func btcChainResourceUTXOs(utxos []btcUTXO) []chainresource.UTXO {
	out := make([]chainresource.UTXO, 0, len(utxos))
	for _, utxo := range utxos {
		out = append(out, chainresource.UTXO{
			TxID:  utxo.Txid,
			Vout:  utxo.Vout,
			Value: utxo.Value,
		})
	}
	return out
}

// PrefundGas is not applicable for Bitcoin — transaction fees come from UTXOs.
func (b *BitcoinChain) PrefundGas(_ context.Context, _ blockchain.WalletDetails, _ string) (bool, error) {
	return false, nil
}
