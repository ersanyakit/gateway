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

	// Bitcoin Mainnet
	registry.Register(asset.NewBTC())

	// Solana Mainnet
	registry.Register(asset.NewSOL(constants.Solana))
	registry.Register(asset.NewSPL(constants.Solana, "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB", "USDT", "Tether USD", 6))

	// Tron Mainnet
	registry.Register(asset.NewTRX(constants.TRON))
	registry.Register(asset.NewTRC20(constants.TRON, "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t", "USDT", "Tether USD", 6))
	registry.Register(asset.NewTRC20(constants.TRON, "TYhWwKpw43ENFWBTGpzLHn3882f2au7SMi", "WBTC", "Wrapped BTC", 8))

	// Chiliz
	registry.Register(asset.NewEVMNative(constants.Chiliz, "CHZ", "Chiliz", 18))

	return registry
}
