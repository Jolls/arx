#!/usr/bin/env bash
# Prints the full Postgres schema + test seed as one SQL script, for a fresh database:
#   bash SQL/postgres/build_schema.sh | psql "$DSN" -q -v ON_ERROR_STOP=1
# Used by the postgres-integration CI job (#19). FKs form cycles (company <-> contact,
# part <-> part_attachment), so no per-file order works: tables load first with their
# single-line `ALTER TABLE ... FOREIGN KEY` statements held back, then all FKs, then
# triggers and seed data. Inline REFERENCES only point at tables earlier in this list.
set -euo pipefail
cd "$(dirname "$0")"

tables=(
  uom contact company_attachment company part_category part mfg_part supplier_part price bom
  attachment_category part_attachment purchase_order po_line inventory_transaction build lot unit
  form form_row form_record result genealogy form_row_history form_events
  record_events record_event_results app_config named_queries users
  schema_migrations
)
fk='^ALTER TABLE .*FOREIGN KEY'

for t in "${tables[@]}"; do grep -vE "$fk" "$t.sql"; echo; done
for t in "${tables[@]}"; do grep -hE "$fk" "$t.sql" || true; done
for f in triggers seed_test_data seed_company_logo; do cat "$f.sql"; echo; done
