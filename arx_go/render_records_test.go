package main

import (
	"html/template"
	"strings"
	"testing"
	"time"

	"arx/arx_go/models"
)

// Characterization (#446): pins down today's behavior that every non-empty
// pf_type="attach" result is treated as an image by isImageRow, regardless of
// file type — proves the PDF carve-out added for #446 (isFileRow) is
// additive to isImageRow's existing image-type behavior, not a regression.
func TestIsImageRow_AttachStepAnyValueIsImage(t *testing.T) {
	row := models.ResultRow{
		Step:   &models.TestStep{PFType: "attach"},
		Result: &models.TestResult{Result: "shot.png"},
	}
	if !isImageRow(row) {
		t.Errorf("isImageRow(attach step, non-PDF value) = false, want true")
	}
}

// Red (#446): a PDF attach-step result must be excluded from the image
// gallery (isImageRow) and picked up instead by isFileRow. Fails today:
// isFileRow does not exist and isImageRow treats every attach value,
// including a PDF, as an image.
func TestIsFileRow_PDFAttachStepIsFileNotImage(t *testing.T) {
	row := models.ResultRow{
		Step:   &models.TestStep{PFType: "attach"},
		Result: &models.TestResult{Result: "cert.pdf"},
	}
	if isImageRow(row) {
		t.Errorf("isImageRow(attach step, PDF value) = true, want false")
	}
	if !isFileRow(row) {
		t.Errorf("isFileRow(attach step, PDF value) = false, want true")
	}
}

// Characterization (#453): a non-attach-step row whose value matches imageResultRe (a legacy
// pre-#587 row) still classifies as an image via attachRowKind/isImageRow — the Go-side
// precondition for acceptance criterion 2 (the legacy fallback must keep working). Unaffected
// by #453, since that fix only changes which template func the main-cell markup calls, not the
// classification logic itself.
func TestIsImageRow_LegacyNonAttachRowMatchingRegexStillImage(t *testing.T) {
	row := models.ResultRow{
		Step:   &models.TestStep{PFType: ""},
		Result: &models.TestResult{Result: "SN-123_rID-4_tID-5.png"},
	}
	if !isImageRow(row) {
		t.Errorf("isImageRow(legacy non-attach row matching imageResultRe) = false, want true")
	}
}

// minimalRecordsShowData returns the "content" page data records_show.html needs to execute
// without a live Handler, with one result row under test.
func minimalRecordsShowData(row models.ResultRow) map[string]any {
	return map[string]any{
		"Form":              models.TestForm{PartNumber: "PN-453", Description: "Test Part"},
		"Record":            models.TestRecord{ID: 1, SerialNumber: "SN test", SerialNumberPN: "PN-453"},
		"Rows":              []models.ResultRow{row},
		"ImageRows":         []models.ResultRow(nil),
		"FileRows":          []models.ResultRow(nil),
		"Events":            nil,
		"Snapshots":         nil,
		"Trace":             nil,
		"CanApproveRecords": false,
		"PrevID":            0,
		"NextID":            0,
		"CSRFToken":         "",
		"DebugMode":         false,
		"UserLoc":           time.UTC,
	}
}

// attachStepRow builds a Level-0 pf_type="attach" result row with the given filename.
func attachStepRow(filename string) models.ResultRow {
	return models.ResultRow{
		Step:   &models.TestStep{PFType: "attach"},
		Result: &models.TestResult{Result: filename},
	}
}

// TestRecordsShowMainCell_AttachStepNonMatchingSerial is #453's core acceptance criterion for
// the record detail page: an attach-step result whose filename's serial breaks imageResultRe
// (a space, here) must still render as a clickable thumbnail/link, matching the
// Screenshots/Attachments sections. Fails today: the main cell gates on the bare imageResult
// regex instead of the pf_type="attach"-aware classification, so it falls through to plain text.
func TestRecordsShowMainCell_AttachStepNonMatchingSerial(t *testing.T) {
	row := attachStepRow("SN my serial_rID-7_tID-9.png")
	tmpl, err := template.New("").Funcs(recordsTemplateFuncs()).ParseFS(templatesFS, "templates/records/records_show.html")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var buf strings.Builder
	if err := tmpl.ExecuteTemplate(&buf, "content", minimalRecordsShowData(row)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "img-hover-wrap") {
		t.Errorf("main cell for attach-step image with non-matching serial missing thumbnail markup; got:\n%s", out)
	}
	if strings.Contains(out, "SN my serial_rID-7_tID-9.png") {
		t.Errorf("main cell rendered the raw filename as plain text instead of a thumbnail; got:\n%s", out)
	}
}

// TestRecordPrintMainCell_AttachStepNonMatchingSerial is the print-view equivalent of the above:
// the cell must render "[see gallery]" (image) rather than the raw filename.
func TestRecordPrintMainCell_AttachStepNonMatchingSerial(t *testing.T) {
	row := attachStepRow("SN my serial_rID-7_tID-9.png")
	tmpl, err := template.New("").Funcs(recordsTemplateFuncs()).ParseFS(templatesFS, "templates/records/record_print.html")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var buf strings.Builder
	if err := tmpl.ExecuteTemplate(&buf, "record_print.html", minimalRecordsShowData(row)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "[see gallery]") {
		t.Errorf("print main cell for attach-step image with non-matching serial missing [see gallery]; got:\n%s", out)
	}
	if strings.Contains(out, "SN my serial_rID-7_tID-9.png") {
		t.Errorf("print main cell rendered the raw filename as plain text instead of [see gallery]; got:\n%s", out)
	}
}

// TestRecordPrintMainCell_AttachStepPDFNonMatchingSerial covers the PDF/file half: must render
// "[see attachments]" instead of the raw filename.
func TestRecordPrintMainCell_AttachStepPDFNonMatchingSerial(t *testing.T) {
	row := attachStepRow("SN my serial_rID-7_tID-9.pdf")
	tmpl, err := template.New("").Funcs(recordsTemplateFuncs()).ParseFS(templatesFS, "templates/records/record_print.html")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var buf strings.Builder
	if err := tmpl.ExecuteTemplate(&buf, "record_print.html", minimalRecordsShowData(row)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "[see attachments]") {
		t.Errorf("print main cell for attach-step PDF with non-matching serial missing [see attachments]; got:\n%s", out)
	}
	if strings.Contains(out, "SN my serial_rID-7_tID-9.pdf") {
		t.Errorf("print main cell rendered the raw filename as plain text instead of [see attachments]; got:\n%s", out)
	}
}

// TestRecordsShowMainCell_LegacyNonAttachRowStillRenders is acceptance criterion 2: a step not
// marked pf_type="attach" whose value happens to match the legacy imageResultRe pattern must
// keep rendering as before (the thumbnail fallback for pre-#587 rows) — no regression.
func TestRecordsShowMainCell_LegacyNonAttachRowStillRenders(t *testing.T) {
	row := models.ResultRow{
		Step:   &models.TestStep{PFType: ""},
		Result: &models.TestResult{Result: "SN-123_rID-4_tID-5.png"},
	}
	tmpl, err := template.New("").Funcs(recordsTemplateFuncs()).ParseFS(templatesFS, "templates/records/records_show.html")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var buf strings.Builder
	if err := tmpl.ExecuteTemplate(&buf, "content", minimalRecordsShowData(row)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "img-hover-wrap") {
		t.Errorf("main cell for legacy non-attach row matching imageResultRe lost its thumbnail markup; got:\n%s", out)
	}
}
