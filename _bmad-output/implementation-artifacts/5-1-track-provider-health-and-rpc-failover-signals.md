---
story_id: "5.1"
story_key: "5-1-track-provider-health-and-rpc-failover-signals"
epic: "Epic 5: Production Operations & Scale Readiness"
status: ready-for-dev
created: 2026-06-28
updated: 2026-06-28
baseline_commit: a8d1baa1f06905b701fa8e6b5d5dba3d37b2c6
---

# Story 5.1: Track Provider Health and RPC Failover Signals

Status: ready-for-dev

## Story

As an operator,
I want RPC/provider health, lag, and consistency to be measured,
so that stale nodes, missed blocks, and provider outages are visible before they cause missed deposits or stuck withdrawals.

## Acceptance Criteria

1. Given multiple providers or RPC URLs are configured for a chain, when health checks run, then the system records provider reachability, latest height/slot, response latency, error rate, and stale-head indicators, and results are tagged by chain and provider.
2. Given providers disagree on canonical head or lag beyond threshold, when provider comparison runs, then the system marks the provider unhealthy or degraded, and emits metrics/logs suitable for alerting.
3. Given failover strategy is configured, when a provider is unhealthy, then chain operations use the configured fallback or degraded-mode policy, and fallback decisions are observable in logs/metrics.
4. Given provider health features are implemented, when automated tests run, then they cover healthy provider, timeout, stale head, inconsistent head, failover selection, and metric emission.

## Tasks / Subtasks

- [ ] Task 1: Define provider health data contract, thresholds, and redaction rules (AC: 1, 2, 3)
  - [ ] Add a narrow provider-health model/repository or equivalent latest-snapshot store so `/metrics` and readiness do not perform live RPC calls on every scrape.
  - [ ] Track at minimum: chain id, chain name, provider identity, redacted provider label, provider URL hash, reachability, status (`healthy`, `degraded`, `unhealthy`), latest height/slot, optional head hash, response latency, lag from reference head, error category, redacted error detail, selected/failover decision, checked timestamp, and consecutive failure/error-rate data.
  - [ ] Never store or expose full provider URLs if they may contain credentials, API keys, or query tokens. Persist/log/metric-label only a configured label, host-only redaction, and/or stable hash.
  - [ ] Add config helpers for health timeout, interval, stale lag threshold, and failover strategy using existing env-style patterns. Support chain-specific overrides such as chain id and normalized chain name keys where practical.
  - [ ] Keep default behavior non-disruptive for local development: observe/report health by default; only reorder/block provider use when an explicit failover strategy is configured.
  - [ ] If adding a DB table, register it in `services/database.autoMigrateModels`, add `VerifySchema` coverage, and add schema tests.

- [ ] Task 2: Build deterministic provider probes for each supported chain family (AC: 1, 2, 4)
  - [ ] Create a reusable provider-health service instead of leaving probe logic embedded only in `api/handlers/v1_readiness.go`.
  - [ ] Reuse current probe behavior where possible: EVM JSON-RPC block probe, Solana finalized slot probe, Bitcoin tip height probe, and TRON gRPC block probe.
  - [ ] Use `blockchain.BaseChain.RPCs()` semantics for HTTP RPC candidates so env-provided URLs and chain defaults are merged consistently.
  - [ ] Preserve TRON-specific endpoint behavior from `v1TronGRPCEndpointsForChain`; do not silently switch TRON JSON-RPC and gRPC semantics.
  - [ ] Measure latency per provider with `context.Context` timeouts and classify errors into bounded categories such as `timeout`, `http_status`, `rpc_error`, `decode_error`, `unconfigured`, and `unreachable`.
  - [ ] Capture canonical head evidence where practical: height/slot always, and head hash/block id when a chain probe can get it cheaply. If a chain family cannot provide hash evidence in this story, mark that evidence as unavailable rather than treating it as successful quorum.

- [ ] Task 3: Compare providers and make failover/degraded decisions observable (AC: 2, 3, 4)
  - [ ] Compare providers within the same chain after a probe run. Use the highest healthy height/slot as the reference head unless a stricter configured policy exists.
  - [ ] Mark providers `degraded` for lag beyond threshold, missing hash evidence when a hash is required, or retryable throttling; mark `unhealthy` for timeout, unreachable endpoint, invalid response, or canonical head/hash disagreement beyond configured tolerance.
  - [ ] Implement a small failover decision helper that can rank configured provider URLs by latest health. Existing chain operations that already iterate RPC URLs should consume the ranked list or an equivalent health-aware selector.
  - [ ] Emit a bounded log/metric signal whenever the selected provider is not the configured primary. Include chain id/name, provider hash/label, status, reason, and strategy. Do not include raw URLs or secrets.
  - [ ] If every provider is unhealthy, apply the configured degraded-mode policy. In production, outbound-sensitive operations must not silently proceed as healthy.
  - [ ] Do not mutate payment, deposit, ledger, withdrawal, refund, or sweep lifecycle state from provider-health comparison itself. Chain Indexer still produces facts; business state changes remain in the existing boundaries.

- [ ] Task 4: Wire provider health into runtime, readiness, and metrics (AC: 1, 2, 3)
  - [ ] Add a `startProviderHealthWorker(ctx)` style loop in `main.go` following existing worker patterns for deposit facts, sweep jobs, reconciliation, and finalization. Run once at startup and then by configured interval.
  - [ ] Add router/dependency wiring for the provider-health repo/service without introducing a new process, broker, or physical service boundary.
  - [ ] Update `GET /api/v1/common/readiness` to include provider-health summary checks per chain and provider, using latest health snapshots where available. Keep the existing V1 error envelope and authenticated readiness route behavior.
  - [ ] Update `/metrics` to expose Prometheus-compatible gauges from latest snapshots, not live RPC calls. Suggested metrics: `gateway_provider_health`, `gateway_provider_latest_height`, `gateway_provider_lag_blocks`, `gateway_provider_response_latency_ms`, `gateway_provider_consecutive_failures`, and `gateway_provider_failover_decision`.
  - [ ] Preserve existing operational metrics: webhook delivery backlog, sweep job backlog, reconciliation jobs, chain worker count, chain state block/slot, migration readiness, and signer readiness.
  - [ ] Update `docs/integration-guide.md` and `readme.md` for new readiness checks, provider health metrics, env configuration, and production launch wording.

- [ ] Task 5: Keep schema, docs, and operator evidence production-safe (AC: 1, 2, 3)
  - [ ] If health snapshots are persisted, add bounded retention or upsert-latest behavior. Do not create an unbounded row per probe without an explicit retention policy.
  - [ ] Keep Prometheus labels low-cardinality: chain id/name, provider label/hash, status, and bounded reason category are acceptable; raw errors, block hashes, URLs, tx hashes, and arbitrary payloads are not labels.
  - [ ] Redact raw provider URLs in logs, metrics, readiness details, tests, and docs examples. API keys in URL userinfo/query params must never appear in output.
  - [ ] Update docs to say this story adds provider health/failover observability, not full archive/quorum exchange-grade infrastructure. Archive/quorum strategy evidence remains part of later Epic 5 readiness.
  - [ ] Add or update contract tests if the readiness response fields or documented metrics change.

- [ ] Task 6: Add focused validation and update story records (AC: 1, 2, 3, 4)
  - [ ] Unit-test config parsing, URL redaction/hash stability, provider status classification, stale lag threshold behavior, and failover ranking.
  - [ ] Use `httptest.NewServer` or injected fake probers for EVM, Solana, and Bitcoin tests. Do not require live public RPC access in automated tests.
  - [ ] For TRON, prefer an injected prober or gRPC test double. Do not make tests depend on `grpc.trongrid.io` or Shasta availability.
  - [ ] Add metrics tests that assert provider gauges are emitted and that raw URLs/secrets do not appear in metric output.
  - [ ] Add readiness tests that cover healthy, timeout/unhealthy, stale/degraded, and missing snapshot behavior.
  - [ ] If schema changes are added, include repository tests and `services/database` schema verification tests.
  - [ ] Run targeted validation: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./services/providerhealth ./api/handlers ./api/routes ./blockchain ./repositories ./services/database`.
  - [ ] Run full validation: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./...`.
  - [ ] Run static validation: `GOCACHE=/tmp/gateway-gocache-bmad go vet -p=1 ./...`.
  - [ ] Run whitespace validation: `git diff --check && git diff --cached --check`.
  - [ ] Update Dev Agent Record, Completion Notes, File List, Change Log, and move the story to `review` only after validation passes.

## Dev Notes

### Current Implementation Snapshot

- `GET /api/v1/common/readiness` is implemented in `api/handlers/v1_readiness.go`. It currently checks database access, migration strategy, production signer policy, metrics access policy, portal JWT secret, webhook/sweep/reconciliation backlog, chain registry, worker registration, Trust Wallet Core HD wallet derivation, and live RPC/gRPC block probes.
- Current readiness probe helpers are handler-local: `v1ProbeEVMRPC`, `v1ProbeSolanaRPC`, `v1ProbeBitcoinREST`, `v1ProbeTronGRPC`, and `v1JSONRPCCall`. Story 5.1 should move or wrap this logic into a reusable service so readiness, worker, and tests do not duplicate probe behavior.
- `/metrics` is implemented in `api/handlers/metrics.go`. It currently emits build, migration, signer, webhook backlog, sweep backlog, reconciliation, chain worker count, and chain state metrics. It reads persisted/repository state and should not start making live RPC calls during scrape.
- `models.ChainState` and `repositories.ChainStateRepo` track last processed and confirmed block/slot per chain. They do not track per-provider reachability, latency, lag, error categories, or failover decisions.
- `blockchain.BaseChain.RPCs()` merges env URLs and defaults, dedupes them, and supports both chain-name env keys and `CHAIN_<id>_RPC_URLS`. Use this behavior for provider discovery rather than reading only `RPCHttp`.
- Some existing chain code bypasses `RPCs()` and reads `RPCHttp` directly, for example EVM balance failover in `blockchain/chains/evm_compatible.go`. Check touched call sites carefully before claiming health-aware failover coverage.
- Existing `workers/listeners/rpcutil` has retryable/throttle classification helpers that can be reused for bounded error categories and backoff semantics.
- `main.go` already has interval worker patterns: webhook retry, session expiry, transaction finality, deposit fact processing, sweep jobs, reconciliation, and transfer finalization. Provider health should follow this style instead of introducing a new scheduler library.
- `startChainInfrastructure` wires chain listener workers and updates `ChainStateRepo`. Provider health must not turn listener events into business mutations; Chain Indexer facts remain the business boundary input.
- Recent work added `/metrics`, production readiness gates, outbound policy/audit controls, TRON testnet support, and admin/merchant UI changes. Expect a dirty worktree; do not revert unrelated changes.

### Architecture And Product Guardrails

- FR36 requires RPC/provider health scoring, fallback consistency checks, archive/quorum strategy, and per-provider metrics.
- NFR8 requires measurable SLO/alert thresholds for chain catch-up throughput, block lag, webhook lag, sweep backlog, signer latency, and reconciliation drift.
- NFR9 requires structured logs, Prometheus-compatible metrics, traces, dashboards, and alert rules.
- NFR10 requires stale node, missed block, provider outage, and inconsistent head behavior to trigger failover/quorum handling.
- AD-4 says Chain Indexer owns block/slot progress, raw transaction/log extraction, provider health, finality signals, and reorg detection. It emits facts; Deposit/Payment/Ledger/Webhook mutate business state.
- AD-7 still applies to outbound money movement. Provider failover must not bypass ledger holds, chain-resource reservations, signer policy, or reconciliation safeguards from Epic 4.
- Epic 4 retrospective action item says provider health signal contract must define provider state, lag, stale/inconsistent head, timeout, failover decision, and metric/log tags before implementation.

### Implementation Boundaries

- Do not add Redis, Kafka/NATS/SQS, an external monitoring vendor SDK, a new physical service, or a SPA UI.
- Do not treat this story as full archive/quorum infrastructure. It should create observable provider health and failover signals; later Epic 5 work covers broader launch evidence.
- Do not store raw provider URLs, API keys, headers, bearer tokens, or unbounded RPC payloads.
- Do not perform live RPC calls from `/metrics`; Prometheus scrapes must remain bounded and fast.
- Do not make automated tests depend on public RPC providers. Use local HTTP servers, fake probers, and deterministic inputs.
- Do not block all local development when no provider health snapshots exist. Production fail-closed/degraded behavior must be explicit and configurable.
- Do not change V1 public error envelope shape or webhook/API backwards compatibility while adding readiness details.
- Do not claim production wallet-provider custody readiness from this story alone. External signer, migrations, observability/runbooks, backup/restore, and launch gates remain open Epic 5 work.

### Likely Files To Touch

- `models/provider_health.go` or similarly named new provider-health snapshot model
- `repositories/provider_health_repo.go`
- `repositories/provider_health_repo_test.go`
- `services/providerhealth/*.go`
- `services/providerhealth/*_test.go`
- `api/handlers/v1_readiness.go`
- `api/handlers/v1_readiness_test.go`
- `api/handlers/metrics.go`
- `api/handlers/metrics_test.go`
- `api/routes/routes.go`
- `main.go`
- `blockchain/basechain.go`
- `blockchain/basechain_test.go`
- `blockchain/factory.go` only if the failover selector needs factory-level wiring
- `workers/listeners/rpcutil/rpcutil.go` only if retry/error classification is reused or extended
- `services/database/database.go` and `services/database/database_test.go` if a table is added
- `types/v1api.go` only if readiness response shape changes
- `docs/integration-guide.md`
- `readme.md`
- `docs/docs.go`, `docs/swagger.json`, `docs/swagger.yaml` only if Swagger-documented response types change
- `_bmad-output/implementation-artifacts/5-1-track-provider-health-and-rpc-failover-signals.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

### Testing Requirements

- Healthy provider: records reachable status, latest height/slot, latency, and healthy metric.
- Timeout/unreachable provider: records bounded error category, increments failure evidence, marks unhealthy, and redacts endpoint details.
- Stale head: with two providers, one lagging beyond threshold becomes degraded or unhealthy according to configured policy.
- Inconsistent head: same-height conflicting head hash/block id is degraded/unhealthy and emits alertable metric/log signal.
- Failover selection: configured primary unhealthy causes fallback selection; decision is visible in metric/log output and raw URLs are absent.
- Missing snapshots: readiness should be explicit about unknown/unavailable health without panicking or treating the system as fully healthy in production mode.
- Metrics: output is Prometheus text, low-cardinality, deterministic in tests, and does not leak provider URL credentials.
- Schema: if a provider health table is added, AutoMigrate registration, VerifySchema requirements, indexes, and repository upsert/list behavior are tested.

### Latest Technical Context

- No new external library is required for Story 5.1. Use the current stack: Go `1.25.4`, Gofiber `v3.3.0`, GORM `v1.31.1`, PostgreSQL driver `v1.6.0`, go-ethereum `v1.17.2`, btcd `v0.25.0`, solana-go `v1.12.0`, and the existing TRON SDK.
- Use Go standard `net/http`, `context`, `time`, `crypto/sha256`, and existing project helpers before adding dependencies.
- Keep metric formatting compatible with the existing hand-written Prometheus text emitter in `api/handlers/metrics.go`.

### Project Structure Notes

- The repo still uses the `core/...` module path and a modular-monolith layout. New service code should live under `services/providerhealth` or an equally narrow existing-package-aligned location.
- Models live under `models/`; repository ownership lives under `repositories/`; database registration and production schema verification live in `services/database`.
- Chain runtime types live under `blockchain/`; concrete chain families live under `blockchain/chains/`; listeners live under `workers/listeners`.
- Do not introduce an `internal/chainindexer` tree in this story unless the surrounding repo has already moved there. Planning artifacts mention future boundaries, but current code does not use that directory structure yet.

### References

- Story source: `_bmad-output/planning-artifacts/epics.md` Story 5.1.
- PRD FR36, NFR8, NFR9, NFR10, operator dashboard requirements: `_bmad-output/planning-artifacts/prds/prd-gateway-2026-06-27/prd.md`.
- Architecture AD-4, AD-7 and project structure notes: `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md`.
- Solution design scale phase: `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/SOLUTION-DESIGN.md`.
- Project rules: `_bmad-output/project-context.md`.
- Current readiness implementation: `api/handlers/v1_readiness.go`.
- Current metrics implementation: `api/handlers/metrics.go`.
- Current chain/RPC merge behavior: `blockchain/basechain.go`.
- Current chain factory registration: `application/configuration/chains.go`.
- Current chain state persistence: `models/chainstate.go`, `repositories/chainstate_repo.go`.
- Current worker patterns: `main.go`.
- Epic 4 retrospective: `_bmad-output/implementation-artifacts/epic-4-retro-2026-06-28.md`.

## Dev Agent Record

### Agent Model Used

Codex

### Debug Log References

- 2026-06-28: Story created from Epic 5.1 after Epic 4 code review and retrospective completed. Inspected sprint-status, Epic 5 requirements, PRD FR36/NFR8/NFR9/NFR10, architecture AD-4/AD-7, current readiness, metrics, chain factory, chain state, RPC config, listener config, and main worker patterns.
- 2026-06-28: No web research was required because the story uses existing project dependencies and standard-library probes; no new external technology or API version is being introduced.

### Completion Notes List

- Ultimate context engine analysis completed - comprehensive developer guide created.

### File List

- `_bmad-output/implementation-artifacts/5-1-track-provider-health-and-rpc-failover-signals.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

### Change Log

- 2026-06-28: Created ready-for-dev Story 5.1 with provider health snapshot, probe, consistency, failover, readiness, metrics, docs, and validation guidance.
