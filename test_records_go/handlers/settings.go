package handlers

import (
	"log"
	"net/http"
	"strings"

	"arx/test_records_go/config"
	"arx/test_records_go/db"
)

func (h *Handler) Settings(w http.ResponseWriter, r *http.Request) {
	h.render(w, "settings.html", h.settingsData(w, r, nil))
}

func (h *Handler) SettingsSave(w http.ResponseWriter, r *http.Request) {

	dbServer := strings.TrimSpace(r.FormValue("db_server"))
	dbName := strings.TrimSpace(r.FormValue("db_name"))
	dbUser := strings.TrimSpace(r.FormValue("db_user"))
	password := strings.TrimSpace(r.FormValue("db_password"))
	imageRoot := strings.TrimSpace(r.FormValue("image_root"))
	docRoot := strings.TrimSpace(r.FormValue("doc_control_root"))

	local, _ := config.LoadLocal()
	if local == nil {
		local = &config.LocalConfig{}
	}

	if dbServer != "" {
		local.DBServer = dbServer
		h.cfg.DBServer = dbServer
	}
	if dbName != "" {
		local.DBName = dbName
		h.cfg.DBName = dbName
	}
	if dbUser != "" {
		local.DBUser = dbUser
		h.cfg.DBUser = dbUser
	}

	debugMode := r.FormValue("debug_mode") == "1"
	testMode := r.FormValue("test_mode") == "1"
	local.ImageRoot = imageRoot
	local.DocControlRoot = docRoot
	local.DebugMode = debugMode
	local.TestMode = &testMode
	h.cfg.ImageRoot = imageRoot
	h.cfg.DocControlRoot = docRoot
	h.cfg.DebugMode = debugMode
	h.cfg.TestMode = testMode

	var connErr string
	if password != "" {
		dsn := h.cfg.BuildDSN(password)
		newDB, err := db.Connect(dsn)
		if err != nil {
			connErr = err.Error()
		} else {
			if h.db != nil {
				h.db.Close()
			}
			h.db = newDB
			local.DBPassword = password
			h.cfg.DBPassword = password
			h.CheckSchemaVersion(r.Context())
		}
	}

	if err := config.SaveLocal(local); err != nil {
		log.Printf("warning: could not save local config: %v", err)
	}

	if connErr != "" {
		h.render(w, "settings.html", h.settingsData(w, r, map[string]any{
			"Error": "Connection failed: " + connErr,
		}))
		return
	}

	if h.db != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	h.render(w, "settings.html", h.settingsData(w, r, map[string]any{
		"Success": "Settings saved. Enter your database password to connect.",
	}))
}

func (h *Handler) WhatsNew(w http.ResponseWriter, r *http.Request) {
	h.render(w, "whats_new.html", map[string]any{
		"ReleaseNotes": h.releaseNotes,
	})
}

func (h *Handler) settingsData(w http.ResponseWriter, r *http.Request, extra map[string]any) map[string]any {
	data := map[string]any{
		"Connected":      h.db != nil,
		"DBServer":       h.cfg.DBServer,
		"DBName":         h.cfg.DBName,
		"DBUser":         h.cfg.DBUser,
		"ImageRoot":      h.cfg.ImageRoot,
		"DocControlRoot": h.cfg.DocControlRoot,
		"TestMode":       h.cfg.TestMode,
		"DebugMode":      h.cfg.DebugMode,
		"ReleaseNotes":   h.releaseNotes,
		"CsrfToken":      h.csrfToken(w, r),
	}
	for k, v := range extra {
		data[k] = v
	}
	return data
}

