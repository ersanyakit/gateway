# Gateway Integration Guide

This document is written for developers and AI coding agents integrating merchant systems with this payment gateway.

## Quick Summary

```yaml
base_url: "https://your-gateway.example.com"
auth_read_endpoints:
  required_headers:
    X-API-Key: "<domain_api_key>"
auth_write_endpoints:
  required_headers:
    X-API-Key: "<domain_api_key>"
    X-API-Secret: "<domain_api_secret>"
    X-Gateway-Timestamp: "<unix_seconds>"
    X-Gateway-Signature: "sha256=<hmac_sha256_hex(method + path/query + timestamp + raw body)>"
  request_signature_payload: "METHOD\npath?query\ntimestamp\nraw_body"
webhook:
  callback_model: "single callback URL per domain"
  configured_in: "merchant portal domain settings"
  signature_header: "X-Gateway-Signature"
  signature_payload: "timestamp + raw_body"
  current_events:
    - native_transfer
    - transaction_reorged
    - payment_succeeded
    - payment_failed
    - payment_expired
    - payout.requested.v1
    - payout.broadcast.v1
    - payout.finalized.v1
    - payout.rejected.v1
    - payout.failed.v1
    - refund.requested.v1
    - refund.broadcast.v1
    - refund.succeeded.v1
    - refund.rejected.v1
    - refund.failed.v1
    - sweep.requested.v1
    - sweep.succeeded.v1
    - sweep.failed.v1
    - sweep.dead_lettered.v1
```

## Credentials

A merchant domain has:

- `api_key`: public API identifier.
- `api_secret`: secret used for HMAC request signing. It is returned only when the domain is created or when the API secret is rotated.
- `webhook_secret`: secret used by the gateway to sign webhook callbacks.
- `webhook_url`: single callback URL used for all currently supported webhook events.

If the `api_secret` is lost, rotate it from the merchant portal endpoint:

```http
POST /merchant/domains/{domain_id}/rotate-api-secret
```

The rotated secret is returned once in the response.

## Request Signing

Write endpoints require HMAC signing.

Signed endpoints include:

- `POST /payments/create`
- `POST /api/v1/payment/create`
- `POST /api/v1/payment/white-label`
- `POST /api/v1/payment/static-address`
- `POST /api/v1/wallet/create`
- `POST /api/v1/payout/create`
- `POST /api/v1/refund/create`
- `POST /api/v1/transaction/rescan`

Read endpoints generally require only `X-API-Key` or `Authorization: Bearer <api_key>`.

Before sending production traffic, call `GET /api/v1/common/readiness` with the domain API key. It returns `200` only when the database, all configured chains, listener workers, Trust Wallet Core HD wallet derivation, and live RPC/gRPC block probes are ready; otherwise it returns `503` with the failing checks.

### Signature Algorithm

V1 request signing binds method, original path/query target, timestamp, and raw body.

```text
timestamp = current unix timestamp in seconds
METHOD = uppercase HTTP method, for example POST
path_and_query = request path plus query string, for example /api/v1/payment/create
body = exact raw JSON request body bytes
message = METHOD + "\n" + path_and_query + "\n" + timestamp + "\n" + body
signature = hex(HMAC_SHA256(api_secret, message))
header = "sha256=" + signature
```

For `POST /api/v1/payment/create`, the canonical prefix is `POST\n/api/v1/payment/create\n<timestamp>\n<body>`. If a signed endpoint includes query parameters, include them exactly as sent in `path_and_query`.

The gateway accepts `X-Gateway-Signature` as either `sha256=<hex>` or `<hex>`.

### Node.js Signing Example

```js
import crypto from "crypto";

function sign(apiSecret, method, pathAndQuery, body) {
  const timestamp = Math.floor(Date.now() / 1000).toString();
  const signature = crypto
    .createHmac("sha256", apiSecret)
    .update(method.trim().toUpperCase())
    .update("\n")
    .update(pathAndQuery.trim())
    .update("\n")
    .update(timestamp)
    .update("\n")
    .update(body)
    .digest("hex");

  return {
    "X-Gateway-Timestamp": timestamp,
    "X-Gateway-Signature": `sha256=${signature}`,
  };
}
```

## Create Hosted Payment

Use this when your customer should pay through the hosted checkout page.

```http
POST /api/v1/payment/create
```

Request body:

```json
{
  "order_id": "ORDER-1001",
  "amount": "25.00",
  "currency": "USD",
  "description": "Order #1001",
  "user_id": "customer_42",
  "success_url": "https://merchant.example.com/success",
  "cancel_url": "https://merchant.example.com/cancel"
}
```

Example:

```bash
BODY='{"order_id":"ORDER-1001","amount":"25.00","currency":"USD","description":"Order #1001","user_id":"customer_42","success_url":"https://merchant.example.com/success","cancel_url":"https://merchant.example.com/cancel"}'
TS="$(date +%s)"
SIG="$(printf "POST\n/api/v1/payment/create\n%s\n%s" "$TS" "$BODY" | openssl dgst -sha256 -hmac "$API_SECRET" -hex | awk '{print $2}')"

curl -X POST "$BASE_URL/api/v1/payment/create" \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $API_KEY" \
  -H "X-API-Secret: $API_SECRET" \
  -H "X-Gateway-Timestamp: $TS" \
  -H "X-Gateway-Signature: sha256=$SIG" \
  -H "Idempotency-Key: ORDER-1001-create-v1" \
  --data "$BODY"
```

Response shape:

```json
{
  "result": "ok",
  "data": {
    "payment_id": "uuid",
    "track_id": "uuid",
    "session_token": "token",
    "checkout_url": "https://gateway.example.com/checkout/token",
    "status": "pending",
    "expires_at": "2026-06-06T10:00:00Z",
    "order_id": "ORDER-1001",
    "amount": "25.00",
    "currency": "USD",
    "chain_id": 1,
    "symbol": "USDT",
    "token": "0xdAC17F958D2ee523a2206206994597C13D831ec7",
    "decimals": 6,
    "expected_amount_raw": "25000000",
    "deposit_address": "0xDepositAddress"
  }
}
```

Send `Idempotency-Key` on create requests. If older client code still names this header `X-Idempotency-Key`, migrate it to `Idempotency-Key`; do not send both. A retry with the same key and identical payload returns the cached create response.

Idempotency conflict example: reusing the same key with a different payload returns `409`:

```json
{
  "result": "error",
  "message": "idempotency key was already used with a different request"
}
```

Customer completes payment at `checkout_url`.

## Create Static Deposit Address

Use this when your system wants a permanent deposit address per user and asset.

```http
POST /api/v1/payment/static-address
```

Request body:

```json
{
  "user_id": "customer_42",
  "product_id": "checkout",
  "chain_id": 1,
  "symbol": "USDT",
  "token": "0xdAC17F958D2ee523a2206206994597C13D831ec7",
  "label": "Main wallet"
}
```

Response:

```json
{
  "result": "ok",
  "data": {
    "wallet_id": "uuid",
    "user_id": "customer_42",
    "product_id": "static:1:USDT:token:0xdac17f958d2ee523a2206206994597c13d831ec7:product:checkout",
    "chain": "ethereum",
    "chain_id": 1,
    "symbol": "USDT",
    "token": "0xdAC17F958D2ee523a2206206994597C13D831ec7",
    "address": "0x...",
    "label": "Main wallet",
    "created_at": "2026-06-26T12:00:00Z"
  }
}
```

Static addresses are deterministic inside the authenticated domain/product/user/asset scope. The asset scope is `chain_id`, `symbol`, and optional `token`; repeated calls for the same domain/product/user/asset return the existing address. Unsupported chains or assets return the V1 error envelope before wallet creation.

Deposits to this address are detected by blockchain listeners and can generate transaction/payment webhooks depending on the matching flow.

## Create Wallet Provider Wallet

Use this when your product needs Gateway to act as a wallet provider. The endpoint creates or returns one deterministic multi-chain wallet for a merchant user and product scope.

```http
POST /api/v1/wallet/create
```

Request body:

```json
{
  "user_id": "customer_42",
  "product_id": "wallet"
}
```

Response:

```json
{
  "result": "ok",
  "data": {
    "wallet_id": "uuid",
    "user_id": "customer_42",
    "product_id": "wallet",
    "addresses": {
      "ethereum": "0x...",
      "bitcoin": "bc1...",
      "solana": "So...",
      "tron": "T..."
    },
    "created_at": "2026-06-26T12:00:00Z"
  }
}
```

Read wallet data:

```http
GET /api/v1/wallet/info?wallet_id=<uuid>
GET /api/v1/wallet/info?user_id=customer_42&product_id=wallet
GET /api/v1/wallet/addresses?wallet_id=<uuid>
GET /api/v1/wallet/list?user_id=customer_42
GET /api/v1/wallet/balance?wallet_id=<uuid>
```

Wallet-provider deposits are matched by address and posted to the ledger for that wallet. Use `GET /api/v1/wallet/balance` for wallet-scoped balances and `GET /api/v1/common/balance` for domain aggregate balances.

## Query Payment

```http
GET /api/v1/payment/info?order_id=ORDER-1001
GET /api/v1/payment/info?track_id=<session_token>
```

Payment info/history statuses:

- `pending`
- `awaiting_payment`
- `paid`
- `expired`
- `canceled`
- `failed`
- `underpaid`

Checkout polling uses:

```http
GET /checkout/{token}/status.json
```

Example checkout status response:

```json
{
  "success": true,
  "status": "confirming",
  "paid": false,
  "payment_id": "uuid",
  "tx_hash": "0xhash",
  "success_path": "/checkout/token/return/success",
  "cancel_path": "/checkout/token/cancel",
  "payable": true,
  "terminal": false
}
```

Documented checkout status values:

- `active`
- `pending`
- `confirming`
- `paid`
- `expired`
- `canceled`
- `failed`
- `underpaid`

```json
["active", "pending", "confirming", "paid", "expired", "canceled", "failed", "underpaid"]
```

`payable` is false for terminal states or sessions that should not accept a new payment attempt. `terminal` is true for final payer-facing states such as `paid`, `expired`, `canceled`, `failed`, and `underpaid`.

Terminal checkout example:

```json
{
  "success": true,
  "status": "paid",
  "paid": true,
  "payment_id": "uuid",
  "tx_hash": "0xhash",
  "success_path": "/checkout/token/return/success",
  "cancel_path": "/checkout/token/cancel",
  "payable": false,
  "terminal": true
}
```

Detected deposits are held behind a confirmation gate before a payment becomes `paid`. If a finalized deposit is later invalidated by a reorg, the gateway reverses the ledger entries, marks the transaction `reorged`, and emits correction webhooks when callback settings are configured.

## Create Payout Request

Use this to request a withdrawal from the merchant reserve wallet. The request is not broadcast immediately; admin approval is required.

```http
POST /api/v1/payout/create
```

Request body:

```json
{
  "chain": "ethereum",
  "symbol": "USDT",
  "to_address": "0xRecipient...",
  "amount": "10.50",
  "note": "User withdrawal"
}
```

Response:

```json
{
  "result": "ok",
  "data": {
    "payout_id": "uuid",
    "chain": "ethereum",
    "symbol": "USDT",
    "to_address": "0xRecipient...",
    "amount_raw": "10500000",
    "status": "pending"
  }
}
```

Payout statuses:

- `pending`: awaiting admin approval.
- `processing`: approved and broadcast/finalization is in progress.
- `approved`: on-chain broadcast completed and ledger finalized.
- `rejected`: rejected by admin.
- `failed`: transfer failed.

Payout lifecycle events are sent as webhooks when a domain webhook is configured:

- `payout.requested.v1`
- `payout.broadcast.v1`
- `payout.finalized.v1`
- `payout.rejected.v1`
- `payout.failed.v1`

Polling remains supported:

```http
GET /api/v1/payout/info?payout_id=<uuid>
GET /api/v1/payout/history
```

## Create Refund Request

Use this to request refund of a paid payment. Admin approval is required before on-chain transfer.

```http
POST /api/v1/refund/create
```

Request body:

```json
{
  "payment_id": "payment_uuid",
  "amount_raw": "25000000",
  "reason": "Customer requested refund"
}
```

Alternative identifier:

```json
{
  "order_id": "ORDER-1001",
  "reason": "Customer requested refund"
}
```

Refund statuses:

- `pending`
- `processing`
- `succeeded`
- `rejected`
- `failed`

Creating a refund request reserves the refundable amount in the ledger. Rejected refunds or refunds that fail before broadcast release that reservation.

Refund lifecycle events are sent as webhooks when a domain webhook is configured:

- `refund.requested.v1`
- `refund.broadcast.v1`
- `refund.succeeded.v1`
- `refund.rejected.v1`
- `refund.failed.v1`

Polling remains supported:

```http
GET /api/v1/refund/info?refund_id=<uuid>
GET /api/v1/refund/history
```

## Transaction Rescan

Use this when a blockchain transaction exists but was not processed or needs replay.

```http
POST /api/v1/transaction/rescan
```

Request body:

```json
{
  "chain": "ethereum",
  "tx_hash": "0x..."
}
```

The gateway re-fetches the transaction and replays wallet matching, payment matching, ledger posting, and webhook processing.

## Webhooks

The gateway uses one callback URL per domain. The merchant integration should route by `event_type` in the JSON body or by `X-Gateway-Event` header.

The versioned money event catalog is maintained in `docs/money-event-catalog.md`. It maps current emitted names, legacy underscore aliases, and canonical dotted event names without replacing the examples below.

Headers:

```text
X-Gateway-Event: payment_succeeded
X-Gateway-Event-Id: <event_id>
X-Gateway-Timestamp: <unix_seconds>
X-Gateway-Signature: sha256=<hmac_sha256_hex(timestamp + raw_body)>
```

Verify webhooks with the domain `webhook_secret`, not the API secret.

### Webhook Verification Example

```js
import crypto from "crypto";

function verifyWebhook(webhookSecret, timestamp, rawBody, receivedSignature) {
  const sig = receivedSignature.replace(/^sha256=/, "");
  const expected = crypto
    .createHmac("sha256", webhookSecret)
    .update(timestamp)
    .update(rawBody)
    .digest("hex");

  return crypto.timingSafeEqual(Buffer.from(sig), Buffer.from(expected));
}
```

Reject webhooks when:

- Timestamp is too old or too far in the future.
- Signature is invalid.
- `event_id` was already processed.

### Delivery, Replay, and Deduplication

Webhook delivery is at-least-once. Retries and operator replay can deliver the same event more than once. A replay preserves the original event id, event type, event version, merchant/domain scope, and payload idempotency fields so consumers can safely deduplicate.

Use these fields as the consumer dedupe key:

- `X-Gateway-Event-Id` header and `event_id` payload field.
- `X-Gateway-Event` / `event_type`.
- `X-Gateway-Event-Version` / `event_version`.
- `merchant_id` and `domain_id` scope.

Persist the dedupe decision before fulfilling an order, crediting an account, or triggering irreversible downstream work. A replay should be treated as recovery of the same event, not as a new business event.

### Transaction Webhook Payload

Event example: `native_transfer`

```json
{
  "event_id": "1-0xhash-log:0:native_transfer",
  "event_type": "native_transfer",
  "transaction_id": "uuid",
  "merchant_id": "uuid",
  "domain_id": "uuid",
  "product_id": "static:1:ETH",
  "user_id": "customer_42",
  "wallet_id": "uuid",
  "chain_id": 1,
  "hash": "0xhash",
  "log_index": "log:0",
  "block_number": "123456",
  "block_hash": "0xblock",
  "token": null,
  "symbol": "ETH",
  "decimals": 18,
  "from": "0xSender",
  "to": "0xDepositAddress",
  "amount_raw": "1000000000000000000",
  "status": "confirmed",
  "created_at": "2026-06-06T10:00:00Z"
}
```

Transaction webhook `event_id` is `<transaction_unique_hash>:<event_type>`, so a later `transaction_reorged` correction for the same transaction has a distinct idempotency key.

The existing underscore webhook event names remain compatibility aliases until a versioned event catalog migration explicitly retires them.

The full versioned money lifecycle catalog is documented in `docs/money-event-catalog.md`.

### Payment Webhook Payload

Event examples:

- `payment_succeeded`
- `payment_failed`
- `payment_expired`

```json
{
  "event_id": "payment_uuid:payment_succeeded",
  "event_type": "payment_succeeded",
  "payment_id": "uuid",
  "session_token": "token",
  "order_id": "ORDER-1001",
  "status": "paid",
  "merchant_id": "uuid",
  "domain_id": "uuid",
  "product_id": "ORDER-1001",
  "user_id": "customer_42",
  "wallet_id": "uuid",
  "amount": "25.00",
  "currency": "USD",
  "chain_id": 1,
  "symbol": "USDT",
  "token": "0xdAC17F958D2ee523a2206206994597C13D831ec7",
  "decimals": 6,
  "expected_amount_raw": "25000000",
  "deposit_address": "0xDepositAddress",
  "tx_hash": "0xhash",
  "tx_unique_hash": "1-0xhash-log:0",
  "created_at": "2026-06-06T09:55:00Z",
  "paid_at": "2026-06-06T10:00:00Z"
}
```

### Lifecycle Webhook Payload

Event examples:

- `payout.finalized.v1`
- `refund.succeeded.v1`
- `sweep.succeeded.v1`

```json
{
  "event_id": "entity_uuid:payout.finalized.v1",
  "event_type": "payout.finalized.v1",
  "event_version": "v1",
  "entity_type": "payout",
  "entity_id": "uuid",
  "merchant_id": "uuid",
  "domain_id": "uuid",
  "wallet_id": "uuid",
  "chain": "ethereum",
  "symbol": "USDT",
  "token": "0xdAC17F958D2ee523a2206206994597C13D831ec7",
  "decimals": 6,
  "amount_raw": "10500000",
  "to_address": "0xRecipient",
  "status": "approved",
  "tx_hash": "0xhash",
  "created_at": "2026-06-26T10:00:00Z",
  "updated_at": "2026-06-26T10:05:00Z"
}
```

## Recommended Merchant-Side Flow

### Hosted Checkout Flow

1. Merchant backend creates a payment with `POST /api/v1/payment/create`.
2. Merchant redirects customer to `checkout_url`.
3. Customer selects chain/asset and pays.
4. Gateway detects blockchain transaction.
5. Gateway marks payment `paid`.
6. Gateway sends `payment_succeeded` webhook to the domain callback URL.
7. Merchant verifies webhook signature.
8. Merchant idempotently fulfills `order_id`.

### Static Address Flow

1. Merchant backend creates/reuses address with `POST /api/v1/payment/static-address`.
2. Merchant shows the address to the customer.
3. Gateway detects deposits to the address.
4. Gateway sends transaction webhook events to the domain callback URL.
5. Merchant verifies signature and credits the user based on `event_id`.

### Payout Flow

1. Merchant backend creates payout request with `POST /api/v1/payout/create`.
2. Admin approves or rejects in the admin panel.
3. Merchant polls `GET /api/v1/payout/info`.
4. Merchant treats `approved`, `rejected`, and `failed` as terminal states.

### Refund Flow

1. Merchant backend creates refund request with `POST /api/v1/refund/create`.
2. Admin approves or rejects in the admin panel.
3. Merchant polls `GET /api/v1/refund/info`.
4. Merchant treats `succeeded`, `rejected`, and `failed` as terminal states.

## Idempotency Rules

Merchant systems should store and deduplicate:

- Payment webhook: `event_id`
- Transaction webhook: `event_id`
- Payment create: use `Idempotency-Key` header, or rely on `order_id` fallback.
- Payout/refund create: store returned `payout_id` / `refund_id`.

Never fulfill an order based only on frontend return URLs. Always use webhook verification or server-side payment status query.

## Contract Evidence - Epic 1

Partner API Contract Evidence is in `docs/epic-1-integration-evidence.md`. It lists covered endpoints, auth modes, idempotency behavior, static wallet domain/product/user/asset scope, checkout state semantics, and known limitations for merchant/domain and tenant/domain isolation. Known production limitations are summarized below and expanded in the evidence file.

## Error Handling

Common API errors:

```json
{
  "result": "error",
  "message": "invalid request signature"
}
```

The same V1 envelope is used for validation failures, idempotency conflicts, unsupported assets, expired or unavailable sessions, and authorization failures. Error messages are category-level and must not expose API secrets, raw signatures, stack traces, or whether another tenant/domain owns a resource.

Webhook delivery behavior:

- Gateway retries failed webhook deliveries.
- Gateway may replay a failed or dead-lettered webhook with the same event id/type/version.
- Merchant endpoint should return any `2xx` status after successfully persisting the event.
- Merchant endpoint should be idempotent because retries can happen.

## Security Checklist

- Keep `api_secret` server-side only.
- Keep `webhook_secret` server-side only.
- Verify request/webhook signatures using raw request body bytes.
- Reject old timestamps.
- Deduplicate webhook `event_id`.
- Do not process unsigned callbacks.
- Do not expose API secret in frontend code.

## Known Production Limitations

Epic 1 hardens partner intake, hosted checkout state, static address scope, idempotency, and integration evidence. It is controlled-launch evidence, not a claim of production custody or exchange-grade readiness.

Production custody remains gated.

Do not treat the gateway as production custody/exchange-ready until the external signer boundary, reconciliation jobs, ledger-derived balances, versioned migrations, observability/SLOs, backup/restore, compliance controls, and controlled launch gates are complete.
