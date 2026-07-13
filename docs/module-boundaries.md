# Module Boundaries

This gateway is a modular monolith. Modules may share a process and database, but money-path state changes must cross boundaries through documented services, repositories, worker commands or money events.

## Composition Root

Application startup wiring belongs in the composition root:

- `application.CompositionRoot` owns the runtime dependencies passed from `main.go`.
- `workers/supervisor` owns periodic worker lifecycle, context cancellation, restart policy and shutdown order.
- `main.go` may choose configuration and call the composition root, but it should not grow new money-path business rules.

## Module Ownership

| Module | Owns tables/state | Accepts commands | Emits events |
| --- | --- | --- | --- |
| Chain indexer | `chain_states`, `chain_facts` | listener observations, historical replay | chain fact observations |
| Deposits | `deposits`, payment deposit allocations | finalized or confirming chain facts | `deposit.detected.v1`, `deposit.finalized.v1` |
| Payments | `payment_sessions`, idempotency keys | payment creation, deposit match outcomes | `payment.*.v1` |
| Ledger | `ledger_entries`, balance projections, holds | settlement, reversal, reserve and release commands | ledger journal records |
| Outbound | withdrawal, refund, sweep and outbound transaction rows | hold-backed broadcast requests | `withdrawal.*.v1`, `refund.*.v1`, `sweep.*.v1` |
| Webhooks | money event outbox and delivery rows | deliver, retry, replay and dead-letter commands | at-least-once partner deliveries |
| Reconciliation | reconciliation jobs | scoped uncertainty reports | operator-facing reconciliation evidence |

## Boundary Rules

- Chain listeners record facts only; they do not mutate payments, ledger balances, webhooks or sweeps directly.
- Deposit, payment, ledger and outbound modules use repositories or service interfaces at their boundary instead of arbitrary cross-module table updates.
- Public partner API behavior remains compatible during refactors; structural moves need contract tests.
- Worker loops must accept cancellation through context and be registered with the supervisor when added to startup.
