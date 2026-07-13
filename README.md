# Gateway - Multi-Chain Crypto Payment Gateway and Wallet Provider Infrastructure

Language: **English** | [Türkçe](README.tr.md)

Gateway is a Go-based **B2B crypto payment gateway**, **multi-chain cryptocurrency payment processor**, and **wallet-provider infrastructure platform**. It helps merchants accept crypto payments, lets developers generate user-level multi-chain wallet addresses, and gives wallet or exchange-style products a shared money core for deposits, payments, payouts, refunds, sweeps, webhooks, ledgers, and reconciliation.

This project is not just a hosted checkout page. It combines hosted checkout, static deposit addresses, a merchant API, HMAC-signed webhooks, blockchain listeners, ledger-backed balances, finality gates, sweep operations, refund and payout workflows, provider health checks, and production readiness controls in one Go application. The stack is built with Go Fiber v3, GORM, PostgreSQL, Trust Wallet Core, Tailwind CSS, and server-rendered merchant, admin, invoice, and checkout views.

Primary search intent covered by this README: `crypto payment gateway`, `multi-chain payment gateway`, `cryptocurrency payment processor`, `wallet provider API`, `merchant crypto checkout`, `static deposit address`, `blockchain payment gateway`, `HMAC webhook payment API`, `Go payment gateway`, `PostgreSQL ledger reconciliation`.

## What Does Gateway Do?

Gateway is designed for merchant, wallet, and exchange-style teams that need crypto payment infrastructure without building every chain, wallet, listener, ledger, and webhook component from scratch.

- Provides hosted checkout, invoice/payment links, static deposit addresses, and a `/api/v1` merchant API.
- Brings together address generation, address validation, transfer/sweep preparation, and listener infrastructure for Bitcoin, Ethereum, TRON, Solana, Avalanche, BNB Chain, Base, Arbitrum, Unichain, and Chiliz.
- Hardens the integration contract with API keys, API secrets, HMAC signatures, timestamp checks, permission scopes, IP allowlists, and idempotency.
- Connects on-chain transaction events to durable chain facts, deposit processing, finality gates, ledger posting, payment matching, and a money event outbox.
- Sends signed webhook events to merchant systems and manages failed delivery records with retry, replay, and operator diagnostics surfaces.
- Exposes merchant and admin portals for domains, products, wallets, payments, webhooks, withdrawals, refunds, sweeps, and recovery operations.
- Makes money-movement risk visible through production readiness checks, provider health, migration discipline, signer policy, metrics, and observability controls.

## Problems It Solves

| Problem | Gateway Solution | Result |
| --- | --- | --- |
| Merchants want to accept crypto payments without building a separate checkout, wallet, listener, and webhook stack for every chain. | Hosted checkout, payment sessions, static deposit addresses, merchant API, and a multi-chain asset registry are built into one product. | Merchant integration becomes faster and less error-prone. |
| User-level deposit addresses are risky to generate and track manually. | Trust Wallet Core-based HD wallet/address generation, address lookup, blockchain listeners, and deposit lifecycle services are used together. | User/address matching and payment tracking become systematic. |
| Teams cannot clearly answer whether an on-chain payment arrived, how many confirmations it has, or whether it was underpaid or overpaid. | Payment sessions, expected amounts, asset/chain matching, finality workers, and payment status events are used. | Payment state is visible to merchants, admins, and webhook consumers. |
| Network retries can create duplicate payments for the same order. | `Idempotency-Key` support works with request payload fingerprints. | Client retries become safer and duplicate invoice risk is reduced. |
| Merchant systems can miss money events when webhook callbacks fail. | Durable webhook delivery records, HMAC signatures, retry, replay, and diagnostics surfaces are provided. | Merchant callback delivery can be inspected and replayed. |
| Balances become hard to reconcile when transaction rows are treated as the only source of truth. | Ledger-derived balances, reserve reconciliation jobs, and the money event outbox provide a clearer accounting path. | Balance authority is explicit and drift/recovery work is visible. |
| Withdrawals, refunds, and sweeps carry private key, fee, nonce/UTXO, and operational approval risks. | Signer mode guards, chain resource policy, sweep jobs, refund/payout lifecycle states, and readiness gates separate the risky boundaries. | Controlled beta limits stay clear and production custody claims remain evidence-gated. |
| "Production-ready payment gateway" claims are often vague. | Controlled launch readiness levels, `/api/v1/common/readiness`, `/metrics`, runbooks, and audit documents define the current maturity level. | The line between beta, production gateway, wallet-provider custody, and exchange-grade tracking stays explicit. |

## Who Is It For?

- **Merchant and dealer teams:** Businesses that want to accept crypto, issue checkout links, generate static deposit addresses, and update their own systems through webhooks.
- **Developer integrators:** Teams that need a crypto payment API with API keys, HMAC signing, idempotency, hosted checkout, static wallets, and webhook contracts.
- **Wallet and exchange-style product teams:** Platforms that need user-level multi-chain deposit wallets, ledgers, reconciliation, withdrawals, refunds, sweeps, and provider health in one money core.
- **Operations and security teams:** Teams that need visibility into money movement, webhook backlog, signer readiness, provider lag, migration state, reconciliation drift, and admin recovery.

## Core Features

- Multi-chain HD wallet/address generation and address validation
- Hosted crypto checkout, invoice/payment links, QR codes, and websocket payment status
- Static deposit address API and user-level wallet infrastructure
- Merchant portal for domains, API credentials, products, invoices, withdrawals, and webhooks
- Admin panel for merchants, withdrawals, refunds, sweeps, webhook replay, recovery, and admin users
- `/api/v1` merchant API with API key, bearer token, HMAC request signing, permission scopes, and IP allowlists
- Idempotent payment creation and duplicate request protection
- Blockchain listener workers for Bitcoin, EVM-family chains, Solana, and TRON
- Durable chain facts, deposit fact processing, transaction finality, and payment matching
- Webhook delivery, retry, replay, diagnostics, and signed callback events
- Opt-in x402 v2 seller middleware for static HTTP resources and dynamic hosted checkout payments using exact EVM/Solana schemes
- Ledger, withdrawal request, refund, sweep job, price quote, provider health, and activity log models
- CoinGecko price service integration and custom token price overrides
- Prometheus-compatible `/metrics`, request observability, and readiness endpoints
- Swagger/OpenAPI output and integration documentation

## Supported Networks and Assets

Chain records live in `application/configuration/chains.go`. Asset records live in `application/configuration/assets.go`.

Supported networks:

- Bitcoin
- Ethereum
- TRON and TRON Nile testnet
- Solana
- Avalanche C-Chain
- BNB Chain
- Chiliz and Chiliz Spicy testnet
- Base
- Arbitrum One
- Unichain

Example registered assets:

- Native assets: BTC, ETH, TRX, SOL, AVAX, BNB, CHZ
- Stablecoins and wrapped tokens: USDT, USDC, WBTC, WETH, WBNB, WAVAX, WCHZ, WSOL
- ERC-20, SPL, and TRC-20 token records
- Project-specific registered tokens: TBT, CHZINU, PEPPER, COOLVIBES
- Wrapped token aliases: WBTC -> BTC, WETH -> ETH, WBNB -> BNB, WAVAX -> AVAX, WCHZ -> CHZ, WSOL -> SOL

## Payment Flow

1. A merchant account is created through the merchant portal, admin portal, or internal endpoints.
2. A merchant domain is configured with API key, API secret, webhook URL, webhook secret, and permission scopes.
3. The merchant creates an invoice/payment session through `/api/v1/payment/create`, hosted checkout, or the portal.
4. The customer selects a supported network and asset on the checkout screen.
5. Gateway creates the deposit address, expected amount, QR code, and realtime status channel for the payment session.
6. Blockchain listener workers watch their networks and store on-chain events as durable chain facts.
7. The deposit fact worker matches address ownership, chain, asset, amount, and finality state.
8. When the payment session matches the expected amount and asset, payment status is updated. Underpaid, overpaid, partial paid, or succeeded events can be emitted.
9. Ledger entries, deposit lifecycle, sweep/refund/payout jobs, and money event outbox flows progress idempotently.
10. The webhook notifier sends a signed event to the merchant callback URL. Failed attempts are managed through retry and replay.

## Product Readiness

Gateway is currently positioned for controlled merchant/dealer beta evaluation. Production payment gateway, wallet-provider custody, and exchange-grade tracking claims are gated by separate readiness levels. In particular, production custody must not rely on an in-process software signer. KMS, HSM, MPC, Vault, or an equivalent external signer adapter, reconciliation evidence, compliance scope, and operational runbooks must be completed before holding high-volume customer funds.

Detailed readiness and operating boundaries:

- [Controlled Launch Readiness](docs/controlled-launch-readiness.md)
- [Product Readiness Audit](docs/product-readiness-audit.md)
- [Payment Gateway Wallet Provider Audit](docs/payment-gateway-wallet-provider-audit.md)
- [Integration Guide](docs/integration-guide.md)
- [Money Path Observability Runbook](docs/money-path-observability-runbook.md)
- [Sweep Operations Runbook](docs/sweep-operations-runbook.md)

## Developer Documentation

Developer-focused contribution and extension notes live in [Developer Guide](docs/developer-guide.md). It covers the local development loop, code ownership boundaries, common change checklists, migrations, workers, API changes, asset/chain additions, tests, and PR expectations.

## Project Structure

```text
.
├── api/
│   ├── handlers/          # HTTP handlers for checkout, merchant portal, admin, and v1 API
│   ├── middleware/        # Security headers, observability, CORS, and rate limiting
│   ├── router/            # Action router helpers
│   └── routes/            # Fiber routes and Swagger routes
├── application/
│   └── configuration/     # Chain factory and asset registry configuration
├── asset/                 # Asset types, deployment records, and registry
├── blockchain/            # Chain interface, factory, walletcore provider, and chain implementations
├── constants/             # Command, product, webhook event, and chain constants
├── contracts/             # ERC20 and Multicall bindings
├── docs/                  # Integration, readiness, runbook, audit, and Swagger/OpenAPI output
├── helpers/               # Encryption, credentials, and utility helpers
├── models/                # GORM models
├── repositories/          # Database access layer
├── services/              # Deposit, pricing, realtime, reconciliation, tx rescan, system, and webhook services
├── static/                # Chain and coin images
├── types/                 # Request/response DTOs
├── views/                 # Merchant portal, admin, gateway, and invoice HTML views
└── workers/               # Listeners, dispatcher, supervisor, and address index workers
```

## Requirements

- Go `1.25.4`
- PostgreSQL
- Node.js and npm, used for Tailwind CSS output
- Git submodule support and Trust Wallet Core source code
- Native Trust Wallet Core library build, including CGo, clang/cmake, and `scripts/build_wallet_core.sh`
- RPC access for the networks that should be monitored by chain listeners

## Installation

Clone the repository with the Trust Wallet Core submodule:

```bash
git clone --recurse-submodules <repo-url> gateway
cd gateway
```

If the repository was cloned without submodules or the submodule directory is empty, run:

```bash
git submodule update --init --recursive third_party/trustwallet/wallet-core
```

If submodule URL metadata needs to be refreshed in an existing clone, run:

```bash
git submodule sync --recursive
git submodule update --init --recursive third_party/trustwallet/wallet-core
```

After the submodule is ready, this file should exist:

```bash
test -f third_party/trustwallet/wallet-core/samples/go/go.mod
```

Because `go.mod` contains `replace tw => ./third_party/trustwallet/wallet-core/samples/go`, running `go mod download`, `go test`, or `go run .` before initializing the submodule will fail with:

```text
tw@v0.0.0: replacement directory ./third_party/trustwallet/wallet-core/samples/go does not exist
```

The fix is to initialize the submodule, not to use the fallback:

```bash
git submodule update --init --recursive third_party/trustwallet/wallet-core
```

Trust Wallet Core is required for this project. Mnemonic validation, HD private key derivation, chain address generation, and money movement signing paths depend on Trust Wallet Core. The `walletcorefallback` build tag is only for narrow local debugging and must not be used for payments, withdrawals, refunds, sweeps, or production runs.

Install dependencies and build the native Trust Wallet Core libraries:

```bash
go mod download
npm install
./scripts/build_wallet_core.sh
npm run build:css
```

On macOS, `./scripts/build_wallet_core.sh` runs `tools/install-sys-dependencies-mac`. On Linux, it runs `tools/install-sys-dependencies-linux`. Then it runs Trust Wallet dependency, Rust dependency, native generated file, and CMake build steps. Wallet generation and transfer signing should not be expected to work before this build completes.

Prepare the `.env` file:

```bash
cp .env.sample .env
```

Minimal local `.env` example:

```env
DATABASE_URL="host=127.0.0.1 port=5432 user=postgres password=postgres dbname=gateway sslmode=disable"
PORT=":3001"
APP_ENV=development
GATEWAY_LOG_FORMAT=text
GATEWAY_LOG_LEVEL=info
HTTP_READ_TIMEOUT=15s
HTTP_WRITE_TIMEOUT=30s
HTTP_IDLE_TIMEOUT=60s
CORS_ALLOWED_ORIGINS=http://localhost:3001
ALLOW_PRIVATE_WEBHOOK_URLS=true
ALLOW_AUTOMIGRATE_IN_PRODUCTION=false
SIGNER_MODE=software
ALLOW_SOFTWARE_SIGNER_IN_PRODUCTION=false
METRICS_BEARER_TOKEN=
PROVIDER_HEALTH_INTERVAL=1m
PROVIDER_HEALTH_TIMEOUT=8s
PROVIDER_HEALTH_STALE_LAG_BLOCKS=3
PROVIDER_FAILOVER_STRATEGY=prefer_healthy
MASTER_KEY=32-byte-or-longer-secret
PORTAL_JWT_SECRET=32-byte-or-longer-portal-jwt-secret
MNEMONIC_PHRASE="your bip39 mnemonic phrase"
ADMIN_EMAIL=admin@example.com
ADMIN_PASSWORD=change-this-password
ADMIN_NAME=Gateway Admin
TRON_GRPC_ENDPOINTS=grpc.trongrid.io:50051
TRON_TESTNET_JSONRPC_URLS=https://nile.trongrid.io/jsonrpc
TRON_TESTNET_HTTP_ENDPOINTS=https://nile.trongrid.io
TRON_TESTNET_GRPC_ENDPOINTS=grpc.nile.trongrid.io:50051
TRON_TESTNET_SWEEP_ADDRESS=
```

Run database migrations:

```bash
go run . -migrate
```

Add seed data:

```bash
go run . -seed
```

Run migration and seed together:

```bash
go run . -install
```

Start the application:

```bash
go run .
```

For development with automatic Go and HTML reloads, use Air:

```bash
go install github.com/air-verse/air@latest
npm run dev
```

Air runs the app on the `PORT=:3001` value from `.env`. Open the browser through the Air proxy at `http://localhost:3000` for auto-refresh. With `APP_ENV=development`, Fiber reloads HTML templates on every render and disables static asset caching. If you change `PORT`, also update `proxy.app_port` in `.air.toml`. If you are changing Tailwind source CSS, run the watcher in a second terminal:

```bash
npm run dev:css
```

Temporary port override example:

```bash
PORT=:3001 go run .
```

`go run .` loads the `.env` file during application startup. Environment variables already set in the shell take precedence over `.env` values. `PORT` must use Fiber listen-address format, for example `:3001`.

## Environment Variables

Parameter formats:

| Format | Description |
| --- | --- |
| Duration | Uses Go duration format. Example: `30s`, `5m`, `1h`. |
| RPC list | Comma-separated URL list. Example: `https://rpc-1,https://rpc-2`. |
| Amount/raw unit | Gas, fee, prefund, and transfer policy values are in the smallest unit of the chain, such as wei, sun, lamports, or sat/vbyte. |
| Chain name | In environment variable names, chain names are uppercase and dashes become underscores. Example: `CHILIZ_SPICY_RPC_URLS`. |

Required or critical variables:

| Variable | Description |
| --- | --- |
| `DATABASE_URL` | PostgreSQL connection string. URL or `key=value` DSN format is supported. The app will not start without this value. |
| `PORT` | Fiber listen address. Example: `:3001`. |
| `APP_ENV` | Runtime environment. Some security checks become stricter when set to `production`. Use `development` for local work. |
| `ALLOW_PRIVATE_WEBHOOK_URLS` | Allows webhook delivery to `localhost`, `127.0.0.1`, or private network IPs during local development. Ignored when `APP_ENV=production`. |
| `ALLOW_AUTOMIGRATE_IN_PRODUCTION` | Default is `false`. Startup `AutoMigrate` does not run in production unless this is temporarily enabled in a controlled maintenance window. |
| `GATEWAY_DB_MIGRATION_VERSION` | Evidence of the latest applied versioned GORM migration artifact. In production, it must match `services/dbmigrations.LatestID()`. |
| `ADDRESS_INDEX_PRELOAD_LIMIT` | Leave unset (default `-1`) to preload the complete authoritative wallet-address index required by chain listeners. Any finite limit, including `0`, keeps the index non-authoritative and chain listeners fail closed instead of querying the database per event or risking false ownership matches. |
| `SIGNER_MODE` | `software`, `kms`, `hsm`, `mpc`, `vault`, or another external custody mode. `software` is for development only; production signing and watch-only address derivation fail closed without an active external custody adapter. |
| `ALLOW_SOFTWARE_SIGNER_IN_PRODUCTION` | Legacy risk marker. Even if `true`, software signing is not allowed under `APP_ENV=production` and does not pass production readiness. |
| `METRICS_BEARER_TOKEN` | Bearer token for the `/metrics` Prometheus endpoint. Required in production; if empty, the endpoint returns `503`. |
| `PROVIDER_HEALTH_INTERVAL` | Provider health worker interval. Default: `1m`. |
| `PROVIDER_HEALTH_TIMEOUT` | Timeout for a single provider probe. Default: `8s`. |
| `PROVIDER_HEALTH_STALE_LAG_BLOCKS` | Block/slot lag threshold used to mark a provider stale/degraded relative to the reference head in the same chain. Default: `3`. |
| `PROVIDER_FAILOVER_STRATEGY` | `observe` only reports. `prefer_healthy` moves healthy providers earlier in `RPCs()`. `fail_closed` is an explicit strategy value for future production degraded-mode policies. |
| `PROVIDER_HEALTH_REQUIRE_HASH` | If `true`, provider snapshots without hash evidence are marked degraded. Hash evidence may remain unavailable for some chain families. |
| `PORTAL_JWT_SECRET` | JWT signing secret for merchant/admin portal mutations. Must be stable in production. Falls back to `DEALER_SESSION_SECRET`, `SESSION_SECRET`, or `MASTER_KEY` when missing. |
| `DEALER_SESSION_SECRET` / `SESSION_SECRET` | Fallback secrets for merchant/admin session signing. Production must not fall back to random runtime secrets. |
| `MASTER_KEY` | Used for API secret, webhook secret, and credential encryption. |
| `MNEMONIC_PHRASE` | Mnemonic used by Trust Wallet Core for BIP39 validation, HD wallet generation, and development signing. Under `APP_ENV=production`, the application process does not use mnemonic/private-key helpers; production custody must stay behind the external adapter boundary. |
| `ADMIN_EMAIL` | Bootstrap admin account email. |
| `ADMIN_PASSWORD` | Bootstrap admin account password. |
| `ADMIN_NAME` | Bootstrap admin display name. |

Optional service variables:

| Variable | Default / Description |
| --- | --- |
| `GATEWAY_LOG_FORMAT` | `json` or `text`. If empty, production uses `json`; other environments use `text`. |
| `GATEWAY_LOG_LEVEL` | `debug`, `info`, `warn`, or `error`. Default: `info`. |
| `HTTP_READ_TIMEOUT` | HTTP request read timeout. Default: `15s`. |
| `HTTP_WRITE_TIMEOUT` | HTTP response write timeout. Default: `30s`. |
| `HTTP_IDLE_TIMEOUT` | Keep-alive idle timeout. Default: `60s`. |
| `CORS_ALLOWED_ORIGINS` | Comma-separated allowed origin list. Empty-origin requests are allowed. |
| `API_KEY_RATE_LIMIT_PER_MINUTE` | Per-minute limit for `/api/v1`. Default: `120`. |
| `X402_ENABLED` | Enables generic static x402 seller routes. Payment-link x402 is configured per link in the merchant panel/API. Default: `false`. |
| `X402_FACILITATOR_URL` | x402 facilitator URL. Default: `https://x402.org/facilitator` (testnet facilitator). |
| `X402_NETWORKS` / `X402_NETWORK` | Comma-separated CAIP-2 networks accepted by x402. Default: `eip155:84532` (Base Sepolia). This integration supports EVM (`eip155:*`) and Solana (`solana:*`) exact payments. |
| `X402_PAY_TO` | Fixed receiving address for generic static x402 routes. A network-specific override can be set with `X402_PAY_TO_EIP155_84532` or `X402_PAY_TO_SOLANA_<reference>`. Do not set this for payment-link mode. |
| `X402_PRICE` | Fixed exact-scheme price for generic static routes, for example `$0.001`. |
| `X402_ROUTES` | Comma-, semicolon-, or newline-separated generic static route patterns, for example `GET /your-paid-route`. Only registered application routes should be listed. |
| `X402_ROUTE_DESCRIPTION` / `X402_SERVICE_NAME` | Optional metadata included in x402 payment requirements. |
| `X402_SYNC_FACILITATOR_ON_START` | Synchronizes facilitator-supported schemes on startup. Default: `true`; required for facilitator-provided Solana fee-payer metadata. |
| `X402_TIMEOUT` | Timeout for facilitator verification and settlement. Default: `30s`. |
| `WEBHOOK_RETRY_INTERVAL` | Webhook retry worker interval. Default: `30s`. |
| `WEBHOOK_MAX_ATTEMPTS` | Maximum webhook delivery attempts. |
| `WEBHOOK_RETRY_BACKOFF_BASE` | Initial duration for webhook retry exponential backoff. |
| `WEBHOOK_RETRY_BACKOFF_MAX` | Maximum duration for webhook retry exponential backoff. |
| `WEBHOOK_DELIVERY_CLAIM_TIMEOUT` | Timeout for claimed webhook delivery locks. |
| `TRANSACTION_FINALITY_INTERVAL` | Pending transaction finality worker interval. Default: `20s`. |
| `DEPOSIT_FACT_INTERVAL` | Deposit fact processing worker interval. Default: `10s`. |
| `SWEEP_JOB_INTERVAL` | Sweep worker interval. Default: `15s`. |
| `SWEEP_JOB_LOCK_TIMEOUT` | Sweep job lock timeout. Values below the minimum execution timeout are raised to the safe minimum. |
| `SWEEP_PREFUND_RETRY_AFTER` | Wait time before retrying a failed sweep prefund. Default: `10m`. |
| `RECONCILIATION_INTERVAL` | Ledger/reserve reconciliation worker interval. Default: `5m`. |
| `RESERVE_RECONCILIATION_LIMIT` | Reserve reconciliation batch limit. Default: `200`, maximum: `1000`. |
| `GATEWAY_SHUTDOWN_TIMEOUT` | Graceful shutdown timeout. Default: `5s`. |
| `GATEWAY_VERBOSE_TX` | Enables detailed transaction logs. Accepts `true`, `1`, `yes`, `on`, or `verbose`. |
| `GATEWAY_VERBOSE_EVENTS` | Enables detailed chain event logs. Accepts `true`, `1`, `yes`, `on`, or `verbose`. |
| `FINALITY_CONFIRMATIONS_DEFAULT` | Default confirmation fallback. |
| `CHAIN_<id>_CONFIRMATIONS` | Chain ID-specific confirmation override. |
| `<CHAIN_NAME>_CONFIRMATIONS` | Chain slug-specific confirmation override. Example: `ETHEREUM_CONFIRMATIONS`. |
| `CHAIN_<id>_START_BLOCK` | Listener start block/slot override by chain ID. |
| `<CHAIN_NAME>_START_BLOCK` | Listener start override by chain slug. Example: `ETHEREUM_START_BLOCK`. |
| `START_BLOCK_<CHAIN_NAME>` | Alternative listener start override. |
| `CHAIN_START_BLOCK_DEFAULT` | Default start block/slot for all chains. |
| `COINGECKO_BASE_URL` | CoinGecko API URL. Default: `https://api.coingecko.com/api/v3`. |
| `COINGECKO_CACHE_TTL` | Price cache duration. |
| `COINGECKO_RATE_LIMIT_COOLDOWN` | Cooldown after CoinGecko rate limits. |
| `COINGECKO_API_KEY` | CoinGecko API key. |
| `PRICE_<SYMBOL>_<CURRENCY>` | Custom token price when CoinGecko does not provide it. Example: `PRICE_PEPPER_USD=0.0001`. |
| `OIDC_AUTHORITY` / `OIDC_ISSUER_URL` | OIDC discovery authority/issuer URL. |
| `OIDC_AUTH_URL` | OIDC authorization endpoint override. |
| `OIDC_TOKEN_URL` | OIDC token endpoint override. |
| `OIDC_USERINFO_URL` | OIDC userinfo endpoint override. |
| `OIDC_CLIENT_ID` | Client ID for merchant portal OIDC login. |
| `OIDC_CLIENT_SECRET` | Client secret for merchant portal OIDC login. |
| `OIDC_REDIRECT_URI` | OIDC callback URL. |
| `OIDC_PROVIDER_NAME` | OIDC provider name. |
| `OIDC_SCOPES` | OIDC scope list. |
| `OIDC_PROMPT` | OIDC prompt parameter. |

RPC variables:

| Format | Description |
| --- | --- |
| `<CHAIN_NAME>_RPC_URLS` | Comma-separated RPC list. Example: `ETHEREUM_RPC_URLS`. |
| `CHAIN_<id>_RPC_URLS` | Chain ID-specific RPC list. Example: `CHAIN_1_RPC_URLS`. |
| `BSC_RPC_URLS`, `BINANCE_RPC_URLS` | BNB Chain alias RPC variables. |
| `BITCOIN_RPC_URLS` / `CHAIN_0_RPC_URLS` | Bitcoin endpoint list. The listener detects UniSat Open API, Bitcoin Core JSON-RPC, and Esplora-compatible APIs from these chain RPC URLs. |
| `TRON_JSONRPC_URLS` | TRON JSON-RPC endpoint list. |
| `TRON_HTTP_ENDPOINT` / `TRON_HTTP_ENDPOINTS` | TRON HTTP API endpoint settings. |
| `TRON_GRPC_ENDPOINT` / `TRON_GRPC_ENDPOINTS` | TRON listener gRPC endpoint settings. |
| `TRON_TESTNET_JSONRPC_URLS` | TRON Nile testnet JSON-RPC endpoint list. Override this to use Shasta or another TRON testnet. |
| `TRON_TESTNET_HTTP_ENDPOINT` / `TRON_TESTNET_HTTP_ENDPOINTS` | TRON Nile testnet HTTP API endpoint settings. Override this to use Shasta or another TRON testnet. |
| `TRON_TESTNET_GRPC_ENDPOINT` / `TRON_TESTNET_GRPC_ENDPOINTS` | TRON Nile testnet listener gRPC endpoint settings. Override this to use Shasta or another TRON testnet. |
| `TRON_PRO_API_KEY` | Optional TRON API access key. |
| `REQUIRE_EVM_TRACE` | Requires EVM listener trace support when `true`. |
| `DEBUG_EVM_TRACE` | Enables EVM trace debug logs when `true`. |

Gas, fee, sweep, and prefund settings:

| Variable | Description |
| --- | --- |
| `EVM_GAS_THRESHOLD_WEI` | EVM wallet gas threshold. |
| `EVM_GAS_PREFUND_WEI` | EVM prefund amount. |
| `EVM_MAX_GAS_PRICE_WEI` | Upper bound for EVM gas price policy. |
| `<CHAIN_NAME>_SWEEP_ADDRESS` | Chain-specific sweep target address. Example: `ETHEREUM_SWEEP_ADDRESS`. |
| `EVM_SWEEP_ADDRESS` | Fallback sweep address for all EVM chains. |
| `SWEEP_ADDRESS` | General sweep fallback address for Bitcoin, EVM, Solana, and TRON. |
| `BITCOIN_SWEEP_ADDRESS` / `BTC_SWEEP_ADDRESS` | Bitcoin sweep target address. |
| `BITCOIN_MIN_FEE_RATE_SAT_PER_VBYTE` | Lower bound for Bitcoin fee rate. Default: `1`. |
| `BITCOIN_MAX_FEE_RATE_SAT_PER_VBYTE` | Upper bound for Bitcoin fee rate. Default: `10000`. |
| `BITCOIN_FEE_RATE_SAT_PER_VBYTE` | Bitcoin transaction fee rate. Default: `10`. |
| `TRON_GAS_THRESHOLD_SUN` | TRON gas threshold. |
| `TRON_GAS_PREFUND_SUN` | TRON prefund amount. |
| `TRON_TRC20_FEE_LIMIT_SUN` | TRC-20 transfer fee limit. Default: `50000000`. |
| `TRON_NATIVE_SWEEP_FEE_SUN` | TRON native sweep fee reserve. Default: `1100000`. |
| `TRON_SWEEP_ADDRESS` / `TRX_SWEEP_ADDRESS` | TRON sweep target address. |
| `TRON_TESTNET_SWEEP_ADDRESS` / `TRX_TESTNET_SWEEP_ADDRESS` / `NILE_SWEEP_ADDRESS` / `TRON_NILE_SWEEP_ADDRESS` / `SHASTA_SWEEP_ADDRESS` | TRON testnet sweep target address. |
| `SOLANA_GAS_THRESHOLD_LAMPORTS` | Solana gas threshold. |
| `SOLANA_GAS_PREFUND_LAMPORTS` | Solana prefund amount. |
| `SOLANA_TRANSFER_FEE_LAMPORTS` | Solana transfer fee. Default: `5000`. |
| `SOLANA_SWEEP_ADDRESS` | Solana sweep target address. |

## HTTP Interfaces

Main web screens:

- `/` - Merchant portal home
- `/merchant/login` - Merchant portal login
- `/merchant/register` - Merchant registration
- `/merchant/dashboard` - Merchant dashboard
- `/merchant/onboarding` - Onboarding screen
- `/admin/login` - Admin login
- `/admin` - Admin dashboard
- `/payment-links/:token` - Payment link
- `/checkout/:token` - Checkout screen
- `/checkout/:token/pay` - Payment screen
- `/checkout/:token/ws` - Checkout websocket status channel
- `/checkout/:token/qr.png` - Payment QR code
- `/checkout/:token/status.json` - Checkout status JSON
- `/invoice/:token` - Invoice screen

## x402 HTTP Payments

The gateway can act as an opt-in x402 v2 seller for existing Fiber routes and for the hosted checkout flow. x402 is disabled by default and does not replace the merchant API key, HMAC, or portal authentication flows.

The integration uses the official [`x402-foundation/x402` Go SDK](https://github.com/x402-foundation/x402/tree/main/go) through a Fiber-to-`net/http` adapter. It supports the `exact` scheme for EVM and Solana networks:

Protocol references: [`HTTP 402 core concepts`](https://docs.x402.org/core-concepts/http-402) and the [`seller quickstart`](https://docs.x402.org/getting-started/quickstart-for-sellers).

1. An unpaid request receives `402 Payment Required` with a Base64-encoded `PAYMENT-REQUIRED` header.
2. The buyer signs the payment and retries with `PAYMENT-SIGNATURE`.
3. The facilitator verifies and settles the payment.
4. A successful response includes `PAYMENT-RESPONSE` settlement details.

There are two seller modes:

- Generic static resources: protect only the routes in `X402_ROUTES`; `X402_PAY_TO` and `X402_PRICE` are fixed deployment values.
- Payment links: enable x402 while creating the link in the merchant panel, or send `x402_enabled` to the payment-link API. The checkout asset selection supplies the network and token; the middleware loads the `PaymentSession` by checkout token and dynamically uses `DepositAddress` as `payTo`, `SelectedToken` as the asset, and `ExpectedAmountRaw` as the amount. `X402_PAY_TO`, `X402_PRICE`, and checkout route env variables are not used.

Enable it with deployment configuration (the default Base Sepolia values are intended for testing):

```env
X402_ENABLED=true
X402_FACILITATOR_URL=https://x402.org/facilitator
X402_NETWORKS=eip155:84532
X402_PAY_TO=0x1111111111111111111111111111111111111111
X402_PRICE=$0.001
X402_ROUTES=GET /payment-links/*
X402_SYNC_FACILITATOR_ON_START=true
X402_TIMEOUT=30s
```

For a merchant payment link, select x402 in the link creation form (fixed-amount links only):

```json
{
  "x402_enabled": true
}
```

The network is derived from the token asset selected in checkout, so there is no duplicate network setting on the payment link. x402 exact checkout requires a selected token asset (for example USDC); native ETH/SOL/BTC selections and donation links continue through the regular checkout payment flow.

`X402_ROUTES` must point to generic routes that are actually registered by the gateway. Multiple generic routes can be separated with commas, semicolons, or newlines. The payment-link checkout route is mounted automatically and is controlled by the link's x402 setting. Generic multi-network resources use `X402_NETWORKS` in CAIP-2 form and can provide network-specific receivers such as `X402_PAY_TO_SOLANA_<genesis-hash>`.

The middleware synchronizes the facilitator-supported scheme list on startup by default; this is needed for facilitator-provided Solana fee-payer metadata. Set `X402_SYNC_FACILITATOR_ON_START=false` only when startup must not contact the facilitator and the configured networks do not need that metadata. Request-time verification and settlement still use the configured facilitator. If x402 is enabled with invalid configuration, the middleware is not attached and the configuration error is logged. Generic x402 settlements are direct resource-server settlements; checkout-mode transfers go to the session's generated deposit address and are subsequently matched by the existing chain listener/reconciliation flow into that `PaymentSession`.

Legacy/internal command endpoint examples:

- `POST merchant.create`
- `POST merchant.fetch`
- `POST merchant.domain.create`
- `POST merchant.wallet.create`
- `POST system.withdraw`
- `POST system.sweep`
- `POST /payments/create`

## V1 Merchant API

Detailed integration guide: [`docs/integration-guide.md`](docs/integration-guide.md)

`/api/v1` read endpoints use an API key or bearer token:

```http
X-API-Key: <domain-api-key>
```

or:

```http
Authorization: Bearer <domain-api-key>
```

POST endpoints that create money movement or operations also require HMAC signing:

```http
X-API-Key: <domain-api-key>
X-API-Secret: <domain-api-secret>
X-Gateway-Timestamp: <unix_seconds>
X-Gateway-Signature: sha256=<hmac_sha256(method + path/query + timestamp + raw_body)>
```

Common endpoints:

- `GET /api/v1/common/status`
- `GET /api/v1/common/readiness`
- `GET /api/v1/common/balance`
- `GET /api/v1/common/prices`
- `GET /api/v1/common/currencies`
- `GET /api/v1/common/fiat-currencies`
- `GET /api/v1/common/networks`

Wallet provider endpoints:

- `POST /api/v1/wallet/create`
- `GET /api/v1/wallet/info`
- `GET /api/v1/wallet/addresses`
- `GET /api/v1/wallet/list`
- `GET /api/v1/wallet/balance`
- `GET /api/v1/wallets`

Payment endpoints:

- `POST /api/v1/payment/create`
- `POST /api/v1/payment/white-label`
- `POST /api/v1/payment/static-address`
- `GET /api/v1/payment/static-addresses`
- `GET /api/v1/payment/info`
- `GET /api/v1/payment/history`
- `GET /api/v1/payment/statistics`
- `GET /api/v1/payment/currencies`
- `GET /api/v1/payment/status-table`

Payout endpoints:

- `POST /api/v1/payout/create`
- `GET /api/v1/payout/info`
- `GET /api/v1/payout/history`
- `GET /api/v1/payout/status-table`

Refund endpoints:

- `POST /api/v1/refund/create`
- `GET /api/v1/refund/info`
- `GET /api/v1/refund/history`

Example payment creation request:

```bash
curl -X POST http://localhost:3001/api/v1/payment/create \
  -H "Content-Type: application/json" \
  -H "X-API-Key: <api-key>" \
  -H "X-API-Secret: <api-secret>" \
  -H "X-Gateway-Timestamp: <unix_seconds>" \
  -H "X-Gateway-Signature: sha256=<signature>" \
  -H "Idempotency-Key: order-2024-001" \
  -d '{
    "order_id": "ORD-2024-001",
    "amount": "25.00",
    "currency": "USD",
    "description": "Product purchase",
    "user_id": "customer_42",
    "success_url": "https://example.com/success",
    "cancel_url": "https://example.com/cancel"
  }'
```

Example static address creation request:

```bash
curl -X POST http://localhost:3001/api/v1/payment/static-address \
  -H "Content-Type: application/json" \
  -H "X-API-Key: <api-key>" \
  -H "X-API-Secret: <api-secret>" \
  -H "X-Gateway-Timestamp: <unix_seconds>" \
  -H "X-Gateway-Signature: sha256=<signature>" \
  -d '{
    "user_id": "customer_42",
    "chain_id": 1,
    "symbol": "USDT",
    "label": "Main wallet"
}'
```

Example wallet provider wallet creation request:

```bash
curl -X POST http://localhost:3001/api/v1/wallet/create \
  -H "Content-Type: application/json" \
  -H "X-API-Key: <api-key>" \
  -H "X-API-Secret: <api-secret>" \
  -H "X-Gateway-Timestamp: <unix_seconds>" \
  -H "X-Gateway-Signature: sha256=<signature>" \
  -d '{
    "user_id": "customer_42",
    "product_id": "wallet"
  }'
```

## Swagger

Swagger UI:

- `/swagger/*`
- `/docs/*`

Regenerate Swagger output:

```bash
swag init -g main.go -o docs
```

## Database

In development, migrations are managed by `AutoMigrate` in `services/database/database.go` for these main tables:

- `chain_states`
- `blocks`
- `chain_facts`
- `deposits`
- `domains`
- `merchants`
- `transactions`
- `ledger_entries`
- `wallets`
- `products`
- `payment_sessions`
- `idempotency_keys`
- `money_event_outboxes`
- `webhook_deliveries`
- `sweep_jobs`
- `withdrawal_requests`
- `refunds`
- `price_quotes`
- `reconciliation_jobs`
- `activity_logs`
- `provider_health_snapshots`
- `wallet_address_lookups`
- `admins`

The PostgreSQL `uuid-ossp` extension is enabled at migration startup. When `APP_ENV=production`, startup `AutoMigrate` is disabled by default and the application only validates expected schema columns. Production schema changes must be managed through the versioned GORM artifact registry in `services/dbmigrations`, during a maintenance window, and outside the startup path. See `docs/production-migration-discipline.md`.

## Workers

The application starts these background processes:

- Address index preload and missing address backfill
- Bootstrap admin account creation
- Webhook retry worker
- Payment session expiry worker
- Deposit fact processing worker
- Pending transaction finality worker
- Ledger/reserve reconciliation worker
- Bitcoin, Ethereum/EVM, Solana, and TRON listener workers

Listeners publish candidate transaction events through the dispatcher. Before a durable `chain_facts` write, the subscriber requires a registered asset and a confirmed, positive transfer to an address in the complete platform wallet index; same-merchant custody transfers and incomplete token identities are ignored. Bitcoin checks every input address, while Solana SPL token accounts are mapped to their base owners from strict transaction metadata. If the complete index is unavailable, chain listeners fail closed. The deposit worker repeats the asset/internal-transfer checks before legacy facts can reach transaction or ledger state. The listener path does not mark payments paid, write ledger entries, enqueue webhooks, or create sweep jobs. After the finality gate passes, the deposit fact worker advances transaction/deposit lifecycle, ledger posting, payment matching, and money event outbox flows idempotently.

Use `GET /api/v1/common/readiness` to validate gateway and wallet-provider readiness in live environments. The endpoint checks database access, production migration policy and `GATEWAY_DB_MIGRATION_VERSION` evidence, signer generation gates, backlog/drift state, all chain records, listener worker records, Trust Wallet Core HD wallet derivation, and latest provider health snapshots. It returns `503` when dependencies are missing or broken. Provider health workers do not store raw RPC URLs or expose them through readiness/metric output.

Use `GET /metrics` for Prometheus-compatible operational metrics. The endpoint returns gauges for money event outbox backlog, webhook delivery backlog, sweep job backlog, withdrawal/refund backlog, reconciliation drift, chain worker count, chain state block/slot, provider health/lag/latency/failover, wallet address lookup row count, migration/signer readiness, and external signer adapter readiness. In production, `Authorization: Bearer <METRICS_BEARER_TOKEN>` is required. Runbook: `docs/money-path-observability-runbook.md`.

Every HTTP response carries `X-Request-ID`. Request logs are limited to method, path, route, status, duration, error type, and request id. Query strings, request bodies, `Authorization`, API keys, signatures, and secret values are not logged. Panics return a sanitized `500` response with `request_id`.

## Security Notes

- `MASTER_KEY` and `MNEMONIC_PHRASE` must come from a secret manager, KMS, or an equivalent secure system in production.
- Startup `AutoMigrate` must not be left enabled in production. `ALLOW_AUTOMIGRATE_IN_PRODUCTION=true` is reported by readiness as a production launch blocker.
- `/metrics` should be exposed in production only behind a private network or reverse proxy allowlist and must require `METRICS_BEARER_TOKEN`.
- Production custody cannot use in-process software signing or local private-key signing. Do not hold high-volume customer funds before KMS/HSM/MPC/Vault or an equivalent external signer adapter completes chain-specific signing.
- Admin passwords must be strong and unique.
- Merchant portal/admin forms and public API endpoints must be additionally validated with portal JWT, rate limit, and reverse proxy settings before production.
- Webhook signatures are designed to be verified through `X-Gateway-Signature`, `X-Gateway-Timestamp`, and `X-Gateway-Event` headers.
- Public access must use HTTPS, secure cookie settings, and a restricted CORS origin list.

## Development Commands

Go tests:

```bash
go test ./...
```

Exchange webhook/balance integration smoke test:

```bash
cd ../exchange
./scripts/smoke_gateway_deposit.sh
```

This test does not require admin login. Gateway decrypts the domain webhook secret, sends a signed BTC deposit to the exchange callback, and verifies that the balance increases in the exchange database.

If the Trust Wallet Core submodule is not ready, first run `git submodule update --init --recursive third_party/trustwallet/wallet-core`. If the native build is not ready, run `./scripts/build_wallet_core.sh`. The `walletcorefallback` build tag is only for narrow local debugging and must not be used for production wallet generation or transfer signing.

CSS build:

```bash
npm run build:css
```

Swagger build:

```bash
swag init -g main.go -o docs
```

Migration:

```bash
go run . -migrate
```

Application:

```bash
go run .
```

## Adding a New Chain or Token

To add a new chain:

1. Add a chain implementation under `blockchain/chains/` or use the existing EVM-compatible implementation.
2. Add chain ID and slug information to `constants/chains.go`.
3. Register the chain in the factory inside `application/configuration/chains.go`.
4. Validate listener worker selection in `main.go` if needed.
5. Add the corresponding chain image under `static/chains/`.

To add a new token:

1. Register the token type with the appropriate constructor in `asset/`.
2. Add the record to `application/configuration/assets.go`.
3. Validate token symbol, decimals, token address/mint, and chain ID.
4. Add the corresponding image under `static/coins/` if needed.

## Roadmap

See `ROADMAP.md` for the detailed technical audit and prioritized development work. Before production use, finality gates, reorg handling, portal JWT, Redis-backed rate limiting, webhook URL re-validation, structured logging, and KMS/Vault integration should be evaluated carefully.
