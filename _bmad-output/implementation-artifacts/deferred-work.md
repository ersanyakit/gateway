## Deferred from: code review of 4-1-reserve-ledger-holds-before-outbound-money-movement (2026-06-27)

- Withdrawal/refund post-broadcast ledger failures do not open scoped reconciliation [repositories/withdrawal_request_repo.go:369]. This is a real money-state uncertainty gap, but it predates Story 4.1 and needs a broader repository/handler reconciliation boundary rather than a local hold gate patch.
