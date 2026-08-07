package listeners

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"core/blockchain"
	"core/constants"
	"core/models"
)

type configTestChain struct {
	id   constants.ChainID
	name string
}

func (c configTestChain) ChainID() constants.ChainID { return c.id }
func (c configTestChain) Name() string               { return c.name }
func (c configTestChain) WSS() []string              { return nil }
func (c configTestChain) RPCs() []string             { return nil }
func (c configTestChain) Create(context.Context) (*blockchain.WalletDetails, error) {
	return nil, errors.New("not used")
}
func (c configTestChain) CreateHDWallet(context.Context, int, int) (*blockchain.WalletDetails, error) {
	return nil, errors.New("not used")
}
func (c configTestChain) Deposit(context.Context, blockchain.WalletDetails, string, string) (*blockchain.TransactionResult, error) {
	return nil, errors.New("not used")
}
func (c configTestChain) Withdraw(context.Context, blockchain.WalletDetails, string, string) (*blockchain.TransactionResult, error) {
	return nil, errors.New("not used")
}
func (c configTestChain) WithdrawToken(context.Context, blockchain.WalletDetails, string, string, string) (*blockchain.TransactionResult, error) {
	return nil, errors.New("not used")
}
func (c configTestChain) Sweep(context.Context, blockchain.WalletDetails) (*blockchain.TransactionResult, error) {
	return nil, errors.New("not used")
}
func (c configTestChain) SweepTo(context.Context, blockchain.WalletDetails, string) (*blockchain.TransactionResult, error) {
	return nil, errors.New("not used")
}
func (c configTestChain) SweepERC20To(context.Context, blockchain.WalletDetails, string, string) (*blockchain.TransactionResult, error) {
	return nil, errors.New("not used")
}
func (c configTestChain) PrefundGas(context.Context, blockchain.WalletDetails, string) (bool, error) {
	return false, errors.New("not used")
}
func (c configTestChain) ValidateAddress(string) bool { return false }
func (c configTestChain) AddWorker(blockchain.Worker) error {
	return errors.New("not used")
}
func (c configTestChain) RemoveWorker(blockchain.Worker) error {
	return errors.New("not used")
}
func (c configTestChain) WorkerCount() int { return 0 }
func (c configTestChain) BatchBalances(context.Context, []string, int) []models.BalanceResult {
	return nil
}
func (c configTestChain) StartWorkers(context.Context) error { return errors.New("not used") }
func (c configTestChain) StopWorkers() error                 { return errors.New("not used") }

func TestConfiguredStartBlockUsesChainSpecificIDFirst(t *testing.T) {
	t.Setenv("CHAIN_1_START_BLOCK", "123")
	t.Setenv("ETHEREUM_START_BLOCK", "456")

	got, ok := ConfiguredStartBlock(configTestChain{id: constants.Ethereum, name: "ethereum"})
	if !ok {
		t.Fatal("ConfiguredStartBlock should find chain id env")
	}
	if got != 123 {
		t.Fatalf("ConfiguredStartBlock = %d, want 123", got)
	}
}

func TestConfiguredStartBlockUsesNormalizedChainName(t *testing.T) {
	t.Setenv("CHILIZ_SPICY_START_BLOCK", "77")

	got, ok := ConfiguredStartBlock(configTestChain{id: constants.ChilizSpicy, name: "chiliz-spicy"})
	if !ok {
		t.Fatal("ConfiguredStartBlock should find normalized chain name env")
	}
	if got != 77 {
		t.Fatalf("ConfiguredStartBlock = %d, want 77", got)
	}
}

func TestConfiguredStartBlockIgnoresZero(t *testing.T) {
	t.Setenv("CHAIN_START_BLOCK_DEFAULT", "0")

	if got, ok := ConfiguredStartBlock(configTestChain{id: constants.Bitcoin, name: "bitcoin"}); ok {
		t.Fatalf("ConfiguredStartBlock ok with value %d, want false", got)
	}
}

func TestResolveStartBlockRequiresExplicitPolicyForEmptyState(t *testing.T) {
	t.Setenv("SCANNER_START_POLICY", "")
	_, err := ResolveStartBlock(configTestChain{id: constants.Ethereum, name: "ethereum"}, 0, 100)
	if !errors.Is(err, ErrStartBlockRequired) {
		t.Fatalf("ResolveStartBlock error = %v, want ErrStartBlockRequired", err)
	}
}

func TestResolveStartBlockAllowsExplicitTailPolicy(t *testing.T) {
	t.Setenv("SCANNER_START_POLICY", "tail")
	decision, err := ResolveStartBlock(configTestChain{id: constants.Ethereum, name: "ethereum"}, 0, 100)
	if err != nil {
		t.Fatalf("ResolveStartBlock tail policy: %v", err)
	}
	if decision.From != 100 || decision.Policy != StartPolicyTail || !decision.HistorySkipped {
		t.Fatalf("decision = %+v, want tail from safe latest with history skipped", decision)
	}
}

func TestResolveStartBlockUsesConfiguredStartBlock(t *testing.T) {
	t.Setenv("CHAIN_1_START_BLOCK", "42")
	decision, err := ResolveStartBlock(configTestChain{id: constants.Ethereum, name: "ethereum"}, 0, 100)
	if err != nil {
		t.Fatalf("ResolveStartBlock configured start: %v", err)
	}
	if decision.From != 42 || !decision.Configured {
		t.Fatalf("decision = %+v, want configured from 42", decision)
	}
}

func TestApplyStartBlockDecisionPersistsPolicyEvidence(t *testing.T) {
	state := &models.ChainState{}
	ApplyStartBlockDecision(state, StartBlockDecision{
		From:           100,
		Policy:         StartPolicyTail,
		HistorySkipped: true,
		Warning:        "history skipped",
	})

	if state.ScannerStartBlock != 100 || state.ScannerStartPolicy != StartPolicyTail {
		t.Fatalf("state start policy = %d/%q, want 100/%q", state.ScannerStartBlock, state.ScannerStartPolicy, StartPolicyTail)
	}
	if state.ContinuityStatus != ContinuityStatusHistoryTail || !strings.Contains(state.ContinuityReason, "history skipped") {
		t.Fatalf("state continuity evidence = %q/%q, want history skip evidence", state.ContinuityStatus, state.ContinuityReason)
	}
}

func TestValidateParentContinuityDetectsMismatch(t *testing.T) {
	state := &models.ChainState{LastProcessedBlock: 10, LastProcessedHash: "0xabc"}

	err := ValidateParentContinuity(state, 11, "0xdef")
	if !errors.Is(err, ErrParentContinuity) {
		t.Fatalf("ValidateParentContinuity error = %v, want ErrParentContinuity", err)
	}
	if state.ContinuityStatus != ContinuityStatusRollback {
		t.Fatalf("continuity status = %q, want %q", state.ContinuityStatus, ContinuityStatusRollback)
	}
	if !strings.Contains(state.ContinuityReason, "0xabc") || !strings.Contains(state.ContinuityReason, "0xdef") {
		t.Fatalf("continuity reason = %q, want mismatch evidence", state.ContinuityReason)
	}
}

func TestRewindParentContinuityCheckpointClearsStaleHash(t *testing.T) {
	t.Setenv("SCANNER_REORG_REWIND_BLOCKS", "1")
	state := &models.ChainState{
		LastProcessedBlock:      10,
		LastProcessedHash:       "0xstale",
		LastProcessedParentHash: "0xolder",
		ContinuityStatus:        ContinuityStatusRollback,
		ContinuityReason:        "parent mismatch",
	}

	RewindParentContinuityCheckpoint(state, 11)

	if state.LastProcessedBlock != 9 {
		t.Fatalf("last processed block = %d, want 9", state.LastProcessedBlock)
	}
	if state.LastProcessedHash != "" || state.LastProcessedParentHash != "" {
		t.Fatalf("checkpoint hashes = %q/%q, want cleared", state.LastProcessedHash, state.LastProcessedParentHash)
	}
	if state.ContinuityStatus != ContinuityStatusRollback || state.ContinuityReason == "" {
		t.Fatalf("rollback evidence lost: %+v", state)
	}
}

func TestRewindParentContinuityCheckpointUsesChainSpecificReplayWindow(t *testing.T) {
	t.Setenv("SCANNER_REORG_REWIND_BLOCKS", "4")
	t.Setenv("CHAIN_1_REORG_REWIND_BLOCKS", "12")
	state := &models.ChainState{
		ChainID:            constants.Ethereum,
		LastProcessedBlock: 100,
		LastProcessedHash:  "0xstale",
		ContinuityStatus:   ContinuityStatusRollback,
		ContinuityReason:   "parent mismatch",
	}

	RewindParentContinuityCheckpoint(state, 101)

	if state.LastProcessedBlock != 88 {
		t.Fatalf("last processed block = %d, want 88", state.LastProcessedBlock)
	}
	if !strings.Contains(state.ContinuityReason, "rewound 12 blocks") {
		t.Fatalf("continuity reason = %q", state.ContinuityReason)
	}
}

func TestRecordProcessedBlockCheckpointStoresHashEvidence(t *testing.T) {
	state := &models.ChainState{}

	RecordProcessedBlockCheckpoint(state, 12, "0xhash", "0xparent")
	if state.LastProcessedBlock != 12 || state.LastProcessedHash != "0xhash" || state.LastProcessedParentHash != "0xparent" {
		t.Fatalf("checkpoint = %+v, want block/hash/parent", state)
	}
	if state.ContinuityStatus != ContinuityStatusOK {
		t.Fatalf("continuity status = %q, want %q", state.ContinuityStatus, ContinuityStatusOK)
	}
}

func TestSupportedListenersRespectConfiguredStartBlock(t *testing.T) {
	for _, path := range []string{
		"evm/listener.go",
		"bitcoin/bitcoin.go",
		"solana/listener.go",
		"tron/tron.go",
	} {
		t.Run(path, func(t *testing.T) {
			sourceBytes, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			source := string(sourceBytes)
			for _, token := range []string{
				"ResolveStartBlock(r.chain",
				"decision.From",
				"ApplyStartBlockDecision(r.chainState, decision)",
			} {
				if !strings.Contains(source, token) {
					t.Fatalf("%s does not preserve configured start block behavior %q", path, token)
				}
			}
		})
	}
}
