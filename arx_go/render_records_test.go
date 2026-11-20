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
	tmpl, err := template.New("").Funcs(recordsTemplateFuncs()).ParseFS(templatesFS, "templates/shared/audit_log.html", "templates/records/records_show.html")
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

// auditLogTail renders records_show.html "content" and returns its audit-log block (from the
// collapse link to the end), whitespace-collapsed.
func auditLogTail(t *testing.T, data map[string]any) string {
	t.Helper()
	tmpl, err := template.New("").Funcs(recordsTemplateFuncs()).ParseFS(templatesFS, "templates/shared/audit_log.html", "templates/records/records_show.html")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var buf strings.Builder
	if err := tmpl.ExecuteTemplate(&buf, "content", data); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	i := strings.Index(out, `href="#auditLog"`)
	if i < 0 {
		t.Fatalf("no audit log in output:\n%s", out)
	}
	return strings.Join(strings.Fields(out[i:]), " ")
}

// Characterization (#487): pins the record page's audit-log markup (badges, user, comment,
// Results column and snapshot sub-row) so moving it into a shared partial changes nothing.
func TestRecordsShowAuditLog_RenderUnchanged(t *testing.T) {
	d := time.Date(2026, 5, 28, 14, 30, 0, 0, time.UTC)
	data := minimalRecordsShowData(attachStepRow("x.png"))
	data["Events"] = []models.RecordEvent{
		{ID: 1, EventType: "completed", Username: "u1", EventDate: &d},
		{ID: 2, EventType: "approved", Username: "u2", EventDate: &d},
		{ID: 3, EventType: "unlocked", Username: "u3", EventDate: &d, Comments: "fix spec"},
		{ID: 4, EventType: "locked", Username: "u4", EventDate: &d},
		{ID: 5, EventType: "custom", Username: "u5", EventDate: &d},
	}
	data["Snapshots"] = map[int][]models.SnapshotDiffRow{1: {
		{RecordResultSnapshot: models.RecordResultSnapshot{Parameter: "V", Result: "5.5"}, Status: "changed"},
		{RecordResultSnapshot: models.RecordResultSnapshot{Parameter: "I", Result: "1"}, Status: "unchanged"},
	}}
	data["AuditResults"] = true
	// Captured from records_show.html before #487 moved the block into shared/audit_log.html.
	const want = `href="#auditLog" role="button"> <i class="bi bi-clock-history"></i> Audit log (5) </a> <div class="collapse" id="auditLog"> <div class="table-responsive-sm"> <table class="table table-sm mt-2 small w-auto"> <thead> <tr><th>Date</th><th>Event</th><th>User</th><th>Comment</th><th>Results</th></tr> </thead> <tbody> ` +
		`<tr> <td class="text-nowrap text-muted">2026-05-28 14:30</td> <td> <span class="badge bg-secondary">completed</span> </td> <td>u1</td> <td></td> <td> <button class="btn btn-sm btn-link p-0" type="button" data-bs-toggle="collapse" data-bs-target="#snap1"> <i class="bi bi-table"></i> View (2) </button> </td> </tr> ` +
		`<tr class="collapse" id="snap1"> <td colspan="5" class="bg-body-tertiary"> <div class="table-responsive-sm"> <table class="table table-sm small mb-0 w-auto"> <thead> <tr><th>Parameter</th><th>Spec</th><th>Units</th><th>Result</th><th>P/F</th><th>Comment</th><th></th></tr> </thead> <tbody> <tr class="table-warning"> <td>V</td> <td></td> <td></td> <td>5.5</td> <td></td> <td></td> <td class="text-muted">changed</td> </tr> <tr class=""> <td>I</td> <td></td> <td></td> <td>1</td> <td></td> <td></td> <td class="text-muted"></td> </tr> </tbody> </table> </div> </td> </tr> ` +
		`<tr> <td class="text-nowrap text-muted">2026-05-28 14:30</td> <td> <span class="badge bg-success">approved</span> </td> <td>u2</td> <td></td> <td> </td> </tr> ` +
		`<tr> <td class="text-nowrap text-muted">2026-05-28 14:30</td> <td> <span class="badge bg-warning text-dark">unlocked</span> </td> <td>u3</td> <td>fix spec</td> <td> </td> </tr> ` +
		`<tr> <td class="text-nowrap text-muted">2026-05-28 14:30</td> <td> <span class="badge bg-secondary">locked</span> </td> <td>u4</td> <td></td> <td> </td> </tr> ` +
		`<tr> <td class="text-nowrap text-muted">2026-05-28 14:30</td> <td> <span class="badge bg-body-tertiary text-body border">custom</span> </td> <td>u5</td> <td></td> <td> </td> </tr> </tbody> </table> </div> </div> </div>`
	if got := auditLogTail(t, data); got != want {
		t.Errorf("audit log markup changed:\ngot:  %s\nwant: %s", got, want)
	}
}

// renderAuditLog executes the shared audit_log partial (#487) on its own.
func renderAuditLog(t *testing.T, data map[string]any) string {
	t.Helper()
	tmpl, err := template.New("").Funcs(recordsTemplateFuncs()).ParseFS(templatesFS, "templates/shared/audit_log.html")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var buf strings.Builder
	if err := tmpl.ExecuteTemplate(&buf, "audit_log", data); err != nil {
		t.Fatalf("execute: %v", err)
	}
	return buf.String()
}

// Red (#487): the form definition page's log (no AuditResults) shows each event's badge, user and
// comment, without the record-only Results column; with no events it renders nothing.
func TestAuditLogPartial_FormEvents(t *testing.T) {
	d := time.Date(2026, 5, 28, 14, 30, 0, 0, time.UTC)
	out := renderAuditLog(t, map[string]any{
		"Events": []models.RecordEvent{
			{ID: 1, EventType: "locked", Username: "admin", EventDate: &d, Comments: "Released revision 1"},
			{ID: 2, EventType: "unlocked", Username: "bob", EventDate: &d, Comments: "Fixing tolerance"},
		},
		"UserLoc": time.UTC,
	})
	for _, want := range []string{"Audit log (2)", `badge bg-secondary">locked`, `badge bg-warning text-dark">unlocked`,
		"Fixing tolerance", "<td>bob</td>", "2026-05-28 14:30"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Results") {
		t.Errorf("form audit log has a Results column:\n%s", out)
	}
	if out := renderAuditLog(t, map[string]any{"Events": []models.RecordEvent(nil), "UserLoc": time.UTC}); strings.TrimSpace(out) != "" {
		t.Errorf("no events: got %q, want empty", out)
	}
}

// Red (#487): with AuditResults the partial keeps the record page's Results column and snapshot sub-row.
func TestAuditLogPartial_ResultsColumn(t *testing.T) {
	d := time.Date(2026, 5, 28, 14, 30, 0, 0, time.UTC)
	out := renderAuditLog(t, map[string]any{
		"Events":       []models.RecordEvent{{ID: 7, EventType: "completed", Username: "u", EventDate: &d}},
		"Snapshots":    map[int][]models.SnapshotDiffRow{7: {{Status: "added"}}},
		"AuditResults": true,
		"UserLoc":      time.UTC,
	})
	for _, want := range []string{"<th>Results</th>", `id="snap7"`, "View (1)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
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
	tmpl, err := template.New("").Funcs(recordsTemplateFuncs()).ParseFS(templatesFS, "templates/shared/audit_log.html", "templates/records/records_show.html")
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
