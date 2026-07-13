# Developer Guide

Language: **English** | [Türkçe](developer-guide.tr.md)

This guide explains how to develop Gateway safely. Use it with the main README, the integration guide, and the module-boundary documents when opening a feature branch or reviewing a money-path change.

## First Read

- Start with `README.md` for setup, environment variables, runtime commands, and API overview.
- Read `docs/module-boundaries.md` before changing deposits, payments, ledger, outbound flows, webhooks, reconciliation, listeners, or workers.
- Read `docs/production-migration-discipline.md` before adding or changing persisted state.
- Read `docs/integration-guide.md` before changing `/api/v1` partner API behavior.
- Read `docs/money-event-catalog.md` before adding, renaming, or changing webhook/money events.

## Local Setup

Gateway is a Go modular monolith with PostgreSQL, GORM, Fiber v3, server-rendered HTML, Tailwind CSS, and Trust Wallet Core.

Required local tools:

- Go `1.25.4`
- PostgreSQL
- Node.js and npm
- Git submodule support
- Native build tooling required by Trust Wallet Core, through `scripts/build_wallet_core.sh`

One-time setup:

```bash
git submodule update --init --recursive third_party/trustwallet/wallet-core
go mod download
npm install
./scripts/build_wallet_core.sh
npm run build:css
cp .env.sample .env
```

The Trust Wallet Core submodule is required because `go.mod` replaces `tw` with `./third_party/trustwallet/wallet-core/samples/go`. If `go test`, `go run`, or `go mod download` fails with a missing `tw` replacement directory, initialize the submodule instead of removing the replace directive.

Use development-only secrets and a development-only mnemonic in `.env`. Never commit real keys, production mnemonics, webhook secrets, API secrets, or RPC credentials.

## Development Loop

Initialize or update the local database:

```bash
go run . -migrate
go run . -seed
```

Use `-install` when you want both migration and seed:

```bash
go run . -install
```

Run the app directly:

```bash
go run .
```

Run with Go and HTML auto-reload:

```bash
go install github.com/air-verse/air@latest
npm run dev
```

Open `http://localhost:3000` when using Air. Air proxies to the application port from `.env`, normally `PORT=:3001`. If you change `PORT`, update `.air.toml` `proxy.app_port` as well.

When editing Tailwind source CSS, run this in a second terminal:

```bash
npm run dev:css
```

Build generated/minified CSS before committing view or CSS changes:

```bash
npm run build:css
```

## Runtime Shape

Application startup flows through `main.go`:

- `.env` is loaded.
- The database is initialized from `DATABASE_URL`.
- `routes.NewRouter` builds the Fiber app, repositories, services, chain factory, asset registry, payment hub, portals, `/api/v1`, metrics, Swagger, and docs routes.
- `services/database.Migrate` runs startup migrations outside production, or verifies schema in production.
- The worker supervisor starts periodic workers.
- Address index loading, admin bootstrap, chain infrastructure, and the HTTP server start.

The main composition root is `application.CORE`. New shared dependencies should be wired through the composition root and `api/routes/routes.go` instead of hidden package globals.

## Code Ownership

Use the existing package boundaries:

| Area | Where to change |
| --- | --- |
| HTTP routing | `api/routes/routes.go`, `constants/commands.go` for legacy command paths |
| HTTP handlers | `api/handlers/` |
| Request/response DTOs and validation | `types/` |
| Business services | `services/` |
| Database access | `repositories/` |
| GORM models | `models/` |
| Chain configuration | `constants/chains.go`, `application/configuration/chains.go`, `blockchain/chains/` |
| Asset/token configuration | `application/configuration/assets.go`, `asset/` |
| Periodic/background work | `workers/`, `services/*`, `workers/supervisor` |
| Merchant/admin/checkout UI | `views/`, `views/assets/` |
| OpenAPI output | `docs/swagger.yaml`, `docs/swagger.json`, `docs/docs.go` |

Money-path changes must preserve the ownership rules in `docs/module-boundaries.md`. Chain listeners record facts; they do not directly mutate payments, ledger balances, webhooks, or sweeps. Deposit, payment, ledger, outbound, webhook, and reconciliation changes should cross boundaries through services, repositories, worker commands, or money events.

## Common Changes

### Add or Change a V1 API Endpoint

1. Add or update DTOs and validation in `types/`.
2. Implement handler behavior in `api/handlers/`.
3. Register the route in `api/routes/routes.go`.
4. Decide whether the endpoint is read-only API-key access or mutating HMAC-signed access.
5. Add scope, IP allowlist, idempotency, and replay protection behavior when the endpoint creates money movement or durable operations.
6. Update Swagger annotations and regenerate docs:

```bash
swag init -g main.go -o docs
```

7. Update `docs/integration-guide.md` when partner-facing behavior changes.
8. Add focused handler, auth, repository, and contract tests.

### Add or Change a Database Model

1. Add or update the GORM model under `models/`.
2. Add repository methods under `repositories/`; keep query and transaction behavior out of handlers.
3. Register the model in `services/database.autoMigrateModels`.
4. Add schema verification coverage in `services/database.VerifySchema` helpers and tests.
5. Add a versioned artifact in `services/dbmigrations/migrations.go` with summary, forward plan, lock impact, backfill, rollback, verification query, and verification command.
6. Update `docs/production-migration-discipline.md` when the artifact list changes.
7. Add repository tests for idempotency, locking, conflicts, and lifecycle transitions.

Production schema changes are not startup-only `AutoMigrate` changes. Keep production rollout, backfill, verification, and rollback evidence explicit.

### Add a Worker

1. Put pure work in a service with a `RunOnce`-style method when possible.
2. Make loops accept `context.Context` and stop promptly on cancellation.
3. Register lifecycle ownership with `workers/supervisor`.
4. Use `WorkerLeaseRepo` when only one process should own the job.
5. Persist progress before acknowledging external or cross-module work.
6. Add tests for cancellation, retry, idempotency, lock expiry, and partial failure recovery.

### Add a Chain

1. Add the chain ID, name, logo slug, and testnet classification in `constants/chains.go`.
2. Implement the chain in `blockchain/chains/` and satisfy the shared chain interface.
3. Register it in `application/configuration/chains.go`, including aliases.
4. Add RPC env handling and listener start-block policy support when the chain has a listener.
5. Add native asset and token deployments in `application/configuration/assets.go`.
6. Add route/API/readiness/test coverage for chain discovery and address validation.
7. Add static chain/coin assets when the UI needs logos.

### Add an Asset or Token Deployment

1. Add or update the `asset.AssetDefinition` in `application/configuration/assets.go`.
2. Use the correct chain ID, contract address or mint, decimals, native flag, symbol, and enabled flag.
3. Add aliases only when the project intentionally treats wrapped or alternate symbols as a canonical asset.
4. Add price fallback env support when CoinGecko does not cover the symbol.
5. Add or update asset registry tests when the deployment affects grouping, selection, logos, or API output.

### Change Webhooks or Money Events

1. Update the catalog in `services/webhook/event_catalog.go`.
2. Update `docs/money-event-catalog.md`.
3. Preserve event ID stability and idempotency.
4. Keep webhook delivery at-least-once and retry-safe.
5. Add contract tests for event shape, ordering, replay, and diagnostic behavior.

### Change Merchant/Admin/Checkout Views

1. Edit templates in `views/`.
2. Edit CSS/JS in `views/assets/`.
3. Run `npm run build:css` when Tailwind input changes.
4. Add or update view rendering tests in `views_test.go` or handler tests.
5. Verify checkout and portal routes in development mode so HTML reload and no-cache static assets are exercised.

## Testing

Run focused tests while developing:

```bash
go test ./services/deposits ./repositories -run TestName
```

Run the full Go suite:

```bash
go test ./...
```

Run fallback-mode regression when native Trust Wallet Core is not available or when you need the CI-style isolated wallet mode:

```bash
GATEWAY_REGRESSION_MODE=fallback ./scripts/regression.sh
```

Run native regression before changes that touch wallet generation, signing, address validation, chain implementations, or production-critical money movement:

```bash
./scripts/regression.sh
```

Some repository and integration tests need PostgreSQL and skip unless a DSN is provided. Use a disposable database:

```bash
TEST_DATABASE_URL="host=127.0.0.1 port=5432 user=postgres password=postgres dbname=gateway_test sslmode=disable" go test ./repositories ./services/database ./workers/indexer ./services/txrescan
```

Run Go vet through the regression script or directly:

```bash
go vet ./...
```

## Safety Rules

- Do not run the server with `-tags walletcorefallback`; `main.go` rejects fallback server runs unless explicitly overridden for narrow debugging.
- Do not use `SIGNER_MODE=software` for production custody. Production signing must remain behind an external signer mode such as KMS, HSM, MPC, or Vault.
- Do not add money-path state changes directly in chain listeners.
- Do not write cross-module table updates from handlers just because a repository is reachable.
- Do not bypass idempotency for payment, payout, refund, sweep, webhook, or ledger work.
- Do not weaken HMAC signing, timestamp checks, API scopes, IP allowlist behavior, or replay protection for mutating partner API routes.
- Do not commit `.env`, private keys, mnemonics, API secrets, webhook secrets, production RPC credentials, or generated local database dumps.

## Pull Request Checklist

- Relevant docs are updated: README, integration guide, money event catalog, runbook, or migration discipline as needed.
- New persisted state is registered in model, migration artifact, schema verification, and repository tests.
- New partner behavior has API contract and Swagger updates.
- New worker behavior is cancellable, idempotent, observable, and supervisor-owned.
- Money-path changes respect module ownership boundaries.
- CSS changes have rebuilt `views/assets/tailwind.css`.
- Tests include the narrow package suite plus broader regression where the change has cross-module impact.
