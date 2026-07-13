package chains

import (
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
	"core/blockchain/addrutil"
	"core/blockchain/walletcore"
	"core/constants"
	"core/services/chainresource"
	"core/services/signer"

	twbitcoin "tw/protos/bitcoin"
	twbitcoinv2 "tw/protos/bitcoinv2"
	twcommon "tw/protos/common"
	twutxo "tw/protos/utxo"
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
		_ = resp.Body.Close()
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
		_ = resp.Body.Close()
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
	bitcoinSigHashAll          uint32 = 1
)

func (b *BitcoinChain) sendTo(ctx context.Context, wallet blockchain.WalletDetails, toAddress string, sendSat int64) (*blockchain.TransactionResult, error) {
	if sendSat <= 0 {
		return nil, fmt.Errorf("bitcoin amount must be greater than zero")
	}
	if err := authorizeWalletSigning(ctx, b.Name(), b.ChainID(), wallet, "transfer.native", fmt.Sprintf("%d", sendSat), toAddress); err != nil {
		return nil, err
	}
	if err := requireDatabaseResourceReservation(ctx, b.Name(), "transfer.native"); err != nil {
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
	}

	if !b.ValidateAddress(toAddress) {
		return nil, fmt.Errorf("invalid bitcoin destination address: %s", toAddress)
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

	var rawTxHex string
	var fallbackTxID string
	if shouldUseExternalTransactionSigner(wallet) {
		rawTxHex, fallbackTxID, err = b.signBitcoinWithCustody(ctx, wallet, toAddress, sendSat, false, selected)
		if err != nil {
			_ = utxoReservation.Release()
			return nil, err
		}
	} else {
		privKeyBytes, err := bitcoinPrivateKeyBytes(wallet)
		if err != nil {
			_ = utxoReservation.Release()
			return nil, err
		}
		rawTxHex, fallbackTxID, err = b.signBitcoinWithTrustWallet(wallet, privKeyBytes, toAddress, sendSat, false, selected)
		if err != nil {
			_ = utxoReservation.Release()
			return nil, err
		}
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
	if err := requireDatabaseResourceReservation(ctx, b.Name(), "sweep.native"); err != nil {
		return nil, err
	}
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

	if !b.ValidateAddress(toAddress) {
		return nil, fmt.Errorf("invalid bitcoin destination address: %s", toAddress)
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

	var rawTxHex string
	var fallbackTxID string
	if shouldUseExternalTransactionSigner(wallet) {
		rawTxHex, fallbackTxID, err = b.signBitcoinWithCustody(ctx, wallet, toAddress, totalSat, true, confirmed)
		if err != nil {
			_ = utxoReservation.Release()
			return nil, err
		}
	} else {
		privKeyBytes, err := bitcoinPrivateKeyBytes(wallet)
		if err != nil {
			_ = utxoReservation.Release()
			return nil, err
		}
		rawTxHex, fallbackTxID, err = b.signBitcoinWithTrustWallet(wallet, privKeyBytes, toAddress, totalSat, true, confirmed)
		if err != nil {
			_ = utxoReservation.Release()
			return nil, err
		}
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

func bitcoinPrivateKeyBytes(wallet blockchain.WalletDetails) ([]byte, error) {
	if signer.IsProduction() {
		return nil, signer.ErrProductionSecretMaterialAccessDisabled
	}
	privKeyBytes, err := hex.DecodeString(strings.TrimSpace(wallet.PrivateKey))
	if err != nil {
		return nil, fmt.Errorf("invalid bitcoin private key: %w", err)
	}
	return privKeyBytes, nil
}

func bitcoinSourceScript(address string) ([]byte, error) {
	sourceScript, witness, err := addrutil.BitcoinWitnessScript(address)
	if err != nil {
		return nil, fmt.Errorf("unsupported bitcoin source address: %w", err)
	}
	switch {
	case witness.Version == 0 && len(witness.Program) == 20:
		return sourceScript, nil
	case witness.Version == 1 && len(witness.Program) == 32:
		return sourceScript, nil
	default:
		return nil, fmt.Errorf("unsupported bitcoin witness version=%d length=%d", witness.Version, len(witness.Program))
	}
}

func (b *BitcoinChain) signBitcoinWithTrustWallet(wallet blockchain.WalletDetails, privateKey []byte, toAddress string, amountSat int64, useMax bool, utxos []btcUTXO) (string, string, error) {
	if amountSat <= 0 {
		return "", "", fmt.Errorf("bitcoin amount must be greater than zero")
	}
	sourceScript, err := bitcoinSourceScript(wallet.Address)
	if err != nil {
		return "", "", err
	}
	inputs := make([]*twbitcoinv2.Input, 0, len(utxos))
	for _, utxo := range utxos {
		input, err := bitcoinTrustWalletInputV2(utxo, sourceScript)
		if err != nil {
			return "", "", err
		}
		inputs = append(inputs, input)
	}
	if len(inputs) == 0 {
		return "", "", fmt.Errorf("bitcoin no confirmed UTXOs for %s", wallet.Address)
	}
	feeRate, err := chainresource.BitcoinFeeRateSatPerVByte()
	if err != nil {
		return "", "", err
	}

	builder := &twbitcoinv2.TransactionBuilder{
		Version:       twbitcoinv2.TransactionVersion_V2,
		Inputs:        inputs,
		InputSelector: twbitcoinv2.InputSelector_UseAll,
		FeePerVb:      feeRate,
		DustPolicy:    &twbitcoinv2.TransactionBuilder_FixedDustThreshold{FixedDustThreshold: 546},
	}
	if useMax {
		builder.MaxAmountOutput = bitcoinTrustWalletOutputV2(0, toAddress)
	} else {
		builder.Outputs = []*twbitcoinv2.Output{bitcoinTrustWalletOutputV2(amountSat, toAddress)}
		builder.ChangeOutput = bitcoinTrustWalletOutputV2(0, wallet.Address)
	}

	input := &twbitcoin.SigningInput{
		CoinType: trustWalletCoinTypeBitcoin,
		SigningV2: &twbitcoinv2.SigningInput{
			PrivateKeys: [][]byte{privateKey},
			ChainInfo: &twbitcoinv2.ChainInfo{
				P2PkhPrefix: 0,
				P2ShPrefix:  5,
				Hrp:         "bc",
			},
			Transaction: &twbitcoinv2.SigningInput_Builder{Builder: builder},
		},
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
	v2Output := output.GetSigningResultV2()
	if v2Output == nil {
		return "", "", fmt.Errorf("bitcoin Trust Wallet Core signing returned empty v2 result")
	}
	if v2Output.GetError() != twcommon.SigningError_OK {
		msg := strings.TrimSpace(v2Output.GetErrorMessage())
		if msg == "" {
			msg = v2Output.GetError().String()
		}
		return "", "", fmt.Errorf("bitcoin Trust Wallet Core signing error: %s", msg)
	}
	encoded := v2Output.GetEncoded()
	if len(encoded) == 0 {
		return "", "", fmt.Errorf("bitcoin Trust Wallet Core signing returned empty transaction")
	}
	txID := hex.EncodeToString(v2Output.GetTxid())
	if txID == "" {
		return "", "", fmt.Errorf("bitcoin Trust Wallet Core signing returned empty transaction id")
	}
	return hex.EncodeToString(encoded), txID, nil
}

func (b *BitcoinChain) signBitcoinWithCustody(ctx context.Context, wallet blockchain.WalletDetails, toAddress string, amountSat int64, useMax bool, utxos []btcUTXO) (string, string, error) {
	feeRate, err := chainresource.BitcoinFeeRateSatPerVByte()
	if err != nil {
		return "", "", err
	}
	payloadUTXOs := make([]map[string]any, 0, len(utxos))
	for _, utxo := range utxos {
		payloadUTXOs = append(payloadUTXOs, map[string]any{
			"txid":        strings.TrimSpace(utxo.Txid),
			"vout":        utxo.Vout,
			"value":       utxo.Value,
			"confirmed":   utxo.Status.Confirmed,
			"source_addr": strings.TrimSpace(wallet.Address),
		})
	}
	intent := "transfer.native"
	amountRaw := fmt.Sprintf("%d", amountSat)
	if useMax {
		intent = "sweep.native"
		amountRaw = "max"
	}
	response, err := signTransactionWithCustody(ctx, b.Name(), b.ChainID(), wallet, intent, amountRaw, toAddress, map[string]any{
		"format":          "bitcoin_unsigned_transaction.v1",
		"source_address":  strings.TrimSpace(wallet.Address),
		"to_address":      strings.TrimSpace(toAddress),
		"amount_sat":      amountSat,
		"use_max":         useMax,
		"fee_rate_sat_vb": feeRate,
		"utxos":           payloadUTXOs,
	})
	if err != nil {
		return "", "", err
	}
	rawTxHex, err := signedPayloadHex(response)
	if err != nil {
		return "", "", fmt.Errorf("bitcoin external signer transaction missing: %w", err)
	}
	return rawTxHex, strings.TrimSpace(response.TxHash), nil
}

func bitcoinTrustWalletInputV2(utxo btcUTXO, script []byte) (*twbitcoinv2.Input, error) {
	if utxo.Value <= 0 {
		return nil, fmt.Errorf("bitcoin UTXO amount must be positive")
	}
	hash, err := bitcoinTrustWalletOutPointHash(utxo.Txid)
	if err != nil {
		return nil, err
	}
	return &twbitcoinv2.Input{
		OutPoint:    &twutxo.OutPoint{Hash: hash, Vout: utxo.Vout},
		Value:       utxo.Value,
		SighashType: bitcoinSigHashAll,
		Sequence:    &twbitcoinv2.Input_Sequence{Sequence: math.MaxUint32 - 2},
		ClaimingScript: &twbitcoinv2.Input_ScriptData{
			ScriptData: script,
		},
	}, nil
}

func bitcoinTrustWalletOutputV2(value int64, address string) *twbitcoinv2.Output {
	return &twbitcoinv2.Output{
		Value:       value,
		ToRecipient: &twbitcoinv2.Output_ToAddress{ToAddress: address},
	}
}

func bitcoinTrustWalletOutPointHash(txid string) ([]byte, error) {
	raw, err := hex.DecodeString(strings.TrimSpace(txid))
	if err != nil {
		return nil, fmt.Errorf("bitcoin txid parse: %w", err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("bitcoin txid must be 32 bytes")
	}
	for i, j := 0, len(raw)-1; i < j; i, j = i+1, j-1 {
		raw[i], raw[j] = raw[j], raw[i]
	}
	return raw, nil
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
