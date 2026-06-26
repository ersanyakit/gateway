# Review - Adversarial Boundary Lens

Verdict: Pass with tightening.

Attack scenario:

- Team A builds Webhook with dotted events only.
- Team B keeps Payment emitting `payment_succeeded` and writes direct delivery retries.
- Both can claim they obeyed different readings of AD-5 and the current code reality.

Fix:

- Clarify that current underscore events remain compatibility aliases until an event catalog migration retires them.
- Clarify source modules enqueue and never deliver callbacks inline.

Second scenario:

- Team A treats PaymentSession `paid` as balance.
- Team B treats Ledger as balance.
- AD-3 already blocks this.

Third scenario:

- Team A retries sweep by broadcasting again.
- Team B retries by reconciling broadcast state first.
- AD-7 blocks blind retry.
