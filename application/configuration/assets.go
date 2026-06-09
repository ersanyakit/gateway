package application

import (
	"core/asset"
	"core/constants"
)

func NewAssetRegistry() *asset.Registry {
	registry := asset.NewRegistry()

	for _, def := range defaultAssetDefinitions() {
		registry.RegisterDefinition(def)
	}

	registry.RegisterAlias("WBTC", "BTC")
	registry.RegisterAlias("WETH", "ETH")
	registry.RegisterAlias("WBNB", "BNB")
	registry.RegisterAlias("WAVAX", "AVAX")
	registry.RegisterAlias("WCHZ", "CHZ")
	registry.RegisterAlias("WSOL", "SOL")

	return registry
}

func defaultAssetDefinitions() []asset.AssetDefinition {
	return []asset.AssetDefinition{
		{
			Symbol:   "BTC",
			Name:     "Bitcoin",
			Type:     asset.AssetNative,
			Decimals: 8,
			LogoSlug: "btc",
			Deployments: []asset.Deployment{
				{ChainID: constants.Bitcoin, Native: true, Enabled: true},
				{ChainID: constants.Ethereum, Symbol: "WBTC", Name: "Wrapped BTC", Address: "0x2260FAC5E5542a773Aa44fBCfeDf7C193bc2C599", Decimals: 8, Enabled: true},
				{ChainID: constants.Avalanche, Symbol: "WBTC", Name: "Wrapped BTC", Address: "0x0555E30da8f98308EdB960aa94C0Db47230d2B9c", Decimals: 8, Enabled: true},
				{ChainID: constants.Binance, Symbol: "WBTC", Name: "Wrapped BTC", Address: "0x0555E30da8f98308EdB960aa94C0Db47230d2B9c", Decimals: 8, Enabled: true},
				{ChainID: constants.TRON, Symbol: "WBTC", Name: "Wrapped BTC", Address: "TYhWwKpw43ENFWBTGpzLHn3882f2au7SMi", Decimals: 8, Enabled: true},
			},
		},
		{
			Symbol:   "ETH",
			Name:     "Ether",
			Type:     asset.AssetNative,
			Decimals: 18,
			LogoSlug: "eth",
			Deployments: []asset.Deployment{
				{ChainID: constants.Ethereum, Native: true, Enabled: true},
				{ChainID: constants.Ethereum, Symbol: "WETH", Name: "Wrapped Ether", Address: "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2", Decimals: 18, Enabled: true},
				{ChainID: constants.Base, Native: true, Name: "Base Ether", Enabled: true},
				{ChainID: constants.Base, Symbol: "WETH", Name: "Wrapped Ether", Address: "0x4200000000000000000000000000000000000006", Decimals: 18, Enabled: true},
				{ChainID: constants.Arbitrum, Native: true, Name: "Arbitrum Ether", Enabled: true},
				{ChainID: constants.Arbitrum, Symbol: "WETH", Name: "Wrapped Ether", Address: "0x82aF49447D8a07e3bd95BDd56f35241523fBab1", Decimals: 18, Enabled: true},
				{ChainID: constants.Unichain, Native: true, Name: "Unichain Ether", Enabled: true},
				{ChainID: constants.Unichain, Symbol: "WETH", Name: "Wrapped Ether", Address: "0x4200000000000000000000000000000000000006", Decimals: 18, Enabled: true},
			},
		},
		{
			Symbol:   "USDT",
			Name:     "Tether USD",
			Type:     asset.AssetERC20,
			Decimals: 6,
			LogoSlug: "usdt",
			Deployments: []asset.Deployment{
				{ChainID: constants.Ethereum, Address: "0xdAC17F958D2ee523a2206206994597C13D831ec7", Decimals: 6, Enabled: true},
				{ChainID: constants.Solana, Mint: "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB", Decimals: 6, Enabled: true},
				{ChainID: constants.TRON, Address: "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t", Decimals: 6, Enabled: true},
			},
		},
		{
			Symbol:   "USDC",
			Name:     "USD Coin",
			Type:     asset.AssetERC20,
			Decimals: 6,
			LogoSlug: "usdc",
			Deployments: []asset.Deployment{
				{ChainID: constants.Ethereum, Address: "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", Decimals: 6, Enabled: true},
				{ChainID: constants.Base, Address: "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913", Decimals: 6, Enabled: true},
				{ChainID: constants.Arbitrum, Address: "0xaf88d065e77c8cC2239327C5EDb3A432268e5831", Decimals: 6, Enabled: true},
				{ChainID: constants.Avalanche, Address: "0xB97EF9Ef8734C71904D8002F8b6Bc66Dd9c48a6E", Decimals: 6, Enabled: true},
				{ChainID: constants.Chiliz, Address: "0xa37936F56249965d407E39347528a1A91eB1cbef", Decimals: 6, Enabled: true},
				{ChainID: constants.Solana, Mint: "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v", Decimals: 6, Enabled: true},
			},
		},
		{
			Symbol:   "SOL",
			Name:     "Solana",
			Type:     asset.AssetNative,
			Decimals: 9,
			LogoSlug: "sol",
			Deployments: []asset.Deployment{
				{ChainID: constants.Solana, Native: true, Enabled: true},
				{ChainID: constants.Solana, Symbol: "WSOL", Name: "Wrapped Solana", Mint: "So11111111111111111111111111111111111111112", Decimals: 9, Enabled: true},
			},
		},
		{
			Symbol:   "TRX",
			Name:     "Tron",
			Type:     asset.AssetNative,
			Decimals: 6,
			LogoSlug: "trx",
			Deployments: []asset.Deployment{
				{ChainID: constants.TRON, Native: true, Enabled: true},
			},
		},
		{
			Symbol:   "AVAX",
			Name:     "Avalanche",
			Type:     asset.AssetNative,
			Decimals: 18,
			LogoSlug: "avax",
			Deployments: []asset.Deployment{
				{ChainID: constants.Avalanche, Native: true, Enabled: true},
				{ChainID: constants.Avalanche, Symbol: "WAVAX", Name: "Wrapped AVAX", Address: "0xB31f66AA3C1e785363F0875A1B74E27b85FD66c7", Decimals: 18, Enabled: true},
			},
		},
		{
			Symbol:   "BNB",
			Name:     "Binance Coin",
			Type:     asset.AssetNative,
			Decimals: 18,
			LogoSlug: "bnb",
			Deployments: []asset.Deployment{
				{ChainID: constants.Binance, Native: true, Enabled: true},
				{ChainID: constants.Binance, Symbol: "WBNB", Name: "Wrapped BNB", Address: "0xbb4CdB9CBd36B01bD1cBaEBF2De08d9173bc095c", Decimals: 18, Enabled: true},
			},
		},
		{
			Symbol:   "CHZ",
			Name:     "Chiliz",
			Type:     asset.AssetNative,
			Decimals: 18,
			LogoSlug: "chz",
			Deployments: []asset.Deployment{
				{ChainID: constants.Chiliz, Native: true, Enabled: true},
				{ChainID: constants.ChilizSpicy, Native: true, Name: "Chiliz Spicy (Testnet)", Enabled: true},
				{ChainID: constants.Solana, Mint: "6eftxVbSAunVEoxUWdGhPdxg5UdsJ8Wkwy5w5YFuxouw", Decimals: 8, Enabled: true},
				{ChainID: constants.Base, Address: "0x70c8392DE9b39a1E48d12A70Af6FF4Be25D6D0A2", Decimals: 18, Enabled: true},
				{ChainID: constants.Chiliz, Symbol: "WCHZ", Name: "Wrapped Chiliz", Address: "0x677f7e16c7dd57be1d4c8ad1244883214953dc47", Decimals: 18, Enabled: true},
				{ChainID: constants.Ethereum, Symbol: "CHZ", Name: "Chiliz", Address: "0x3506424F91fD33084466F402d5D97f05F8e3b4AF", Decimals: 18, Enabled: true},
			},
		},
		{
			Symbol:   "TBT",
			Name:     "TBT Token",
			Type:     asset.AssetERC20,
			Decimals: 18,
			LogoSlug: "tbt",
			Deployments: []asset.Deployment{
				{ChainID: constants.Chiliz, Address: "0xfed7A6423cdeBb4c05552DC888de5acC657444F4", Decimals: 18, Enabled: true},
			},
		},
		{
			Symbol:   "CHZINU",
			Name:     "ChilizINU",
			Type:     asset.AssetERC20,
			Decimals: 18,
			LogoSlug: "chzinu",
			Deployments: []asset.Deployment{
				{ChainID: constants.Chiliz, Address: "0xF3928e7871eb136DD6648Ad08aEEF6B6ea893001", Decimals: 4, Enabled: true},
			},
		},
		{
			Symbol:   "PEPPER",
			Name:     "PEPPER",
			Type:     asset.AssetERC20,
			Decimals: 18,
			LogoSlug: "pepper",
			Deployments: []asset.Deployment{
				{ChainID: constants.Solana, Mint: "GozPNCAseytzxCR3d2k8hTsTYkr4SDpuXy2RQAZFVx2g", Decimals: 3, Enabled: true},
				{ChainID: constants.Base, Address: "0x5e985E4BCa4664E985f3FaF8140EbA25b10E28C2", Decimals: 18, Enabled: true},
				{ChainID: constants.Chiliz, Address: "0x60f397acbcfb8f4e3234c659a3e10867e6fa6b67", Decimals: 18, Enabled: true},
			},
		},

		{
			Symbol:   "LGBT",
			Name:     "COOLVIBES",
			Type:     asset.AssetSPL,
			Decimals: 6,
			LogoSlug: "lgbt",
			Deployments: []asset.Deployment{
				{ChainID: constants.Solana, Mint: "9qpnNkj8wqecEhnrKwzhNAtzSknizFqDEzxPd1Ajpump", Decimals: 6, Enabled: true},
			},
		},
	}
}
