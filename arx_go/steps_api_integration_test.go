//go:build integration

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// stepsResolve posts body to APIStepsResolve and decodes the response.
func stepsResolve(t *testing.T, h *Handler, body any) stepResolveResponse {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/steps/resolve", bytes.NewReader(b)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.APIStepsResolve(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("APIStepsResolve: status %d: %s", rec.Code, rec.Body.String())
	}
	var out stepResolveResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v: %s", err, rec.Body.String())
	}
	return out
}

// #469: the edit page embeds its step context and re-resolves live through /api/steps/resolve with the
// values being typed; queries run once and are echoed back rather than re-run.
func TestIntegration_StepsResolve_EditPageLive(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	f := recForm(t, h)
	qname := smokeUniq("lim")
	qid := repInsert(t, h, `INSERT INTO named_queries (name, sql, description, params) VALUES ($1,'SELECT ''4''',NULL,NULL) RETURNING id`,
		[]any{qname}, `DELETE FROM named_queries WHERE id=$1`)
	sA := wStep(t, h, f.formID, 0, "Upstream", nil)
	sB := wStep(t, h, f.formID, 0, "Bounded", map[string]any{"spec_max": fmt.Sprintf("={%d}+1", sA), "specification": "<= {max}"})
	sC := wStep(t, h, f.formID, 0, "Defaulted", map[string]any{"default_result": fmt.Sprintf("={%d}*2", sA)})
	sD := wStep(t, h, f.formID, 0, "Conditional", map[string]any{"hide_formula": fmt.Sprintf("{%d}=skip", sA)})
	sE := wStep(t, h, f.formID, 0, "Queried", map[string]any{"spec_max": "query:" + qname})
	repExec(t, h, `UPDATE form SET test_order=$1 WHERE id=$2`, wOrder(sA, sB, sC, sD, sE), f.formID)
	r := wNewID(t, wPost(h.CreateRecord, "/forms/x/records/new", url.Values{"serial_number": {"SN-L1"}, "suggested_serial_number": {"1"}}, f.formID), "/records/")
	wCleanupRecord(t, h, r)

	// The page's embedded context, exactly as the browser would post it back.
	edit := recGet(h.EditRecord, "/records/x/edit", r)
	page := edit.Body.String()
	start := strings.Index(page, "var STEP_CTX = ")
	if start < 0 {
		t.Fatal("edit page has no STEP_CTX")
	}
	raw := page[start+len("var STEP_CTX = "):]
	raw = raw[:strings.Index(raw, ";\n")]
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("STEP_CTX isn't JSON: %v: %s", err, raw)
	}
	body["run_queries"] = true
	id := strconv.Itoa

	body["values"] = map[string]string{id(sA): "4"}
	got := stepsResolve(t, h, body)
	if s := got.Steps[sB]; s.SpecMax != "5" || s.Specification != "<= 5" {
		t.Errorf("live bound = %q / %q", s.SpecMax, s.Specification)
	}
	if s := got.Steps[sC]; s.DefaultResult != "8" {
		t.Errorf("live default = %q", s.DefaultResult)
	}
	if got.Steps[sD].Hidden || got.Steps[sE].SpecMax != "4" || got.Queries["query:"+qname] != "4" {
		t.Errorf("hidden %v / query bound %q / queries %v", got.Steps[sD].Hidden, got.Steps[sE].SpecMax, got.Queries)
	}

	body["values"] = map[string]string{id(sA): "skip"}
	got = stepsResolve(t, h, body)
	if !got.Steps[sD].Hidden || got.Steps[sC].DefaultResult != "" {
		t.Errorf("after skip: hidden %v default %q", got.Steps[sD].Hidden, got.Steps[sC].DefaultResult)
	}

	// Echoed queries aren't re-run; without them the query runs again.
	repExec(t, h, `UPDATE named_queries SET sql='SELECT ''9''' WHERE id=$1`, qid)
	body["queries"] = got.Queries
	if s := stepsResolve(t, h, body).Steps[sE]; s.SpecMax != "4" {
		t.Errorf("echoed query re-ran: %q", s.SpecMax)
	}
	delete(body, "queries")
	if s := stepsResolve(t, h, body).Steps[sE]; s.SpecMax != "9" {
		t.Errorf("fresh query = %q, want 9", s.SpecMax)
	}

	// A failing query reports a user-safe error; echoed back as failed, it isn't retried or re-reported.
	repExec(t, h, `UPDATE named_queries SET name=name||'_gone' WHERE id=$1`, qid)
	got = stepsResolve(t, h, body)
	spec := "query:" + qname
	if msg := got.QueryErrors[spec]; !strings.Contains(msg, "named query not found") {
		t.Errorf("query error = %q", msg)
	}
	body["failed_queries"] = []string{spec}
	if got = stepsResolve(t, h, body); len(got.QueryErrors) != 0 {
		t.Errorf("echoed failed query re-reported: %v", got.QueryErrors)
	}
	if len(got.Steps[sB].Warnings) != 0 {
		t.Errorf("edit-page resolve returned warnings without lint: %v", got.Steps[sB].Warnings)
	}
}

// #469/#471: the definition editor previews an unsaved row against the form's newest record, and lints it,
// without writing anything.
func TestIntegration_StepsResolve_DefinitionPreview(t *testing.T) {
	h, cleanup := liveHandler(t)
	t.Cleanup(cleanup)
	f := recForm(t, h)
	r := wNewID(t, wPost(h.CreateRecord, "/forms/x/records/new", url.Values{"serial_number": {"SN-P1"}, "suggested_serial_number": {"1"}}, f.formID), "/records/")
	wCleanupRecord(t, h, r)
	var histBefore, histAfter int
	wScan(t, h, `SELECT COUNT(*) FROM form_row_history h JOIN form_row s ON s.id=h.form_row_id WHERE s.form_id=$1`, []any{f.formID}, &histBefore)

	unsaved := stepDefJSON{ID: -1, Parameter: "{record.sn} on {form.pn}", Specification: "{2147483000}", SpecMin: "5mW"}
	got := stepsResolve(t, h, map[string]any{
		"steps":   []stepDefJSON{unsaved, {ID: f.s2, SpecNom: "5"}},
		"show":    []int{-1},
		"form_id": f.formID, "record_id": -1, "run_queries": true, "lint": true,
	})
	if !strings.Contains(got.SampleRecord, "SN-P1") {
		t.Errorf("sample record = %q", got.SampleRecord)
	}
	s := got.Steps[-1]
	if s.Parameter != "SN-P1 on "+f.formPN {
		t.Errorf("preview parameter = %q", s.Parameter)
	}
	var msgs []string
	for _, w := range s.Warnings {
		msgs = append(msgs, w.Field+": "+w.Msg)
	}
	if want := []string{"specification: No step {2147483000} on this form", "spec_min: Not a number; P/F will be blank"}; fmt.Sprint(msgs) != fmt.Sprint(want) {
		t.Errorf("warnings = %v, want %v", msgs, want)
	}
	wScan(t, h, `SELECT COUNT(*) FROM form_row_history h JOIN form_row s ON s.id=h.form_row_id WHERE s.form_id=$1`, []any{f.formID}, &histAfter)
	if histAfter != histBefore {
		t.Errorf("preview wrote %d history rows", histAfter-histBefore)
	}
}
