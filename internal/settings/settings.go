// Package settings is the Settings-page data layer (#190, #247): dropdown options, the attachment
// category list and the backup table read.
package settings

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

	"arx/internal/dbq"
)

// Option is one id/name dropdown entry.
type Option struct {
	ID   int
	Name string
}

// BackupTables is the allowlist of tables the backup exports, in export order. users and
// app_config are exported separately (column / row filtering) and are not in the list.
var BackupTables = []string{
	"part", "bom", "company", "contact", "purchase_order", "po_line",
	"part_attachment", "price",
	"mfg_part", "supplier_part", "company_attachment",
	"uom",
	"form", "form_record", "result",
	"form_row", "form_events", "record_events",
	"named_queries", "form_row_history",
	"inventory_transaction", "build", "lot", "unit",
	"genealogy", "purchase_order_history", "record_event_results",
	"part_category", "attachment_category",
}

// BackupUsers and BackupAppConfig are the two tables the backup filters before writing.
const (
	BackupUsers     = "users"
	BackupAppConfig = "app_config"
)

type Service struct {
	q  *dbq.Queries
	db dbq.DBTX
}

func New(db dbq.DBTX) *Service { return &Service{q: dbq.New(db), db: db} }

// ContactOptions returns active contacts by display name; companyID > 0 scopes to that company's
// contacts, 0 returns all of them.
func (s *Service) ContactOptions(ctx context.Context, companyID int) ([]Option, error) {
	rows, err := s.q.ListContactOptions(ctx, companyID)
	if err != nil {
		return nil, err
	}
	out := make([]Option, 0, len(rows))
	for _, r := range rows {
		out = append(out, Option{ID: r.ID, Name: r.DisplayName})
	}
	return out, nil
}

// SupplierOptions returns active companies by name.
func (s *Service) SupplierOptions(ctx context.Context) ([]Option, error) {
	rows, err := s.q.ListSupplierOptions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Option, 0, len(rows))
	for _, r := range rows {
		out = append(out, Option{ID: r.ID, Name: r.Name})
	}
	return out, nil
}

// AttachmentCategories returns the attachment Category dropdown options in order.
func (s *Service) AttachmentCategories(ctx context.Context) ([]string, error) {
	return s.q.ListAttachmentCategories(ctx)
}

// ReplaceAttachmentCategories replaces the list with cats in order, keeping the first of any
// repeated name. The caller owns the transaction (build the service on the tx).
func (s *Service) ReplaceAttachmentCategories(ctx context.Context, cats []string) error {
	if err := s.q.DeleteAttachmentCategories(ctx); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, c := range cats {
		if seen[c] {
			continue
		}
		seen[c] = true
		if err := s.q.InsertAttachmentCategory(ctx, dbq.InsertAttachmentCategoryParams{DisplayName: c, SortOrder: len(seen) - 1}); err != nil {
			return err
		}
	}
	return nil
}

// QueryTable reads every row of one backup table. sqlc cannot take a table name, so this is the one
// raw statement here; the name is checked against BackupTables (plus users / app_config) first, so
// nothing caller-supplied ever reaches the SQL text.
func (s *Service) QueryTable(ctx context.Context, table string) (*sql.Rows, error) {
	if table != BackupUsers && table != BackupAppConfig && !slices.Contains(BackupTables, table) {
		return nil, fmt.Errorf("settings: %q is not a backup table", table)
	}
	return s.db.QueryContext(ctx, "SELECT * FROM "+table)
}
