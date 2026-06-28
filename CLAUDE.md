# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Build
go build ./...

# Initialize required Trust Wallet Core submodule after a plain clone
git submodule update --init --recursive third_party/trustwallet/wallet-core

# Build Trust Wallet Core native library when missing/stale
./scripts/build_wallet_core.sh

# Run
go run .

# Run with DB migration + seed
go run . -migrate
go run . -seed
go run . -install   # migrate + seed

# Tests
go test ./...
go test ./repositories/ -run TestMerchantRepo   # single test

# Regenerate Swagger docs
swag init -g main.go -o docs
```

The `.env` file (see `.env.sample`) must be present. Required vars: `DATABASE_URL`, `MASTER_KEY`, `PORT`, `MNEMONIC_PHRASE`.
Wallet mnemonic validation, HD private key derivation, and address generation use Trust Wallet Core by default. The fallback provider is only selected with `-tags walletcorefallback` and should not be used for production wallet generation.

## Architecture

**Go module name:** `core`  
**HTTP framework:** Gofiber v3  
**Database:** PostgreSQL via GORM (auto-migrated)

### Global singleton

`application.CORE` (`application/application.go`) is the single root object holding `*gorm.DB` and `*routes.Router`. Everything downstream accesses repos and services through `CORE.Router`.

### Layer map

| Package | Role |
|---|---|
| `application/configuration/` | **Entry point for chain & asset registration.** Add new chains in `chains.go`, new tokens in `assets.go`. |
| `blockchain/` | `Chain` interface + `ChainFactory` (registry + alias lookup). Per-chain implementations live in `blockchain/chains/`. |
| `asset/` | `Registry` of tradable assets. Constructors: `NewEVMNative`, `NewERC20`, `NewBTC`, `NewSOL`, `NewSPL`, `NewTRX`, `NewTRC20`. |
| `constants/` | `ChainID` enum, `CommandType` strings (used as HTTP route paths), app-wide constants. |
| `models/` | GORM models: `Merchant`, `Domain`, `Wallet`, `Transaction`, `PaymentSession`, `Product`, `WithdrawalRequest`, `ActivityLog`, `ChainState`. |
| `repositories/` | One repo per model; all DB access lives here. |
| `services/system/` | Business logic wrapping repos (`MerchantService`, `WalletService`, `DomainService`). |
| `services/realtime/` | `PaymentHub` — WebSocket fan-out for live payment status per session token. |
| `services/webhook/` | `Notifier` — delivers HMAC-signed webhook POSTs to merchant callback URLs; retries on failure. |
| `services/pricing/` | CoinGecko price oracle. |
| `api/handlers/` | Fiber handler functions, one file per domain. |
| `api/routes/routes.go` | Wires all routes onto the Fiber app and constructs every repo/service. |
| `workers/dispatcher/` | Pub/sub event bus: blockchain listeners publish `Event`s; `main.go` subscribes and writes transactions + fires webhooks. |
| `workers/listeners/` | Per-chain block/tx listeners (EVM generic, Bitcoin, Solana, TRON, Avalanche, Binance, Chiliz, Ethereum). |
| `helpers/` | `credentials.go` — API key generation (`gw_live_…`/`gw_test_…`), HMAC signature helpers, AES-GCM secret encryption (needs `MASTER_KEY` env var). |
| `contracts/` | Generated Go ABI bindings for ERC-20 and Multicall3. |
| `views/` | HTML templates rendered by Gofiber's html engine (`.html` extension). |

### Route convention

HTTP routes use `CommandType` constants as path strings (e.g. `constants.CMD_MERCHANT_CREATE` → `"merchant.create"`). All are POST routes except the dealer/admin/checkout UI routes which use GET.

### Blockchain listener lifecycle

Listeners are currently **disabled** in `main.go` (`isEnabled = false`). When enabled, each chain gets an `RpcListener` worker registered on `ChainFactory`, subscribed to the `Dispatcher` bus. On each tx event the main loop calls `handleDepositWebhook` → `handlePaymentDeposit` → `publishPaymentUpdate`.

### Payment flow

1. `POST /payments/create` → creates `PaymentSession`
2. Customer visits `/checkout/:token` → asset selection → `/checkout/:token/pay` (shows address + QR)
3. `/checkout/:token/ws` — WebSocket connection to `PaymentHub` for live updates
4. Blockchain listener detects inbound tx → matches wallet → marks session paid → broadcasts to hub + fires webhook

### Webhook security

Webhooks are HMAC-SHA256 signed: `HMAC(secret, timestamp || body)`. Merchant secrets are stored AES-GCM encrypted (key derived from `MASTER_KEY`). Replay protection uses a 30-second timestamp skew (`helpers.ValidateTimestamp`).
