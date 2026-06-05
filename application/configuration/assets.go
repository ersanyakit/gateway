package application

import (
	"core/asset"
	"core/constants"
)

func NewAssetRegistry() *asset.Registry {
	registry := asset.NewRegistry()

	// Ethereum
	registry.Register(asset.NewEVMNative(constants.Ethereum, "ETH", "Ethereum", 18))
	registry.Register(asset.NewERC20(constants.Ethereum, "0xdAC17F958D2ee523a2206206994597C13D831ec7", "USDT", "Tether USD", 6))
	registry.Register(asset.NewERC20(constants.Ethereum, "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", "USDC", "USDC", 6))
	registry.Register(asset.NewERC20(constants.Ethereum, "0x2260FAC5E5542a773Aa44fBCfeDf7C193bc2C599", "WBTC", "Wrapped BTC", 8))

	// Avalanche
	registry.Register(asset.NewEVMNative(constants.Avalanche, "AVAX", "Avalanche", 18))
	registry.Register(asset.NewERC20(constants.Avalanche, "0x0555E30da8f98308EdB960aa94C0Db47230d2B9c", "WBTC", "Wrapped BTC", 8))

	// BNB
	registry.Register(asset.NewEVMNative(constants.Binance, "BNB", "Binance Coin", 18))
	registry.Register(asset.NewERC20(constants.Binance, "0x0555E30da8f98308EdB960aa94C0Db47230d2B9c", "WBTC", "Wrapped BTC", 8))

	// Base
	registry.Register(asset.NewEVMNative(constants.Base, "ETH", "Base Ether", 18))
	registry.Register(asset.NewERC20(constants.Base, "0x4200000000000000000000000000000000000006", "WETH", "Wrapped Ether", 18))
	registry.Register(asset.NewERC20(constants.Base, "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913", "USDC", "USDC", 6))

	// Unichain
	registry.Register(asset.NewEVMNative(constants.Unichain, "ETH", "Unichain Ether", 18))
	registry.Register(asset.NewERC20(constants.Unichain, "0x4200000000000000000000000000000000000006", "WETH", "Wrapped Ether", 18))

	// Bitcoin Mainnet
	registry.Register(asset.NewBTC())

	// Solana Mainnet
	registry.Register(asset.NewSOL(constants.Solana))
	registry.Register(asset.NewSPL(constants.Solana, "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB", "USDT", "Tether USD", 6))
	registry.Register(asset.NewSPL(constants.Solana, "9qpnNkj8wqecEhnrKwzhNAtzSknizFqDEzxPd1Ajpump", "LGBT", "COOLVIBES", 6))

	// Tron Mainnet
	registry.Register(asset.NewTRX(constants.TRON))
	registry.Register(asset.NewTRC20(constants.TRON, "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t", "USDT", "Tether USD", 6))
	registry.Register(asset.NewTRC20(constants.TRON, "TYhWwKpw43ENFWBTGpzLHn3882f2au7SMi", "WBTC", "Wrapped BTC", 8))

	// Chiliz Mainnet
	registry.Register(asset.NewEVMNative(constants.Chiliz, "CHZ", "Chiliz", 18))

	// Chiliz Spicy Testnet
	registry.Register(asset.NewEVMNative(constants.ChilizSpicy, "CHZ", "Chiliz Spicy (Testnet)", 18))

	// Wrapped token aliases — share logo, price group, and display name with the underlying asset.
	registry.RegisterAlias("WBTC", "BTC")
	registry.RegisterAlias("WETH", "ETH")
	registry.RegisterAlias("WBNB", "BNB")
	registry.RegisterAlias("WCHZ", "CHZ")

	return registry
}
