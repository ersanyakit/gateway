package global

import (
	"core/asset"
	"core/blockchain"
	"sync"
)

type Global struct {
	Registry *asset.Registry
	Factory  *blockchain.ChainFactory
}

var (
	instance *Global
	once     sync.Once
)

func Get() *Global {
	once.Do(func() {
		instance = &Global{
			Registry: newAssetRegistry(),
			Factory:  newChainFactory(),
		}
	})
	return instance
}
