package listeners

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"core/blockchain"
	"core/models"
)

const (
	StartPolicyRequire          = "require"
	StartPolicyTail             = "tail"
	StartPolicyGenesis          = "genesis"
	ContinuityStatusOK          = "ok"
	ContinuityStatusRollback    = "rollback_required"
	ContinuityStatusHistoryTail = "history_skipped"
)

var (
	ErrStartBlockRequired = errors.New("scanner start block is required for empty chain state")
	ErrParentContinuity   = errors.New("scanner parent hash continuity failed")
)

type StartBlockDecision struct {
	From           int64
	Configured     bool
	Policy         string
	HistorySkipped bool
	Warning        string
}

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

func ResolveStartBlock(chain blockchain.Chain, lastProcessed int64, safeLatest int64) (StartBlockDecision, error) {
	if lastProcessed > 0 {
		return StartBlockDecision{
			From:   lastProcessed + 1,
			Policy: StartPolicyRequire,
		}, nil
	}
	if configured, ok := ConfiguredStartBlock(chain); ok {
		return StartBlockDecision{
			From:       configured,
			Configured: true,
			Policy:     StartPolicyRequire,
		}, nil
	}
	policy := ScannerStartPolicy()
	switch policy {
	case StartPolicyTail:
		return StartBlockDecision{
			From:           safeLatest,
			Policy:         StartPolicyTail,
			HistorySkipped: true,
			Warning:        "empty chain state without configured start block; scanner tail policy skips unknown history",
		}, nil
	case StartPolicyGenesis:
		return StartBlockDecision{
			From:    1,
			Policy:  StartPolicyGenesis,
			Warning: "empty chain state without configured start block; scanner genesis policy starts at block 1",
		}, nil
	default:
		return StartBlockDecision{Policy: StartPolicyRequire}, ErrStartBlockRequired
	}
}

func ApplyStartBlockDecision(state *models.ChainState, decision StartBlockDecision) {
	if state == nil || decision.From <= 0 {
		return
	}
	if state.ScannerStartBlock == 0 {
		state.ScannerStartBlock = decision.From
	}
	if strings.TrimSpace(state.ScannerStartPolicy) == "" {
		state.ScannerStartPolicy = decision.Policy
	}
	if decision.HistorySkipped {
		state.ContinuityStatus = ContinuityStatusHistoryTail
		state.ContinuityReason = decision.Warning
	}
}

func ValidateParentContinuity(state *models.ChainState, nextBlockNumber int64, nextParentHash string) error {
	if state == nil || state.LastProcessedBlock <= 0 || nextBlockNumber != state.LastProcessedBlock+1 {
		return nil
	}
	checkpointHash := strings.TrimSpace(state.LastProcessedHash)
	nextParentHash = strings.TrimSpace(nextParentHash)
	if checkpointHash == "" || nextParentHash == "" {
		return nil
	}
	if !strings.EqualFold(checkpointHash, nextParentHash) {
		state.ContinuityStatus = ContinuityStatusRollback
		state.ContinuityReason = fmt.Sprintf("block %d parent %s does not match checkpoint %s", nextBlockNumber, nextParentHash, checkpointHash)
		return fmt.Errorf("%w: %s", ErrParentContinuity, state.ContinuityReason)
	}
	state.ContinuityStatus = ContinuityStatusOK
	state.ContinuityReason = ""
	return nil
}

func RewindParentContinuityCheckpoint(state *models.ChainState, nextBlockNumber int64) {
	if state == nil || nextBlockNumber <= 0 {
		return
	}
	rewindTo := nextBlockNumber - 2
	if rewindTo < 0 {
		rewindTo = 0
	}
	state.LastProcessedBlock = rewindTo
	state.LastProcessedHash = ""
	state.LastProcessedParentHash = ""
}

func RecordProcessedBlockCheckpoint(state *models.ChainState, blockNumber int64, hash string, parentHash string) {
	if state == nil {
		return
	}
	state.LastProcessedBlock = blockNumber
	state.LastProcessedHash = strings.TrimSpace(hash)
	state.LastProcessedParentHash = strings.TrimSpace(parentHash)
	if state.ContinuityStatus != ContinuityStatusHistoryTail {
		state.ContinuityStatus = ContinuityStatusOK
		state.ContinuityReason = ""
	}
}

func ScannerStartPolicy() string {
	for _, key := range []string{"SCANNER_START_POLICY", "CHAIN_START_POLICY"} {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
		case StartPolicyTail:
			return StartPolicyTail
		case StartPolicyGenesis:
			return StartPolicyGenesis
		case StartPolicyRequire:
			return StartPolicyRequire
		}
	}
	return StartPolicyRequire
}
