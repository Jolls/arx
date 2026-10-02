#!/usr/bin/env bash
# Init script for compose.yml (#300): loads schema, triggers, seed and ledger baseline into $POSTGRES_DB.
set -euo pipefail
bash /arx-sql/build_schema.sh | PGOPTIONS=--client-min-messages=warning psql -U postgres -d "$POSTGRES_DB" -q -v ON_ERROR_STOP=1
