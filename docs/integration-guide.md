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
    X-Gateway-Signature: "sha256=<hmac_sha256_hex(timestamp + raw_body)>"
webhook:
  callback_model: "single callback URL per domain"
  configured_in: "merchant portal domain settings"
  signature_header: "X-Gateway-Signature"
  signature_payload: "timestamp + raw_body"
  current_events:
    - native_transfer
    - payment_succeeded
    - payment_failed
    - payment_expired
  not_currently_sent_as_webhooks:
    - payout status events
    - refund status events
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

```text
timestamp = current unix timestamp in seconds
body = exact raw JSON request body bytes
message = timestamp + body
signature = hex(HMAC_SHA256(api_secret, message))
header = "sha256=" + signature
```

The gateway accepts `X-Gateway-Signature` as either `sha256=<hex>` or `<hex>`.

### Node.js Signing Example

```js
import crypto from "crypto";

function sign(apiSecret, body) {
  const timestamp = Math.floor(Date.now() / 1000).toString();
  const signature = crypto
    .createHmac("sha256", apiSecret)
    .update(timestamp)
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
SIG="$(printf "%s%s" "$TS" "$BODY" | openssl dgst -sha256 -hmac "$API_SECRET" -hex | awk '{print $2}')"

curl -X POST "$BASE_URL/api/v1/payment/create" \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $API_KEY" \
  -H "X-API-Secret: $API_SECRET" \
  -H "X-Gateway-Timestamp: $TS" \
  -H "X-Gateway-Signature: sha256=$SIG" \
  --data "$BODY"
```

Response shape:

```json
{
  "success": true,
  "payment_id": "uuid",
  "session_token": "token",
  "checkout_url": "https://gateway.example.com/checkout/token",
  "status": "pending",
  "expires_at": "2026-06-06T10:00:00Z",
  "deposit_wallet": "uuid"
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
  "chain_id": 1,
  "symbol": "USDT",
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
    "chain": "ethereum",
    "symbol": "USDT",
    "address": "0x...",
    "label": "Main wallet"
  }
}
```

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

Statuses:

- `pending`
- `awaiting_payment`
- `paid`
- `expired`
- `canceled`
- `failed`

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

Current behavior: payout status is not sent as a webhook. Poll:

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

Current behavior: refund status is not sent as a webhook. Poll:

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

### Transaction Webhook Payload

Event example: `native_transfer`

```json
{
  "event_id": "1-0xhash-log:0",
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

## Error Handling

Common API errors:

```json
{
  "result": "error",
  "error": "invalid request signature"
}
```

Webhook delivery behavior:

- Gateway retries failed webhook deliveries.
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
