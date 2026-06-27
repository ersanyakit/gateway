package chains

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"

	"core/blockchain"
	"core/blockchain/walletcore"
	"core/constants"
	"core/contracts/erc20"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	twcommon "tw/protos/common"
	twethereum "tw/protos/ethereum"
)

const evmNativeTransferGasLimit uint64 = 21000

func evmDepositNative(ctx context.Context, chainName string, chainID constants.ChainID, rpcs []string, wallet blockchain.WalletDetails, amountRaw string, toAddress string) (*blockchain.TransactionResult, error) {
	return evmWithdrawNative(ctx, chainName, chainID, rpcs, wallet, amountRaw, toAddress)
}

func evmWithdrawNative(ctx context.Context, chainName string, chainID constants.ChainID, rpcs []string, wallet blockchain.WalletDetails, amountRaw string, toAddress string) (*blockchain.TransactionResult, error) {
	amountWei, err := nativeAmountRaw(amountRaw)
	if err != nil {
		return nil, err
	}
	return evmSendNative(ctx, chainName, chainID, rpcs, wallet, amountWei, toAddress)
}

func evmWithdrawERC20(ctx context.Context, chainName string, chainID constants.ChainID, rpcs []string, wallet blockchain.WalletDetails, contractAddr, amountRaw, toAddress string) (*blockchain.TransactionResult, error) {
	amount, err := nativeAmountRaw(amountRaw)
	if err != nil {
		return nil, err
	}
	return evmSendERC20(ctx, chainName, chainID, rpcs, wallet, contractAddr, amount, toAddress)
}

func evmSweepNative(ctx context.Context, chainName string, chainID constants.ChainID, rpcs []string, wallet blockchain.WalletDetails) (*blockchain.TransactionResult, error) {
	toAddress, err := evmSweepDestination(chainName)
	if err != nil {
		return nil, err
	}
	if !common.IsHexAddress(toAddress) {
		return nil, fmt.Errorf("invalid sweep address for %s: %s", chainName, toAddress)
	}
	if err := authorizeWalletSigning(ctx, chainName, chainID, wallet, "sweep.native", "max", toAddress); err != nil {
		return nil, err
	}

	client, err := dialFirstEVMRPC(ctx, rpcs)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	privateKey, from, err := evmPrivateKeyAndAddress(wallet)
	if err != nil {
		return nil, err
	}

	balance, err := client.BalanceAt(ctx, from, nil)
	if err != nil {
		return nil, fmt.Errorf("%s balance fetch failed: %w", chainName, err)
	}

	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s gas price fetch failed: %w", chainName, err)
	}

	gasCost := new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(evmNativeTransferGasLimit))
	amountWei := new(big.Int).Sub(balance, gasCost)
	if amountWei.Sign() <= 0 {
		return nil, fmt.Errorf("%s sweep balance is not enough for gas: balance=%s gas_cost=%s", chainName, balance.String(), gasCost.String())
	}

	return evmSendNativeWithClient(ctx, client, privateKey, from, chainName, chainID, amountWei, toAddress, gasPrice)
}

func evmSendNative(ctx context.Context, chainName string, chainID constants.ChainID, rpcs []string, wallet blockchain.WalletDetails, amountWei *big.Int, toAddress string) (*blockchain.TransactionResult, error) {
	if !common.IsHexAddress(toAddress) {
		return nil, fmt.Errorf("invalid recipient address for %s: %s", chainName, toAddress)
	}
	amountText := ""
	if amountWei != nil {
		amountText = amountWei.String()
	}
	if err := authorizeWalletSigning(ctx, chainName, chainID, wallet, "transfer.native", amountText, toAddress); err != nil {
		return nil, err
	}

	client, err := dialFirstEVMRPC(ctx, rpcs)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	privateKey, from, err := evmPrivateKeyAndAddress(wallet)
	if err != nil {
		return nil, err
	}

	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s gas price fetch failed: %w", chainName, err)
	}

	return evmSendNativeWithClient(ctx, client, privateKey, from, chainName, chainID, amountWei, toAddress, gasPrice)
}

func evmSendNativeWithClient(ctx context.Context, client *ethclient.Client, privateKey *ecdsa.PrivateKey, from common.Address, chainName string, chainID constants.ChainID, amountWei *big.Int, toAddress string, gasPrice *big.Int) (*blockchain.TransactionResult, error) {
	if amountWei == nil || amountWei.Sign() <= 0 {
		return nil, errors.New("amount must be greater than zero")
	}
	if gasPrice == nil || gasPrice.Sign() <= 0 {
		return nil, fmt.Errorf("%s gas price must be greater than zero", chainName)
	}
	if err := evmVerifyChainID(ctx, client, chainName, chainID); err != nil {
		return nil, err
	}
	gasCost := new(big.Int).Mul(new(big.Int).Set(gasPrice), new(big.Int).SetUint64(evmNativeTransferGasLimit))
	requiredBalance := new(big.Int).Add(new(big.Int).Set(amountWei), gasCost)
	if err := evmEnsureNativeBalance(ctx, client, from, requiredBalance, chainName); err != nil {
		return nil, err
	}

	nonce, err := client.PendingNonceAt(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("%s nonce fetch failed: %w", chainName, err)
	}

	signedTx, err := evmSignNativeWithTrustWallet(chainName, chainID, privateKey, nonce, gasPrice, amountWei, toAddress)
	if err != nil {
		return nil, err
	}

	if err := client.SendTransaction(ctx, signedTx); err != nil {
		return nil, fmt.Errorf("%s tx broadcast failed: %w", chainName, err)
	}

	return &blockchain.TransactionResult{
		TxHash:  signedTx.Hash().Hex(),
		Success: true,
	}, nil
}

func dialFirstEVMRPC(ctx context.Context, rpcs []string) (*ethclient.Client, error) {
	var lastErr error
	for _, rpcURL := range rpcs {
		rpcURL = strings.TrimSpace(rpcURL)
		if rpcURL == "" {
			continue
		}

		client, err := ethclient.DialContext(ctx, rpcURL)
		if err != nil {
			lastErr = err
			continue
		}
		return client, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("no RPC endpoint configured")
}

func evmPrivateKeyAndAddress(wallet blockchain.WalletDetails) (*ecdsa.PrivateKey, common.Address, error) {
	privateKeyHex := strings.TrimPrefix(strings.TrimSpace(wallet.PrivateKey), "0x")
	if privateKeyHex == "" {
		return nil, common.Address{}, errors.New("private key is required")
	}

	privateKey, err := crypto.HexToECDSA(privateKeyHex)
	if err != nil {
		return nil, common.Address{}, fmt.Errorf("invalid private key: %w", err)
	}

	from := crypto.PubkeyToAddress(privateKey.PublicKey)
	if wallet.Address != "" && !strings.EqualFold(wallet.Address, from.Hex()) {
		return nil, common.Address{}, fmt.Errorf("private key does not match wallet address: expected %s got %s", wallet.Address, from.Hex())
	}

	return privateKey, from, nil
}

func nativeAmountRaw(value string) (*big.Int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("amount_raw is required")
	}
	if strings.HasPrefix(value, "-") || strings.Contains(value, ".") {
		return nil, errors.New("amount_raw must be a positive integer")
	}

	amount, ok := new(big.Int).SetString(value, 10)
	if !ok || amount.Sign() <= 0 {
		return nil, errors.New("invalid amount_raw")
	}
	return amount, nil
}

// evmSweepNativeTo sweeps all native balance from wallet to a specific address.
func evmSweepNativeTo(ctx context.Context, chainName string, chainID constants.ChainID, rpcs []string, wallet blockchain.WalletDetails, toAddress string) (*blockchain.TransactionResult, error) {
	if !common.IsHexAddress(toAddress) {
		return nil, fmt.Errorf("invalid sweep-to address for %s: %s", chainName, toAddress)
	}
	if err := authorizeWalletSigning(ctx, chainName, chainID, wallet, "sweep.native", "max", toAddress); err != nil {
		return nil, err
	}

	client, err := dialFirstEVMRPC(ctx, rpcs)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	privateKey, from, err := evmPrivateKeyAndAddress(wallet)
	if err != nil {
		return nil, err
	}

	balance, err := client.BalanceAt(ctx, from, nil)
	if err != nil {
		return nil, fmt.Errorf("%s balance fetch failed: %w", chainName, err)
	}

	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s gas price fetch failed: %w", chainName, err)
	}

	gasCost := new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(evmNativeTransferGasLimit))
	amountWei := new(big.Int).Sub(balance, gasCost)
	if amountWei.Sign() <= 0 {
		return nil, fmt.Errorf("%s sweep balance not enough for gas: balance=%s gas_cost=%s", chainName, balance.String(), gasCost.String())
	}

	return evmSendNativeWithClient(ctx, client, privateKey, from, chainName, chainID, amountWei, toAddress, gasPrice)
}

// evmNativeBalance returns the current native balance (in wei) for address.
func evmNativeBalance(ctx context.Context, rpcs []string, address string) (*big.Int, error) {
	if !common.IsHexAddress(address) {
		return nil, fmt.Errorf("invalid EVM address: %s", address)
	}
	client, err := dialFirstEVMRPC(ctx, rpcs)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	return client.BalanceAt(ctx, common.HexToAddress(address), nil)
}

func evmVerifyChainID(ctx context.Context, client *ethclient.Client, chainName string, chainID constants.ChainID) error {
	got, err := client.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("%s chain id fetch failed: %w", chainName, err)
	}
	want := big.NewInt(int64(chainID))
	if got == nil || got.Cmp(want) != 0 {
		gotText := "<nil>"
		if got != nil {
			gotText = got.String()
		}
		return fmt.Errorf("%s RPC chain id mismatch: expected %s got %s", chainName, want.String(), gotText)
	}
	return nil
}

func evmEnsureNativeBalance(ctx context.Context, client *ethclient.Client, from common.Address, required *big.Int, chainName string) error {
	if required == nil || required.Sign() < 0 {
		return fmt.Errorf("%s required balance is invalid", chainName)
	}
	balance, err := client.BalanceAt(ctx, from, nil)
	if err != nil {
		return fmt.Errorf("%s native balance fetch failed: %w", chainName, err)
	}
	if balance.Cmp(required) < 0 {
		return fmt.Errorf("%s native balance is not enough: balance=%s required=%s", chainName, balance.String(), required.String())
	}
	return nil
}

const erc20TransferGasLimit uint64 = 65000

// evmSweepERC20To transfers the full ERC-20 token balance from wallet to toAddress.
// The caller must ensure wallet has enough native balance for gas before calling this.
func evmSweepERC20To(ctx context.Context, chainName string, chainID constants.ChainID, rpcs []string, wallet blockchain.WalletDetails, contractAddr, toAddress string) (*blockchain.TransactionResult, error) {
	if !common.IsHexAddress(contractAddr) {
		return nil, fmt.Errorf("invalid token contract address: %s", contractAddr)
	}
	if !common.IsHexAddress(toAddress) {
		return nil, fmt.Errorf("invalid sweep-to address for %s: %s", chainName, toAddress)
	}
	if err := authorizeWalletSigning(ctx, chainName, chainID, wallet, "sweep.token", "max", toAddress); err != nil {
		return nil, err
	}

	client, err := dialFirstEVMRPC(ctx, rpcs)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	privateKey, from, err := evmPrivateKeyAndAddress(wallet)
	if err != nil {
		return nil, err
	}

	tokenContract := common.HexToAddress(contractAddr)
	caller, err := erc20.NewERC20Caller(tokenContract, client)
	if err != nil {
		return nil, fmt.Errorf("%s ERC-20 caller init: %w", chainName, err)
	}

	balance, err := caller.BalanceOf(&bind.CallOpts{Context: ctx}, from)
	if err != nil {
		return nil, fmt.Errorf("%s ERC-20 balanceOf failed: %w", chainName, err)
	}
	if balance == nil || balance.Sign() <= 0 {
		return nil, fmt.Errorf("%s ERC-20 balance is zero for %s", chainName, from.Hex())
	}

	return evmSendERC20WithClient(ctx, client, privateKey, from, chainName, chainID, contractAddr, balance, toAddress)
}

func evmSendERC20(ctx context.Context, chainName string, chainID constants.ChainID, rpcs []string, wallet blockchain.WalletDetails, contractAddr string, amount *big.Int, toAddress string) (*blockchain.TransactionResult, error) {
	if !common.IsHexAddress(contractAddr) {
		return nil, fmt.Errorf("invalid token contract address: %s", contractAddr)
	}
	if !common.IsHexAddress(toAddress) {
		return nil, fmt.Errorf("invalid token recipient for %s: %s", chainName, toAddress)
	}
	amountText := ""
	if amount != nil {
		amountText = amount.String()
	}
	if err := authorizeWalletSigning(ctx, chainName, chainID, wallet, "transfer.token", amountText, toAddress); err != nil {
		return nil, err
	}

	client, err := dialFirstEVMRPC(ctx, rpcs)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	privateKey, from, err := evmPrivateKeyAndAddress(wallet)
	if err != nil {
		return nil, err
	}

	return evmSendERC20WithClient(ctx, client, privateKey, from, chainName, chainID, contractAddr, amount, toAddress)
}

func evmSendERC20WithClient(ctx context.Context, client *ethclient.Client, privateKey *ecdsa.PrivateKey, from common.Address, chainName string, chainID constants.ChainID, contractAddr string, amount *big.Int, toAddress string) (*blockchain.TransactionResult, error) {
	if amount == nil || amount.Sign() <= 0 {
		return nil, errors.New("amount must be greater than zero")
	}
	if !common.IsHexAddress(contractAddr) {
		return nil, fmt.Errorf("invalid token contract address: %s", contractAddr)
	}
	if !common.IsHexAddress(toAddress) {
		return nil, fmt.Errorf("invalid token recipient for %s: %s", chainName, toAddress)
	}

	tokenContract := common.HexToAddress(contractAddr)
	caller, err := erc20.NewERC20Caller(tokenContract, client)
	if err != nil {
		return nil, fmt.Errorf("%s ERC-20 caller init: %w", chainName, err)
	}
	balance, err := caller.BalanceOf(&bind.CallOpts{Context: ctx}, from)
	if err != nil {
		return nil, fmt.Errorf("%s ERC-20 balanceOf failed: %w", chainName, err)
	}
	if balance == nil || balance.Cmp(amount) < 0 {
		return nil, fmt.Errorf("%s ERC-20 balance is not enough: balance=%s amount=%s", chainName, balance.String(), amount.String())
	}
	if err := evmVerifyChainID(ctx, client, chainName, chainID); err != nil {
		return nil, err
	}

	nonce, err := client.PendingNonceAt(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("%s nonce fetch failed: %w", chainName, err)
	}
	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s gas price fetch failed: %w", chainName, err)
	}
	if gasPrice == nil || gasPrice.Sign() <= 0 {
		return nil, fmt.Errorf("%s gas price must be greater than zero", chainName)
	}
	gasCost := new(big.Int).Mul(new(big.Int).Set(gasPrice), new(big.Int).SetUint64(erc20TransferGasLimit))
	if err := evmEnsureNativeBalance(ctx, client, from, gasCost, chainName); err != nil {
		return nil, err
	}

	signedTx, err := evmSignERC20WithTrustWallet(chainName, chainID, privateKey, nonce, gasPrice, contractAddr, amount, toAddress)
	if err != nil {
		return nil, err
	}
	if err := client.SendTransaction(ctx, signedTx); err != nil {
		return nil, fmt.Errorf("%s ERC-20 tx broadcast failed: %w", chainName, err)
	}
	return &blockchain.TransactionResult{TxHash: signedTx.Hash().Hex(), Success: true}, nil
}

func evmSignNativeWithTrustWallet(chainName string, chainID constants.ChainID, privateKey *ecdsa.PrivateKey, nonce uint64, gasPrice *big.Int, amount *big.Int, toAddress string) (*types.Transaction, error) {
	input := &twethereum.SigningInput{
		ChainId:    evmBigIntBytes(big.NewInt(int64(chainID))),
		Nonce:      evmUint64Bytes(nonce),
		TxMode:     twethereum.TransactionMode_Legacy,
		GasPrice:   evmBigIntBytes(gasPrice),
		GasLimit:   evmUint64Bytes(evmNativeTransferGasLimit),
		ToAddress:  toAddress,
		PrivateKey: crypto.FromECDSA(privateKey),
		Transaction: &twethereum.Transaction{
			TransactionOneof: &twethereum.Transaction_Transfer_{
				Transfer: &twethereum.Transaction_Transfer{
					Amount: evmBigIntBytes(amount),
					Data:   []byte{},
				},
			},
		},
	}
	var output twethereum.SigningOutput
	if err := walletcore.Sign(input, &output, chainID); err != nil {
		return nil, fmt.Errorf("%s Trust Wallet Core native signing failed: %w", chainName, err)
	}
	return evmTransactionFromTrustWalletOutput(chainName, "native", &output)
}

func evmSignERC20WithTrustWallet(chainName string, chainID constants.ChainID, privateKey *ecdsa.PrivateKey, nonce uint64, gasPrice *big.Int, contractAddr string, amount *big.Int, toAddress string) (*types.Transaction, error) {
	input := &twethereum.SigningInput{
		ChainId:    evmBigIntBytes(big.NewInt(int64(chainID))),
		Nonce:      evmUint64Bytes(nonce),
		TxMode:     twethereum.TransactionMode_Legacy,
		GasPrice:   evmBigIntBytes(gasPrice),
		GasLimit:   evmUint64Bytes(erc20TransferGasLimit),
		ToAddress:  common.HexToAddress(contractAddr).Hex(),
		PrivateKey: crypto.FromECDSA(privateKey),
		Transaction: &twethereum.Transaction{
			TransactionOneof: &twethereum.Transaction_Erc20Transfer{
				Erc20Transfer: &twethereum.Transaction_ERC20Transfer{
					To:     toAddress,
					Amount: evmBigIntBytes(amount),
				},
			},
		},
	}
	var output twethereum.SigningOutput
	if err := walletcore.Sign(input, &output, chainID); err != nil {
		return nil, fmt.Errorf("%s Trust Wallet Core ERC-20 signing failed: %w", chainName, err)
	}
	return evmTransactionFromTrustWalletOutput(chainName, "ERC-20", &output)
}

func evmTransactionFromTrustWalletOutput(chainName string, kind string, output *twethereum.SigningOutput) (*types.Transaction, error) {
	if output.GetError() != twcommon.SigningError_OK {
		msg := strings.TrimSpace(output.GetErrorMessage())
		if msg == "" {
			msg = output.GetError().String()
		}
		return nil, fmt.Errorf("%s Trust Wallet Core %s signing error: %s", chainName, kind, msg)
	}
	encoded := output.GetEncoded()
	if len(encoded) == 0 {
		return nil, fmt.Errorf("%s Trust Wallet Core %s signing returned empty transaction", chainName, kind)
	}
	signedTx := new(types.Transaction)
	if err := signedTx.UnmarshalBinary(encoded); err != nil {
		return nil, fmt.Errorf("%s Trust Wallet Core %s transaction decode failed: %w", chainName, kind, err)
	}
	return signedTx, nil
}

func evmUint64Bytes(value uint64) []byte {
	return evmBigIntBytes(new(big.Int).SetUint64(value))
}

func evmBigIntBytes(value *big.Int) []byte {
	if value == nil {
		return nil
	}
	if value.Sign() == 0 {
		return []byte{0}
	}
	return value.Bytes()
}

// evmPrefundGas sends minGas wei from reserveWallet to userAddress if the user's native balance is below threshold.
// Returns true if a prefund transfer was actually sent.
func evmPrefundGas(ctx context.Context, chainName string, chainID constants.ChainID, rpcs []string, reserveWallet blockchain.WalletDetails, userAddress string, threshold, prefundAmount *big.Int) (bool, error) {
	prefundAmountText := ""
	if prefundAmount != nil {
		prefundAmountText = prefundAmount.String()
	}
	if err := authorizeWalletSigning(ctx, chainName, chainID, reserveWallet, "prefund.native", prefundAmountText, userAddress); err != nil {
		return false, err
	}

	balance, err := evmNativeBalance(ctx, rpcs, userAddress)
	if err != nil {
		return false, fmt.Errorf("%s native balance check: %w", chainName, err)
	}
	if balance.Cmp(threshold) >= 0 {
		return false, nil // already has enough gas
	}
	_, err = evmSendNative(ctx, chainName, chainID, rpcs, reserveWallet, prefundAmount, userAddress)
	if err != nil {
		return false, fmt.Errorf("%s gas prefund failed: %w", chainName, err)
	}
	return true, nil
}

func evmGasThreshold() *big.Int {
	if raw := strings.TrimSpace(os.Getenv("EVM_GAS_THRESHOLD_WEI")); raw != "" {
		if v, ok := new(big.Int).SetString(raw, 10); ok && v.Sign() > 0 {
			return v
		}
	}
	return new(big.Int).SetInt64(500_000_000_000_000) // 0.0005 ETH default
}

func evmGasPrefundAmount() *big.Int {
	if raw := strings.TrimSpace(os.Getenv("EVM_GAS_PREFUND_WEI")); raw != "" {
		if v, ok := new(big.Int).SetString(raw, 10); ok && v.Sign() > 0 {
			return v
		}
	}
	return new(big.Int).SetInt64(2_000_000_000_000_000) // 0.002 ETH default
}

func evmSweepDestination(chainName string) (string, error) {
	envName := strings.ToUpper(strings.NewReplacer("-", "_").Replace(chainName)) + "_SWEEP_ADDRESS"
	for _, key := range []string{envName, "EVM_SWEEP_ADDRESS", "SWEEP_ADDRESS"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value, nil
		}
	}
	return "", fmt.Errorf("sweep destination is required: set %s, EVM_SWEEP_ADDRESS or SWEEP_ADDRESS", envName)
}

func unsupportedTransfer(chainName string) (*blockchain.TransactionResult, error) {
	return nil, fmt.Errorf("%s transfer/sweep broadcast is not implemented yet", chainName)
}
