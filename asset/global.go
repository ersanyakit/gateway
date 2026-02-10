package asset

import "sync"

var (
	globalRegistry *Registry
	once           sync.Once
)

func Global() *Registry {
	once.Do(func() {
		globalRegistry = NewRegistry()
	})
	return globalRegistry
}
