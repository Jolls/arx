# Roadmap

Plan and long-term direction, organized by horizon. Status is tracked on the [milestones](https://github.com/Jolls/arx/milestones) and issues themselves; this file is the high-level view. Read it before proposing structural or schema changes so decisions point toward the end state, not away from it. Issue numbers are `Jolls/arx` issues; work with no open issue is treated as shipped.

Plans can change; nothing here is a commitment.

## Vision

Arx is a lightweight parts, purchasing, and records system, not a large-ERP competitor. It serves small R&D teams replacing spreadsheet BOMs alongside a company ERP, and small companies that want one all-in-one package. It grows deeper in those jobs, not wider into full ERP scope.

- **Parts & purchasing:** catalog with compliance, revision history, alternates and custom fields; sourcing layer (AVL, lead times, supplier performance); inventory-driven purchasing; reporting and data exchange.
- **Records (test data & quality):** revision-controlled form definitions; analytics (yield dashboard, cross-form search).
- **Platform:** Postgres-only, self-hostable (StartOS), backup/restore; audit trail across all tables; JSON API and mobile-friendly UI.

## v0.8.0 — Postgres and platform

Move off SQL Server to Postgres, with ArxProd on Azure Database for PostgreSQL, and make self-hosting practical. Deployment stays a per-user `Arx.exe` ([#189](https://github.com/Jolls/arx/issues/189)).

- Package Postgres as a StartOS service for ArxDev and self-hosting ([#24](https://github.com/Jolls/arx/issues/24))

## v0.9.0 — Parts, sourcing, and audit

New user-facing features for the parts catalog and supplier layer.

- **Parts:** alternate/substitute parts ([#7](https://github.com/Jolls/arx/issues/7)), revision history / ECO log ([#8](https://github.com/Jolls/arx/issues/8)), ECO process ([#1](https://github.com/Jolls/arx/issues/1)), RoHS/compliance flags ([#13](https://github.com/Jolls/arx/issues/13)), CSV import ([#10](https://github.com/Jolls/arx/issues/10))
- **Sourcing:** AVL attributes on sourcing links ([#4](https://github.com/Jolls/arx/issues/4))
- **Audit:** change log ([#12](https://github.com/Jolls/arx/issues/12)), multi-table audit trigger design ([#25](https://github.com/Jolls/arx/issues/25))
- **Test records:** result snapshots as revision-controlled definitions ([#18](https://github.com/Jolls/arx/issues/18))
- **Schema/infra:** flexible part user fields ([#14](https://github.com/Jolls/arx/issues/14))
- **Ops:** backup/restore ([#26](https://github.com/Jolls/arx/issues/26))

## Later (unscheduled)

- Purchasing: one-click draft PO for below-reorder parts ([#23](https://github.com/Jolls/arx/issues/23)), lead time per PO line ([#6](https://github.com/Jolls/arx/issues/6)), supplier performance metrics ([#5](https://github.com/Jolls/arx/issues/5))
- Catalog: custom field labels ([#9](https://github.com/Jolls/arx/issues/9)), barcode/QR labels ([#11](https://github.com/Jolls/arx/issues/11))
- Reporting/search: yield dashboard ([#3](https://github.com/Jolls/arx/issues/3)), global record search ([#2](https://github.com/Jolls/arx/issues/2))
- Access: JSON API ([#15](https://github.com/Jolls/arx/issues/15))
- Platform: structured `app_config` instead of a flat key-value table ([#17](https://github.com/Jolls/arx/issues/17))

## Deferred decisions

- **Central Arx server instead of a per-user `Arx.exe`** ([#189](https://github.com/Jolls/arx/issues/189)) — current model is per-user exe + shared Postgres. Revisit if shop-floor tablets or the JSON API become a real need, the user base grows beyond trusted staff, or Graph API access becomes available (removes the service-account OneDrive box a server-side file store needs).
  - Cheaper hardening within the current model: Entra ID auth to Azure Postgres (no shared DB password); normalize absolute OneDrive/SharePoint attachment paths to root-relative `LOCAL:` form.

## Before going public

Repo hardening, tracked in [#162](https://github.com/Jolls/arx/issues/162): secret scanning ([#144](https://github.com/Jolls/arx/issues/144)), private vulnerability reporting ([#143](https://github.com/Jolls/arx/issues/143)), rulesets and Dependabot ([#166](https://github.com/Jolls/arx/issues/166)).
