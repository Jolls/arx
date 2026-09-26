package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// legacyMigrations are the pre-#48 files: never renamed, never registered in
// schema_migrations. Do not add to this list — new migrations use the
// YYYYMMDDHHMMSS_<issue>_<desc>.sql name and register themselves.
var legacyMigrations = map[string]bool{
	"migrate_40_part_description.sql":                     true,
	"migrate_56_part_attachment_vendor_scope.sql":         true,
	"migrate_71_attachment_hash.sql":                      true,
	"migrate_735a_null_fk_promotion.sql":                  true,
	"migrate_735b_price_id_fk_promotion.sql":              true,
	"migrate_735c_primary_attachment_id_fk_promotion.sql": true,
	"migrate_740_unit_table.sql":                          true,
	"migrate_741_genealogy_table.sql":                     true,
	"migrate_742_form_record_unit_fk.sql":                 true,
	"migrate_743_part_tracking_mode.sql":                  true,
	"migrate_744_enum_formalization.sql":                  true,
	"migrate_746_genealogy_trace_indexes.sql":             true,
	"migrate_750_users_is_admin.sql":                      true,
	"migrate_769_traceability_renames.sql":                true,
	"migrate_794_named_queries_drop_alias.sql":            true,
	"migrate_799_unit_source.sql":                         true,
	"migrate_80_supplier_bulk_order_options.sql":          true,
	"migrate_847_users_timezone.sql":                      true,
	"migrate_872_lot_and_record_notes.sql":                true,
	"migrate_874_form_record_type_rename.sql":             true,
	"migrate_drop_contact_user_account_link.sql":          true,
	"migrate_drop_has_bom.sql":                            true,
	"migrate_max_subbatch_result_param_rename.sql":        true,
	"migrate_release_status_check.sql":                    true,
	"migrate_rename_named_queries_active.sql":             true,
	"migrate_rename_test_record_family.sql":               true,
	"migrate_rename_uom.sql":                              true,
	"migrate_schema_v4.sql":                               true,
}

var (
	migrationName   = regexp.MustCompile(`^(\d{14})_\d+_[a-z0-9_]+\.sql$`)
	migrationGuard  = regexp.MustCompile(`(?i)FROM\s+dbo\.schema_migrations\s+WHERE\s+version_id\s*=\s*(\d+)`)
	migrationInsert = regexp.MustCompile(`(?i)INSERT\s+INTO\s+dbo\.schema_migrations\s*\(version_id,\s*is_applied\)\s*VALUES\s*\((\d+),\s*1\)`)

	pgMigrationGuard  = regexp.MustCompile(`(?i)FROM\s+schema_migrations\s+WHERE\s+version_id\s*=\s*(\d+)`)
	pgMigrationInsert = regexp.MustCompile(`(?i)INSERT\s+INTO\s+schema_migrations\s*\(version_id,\s*is_applied\)\s*SELECT\s+(\d+),\s*TRUE`)
)

// TestMigrationsSelfRegister guards against a migration that changes the schema
// but never records itself in schema_migrations (#48, cf. #40). Covers both the
// T-SQL set (SQL/azure/migrations) and the Postgres set (SQL/postgres/migrations).
func TestMigrationsSelfRegister(t *testing.T) {
	for _, set := range []struct {
		name          string
		dir           string
		guard, insert *regexp.Regexp
	}{
		{"azure", filepath.Join("..", "SQL", "azure", "migrations"), migrationGuard, migrationInsert},
		{"postgres", filepath.Join("..", "SQL", "postgres", "migrations"), pgMigrationGuard, pgMigrationInsert},
	} {
		t.Run(set.name, func(t *testing.T) {
			entries, err := os.ReadDir(set.dir)
			if err != nil {
				t.Fatal(err)
			}
			seen := map[string]string{}
			for _, e := range entries {
				name := e.Name()
				if e.IsDir() || !strings.HasSuffix(name, ".sql") || legacyMigrations[name] {
					continue
				}
				m := migrationName.FindStringSubmatch(name)
				if m == nil {
					t.Errorf("%s: name must be YYYYMMDDHHMMSS_<issue>_<description>.sql", name)
					continue
				}
				version := m[1]
				if prev, dup := seen[version]; dup {
					t.Errorf("%s: version %s already used by %s", name, version, prev)
				}
				seen[version] = name

				body, err := os.ReadFile(filepath.Join(set.dir, name))
				if err != nil {
					t.Fatal(err)
				}
				for label, re := range map[string]*regexp.Regexp{"NOT EXISTS guard": set.guard, "INSERT": set.insert} {
					g := re.FindStringSubmatch(string(body))
					if g == nil {
						t.Errorf("%s: missing schema_migrations %s", name, label)
					} else if g[1] != version {
						t.Errorf("%s: %s registers version %s, filename says %s", name, label, g[1], version)
					}
				}
			}
		})
	}
}
