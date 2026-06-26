package reconciliation

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"core/blockchain"
	"core/constants"
	"core/models"
	"core/repositories"

	"github.com/google/uuid"
)

const (
	reserveIssueNone        reserveIssueKind = ""
	reserveIssueDeficit     reserveIssueKind = "deficit"
	reserveIssueMissing     reserveIssueKind = "missing_component"
	reserveIssueUnreadable  reserveIssueKind = "unreadable_component"
	reserveReasonMaxLength                   = 120
)

type reserveIssueKind string

type ReserveService struct {
	WalletRepo         *repositories.WalletRepo
	LedgerRepo         *repositories.LedgerRepo
	ReconciliationRepo *repositories.ReconciliationRepo
	Chains             *blockchain.ChainFactory
}

type ReserveReport struct {
	WalletsChecked      int
	BalanceQueries      int
	JobsOpened          int
	Deficits            int
	MissingComponents   int
	UnreadableBalances  int
	QueryErrors         int
	MissingAddresses    int
	UnavailableChains   int
}

type reserveExpectedBalance struct {
	Token      *string
	Symbol     string
	Decimals   uint8
	BalanceRaw *big.Int
}

func NewReserveService(walletRepo *repositories.WalletRepo, ledgerRepo *repositories.LedgerRepo, reconciliationRepo *repositories.ReconciliationRepo, chains *blockchain.ChainFactory) *ReserveService {
	return &ReserveService{
		WalletRepo:         walletRepo,
		LedgerRepo:         ledgerRepo,
		ReconciliationRepo: reconciliationRepo,
		Chains:             chains,
	}
}

func (s *ReserveService) RunOnce(ctx context.Context, limit int) (ReserveReport, error) {
	var report ReserveReport
	if s == nil || s.WalletRepo == nil || s.LedgerRepo == nil || s.ReconciliationRepo == nil || s.Chains == nil {
		return report, nil
	}

	wallets, err := s.WalletRepo.ListReserveWallets(ctx, limit)
	if err != nil {
		return report, err
	}

	var errs []error
	for _, wallet := range wallets {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		report.WalletsChecked++

		rows, err := s.LedgerRepo.MerchantBalances(ctx, wallet.MerchantID)
		if err != nil {
			errs = append(errs, fmt.Errorf("merchant balance query %s: %w", wallet.MerchantID, err))
			continue
		}
		expectedByChain := reserveExpectedByChain(rows)
		for chainID, expected := range expectedByChain {
			if len(expected) == 0 {
				continue
			}
			chain, err := s.Chains.GetChainByID(chainID)
			if err != nil {
				report.UnavailableChains++
				created, createErr := s.openJob(ctx, chainID, "chain_unavailable", wallet.MerchantID, "")
				if createErr != nil {
					errs = append(errs, createErr)
				}
				if created {
					report.JobsOpened++
				}
				continue
			}

			address := repositories.WalletAddressForChainID(wallet, chainID)
			if strings.TrimSpace(address) == "" {
				report.MissingAddresses++
				created, createErr := s.openJob(ctx, chainID, "missing_address", wallet.MerchantID, "")
				if createErr != nil {
					errs = append(errs, createErr)
				}
				if created {
					report.JobsOpened++
				}
				continue
			}

			report.BalanceQueries++
			results := chain.BatchBalances(ctx, []string{address}, 1)
			result, ok := balanceResultForAddress(results, address)
			if !ok {
				report.QueryErrors++
				created, createErr := s.openJob(ctx, chainID, "no_result", wallet.MerchantID, "")
				if createErr != nil {
					errs = append(errs, createErr)
				}
				if created {
					report.JobsOpened++
				}
				continue
			}
			if result.Error != nil {
				report.QueryErrors++
				created, createErr := s.openJob(ctx, chainID, "query_failed", wallet.MerchantID, "")
				if createErr != nil {
					errs = append(errs, createErr)
				}
				if created {
					report.JobsOpened++
				}
				continue
			}

			components := parseBalanceComponents(result.Balance)
			for _, expectedBalance := range expected {
				issue := evaluateExpectedReserve(components, expectedBalance, chainID)
				if issue == reserveIssueNone {
					continue
				}

				switch issue {
				case reserveIssueDeficit:
					report.Deficits++
				case reserveIssueMissing:
					report.MissingComponents++
				case reserveIssueUnreadable:
					report.UnreadableBalances++
				}

				created, createErr := s.openJob(ctx, chainID, string(issue), wallet.MerchantID, expectedBalance.Symbol)
				if createErr != nil {
					errs = append(errs, createErr)
				}
				if created {
					report.JobsOpened++
				}
			}
		}
	}

	return report, errors.Join(errs...)
}

func reserveExpectedByChain(rows []repositories.LedgerBalanceRow) map[constants.ChainID][]reserveExpectedBalance {
	out := make(map[constants.ChainID][]reserveExpectedBalance)
	for _, row := range rows {
		if row.Account != models.LedgerAccountMerchantAvailable {
			continue
		}
		balance, ok := new(big.Int).SetString(strings.TrimSpace(row.BalanceRaw), 10)
		if !ok || balance.Sign() <= 0 {
			continue
		}
		chainID := constants.ChainID(row.ChainID)
		out[chainID] = append(out[chainID], reserveExpectedBalance{
			Token:      row.Token,
			Symbol:     strings.ToUpper(strings.TrimSpace(row.Symbol)),
			Decimals:   row.Decimals,
			BalanceRaw: balance,
		})
	}
	return out
}

func evaluateExpectedReserve(components map[string]string, expected reserveExpectedBalance, chainID constants.ChainID) reserveIssueKind {
	if expected.BalanceRaw == nil || expected.BalanceRaw.Sign() <= 0 {
		return reserveIssueNone
	}
	actual, found, readable := actualRawForExpected(components, expected, chainID)
	if !found {
		return reserveIssueMissing
	}
	if !readable {
		return reserveIssueUnreadable
	}
	if actual.Cmp(expected.BalanceRaw) < 0 {
		return reserveIssueDeficit
	}
	return reserveIssueNone
}

func actualRawForExpected(components map[string]string, expected reserveExpectedBalance, chainID constants.ChainID) (*big.Int, bool, bool) {
	if len(components) == 0 {
		return nil, false, false
	}
	for _, symbol := range balanceSymbolCandidates(expected, chainID) {
		raw, ok := components[symbol]
		if !ok {
			continue
		}
		amount, readable := amountToRaw(raw, expected.Decimals)
		return amount, true, readable
	}
	if isNativeExpected(expected) {
		if raw, ok := components[""]; ok {
			amount, readable := amountToRaw(raw, expected.Decimals)
			return amount, true, readable
		}
	}
	return nil, false, false
}

func parseBalanceComponents(raw string) map[string]string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	components := make(map[string]string)
	parts := strings.Split(raw, "|")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, ":")
		if !ok {
			if len(parts) == 1 {
				components[""] = part
			}
			continue
		}
		key = strings.ToUpper(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			components[key] = value
		}
	}
	return components
}

func amountToRaw(value string, decimals uint8) (*big.Int, bool) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "-") {
		return nil, false
	}
	if strings.HasPrefix(value, "0x") || strings.HasPrefix(value, "0X") {
		amount, ok := new(big.Int).SetString(value[2:], 16)
		return amount, ok
	}
	if !strings.Contains(value, ".") {
		amount, ok := new(big.Int).SetString(value, 10)
		if !ok || amount.Sign() < 0 {
			return nil, false
		}
		return amount, true
	}

	decimal, ok := new(big.Rat).SetString(value)
	if !ok || decimal.Sign() < 0 {
		return nil, false
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	decimal.Mul(decimal, new(big.Rat).SetInt(scale))
	if decimal.Denom().Cmp(big.NewInt(1)) != 0 {
		return nil, false
	}
	return new(big.Int).Set(decimal.Num()), true
}

func balanceResultForAddress(results []models.BalanceResult, address string) (models.BalanceResult, bool) {
	for _, result := range results {
		if strings.EqualFold(strings.TrimSpace(result.Address), strings.TrimSpace(address)) {
			return result, true
		}
	}
	if len(results) == 1 {
		return results[0], true
	}
	return models.BalanceResult{}, false
}

func balanceSymbolCandidates(expected reserveExpectedBalance, chainID constants.ChainID) []string {
	seen := map[string]struct{}{}
	candidates := make([]string, 0, 4)
	add := func(symbol string) {
		symbol = strings.ToUpper(strings.TrimSpace(symbol))
		if symbol == "" {
			return
		}
		if _, ok := seen[symbol]; ok {
			return
		}
		seen[symbol] = struct{}{}
		candidates = append(candidates, symbol)
	}

	add(expected.Symbol)
	if isNativeExpected(expected) {
		for _, symbol := range nativeSymbols(chainID) {
			add(symbol)
		}
	}
	return candidates
}

func nativeSymbols(chainID constants.ChainID) []string {
	switch chainID {
	case constants.Bitcoin:
		return []string{"BTC", "BITCOIN"}
	case constants.Ethereum, constants.Base, constants.Arbitrum, constants.Unichain:
		return []string{"ETH", "ETHEREUM"}
	case constants.Binance:
		return []string{"BNB", "BSC", "BINANCE"}
	case constants.Avalanche:
		return []string{"AVAX", "AVALANCHE"}
	case constants.Chiliz, constants.ChilizSpicy:
		return []string{"CHZ", "CHILIZ"}
	case constants.Solana:
		return []string{"SOL", "SOLANA"}
	case constants.TRON:
		return []string{"TRX", "TRON"}
	default:
		return nil
	}
}

func isNativeExpected(expected reserveExpectedBalance) bool {
	return expected.Token == nil || strings.TrimSpace(*expected.Token) == ""
}

func (s *ReserveService) openJob(ctx context.Context, chainID constants.ChainID, kind string, merchantID uuid.UUID, symbol string) (bool, error) {
	reason := reserveReason(kind, chainID, merchantID, symbol)
	_, created, err := s.ReconciliationRepo.CreateOpenIfMissing(ctx, chainID, 0, 0, reason)
	if err != nil {
		return false, fmt.Errorf("reserve reconciliation job %q: %w", reason, err)
	}
	return created, nil
}

func reserveReason(kind string, chainID constants.ChainID, merchantID uuid.UUID, symbol string) string {
	chainName := constants.ChainName(chainID)
	if chainName == "" {
		chainName = fmt.Sprintf("chain_%d", chainID)
	}
	merchant := merchantID.String()
	if len(merchant) > 12 {
		merchant = merchant[:12]
	}
	parts := []string{"reserve_balance", kind, chainName, merchant}
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if symbol != "" {
		parts = append(parts, symbol)
	}
	reason := strings.Join(parts, ":")
	if len(reason) > reserveReasonMaxLength {
		return reason[:reserveReasonMaxLength]
	}
	return reason
}
