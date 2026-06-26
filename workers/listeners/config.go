package listeners

import (
	"os"
	"strconv"
	"strings"

	"core/blockchain"
)

func ConfiguredStartBlock(chain blockchain.Chain) (int64, bool) {
	if chain == nil {
		return 0, false
	}
	keys := []string{
		"CHAIN_" + strconv.FormatInt(int64(chain.ChainID()), 10) + "_START_BLOCK",
	}
	if name := strings.TrimSpace(chain.Name()); name != "" {
		envName := strings.ToUpper(strings.NewReplacer("-", "_").Replace(name))
		keys = append(keys, envName+"_START_BLOCK", "START_BLOCK_"+envName)
	}
	keys = append(keys, "CHAIN_START_BLOCK_DEFAULT")

	for _, key := range keys {
		raw := strings.TrimSpace(os.Getenv(key))
		if raw == "" {
			continue
		}
		value, err := strconv.ParseInt(raw, 10, 64)
		if err == nil && value > 0 {
			return value, true
		}
	}
	return 0, false
}
