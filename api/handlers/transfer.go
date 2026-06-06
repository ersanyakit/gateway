package handlers

import (
	"errors"
	"fmt"
	"strings"

	"core/blockchain"
	"core/constants"
	"core/models"
	"core/repositories"
	requesttypes "core/types"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

func HandleWithdraw(walletRepo *repositories.WalletRepo, chains *blockchain.ChainFactory) fiber.Handler {
	return func(c fiber.Ctx) error {
		var params requesttypes.TransferParams
		if err := c.Bind().Body(&params); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"success": false,
				"error":   "Invalid JSON body: " + err.Error(),
			})
		}

		params.Context = c.Context()
		if err := params.ValidateWithdraw(); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"success": false,
				"errors":  err.Error(),
			})
		}

		result, err := ExecuteWalletTransfer(walletRepo, chains, params, false)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"success": false,
				"error":   err.Error(),
			})
		}

		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"success": true,
			"tx_hash": result.TxHash,
		})
	}
}

func HandleSweep(walletRepo *repositories.WalletRepo, chains *blockchain.ChainFactory) fiber.Handler {
	return func(c fiber.Ctx) error {
		var params requesttypes.TransferParams
		if err := c.Bind().Body(&params); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"success": false,
				"error":   "Invalid JSON body: " + err.Error(),
			})
		}

		params.Context = c.Context()
		if err := params.ValidateSweep(); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"success": false,
				"errors":  err.Error(),
			})
		}

		result, err := ExecuteWalletTransfer(walletRepo, chains, params, true)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"success": false,
				"error":   err.Error(),
			})
		}

		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"success": true,
			"tx_hash": result.TxHash,
		})
	}
}

func ExecuteWalletTransfer(walletRepo *repositories.WalletRepo, chains *blockchain.ChainFactory, params requesttypes.TransferParams, sweep bool) (*blockchain.TransactionResult, error) {
	chain, err := chains.GetChain(*params.Chain)
	if err != nil {
		return nil, err
	}

	walletUUID, err := uuid.Parse(*params.WalletID)
	if err != nil {
		return nil, errors.New("invalid WalletID format")
	}

	wallet, err := walletRepo.FindByID(params.Context, walletUUID)
	if err != nil {
		return nil, err
	}

	derivedWallet, err := chain.CreateHDWallet(params.Context, int(wallet.HDAccountID), int(wallet.HDAddressId))
	if err != nil {
		return nil, err
	}
	if err := verifyDerivedWalletAddress(*wallet, chain.ChainID(), derivedWallet.Address); err != nil {
		return nil, err
	}

	if sweep {
		return chain.SweepTo(params.Context, *derivedWallet, *params.ToAddress)
	}

	return chain.Withdraw(params.Context, *derivedWallet, *params.AmountRaw, *params.ToAddress)
}

func verifyDerivedWalletAddress(wallet models.Wallet, chainID constants.ChainID, derivedAddress string) error {
	expected := walletAddressForChain(wallet, chainID)
	if expected == "" {
		return fmt.Errorf("wallet has no address for chain %d", chainID)
	}

	if isEVMChain(chainID) {
		if !strings.EqualFold(expected, derivedAddress) {
			return fmt.Errorf("derived address mismatch: expected %s got %s", expected, derivedAddress)
		}
		return nil
	}

	if expected != derivedAddress {
		return fmt.Errorf("derived address mismatch: expected %s got %s", expected, derivedAddress)
	}
	return nil
}

func walletAddressForChain(wallet models.Wallet, chainID constants.ChainID) string {
	switch chainID {
	case constants.Bitcoin:
		return wallet.BitcoinAddress
	case constants.Ethereum:
		return wallet.EthereumAddress
	case constants.Avalanche:
		return wallet.AvalancheAddress
	case constants.Binance:
		return wallet.BinanceAddress
	case constants.Base:
		return wallet.BaseAddress
	case constants.Arbitrum:
		return wallet.ArbitrumAddress
	case constants.Unichain:
		return wallet.UnichainAddress
	case constants.TRON:
		return wallet.TronAddress
	case constants.Solana:
		return wallet.SolanaAddress
	case constants.Chiliz:
		return wallet.ChilizAddress
	default:
		return ""
	}
}

func isEVMChain(chainID constants.ChainID) bool {
	switch chainID {
	case constants.Ethereum, constants.Avalanche, constants.Binance, constants.Base, constants.Arbitrum, constants.Unichain, constants.Chiliz:
		return true
	default:
		return false
	}
}
