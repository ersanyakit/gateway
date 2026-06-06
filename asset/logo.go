package asset

import (
	"core/constants"
	"strings"
)

// CoinLogoURL returns the /static/coins URL for a given coin symbol.
// Returns empty string for unknown symbols so templates can conditionally render.
func CoinLogoURL(symbol string) string {
	name := logoFilename(strings.ToUpper(strings.TrimSpace(symbol)))
	if name == "" {
		return ""
	}
	return "/static/coins/" + name + ".svg"
}

// ChainLogoURL returns the /static/chains URL for a given chain ID.
// Returns empty string for chains without an icon.
func ChainLogoURL(chainID constants.ChainID) string {
	name := chainLogoFilename(chainID)
	if name == "" {
		return ""
	}
	return "/static/chains/" + name + ".svg"
}

func chainLogoFilename(chainID constants.ChainID) string {
	switch chainID {
	case constants.Bitcoin:
		return "bitcoinchain"
	case constants.Base:
		return "basechain"
	case constants.Arbitrum:
		return "arbitrumchain"
	case constants.Ethereum:
		return "ethereumchain"
	case constants.Binance:
		return "bnbchain"
	case constants.Unichain:
		return "unichain"
	case constants.Avalanche:
		return "avalanchechain"
	case constants.Solana:
		return "solanachain"
	case constants.TRON:
		return "tronchain"
	case constants.Chiliz, constants.ChilizSpicy:
		return "chilizchain"
	default:
		return ""
	}
}

func logoFilename(sym string) string {
	switch sym {
	case "ETH":
		return "eth"
	case "BTC":
		return "btc"
	case "USDT":
		return "usdt"
	case "USDC":
		return "usdc"
	case "BNB":
		return "bnb"
	case "AVAX":
		return "avax"
	case "SOL":
		return "sol"
	case "TRX":
		return "trx"
	case "MATIC", "POL":
		return "matic"
	case "CHZ":
		return "chz"
	default:
		return ""
	}
}
