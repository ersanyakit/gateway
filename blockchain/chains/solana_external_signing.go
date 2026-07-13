package chains

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	blockchain "core/blockchain"
	"core/services/chainresource"

	solana "github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
)

func (s *SolanaChain) signAndSendSolanaTransaction(ctx context.Context, rpcClient *rpc.Client, tx *solana.Transaction, wallet blockchain.WalletDetails, from solana.PublicKey, intent string, amountRaw string, destination string, lease *chainresource.SequenceLease) (solana.Signature, error) {
	if tx == nil {
		_ = lease.Release()
		return solana.Signature{}, fmt.Errorf("%s transaction is nil", s.Name())
	}
	if shouldUseExternalTransactionSigner(wallet) {
		unsignedTx, err := tx.MarshalBinary()
		if err != nil {
			_ = lease.Release()
			return solana.Signature{}, fmt.Errorf("%s unsigned tx encode failed: %w", s.Name(), err)
		}
		messageBytes, err := tx.Message.MarshalBinary()
		if err != nil {
			_ = lease.Release()
			return solana.Signature{}, fmt.Errorf("%s unsigned message encode failed: %w", s.Name(), err)
		}
		response, err := signTransactionWithCustody(ctx, s.Name(), s.ChainID(), wallet, intent, amountRaw, destination, map[string]any{
			"format":                      "solana_unsigned_transaction.v1",
			"source_address":              strings.TrimSpace(wallet.Address),
			"payer":                       from.String(),
			"recent_blockhash":            tx.Message.RecentBlockhash.String(),
			"unsigned_transaction_base64": base64.StdEncoding.EncodeToString(unsignedTx),
			"message_base64":              base64.StdEncoding.EncodeToString(messageBytes),
		})
		if err != nil {
			_ = lease.Release()
			return solana.Signature{}, err
		}
		signedBytes, err := signedPayloadBytes(response)
		if err != nil {
			_ = lease.Release()
			return solana.Signature{}, fmt.Errorf("%s external signer transaction missing: %w", s.Name(), err)
		}
		signedTx, err := solana.TransactionFromBytes(signedBytes)
		if err != nil {
			_ = lease.Release()
			return solana.Signature{}, fmt.Errorf("%s external signer transaction decode failed: %w", s.Name(), err)
		}
		if len(signedTx.Signatures) == 0 {
			_ = lease.Release()
			return solana.Signature{}, fmt.Errorf("%s external signer returned transaction without signatures", s.Name())
		}
		signedMessageBytes, err := signedTx.Message.MarshalBinary()
		if err != nil {
			_ = lease.Release()
			return solana.Signature{}, fmt.Errorf("%s external signer transaction message encode failed: %w", s.Name(), err)
		}
		if !bytes.Equal(signedMessageBytes, messageBytes) {
			_ = lease.Release()
			return solana.Signature{}, fmt.Errorf("%s external signer returned transaction for a different message", s.Name())
		}
		signature, err := rpcClient.SendTransaction(ctx, signedTx)
		if err != nil {
			_ = lease.Consume(signedTx.Signatures[0].String())
			return solana.Signature{}, fmt.Errorf("%s tx broadcast failed: %w", s.Name(), err)
		}
		return signature, nil
	}

	privateKey, _, err := solanaPrivateKeyAndAddress(wallet)
	if err != nil {
		_ = lease.Release()
		return solana.Signature{}, err
	}
	signatures, err := tx.Sign(func(key solana.PublicKey) *solana.PrivateKey {
		if key.Equals(from) {
			return &privateKey
		}
		return nil
	})
	if err != nil {
		_ = lease.Release()
		return solana.Signature{}, fmt.Errorf("%s tx signing failed: %w", s.Name(), err)
	}
	signature, err := rpcClient.SendTransaction(ctx, tx)
	if err != nil {
		txSig := ""
		if len(signatures) > 0 {
			txSig = signatures[0].String()
		}
		_ = lease.Consume(txSig)
		return solana.Signature{}, fmt.Errorf("%s tx broadcast failed: %w", s.Name(), err)
	}
	return signature, nil
}
