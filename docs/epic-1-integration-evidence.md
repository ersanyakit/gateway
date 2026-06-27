# Epic 1 Integration Evidence

Date: 2026-06-27

This document records the partner-facing contract evidence for Epic 1: Partner Integration & Payment Intake Hardening.

## Covered Endpoints

- `GET /api/v1/common/readiness` verifies database, chain, listener, Trust Wallet Core, and live RPC readiness before production traffic.
- `POST /api/v1/payment/create` creates a hosted checkout payment session and returns the V1 response envelope.
- `POST /api/v1/payment/white-label` uses the same contract as hosted payment create.
- `POST /api/v1/payment/static-address` creates or returns a deterministic static address for the authenticated domain/product/user/asset scope.
- `GET /api/v1/payment/info` reads payment state by order or track id inside the authenticated domain.
- `GET /api/v1/payment/status-table` exposes supported payment status semantics.
- `GET /checkout/{token}/status.json` exposes payer-safe checkout status fields for hosted checkout polling.

## Supported Authentication Modes

- Read endpoints accept `X-API-Key`.
- Read endpoints also accept `Authorization: Bearer <api_key>` where implemented by the V1 auth helper.
- Mutating V1 endpoints require `X-API-Key`, `X-API-Secret`, `X-Gateway-Timestamp`, and `X-Gateway-Signature`.
- V1 request signing uses canonical `method + path/query + timestamp + raw body`.
- Webhook verification is separate and uses `timestamp + raw_body` with the domain webhook secret.

## Idempotency Behavior

- Payment create accepts `Idempotency-Key`.
- If `Idempotency-Key` is omitted, `order_id` is used as the domain-scoped fallback key.
- Repeating the same key and payload returns the cached response.
- Reusing the same key with a different payload returns the V1 error envelope with conflict status.
- Contract tests cover the documented `Idempotency-Key` behavior and Swagger parameter.

## Static Wallet Scope Rules

- Static address scope is deterministic inside the authenticated `domain/product/user/asset` boundary.
- Asset scope is `chain_id`, `symbol`, and optional `token`.
- Repeated calls for the same domain/product/user/asset return the existing address.
- Unsupported chain or asset requests fail before wallet creation.
- Static address responses include `wallet_id`, `user_id`, `product_id`, `chain`, `chain_id`, `symbol`, optional `token`, `address`, optional `label`, and `created_at`.

## Checkout State Semantics

- Hosted checkout status can report `active`, `pending`, `confirming`, `paid`, `expired`, `canceled`, `failed`, `underpaid`, `overpaid`, and `partial_paid`.
- `paid`, `expired`, `canceled`, `failed`, `underpaid`, `overpaid`, and `partial_paid` are terminal payer-facing states.
- `payable` indicates whether the checkout should still present payment affordances.
- `terminal` indicates whether the payer-facing state is final.
- Checkout status responses expose only safe fields such as status, paid flag, payment id, tx hash, success/cancel paths, `payable`, and `terminal`.

## Error Envelope

V1 success responses use:

```json
{
  "result": "ok",
  "data": {}
}
```

V1 error responses use:

```json
{
  "result": "error",
  "message": "invalid request signature"
}
```

The envelope applies to authentication failures, validation failures, idempotency conflicts, unsupported assets, expired or unavailable sessions, and authorization failures. Messages must not expose API secrets, raw signatures, private keys, mnemonics, stack traces, or cross-tenant resource ownership.

## Known Production Limitations

Epic 1 does not complete production custody or exchange-grade readiness. External signer enforcement, reconciliation jobs, ledger-derived balance authority, durable event delivery, versioned migrations, operational observability, backup/restore, compliance controls, and controlled launch gates remain required before production custody claims.

## Verification

Expected local verification for this evidence:

```bash
go test -count=1 ./docs ./api/handlers ./types
go test -count=1 ./...
go vet ./...
git diff --check
```
