package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"arx/arx_go/models"
	arxdb "arx/arxlib/db"
	"arx/arxlib/urlutil"
)

// illegalFileNameChars are characters not permitted in a Windows filename.
const illegalFileNameChars = `<>:"/\|?*`

// descriptionMaxLen caps the part description portion of a generated attachment filename.
const descriptionMaxLen = 20

// buildAttachmentFileName produces the base filename for an imported attachment:
// "<PartNumber> <Rev> <Description> <Category><ext>". Blank (or whitespace-only) parts
// are skipped so separators never double up, and the description is truncated to
// descriptionMaxLen. Each part is sanitised of filesystem-illegal characters, and the
// result is always a bare base name (no directory component), so it cannot
// escape the target folder.
//
// This is the sole implementation of the naming convention; the Browse live
// preview in templates/parts/part_attachments.html calls it via
// GET /api/part/{id}/attachment-name rather than duplicating the rule (#558).
func buildAttachmentFileName(partNumber, rev, description, category, ext string) string {
	description = truncateRunes(strings.TrimSpace(description), descriptionMaxLen)
	var parts []string
	for _, p := range []string{partNumber, rev, description, category} {
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

// copyReaderIntoDocControl streams src into <root>/<name> without overwriting.
// If the target already exists it returns (true, nil) and writes nothing.
// A failure leaves no orphan target behind.
func copyReaderIntoDocControl(root, name string, src io.Reader) (existed bool, err error) {
	target := filepath.Join(root, name)
	dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		if os.IsExist(err) {
			return true, nil
		}
		return false, err
	}

	// Close dst before any cleanup so os.Remove isn't blocked by an open handle
	// (Windows sharing violation).
	_, copyErr := io.Copy(dst, src)
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
	return copyReaderIntoDocControl(root, name, bytes.NewReader(data))
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

// copyReaderIntoDocControlUnique calls copyReaderIntoDocControl, retrying with
// " (2)", " (3)", ... appended before ext on collision. name must already
// include ext. Safe to retry against the same src: a collision is detected
// before src is read, so no bytes are consumed on a failed attempt.
func copyReaderIntoDocControlUnique(root, name, ext string, src io.Reader) (finalName string, err error) {
	base := strings.TrimSuffix(name, ext)
	candidate := name
	for attempt := 1; attempt <= 20; attempt++ {
		existed, err := copyReaderIntoDocControl(root, candidate, src)
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
// file_name value to store, or a reason (error / import collision) the
// caller must surface to the user instead of saving.
type attachmentFileInput struct {
	FileName  string
	Collision map[string]string
	ErrMsg    string
}

// attachmentUploads returns the files submitted under the given multipart form
// field, or nil when none were submitted. It returns a slice (not a single
// file) so a future multi-file upload can accept multiple files per submission
// without reshaping the callers' contract; today every caller uses only the
// first entry. Does not call r.ParseMultipartForm — the CSRF middleware
// (RequireCsrfOnPost) already parses the body ahead of every handler.
func attachmentUploads(r *http.Request, field string) []*multipart.FileHeader {
	if r.MultipartForm == nil || r.MultipartForm.File == nil {
		return nil
	}
	return r.MultipartForm.File[field]
}

// attachmentUploadSource abstracts a single pending file's bytes for
// resolveAttachmentFileInput, so the same resolution logic serves both a live
// multipart upload (single-file Create/Update) and a file staged to disk from
// an earlier multi-file batch POST that's resuming after a collision decision
// (#70) — its bytes no longer live in any *http.Request by the time of resume.
type attachmentUploadSource struct {
	ext  string
	open func() (io.ReadCloser, error)
}

// multipartUploadSource wraps a directly-submitted upload.
func multipartUploadSource(fh *multipart.FileHeader) attachmentUploadSource {
	return attachmentUploadSource{
		ext:  filepath.Ext(fh.Filename),
		open: func() (io.ReadCloser, error) { return fh.Open() },
	}
}

// stagedUploadSource wraps a file previously staged to disk by
// startAttachmentBatch, resumed after a collision decision (#70).
func stagedUploadSource(path string) attachmentUploadSource {
	return attachmentUploadSource{
		ext:  filepath.Ext(path),
		open: func() (io.ReadCloser, error) { return os.Open(path) },
	}
}

// resolveAttachmentFileInput inspects the form for either a manual FILFileName
// value, an uploaded file, or a link_existing carry-over from a prior
// collision, shared by PartAttachmentCreate and PartAttachmentUpdate.
// replaceName, when non-empty, is the current LOCAL: file (already stripped
// of its prefix) that this same row is replacing; if the newly generated name
// matches it exactly, replaceLocalFileFrom is used instead of
// copyReaderIntoDocControl so the row's own file is swapped in place rather
// than reported as a false collision against itself. replaceName is always
// empty for Create, so this branch never affects that path. upload is nil
// when the caller has no pending file (manual FILFileName / link_existing).
// allowLinkExisting must be false when resolving a later file in a batch
// resumed after a "Link to existing file" decision (#70): the resume POST's
// link_existing=1/link_name form values apply only to the one file that
// collided, but the same *http.Request is reused for every file recursively
// imported afterward — without this, every subsequent file in the batch
// would also be silently linked to that same colliding name instead of
// being imported from its staged bytes.
func (h *Handler) resolveAttachmentFileInput(ctx context.Context, r *http.Request, partID, rev, category, comment, replaceName string, upload *attachmentUploadSource, allowLinkExisting bool) attachmentFileInput {
	if allowLinkExisting && fv(r, "link_existing") == "1" {
		name := sanitizeFileNamePart(filepath.Base(fv(r, "link_name")))
		if name == "" {
			return attachmentFileInput{ErrMsg: "That file no longer exists in Doc Control."}
		}
		if _, err := os.Stat(filepath.Join(h.cfg.DocControlRoot, name)); err != nil {
			return attachmentFileInput{ErrMsg: "That file no longer exists in Doc Control."}
		}
		return attachmentFileInput{FileName: "LOCAL:" + name}
	}

	if upload == nil {
		return attachmentFileInput{FileName: urlutil.NormalizeLink(fv(r, "FILFileName"))}
	}
	if h.cfg.DocControlRoot == "" {
		return attachmentFileInput{ErrMsg: "DOC_CONTROL_ROOT is not configured; cannot import files."}
	}
	p, err := h.fetchPartBasic(ctx, partID)
	if err != nil {
		return attachmentFileInput{ErrMsg: "Error loading part: " + err.Error()}
	}
	name := buildAttachmentFileName(p.PartNumber, rev, p.Description, category, upload.ext)
	f, err := upload.open()
	if err != nil {
		return attachmentFileInput{ErrMsg: "Error reading upload: " + err.Error()}
	}
	defer f.Close()

	if replaceName != "" && strings.EqualFold(name, replaceName) {
		if err := replaceLocalFileFrom(h.cfg.DocControlRoot, name, f); err != nil {
			return attachmentFileInput{ErrMsg: "Error replacing file: " + err.Error()}
		}
	} else {
		existed, err := copyReaderIntoDocControl(h.cfg.DocControlRoot, name, f)
		if err != nil {
			return attachmentFileInput{ErrMsg: "Error copying file: " + err.Error()}
		}
		if existed {
			return attachmentFileInput{Collision: map[string]string{
				"Name": name,
				"Category": category, "Rev": rev, "OrderID": fv(r, "order_id"),
				"Comment": comment, "VendorScope": fv(r, "vendor_scope"),
			}}
		}
	}
	return attachmentFileInput{FileName: "LOCAL:" + name}
}

// batchDirPrefix names every temp directory created by a multi-file
// attachment batch (#70), so a client-supplied batch_dir value can be
// verified to be one of ours before anything touches the filesystem with it.
const batchDirPrefix = "arx-attach-batch-"

// stagedFileNamePattern matches the "NNN.ext" names startAttachmentBatch
// gives staged files. Used to validate a client-supplied batch_remaining
// value before joining it onto a filesystem path.
var stagedFileNamePattern = regexp.MustCompile(`^[0-9]{3}[A-Za-z0-9.]*$`)

// batchPartIDMarker is the name of the file startAttachmentBatch writes
// inside its temp dir recording which part the batch belongs to, so a
// forged/stale batch_dir posted to a different part's attachments page is
// rejected instead of importing staged files under the wrong part (#70).
const batchPartIDMarker = ".part_id"

// isBatchTempDir reports whether dir is exactly one of ours — a direct child
// of the OS temp directory, named with batchDirPrefix, that still exists —
// and was created for partID.
func isBatchTempDir(dir, partID string) bool {
	if !strings.HasPrefix(filepath.Base(dir), batchDirPrefix) {
		return false
	}
	if filepath.Dir(dir) != filepath.Clean(os.TempDir()) {
		return false
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return false
	}
	marker, err := os.ReadFile(filepath.Join(dir, batchPartIDMarker))
	return err == nil && string(marker) == partID
}

// startAttachmentBatch handles a multi-file Add Attachment submission (#70).
// categories holds one category per file in ups, same order — batch files
// need their own category (rather than the single-file form's one shared
// Category field) because buildAttachmentFileName only varies by
// category/rev/ext, so two files in one batch sharing a category (and
// extension) would generate identical names and collide with each other,
// not just with pre-existing files.
// Every uploaded file is staged to its own temp file, named "<3-digit
// index><ext>" in submission order, before any import runs — a mid-batch
// collision re-renders the page (a fresh request), and the original
// multipart upload's bytes do not survive that. Only the extension is staged
// per name because buildAttachmentFileName never uses the original filename.
func (h *Handler) startAttachmentBatch(w http.ResponseWriter, r *http.Request, id, rev, comment string, oID, supplierPartID, mfgPartID any, ups []*multipart.FileHeader, categories []string) {
	dir, err := os.MkdirTemp("", batchDirPrefix)
	if err != nil {
		h.renderError(w, r, "Error starting import: "+err.Error())
		return
	}
	if err := os.WriteFile(filepath.Join(dir, batchPartIDMarker), []byte(id), 0644); err != nil {
		os.RemoveAll(dir)
		h.renderError(w, r, "Error starting import: "+err.Error())
		return
	}
	staged := make([]string, 0, len(ups))
	for i, fh := range ups {
		name := fmt.Sprintf("%03d%s", i, filepath.Ext(fh.Filename))
		src, err := fh.Open()
		if err != nil {
			os.RemoveAll(dir)
			h.renderError(w, r, "Error reading upload: "+err.Error())
			return
		}
		dst, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			src.Close()
			os.RemoveAll(dir)
			h.renderError(w, r, "Error staging upload: "+err.Error())
			return
		}
		_, copyErr := io.Copy(dst, src)
		src.Close()
		dst.Close()
		if copyErr != nil {
			os.RemoveAll(dir)
			h.renderError(w, r, "Error staging upload: "+copyErr.Error())
			return
		}
		staged = append(staged, name)
	}
	h.importAttachmentBatch(w, r, id, rev, comment, oID, supplierPartID, mfgPartID, dir, staged, categories, len(staged), false)
}

// resumeAttachmentBatch continues a batch after a collision decision posted
// back from the collision page (#70): "Link to existing file" (link_existing=1,
// batch_remaining/batch_categories include the colliding file so it's
// retried and resolved via the link_existing branch above) or "Cancel"
// (batch_remaining is empty, aborting the rest of the batch — files already
// imported stay imported). dir, remaining, and their categories are
// client-supplied (hidden form fields) and are validated before any
// filesystem access or DB write.
func (h *Handler) resumeAttachmentBatch(w http.ResponseWriter, r *http.Request, id, rev, comment string, oID, supplierPartID, mfgPartID any, dir string) {
	if !isBatchTempDir(dir, id) {
		h.renderError(w, r, "This import batch has expired or is invalid. Please re-select your files.")
		return
	}
	total, err := strconv.Atoi(fv(r, "batch_total"))
	if err != nil || total <= 0 {
		os.RemoveAll(dir)
		h.renderError(w, r, "This import batch has expired or is invalid. Please re-select your files.")
		return
	}
	var remaining []string
	if v := fv(r, "batch_remaining"); v != "" {
		remaining = strings.Split(v, ",")
	}
	for _, name := range remaining {
		if !stagedFileNamePattern.MatchString(name) {
			os.RemoveAll(dir)
			h.renderError(w, r, "This import batch has expired or is invalid. Please re-select your files.")
			return
		}
	}
	var categories []string
	if v := fv(r, "batch_categories"); v != "" {
		categories = strings.Split(v, ",")
	}
	if len(categories) != len(remaining) {
		os.RemoveAll(dir)
		h.renderError(w, r, "This import batch has expired or is invalid. Please re-select your files.")
		return
	}
	for _, category := range categories {
		if category == "" || isGeneratedCategory(category) {
			os.RemoveAll(dir)
			h.renderError(w, r, "This import batch has expired or is invalid. Please re-select your files.")
			return
		}
	}
	h.importAttachmentBatch(w, r, id, rev, comment, oID, supplierPartID, mfgPartID, dir, remaining, categories, total, true)
}

// importAttachmentBatch imports staged[0] under categories[0], then recurses
// on staged[1:]/categories[1:] — one file at a time, in submission order
// (#70). A collision re-renders the attachments page with staged[1:] and
// categories[1:], and separately all of staged/categories for the "Link to
// existing" retry, encoded into ImportCollision so the next POST can resume
// via resumeAttachmentBatch. dir is removed once staged is exhausted or an
// error (not a collision) aborts the batch. allowLinkExisting is true only
// when staged[0] is the file a "Link to existing file" resume POST is
// retrying — r's link_existing=1/link_name values apply to that one file
// only, so every recursive call after it passes false (r is reused across
// the whole recursion and never stops carrying those values).
func (h *Handler) importAttachmentBatch(w http.ResponseWriter, r *http.Request, id, rev, comment string, oID, supplierPartID, mfgPartID any, dir string, staged, categories []string, total int, allowLinkExisting bool) {
	if len(staged) == 0 {
		os.RemoveAll(dir)
		http.Redirect(w, r, fmt.Sprintf("/part/%s/attachments", id), http.StatusFound)
		return
	}
	category := categories[0]
	upload := stagedUploadSource(filepath.Join(dir, staged[0]))
	in := h.resolveAttachmentFileInput(r.Context(), r, id, rev, category, comment, "", &upload, allowLinkExisting)
	if in.ErrMsg != "" {
		os.RemoveAll(dir)
		h.renderPartAttachments(w, r, id, map[string]any{"Error": in.ErrMsg})
		return
	}
	if in.Collision != nil {
		in.Collision["BatchDir"] = dir
		in.Collision["BatchTotal"] = strconv.Itoa(total)
		in.Collision["BatchIndex"] = strconv.Itoa(total - len(staged) + 1)
		// For "Link to existing file": resume must retry staged[0] itself
		// (this time taking the link_existing branch), so the full list
		// (current file included) is what that form's batch_remaining/
		// batch_categories post.
		in.Collision["BatchRemaining"] = strings.Join(staged, ",")
		in.Collision["BatchCategories"] = strings.Join(categories, ",")
		h.renderPartAttachments(w, r, id, map[string]any{"ImportCollision": in.Collision})
		return
	}
	// Batch imports record the hash but do not warn on a duplicate (#71): the
	// mid-batch collision-resume state machine is already the most intricate
	// code in this file, and a second interrupt state would double it.
	hash := computeAttachmentHash(h.cfg.DocControlRoot, in.FileName)
	if err := h.insertAttachmentRow(r.Context(), id, in.FileName, rev, category, oID, comment, supplierPartID, mfgPartID, hash); err != nil {
		os.RemoveAll(dir)
		h.renderError(w, r, "Error adding attachment: "+err.Error())
		return
	}
	h.importAttachmentBatch(w, r, id, rev, comment, oID, supplierPartID, mfgPartID, dir, staged[1:], categories[1:], total, false)
}

// replaceLocalFileFrom streams src into root under name, keeping name intact
// even if the copy fails: it copies to a temporary sibling file first and only
// removes the existing file and swaps the temp file into place once the copy
// has fully succeeded, so a mid-copy failure never leaves name missing.
func replaceLocalFileFrom(root, name string, src io.Reader) error {
	tmpName := name + ".tmp_replace"
	existed, err := copyReaderIntoDocControl(root, tmpName, src)
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
// write has fully succeeded. Mirrors replaceLocalFileFrom's copy-then-swap for
// byte data instead of an io.Reader source.
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

// execFunc is the ExecContext shape shared by h.execContext and (*txLogger).ExecContext,
// so a helper can run against either.
type execFunc func(ctx context.Context, query string, args ...any) (sql.Result, error)

// primaryAttachmentEnsureSQL builds the statement that points a parent's primary at
// its first active attachment (lowest sort_order, ties by id), but only when the
// current primary is NULL or no longer active. A live primary is never changed, so
// it is safe to run after every insert and soft-delete; when no active attachment
// remains the subselect yields NULL and the primary is cleared (#121). extraFilter
// is an optional " AND ..." clause on the attachment alias `a`. Takes @p1 = parent id.
func primaryAttachmentEnsureSQL(d arxdb.Dialect, parentTable, primaryCol, attTable, attPK, ownerCol, extraFilter string) string {
	return fmt.Sprintf(
		`UPDATE %[1]s SET %[2]s = (
			SELECT %[7]sa.%[4]s FROM %[3]s a
			WHERE a.%[5]s = %[1]s.id AND a.is_active = %[6]s%[9]s
			ORDER BY COALESCE(a.sort_order, 0), a.%[4]s%[8]s)
		WHERE id = @p1 AND (%[2]s IS NULL OR NOT EXISTS (
			SELECT 1 FROM %[3]s x WHERE x.%[4]s = %[1]s.%[2]s AND x.is_active = %[6]s))`,
		parentTable, primaryCol, attTable, attPK, ownerCol, d.BoolLiteral(true),
		d.TopClause("1"), d.LimitClause("1"), extraFilter)
}

// ensurePartPrimary applies primaryAttachmentEnsureSQL to a part. The generated
// PDF Preview / Thumbnail rows never become the auto-set primary — they are
// derived images, not the part's own files.
func (h *Handler) ensurePartPrimary(ctx context.Context, exec execFunc, partID any) error {
	_, err := exec(ctx, primaryAttachmentEnsureSQL(h.dia(), h.cfg.PartsTable(), "primary_attachment_id",
		h.cfg.AttachmentsTable(), "id", "part_id",
		fmt.Sprintf(" AND COALESCE(a.category, '') NOT IN ('%s', '%s')", previewCategory, thumbnailCategory)), partID)
	return err
}

func (h *Handler) ensureSupplierPrimary(ctx context.Context, exec execFunc, supplierID any) error {
	_, err := exec(ctx, primaryAttachmentEnsureSQL(h.dia(), h.cfg.CompanyTable(), "primary_attachment_id",
		h.cfg.CompanyAttachmentsTable(), "supplier_attachment_id", "supplier_id", ""), supplierID)
	return err
}

// execThenEnsurePrimary runs an attachment INSERT or soft-delete UPDATE and then
// ensure, in one transaction, so a failure can't leave the write done but the
// primary stale (#121).
func (h *Handler) execThenEnsurePrimary(ctx context.Context, ensure func(context.Context, execFunc, any) error, parentID any, query string, args ...any) error {
	tx, err := h.beginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return err
	}
	if err := ensure(ctx, tx.ExecContext, parentID); err != nil {
		return err
	}
	return tx.Commit()
}

// setPrimaryAttachment updates a parent record's primary attachment pointer.
// Pass nil for attachmentID to clear the primary.
func (h *Handler) setPrimaryAttachment(ctx context.Context, table, idCol, primaryCol string, parentID int, attachmentID any) error {
	_, err := h.execContext(ctx, fmt.Sprintf(
		`UPDATE %s SET %s=@p1 WHERE %s=@p2`, table, primaryCol, idCol,
	), attachmentID, parentID)
	return err
}

// Vendor-scope columns on part_attachment (#56). Interpolated into SQL, so these are
// the only permitted values — never user input.
const (
	supplierScopeCol = "supplier_part_id"
	mfgScopeCol      = "mfg_part_id"
)

// vendorScopeOption is one choice in the attachments page's "Linked Vendor" picker.
// Value is the form token parsed by resolveVendorScope.
type vendorScopeOption struct {
	Value string
	Label string
}

// fetchVendorScopeOptions lists the part's own supplier links and manufacturer parts —
// the only vendors one of its attachments may be scoped to (#56).
func (h *Handler) fetchVendorScopeOptions(r *http.Request, partID string) ([]vendorScopeOption, error) {
	links, err := h.fetchSupplierLinks(r, partID)
	if err != nil {
		return nil, err
	}
	var opts []vendorScopeOption
	for _, lk := range links {
		opts = append(opts, vendorScopeOption{
			Value: fmt.Sprintf("s:%d", lk.ID),
			Label: vendorScopeLabel("Supplier", lk.SupplierName, lk.SupplierPN),
		})
	}
	mfgParts, err := h.fetchMfgParts(r, partID)
	if err != nil {
		return nil, err
	}
	for _, mp := range mfgParts {
		opts = append(opts, vendorScopeOption{
			Value: fmt.Sprintf("m:%d", mp.ID),
			Label: vendorScopeLabel("Mfg", mp.MfgName, mp.MfgPartNumber),
		})
	}
	return opts, nil
}

func vendorScopeLabel(kind, name, pn string) string {
	if pn == "" {
		return kind + ": " + name
	}
	return kind + ": " + name + " (" + pn + ")"
}

// resolveVendorScope turns a submitted "Linked Vendor" token into the supplier_part_id /
// mfg_part_id pair to store (#56). An empty token means part-level (both NULL). The
// referenced link must belong to partID — the picker only offers this part's own vendors,
// and nothing else may be scoped to an attachment of a different part.
func (h *Handler) resolveVendorScope(ctx context.Context, partID, token string) (supplierPartID, mfgPartID any, err error) {
	if token == "" {
		return nil, nil, nil
	}
	kind, rest, ok := strings.Cut(token, ":")
	if !ok {
		return nil, nil, fmt.Errorf("invalid linked vendor selection")
	}
	id, convErr := strconv.Atoi(rest)
	if convErr != nil {
		return nil, nil, fmt.Errorf("invalid linked vendor selection")
	}
	var table string
	switch kind {
	case "s":
		table = h.cfg.SupplierPartTable()
	case "m":
		table = h.cfg.MfgPartTable()
	default:
		return nil, nil, fmt.Errorf("invalid linked vendor selection")
	}
	var count int
	if err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT COUNT(*) FROM %s WHERE id=@p1 AND part_id=@p2`, table,
	), id, partID).Scan(&count); err != nil {
		return nil, nil, err
	}
	if count == 0 {
		return nil, nil, fmt.Errorf("that supplier or manufacturer is not linked to this part")
	}
	if kind == "s" {
		return id, nil, nil
	}
	return nil, id, nil
}

// fetchAttachmentsByVendor returns a part's active attachments that are scoped to a vendor
// link, keyed by that link's id, for the Suppliers / Mfg Parts tabs (#56). col is one of
// supplierScopeCol / mfgScopeCol.
func (h *Handler) fetchAttachmentsByVendor(r *http.Request, partID, col string) map[int][]models.Attachment {
	rows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT %s, id, file_name, category, part_revision
		FROM %s
		WHERE part_id = @p1 AND is_active = %s AND %s IS NOT NULL
		ORDER BY sort_order, id
	`, col, h.cfg.AttachmentsTable(), h.dia().BoolLiteral(true), col), partID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := map[int][]models.Attachment{}
	for rows.Next() {
		var vendorID sql.NullInt64
		var att models.Attachment
		var fname, category, rev sql.NullString
		if rows.Scan(&vendorID, &att.ID, &fname, &category, &rev) != nil || !vendorID.Valid {
			continue
		}
		att.FileName = fname.String
		att.Category = category.String
		att.PartRevision = rev.String
		out[int(vendorID.Int64)] = append(out[int(vendorID.Int64)], att)
	}
	return out
}

// attachmentUsage is one row/owner in the "where used" results for a file link.
type attachmentUsage struct {
	Kind    string // "part" or "supplier"
	OwnerID int
	Code    string // part number (empty for suppliers)
	Label   string // part description or supplier name
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
		SELECT 'part' AS kind, p.id, p.part_number, p.description
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

// ── Content hash / duplicate detection (#71) ────────────────────────────────

// hashBytes returns the lowercase-hex SHA-256 of data. Used where the bytes are
// already in memory (clipboard paste, DigiKey import) so the file isn't re-read.
func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// hashLinkString returns the lowercase-hex SHA-256 of an attachment link string.
// Used for directory-style LOCAL: links, http(s) URLs, absolute/UNC paths, and as
// the fallback when a LOCAL: file can't be read.
func hashLinkString(link string) string {
	return hashBytes([]byte(link))
}

// companyAttachmentRoot is the filesystem root company_attachment LOCAL: links
// resolve against: SUPPLIER_FILES_ROOT, falling back to DOC_CONTROL_ROOT.
func (h *Handler) companyAttachmentRoot() string {
	if h.cfg.SupplierFilesRoot != "" {
		return h.cfg.SupplierFilesRoot
	}
	return h.cfg.DocControlRoot
}

// computeAttachmentHash returns the hash identifying one attachment link (#71):
// the file's content for a single-file LOCAL: link under root, or the link string
// itself for a directory-style LOCAL: link, an http(s) URL, an absolute path, or a
// LOCAL: file that is missing/unreadable. root is the filesystem root the table's
// LOCAL: links resolve against — DocControlRoot for part_attachment,
// companyAttachmentRoot() for company_attachment. Never returns "".
func computeAttachmentHash(root, link string) string {
	if root == "" || !urlutil.IsLocalFile(link) || urlutil.IsLocalDir(link) {
		return hashLinkString(link)
	}
	rel := strings.ReplaceAll(urlutil.StripLocalPrefix(link), "\\", "/")
	path, ok := safePath(root, rel)
	if !ok {
		return hashLinkString(link)
	}
	f, err := os.Open(path)
	if err != nil {
		return hashLinkString(link)
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return hashLinkString(link)
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// attIDFromExtra returns the AttID recorded under the first of keys present
// in extra whose value carries one (e.g. "ImportCollision", "DuplicateWarning"),
// so a warning rendered directly (not via redirect) keeps the same Edit form
// open that raised it, matching the ?edit= query-param behavior.
func attIDFromExtra(extra map[string]any, keys ...string) string {
	for _, k := range keys {
		if m, ok := extra[k].(map[string]string); ok {
			if id := m["AttID"]; id != "" {
				return id
			}
		}
	}
	return ""
}

// duplicateAttachment identifies the existing active attachment a pending one
// collides with, for the warning banner's link.
type duplicateAttachment struct {
	ID    int
	Label string // part number, or company name
	URL   string // "/part/<id>/attachments" or "/supplier/<id>/attachments"
}

// findDuplicateAttachment returns the oldest active attTable row whose hash
// matches, excluding excludeID (0 = exclude nothing, i.e. the create path).
// Returns nil when hash is empty or nothing matches. Soft-deleted rows are
// never compared. idCol/labelCol/joinCol/joinTable/urlFmt parameterize the
// query shape shared by findDuplicatePartAttachment and
// findDuplicateCompanyAttachment — the two tables are otherwise checked
// completely independently (a match in one never flags against the other).
func (h *Handler) findDuplicateAttachment(ctx context.Context, hash string, excludeID int, attTable, idCol, joinTable, joinCol, labelCol, urlFmt string) (*duplicateAttachment, error) {
	if hash == "" {
		return nil, nil
	}
	var dup duplicateAttachment
	var joinID int
	var label string
	err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT %sa.%s, j.id, j.%s
		FROM %s a JOIN %s j ON j.id = a.%s
		WHERE a.is_active = %s AND a.hash = @p1 AND a.%s <> @p2
		ORDER BY a.%s`+h.dia().LimitClause("1"),
		h.dia().TopClause("1"), idCol, labelCol, attTable, joinTable, joinCol, h.dia().BoolLiteral(true), idCol, idCol,
	), hash, excludeID).Scan(&dup.ID, &joinID, &label)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	dup.Label = label
	dup.URL = fmt.Sprintf(urlFmt, joinID)
	return &dup, nil
}

func (h *Handler) findDuplicatePartAttachment(ctx context.Context, hash string, excludeID int) (*duplicateAttachment, error) {
	return h.findDuplicateAttachment(ctx, hash, excludeID,
		h.cfg.AttachmentsTable(), "id", h.cfg.PartsTable(), "part_id", "part_number", "/part/%d/attachments")
}

func (h *Handler) findDuplicateCompanyAttachment(ctx context.Context, hash string, excludeID int) (*duplicateAttachment, error) {
	return h.findDuplicateAttachment(ctx, hash, excludeID,
		h.cfg.CompanyAttachmentsTable(), "supplier_attachment_id", h.cfg.CompanyTable(), "supplier_id", "name", "/supplier/%d/attachments")
}
