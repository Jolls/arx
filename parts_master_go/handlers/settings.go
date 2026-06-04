package handlers

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"arx/parts_master_go/config"
	"arx/parts_master_go/db"
)

type contactOption struct {
	ID   int
	Name string
}

type supplierOption struct {
	ID   int
	Name string
}

func (h *Handler) fetchContactOptions(r *http.Request) []contactOption {
	rows, err := h.queryContext(r.Context(),
		fmt.Sprintf(`SELECT CNID, CNName FROM %s WHERE CNActive = 1 ORDER BY CNName`,
			h.cfg.ContactTable()))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []contactOption
	for rows.Next() {
		var c contactOption
		if rows.Scan(&c.ID, &c.Name) == nil {
			out = append(out, c)
		}
	}
	return out
}

func (h *Handler) fetchSupplierOptions(r *http.Request) []supplierOption {
	rows, err := h.queryContext(r.Context(),
		fmt.Sprintf(`SELECT id, name FROM %s WHERE is_active = 1 ORDER BY name`,
			h.cfg.CompanyTable()))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []supplierOption
	for rows.Next() {
		var s supplierOption
		if rows.Scan(&s.ID, &s.Name) == nil {
			out = append(out, s)
		}
	}
	return out
}

func (h *Handler) settingsData(w http.ResponseWriter, r *http.Request, extra map[string]any) map[string]any {
	var contacts []contactOption
	var suppliers []supplierOption
	if h.db != nil {
		contacts = h.fetchContactOptions(r)
		suppliers = h.fetchSupplierOptions(r)
	}

	data := map[string]any{
		"Connected":              h.db != nil,
		"DBServer":               h.cfg.DBServer,
		"DBName":                 h.cfg.DBName,
		"TestDBName":             h.cfg.TestDBName,
		"ActiveDBName":           h.cfg.ActiveDBName(),
		"DBUser":                 h.cfg.DBUser,
		"DocControlRoot":         h.cfg.DocControlRoot,
		"POFolderRoot":           h.cfg.POFolderRoot,
		"SupplierFilesRoot":      h.cfg.SupplierFilesRoot,
		"TestMode":               h.cfg.TestMode,
		"DebugMode":              h.cfg.DebugMode,
		"PODefaultContactID":     h.cfg.PODefaults.ContactID,
		"PODefaultReceiverID":    h.cfg.PODefaults.ReceiverID,
		"AttachmentCategories":   h.appConfigGetOr(r.Context(), "attachment_categories", ""),
		"Contacts":               contacts,
		"Suppliers":              suppliers,
		"ReleaseNotes":           h.releaseNotes,
		"ActiveTab":              "settings",
		"CsrfToken":              h.csrfToken(w, r),
	}
	for k, v := range extra {
		data[k] = v
	}
	return data
}

func (h *Handler) WhatsNew(w http.ResponseWriter, r *http.Request) {
	h.render(w, "whats_new.html", map[string]any{
		"ReleaseNotes": h.releaseNotes,
		"ActiveTab":    "settings",
	})
}

func (h *Handler) Settings(w http.ResponseWriter, r *http.Request) {
	h.render(w, "settings.html", h.settingsData(w, r, nil))
}

func (h *Handler) SettingsSave(w http.ResponseWriter, r *http.Request) {

	dbServer := strings.TrimSpace(r.FormValue("db_server"))
	dbName := strings.TrimSpace(r.FormValue("db_name"))
	testDBName := strings.TrimSpace(r.FormValue("test_db_name"))
	dbUser := strings.TrimSpace(r.FormValue("db_user"))
	password := strings.TrimSpace(r.FormValue("db_password"))
	docRoot := strings.TrimSpace(r.FormValue("doc_control_root"))
	poRoot := strings.TrimSpace(r.FormValue("po_folder_root"))
	supplierFilesRoot := strings.TrimSpace(r.FormValue("supplier_files_root"))
	contactID, _ := strconv.Atoi(r.FormValue("po_default_contact_id"))
	receiverID, _ := strconv.Atoi(r.FormValue("po_default_receiver_id"))

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
	if testDBName != "" {
		local.TestDBName = testDBName
		h.cfg.TestDBName = testDBName
	}
	if dbUser != "" {
		local.DBUser = dbUser
		h.cfg.DBUser = dbUser
	}

	debugMode := r.FormValue("debug_mode") == "1"
	testMode := r.FormValue("test_mode") == "1"
	local.DocControlRoot = docRoot
	local.POFolderRoot = poRoot
	local.SupplierFilesRoot = supplierFilesRoot
	local.DebugMode = debugMode
	local.TestMode = &testMode
	h.cfg.DocControlRoot = docRoot
	h.cfg.POFolderRoot = poRoot
	h.cfg.SupplierFilesRoot = supplierFilesRoot
	h.cfg.DebugMode = debugMode
	h.cfg.TestMode = testMode

	local.PODefaultContactID = &contactID
	local.PODefaultReceiverID = &receiverID
	h.cfg.PODefaults.ContactID = contactID
	h.cfg.PODefaults.ReceiverID = receiverID

	if h.db != nil {
		cats := strings.Join(splitCSV(r.FormValue("attachment_categories")), ",")
		if err := h.appConfigSet(r.Context(), "attachment_categories", cats); err != nil {
			log.Printf("warning: could not save attachment_categories: %v", err)
		}
	}

	// Use new password if provided, otherwise reconnect with saved password.
	// This lets test-mode toggles take effect immediately without re-entering credentials.
	connectWith := password
	if connectWith == "" {
		connectWith = h.cfg.DBPassword
	}

	var connErr string
	if connectWith != "" {
		dsn := h.cfg.BuildDSN(connectWith)
		newDB, err := db.Connect(dsn)
		if err != nil {
			connErr = err.Error()
		} else {
			if h.db != nil {
				h.db.Close()
			}
			h.db = newDB
			if password != "" {
				local.DBPassword = password
				h.cfg.DBPassword = password
			}
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
