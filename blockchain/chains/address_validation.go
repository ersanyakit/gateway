package chains

import (
	"core/blockchain/walletcore"
	"core/constants"
)

func validateAddressWithWalletCore(chainID constants.ChainID, address string) bool {
	return walletcore.ValidateAddress(address, chainID)
}
