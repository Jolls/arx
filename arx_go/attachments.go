package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"arx/arxlib/urlutil"
)

// illegalFileNameChars are characters not permitted in a Windows filename.
const illegalFileNameChars = `<>:"/\|?*`

// titleMaxLen caps the part title portion of a generated attachment filename.
const titleMaxLen = 20

// buildAttachmentFileName produces the base filename for an imported attachment:
// "<PartNumber> <Rev> <Title> <Category><ext>". Blank (or whitespace-only) parts
// are skipped so separators never double up, and the title is truncated to
// titleMaxLen. Each part is sanitised of filesystem-illegal characters, and the
// result is always a bare base name (no directory component), so it cannot
// escape the target folder.
//
// This is the sole implementation of the naming convention; the Browse live
// preview in templates/parts/part_attachments.html calls it via
// GET /api/part/{id}/attachment-name rather than duplicating the rule (#558).
func buildAttachmentFileName(partNumber, rev, title, category, ext string) string {
	title = truncateRunes(strings.TrimSpace(title), titleMaxLen)
	var parts []string
	for _, p := range []string{partNumber, rev, title, category} {
		if s := sanitizeFileNamePart(p); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " ") + ext
}

// buildResultImageName produces the base filename for a pasted test-record
// result image: "SN<serial>_rID<recordID>_tID<testID>_<timestamp><ext>".
// Keeps the legacy SN/rID/tID field order but drops the dashes VBA used
// between labels and values, so the write-time timestamp (the only source of
// uniqueness — repeat pastes are not de-duped/suffixed) doesn't visually blend
// with sanitizeFileNamePart's dash-for-illegal-char substitution.
func buildResultImageName(serial string, recordID, testID int, ext string) string {
	serial = sanitizeFileNamePart(serial)
	timestamp := time.Now().Format("20060102_150405")
	return fmt.Sprintf("SN%s_rID%d_tID%d_%s%s", serial, recordID, testID, timestamp, ext)
}

// truncateRunes returns s truncated to at most n runes.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// sanitizeFileNamePart trims a name part and replaces any filesystem-illegal
// character with '-'.
func sanitizeFileNamePart(s string) string {
	s = strings.TrimSpace(s)
	return strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune(illegalFileNameChars, r) {
			return '-'
		}
		return r
	}, s)
}

// copyIntoDocControl copies src to <root>/<name> without overwriting.
// If the target already exists it returns (true, nil) and does not copy.
// A copy failure returns an error and leaves no orphan target behind.
func copyIntoDocControl(root, name, src string) (existed bool, err error) {
	target := filepath.Join(root, name)
	dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		if os.IsExist(err) {
			return true, nil
		}
		return false, err
	}

	in, err := os.Open(src)
	if err != nil {
		dst.Close()
		os.Remove(target) // remove the empty target we just created
		return false, err
	}
	defer in.Close()

	// Close dst before any cleanup so os.Remove isn't blocked by an open handle
	// (Windows sharing violation).
	_, copyErr := io.Copy(dst, in)
	closeErr := dst.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(target)
		if copyErr != nil {
			return false, copyErr
		}
		return false, closeErr
	}
	return false, nil
}

// writeIntoDocControl writes data to <root>/<name> without overwriting.
// If the target already exists it returns (true, nil) and does not write.
// A write failure leaves no orphan target behind.
func writeIntoDocControl(root, name string, data []byte) (existed bool, err error) {
	target := filepath.Join(root, name)
	dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		if os.IsExist(err) {
			return true, nil
		}
		return false, err
	}

	_, writeErr := dst.Write(data)
	closeErr := dst.Close()
	if writeErr != nil || closeErr != nil {
		os.Remove(target)
		if writeErr != nil {
			return false, writeErr
		}
		return false, closeErr
	}
	return false, nil
}

// writeIntoDocControlUnique calls writeIntoDocControl, retrying with " (2)",
// " (3)", ... appended before ext on collision. name must already include ext.
func writeIntoDocControlUnique(root, name, ext string, data []byte) (finalName string, err error) {
	base := strings.TrimSuffix(name, ext)
	candidate := name
	for attempt := 1; attempt <= 20; attempt++ {
		existed, err := writeIntoDocControl(root, candidate, data)
		if err != nil {
			return "", err
		}
		if !existed {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s (%d)%s", base, attempt+1, ext)
	}
	return "", fmt.Errorf("could not find a unique name for %q after 20 attempts", name)
}

// attachmentFileInput carries the outcome of resolveAttachmentFileInput: the
// file_name value to store, an optional source file to remove afterward
// (Move mode), or a reason (error / import collision) the caller must
// surface to the user instead of saving.
type attachmentFileInput struct {
	FileName  string
	MoveSrc   string
	Collision map[string]string
	ErrMsg    string
}

// resolveAttachmentFileInput inspects the form for either a manual FILFileName
// value or a browse-import source_path, shared by PartAttachmentCreate and
// PartAttachmentUpdate. replaceName, when non-empty, is the current LOCAL:
// file (already stripped of its prefix) that this same row is replacing; if
// the newly generated name matches it exactly, replaceLocalFile is used
// instead of copyIntoDocControl so the row's own file is swapped in place
// rather than reported as a false collision against itself. replaceName is
// always empty for Create, so this branch never affects that path.
func (h *Handler) resolveAttachmentFileInput(ctx context.Context, r *http.Request, partID, rev, category, comment, replaceName string) attachmentFileInput {
	fileName := fv(r, "FILFileName")
	src := fv(r, "source_path")
	if src == "" {
		return attachmentFileInput{FileName: urlutil.NormalizeLink(fileName)}
	}
	if h.cfg.DocControlRoot == "" {
		return attachmentFileInput{ErrMsg: "DOC_CONTROL_ROOT is not configured; cannot import files."}
	}
	p, err := h.fetchPartBasic(ctx, partID)
	if err != nil {
		return attachmentFileInput{ErrMsg: "Error loading part: " + err.Error()}
	}
	move := fv(r, "move_source") == "1"
	name := buildAttachmentFileName(p.PartNumber, rev, p.Title, category, filepath.Ext(src))
	if fv(r, "link_existing") != "1" {
		if replaceName != "" && strings.EqualFold(name, replaceName) {
			if err := replaceLocalFile(h.cfg.DocControlRoot, name, src); err != nil {
				return attachmentFileInput{ErrMsg: "Error replacing file: " + err.Error()}
			}
		} else {
			existed, err := copyIntoDocControl(h.cfg.DocControlRoot, name, src)
			if err != nil {
				return attachmentFileInput{ErrMsg: "Error copying file: " + err.Error()}
			}
			if existed {
				return attachmentFileInput{Collision: map[string]string{
					"Name": name, "SourcePath": src,
					"Category": category, "Rev": rev, "OrderID": fv(r, "order_id"),
					"Move": fv(r, "move_source"), "Comment": comment,
				}}
			}
		}
	}
	result := attachmentFileInput{FileName: "LOCAL:" + name}
	if move {
		result.MoveSrc = src
	}
	return result
}

// replaceLocalFile copies src into root under name, keeping name intact even
// if the copy fails: it copies to a temporary sibling file first and only
// removes the existing file and swaps the temp file into place once the copy
// has fully succeeded, so a mid-copy failure never leaves name missing.
func replaceLocalFile(root, name, src string) error {
	tmpName := name + ".tmp_replace"
	existed, err := copyIntoDocControl(root, tmpName, src)
	if err != nil {
		return err
	}
	if existed {
		return fmt.Errorf("a temporary file %q already exists; please try again", tmpName)
	}
	tmpPath := filepath.Join(root, tmpName)
	target := filepath.Join(root, name)
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return err
	}
	return nil
}

// replaceDocControlData writes data into <root>/<name>, keeping name intact
// even if the write fails: it writes to a temporary sibling file first and
// only removes the existing file and swaps the temp file into place once the
// write has fully succeeded. Mirrors replaceLocalFile's copy-then-swap for
// byte data instead of a source file on disk.
func replaceDocControlData(root, name string, data []byte) error {
	tmpName := name + ".tmp_replace"
	existed, err := writeIntoDocControl(root, tmpName, data)
	if err != nil {
		return err
	}
	if existed {
		return fmt.Errorf("a temporary file %q already exists; please try again", tmpName)
	}
	tmpPath := filepath.Join(root, tmpName)
	target := filepath.Join(root, name)
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return err
	}
	return nil
}

// deleteAttachmentFileIfUnshared removes root/<strippedName> unless another
// active row in table still has fullFileName (e.g. via the "Link to existing
// file" import flow), in which case the file is left in place for that row.
// A file that's already gone is treated as success, not an error.
func (h *Handler) deleteAttachmentFileIfUnshared(ctx context.Context, table, idCol, fileCol string, excludeID any, fullFileName, root, strippedName string) error {
	var count int
	if err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT COUNT(*) FROM %s WHERE %s=@p1 AND is_active=%s AND %s<>@p2`, table, fileCol, h.dia().BoolLiteral(true), idCol,
	), fullFileName, excludeID).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	if err := os.Remove(filepath.Join(root, strippedName)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// softDeleteAttachment sets is_active=0 on an attachment row.
// ownerCol/ownerID add an ownership filter when non-empty/non-zero.
func (h *Handler) softDeleteAttachment(ctx context.Context, table, idCol string, id int, ownerCol string, ownerID int) error {
	if ownerCol != "" {
		_, err := h.execContext(ctx, fmt.Sprintf(
			`UPDATE %s SET is_active=%s WHERE %s=@p1 AND %s=@p2`, table, h.dia().BoolLiteral(false), idCol, ownerCol,
		), id, ownerID)
		return err
	}
	_, err := h.execContext(ctx, fmt.Sprintf(
		`UPDATE %s SET is_active=%s WHERE %s=@p1`, table, h.dia().BoolLiteral(false), idCol,
	), id)
	return err
}

// setPrimaryAttachment updates a parent record's primary attachment pointer.
// Pass nil for attachmentID to clear the primary.
func (h *Handler) setPrimaryAttachment(ctx context.Context, table, idCol, primaryCol string, parentID int, attachmentID any) error {
	_, err := h.execContext(ctx, fmt.Sprintf(
		`UPDATE %s SET %s=@p1 WHERE %s=@p2`, table, primaryCol, idCol,
	), attachmentID, parentID)
	return err
}

// attachmentUsage is one row/owner in the "where used" results for a file link.
type attachmentUsage struct {
	Kind    string // "part" or "supplier"
	OwnerID int
	Code    string // part number (empty for suppliers)
	Label   string // part title or supplier name
}

// AttachmentWhereUsed shows every part and supplier that links the given file
// (exact stored-string match — see docs/plans/557-attachment-where-used.md for
// the LOCAL: root-ambiguity caveat between DocControlRoot and SupplierFilesRoot).
func (h *Handler) AttachmentWhereUsed(w http.ResponseWriter, r *http.Request) {
	file := strings.TrimSpace(r.URL.Query().Get("file"))
	if file == "" {
		h.renderError(w, r, "No file link specified.")
		return
	}
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT 'part' AS kind, p.id, p.part_number, p.title
		FROM %s fa JOIN %s p ON p.id = fa.part_id
		WHERE fa.is_active = %s AND fa.file_name = @p1
		UNION ALL
		SELECT 'supplier' AS kind, c.id, '', c.name
		FROM %s ca JOIN %s c ON c.id = ca.supplier_id
		WHERE ca.is_active = %s AND ca.file_path = @p1
		ORDER BY 1, 4
	`, h.cfg.AttachmentsTable(), h.cfg.PartsTable(), h.dia().BoolLiteral(true),
		h.cfg.CompanyAttachmentsTable(), h.cfg.CompanyTable(), h.dia().BoolLiteral(true)), file)
	if err != nil {
		h.renderError(w, r, "Error retrieving where-used: "+err.Error())
		return
	}
	defer rows.Close()
	var usages []attachmentUsage
	for rows.Next() {
		var u attachmentUsage
		var code, label sql.NullString
		if err := rows.Scan(&u.Kind, &u.OwnerID, &code, &label); err != nil {
			h.renderError(w, r, "Error reading where-used: "+err.Error())
			return
		}
		u.Code = code.String
		u.Label = label.String
		usages = append(usages, u)
	}
	h.render(w, r, "parts/attachment_where_used.html", map[string]any{
		"FileLink": file, "Usages": usages,
		"ActiveTab": "parts", "TestMode": h.cfg.TestMode,
	})
}
