---
story_id: "1.4"
story_key: "1-4-hosted-checkout-payment-state-experience"
epic: "Epic 1: Partner Integration & Payment Intake Hardening"
status: done
created: 2026-06-27
updated: 2026-06-27
baseline_commit: fa0750bf7ffe6d66636757023fdc689663b6b1ed
---

# Story 1.4: Hosted Checkout Payment State Experience

Status: done

## Story

Bir payer olarak,
hosted checkout sayfasinda odeme talimatlarini ve durum degisimlerini net gormek istiyorum,
boylece crypto odemeyi karisiklik yasamadan tamamlayabilir ve odemenin pending, paid, expired, failed veya underpaid oldugunu anlayabilirim.

## Requirements Trace

- **FRs:** FR2, FR5, FR6, FR14, FR16
- **NFRs:** NFR11, NFR14, NFR15
- **PRD:** `_bmad-output/planning-artifacts/prds/prd-gateway-2026-06-27/prd.md`
- **UX:** `_bmad-output/planning-artifacts/ux-designs/ux-gateway-2026-06-27/EXPERIENCE.md`
- **Architecture:** `_bmad-output/planning-artifacts/architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md`
- **Project Context:** `_bmad-output/project-context.md`

## Acceptance Criteria

1. Given payer opens valid checkout URL, when payment session active, checkout displays selected asset, chain/network, expected amount, deposit address, QR code, expiry info, and displayed amount/address matches payment session contract.
2. Given desktop/mobile view, when page renders, payment instructions, QR code, address copy action, amount, status usable without layout overlap; mobile rendering covered by regression/view tests where feasible.
3. Given session waiting for chain detection/finality, when status refreshes through polling/websocket/existing status, checkout shows pending/confirming and does not show paid before required settlement/finality state.
4. Given session succeeds, when status refreshed, checkout shows paid/succeeded and final state remains stable across refresh.
5. Given session expires before settlement, when payer opens/refreshes checkout, checkout shows expired and prevents interpreting invoice as still payable.
6. Given detected amount below expected or fails matching policy, when status refreshed, checkout shows underpaid or failed according to policy, distinct from pending/paid.
7. Automated tests cover active, pending/confirming, paid, expired, failed, underpaid states and verify checkout does not expose secrets/internal diagnostics.

## Tasks / Subtasks

- [x] Task 1: Normalize checkout state model and safe response contract (AC: 3, 4, 5, 6, 7)
  - [x] Add or reuse a single helper for payer-facing checkout states: `active`, `pending`, `confirming`, `paid`, `expired`, `failed`, `underpaid`, plus `canceled` only where existing cancel flow needs it.
  - [x] Map persisted session states and existing realtime/status payloads consistently; expired must be reported lazily when `ExpiresAt` is past and the session is not terminal paid/canceled/failed.
  - [x] Do not show `paid` until the existing persisted settlement/finality state says paid. Chain detection or transaction presence alone must remain pending/confirming.
  - [x] Ensure status JSON and websocket payload expose only safe fields required by checkout UX, such as status, paid flag, payment id, safe tx hash, success/cancel paths, and never secrets/internal diagnostics.
  - [x] If underpaid is not yet a persisted model state, add the smallest explicit mapping needed for checkout display/tests without inventing settlement rules that belong to later deposit/ledger stories.

- [x] Task 2: Render active payment instructions without layout or contract drift (AC: 1, 2)
  - [x] `views/gateway/pay.html` must display selected asset, chain/network, expected amount, deposit address, QR code, expiry timer, address copy action, exact amount copy action, and current status.
  - [x] Displayed amount and address must come from the persisted `PaymentSession` contract (`ExpectedAmountRaw`/selected decimals/symbol and `DepositAddress`), not from a recalculated quote.
  - [x] Keep asset selection in `views/gateway/checkout.html` for sessions that do not have a selected asset yet; do not introduce a SPA or new frontend stack.
  - [x] Mobile rendering must stack QR/address/actions predictably and avoid text overlap for long addresses, long order ids, and status copy.
  - [x] Avoid large explanatory feature text; payer copy should be short and factual: "Waiting for payment", "Payment confirming", "Paid", "Expired", "Underpaid", "Failed".

- [x] Task 3: Make refresh, websocket, and terminal transitions consistent (AC: 3, 4, 5, 6)
  - [x] `HandleCheckoutStatus` must return the same payer-facing status helper result as the websocket initial event and any rendered state.
  - [x] Active sessions must remain payable and show waiting/pending copy.
  - [x] Confirming/finality-wait sessions must show confirming copy and must not redirect to success.
  - [x] Paid sessions must redirect/show success and remain stable across refresh.
  - [x] Expired sessions must show terminal expired copy, disable or hide payable affordances where feasible, and must not silently create a new invoice.
  - [x] Failed and underpaid states must be distinct from paid and pending in both JSON and UI copy.

- [x] Task 4: Preserve payment boundaries and existing behavior (AC: 3, 4, 5, 6, 7)
  - [x] Do not add ledger mutation, deposit matching, finality calculation, or chain-indexer state mutation in this story.
  - [x] Keep `PaymentRepo`, `models.PaymentSession`, `services/realtime`, and existing handler boundaries; do not bypass repositories for normal session updates.
  - [x] Keep cancel behavior compatible with current checkout cancel flow and webhook delivery.
  - [x] Keep Story 1.1 signed v1 auth, Story 1.2 idempotent payment create/selected asset contract, and Story 1.3 static wallet issuance behavior passing.

- [x] Task 5: Add checkout state tests and update docs if public contract changes (AC: 1, 2, 3, 4, 5, 6, 7)
  - [x] Add focused handler/helper tests for active, pending/confirming, paid, expired, failed, and underpaid checkout states.
  - [x] Add regression assertions that checkout HTML includes asset/network, amount, address, QR path, expiry/status areas, copy actions, and does not include secret/internal diagnostic fields.
  - [x] Add mobile/layout-feasible regression coverage through template/static assertions; use Playwright only if the current project test setup already supports it without new heavy infrastructure.
  - [x] If status JSON/websocket/OpenAPI-visible response fields change, update relevant structs/comments and regenerate Swagger artifacts.
  - [x] Targeted validation: `go test -count=1 ./api/handlers ./services/realtime ./types`.
  - [x] Full validation: `go test -count=1 ./...`.
  - [x] Static validation: `go vet ./...`.
  - [x] Update Dev Agent Record, Completion Notes, File List, Change Log, and set story status according to `bmad-dev-story`.

### Review Findings

- [x] [Review][Patch] Preserve underpaid terminal state on cancel [repositories/payment_repo.go:343] - Direct checkout cancel could overwrite an `underpaid` terminal payment as `canceled`; fixed with a shared cancel-terminal guard and repository regression coverage.
- [x] [Review][Patch] Populate terminal/payable flags on realtime broadcast updates [main.go:701] - Paid websocket broadcasts did not include the new `terminal`/`payable` payload flags; fixed with a tested broadcast event helper.

## Dev Notes

### Current Implementation Snapshot

- `api/handlers/payment.go` owns hosted checkout flows:
  - `HandleCheckout` renders asset selection or redirects selected/paid sessions.
  - `HandleCheckoutSelectAsset` validates selected asset, computes quote snapshot, persists selected chain/symbol/token/decimals/expected amount/deposit address, then redirects to `/pay`.
  - `HandleCheckoutPay` renders `views/gateway/pay.html` with `Session`, `QRCodeURL`, `PaymentURI`, `ChainName`, `ChainLogoURL`, `AmountDisplay`, and expiry/product fields.
  - `HandleCheckoutQRCode` builds the QR payload from `paymentURI` or deposit address.
  - `HandleCheckoutStatus` currently returns JSON with `success`, `status`, `paid`, `success_path`, and `cancel_path`.
  - `HandleCheckoutSocket` publishes the initial realtime payment event to websocket subscribers.
  - `HandleCheckoutCancel` cancels unpaid sessions and delivers webhook.
  - `HandleCheckoutSuccessReturn` only redirects to the merchant success URL when the session is paid.
- `paymentSessionResponseStatus(session, now)` already lazily reports `expired` for nonterminal sessions when `ExpiresAt` has passed. Reuse or centralize this behavior instead of adding another expiry rule.
- `HandleCheckoutPay` redirects paid sessions to `/return/success`, expired sessions to an error page, and pending unselected sessions back to asset selection.
- `views/gateway/pay.html` already displays amount, order amount/currency, countdown, QR, chain pill, live status panel, address copy, amount copy, payment URI, warning, change asset, and cancel actions. It currently treats `expired`, `canceled`, and `failed` similarly and does not explicitly handle `underpaid`.
- `views/gateway/checkout.html` owns server-rendered asset selection. Keep checkout isolated from authenticated app chrome.
- `models.PaymentSession` stores selected asset and contract fields used by checkout: `SelectedChainID`, `SelectedSymbol`, `SelectedToken`, `SelectedDecimals`, `ExpectedAmountRaw`, `DepositAddress`, `ExpiresAt`, `Status`.
- Existing status constants should be checked before adding new ones. If an `underpaid` status does not exist, decide whether a minimal constant is needed for checkout contract tests or whether an existing failed/matching outcome can map to underpaid.
- `services/realtime` contains the websocket event shape used by checkout status pushes. Keep payloads small and safe.
- Public API docs only need regeneration if public Swagger-visible request/response structs or route comments change.

### Architecture Compliance

- AD-2: Hosted checkout payment state must use the existing Payment Session, Wallet, Webhook, and Money Core boundaries; do not duplicate money movement behavior in the UI.
- AD-3: Ledger remains the balance authority. Checkout can display persisted payment status, but must not infer paid from a chain transaction before finality/settlement gates.
- AD-4: Chain indexer facts must not directly mutate checkout business state from this story.
- AD-5 / FR10: Checkout refresh/status paths must remain duplicate-safe and idempotent from the payer perspective.
- AD-8: Webhook behavior remains boundary-owned; checkout rendering must not leak webhook internals.
- Project context: server-rendered UX must stay compatible with existing `views/`, `static/`, and Tailwind/static CSS setup. Do not introduce a SPA.

### Implementation Guardrails

- Use a single state mapping helper for HTML render, JSON polling, and websocket initial state to avoid drift.
- Preserve the selected asset and quote snapshot from Story 1.2; do not recalculate display amount from current oracle prices on refresh.
- Do not add real deposit matching, ledger entries, finality calculation, or chain indexer writes in this story.
- Do not expose mnemonic, private key, API secret, webhook secret, raw DB diagnostic blobs, stack traces, or internal matching traces in checkout HTML/JSON/websocket payloads.
- Keep existing cancel and expired webhook behavior unless tests show a regression.
- Keep paid terminal state stable across refresh. A paid session must not be downgraded to expired because its `ExpiresAt` is in the past.
- Avoid adding new frontend dependencies unless absolutely necessary for tests already present in the repo.
- Any public contract change must update tests first and Swagger artifacts after implementation.

### Previous Story Intelligence

- Story 1.1 established signed v1 request validation and sensitive value redaction. Checkout changes must not weaken partner auth or error hygiene.
- Story 1.2 created selected asset and quote snapshot persistence. Checkout display must read those persisted fields and not drift from the payment session contract.
- Story 1.2 also added lazy expiry reporting for partner and checkout status surfaces; keep that behavior consistent in payer-facing state helpers.
- Story 1.3 tightened registry and chain validation before wallet mutation. Checkout asset selection must preserve those pre-mutation validation expectations.
- Story 1.3 review caught docs drift and missing validation paths; this story should include explicit tests for UI/status states and docs changes if any public fields move.

### Testing Requirements

- Use standard Go `testing`, Fiber/httptest, and existing handler fake patterns in `api/handlers/*_test.go`.
- Add helper tests for checkout state mapping:
  - selected active/awaiting-payment session reports active or waiting state;
  - pending/confirming state does not set paid or success redirect;
  - paid terminal state remains paid even after expiry time;
  - expired nonterminal state reports expired;
  - failed state reports failed;
  - underpaid state reports underpaid when represented by the current model/mapping.
- Add handler tests for `HandleCheckoutStatus` JSON and websocket initial payload where practical.
- Add render/template tests or HTML assertions for active checkout instructions:
  - selected asset and chain/network are present;
  - exact amount and deposit address match `PaymentSession`;
  - QR path or image URL is present;
  - copy actions are present;
  - expiry/status areas are present;
  - no secret/internal diagnostic strings are included.
- Verify mobile/layout-feasible behavior through stable class/structure assertions if no browser test harness exists.
- Targeted commands: `go test -count=1 ./api/handlers ./services/realtime ./types`.
- Full commands: `go test -count=1 ./...` and `go vet ./...`.

### Relevant Files

- Update likely:
  - `api/handlers/payment.go`
  - `views/gateway/pay.html`
  - `static/assets/checkout.js` or existing checkout static file if status JS is shared there
  - `services/realtime/*`
  - `models/payment_session.go`
  - `api/handlers/payment_test.go`
- Update if public contract changes:
  - `types/payment.go`
  - `types/v1api.go`
  - `docs/docs.go`
  - `docs/swagger.json`
  - `docs/swagger.yaml`
- Read before modifying:
  - `views/gateway/checkout.html`
  - `views/gateway/payment_result.html`
  - `repositories/payment_repo.go`
  - `_bmad-output/implementation-artifacts/1-2-idempotent-payment-session-creation.md`
  - `_bmad-output/implementation-artifacts/1-3-deterministic-static-wallet-issuance-for-partner-scopes.md`

## Project Structure Notes

- Hosted checkout remains server-rendered under `views/gateway/` with supporting static assets under the existing static asset tree.
- Payment session lifecycle remains in `api/handlers/payment.go`, `repositories/payment_repo.go`, and `models/payment_session.go`.
- Realtime status remains under `services/realtime`; do not create a separate websocket stack.
- Tests should stay close to existing handler/helper test files unless repository-level changes require their own package tests.

## Dev Agent Record

### Agent Model Used

Codex

### Debug Log References

- Red phase: `go test -count=1 ./api/handlers` failed on missing checkout state helper/status constants.
- Targeted validation: `go test -count=1 ./api/handlers ./services/realtime ./types`
- Review patch validation: `go test -count=1 ./repositories`
- Review patch validation: `go test -count=1 ./api/handlers ./services/realtime ./types`
- Review patch validation: `go test -count=1 -run TestPaymentRealtimeBroadcastEventMarksPaidTerminal ./`
- Swagger regeneration: `swag init -g main.go -o docs`
- Full validation: `go test -count=1 ./...`
- Static validation: `go vet ./...`
- Whitespace validation: `git diff --check`

### Completion Notes List

- Added one payer-facing checkout state helper that maps persisted payment sessions to `active`, `pending`, `confirming`, `paid`, `expired`, `canceled`, `failed`, and `underpaid` without adding deposit matching, ledger mutation, finality calculation, or chain-indexer writes.
- Updated checkout status JSON and websocket initial events to use the same helper and expose only safe fields: status, paid flag, payment id, tx hash, success/cancel paths, payable, and terminal flags.
- Updated realtime broadcast events to emit payer-facing `active`/`confirming` states plus payable/terminal flags for live checkout subscribers.
- Added a minimal `underpaid` payment status constant for display/contract mapping; no settlement policy was invented in this story.
- Updated hosted pay rendering so active/confirming sessions show the persisted amount/address/QR/copy actions, while terminal states show distinct status copy and do not render payable QR/copy affordances.
- Updated public status response docs and V1 status enum/table for `underpaid`, then regenerated Swagger artifacts.
- Added regression tests for state mapping, status payload safety, realtime event state, pay template contract fields, terminal non-payable rendering, and root view rendering data.
- Code review patch: direct cancel now treats `underpaid` as terminal and cannot overwrite it to `canceled`.
- Code review patch: realtime paid broadcasts now populate `terminal`/`payable` flags consistently with the checkout websocket payload contract.

### File List

- `api/handlers/payment.go`
- `api/handlers/payment_test.go`
- `api/handlers/v1api.go`
- `docs/docs.go`
- `docs/swagger.json`
- `docs/swagger.yaml`
- `main.go`
- `models/payment_session.go`
- `repositories/payment_repo.go`
- `repositories/payment_repo_test.go`
- `services/realtime/payment_hub.go`
- `types/payment.go`
- `views/assets/style.css`
- `views/gateway/pay.html`
- `views_test.go`
- `_bmad-output/implementation-artifacts/1-4-hosted-checkout-payment-state-experience.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

### Change Log

- 2026-06-27: Story created with PRD, UX, architecture, project-context, previous-story, and current-code context.
- 2026-06-27: Implemented hosted checkout payer-facing state helper, safe status/realtime payloads, terminal/non-payable rendering, underpaid contract support, tests, and Swagger docs.
- 2026-06-27: Addressed code review finding - underpaid terminal sessions now block cancel mutation.
- 2026-06-27: Addressed code review finding - realtime broadcast events now include terminal/payable state flags.
