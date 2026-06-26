package blockchain

import (
	"testing"

	"core/constants"
)

func TestBaseChainRPCsMergesEnvAndDefaults(t *testing.T) {
	t.Setenv("ETHEREUM_RPC_URLS", "https://env-1.example, https://env-2.example")
	t.Setenv("CHAIN_1_RPC_URLS", "https://env-2.example,https://chain-id.example")
	chain := BaseChain{
		ID:        constants.Ethereum,
		ChainName: "ethereum",
		RPCHttp:   []string{"https://default.example", "https://env-1.example"},
	}

	got := chain.RPCs()
	want := []string{"https://env-1.example", "https://env-2.example", "https://chain-id.example", "https://default.example"}
	if len(got) != len(want) {
		t.Fatalf("RPCs len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("RPCs[%d] = %q, want %q; all=%#v", i, got[i], want[i], got)
		}
	}
}

func TestBaseChainDerivedPath(t *testing.T) {
	chain := BaseChain{}
	if got := chain.GetDerivedPath(44, 60, 1, 0, 7); got != "m/44'/60'/1'/0/7" {
		t.Fatalf("derived path = %q", got)
	}
}

func TestBaseChainWorkerCount(t *testing.T) {
	chain := BaseChain{}
	worker := &baseChainTestWorker{events: make(chan interface{})}

	if got := chain.WorkerCount(); got != 0 {
		t.Fatalf("initial worker count = %d, want 0", got)
	}
	if err := chain.AddWorker(worker); err != nil {
		t.Fatal(err)
	}
	if got := chain.WorkerCount(); got != 1 {
		t.Fatalf("worker count after add = %d, want 1", got)
	}
	if err := chain.RemoveWorker(worker); err != nil {
		t.Fatal(err)
	}
	if got := chain.WorkerCount(); got != 0 {
		t.Fatalf("worker count after remove = %d, want 0", got)
	}
	if err := chain.RemoveWorker(worker); err == nil {
		t.Fatal("expected missing listener error")
	}
}

type baseChainTestWorker struct {
	events chan interface{}
}

func (w *baseChainTestWorker) Start() error { return nil }

func (w *baseChainTestWorker) Stop() error { return nil }

func (w *baseChainTestWorker) Events() <-chan interface{} { return w.events }
