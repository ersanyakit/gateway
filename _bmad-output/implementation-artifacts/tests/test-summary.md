# Test Automation Summary

## Story 4.1 - Generated Tests

### API Tests
- [x] `api/handlers/dealer_test.go` - V1 payout creation keeps insufficient ledger hold failures in the V1 error envelope with HTTP 400.
- [x] `api/handlers/dealer_test.go` - V1 refund creation now has the same insufficient/reservation failure contract coverage as payout creation.
- [x] `api/handlers/dealer_test.go` - Admin withdrawal approve/reject paths are contract-tested for operator audit logging on success and failure.

### E2E / Service Workflow Tests
- [x] `repositories/withdrawal_request_repo_test.go` - Withdrawal approval workflow now covers a successful hold followed by pre-broadcast transfer failure, failed request state, idempotent hold voiding, and no tx hash.
- [x] `repositories/refund_repo_test.go` - Refund workflow now covers successful hold creation followed by pre-broadcast terminal failure, hold voiding, and idempotent duplicate release.
- [x] Existing `repositories/ledger_repo_test.go` coverage exercises successful withdrawal/refund/sweep holds, insufficient funds, duplicate idempotency, concurrent overdraw prevention, sweep release, and ledger invariant balance projections.
- [x] Existing handler contract coverage blocks direct unreserved sweep/withdraw broadcast paths and enforces reserved transfer helpers.

## Story 4.1 - Coverage

- API endpoints: V1 payout/refund hold failure mapping covered for validation-style bad request responses.
- Operator UI handlers: admin withdrawal approval/rejection audit log contracts covered.
- Ledger workflows: successful hold, insufficient funds, concurrent competing holds, duplicate idempotency, pre-broadcast failure release, post-broadcast hold preservation, sweep hold/release, and schema contracts covered.
- UI E2E framework: no Playwright/Cypress-style UI runner exists in this Go server-rendered project; workflow coverage was added through existing Go handler/repository tests.

## Story 4.1 - Validation

- [x] `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./repositories`
- [x] `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./api/handlers -run 'TestV1PayoutCreateMapsInsufficientHoldToBadRequest|TestV1RefundCreateMapsInsufficientHoldToBadRequest|TestAdminWithdrawalOperatorActionsWriteAuditLogs|TestOutboundHandlersRequireLedgerReservationContracts'`
- [x] `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./services/reconciliation`
- [x] `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./repositories ./api/handlers ./services/database`
- [x] `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 . ./services/reconciliation`
- [x] `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./...`
- [x] `GOCACHE=/tmp/gateway-gocache-bmad go vet -p=1 ./...`
- [x] `git diff --check && git diff --cached --check`
- [i] The targeted and full commands that include `./api/handlers` were rerun outside the execution sandbox because `TestV1ProbeEVMRPC` uses `httptest.NewServer` and needs localhost bind permission.

## Story 4.2 - Generated Tests

### API Tests
- [x] `api/handlers/metrics_test.go` - Production software signer gate is exposed as `gateway_production_signer_ready 0` on the protected metrics endpoint and does not leak secret-like environment values.

### E2E / Integration Tests
- [x] `services/signer/policy_test.go` - Unsupported signer modes now hard-fail with sanitized audit evidence.
- [x] `services/signer/policy_test.go` - Signer audit metadata proves sensitive metadata keys are excluded while safe policy metadata keys remain visible.
- [x] `services/signer/policy_test.go` - Production readiness now rejects unsupported signer modes explicitly.

## Story 4.2 - Coverage

- Signer policy acceptance criteria: 5/5 covered by unit and integration-style Go tests.
- API operational signer visibility: metrics surface covered.
- UI E2E coverage: not applicable; Story 4.2 has no browser UI workflow and this repository has no Playwright/Cypress setup.

## Story 4.2 - Validation

- [x] `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./services/signer -run 'TestAuthorize|TestProductionReadiness'`
- [x] `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./api/handlers -run 'TestOperationalMetricsReportsProductionSignerGate|TestOperationalMetricsIncludesBacklogAndChainState|TestV1ProductionSignerReadiness'`
- [x] `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./blockchain/chains -run 'TestEVMSendNativeChecksSignerPolicyBeforeRPCAndPrivateKey|TestBitcoinSendChecksSignerPolicyBeforePrivateKey|TestSolanaWithdrawChecksSignerPolicyBeforeRPCAndPrivateKey|TestTronSendChecksSignerPolicyBeforeRPCAndPrivateKey'`
- [x] `git diff --check`
- [i] Full package validation for `./api/handlers`, `./blockchain/chains`, and `./...` was attempted, but the local sandbox blocks `httptest.NewServer` TCP listen calls with `bind: operation not permitted`. The new tests were isolated and passed.

## Previous Generated Tests

## Generated Tests

### API Tests
- [x] `api/handlers/payment_test.go` - Checkout payer state now explicitly covers a reorg-corrected failed payment that still has a tx hash, preventing stale `paid` or `confirming` UI/API state.

### E2E / Service Workflow Tests
- [x] `repositories/ledger_repo_test.go` - Ledger reversal workflow now proves reorg correction skips existing reversal rows, voided rows, and unrelated transaction hashes while remaining idempotent.
- [x] Existing `repositories/transaction_repo_test.go` coverage exercises deterministic same-height fork replacement, parent mismatch block-range correction, duplicate reorg handling, webhook correction metadata, sweep dead-lettering, and reconciliation job creation.
- [x] Existing `repositories/payment_repo_test.go` coverage exercises payment correction for paid, underpaid, overpaid, partial paid, expired-after-deposit, and already-corrected idempotent paths.
- [x] `repositories/reconciliation_repo_test.go` - Scoped reconciliation coverage exercises active scope-key dedupe, legacy fallback dedupe, evidence recording, sensitive value redaction, retry scheduling, claim lifecycle, resolved/failed re-open behavior, webhook drift, and stuck lifecycle scopes.
- [x] `services/reconciliation/reserve_test.go` - Reserve drift coverage now verifies scoped reconciliation job creation, tenant/resource context, affected ids/evidence JSON, and active scope-key dedupe for reserve deficits.
- [x] Existing `services/webhook/notifier_test.go` and `services/webhook/event_catalog_test.go` coverage exercises `transaction.reorged.v1` payload metadata, reorg-corrected `payment.failed` relation fields, and legacy alias/catalog contract.
- [x] Existing `services/deposits/service_test.go`, `services/database/database_test.go`, and `docs/integration_contract_test.go` coverage exercises corrected chain fact processing guards, schema contract fields, and integration documentation contract.

## Coverage

- API payment read surfaces: checkout state and status payload behavior covered for reorg-corrected failed state.
- Ledger reversal critical errors: existing reversal row, voided row, unrelated transaction row, duplicate correction call, and normal reversal path covered.
- Story 3.5 acceptance coverage: deterministic fork simulation, duplicate reorg event handling, stale checkout/payment state, correction webhook metadata, ledger reversal, sweep blocking, and reconciliation job creation covered by generated plus existing tests.
- Payment correction webhook coverage now includes relation fields and legacy paid outcome backfill for deriving the prior payment lifecycle event.
- Story 3.6 acceptance coverage: scoped reconciliation schema, active dedupe, evidence/outcome lifecycle, reorg-created scope preservation, webhook drift, stuck lifecycle scope, reserve/ledger invariant scope, and operator-visible metrics/readiness statuses are covered.

## Validation

- [x] `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./repositories ./services/webhook ./api/handlers ./docs ./services/database ./services/deposits`
- [x] `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./repositories ./services/reconciliation ./services/database ./api/handlers`
- [x] `OUTBOX_TEST_DATABASE_URL='host=127.0.0.1 port=5432 user=postgres password=postgres dbname=gateway sslmode=disable' GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./repositories -run TestReconciliationRepo -v`
- [x] `OUTBOX_TEST_DATABASE_URL='host=127.0.0.1 port=5432 user=postgres password=postgres dbname=gateway sslmode=disable' GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./services/reconciliation -run TestReserveServiceOpenJobCreatesScopedReconciliation -v`
- [x] `OUTBOX_TEST_DATABASE_URL='host=127.0.0.1 port=5432 user=postgres password=postgres dbname=gateway sslmode=disable' GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./services/database`
- [x] `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./...`
- [x] `GOCACHE=/tmp/gateway-gocache-bmad go vet -p=1 ./...`
- [x] `git diff --check && git diff --cached --check`
- [x] Review rerun: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./repositories ./services/webhook ./docs ./services/database ./services/deposits`
- [x] Review rerun: `GOCACHE=/tmp/gateway-gocache-bmad go vet -p=1 ./...`
- [x] Review rerun: `git diff --check && git diff --cached --check`
- [x] QA E2E generation rerun: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./services/reconciliation`
- [x] QA E2E generation rerun: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./repositories`
- [x] QA E2E generation rerun: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./repositories ./services/reconciliation ./services/database`
- [x] QA E2E generation rerun: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./api/handlers -run 'TestOperationalMetricsIncludesBacklogAndChainState|TestV1ReadinessCountTotal|TestV1ReadinessCountDetails'`

## Notes

- Full repository validation passed on 2026-06-27 after running the Story 3.5 targeted packages, scoped Postgres-backed checks, full test suite, static vet check, and whitespace checks.
- Postgres validation caught and the implementation fixed a parent-mismatch ordering issue where generic same-height hash conflict handling could mask the more specific `parent_mismatch` correction reason.
- Story 3.6 validation passed after running targeted packages, scoped Postgres-backed reconciliation/database checks, full test suite, static vet check, and whitespace checks. The earlier sandbox-only listener bind failure was resolved by rerunning the same tests with the required local test-server permission.
- QA E2E generation for Story 3.6 added reserve drift scoped reconciliation coverage. The new Postgres-backed reserve test skips when `OUTBOX_TEST_DATABASE_URL` is not configured and runs as part of `./services/reconciliation` when a test database DSN is available.
