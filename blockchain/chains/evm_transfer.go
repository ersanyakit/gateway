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
	"core/services/chainresource"
	"core/services/signer"

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

	from, err := evmWalletAddress(chainName, wallet)
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

	return evmSendNativeWithClient(ctx, client, wallet, from, chainName, chainID, amountWei, toAddress, gasPrice, "sweep.native")
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

	from, err := evmWalletAddress(chainName, wallet)
	if err != nil {
		return nil, err
	}

	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s gas price fetch failed: %w", chainName, err)
	}

	return evmSendNativeWithClient(ctx, client, wallet, from, chainName, chainID, amountWei, toAddress, gasPrice, "transfer.native")
}

func evmSendNativeWithClient(ctx context.Context, client *ethclient.Client, wallet blockchain.WalletDetails, from common.Address, chainName string, chainID constants.ChainID, amountWei *big.Int, toAddress string, gasPrice *big.Int, intent string) (*blockchain.TransactionResult, error) {
	if amountWei == nil || amountWei.Sign() <= 0 {
		return nil, errors.New("amount must be greater than zero")
	}
	if err := requireDatabaseResourceReservation(ctx, chainName, intent); err != nil {
		return nil, err
	}
	if err := chainresource.ValidateEVMGasPolicy(chainName, intent, gasPrice, evmNativeTransferGasLimit); err != nil {
		return nil, err
	}
	if err := evmVerifyChainID(ctx, client, chainName, chainID); err != nil {
		return nil, err
	}
	gasCost := new(big.Int).Mul(new(big.Int).Set(gasPrice), new(big.Int).SetUint64(evmNativeTransferGasLimit))
	requiredBalance := new(big.Int).Add(new(big.Int).Set(amountWei), gasCost)
	if err := evmEnsureNativeBalance(ctx, client, from, requiredBalance, chainName); err != nil {
		return nil, err
	}

	nonceReservation, err := chainResources.ReserveNonce(ctx, chainresource.NonceRequest{
		Chain:   chainName,
		Wallet:  from.Hex(),
		Intent:  intent,
		OwnerID: chainResourceOwnerID(ctx, wallet, intent),
	}, func(fetchCtx context.Context) (uint64, error) {
		return client.PendingNonceAt(fetchCtx, from)
	})
	if err != nil {
		return nil, fmt.Errorf("%s nonce reservation failed: %w", chainName, err)
	}

	var signedTx *types.Transaction
	if shouldUseExternalTransactionSigner(wallet) {
		signedTx, err = evmSignNativeWithCustody(ctx, chainName, chainID, wallet, nonceReservation.Nonce, gasPrice, amountWei, toAddress, intent)
		if err != nil {
			_ = nonceReservation.Release()
			return nil, err
		}
	} else {
		privateKey, _, err := evmPrivateKeyAndAddress(wallet)
		if err != nil {
			_ = nonceReservation.Release()
			return nil, err
		}
		signedTx, err = evmSignNativeWithTrustWallet(chainName, chainID, privateKey, nonceReservation.Nonce, gasPrice, amountWei, toAddress)
		if err != nil {
			_ = nonceReservation.Release()
			return nil, err
		}
	}
	txHash := signedTx.Hash().Hex()

	if err := client.SendTransaction(ctx, signedTx); err != nil {
		_ = nonceReservation.Consume(txHash)
		return nil, fmt.Errorf("%s tx broadcast failed: %w", chainName, err)
	}
	_ = nonceReservation.Consume(txHash)

	return &blockchain.TransactionResult{
		TxHash:  txHash,
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
	if signer.IsProduction() {
		return nil, common.Address{}, signer.ErrProductionSecretMaterialAccessDisabled
	}
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

func evmWalletAddress(chainName string, wallet blockchain.WalletDetails) (common.Address, error) {
	if !common.IsHexAddress(strings.TrimSpace(wallet.Address)) {
		return common.Address{}, fmt.Errorf("%s wallet address is required for chain resource reservation", chainName)
	}
	return common.HexToAddress(wallet.Address), nil
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

	from, err := evmWalletAddress(chainName, wallet)
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

	return evmSendNativeWithClient(ctx, client, wallet, from, chainName, chainID, amountWei, toAddress, gasPrice, "sweep.native")
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

	from, err := evmWalletAddress(chainName, wallet)
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

	return evmSendERC20WithClient(ctx, client, wallet, from, chainName, chainID, contractAddr, balance, toAddress, "sweep.token")
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

	from, err := evmWalletAddress(chainName, wallet)
	if err != nil {
		return nil, err
	}

	return evmSendERC20WithClient(ctx, client, wallet, from, chainName, chainID, contractAddr, amount, toAddress, "transfer.token")
}

func evmSendERC20WithClient(ctx context.Context, client *ethclient.Client, wallet blockchain.WalletDetails, from common.Address, chainName string, chainID constants.ChainID, contractAddr string, amount *big.Int, toAddress string, intent string) (*blockchain.TransactionResult, error) {
	if amount == nil || amount.Sign() <= 0 {
		return nil, errors.New("amount must be greater than zero")
	}
	if err := requireDatabaseResourceReservation(ctx, chainName, intent); err != nil {
		return nil, err
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

	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s gas price fetch failed: %w", chainName, err)
	}
	if err := chainresource.ValidateEVMGasPolicy(chainName, intent, gasPrice, erc20TransferGasLimit); err != nil {
		return nil, err
	}
	gasCost := new(big.Int).Mul(new(big.Int).Set(gasPrice), new(big.Int).SetUint64(erc20TransferGasLimit))
	if err := evmEnsureNativeBalance(ctx, client, from, gasCost, chainName); err != nil {
		return nil, err
	}

	nonceReservation, err := chainResources.ReserveNonce(ctx, chainresource.NonceRequest{
		Chain:   chainName,
		Wallet:  from.Hex(),
		Intent:  intent,
		OwnerID: chainResourceOwnerID(ctx, wallet, intent),
	}, func(fetchCtx context.Context) (uint64, error) {
		return client.PendingNonceAt(fetchCtx, from)
	})
	if err != nil {
		return nil, fmt.Errorf("%s nonce reservation failed: %w", chainName, err)
	}

	var signedTx *types.Transaction
	if shouldUseExternalTransactionSigner(wallet) {
		signedTx, err = evmSignERC20WithCustody(ctx, chainName, chainID, wallet, nonceReservation.Nonce, gasPrice, contractAddr, amount, toAddress, intent)
		if err != nil {
			_ = nonceReservation.Release()
			return nil, err
		}
	} else {
		privateKey, _, err := evmPrivateKeyAndAddress(wallet)
		if err != nil {
			_ = nonceReservation.Release()
			return nil, err
		}
		signedTx, err = evmSignERC20WithTrustWallet(chainName, chainID, privateKey, nonceReservation.Nonce, gasPrice, contractAddr, amount, toAddress)
		if err != nil {
			_ = nonceReservation.Release()
			return nil, err
		}
	}
	txHash := signedTx.Hash().Hex()
	if err := client.SendTransaction(ctx, signedTx); err != nil {
		_ = nonceReservation.Consume(txHash)
		return nil, fmt.Errorf("%s ERC-20 tx broadcast failed: %w", chainName, err)
	}
	_ = nonceReservation.Consume(txHash)
	return &blockchain.TransactionResult{TxHash: txHash, Success: true}, nil
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

func evmSignNativeWithCustody(ctx context.Context, chainName string, chainID constants.ChainID, wallet blockchain.WalletDetails, nonce uint64, gasPrice *big.Int, amount *big.Int, toAddress string, intent string) (*types.Transaction, error) {
	payload := map[string]any{
		"format":    "evm_legacy_unsigned_transaction.v1",
		"nonce":     nonce,
		"gas_price": decimalString(gasPrice),
		"gas_limit": evmNativeTransferGasLimit,
		"to":        common.HexToAddress(toAddress).Hex(),
		"value":     decimalString(amount),
		"data":      "0x",
	}
	response, err := signTransactionWithCustody(ctx, chainName, chainID, wallet, intent, decimalString(amount), toAddress, payload)
	if err != nil {
		return nil, err
	}
	return evmTransactionFromExternalSigner(chainName, chainID, response, nonce, common.HexToAddress(toAddress), amount, nil)
}

func evmSignERC20WithCustody(ctx context.Context, chainName string, chainID constants.ChainID, wallet blockchain.WalletDetails, nonce uint64, gasPrice *big.Int, contractAddr string, amount *big.Int, toAddress string, intent string) (*types.Transaction, error) {
	data := evmERC20TransferData(toAddress, amount)
	payload := map[string]any{
		"format":         "evm_legacy_unsigned_transaction.v1",
		"nonce":          nonce,
		"gas_price":      decimalString(gasPrice),
		"gas_limit":      erc20TransferGasLimit,
		"to":             common.HexToAddress(contractAddr).Hex(),
		"value":          "0",
		"data":           "0x" + fmt.Sprintf("%x", data),
		"token_contract": common.HexToAddress(contractAddr).Hex(),
		"token_to":       common.HexToAddress(toAddress).Hex(),
		"token_amount":   decimalString(amount),
	}
	response, err := signTransactionWithCustody(ctx, chainName, chainID, wallet, intent, decimalString(amount), toAddress, payload)
	if err != nil {
		return nil, err
	}
	return evmTransactionFromExternalSigner(chainName, chainID, response, nonce, common.HexToAddress(contractAddr), big.NewInt(0), data)
}

func evmTransactionFromExternalSigner(chainName string, chainID constants.ChainID, response signer.SignTransactionResponse, nonce uint64, to common.Address, value *big.Int, data []byte) (*types.Transaction, error) {
	signedBytes, err := signedPayloadBytes(response)
	if err != nil {
		return nil, fmt.Errorf("%s external signer transaction missing: %w", chainName, err)
	}
	signedTx := new(types.Transaction)
	if err := signedTx.UnmarshalBinary(signedBytes); err != nil {
		return nil, fmt.Errorf("%s external signer transaction decode failed: %w", chainName, err)
	}
	if signedTx.Nonce() != nonce {
		return nil, fmt.Errorf("%s external signer nonce mismatch: expected %d got %d", chainName, nonce, signedTx.Nonce())
	}
	if signedTx.To() == nil || !strings.EqualFold(signedTx.To().Hex(), to.Hex()) {
		got := "<nil>"
		if signedTx.To() != nil {
			got = signedTx.To().Hex()
		}
		return nil, fmt.Errorf("%s external signer destination mismatch: expected %s got %s", chainName, to.Hex(), got)
	}
	if value == nil {
		value = big.NewInt(0)
	}
	if signedTx.Value().Cmp(value) != 0 {
		return nil, fmt.Errorf("%s external signer value mismatch: expected %s got %s", chainName, value.String(), signedTx.Value().String())
	}
	if data != nil && !bytesEqual(signedTx.Data(), data) {
		return nil, fmt.Errorf("%s external signer calldata mismatch", chainName)
	}
	if signedTx.ChainId() == nil || signedTx.ChainId().Cmp(big.NewInt(int64(chainID))) != 0 {
		got := "<nil>"
		if signedTx.ChainId() != nil {
			got = signedTx.ChainId().String()
		}
		return nil, fmt.Errorf("%s external signer chain id mismatch: expected %d got %s", chainName, chainID, got)
	}
	return signedTx, nil
}

func evmERC20TransferData(toAddress string, amount *big.Int) []byte {
	data := make([]byte, 0, 4+32+32)
	data = append(data, []byte{0xa9, 0x05, 0x9c, 0xbb}...)
	to := common.HexToAddress(toAddress)
	data = append(data, common.LeftPadBytes(to.Bytes(), 32)...)
	data = append(data, common.LeftPadBytes(amount.Bytes(), 32)...)
	return data
}

func decimalString(value *big.Int) string {
	if value == nil {
		return "0"
	}
	return value.String()
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
