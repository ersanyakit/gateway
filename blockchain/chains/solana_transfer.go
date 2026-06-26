package chains

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"

	blockchain "core/blockchain"

	solana "github.com/gagliardetto/solana-go"
	associatedtokenaccount "github.com/gagliardetto/solana-go/programs/associated-token-account"
	spltoken "github.com/gagliardetto/solana-go/programs/token"
	"github.com/gagliardetto/solana-go/rpc"
)

const solanaTransferFeeLamports uint64 = 5000

func solanaGasThresholdLamports() uint64 {
	if raw := strings.TrimSpace(os.Getenv("SOLANA_GAS_THRESHOLD_LAMPORTS")); raw != "" {
		if v, err := strconv.ParseUint(raw, 10, 64); err == nil && v > 0 {
			return v
		}
	}
	return 2_100_000 // ~0.0021 SOL: covers ATA creation rent + tx fees
}

func solanaGasPrefundLamports() uint64 {
	if raw := strings.TrimSpace(os.Getenv("SOLANA_GAS_PREFUND_LAMPORTS")); raw != "" {
		if v, err := strconv.ParseUint(raw, 10, 64); err == nil && v > 0 {
			return v
		}
	}
	return 5_000_000 // ~0.005 SOL
}

func (s *SolanaChain) SweepTo(ctx context.Context, wallet blockchain.WalletDetails, toAddress string) (*blockchain.TransactionResult, error) {
	rpcClient, err := s.solanaRPCClient()
	if err != nil {
		return nil, err
	}

	privateKey, from, err := solanaPrivateKeyAndAddress(wallet)
	if err != nil {
		return nil, err
	}

	balance, err := rpcClient.GetBalance(ctx, from, rpc.CommitmentFinalized)
	if err != nil {
		return nil, fmt.Errorf("solana balance fetch failed: %w", err)
	}

	if balance.Value <= solanaTransferFeeLamports {
		return nil, fmt.Errorf("solana sweep balance not enough for fee: balance=%d", balance.Value)
	}

	return s.sendLamportsWithClient(ctx, rpcClient, privateKey, from, balance.Value-solanaTransferFeeLamports, toAddress)
}

// SweepERC20To sweeps all SPL tokens (mint = contractAddr) from wallet to toAddress.
// toAddress is the base Solana wallet address of the destination (not the ATA).
func (s *SolanaChain) SweepERC20To(ctx context.Context, wallet blockchain.WalletDetails, contractAddr, toAddress string) (*blockchain.TransactionResult, error) {
	rpcClient, err := s.solanaRPCClient()
	if err != nil {
		return nil, err
	}

	privateKey, from, err := solanaPrivateKeyAndAddress(wallet)
	if err != nil {
		return nil, err
	}

	mint, err := solana.PublicKeyFromBase58(strings.TrimSpace(contractAddr))
	if err != nil {
		return nil, fmt.Errorf("invalid SPL mint address: %w", err)
	}

	toOwner, err := solana.PublicKeyFromBase58(strings.TrimSpace(toAddress))
	if err != nil {
		return nil, fmt.Errorf("invalid destination address: %w", err)
	}

	srcATA, _, err := solana.FindAssociatedTokenAddress(from, mint)
	if err != nil {
		return nil, fmt.Errorf("derive source ATA: %w", err)
	}

	dstATA, _, err := solana.FindAssociatedTokenAddress(toOwner, mint)
	if err != nil {
		return nil, fmt.Errorf("derive destination ATA: %w", err)
	}

	// Get SPL token balance of source ATA
	balResult, err := rpcClient.GetTokenAccountBalance(ctx, srcATA, rpc.CommitmentFinalized)
	if err != nil {
		return nil, fmt.Errorf("solana SPL balanceOf %s failed: %w", srcATA, err)
	}
	if balResult == nil || balResult.Value == nil || balResult.Value.Amount == "0" {
		return nil, fmt.Errorf("solana SPL balance is zero for mint %s", contractAddr)
	}

	amount, ok := new(big.Int).SetString(balResult.Value.Amount, 10)
	if !ok || amount.Sign() <= 0 {
		return nil, fmt.Errorf("invalid SPL token amount: %s", balResult.Value.Amount)
	}
	transferAmount, err := solanaTokenAmountUint64(amount)
	if err != nil {
		return nil, err
	}

	// Check if destination ATA already exists
	instructions := make([]solana.Instruction, 0, 2)
	dstAccInfo, err := rpcClient.GetAccountInfo(ctx, dstATA)
	if err != nil || dstAccInfo == nil || dstAccInfo.Value == nil {
		// Create destination ATA (payer = source wallet)
		instructions = append(instructions,
			associatedtokenaccount.NewCreateInstruction(from, toOwner, mint).Build(),
		)
	}

	instructions = append(instructions,
		spltoken.NewTransferInstruction(
			transferAmount,
			srcATA,
			dstATA,
			from,
			nil,
		).Build(),
	)

	recent, err := rpcClient.GetLatestBlockhash(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return nil, fmt.Errorf("solana blockhash fetch failed: %w", err)
	}

	tx, err := solana.NewTransaction(instructions, recent.Value.Blockhash, solana.TransactionPayer(from))
	if err != nil {
		return nil, fmt.Errorf("solana SPL tx build failed: %w", err)
	}

	if _, err := tx.Sign(func(key solana.PublicKey) *solana.PrivateKey {
		if key.Equals(from) {
			return &privateKey
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("solana SPL tx signing failed: %w", err)
	}

	signature, err := rpcClient.SendTransaction(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("solana SPL tx broadcast failed: %w", err)
	}

	return &blockchain.TransactionResult{TxHash: signature.String(), Success: true}, nil
}

func (s *SolanaChain) sendSPL(ctx context.Context, wallet blockchain.WalletDetails, contractAddr, amountRaw, toAddress string) (*blockchain.TransactionResult, error) {
	amount, err := nativeAmountRaw(amountRaw)
	if err != nil {
		return nil, err
	}
	if !amount.IsUint64() {
		return nil, fmt.Errorf("solana SPL amount_raw exceeds uint64")
	}
	transferAmount, err := solanaTokenAmountUint64(amount)
	if err != nil {
		return nil, err
	}

	rpcClient, err := s.solanaRPCClient()
	if err != nil {
		return nil, err
	}

	privateKey, from, err := solanaPrivateKeyAndAddress(wallet)
	if err != nil {
		return nil, err
	}

	mint, err := solana.PublicKeyFromBase58(strings.TrimSpace(contractAddr))
	if err != nil {
		return nil, fmt.Errorf("invalid SPL mint address: %w", err)
	}
	toOwner, err := solana.PublicKeyFromBase58(strings.TrimSpace(toAddress))
	if err != nil {
		return nil, fmt.Errorf("invalid destination address: %w", err)
	}

	srcATA, _, err := solana.FindAssociatedTokenAddress(from, mint)
	if err != nil {
		return nil, fmt.Errorf("derive source ATA: %w", err)
	}
	dstATA, _, err := solana.FindAssociatedTokenAddress(toOwner, mint)
	if err != nil {
		return nil, fmt.Errorf("derive destination ATA: %w", err)
	}

	balResult, err := rpcClient.GetTokenAccountBalance(ctx, srcATA, rpc.CommitmentFinalized)
	if err != nil {
		return nil, fmt.Errorf("solana SPL balanceOf %s failed: %w", srcATA, err)
	}
	if balResult == nil || balResult.Value == nil {
		return nil, fmt.Errorf("solana SPL balance is unavailable for mint %s", contractAddr)
	}
	balance, ok := new(big.Int).SetString(balResult.Value.Amount, 10)
	if !ok || balance.Cmp(amount) < 0 {
		return nil, fmt.Errorf("solana SPL balance is not enough: balance=%s amount=%s", balResult.Value.Amount, amount.String())
	}

	instructions := make([]solana.Instruction, 0, 2)
	dstAccInfo, err := rpcClient.GetAccountInfo(ctx, dstATA)
	if err != nil || dstAccInfo == nil || dstAccInfo.Value == nil {
		instructions = append(instructions,
			associatedtokenaccount.NewCreateInstruction(from, toOwner, mint).Build(),
		)
	}
	instructions = append(instructions,
		spltoken.NewTransferInstruction(
			transferAmount,
			srcATA,
			dstATA,
			from,
			nil,
		).Build(),
	)

	recent, err := rpcClient.GetLatestBlockhash(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return nil, fmt.Errorf("solana blockhash fetch failed: %w", err)
	}
	tx, err := solana.NewTransaction(instructions, recent.Value.Blockhash, solana.TransactionPayer(from))
	if err != nil {
		return nil, fmt.Errorf("solana SPL tx build failed: %w", err)
	}
	if _, err := tx.Sign(func(key solana.PublicKey) *solana.PrivateKey {
		if key.Equals(from) {
			return &privateKey
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("solana SPL tx signing failed: %w", err)
	}
	signature, err := rpcClient.SendTransaction(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("solana SPL tx broadcast failed: %w", err)
	}
	return &blockchain.TransactionResult{TxHash: signature.String(), Success: true}, nil
}

func solanaTokenAmountUint64(amount *big.Int) (uint64, error) {
	if amount == nil || amount.Sign() <= 0 {
		return 0, errors.New("solana token amount must be greater than zero")
	}
	if !amount.IsUint64() {
		return 0, fmt.Errorf("solana token amount exceeds uint64")
	}
	return amount.Uint64(), nil
}

func (s *SolanaChain) PrefundGas(ctx context.Context, reserveWallet blockchain.WalletDetails, userAddress string) (bool, error) {
	rpcClient, err := s.solanaRPCClient()
	if err != nil {
		return false, err
	}

	userPubkey, err := solana.PublicKeyFromBase58(strings.TrimSpace(userAddress))
	if err != nil {
		return false, fmt.Errorf("invalid user address: %w", err)
	}

	balance, err := rpcClient.GetBalance(ctx, userPubkey, rpc.CommitmentFinalized)
	if err != nil {
		return false, fmt.Errorf("solana prefund balance check: %w", err)
	}

	if balance.Value >= solanaGasThresholdLamports() {
		return false, nil
	}

	amount := fmt.Sprintf("%d", solanaGasPrefundLamports())
	if _, err := s.Deposit(ctx, reserveWallet, amount, userAddress); err != nil {
		return false, fmt.Errorf("solana gas prefund failed: %w", err)
	}
	return true, nil
}
