package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"arx/internal/records"
)

// nqDateFormat is the display format for named-query updated dates.
const nqDateFormat = "2006-01-02"

// NamedQueryRow is one editable named_queries row for the Settings editor.
type NamedQueryRow struct {
	ID          int
	Name        string
	Description string
	SQL         string
	Params      string
	ResultType  string
	Active      bool
	Updated     string // updated_at formatted for display; blank if never saved
}

// validResultType reports whether rt is one of the allowed result_type values.
func validResultType(rt string) bool {
	switch rt {
	case "list", "single", "multi":
		return true
	}
	return false
}

// loadNamedQueriesFull returns all named queries (active and inactive) with their
// SQL bodies and ids, for the Settings → Named Queries editor. Distinct from
// listNamedQueries, which is the view-only active-only reference used elsewhere.
func (h *Handler) loadNamedQueriesFull(ctx context.Context) ([]NamedQueryRow, error) {
	rows, err := h.records().ListNamedQueries(ctx)
	if err != nil {
		return nil, err
	}

	loc := h.userLocationCtx(ctx)
	var out []NamedQueryRow
	for _, r := range rows {
		q := NamedQueryRow{ID: r.ID, Name: r.Name, Description: r.Description, SQL: r.Sql, Params: r.Params,
			ResultType: r.ResultType, Active: r.IsActive}
		if r.UpdatedAt != nil {
			q.Updated = r.UpdatedAt.In(loc).Format(nqDateFormat)
		}
		out = append(out, q)
	}
	return out, nil
}

// SettingsNamedQueryRowSave inserts or updates a single named query and returns
// the row's id as JSON. Saving is per-row (no bulk write) so an unchanged row is
// never rewritten and a stray edit can't clobber the whole table. A blank id
// inserts a new row; a non-zero id updates that row in place (preserving
// created_at, bumping updated_at). The SELECT-only guard is enforced here.
func (h *Handler) SettingsNamedQueryRowSave(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	writeErr := func(status int, msg string) {
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"error": msg})
	}
	if h.database() == nil {
		writeErr(http.StatusServiceUnavailable, "Not connected to database.")
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	sqlText := strings.TrimSpace(r.FormValue("sql"))
	if name == "" {
		writeErr(http.StatusBadRequest, "Name is required.")
		return
	}
	if sqlText == "" {
		writeErr(http.StatusBadRequest, "SQL is required.")
		return
	}
	if !isSafeQuery(sqlText) {
		writeErr(http.StatusBadRequest, "Query must be a plain SELECT statement (no INSERT/UPDATE/DELETE/DDL or semicolons).")
		return
	}
	resultType := strings.TrimSpace(r.FormValue("result_type"))
	if !validResultType(resultType) {
		resultType = "list"
	}
	description := strings.TrimSpace(r.FormValue("description"))
	params := strings.TrimSpace(r.FormValue("params"))
	active := r.FormValue("active") == "1"

	ctx := r.Context()
	id, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("id")))
	in := records.NamedQueryInput{Name: name, Description: description, Sql: sqlText, Params: params,
		ResultType: resultType, IsActive: active}

	if id > 0 {
		found, err := h.records().UpdateNamedQuery(ctx, id, in)
		if err != nil {
			writeErr(http.StatusBadRequest, namedQuerySaveError(name, err))
			return
		}
		if !found {
			writeErr(http.StatusNotFound, "That named query no longer exists — reload the page.")
			return
		}
	} else {
		var err error
		id, err = h.records().InsertNamedQuery(ctx, in)
		if err != nil {
			writeErr(http.StatusBadRequest, namedQuerySaveError(name, err))
			return
		}
	}
	json.NewEncoder(w).Encode(map[string]any{"id": id, "updated": time.Now().In(h.userLocation(r)).Format(nqDateFormat)})
}

// namedQuerySaveError turns a unique-constraint violation into a readable message
// and otherwise wraps the driver error with the offending query name.
func namedQuerySaveError(name string, err error) string {
	m := strings.ToLower(err.Error())
	if strings.Contains(m, "unique") || strings.Contains(m, "duplicate key") {
		return fmt.Sprintf("A named query named %q already exists — names must be unique.", name)
	}
	return fmt.Sprintf("Could not save named query %q: %s", name, err.Error())
}

// SettingsNamedQueryTest runs a single ad-hoc named query (the one being edited)
// with supplied test parameters and returns the resulting rows as JSON. It does
// not read or write the named_queries table — it executes the posted sql directly
// (SELECT-only, enforced by execQuery) so an admin can test before saving.
func (h *Handler) SettingsNamedQueryTest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	writeErr := func(status int, msg string) {
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"error": msg})
	}

	if h.database() == nil {
		writeErr(http.StatusServiceUnavailable, "Not connected to database.")
		return
	}
	sqlText := strings.TrimSpace(r.FormValue("sql"))
	if sqlText == "" {
		writeErr(http.StatusBadRequest, "No SQL to test.")
		return
	}
	resultType := strings.TrimSpace(r.FormValue("result_type"))
	if !validResultType(resultType) {
		resultType = "list"
	}

	// Params arrive as parallel arrays: param_name / param_value.
	names := r.Form["param_name"]
	values := r.Form["param_value"]
	params := map[string]string{}
	for i, name := range names {
		name = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(name), "@"))
		if name == "" {
			continue
		}
		if i < len(values) {
			params[name] = values[i]
		}
	}

	qr, err := h.execQuery(r.Context(), sqlText, resultType, params)
	if err != nil {
		writeErr(http.StatusBadRequest, err.Error())
		return
	}

	type rowJSON struct {
		Value string `json:"value"`
		Label string `json:"label"`
	}
	out := struct {
		ResultType string    `json:"result_type"`
		Rows       []rowJSON `json:"rows"`
	}{ResultType: qr.ResultType}
	for _, row := range qr.Rows {
		out.Rows = append(out.Rows, rowJSON{Value: row.Value, Label: row.Label})
	}
	json.NewEncoder(w).Encode(out)
}
