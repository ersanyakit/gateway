# Sweep Operations Runbook

Auto-sweeps remain durable `sweep_jobs`; the planner only groups eligible jobs and records metadata. Execution still passes through ledger hold, outbound transaction, signer, fee/resource and reconciliation gates.

## Planning Rules

- Batch only jobs with the same merchant, chain, asset/token, reserve wallet and source policy key.
- Use chain capabilities before assigning a batch. Bitcoin can be planned as a batch; EVM, Solana and TRON default to individual execution unless an operator enables a chain-specific batching implementation.
- Do not batch pending, succeeded or active processing jobs. Batch metadata is assigned only to pending or failed jobs.
- Respect policy thresholds before planning: minimum sweep amount, dust amount, max fee estimate and minimum native gas balance for token sweeps.

## Gas Funding

Token sweeps that need native gas use linked parent state on `sweep_jobs`:

- `prefund_last_attempt_at` gates duplicate funding broadcasts inside `SWEEP_PREFUND_RETRY_AFTER`.
- `prefund_attempts` and `prefund_max_attempts` enforce a retry limit.
- When prefund attempts hit the limit, `operator_action` becomes `review_gas_funding`.
- Funding and the later sweep remain under the same wallet/chain claim policy, so the same wallet cannot be funded and swept by competing workers.

## Operator Recovery

Broadcast-uncertain jobs must not retry blindly. They move to dead-letter with `operator_action=reconcile_broadcast` and a scoped reconciliation job. The operator must inspect chain outcome and then choose one recovery action:

- `retry`: chain evidence shows no broadcast landed; the job returns to failed with `next_run_at`.
- `mark_success`: chain evidence confirms the sweep or replacement transaction; the job records the tx hash and succeeds.
- `preserve_hold`: outcome remains uncertain; keep the ledger hold and dead-letter state.
- `release_hold`: evidence proves no spend happened and the ledger hold can be released through the ledger recovery path.

## Verification

Run:

- `go test -p=1 -count=1 ./services/sweeps ./repositories`
- `go test -p=1 -count=1 ./services/database ./services/dbmigrations`

Production readiness still requires `GATEWAY_DB_MIGRATION_VERSION` to match `dbmigrations.LatestID()` and `services/database.VerifySchema` to pass.
