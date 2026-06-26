---
name: readiness-review
description: Review Gateway implementation readiness against the architecture spine and phase exit criteria.
---

# Readiness Review

The outcome is a clear readiness verdict for a phase, release, or capability.

Review against:

- Architecture Spine ADs
- Solution Design phase exit criteria
- ROADMAP blockers
- Current code behavior
- Tests, migrations, metrics, runbooks, and replay/recovery evidence

Verdict levels:

- `Ready`: evidence covers the phase exit criteria and no AD violation remains.
- `Conditionally ready`: limited rollout is acceptable with named constraints and monitoring.
- `Not ready`: money-safety, custody, reconciliation, webhook, or operational gaps remain.

Lead with findings, ordered by severity. Every finding should name:

- violated or under-evidenced AD
- affected capability
- file or artifact evidence when available
- required fix or acceptance evidence

Do not treat a checklist item as passed because a model exists. Money-state readiness requires behavior, tests, and operational evidence.
