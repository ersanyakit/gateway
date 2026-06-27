package constants

const WebhookEventVersionV1 = "v1"

const (
	WebhookEventNativeTransfer      = "native_transfer"
	WebhookEventTransactionReorged  = "transaction_reorged"
	WebhookEventPaymentSucceeded    = "payment_succeeded"
	WebhookEventPaymentFailed       = "payment_failed"
	WebhookEventPaymentExpired      = "payment_expired"
	WebhookEventPaymentUnderpaid    = "payment_underpaid"
	WebhookEventPaymentOverpaid     = "payment_overpaid"
	WebhookEventPaymentPartialPaid  = "payment_partial_paid"
	WebhookEventPayoutRequestedV1   = "payout.requested.v1"
	WebhookEventPayoutBroadcastV1   = "payout.broadcast.v1"
	WebhookEventPayoutFinalizedV1   = "payout.finalized.v1"
	WebhookEventPayoutRejectedV1    = "payout.rejected.v1"
	WebhookEventPayoutFailedV1      = "payout.failed.v1"
	WebhookEventRefundRequestedV1   = "refund.requested.v1"
	WebhookEventRefundBroadcastV1   = "refund.broadcast.v1"
	WebhookEventRefundSucceededV1   = "refund.succeeded.v1"
	WebhookEventRefundRejectedV1    = "refund.rejected.v1"
	WebhookEventRefundFailedV1      = "refund.failed.v1"
	WebhookEventSweepRequestedV1    = "sweep.requested.v1"
	WebhookEventSweepSucceededV1    = "sweep.succeeded.v1"
	WebhookEventSweepFailedV1       = "sweep.failed.v1"
	WebhookEventSweepDeadLetteredV1 = "sweep.dead_lettered.v1"
)
