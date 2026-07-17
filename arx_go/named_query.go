package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// unsafeKeywordRE matches DML/DDL/admin keywords at word boundaries and
// semicolons. INTO blocks SELECT ... INTO (a table-creating write that would
// otherwise slip past a SELECT-prefixed query); WAITFOR blocks a trivial DoS.
// Word boundaries avoid false positives on column names that contain keyword
// substrings (e.g. created_at, updated_at, alternate).
var unsafeKeywordRE = regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE|DROP|EXEC(UTE)?|TRUNCATE|ALTER|CREATE|INTO|MERGE|GRANT|REVOKE|DENY|WAITFOR|DBCC|BACKUP|RESTORE|SHUTDOWN)\b|;`)

// isSafeQuery rejects anything that isn't a plain SELECT.
// Defense-in-depth only — the real control is DB-level: the app DB user
// should have SELECT rights only on the tables named queries are allowed to
// touch. Until that is confirmed per-environment, this check provides a basic
// guard against obviously unsafe SQL stored in named_queries.
func isSafeQuery(q string) bool {
	trimmed := strings.TrimSpace(q)
	if !strings.HasPrefix(strings.ToUpper(trimmed), "SELECT") {
		return false
	}
	return !unsafeKeywordRE.MatchString(trimmed)
}

// NamedQueryInfo is display metadata for one named query (no SQL body — view-only reference).
type NamedQueryInfo struct {
	Name        string
	Description string
	Params      string
	ResultType  string
}

// listNamedQueries returns active named queries ordered by name, for the def-editor reference.
func (h *Handler) listNamedQueries(ctx context.Context) ([]NamedQueryInfo, error) {
	rows, err := h.queryContext(ctx, fmt.Sprintf(
		`SELECT name, COALESCE(description,''), COALESCE(params,''), result_type
		 FROM %s WHERE is_active = %s ORDER BY name`, h.cfg.NamedQueriesTable(), h.dialect.BoolLiteral(true)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []NamedQueryInfo
	for rows.Next() {
		var q NamedQueryInfo
		if err := rows.Scan(&q.Name, &q.Description, &q.Params, &q.ResultType); err != nil {
			continue
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// parseQuerySpec parses "query:name(@param1=value1,@param2=value2)".
// The caller is responsible for resolving {id} tokens in specNom before calling this.
// Returns the query name and a map of param name â†' value.
func parseQuerySpec(specNom string) (name string, params map[string]string) {
	s := strings.TrimPrefix(specNom, "query:")
	params = map[string]string{}

	parenIdx := strings.Index(s, "(")
	if parenIdx < 0 {
		return strings.TrimSpace(s), params
	}

	name = strings.TrimSpace(s[:parenIdx])
	paramStr := strings.Trim(s[parenIdx:], "()")

	for _, p := range strings.Split(paramStr, ",") {
		p = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(p), "@"))
		eqIdx := strings.Index(p, "=")
		if eqIdx < 0 {
			continue
		}
		key := strings.TrimSpace(p[:eqIdx])
		val := strings.TrimSpace(p[eqIdx+1:])
		params[key] = val
	}
	return name, params
}

// QueryRow is one result row from a named query.
// Value is the stored key (col 1). Label is the human-readable display (col 2, or same as Value if absent).
type QueryRow struct {
	Value string
	Label string
}

// QueryResult holds the output of a named query execution.
type QueryResult struct {
	Rows       []QueryRow
	ResultType string // 'list' or 'single' â€" drives UI behavior in data entry
}

// Values returns the stored values as a semicolon-joined string (for read-only display).
func (qr QueryResult) Values() string {
	vals := make([]string, len(qr.Rows))
	for i, r := range qr.Rows {
		vals[i] = r.Value
	}
	return strings.Join(vals, ";")
}

// runNamedQuery fetches a named query from the named_queries table and executes it.
// specNom must already have {id} tokens substituted (call substituteRefs first).
func (h *Handler) runNamedQuery(ctx context.Context, specNom string) (QueryResult, error) {
	name, params := parseQuerySpec(specNom)
	if name == "" {
		return QueryResult{}, fmt.Errorf("empty query name in spec_nom %q", specNom)
	}

	var storedSQL, resultType string
	err := h.queryRowContext(ctx,
		fmt.Sprintf("SELECT sql, result_type FROM %s WHERE name = @p1 AND is_active = %s", h.cfg.NamedQueriesTable(), h.dialect.BoolLiteral(true)),
		name,
	).Scan(&storedSQL, &resultType)
	if err == sql.ErrNoRows {
		return QueryResult{}, fmt.Errorf("named query %q not found", name)
	}
	if err != nil {
		return QueryResult{}, fmt.Errorf("named query lookup: %w", err)
	}

	result, err := h.execQuery(ctx, storedSQL, resultType, params)
	if err != nil {
		return QueryResult{}, fmt.Errorf("named query %q: %w", name, err)
	}
	return result, nil
}

// execQuery runs an ad-hoc parameterized SELECT and scans it into a QueryResult.
// It enforces isSafeQuery first. resultType == "single" truncates to the first row.
// Column handling matches runNamedQuery: 1 col = value+label, 2+ cols = value,label.
func (h *Handler) execQuery(ctx context.Context, sqlText, resultType string, params map[string]string) (QueryResult, error) {
	if !isSafeQuery(sqlText) {
		return QueryResult{}, fmt.Errorf("query must be a plain SELECT statement")
	}

	args := make([]any, 0, len(params))
	for k, v := range params {
		args = append(args, sql.Named(k, v))
	}

	rows, err := h.queryContext(ctx, sqlText, args...)
	if err != nil {
		return QueryResult{}, fmt.Errorf("execution: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return QueryResult{}, fmt.Errorf("columns: %w", err)
	}
	multiCol := len(cols) >= 2

	result := QueryResult{ResultType: resultType}
	for rows.Next() {
		var val, label sql.NullString
		if multiCol {
			if err := rows.Scan(&val, &label); err != nil {
				continue
			}
		} else {
			if err := rows.Scan(&val); err != nil {
				continue
			}
			label = val
		}
		if !val.Valid || val.String == "" {
			continue
		}
		lbl := label.String
		if !label.Valid || lbl == "" {
			lbl = val.String
		}
		result.Rows = append(result.Rows, QueryRow{Value: val.String, Label: lbl})
		if resultType == "single" {
			break // first row only
		}
	}
	return result, nil
}

// APINamedQuery â€" GET /api/named-query?spec=query:name(@param=value)
// Returns JSON picker options for a named query with already-resolved parameters.
func (h *Handler) APINamedQuery(w http.ResponseWriter, r *http.Request) {
	spec := r.URL.Query().Get("spec")
	if !strings.HasPrefix(spec, "query:") {
		http.Error(w, "spec must start with query:", http.StatusBadRequest)
		return
	}
	qr, err := h.runNamedQuery(r.Context(), spec)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type rowJSON struct {
		Value string `json:"value"`
		Label string `json:"label"`
	}
	type resp struct {
		ResultType string    `json:"result_type"`
		Rows       []rowJSON `json:"rows"`
	}
	out := resp{ResultType: qr.ResultType}
	for _, row := range qr.Rows {
		out.Rows = append(out.Rows, rowJSON{Value: row.Value, Label: row.Label})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
