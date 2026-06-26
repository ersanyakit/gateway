package helpers

import "math/big"

func HexToETH(hexStr string) string {
	value := new(big.Int)
	value.SetString(hexStr[2:], 16) // 0x kaldır

	ethValue := new(big.Float).SetInt(value)
	ethValue = new(big.Float).Quo(ethValue, big.NewFloat(1e18))

	return ethValue.Text('f', 18) // 18 decimal
}
