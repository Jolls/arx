package handlers

import (
	"encoding/json"
	"net/http"

	"arx/arxlib/folderpick"
)

// APIBrowseFolder opens a native Windows folder-picker dialog via PowerShell
// and returns the selected path as JSON {"path":"..."}.
// Used by the Settings page Browse buttons.
func (h *Handler) APIBrowseFolder(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"path": folderpick.BrowseFolder()})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
