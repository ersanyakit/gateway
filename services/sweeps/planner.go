package sweeps

import (
	"errors"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
)

const NativeAsset = "native"

var ErrInvalidSweepAmount = errors.New("invalid sweep amount")

type ChainCapability struct {
	ChainID            constants.ChainID
	ChainName          string
	SupportsBatch      bool
	SupportsGasFunding bool
	MaxBatchSize       int
}

type Policy struct {
	Key             string
	MinimumRaw      string
	DustRaw         string
	MaxFeeRaw       string
	MinNativeGasRaw string
	MaxBatchSize    int
}

type Candidate struct {
	Job                 models.SweepJob
	AmountRaw           string
	FeeEstimateRaw      string
	NativeGasBalanceRaw string
	ReserveWalletID     uuid.UUID
	SourcePolicyKey     string
	RequiresNativeGas   bool
}

type Exclusion struct {
	JobID  uuid.UUID
	Reason string
}

type Plan struct {
	BatchID            uuid.UUID
	BatchKey           string
	ChainID            constants.ChainID
	Asset              string
	MerchantID         uuid.UUID
	ReserveWalletID    uuid.UUID
	PolicyKey          string
	SupportsBatch      bool
	RequiresGasFunding bool
	JobIDs             []uuid.UUID
	TotalAmountRaw     string
	Exclusions         []Exclusion
}

type Planner struct {
	capabilities map[constants.ChainID]ChainCapability
}

func NewPlanner(capabilities map[constants.ChainID]ChainCapability) Planner {
	copied := make(map[constants.ChainID]ChainCapability, len(capabilities))
	for chainID, capability := range capabilities {
		copied[chainID] = normalizeCapability(chainID, capability)
	}
	return Planner{capabilities: copied}
}

func DefaultCapabilities() map[constants.ChainID]ChainCapability {
	out := make(map[constants.ChainID]ChainCapability)
	for _, chainID := range constants.AllChainIDs() {
		out[chainID] = DefaultCapability(chainID)
	}
	return out
}

func DefaultCapability(chainID constants.ChainID) ChainCapability {
	capability := ChainCapability{
		ChainID:            chainID,
		ChainName:          constants.ChainName(chainID),
		SupportsGasFunding: chainID != constants.Bitcoin,
		MaxBatchSize:       1,
	}
	if chainID == constants.Bitcoin {
		capability.SupportsBatch = true
		capability.SupportsGasFunding = false
		capability.MaxBatchSize = 50
	}
	return capability
}

func (p Planner) Plan(candidates []Candidate, policy Policy) ([]Plan, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	policy = normalizePolicy(policy)
	groups := make(map[string][]Candidate)
	exclusions := make(map[string][]Exclusion)
	for _, candidate := range candidates {
		if candidate.Job.ID == uuid.Nil {
			continue
		}
		reason, err := exclusionReason(candidate, policy)
		if err != nil {
			return nil, err
		}
		key := candidateGroupKey(candidate, policy.Key)
		if reason != "" {
			exclusions[key] = append(exclusions[key], Exclusion{JobID: candidate.Job.ID, Reason: reason})
			continue
		}
		groups[key] = append(groups[key], candidate)
	}

	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	plans := make([]Plan, 0, len(keys))
	for _, key := range keys {
		group := groups[key]
		sort.SliceStable(group, func(i, j int) bool {
			return group[i].Job.CreatedAt.Before(group[j].Job.CreatedAt)
		})
		capability := p.capabilityFor(group[0].Job.ChainID)
		maxBatchSize := effectiveMaxBatchSize(capability, policy)
		for start := 0; start < len(group); start += maxBatchSize {
			end := start + maxBatchSize
			if end > len(group) {
				end = len(group)
			}
			plan, err := buildPlan(group[start:end], policy.Key, capability)
			if err != nil {
				return nil, err
			}
			if start == 0 {
				plan.Exclusions = exclusions[key]
			}
			plans = append(plans, plan)
		}
	}
	for key, excluded := range exclusions {
		if _, ok := groups[key]; ok {
			continue
		}
		plans = append(plans, Plan{BatchID: uuid.New(), BatchKey: key, PolicyKey: policy.Key, Exclusions: excluded})
	}
	sort.SliceStable(plans, func(i, j int) bool {
		return plans[i].BatchKey < plans[j].BatchKey
	})
	return plans, nil
}

func (p Planner) capabilityFor(chainID constants.ChainID) ChainCapability {
	if p.capabilities != nil {
		if capability, ok := p.capabilities[chainID]; ok {
			return normalizeCapability(chainID, capability)
		}
	}
	return DefaultCapability(chainID)
}

func normalizeCapability(chainID constants.ChainID, capability ChainCapability) ChainCapability {
	if capability.ChainID == 0 && chainID != 0 {
		capability.ChainID = chainID
	}
	if strings.TrimSpace(capability.ChainName) == "" {
		capability.ChainName = constants.ChainName(capability.ChainID)
	}
	if capability.MaxBatchSize <= 0 {
		capability.MaxBatchSize = 1
	}
	if !capability.SupportsBatch {
		capability.MaxBatchSize = 1
	}
	return capability
}

func normalizePolicy(policy Policy) Policy {
	if strings.TrimSpace(policy.Key) == "" {
		policy.Key = "default"
	}
	return policy
}

func effectiveMaxBatchSize(capability ChainCapability, policy Policy) int {
	maxBatchSize := capability.MaxBatchSize
	if policy.MaxBatchSize > 0 && policy.MaxBatchSize < maxBatchSize {
		maxBatchSize = policy.MaxBatchSize
	}
	if maxBatchSize <= 0 {
		return 1
	}
	return maxBatchSize
}

func buildPlan(candidates []Candidate, policyKey string, capability ChainCapability) (Plan, error) {
	total := big.NewInt(0)
	jobIDs := make([]uuid.UUID, 0, len(candidates))
	requiresGasFunding := false
	for _, candidate := range candidates {
		amount, err := parseRawAmount(candidate.AmountRaw)
		if err != nil {
			return Plan{}, err
		}
		total.Add(total, amount)
		jobIDs = append(jobIDs, candidate.Job.ID)
		if candidate.RequiresNativeGas && capability.SupportsGasFunding {
			requiresGasFunding = true
		}
	}
	first := candidates[0]
	batchKey := candidateGroupKey(first, policyKey)
	return Plan{
		BatchID:            uuid.New(),
		BatchKey:           batchKey,
		ChainID:            first.Job.ChainID,
		Asset:              assetKey(first.Job),
		MerchantID:         first.Job.MerchantID,
		ReserveWalletID:    first.ReserveWalletID,
		PolicyKey:          policyKey,
		SupportsBatch:      capability.SupportsBatch && len(candidates) > 1,
		RequiresGasFunding: requiresGasFunding,
		JobIDs:             jobIDs,
		TotalAmountRaw:     total.String(),
	}, nil
}

func exclusionReason(candidate Candidate, policy Policy) (string, error) {
	amount, err := parseRawAmount(candidate.AmountRaw)
	if err != nil {
		return "", err
	}
	if threshold, ok, err := optionalRawAmount(policy.DustRaw); err != nil {
		return "", err
	} else if ok && amount.Cmp(threshold) <= 0 {
		return "dust", nil
	}
	if minimum, ok, err := optionalRawAmount(policy.MinimumRaw); err != nil {
		return "", err
	} else if ok && amount.Cmp(minimum) < 0 {
		return "below_minimum_balance", nil
	}
	if maxFee, ok, err := optionalRawAmount(policy.MaxFeeRaw); err != nil {
		return "", err
	} else if ok {
		fee, feeErr := parseRawAmount(candidate.FeeEstimateRaw)
		if feeErr != nil {
			return "", feeErr
		}
		if fee.Cmp(maxFee) > 0 {
			return "fee_exceeds_max", nil
		}
	}
	if candidate.RequiresNativeGas {
		if minGas, ok, err := optionalRawAmount(policy.MinNativeGasRaw); err != nil {
			return "", err
		} else if ok {
			gas, gasErr := parseRawAmount(candidate.NativeGasBalanceRaw)
			if gasErr != nil {
				return "", gasErr
			}
			if gas.Cmp(minGas) < 0 {
				return "", nil
			}
		}
	}
	return "", nil
}

func optionalRawAmount(raw string) (*big.Int, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false, nil
	}
	amount, err := parseRawAmount(raw)
	return amount, true, err
}

func parseRawAmount(raw string) (*big.Int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "0"
	}
	amount, ok := new(big.Int).SetString(raw, 10)
	if !ok || amount.Sign() < 0 {
		return nil, ErrInvalidSweepAmount
	}
	return amount, nil
}

func candidateGroupKey(candidate Candidate, policyKey string) string {
	parts := []string{
		candidate.Job.MerchantID.String(),
		strconv.FormatInt(int64(candidate.Job.ChainID), 10),
		assetKey(candidate.Job),
		candidate.ReserveWalletID.String(),
		strings.TrimSpace(candidate.SourcePolicyKey),
		strings.TrimSpace(policyKey),
	}
	return strings.Join(parts, ":")
}

func assetKey(job models.SweepJob) string {
	if job.Token == nil || strings.TrimSpace(*job.Token) == "" {
		return NativeAsset
	}
	return strings.ToLower(strings.TrimSpace(*job.Token))
}
