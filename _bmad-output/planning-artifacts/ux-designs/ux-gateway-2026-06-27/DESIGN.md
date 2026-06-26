---
name: Gateway Ops
status: final
description: Visual system for the Gateway merchant checkout, dealer/admin operations, webhook diagnostics, reconciliation, and launch-readiness surfaces.
sources:
  - ../../prd.md
  - ../../epics.md
  - ../../implementation-readiness-report-2026-06-27.md
  - ../../architecture/architecture-gateway-2026-06-26/ARCHITECTURE-SPINE.md
updated: 2026-06-27
colors:
  ink: '#131313'
  ink-muted: '#5F5F63'
  ink-subtle: '#8A8A91'
  surface: '#FFFFFF'
  surface-wash: '#F7F8FA'
  surface-raised: '#FFFFFF'
  line: '#E5E7EB'
  line-soft: '#EEF0F3'
  accent: '#C026D3'
  accent-hover: '#A21CAF'
  accent-soft: '#FAE8FF'
  success: '#047857'
  success-soft: '#ECFDF5'
  warning: '#B45309'
  warning-soft: '#FFFBEB'
  danger: '#B91C1C'
  danger-soft: '#FEF2F2'
  info: '#1D4ED8'
  info-soft: '#EFF6FF'
typography:
  page-title:
    fontFamily: 'Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, Segoe UI, sans-serif'
    fontSize: 28px
    fontWeight: '760'
    lineHeight: '1.18'
    letterSpacing: '0'
  section-title:
    fontFamily: 'Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, Segoe UI, sans-serif'
    fontSize: 18px
    fontWeight: '720'
    lineHeight: '1.3'
    letterSpacing: '0'
  body:
    fontFamily: 'Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, Segoe UI, sans-serif'
    fontSize: 15px
    fontWeight: '400'
    lineHeight: '1.55'
    letterSpacing: '0'
  label:
    fontFamily: 'Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, Segoe UI, sans-serif'
    fontSize: 13px
    fontWeight: '650'
    lineHeight: '1.35'
    letterSpacing: '0'
  meta:
    fontFamily: 'Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, Segoe UI, sans-serif'
    fontSize: 12px
    fontWeight: '500'
    lineHeight: '1.4'
    letterSpacing: '0'
rounded:
  sm: 4px
  md: 6px
  lg: 8px
  full: 999px
spacing:
  '1': 4px
  '2': 8px
  '3': 12px
  '4': 16px
  '5': 20px
  '6': 24px
  '8': 32px
  '10': 40px
  page-x-mobile: 16px
  page-x-desktop: 32px
components:
  button-primary:
    background: '{colors.accent}'
    foreground: '#FFFFFF'
    radius: '{rounded.md}'
  button-secondary:
    background: '{colors.surface}'
    foreground: '{colors.ink}'
    border: '{colors.line}'
    radius: '{rounded.md}'
  status-success:
    background: '{colors.success-soft}'
    foreground: '{colors.success}'
    radius: '{rounded.full}'
  status-warning:
    background: '{colors.warning-soft}'
    foreground: '{colors.warning}'
    radius: '{rounded.full}'
  status-danger:
    background: '{colors.danger-soft}'
    foreground: '{colors.danger}'
    radius: '{rounded.full}'
  status-info:
    background: '{colors.info-soft}'
    foreground: '{colors.info}'
    radius: '{rounded.full}'
  panel:
    background: '{colors.surface-raised}'
    border: '{colors.line}'
    radius: '{rounded.lg}'
---

# Gateway Ops - Design Spine

## Brand & Style

Gateway Ops is a work-focused financial operations interface. It should feel precise, quiet, and inspectable. The product handles money state, webhook recovery, withdrawal approval, and reconciliation; the visual system must prioritize scan speed and confidence over decorative expression.

The existing product uses server-rendered HTML templates, Tailwind, Inter/system fonts, light surfaces, and a magenta accent. This design spine keeps that foundation but constrains the operational surfaces: dense information, clear status colors, explicit audit context, restrained panels, and no marketing-style hero composition inside app workflows.

## Colors

- **Ink (`{colors.ink}`)** is the default text color for labels, amounts, IDs, and operator decisions.
- **Muted ink (`{colors.ink-muted}`)** is for timestamps, secondary metadata, and helper text.
- **Surface wash (`{colors.surface-wash}`)** is the page background for admin/dealer/ops surfaces.
- **Accent (`{colors.accent}`)** is reserved for primary actions and active navigation, not for status meaning.
- **Success / warning / danger / info** colors carry money-path state. They must never be replaced with accent.
- **Line tokens** separate tables, forms, and log rows. Use borders before shadows.

Avoid new decorative gradients, glow effects, bokeh/orb backgrounds, and accent-dominated dashboards. Existing checkout pages may keep their current accent language, but new operator views should be utilitarian.

## Typography

Use Inter/system sans throughout. No negative letter spacing. Page titles are compact and factual; avoid hero-scale text inside authenticated app surfaces. Amounts, hashes, wallet IDs, request IDs, and event IDs use tabular-number behavior where available.

Use `page-title` only once per surface. Use `section-title` inside panels and detail sections. Use `label` for form labels, table headers, filters, and status legends. Use `meta` for timestamps, correlation IDs, and attempt metadata.

## Layout & Spacing

Operational surfaces use a constrained responsive shell:

- Desktop: persistent top/header area, optional left navigation for admin/dealer sections, main content max width based on task. Dashboards can use full-width tables inside the shell.
- Tablet/mobile: top navigation collapses; tables become stacked rows or horizontally scrollable only when data density requires it.
- Page horizontal padding: `{spacing.page-x-mobile}` on mobile, `{spacing.page-x-desktop}` on desktop.
- Section spacing follows the 4px scale; common gaps are 8, 12, 16, 24, and 32px.

Do not put cards inside cards. Use panels for bounded tools, tables, forms, and repeated list items only. Page sections should be unframed bands or direct shell content.

## Elevation & Depth

Use borders and tonal surfaces first. Shadows are allowed only for popovers, dialogs, dropdowns, and sticky bars that must separate from content. Avoid decorative shadow glow on operator panels because it weakens scan clarity.

## Shapes

Cards/panels/dialogs use `{rounded.lg}` at most. Buttons and inputs use `{rounded.md}`. Status pills may use `{rounded.full}` because the pill shape communicates compact state, not layout framing.

## Components

- **Primary button** — Accent fill, white text, 40px minimum height. Used for one primary command per local decision area.
- **Secondary button** — White fill, line border, ink text. Used for navigation, cancel, copy, and low-risk commands.
- **Danger button** — Danger fill or danger text in a confirmation dialog only. Never use for passive status.
- **Status pill** — Success/warning/danger/info soft background. Must include visible text, not color alone.
- **Money table** — Dense rows, sticky header on long lists, row hover only on pointer devices, no zebra striping unless line contrast is insufficient.
- **Detail panel** — 8px radius, 1px border, section title, compact metadata grid, optional action row.
- **Audit row** — Timestamp, actor, scope, action, outcome, correlation id. Never hide audit metadata behind hover.
- **Checkout payment module** — Asset, network, amount, address, QR, expiry, and status must remain visible without overlap on mobile.
- **Webhook attempt row** — Event id, attempt number, status, next attempt, HTTP result, latency, redacted response preview, replay command.
- **Reconciliation job row** — Reason, scope, affected resources, severity, status, opened time, owner, next action.

## Do's and Don'ts

| Do | Don't |
| --- | --- |
| Use status colors for money-path state | Use magenta accent to mean success, warning, or danger |
| Keep operator screens dense but aligned | Use large marketing cards for admin workflows |
| Show IDs, timestamps, scope, and actor near every risky action | Hide audit context behind hover or secondary pages |
| Use borders and typography for hierarchy | Add decorative glows or gradient backgrounds |
| Make checkout mobile-safe and copyable | Let QR/address/status overlap or resize unpredictably |
| Redact secrets in every diagnostic surface | Display API secrets, mnemonics, raw signatures, or private keys |
