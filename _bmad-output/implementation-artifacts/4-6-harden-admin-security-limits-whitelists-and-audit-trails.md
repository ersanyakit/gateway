---
story_id: "4.6"
story_key: "4-6-harden-admin-security-limits-whitelists-and-audit-trails"
epic: "Epic 4: Safe Outbound Funds & Custody Controls"
status: review
created: 2026-06-28
updated: 2026-06-28
baseline_commit: 9a18d577c78e1dc0e7739cc1648ad8532cb15a29
---

# Story 4.6: Harden Admin Security, Limits, Whitelists, and Audit Trails

Status: review

## Story

As a platform owner,
I want outbound money operations protected by admin security controls and immutable audit trails,
so that high-risk actions can be reviewed, limited, and stopped before funds leave custody.

## Acceptance Criteria

1. Given an admin or dealer portal action can affect outbound money movement, when the action is submitted, then portal mutation JWT/session protections, actor authorization, and tenant/domain scope checks run before state changes, and failures do not leak sensitive resource existence.
2. Given a withdrawal or payout destination is submitted, when policy validation runs, then address whitelist, velocity limit, per-tenant limit, and emergency freeze policy are enforced where configured, and policy failures are audit logged.
3. Given an approval decision is made, when audit logging occurs, then it records actor, role, tenant/domain, subject id, decision, reason, timestamp, correlation id, and before/after status, and audit logs are immutable or append-only by design.
4. Given security hardening is implemented, when automated tests run, then they cover portal mutation JWT enforcement, role separation, scope isolation, whitelist rejection, velocity limit rejection, emergency freeze, audit log creation, and sensitive data redaction.

## Tasks / Subtasks

- [x] Task 1: Harden portal mutation JWT/session and authorization gates before mutation (AC: 1, 4)
  - [x] Verify `/dealer`, `/merchant`, and `/admin` POST routes remain behind `middleware.PortalMutationJWT()` in `api/routes/routes.go`; add route/source-contract coverage so future route additions cannot bypass it.
  - [x] Ensure server-rendered admin/dealer forms that POST include a valid `_portal_jwt` field or a supported `X-Portal-JWT` path; do not rely on cookies alone for unsafe methods.
  - [x] Add focused middleware/route tests for safe method token issuance, unsafe POST rejection without token, session-bound token mismatch rejection, and successful POST with valid token.
  - [x] Keep `RequireAdmin` active-account verification as the authoritative admin session boundary; do not trust `requireAdmin` unless `RequireAdmin` has already populated `admin_session_email`.
  - [x] Add or extend an admin role/permission guard for high-risk actions. At minimum, approval/recover/security-policy mutations must reject non-privileged admins before resource lookup or state mutation.
  - [x] Preserve generic failure messages for unauthorized, wrong-scope, missing-resource, and policy-denied paths. Do not reveal whether another tenant/domain owns a wallet, payout, refund, webhook delivery, or policy record.

- [x] Task 2: Add outbound security policy storage and enforcement boundary (AC: 2, 4)
  - [x] Add narrowly scoped models/repositories for outbound policy state instead of scattering env-only checks through handlers. Suggested tables: outbound policy settings and outbound address whitelist entries.
  - [x] Policy settings must support default-off behavior for address whitelist requirement, per-tenant amount limit, rolling velocity window limit, and emergency freeze. Emergency freeze must block signing/broadcast for withdrawal, payout/recover, refund, and sweep-relevant admin outbound paths.
  - [x] Whitelist entries must be scoped by merchant/domain where applicable plus chain/token/address, with normalized address comparison suitable for the chain family. Do not require a live chain call for whitelist validation.
  - [x] Velocity and per-tenant limits must use ledger/lifecycle persisted data, not live chain balances or transaction-row sums. Use raw integer strings and existing chain/token metadata.
  - [x] Enforce policy before any signing/broadcast callback. For pending request creation, fail before `CreateWithHold` where possible; for approval/recover/refund paths, fail before `ExecuteReservedWalletTransfer`.
  - [x] Policy denials must leave existing holds safe: pre-broadcast denials can fail/reject and void according to the current repository semantics; post-broadcast states must not be blindly voided or retried.
  - [x] Add repository tests for policy lookup precedence, whitelist match/miss, freeze blocking, limit math, and duplicate-safe policy/whitelist persistence.

- [x] Task 3: Extend append-only audit evidence for high-risk decisions (AC: 2, 3, 4)
  - [x] Extend `models.ActivityLog` and `repositories.ActivityLogRepo` only as needed to capture structured scope and decision evidence: domain id, actor role, decision, reason, before status, after status, and correlation id.
  - [x] Make activity logs append-only by design. Prefer model hooks or repository constraints that reject update/delete attempts, plus tests proving audit records cannot be mutated or deleted through the repository/ORM path.
  - [x] Replace free-form-only audit calls for high-risk paths with a helper that records before/after status and policy decision without leaking secrets or raw signatures.
  - [x] Cover these events at minimum: dealer withdrawal create, admin withdrawal approve/reject, admin refund approve/reject, admin recover funds, domain API secret rotation, webhook replay, admin account/TOTP changes, emergency freeze changes, whitelist changes, and policy limit changes.
  - [x] Ensure audit records include request path, method, IP, user agent, and `middleware.RequestIDFromCtx(c)` correlation id as `logDealerActivity` currently does.
  - [x] Add redaction tests so API secrets, webhook secrets, raw signatures, mnemonics, private keys, raw signed transactions, and unbounded diagnostic blobs are never written to audit descriptions or structured metadata.

- [x] Task 4: Surface operator controls without weakening UX or security (AC: 1, 2, 3)
  - [x] Add admin security/policy controls to the existing server-rendered admin dashboard; keep the work-focused table/detail-panel style from `views/dealer/admin_dashboard.html`.
  - [x] Show emergency freeze state, whitelist entries, policy limits, actor, scope, timestamp, and last decision near the controls. Do not hide audit context behind hover-only UI.
  - [x] Add confirmation affordances for destructive/high-risk policy toggles; use existing button/input styles and status colors, not accent color as success/warning/danger.
  - [x] Do not display secrets after rotation. Domain API secret rotation may return the new secret once as it does today; audit output and activity tables must stay redacted.
  - [x] Update CSS/JS only if needed for the admin security controls; avoid SPA framework or decorative redesign.

- [x] Task 5: Keep schemas, docs, and production-readiness evidence synchronized (AC: 1, 2, 3, 4)
  - [x] Register any new model in `services/database.autoMigrateModels`.
  - [x] Add required columns/indexes to `services/database.VerifySchema` and matching schema tests.
  - [x] Update `docs/integration-guide.md`, Swagger docs if public/admin behavior is documented there, and readiness/audit docs if the launch posture changes.
  - [x] Add readiness checks for production policy posture if implemented as required launch gates: portal JWT secret, emergency freeze visibility, privileged admin role coverage, and outbound policy configuration.

- [x] Task 6: Add focused validation and update story records (AC: 1, 2, 3, 4)
  - [x] Add handler/source-contract tests for portal JWT coverage, role separation, no resource existence leakage, policy guard placement before signing, and scoped audit calls.
  - [x] Add repository tests for policy, whitelist, velocity/tenant limits, audit immutability, and schema registration.
  - [x] Add view/template tests for admin security controls and audit metadata visibility.
  - [x] Run targeted validation: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./api/middleware ./api/handlers ./repositories ./services/database`.
  - [x] Run full validation: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./...`.
  - [x] Run static validation: `GOCACHE=/tmp/gateway-gocache-bmad go vet -p=1 ./...`.
  - [x] Run whitespace validation: `git diff --check && git diff --cached --check`.
  - [x] Update Dev Agent Record, Completion Notes, File List, Change Log, and move the story only to `review` after validation passes.

## Dev Notes

### Current Implementation Snapshot

- Portal mutation JWT protection exists in `api/middleware/portal_jwt.go` and is applied to `/dealer`, `/merchant`, and `/admin` in `api/routes/routes.go`. It issues session-bound portal JWTs on safe methods and verifies `_portal_jwt` form field or `X-Portal-JWT` header on unsafe methods. Current test coverage is readiness-oriented; add actual middleware/route behavior tests.
- Admin authentication currently uses `models.Admin` with `Email`, bcrypt `Password`, `TOTPSecret`, `TOTPEnabled`, and `IsActive`. `RequireAdmin` verifies the signed session and active admin via `AdminRepo`, then stores `admin_session_email` in Fiber locals. `requireAdmin` only reads that local.
- Admin model currently has no role field. Story 4.6 should add role/permission semantics narrowly enough to protect high-risk actions without building a full identity service.
- `models.ActivityLog` already records merchant id, actor type/email, event, status, subject type/id, description, IP, user agent, method, path, correlation id, and created timestamp. It does not currently store domain id, actor role, before/after status, structured decision/reason, or immutability hooks.
- `logDealerActivity` centralizes current audit writes in `api/handlers/dealer.go`; it already captures request id via `middleware.RequestIDFromCtx(c)` and client request metadata. Extend or wrap this helper rather than duplicating audit construction across handlers.
- High-risk existing handler paths include `HandleDealerWithdrawalCreate`, `HandleAdminWithdrawalApprove`, `HandleAdminWithdrawalReject`, `HandleAdminRefundApprove`, `HandleAdminRefundReject`, `HandleAdminRecoverFunds`, `HandleDealerDomainRotateAPISecret`, admin account/TOTP handlers, webhook replay, rescan, and test deposit.
- Outbound lifecycle from Story 4.5 separates requested, broadcast, finality, and failed states. Do not collapse approval back into terminal finalization. Policy checks must preserve Story 4.5 broadcast/finality and reconciliation behavior.
- Maker-checker separation currently exists as `requireOutboundMakerChecker`, default-off through env flags. This is not a complete role/dual-approval policy; Story 4.6 should harden but not break existing default-off development behavior unless tests/config explicitly enable it.
- No outbound whitelist, velocity limit, per-tenant limit, or emergency freeze model/repository currently exists.
- `services/database.Migrate` still uses GORM `AutoMigrate` outside production and `VerifySchema` in production mode. Any new schema must be added to both `autoMigrateModels` and schema verification tests.

### Architecture And Product Guardrails

- FR39 requires portal JWT audit, role separation, key rotation policy, IP/device/session controls, immutable audit trail, address whitelist, velocity limits, dual approval, and emergency freeze behavior.
- NFR13 requires admin, signer, withdrawal approval, replay, and recovery actions to be traceable through immutable audit logs.
- NFR18 requires tenant isolation, per-tenant limits/quotas, audit isolation, and strengthened security policy.
- AD-10 says ownership is tenant/domain even if code still exposes merchant/domain. New policy and audit records must carry merchant/domain scope where known and must not assume global-only controls.
- AD-11 says uncertain money state opens scoped reconciliation. Policy denials before broadcast are normal business denials; post-broadcast uncertainty must not be turned into a retry or blind void.
- AD-12 says real-funds production needs signer audit logs, withdrawal approval audit logs, operational gates, and reconciliation dashboards before production custody claims.
- UX design requires dense operator screens, explicit audit rows with timestamp/actor/scope/action/outcome/correlation id, redaction of secrets, and status colors for money/security state.

### Previous Story Intelligence

- Story 4.1 made ledger holds mandatory before outbound signing and established pre-broadcast hold release versus post-broadcast preservation.
- Story 4.2 added signer audit context and production software-signer guardrails. Do not log signer secrets or raw signatures while adding audit detail.
- Story 4.3 added chain resource reservation and broadcast-uncertain handling. Do not add policy logic that causes a second spend on retry.
- Story 4.4 added durable sweep jobs, dead-letter/reconciliation behavior, and validation discipline.
- Story 4.5 added idempotent V1 payout/refund create, source-wallet refund metadata, broadcast-only admin approval, finality-gated terminal processing, lifecycle events, and post-review fixes for lost event reconciliation and failed terminal evidence.
- Deferred work remains: post-broadcast ledger failures have a broader reconciliation gap in `repositories/withdrawal_request_repo.go`. Do not widen that gap; if touched, open scoped reconciliation rather than silently swallowing uncertainty.

### Implementation Boundaries

- Do not add Redis, Kafka/NATS/SQS, external policy engines, a new service boundary, or a SPA frontend.
- Do not implement AML/KYT/sanctions/travel-rule screening in this story. Those remain compliance-scope decisions.
- Do not replace existing admin login, OIDC, or TOTP flows unless the change is needed to enforce roles or audit high-risk actions.
- Do not move balance authority out of ledger. Velocity/limit calculations must use ledger/lifecycle persisted state.
- Do not use live chain balance for policy decisions. Live balance is an operator diagnostic, not policy authority.
- Do not store secrets, private keys, mnemonics, raw signatures, raw signed transactions, webhook secrets, API secrets, or unbounded payloads in audit logs or policy metadata.
- Keep default behavior safe for development: policies that are "where configured" should be default-off unless the story adds an explicit production readiness failure for missing policy.

### Likely Files To Touch

- `models/activity_log.go`
- `models/admin.go`
- `models/outbound_policy.go` or similarly named new policy model file
- `repositories/activity_log_repo.go`
- `repositories/admin_repo.go`
- `repositories/outbound_policy_repo.go` or similarly named new policy repo file
- `api/middleware/portal_jwt.go`
- `api/middleware/*_test.go`
- `api/handlers/dealer.go`
- `api/handlers/dealer_test.go`
- `api/handlers/v1_readiness.go`
- `api/handlers/v1_readiness_test.go`
- `api/routes/routes.go`
- `services/database/database.go`
- `services/database/database_test.go`
- `views/dealer/admin_dashboard.html`
- `views/assets/admin.css`
- `docs/integration-guide.md`
- `docs/docs.go`, `docs/swagger.json`, `docs/swagger.yaml` only if public/API docs change
- `_bmad-output/implementation-artifacts/4-6-harden-admin-security-limits-whitelists-and-audit-trails.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

### Testing Requirements

- Portal JWT tests should exercise the real `PortalMutationJWT` middleware behavior, not only readiness env checks.
- Authorization tests should prove a non-privileged active admin cannot approve/reject/recover/mutate policy and cannot infer resource ownership through error differences.
- Policy tests should cover configured-off behavior, configured-on denial, whitelist normalization, chain/token scoping, freeze blocking, per-tenant amount limits, rolling velocity windows, and audit on denial.
- Audit tests should verify structured fields, before/after statuses, correlation id, redaction, and immutability. If using GORM hooks for immutability, test direct `db.Save`, `db.Delete`, and repository paths.
- Handler/source-contract tests may continue using existing string-contract patterns where useful, but money/security behavior also needs executable tests for policy and portal JWT.
- View tests should parse/render the admin security panel and verify visible freeze/policy/audit context.

### Latest Technical Context

- No new external libraries are required. Use current project stack from `go.mod`: Go `1.25.4`, Gofiber `v3.3.0`, `github.com/gofiber/template/html/v3 v3.0.4`, GORM `v1.31.1`, PostgreSQL driver `v1.6.0`, `github.com/pquerna/otp v1.5.0`, and `golang.org/x/crypto v0.51.0`.
- Do not introduce an authorization framework or legacy form-token library. The current local portal JWT middleware is small, tested, and already wired into portal routes.

### References

- Story source: `_bmad-output/planning-artifacts/epics.md` Story 4.6.
- PRD FR39, NFR13, NFR18: `_bmad-output/planning-artifacts/prds/prd-gateway-2026-06-27/prd.md`.
- Architecture AD-10, AD-11, AD-12 and consistency conventions: `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md`.
- UX audit row and admin surface guidance: `_bmad-output/planning-artifacts/ux-designs/ux-gateway-2026-06-27/DESIGN.md`.
- Current project rules: `_bmad-output/project-context.md`.
- Previous story: `_bmad-output/implementation-artifacts/4-5-complete-withdrawal-payout-and-refund-lifecycle-events.md`.
- Deferred work tracker: `_bmad-output/implementation-artifacts/deferred-work.md`.

## Dev Agent Record

### Agent Model Used

Codex

### Debug Log References

- 2026-06-28: Story created from Epic 4.6 after Story 4.5 reached done. Existing portal JWT middleware, admin session/TOTP model, activity log model/repo, admin routes, outbound approval/recover/refund handlers, database schema verification, UX audit guidance, and previous Epic 4 story records inspected.
- 2026-06-28: Implemented and validated Story 4.6 hardening. User corrected the portal mutation protection direction from legacy form-token terminology to portal mutation JWT; active story text and runtime references were updated to `PortalMutationJWT`, `_portal_jwt`, `X-Portal-JWT`, and `PORTAL_JWT_SECRET`.

### Completion Notes List

- Added portal mutation JWT coverage/contract validation, privileged admin role gates before high-risk resource lookup, outbound policy and whitelist enforcement coverage, append-only/redacted audit evidence, admin security UI evidence, schema coverage, and Trust Wallet Core fallback-safe full regression behavior.
- Validation passed: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./api/middleware ./api/routes ./api/handlers ./repositories ./services/database`.
- Validation passed: `GOCACHE=/tmp/gateway-gocache-bmad go test -p=1 -count=1 ./...`.
- Validation passed: `GOCACHE=/tmp/gateway-gocache-bmad go vet -p=1 ./...`.
- Validation passed: `git diff --check && git diff --cached --check`.
- Cleanup validation passed: no legacy form-token references remain in app docs, views, handlers, routes, or this story artifact.

### File List

- `.gitmodules`
- `CLAUDE.md`
- `_bmad-output/implementation-artifacts/4-6-harden-admin-security-limits-whitelists-and-audit-trails.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `api/handlers/dealer.go`
- `api/handlers/dealer_test.go`
- `blockchain/basechain_test.go`
- `blockchain/chains/bitcoin_transfer_test.go`
- `blockchain/chains/evm_transfer_test.go`
- `blockchain/walletcore/provider_test.go`
- `docs/integration-guide.md`
- `docs/payment-gateway-wallet-provider-audit.md`
- `models/activity_log.go`
- `models/outbound_policy.go`
- `readme.md`
- `repositories/outbound_policy_repo.go`
- `repositories/outbound_policy_repo_test.go`
- `scripts/build_wallet_core.sh`
- `services/database/database_test.go`
- `third_party/trustwallet/README.md`
- `views/dealer/admin_dashboard.html`
- `views_test.go`

### Change Log

- 2026-06-28: Created ready-for-dev Story 4.6 with admin portal JWT/session hardening, role/permission guardrails, outbound policy/whitelist/limit/freeze scope, append-only audit requirements, UI/docs/schema guidance, and validation plan.
- 2026-06-28: Completed implementation and moved to review after targeted tests, full regression, vet, and whitespace validation passed.
