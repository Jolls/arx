# Roadmap

Near-term plan, organized by milestone. Status is tracked on the [milestones](https://github.com/Jolls/arx/milestones) and issues themselves; this file is the high-level view. Long-term direction lives in [`docs/FUTURE_GOALS.md`](docs/FUTURE_GOALS.md).

Plans can change; nothing here is a commitment.

## v0.8.0 — Postgres and platform

Move off SQL Server/Azure to Postgres, and make self-hosting practical.

- Postgres support ([#21](https://github.com/Jolls/arx/issues/21)): dialect gaps ([#32](https://github.com/Jolls/arx/issues/32)), serial allocation ([#33](https://github.com/Jolls/arx/issues/33)), end-to-end verification ([#28](https://github.com/Jolls/arx/issues/28)), then cutover to Postgres-only ([#29](https://github.com/Jolls/arx/issues/29))
- Package Postgres as a StartOS service ([#24](https://github.com/Jolls/arx/issues/24))
- Backup/restore ([#26](https://github.com/Jolls/arx/issues/26))
- Cleanup: Arx header icon ([#22](https://github.com/Jolls/arx/issues/22))

## v0.9.0 — Parts, sourcing, and audit

New user-facing features for the parts catalog and supplier layer.

- **Parts:** alternate/substitute parts ([#7](https://github.com/Jolls/arx/issues/7)), revision history / ECO log ([#8](https://github.com/Jolls/arx/issues/8)), ECO process ([#1](https://github.com/Jolls/arx/issues/1)), RoHS/compliance flags ([#13](https://github.com/Jolls/arx/issues/13)), CSV import ([#10](https://github.com/Jolls/arx/issues/10))
- **Sourcing:** AVL attributes on sourcing links ([#4](https://github.com/Jolls/arx/issues/4))
- **Audit:** change log ([#12](https://github.com/Jolls/arx/issues/12)), multi-table audit trigger design ([#25](https://github.com/Jolls/arx/issues/25))
- **Test records:** result snapshots as revision-controlled definitions ([#18](https://github.com/Jolls/arx/issues/18))
- **Schema/infra:** flexible part user fields ([#14](https://github.com/Jolls/arx/issues/14))

## Later (unscheduled)

- Purchasing: one-click draft PO for below-reorder parts ([#23](https://github.com/Jolls/arx/issues/23)), lead time per PO line ([#6](https://github.com/Jolls/arx/issues/6)), supplier performance metrics ([#5](https://github.com/Jolls/arx/issues/5))
- Catalog: custom field labels ([#9](https://github.com/Jolls/arx/issues/9)), barcode/QR labels ([#11](https://github.com/Jolls/arx/issues/11))
- Reporting/search: yield dashboard ([#3](https://github.com/Jolls/arx/issues/3)), global record search ([#2](https://github.com/Jolls/arx/issues/2))
- Access: JSON API ([#15](https://github.com/Jolls/arx/issues/15)), mobile-responsive UI ([#16](https://github.com/Jolls/arx/issues/16))
- Migration runner instead of hand-run scripts ([#91](https://github.com/Jolls/arx/issues/91))

## Before going public

Repo hardening, tracked in [#162](https://github.com/Jolls/arx/issues/162): secret scanning ([#144](https://github.com/Jolls/arx/issues/144)), private vulnerability reporting ([#143](https://github.com/Jolls/arx/issues/143)), rulesets and Dependabot ([#166](https://github.com/Jolls/arx/issues/166)).
