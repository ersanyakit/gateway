package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"core/asset"
	"core/blockchain"
	"core/constants"
	"core/models"
	"core/repositories"
	"core/services/signer"
	requesttypes "core/types"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

func HandleWithdraw(walletRepo *repositories.WalletRepo, chains *blockchain.ChainFactory) fiber.Handler {
	return func(c fiber.Ctx) error {
		if _, ok := requireAdmin(c); !ok {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"success": false,
				"error":   "admin authentication required",
			})
		}

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

		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"success": false,
			"error":   repositories.ErrLedgerReservationRequired.Error(),
		})
	}
}

func resolveWithdrawalAsset(registry *asset.Registry, chainName string, symbol string, tokenAddress string) (string, *string, string, uint8, error) {
	chain := strings.ToLower(strings.TrimSpace(chainName))
	if chain == "" {
		return "", nil, "", 0, errors.New("chain is required")
	}
	chainID := chainSlugToID(chain)
	if !constants.IsSupportedChainID(chainID) {
		return "", nil, "", 0, fmt.Errorf("unsupported chain: %s", chainName)
	}
	chain = constants.ChainName(chainID)
	if registry == nil {
		return chain, nil, strings.ToUpper(chain), 0, nil
	}

	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	tokenAddress = strings.TrimSpace(tokenAddress)

	var assetInfo asset.Asset
	var ok bool
	if tokenAddress != "" {
		assetInfo, ok = registry.Get(chainID, tokenAddress)
		if !ok {
			return "", nil, "", 0, fmt.Errorf("unsupported token for %s: %s", chain, tokenAddress)
		}
	} else if symbol != "" {
		assetInfo, ok = registry.GetBySymbol(chainID, symbol)
		if !ok {
			return "", nil, "", 0, fmt.Errorf("unsupported asset for %s: %s", chain, symbol)
		}
	} else {
		assetInfo, ok = registry.GetNative(chainID)
		if !ok {
			return "", nil, "", 0, fmt.Errorf("native asset is not registered for %s", chain)
		}
	}

	var token *string
	if !assetInfo.IsNative() {
		value := assetInfo.GetIdentifier()
		token = &value
	}
	return chain, token, assetInfo.GetSymbol(), assetInfo.GetDecimals(), nil
}

func HandleSweep(walletRepo *repositories.WalletRepo, chains *blockchain.ChainFactory) fiber.Handler {
	return func(c fiber.Ctx) error {
		if _, ok := requireAdmin(c); !ok {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"success": false,
				"error":   "admin authentication required",
			})
		}

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

		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"success": false,
			"error":   repositories.ErrLedgerReservationRequired.Error(),
		})
	}
}

func ExecuteWalletTransfer(walletRepo *repositories.WalletRepo, chains *blockchain.ChainFactory, params requesttypes.TransferParams, sweep bool) (*blockchain.TransactionResult, error) {
	return nil, repositories.ErrLedgerReservationRequired
}

func ExecuteReservedWalletTransfer(walletRepo *repositories.WalletRepo, chains *blockchain.ChainFactory, params requesttypes.TransferParams, sweep bool) (*blockchain.TransactionResult, error) {
	if sweep {
		return nil, repositories.ErrLedgerReservationRequired
	}
	params.Context = transferContextWithSignerAudit(params.Context, params)

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

	if params.Token != nil && strings.TrimSpace(*params.Token) != "" {
		return chain.WithdrawToken(params.Context, *derivedWallet, strings.TrimSpace(*params.Token), *params.AmountRaw, *params.ToAddress)
	}

	return chain.Withdraw(params.Context, *derivedWallet, *params.AmountRaw, *params.ToAddress)
}

func transferContextWithSignerAudit(ctx context.Context, params requesttypes.TransferParams) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(params.ActorID) == "" &&
		strings.TrimSpace(params.JobID) == "" &&
		strings.TrimSpace(params.CorrelationID) == "" {
		return ctx
	}
	return signer.WithAuditContext(ctx, signer.AuditContext{
		ActorID:       strings.TrimSpace(params.ActorID),
		JobID:         strings.TrimSpace(params.JobID),
		CorrelationID: strings.TrimSpace(params.CorrelationID),
	})
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
	case constants.TRON, constants.TRONTestnet:
		return wallet.TronAddress
	case constants.Solana:
		return wallet.SolanaAddress
	case constants.Chiliz:
		return wallet.ChilizAddress
	case constants.ChilizSpicy:
		return wallet.ChilizSpicyAddress
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
