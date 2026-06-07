package asset

import (
	"core/constants"
	"strings"
)

type AssetDefinition struct {
	Symbol      string
	Name        string
	Type        AssetType
	Decimals    uint8
	LogoSlug    string
	Deployments []Deployment
}

type Deployment struct {
	ChainID  constants.ChainID
	Symbol   string
	Name     string
	Address  string
	Mint     string
	Decimals uint8
	Native   bool
	Enabled  bool
	Disabled bool
	Type     AssetType
}

type DeploymentAsset struct {
	BaseAsset
	Identifier string
	Address    string
	Mint       string
}

func NewDeploymentAsset(def AssetDefinition, d Deployment) *DeploymentAsset {
	symbol := strings.TrimSpace(d.Symbol)
	if symbol == "" {
		symbol = strings.TrimSpace(def.Symbol)
	}
	name := strings.TrimSpace(d.Name)
	if name == "" {
		name = strings.TrimSpace(def.Name)
	}
	decimals := d.Decimals
	if decimals == 0 {
		decimals = def.Decimals
	}

	native := d.Native
	assetType := d.Type
	if assetType == AssetNative && !native {
		assetType = inferAssetType(d.ChainID)
	}
	if native {
		assetType = AssetNative
	}

	return &DeploymentAsset{
		BaseAsset: BaseAsset{
			ChainID:   d.ChainID,
			ChainType: inferChainType(d.ChainID),
			Type:      assetType,
			Symbol:    symbol,
			Name:      name,
			Decimals:  decimals,
			Native:    native,
		},
		Identifier: DeploymentIdentifier(def, d),
		Address:    strings.TrimSpace(d.Address),
		Mint:       strings.TrimSpace(d.Mint),
	}
}

func (d *DeploymentAsset) GetIdentifier() string {
	if d.Native {
		return d.Symbol
	}
	return d.Identifier
}

func (d Deployment) IsEnabled() bool {
	return d.Enabled || !d.Disabled
}

func DeploymentIdentifier(def AssetDefinition, d Deployment) string {
	symbol := strings.TrimSpace(d.Symbol)
	if symbol == "" {
		symbol = strings.TrimSpace(def.Symbol)
	}
	if d.Native {
		return symbol
	}
	if d.ChainID == constants.Solana {
		if mint := strings.TrimSpace(d.Mint); mint != "" {
			return mint
		}
	}
	if address := strings.TrimSpace(d.Address); address != "" {
		return address
	}
	if mint := strings.TrimSpace(d.Mint); mint != "" {
		return mint
	}
	return symbol
}

func TokenAddress(a Asset) string {
	if a == nil || a.IsNative() {
		return ""
	}
	switch v := a.(type) {
	case *DeploymentAsset:
		if v.GetChainType() == ChainSolana {
			return ""
		}
		return v.Address
	case *EVMAsset:
		return v.ContractAddress
	case *TronAsset:
		return v.ContractAddress
	default:
		return ""
	}
}

func MintAddress(a Asset) string {
	if a == nil || a.IsNative() {
		return ""
	}
	switch v := a.(type) {
	case *DeploymentAsset:
		if v.GetChainType() == ChainSolana {
			if v.Mint != "" {
				return v.Mint
			}
			return v.Identifier
		}
	case *SolanaAsset:
		return v.MintAddress
	}
	return ""
}

func AssetTypeName(t AssetType) string {
	switch t {
	case AssetNative:
		return "native"
	case AssetERC20:
		return "erc20"
	case AssetTRC20:
		return "trc20"
	case AssetSPL:
		return "spl"
	case AssetUTXO:
		return "utxo"
	default:
		return "unknown"
	}
}

func inferChainType(chainID constants.ChainID) ChainType {
	switch chainID {
	case constants.Solana:
		return ChainSolana
	case constants.TRON:
		return ChainTron
	case constants.Bitcoin:
		return ChainBitcoin
	default:
		return ChainEVM
	}
}

func inferAssetType(chainID constants.ChainID) AssetType {
	switch inferChainType(chainID) {
	case ChainSolana:
		return AssetSPL
	case ChainTron:
		return AssetTRC20
	case ChainBitcoin:
		return AssetUTXO
	default:
		return AssetERC20
	}
}
