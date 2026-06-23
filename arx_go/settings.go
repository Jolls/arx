package main

import (
	"archive/zip"
	"encoding/csv"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	arxbase "arx/arxlib/config"
	arxdb "arx/arxlib/db"
)

type contactOption struct {
	ID   int
	Name string
}

type supplierOption struct {
	ID   int
	Name string
}

// fetchContactOptions returns active contacts for the Default Contact dropdown.
// When companyID > 0 it is scoped to that company's contacts (the configured
// default receiver); companyID == 0 returns all contacts as a fallback.
func (h *Handler) fetchContactOptions(r *http.Request, companyID int) []contactOption {
	q := fmt.Sprintf(`SELECT id, display_name FROM %s WHERE is_active = 1`, h.cfg.ContactTable())
	var args []any
	if companyID > 0 {
		q += ` AND company_id = @p1`
		args = append(args, companyID)
	}
	q += ` ORDER BY display_name`
	rows, err := h.queryContext(r.Context(), q, args...)
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
	var users []map[string]any
	var usersError string
	if h.db != nil {
		contacts = h.fetchContactOptions(r, h.cfg.PODefaults.ReceiverID)
		suppliers = h.fetchSupplierOptions(r)
		var err error
		users, err = h.listUsers(r.Context())
		if err != nil {
			usersError = "could not load users: " + err.Error()
		}
	}

	data := map[string]any{
		"Connected":            h.db != nil,
		"DBServer":             h.cfg.DBServer,
		"DBName":               h.cfg.DBName,
		"TestDBName":           h.cfg.TestDBName,
		"ActiveDBName":         h.cfg.ActiveDBName(),
		"DBUser":               h.cfg.DBUser,
		"DocControlRoot":       h.cfg.DocControlRoot,
		"POFolderRoot":         h.cfg.POFolderRoot,
		"SupplierFilesRoot":    h.cfg.SupplierFilesRoot,
		"ImageRoot":            h.cfg.ImageRoot,
		"TestMode":             h.cfg.TestMode,
		"DebugMode":            h.cfg.DebugMode,
		"PODefaultContactID":   h.cfg.PODefaults.ContactID,
		"PODefaultReceiverID":  h.cfg.PODefaults.ReceiverID,
		"AttachmentCategories": h.appConfigGetOr(r.Context(), "attachment_categories", ""),
		"PartCategories":       h.loadCategories(r.Context()),
		"Contacts":             contacts,
		"Suppliers":            suppliers,
		"Users":                users,
		"UsersError":           usersError,
		"CurrentUser":          h.currentUser(r),
		"ReleaseNotes":         h.releaseNotes,
		"ActiveTab":            "settings",
		"CsrfToken":            h.csrfToken(w, r),
	}
	for k, v := range extra {
		data[k] = v
	}
	return data
}

func (h *Handler) WhatsNew(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "whats_new.html", map[string]any{
		"ReleaseNotes": h.releaseNotes,
		"ActiveTab":    "settings",
	})
}

func (h *Handler) Settings(w http.ResponseWriter, r *http.Request) {
	r, _ = h.withUser(r)
	h.render(w, r, "settings.html", h.settingsData(w, r, nil))
}

// SettingsAttachmentCategoriesSave persists the attachment-category list to
// app_config. It has its own endpoint so this partial form can't blank the
// path/PO-default fields that SettingsSave writes from the main settings form.
func (h *Handler) SettingsAttachmentCategoriesSave(w http.ResponseWriter, r *http.Request) {
	if h.db != nil {
		cats := strings.Join(splitCSV(r.FormValue("attachment_categories")), ",")
		if err := h.appConfigSet(r.Context(), "attachment_categories", cats); err != nil {
			log.Printf("warning: could not save attachment_categories: %v", err)
		}
	}
	http.Redirect(w, r, "/settings", http.StatusFound)
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
	imageRoot := strings.TrimSpace(r.FormValue("image_root"))
	contactID, _ := strconv.Atoi(r.FormValue("po_default_contact_id"))
	receiverID, _ := strconv.Atoi(r.FormValue("po_default_receiver_id"))

	local, _ := arxbase.LoadLocal()
	if local == nil {
		local = &arxbase.LocalConfig{}
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
	local.ImageRoot = imageRoot
	local.DebugMode = debugMode
	local.TestMode = &testMode
	h.cfg.DocControlRoot = docRoot
	h.cfg.POFolderRoot = poRoot
	h.cfg.SupplierFilesRoot = supplierFilesRoot
	h.cfg.ImageRoot = imageRoot
	h.cfg.DebugMode = debugMode
	h.cfg.TestMode = testMode

	local.PODefaultContactID = &contactID
	local.PODefaultReceiverID = &receiverID
	h.cfg.PODefaults.ContactID = contactID
	h.cfg.PODefaults.ReceiverID = receiverID

	// Use new password if provided, otherwise reconnect with saved password.
	// This lets test-mode toggles take effect immediately without re-entering credentials.
	connectWith := password
	if connectWith == "" {
		connectWith = h.cfg.DBPassword
	}

	var connErr string
	if connectWith != "" {
		dsn := h.cfg.BuildDSN(connectWith)
		newDB, err := arxdb.Connect(dsn)
		if err != nil {
			connErr = err.Error()
		} else {
			oldDB := h.db
			h.db = newDB
			if password != "" {
				local.DBPassword = password
				h.cfg.DBPassword = password
			}
			h.CheckSchemaVersion(r.Context())
			if oldDB != nil {
				oldDB.Close()
			}
		}
	}

	if err := arxbase.SaveLocal(local); err != nil {
		log.Printf("warning: could not save local config: %v", err)
	}

	if connErr != "" {
		h.render(w, r, "settings.html", h.settingsData(w, r, map[string]any{
			"Error": "Connection failed: " + connErr,
		}))
		return
	}

	if h.db != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	h.render(w, r, "settings.html", h.settingsData(w, r, map[string]any{
		"Success": "Settings saved. Enter your database password to connect.",
	}))
}

func (h *Handler) SettingsBackup(w http.ResponseWriter, r *http.Request) {
	date := time.Now().Format("2006-01-02")
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="arx-backup-`+date+`.zip"`)

	zw := zip.NewWriter(w)
	defer zw.Close()

	tables := []string{
		h.cfg.PartsTable(), h.cfg.BOMTable(), h.cfg.CompanyTable(),
		h.cfg.ContactTable(), h.cfg.POTable(), h.cfg.POLineTable(),
		h.cfg.AttachmentsTable(), h.cfg.LinksTable(), h.cfg.PriceTable(),
		h.cfg.MfgPartTable(), h.cfg.SupplierPartTable(), h.cfg.CompanyAttachmentsTable(),
		h.cfg.UnitTable(), h.cfg.AppConfigTable(),
		h.cfg.FormsTable(), h.cfg.RecordsTable(), h.cfg.ResultsTable(),
		h.cfg.StepsTable(), h.cfg.FormEventsTable(), h.cfg.RecordEventsTable(),
		h.cfg.NamedQueriesTable(), h.cfg.TestDefinitionHistoryTable(),
	}

	for _, tbl := range tables {
		if err := h.writeTableCSV(r, zw, tbl); err != nil {
			log.Printf("backup: error exporting %s: %v", tbl, err)
		}
	}
	if err := h.writeTableCSV(r, zw, h.cfg.UsersTable(), "password_hash"); err != nil {
		log.Printf("backup: error exporting %s: %v", h.cfg.UsersTable(), err)
	}
}

func (h *Handler) writeTableCSV(r *http.Request, zw *zip.Writer, table string, excludeCols ...string) error {
	rows, err := h.queryContext(r.Context(), "SELECT * FROM "+table)
	if err != nil {
		return err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return err
	}

	excluded := make(map[string]bool, len(excludeCols))
	for _, c := range excludeCols {
		excluded[c] = true
	}

	// Build index map of columns to include.
	include := make([]int, 0, len(cols))
	filteredCols := make([]string, 0, len(cols))
	for i, c := range cols {
		if !excluded[c] {
			include = append(include, i)
			filteredCols = append(filteredCols, c)
		}
	}

	fw, err := zw.Create(table + ".csv")
	if err != nil {
		return err
	}

	cw := csv.NewWriter(fw)
	if err := cw.Write(filteredCols); err != nil {
		return err
	}

	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}

	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		row := make([]string, len(include))
		for j, i := range include {
			if vals[i] == nil {
				row[j] = ""
			} else {
				row[j] = fmt.Sprintf("%v", vals[i])
			}
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}

	cw.Flush()
	return cw.Error()
}
