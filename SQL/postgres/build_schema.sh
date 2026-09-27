#!/usr/bin/env bash
# Prints the full Postgres schema + test seed + migration-ledger baseline as one SQL script, for a fresh database:
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

# Ledger baseline (#91): the reference DDL already includes every migration, so record
# goose's version 0 and each migration file as applied. Guarded, because re-running this
# script keeps schema_migrations (its DDL has no DROP).
versions="(0)"
for f in migrations/*.sql; do b=${f##*/}; versions+=", (${b%%_*})"; done
echo "INSERT INTO schema_migrations (version_id, is_applied)"
echo "SELECT v, TRUE FROM (VALUES $versions) t(v)"
echo "WHERE NOT EXISTS (SELECT 1 FROM schema_migrations s WHERE s.version_id = t.v);"
