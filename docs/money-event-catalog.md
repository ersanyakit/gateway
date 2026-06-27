# Money Event Catalog

Bu katalog Gateway'in para hareketi webhook sozlesmesini versioned ve geriye uyumlu sekilde tanimlar. Mevcut entegrasyonlarda kullanilan event adlari korunur; dotted canonical adlar yeni sozlesme hedefidir.

## Common Envelope

Her money event payload'i asagidaki ortak alanlari tasimalidir:

| Field | Meaning |
| --- | --- |
| `event_id` | Stable, consumer-side idempotency key. |
| `event_type` | Emitted event name or compatibility alias. |
| `event_version` | Contract version, currently `v1`. |
| `occurred_at` | Event occurrence timestamp in RFC3339 format. |
| `merchant_id` | Merchant/tenant scope. |
| `domain_id` | Domain scope for callback routing and isolation. |
| `resource_type` | Domain object type such as `payment`, `deposit`, `withdrawal`, `refund`, `sweep`, `transaction`, or `webhook_delivery`. |
| `resource_id` | Stable resource identifier. |
| `resource_status` | Lifecycle status represented by this event. |
| `idempotency_key` | Business idempotency key for replay-safe processing. |
| `correlation_id` | Cross-system tracing key that is safe to expose. |

Payload examples and production deliveries must exclude API signing secrets, webhook signing secrets, raw HMAC header values, private key material, seed phrase material, full internal diagnostics, and unredacted stack traces.

Webhook callback verification is separate from V1 API request signing. Webhooks continue to use the domain webhook signing secret with this HMAC payload:

```text
timestamp + raw_body
```

## Canonical Events

| Event | Family | Producer | Consumers | Terminal | Lifecycle meaning |
| --- | --- | --- | --- | --- | --- |
| `deposit.detected.v1` | deposit | chain indexer | merchant, exchange, operator diagnostics | no | On-chain deposit was observed before finality. |
| `deposit.finalized.v1` | deposit | deposit service | merchant, exchange, operator diagnostics | yes | Deposit reached required finality and can affect wallet/payment state. |
| `payment.succeeded.v1` | payment | payment service | merchant, exchange, operator diagnostics | yes | Payment reached a successful terminal state. |
| `payment.failed.v1` | payment | payment service | merchant, exchange, operator diagnostics | yes | Payment reached a failed terminal state or was corrected. |
| `payment.expired.v1` | payment | payment service | merchant, exchange, operator diagnostics | yes | Checkout expired before successful settlement. |
| `payment.underpaid.v1` | payment | payment service | merchant, exchange, operator diagnostics | yes | Payment received less than expected and needs follow-up. |
| `payment.overpaid.v1` | payment | payment service | merchant, exchange, operator diagnostics | yes | Payment received more than expected and may require refund or reconciliation. |
| `payment.partial_paid.v1` | payment | payment service | merchant, exchange, operator diagnostics | yes | Partial deposit received; automatic checkout aggregation is unsupported. |
| `withdrawal.requested.v1` | withdrawal | withdrawal service | merchant, exchange, operator diagnostics | no | Withdrawal request was created and awaits policy checks. |
| `withdrawal.broadcast.v1` | withdrawal | withdrawal service | merchant, exchange, operator diagnostics | no | Withdrawal transaction was broadcast and awaits finality. |
| `withdrawal.finalized.v1` | withdrawal | withdrawal service | merchant, exchange, operator diagnostics | yes | Withdrawal completed on-chain. |
| `withdrawal.failed.v1` | withdrawal | withdrawal service | merchant, exchange, operator diagnostics | yes | Withdrawal failed, was rejected, or requires intervention. |
| `refund.requested.v1` | refund | refund service | merchant, exchange, operator diagnostics | no | Refund request was created. |
| `refund.broadcast.v1` | refund | refund service | merchant, exchange, operator diagnostics | no | Refund transaction was broadcast. |
| `refund.succeeded.v1` | refund | refund service | merchant, exchange, operator diagnostics | yes | Refund completed successfully. |
| `refund.rejected.v1` | refund | refund service | merchant, exchange, operator diagnostics | yes | Refund was rejected by policy or admin review. |
| `refund.failed.v1` | refund | refund service | merchant, exchange, operator diagnostics | yes | Refund failed before final completion. |
| `sweep.requested.v1` | sweep | sweep service | merchant, exchange, operator diagnostics | no | Sweep job was requested. |
| `sweep.succeeded.v1` | sweep | sweep service | merchant, exchange, operator diagnostics | yes | Sweep completed successfully. |
| `sweep.failed.v1` | sweep | sweep service | merchant, exchange, operator diagnostics | yes | Sweep failed and may be retried or reconciled. |
| `sweep.dead_lettered.v1` | sweep | sweep service | merchant, exchange, operator diagnostics | yes | Sweep exhausted retry policy and needs operator action. |
| `transaction.reorged.v1` | correction | chain indexer | merchant, exchange, operator diagnostics | yes | Prior transaction observation was invalidated or corrected by reorg handling. |
| `webhook.delivery.succeeded.v1` | webhook delivery | webhook service | merchant, exchange, operator diagnostics | yes | Delivery reached terminal success. |
| `webhook.delivery.failed.v1` | webhook delivery | webhook service | merchant, exchange, operator diagnostics | no | Delivery attempt failed and remains retryable. |
| `webhook.delivery.dead_lettered.v1` | webhook delivery | webhook service | merchant, exchange, operator diagnostics | yes | Delivery exhausted retry policy. |
| `webhook.delivery.replayed.v1` | webhook delivery | webhook service | merchant, exchange, operator diagnostics | no | Operator replay was requested for an existing event. |

## Compatibility Aliases

Legacy underscore event names remain supported until a published catalog migration announces retirement:

| Current emitted name | Catalog relation |
| --- | --- |
| `native_transfer` | Alias of `deposit.detected.v1`. |
| `transaction_reorged` | Alias of `transaction.reorged.v1`. |
| `payment_succeeded` | Alias of `payment.succeeded.v1`. |
| `payment_failed` | Alias of `payment.failed.v1`. |
| `payment_expired` | Alias of `payment.expired.v1`. |
| `payment_underpaid` | Alias of `payment.underpaid.v1`. |
| `payment_overpaid` | Alias of `payment.overpaid.v1`. |
| `payment_partial_paid` | Alias of `payment.partial_paid.v1`. |

The current implementation also emits `payout.*.v1` names for withdrawal lifecycle callbacks. They remain current compatibility aliases until a versioned withdrawal naming migration is published:

| Current emitted name | Canonical target |
| --- | --- |
| `payout.requested.v1` | `withdrawal.requested.v1` |
| `payout.broadcast.v1` | `withdrawal.broadcast.v1` |
| `payout.finalized.v1` | `withdrawal.finalized.v1` |
| `payout.rejected.v1` | `withdrawal.failed.v1` |
| `payout.failed.v1` | `withdrawal.failed.v1` |

No emitted event name should be removed silently. Removal requires a new catalog version, a migration note, and consumer lead time.

## Family Fields

Deposit events add chain and transaction context: `chain_id`, `tx_hash`, `tx_unique_hash`, `log_index`, `amount_raw`, `symbol`, `token`, source address, destination address, and confirmation count.

Payment events add checkout context: `payment_id`, `order_id`, amount/currency fields, optional transaction hash, and failure, expiry, or mismatch outcome reason when applicable. Explicit mismatch outcomes include `payment_outcome`, `matched_amount_raw`, `shortfall_amount_raw`, and `excess_amount_raw` where relevant. Reorg-corrected `payment.failed.v1` payloads also include `original_event_id`, `original_resource_id`, and `correction_reason`.

Withdrawal, refund, and sweep events add resource-specific money movement fields: wallet/resource id, chain/symbol/token, integer raw amount, destination address when applicable, transaction hash when broadcast, and failure/rejection reason when terminal failure occurs.

Webhook delivery events add delivery id, target URL, attempt count, retry timing, replay reason, or operator action fields.

## Corrections And Reorgs

`transaction.reorged.v1` is a correction event. It includes `original_event_id`, `original_resource_id`, and `correction_reason` so consumers can relate the correction to previously processed state. When a reorg corrects a payment lifecycle to failed, the `payment.failed.v1` payload carries the same relation fields and points back to the prior payment lifecycle event where it can be derived from the preserved matching outcome.

Correction handling is non-destructive: prior event history remains immutable. Consumers should apply the correction by appending a compensating state transition, not by deleting prior event records.

## Example

```json
{
  "event_id": "payment_uuid:payment.succeeded.v1",
  "event_type": "payment.succeeded.v1",
  "event_version": "v1",
  "occurred_at": "2026-06-27T12:00:00Z",
  "merchant_id": "merchant_uuid",
  "domain_id": "domain_uuid",
  "resource_type": "payment",
  "resource_id": "payment_uuid",
  "resource_status": "succeeded",
  "idempotency_key": "payment_uuid:payment.succeeded.v1",
  "correlation_id": "corr_payment_uuid",
  "payment_id": "payment_uuid",
  "order_id": "ORDER-1001",
  "amount": "25.00",
  "currency": "USD",
  "tx_hash": "0xhash",
  "tx_unique_hash": "1-0xhash-log:0"
}
```
