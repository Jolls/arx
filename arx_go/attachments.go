package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
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
// NOTE: this convention is mirrored in JS for the live preview in
// templates/pm/part_attachments.html — keep both in sync. Deduplication: #558.
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

// softDeleteAttachment sets is_active=0 on an attachment row.
// ownerCol/ownerID add an ownership filter when non-empty/non-zero.
func (h *Handler) softDeleteAttachment(ctx context.Context, table, idCol string, id int, ownerCol string, ownerID int) error {
	if ownerCol != "" {
		_, err := h.execContext(ctx, fmt.Sprintf(
			`UPDATE %s SET is_active=0 WHERE %s=@p1 AND %s=@p2`, table, idCol, ownerCol,
		), id, ownerID)
		return err
	}
	_, err := h.execContext(ctx, fmt.Sprintf(
		`UPDATE %s SET is_active=0 WHERE %s=@p1`, table, idCol,
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
