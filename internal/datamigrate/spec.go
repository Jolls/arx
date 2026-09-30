// Package datamigrate loads a CSV export of the Azure SQL ArxProd database (schema
// version 10, release/0.7) into a Postgres database built from SQL/postgres (#216). It
// backs the console command arx_go/cmd/migrate_data; the runbook is
// docs/216-data-migration/runbook.md.
//
// Everything the tool knows about the two schemas lives in this file. Nothing is
// dropped silently: a source column the target lacks that is not listed in
// droppedColumns is an error.
package datamigrate

import "strings"

// NullMarker stands for SQL NULL in the exported CSVs and in the COPY stream.
const NullMarker = `\N`

// Names of the source clocks a datetime column can have been written by.
const (
	serverClock  = "server"  // the SQL Server's own clock (column default): UTC on Azure SQL
	desktopClock = "desktop" // Go time.Now() parameter: the desktop's wall clock
)

// skipTables are source tables that are not loaded: dead tables dropped by #31, and the
// migration ledger, which the target builds for itself.
var skipTables = map[string]bool{"logs": true, "release_notes": true, "schema_migrations": true}

// derivedTables exist only in the target; migration 194 fills them from app_config.
var derivedTables = map[string]bool{"part_category": true, "attachment_category": true}

// columnRenames maps source column -> target column (migration 31).
var columnRenames = map[string]map[string]string{
	"company": {
		"SUNotes":        "notes",
		"SUNumOfLNKs":    "supplier_part_count",
		"SUNumOfPOs":     "po_count",
		"SUSupplierCode": "supplier_code",
	},
}

// droppedColumns are source columns with no target column (migration 31).
var droppedColumns = map[string]map[string]bool{"part": {"is_lot_tracked": true}}

// deferredColumns break the FK cycles (company <-> contact, company <-> company_attachment,
// part <-> price, part <-> part_attachment): loaded NULL, then set once every table is in.
// Each of these tables has an integer `id` primary key.
var deferredColumns = []tableColumn{
	{"company", "default_contact"}, {"company", "primary_attachment_id"},
	{"part", "primary_attachment_id"}, {"part", "price_id"},
}

// ignoredFKs are not enforced during the load: part.category -> part_category is
// repaired by migration 194, run after the load.
var ignoredFKs = map[tableColumn]bool{{"part", "category"}: true}

type tableColumn struct{ table, column string }

// timestampClocks classifies every datetime column that becomes timestamptz, per
// migration 192 (see the runbook's timezone table and #279). form_record.record_date
// is user-typed and stays a zoneless TIMESTAMP, so it is not listed.
var timestampClocks = map[tableColumn]string{
	{"app_config", "updated_at"}: serverClock, {"form_events", "event_date"}: serverClock,
	{"form_record", "created_at"}: serverClock, {"form_record", "updated_at"}: serverClock,
	{"form_row", "created_at"}: serverClock, {"form_row", "updated_at"}: serverClock,
	{"form_row_history", "changed_at"}: serverClock, {"record_events", "event_date"}: serverClock,
	{"result", "updated_at"}: serverClock, {"unit", "created_at"}: serverClock,
	{"users", "created_at"}: serverClock, {"users", "updated_at"}: serverClock,

	{"build", "created_at"}: desktopClock, {"company", "date_modified"}: desktopClock,
	{"contact", "updated_at"}: desktopClock, {"inventory_transaction", "created_at"}: desktopClock,
	{"lot", "created_at"}: desktopClock, {"named_queries", "created_at"}: desktopClock,
	{"named_queries", "updated_at"}: desktopClock, {"part", "last_rollup_at"}: desktopClock,
	{"purchase_order", "date_modified"}: desktopClock, {"purchase_order_history", "changed_at"}: desktopClock,
}

// sourceSchemaVersion is the only source schema the column maps and clock
// classifications above are valid for (read from the export's app_config).
const sourceSchemaVersion = "10"

// secretMarkers: an app_config key containing any of these is treated as a credential.
var secretMarkers = []string{"secret", "password", "token", "api_key", "apikey", "credential", "private_key", "client_id"}

// appConfigSkip says why an app_config row is not carried over ("" = carry it).
// Credentials must never travel; schema_version belongs to the target. The carried keys
// are printed, so a miss is visible.
func appConfigSkip(key string) string {
	k := strings.ToLower(key)
	if k == "schema_version" {
		return "target owns schema_version"
	}
	for _, m := range secretMarkers {
		if strings.Contains(k, m) {
			return "looks like a credential"
		}
	}
	return ""
}
