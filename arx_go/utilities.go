package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"arx/arxlib/urlutil"
)

// utilCheck is one diagnostic on the Settings → Utilities page: a titled group of
// offending rows. Count == 0 means the check passed. All checks are read-only.
type utilCheck struct {
	Title string
	Desc  string
	Count int
	Rows  []utilRow
}

// utilRow is one flagged item: a clickable label and a human-readable problem.
type utilRow struct {
	Label  string // primary identifier (part number, PO number, company name)
	Detail string // what's wrong
	URL    string // link to the owning record's detail page ("" = no link)
}

// UtilitiesReport renders the read-only data-integrity dashboard (GET
// /settings/utilities). Each check runs independently; a failing check is
// reported without aborting the others.
func (h *Handler) UtilitiesReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var checks []utilCheck
	var errs []string

	add := func(c utilCheck, err error) {
		if err != nil {
			errs = append(errs, c.Title+": "+err.Error())
			return
		}
		checks = append(checks, c)
	}
	add(h.checkDeadLinks(ctx))
	add(h.checkOrphanPointers(ctx))
	add(h.checkSoftDeletedAttachmentPointers(ctx))
	add(h.checkPOActiveDrift(ctx))

	h.render(w, r, "settings/utilities.html", map[string]any{
		"ActiveTab": "settings",
		"TestMode":  h.cfg.TestMode,
		"Checks":    checks,
		"Errors":    errs,
	})
}

// deadLocalTarget reports whether a LOCAL: attachment link points at a file or
// directory that is missing (or the wrong type) under root. It returns a reason
// and true when the target is dead. http links, absolute paths, and empty values
// are not checked (dead == false). A type mismatch (file link → folder, or vice
// versa) is flagged because it fails to serve at runtime the same way a missing
// file does (see ServeLocalFile / ServeLocalDir in files.go).
func deadLocalTarget(root, link string) (reason string, dead bool) {
	if root == "" || !urlutil.IsLocalFile(link) {
		return "", false
	}
	rel := strings.ReplaceAll(urlutil.StripLocalPrefix(link), "\\", "/")
	path, ok := safePath(root, rel)
	if !ok {
		return "unresolvable path: " + link, true
	}
	info, err := os.Stat(path)
	wantDir := urlutil.IsLocalDir(link)
	switch {
	case os.IsNotExist(err):
		return "missing on disk: " + link, true
	case err != nil:
		return "unreadable: " + link, true
	case wantDir && !info.IsDir():
		return "link is a folder but target is a file: " + link, true
	case !wantDir && info.IsDir():
		return "link is a file but target is a folder: " + link, true
	}
	return "", false
}

// checkDeadLinks flags active attachments whose LOCAL: file/folder is missing.
// Part attachments resolve against DOC_CONTROL_ROOT; company attachments against
// SUPPLIER_FILES_ROOT (falling back to DOC_CONTROL_ROOT), mirroring how the app
// serves each (see ServeLocalFile vs ServeSupplierFile).
func (h *Handler) checkDeadLinks(ctx context.Context) (utilCheck, error) {
	check := utilCheck{
		Title: "Dead attachment file links",
		Desc:  "Active attachments whose LOCAL: file or folder no longer exists on disk. External http(s) links are not checked.",
	}

	// scan runs a query returning (link, id, label) and flags rows whose LOCAL:
	// target is dead under root, linking each to urlPrefix+id.
	scan := func(query, root, urlPrefix string) error {
		rows, err := h.queryContext(ctx, query)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var link, label string
			var id int
			if err := rows.Scan(&link, &id, &label); err != nil {
				return err
			}
			if reason, dead := deadLocalTarget(root, link); dead {
				check.Rows = append(check.Rows, utilRow{
					Label:  label,
					Detail: reason,
					URL:    urlPrefix + strconv.Itoa(id),
				})
			}
		}
		return rows.Err()
	}

	// Part attachments resolve against DOC_CONTROL_ROOT.
	partQuery := fmt.Sprintf(
		`SELECT f.file_name, p.id, p.part_number
		 FROM %s f JOIN %s p ON f.part_id = p.id
		 WHERE f.is_active = %s`,
		h.cfg.AttachmentsTable(), h.cfg.PartsTable(), h.dia().BoolLiteral(true))
	if err := scan(partQuery, h.cfg.DocControlRoot, "/part/"); err != nil {
		return check, err
	}

	// Company attachments resolve against SUPPLIER_FILES_ROOT (fallback DOC_CONTROL_ROOT).
	supplierRoot := h.cfg.SupplierFilesRoot
	if supplierRoot == "" {
		supplierRoot = h.cfg.DocControlRoot
	}
	compQuery := fmt.Sprintf(
		`SELECT a.file_path, c.id, c.name
		 FROM %s a JOIN %s c ON a.supplier_id = c.id
		 WHERE a.is_active = %s`,
		h.cfg.CompanyAttachmentsTable(), h.cfg.CompanyTable(), h.dia().BoolLiteral(true))
	if err := scan(compQuery, supplierRoot, "/supplier/"); err != nil {
		return check, err
	}

	check.Count = len(check.Rows)
	return check, nil
}

// checkOrphanPointers flags parts whose soft-FK pointer references a row that no
// longer exists. These columns have no DB-enforced FK (see #213). Unset values
// use a sentinel of 0 (or NULL for default_supplier_id); the `> 0` guard excludes
// both, so an unset pointer is never reported as dangling.
func (h *Handler) checkOrphanPointers(ctx context.Context) (utilCheck, error) {
	check := utilCheck{
		Title: "Orphaned part pointers",
		Desc:  "Parts whose supplier / price / primary-attachment pointer references a row that no longer exists.",
	}
	specs := []struct{ column, target, targetTable string }{
		{"default_supplier_id", "company", h.cfg.CompanyTable()},
		{"price_id", "price", h.cfg.PriceTable()},
		{"primary_attachment_id", "part_attachment", h.cfg.AttachmentsTable()},
	}
	for _, s := range specs {
		q := fmt.Sprintf(
			`SELECT p.id, p.part_number, p.%[1]s
			 FROM %[2]s p
			 WHERE p.%[1]s > 0
			   AND NOT EXISTS (SELECT 1 FROM %[3]s t WHERE t.id = p.%[1]s)`,
			s.column, h.cfg.PartsTable(), s.targetTable)
		rows, err := h.queryContext(ctx, q)
		if err != nil {
			return check, err
		}
		for rows.Next() {
			var partID, value int
			var partNumber string
			if err := rows.Scan(&partID, &partNumber, &value); err != nil {
				rows.Close()
				return check, err
			}
			check.Rows = append(check.Rows, utilRow{
				Label:  partNumber,
				Detail: fmt.Sprintf("%s → missing %s #%d", s.column, s.target, value),
				URL:    fmt.Sprintf("/part/%d", partID),
			})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return check, err
		}
		rows.Close()
	}
	check.Count = len(check.Rows)
	return check, nil
}

// checkSoftDeletedAttachmentPointers flags parts/companies whose primary
// attachment points at an attachment row that still exists but is soft-deleted
// (is_active = 0). Distinct from checkOrphanPointers, which catches rows that are
// gone entirely; the enforced FK on company.primary_attachment_id prevents the
// "gone entirely" case there but not this one.
func (h *Handler) checkSoftDeletedAttachmentPointers(ctx context.Context) (utilCheck, error) {
	check := utilCheck{
		Title: "Primary attachment points at a deleted attachment",
		Desc:  "Parts or companies whose primary attachment references an attachment that has been soft-deleted (is_active = 0).",
	}

	// scan runs a query returning (id, label) and flags every row, linking each
	// to urlPrefix+id.
	scan := func(query, urlPrefix string) error {
		rows, err := h.queryContext(ctx, query)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id int
			var label string
			if err := rows.Scan(&id, &label); err != nil {
				return err
			}
			check.Rows = append(check.Rows, utilRow{
				Label:  label,
				Detail: "primary attachment is soft-deleted",
				URL:    urlPrefix + strconv.Itoa(id),
			})
		}
		return rows.Err()
	}

	partQuery := fmt.Sprintf(
		`SELECT p.id, p.part_number
		 FROM %[1]s p JOIN %[2]s f ON f.id = p.primary_attachment_id
		 WHERE p.primary_attachment_id > 0 AND f.is_active = %[3]s`,
		h.cfg.PartsTable(), h.cfg.AttachmentsTable(), h.dia().BoolLiteral(false))
	if err := scan(partQuery, "/part/"); err != nil {
		return check, err
	}

	compQuery := fmt.Sprintf(
		`SELECT c.id, c.name
		 FROM %[1]s c JOIN %[2]s a ON a.supplier_attachment_id = c.primary_attachment_id
		 WHERE c.primary_attachment_id > 0 AND a.is_active = %[3]s`,
		h.cfg.CompanyTable(), h.cfg.CompanyAttachmentsTable(), h.dia().BoolLiteral(false))
	if err := scan(compQuery, "/supplier/"); err != nil {
		return check, err
	}

	check.Count = len(check.Rows)
	return check, nil
}

// checkPOActiveDrift flags POs whose is_active flag disagrees with what status
// implies. status is authoritative; is_active is an app-maintained convenience
// bit. Reuses statusIsActive so the expected mapping stays in one place.
func (h *Handler) checkPOActiveDrift(ctx context.Context) (utilCheck, error) {
	check := utilCheck{
		Title: "Purchase order is_active drift",
		Desc:  "POs whose is_active flag disagrees with their status. status is authoritative; is_active should be kept in sync by the app.",
	}
	rows, err := h.queryContext(ctx, fmt.Sprintf(
		`SELECT id, number, status, is_active FROM %s`, h.cfg.POTable()))
	if err != nil {
		return check, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		var number, status string
		var isActive bool
		if err := rows.Scan(&id, &number, &status, &isActive); err != nil {
			return check, err
		}
		if want := statusIsActive(status); isActive != want {
			check.Rows = append(check.Rows, utilRow{
				Label:  number,
				Detail: fmt.Sprintf("status %q implies is_active=%v, but stored value is %v", status, want, isActive),
				URL:    fmt.Sprintf("/po/%d", id),
			})
		}
	}
	if err := rows.Err(); err != nil {
		return check, err
	}
	check.Count = len(check.Rows)
	return check, nil
}
