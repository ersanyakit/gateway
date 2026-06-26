---
project_name: gateway
user_name: ersan
date: 2026-06-27
sections_completed:
  - technology_stack
  - language_rules
  - framework_rules
  - testing_rules
  - quality_rules
  - workflow_rules
  - anti_patterns
status: complete
rule_count: 39
optimized_for_llm: true
---

# Project Context for AI Agents

_This file contains critical rules and patterns that AI agents must follow when implementing code in this project. Focus on unobvious details that agents might otherwise miss._

---

## Technology Stack & Versions

- **Runtime:** Go `1.25.4`, module path `core`.
- **Web:** Gofiber `v3.3.0`, `github.com/gofiber/template/html/v3 v3.0.4`, server-rendered templates under `views/`.
- **Database:** PostgreSQL via GORM `v1.31.1` and `gorm.io/driver/postgres v1.6.0`.
- **Crypto/wallet:** Trust Wallet Core Go binding via `replace tw => ./third_party/trustwallet/wallet-core/samples/go`.
- **Chain SDKs:** go-ethereum `v1.17.2`, btcd `v0.25.0`, gagliardetto/solana-go `v1.12.0`, OKX TRON SDK pseudo-version.
- **Realtime/webhook:** Fiber/fasthttp websockets, internal `services/realtime`, `services/webhook`.
- **Docs/contracts:** Swagger artifacts under `docs/`; BMad planning artifacts under `_bmad-output/planning-artifacts`.

## Critical Implementation Rules

### Language-Specific Rules

- Keep package imports on the `core/...` module path; do not introduce a second module root.
- Use `context.Context` already available from Fiber/GORM paths for repository/service calls; do not add background contexts inside request handling unless starting a deliberate worker.
- Preserve raw on-chain amounts as integer strings plus decimals/symbol metadata. Do not recalculate stored quote display values from current oracle prices.
- Use constant-time comparison for signatures/secrets. Do not compare HMACs or secret material with plain `==`.
- Avoid broad goroutine fire-and-forget for money movement. Durable jobs/outbox/reconciliation are preferred for retryable money side effects.

### Framework-Specific Rules

- V1 REST auth currently lives in `api/handlers/v1api.go` helper functions, not global middleware. Keep auth changes local unless deliberately refactoring all call sites.
- Preserve V1 error envelope shape: `{"result":"error","message":"..."}`.
- Public webhook signing still uses `helpers.GenerateSignature(secret, timestamp, body)`. V1 request signing uses canonical method/path/timestamp/body helpers. Do not merge these semantics accidentally.
- GORM models live in `models/`; repository ownership lives in `repositories/`. Do not bypass repositories for cross-boundary money state mutation unless a story explicitly requires it.
- `services/database.Migrate` still uses `AutoMigrate`; production migration discipline is a planned gate, so avoid adding irreversible or lock-heavy schema assumptions silently.
- Server-rendered UX must remain compatible with existing `views/`, `static/`, Tailwind-style templates, and Gofiber rendering. Do not introduce a SPA framework for UI stories.

### Money-Core Rules

- Ledger is the authoritative balance source. Balance APIs must use ledger-derived queries/projections, not transaction row sums or live chain reads.
- Chain listeners/indexers produce facts; they must not directly mark payments paid, mutate business lifecycle, or post ledger entries except through the intended boundary path.
- Every money-affecting transition needs stable idempotency and duplicate-delivery safety at repository/outbox/ledger level.
- Reorg/correction behavior must use compensating entries and correction events, not destructive edits to posted money history.
- Any uncertain money state must open or update a scoped reconciliation job with reason, affected resources, and recovery state.
- Outbound withdrawal/refund/sweep must not sign or broadcast without ledger hold/reservation and chain-resource reservation.
- Production signing must not expose mnemonic/private key to app code, app DB, logs, or responses. `SIGNER_MODE=software` is development-only.

### Security and Diagnostics Rules

- Never log or return API secrets, webhook secrets, raw signatures, mnemonics, private keys, full sensitive payloads, or unredacted diagnostic blobs.
- Auth failures may log tenant/domain context when known, endpoint, failure category, and correlation id.
- Tenant/domain isolation is mandatory: a valid API key for one domain must not reveal whether another domain's payment, wallet, webhook, payout, or refund exists.
- Webhook callback URLs must be validated at delivery time; diagnostics must show redacted attempts, retry state, and replay/audit context.
- High-risk admin actions require auditability. Preserve actor, scope, action, outcome, timestamp, and correlation id.

### Testing Rules

- Use standard Go `testing` patterns already present in `*_test.go`; keep focused helper tests near the package being changed.
- For auth/signature work, test positive and negative paths: API key, bearer, missing secret, mismatched key/secret, malformed/expired/future timestamp, method/path/body tampering, replay.
- For money movement, include duplicate/replay tests and DB-level uniqueness/invariant checks where the story touches ledger/outbox/jobs.
- For chain behavior, prefer deterministic unit/simulator tests over live network calls.
- Run targeted tests for touched packages before full regression. Full regression targets are `go test ./...` and `go vet ./...` when the repo state supports them.
- If a story changes public API/webhook behavior, update or add contract tests and docs/swagger or integration-guide evidence.

### Code Quality & Style Rules

- Keep changes narrowly scoped to the story's owned files. Do not refactor unrelated money paths while fixing one acceptance criterion.
- Prefer small helpers over broad rewrites in handlers and repositories; many flows rely on existing helper semantics.
- Preserve existing compatibility aliases for legacy underscore webhook events until an explicit catalog migration retires them.
- Keep comments sparse and useful. Add comments only for non-obvious money safety, idempotency, signer, or replay logic.
- New public behavior should reference the planning artifacts: PRD, UX, architecture spine, epics, and story file.

### Development Workflow Rules

- Read the current story file and this project context before code changes.
- Treat `_bmad-output/implementation-artifacts/sprint-status.yaml` as workflow state; update story status only through the appropriate BMad story/dev workflow.
- Do not revert unrelated user changes in the working tree.
- For story implementation, update Dev Agent Record, File List, Change Log, completion notes, and status in the story file.
- Run code review in a fresh context after dev-story reaches review status.

### Critical Don't-Miss Rules

- Do not break webhook HMAC behavior while changing V1 request HMAC behavior.
- Do not treat explicit start-block config as full historical backfill. Range replay/backfill remains a production-readiness requirement.
- Do not mark checkout/payment paid before finality gate passes.
- Do not use magenta/accent UI color to mean success/warning/danger. Use status tokens from the UX design spine.
- Do not hide audit/reconciliation context behind hover-only UI.
- Do not introduce external brokers, new service boundaries, or SPA architecture unless the current story explicitly requires it.
- Do not claim production wallet-provider custody readiness until external signer, reconciliation, compliance, observability, backup/restore, and launch gates are complete.

---

## Usage Guidelines

**For AI Agents:**

- Read this file before implementing any code.
- Follow all rules exactly as documented.
- When in doubt, prefer the more restrictive money-safety option.
- Update this file only when new durable project patterns emerge.

**For Humans:**

- Keep this file lean and focused on agent needs.
- Update when the technology stack or money-flow architecture changes.
- Remove rules that become obvious or obsolete over time.

Last Updated: 2026-06-27
