package main

import (
	"archive/zip"
	"encoding/base64"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	arxbase "arx/arxlib/config"
	arxdb "arx/arxlib/db"
)

// landingPreset is one selectable landing-page option (issue #282): a major
// nav tab, offered in the Settings → My Preferences dropdown alongside a
// free-form "Custom link…" choice.
type landingPreset struct {
	Path  string
	Label string
}

var landingPresets = []landingPreset{
	{"/parts", "Parts"},
	{"/suppliers", "Vendors"},
	{"/pos", "POs"},
	{"/contacts", "Contacts"},
	{"/records", "Records"},
	{"/reports", "Reports"},
}

func isPresetLanding(path string) bool {
	for _, p := range landingPresets {
		if p.Path == path {
			return true
		}
	}
	return false
}

// sanitizeLandingRoute reduces a user-pasted link to a safe same-origin
// relative path (path + query, no scheme/host) so it can be used as a
// post-login redirect target without opening a redirect to another host.
// Returns false for empty input or anything that isn't a rooted path.
func sanitizeLandingRoute(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	ri := u.RequestURI() // drops scheme/host; e.g. "http://host/?f=1" -> "/?f=1"
	if !strings.HasPrefix(ri, "/") || strings.HasPrefix(ri, "//") {
		return "", false
	}
	return ri, true
}

// firstNonEmpty returns the first non-empty string in vals, or "" if all are empty.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

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
	var users []map[string]any
	var usersError string
	var namedQueries []NamedQueryRow
	var namedQueriesError string
	var partNumberingPreview string
	// Per-user PO defaults for the My Preferences tab (issue #463).
	var poContacts []contactOption
	var poSuppliers []supplierOption
	var poReceiverName string
	var poContactID, poReceiverID int
	accentColor := "blue"
	landingRoutePref := "/"
	if h.db != nil {
		if u := h.currentUser(r); u != nil {
			poContactID = u.DefaultPOContactID
			poReceiverID = u.DefaultPOReceiverID
			poContacts = h.fetchContactOptions(r, poReceiverID)
			poSuppliers = h.fetchSupplierOptions(r)
			for _, s := range poSuppliers {
				if s.ID == poReceiverID {
					poReceiverName = s.Name
					break
				}
			}
			if isValidAccentTheme(u.AccentColor) {
				accentColor = u.AccentColor
			}
			landingRoutePref = landingRoute(u)
		}
		var err error
		users, err = h.listUsers(r.Context())
		if err != nil {
			usersError = "could not load users: " + err.Error()
		}
		namedQueries, err = h.loadNamedQueriesFull(r.Context())
		if err != nil {
			namedQueriesError = "could not load named queries: " + err.Error()
		}
		partNumberingPreview, _ = h.nextBaseNumber(r.Context())
	}

	data := map[string]any{
		"Connected":             h.db != nil,
		"DBServer":              h.cfg.DBServer,
		"DBName":                h.cfg.DBName,
		"DBEngine":              h.cfg.DBEngine(),
		"TestDBServer":          h.cfg.TestDBServer,
		"TestEngine":            h.cfg.TestEngine,
		"TestDBName":            h.cfg.TestDBName,
		"TestDBUser":            h.cfg.TestDBUser,
		"TestDBPasswordSet":     h.cfg.TestDBPassword != "",
		"ActiveDBName":          h.cfg.ActiveDBName(),
		"DBUser":                h.cfg.DBUser,
		"DocControlRoot":        h.cfg.DocControlRoot,
		"POFolderRoot":          h.cfg.POFolderRoot,
		"SupplierFilesRoot":     h.cfg.SupplierFilesRoot,
		"ImageRoot":             h.cfg.ImageRoot,
		"TestMode":              h.cfg.TestMode,
		"DebugMode":             h.cfg.DebugMode,
		"PODefaultContactID":    poContactID,
		"PODefaultReceiverID":   poReceiverID,
		"PODefaultReceiverName": poReceiverName,
		"POContacts":            poContacts,
		"POSuppliers":           poSuppliers,
		"AttachmentCategories":  h.appConfigGetOr(r.Context(), "attachment_categories", ""),
		"CompanyLogo":           h.companyLogoURL(),
		"AccentColor":           accentColor,
		"AccentThemes":          accentThemes,
		"LandingRoute":          landingRoutePref,
		"LandingRouteIsCustom":  !isPresetLanding(landingRoutePref),
		"LandingPresets":        landingPresets,
		"PartCategories":        h.partCategories,
		"PartNumbering":         h.loadBaseNumberConfig(r.Context()),
		"PartNumberingPreview":  partNumberingPreview,
		"NamedQueries":          namedQueries,
		"NamedQueriesError":     namedQueriesError,
		"Users":                 users,
		"UsersError":            usersError,
		"CurrentUser":           h.currentUser(r),
		"ReleaseNotes":          h.releaseNotes,
		"ActiveTab":             "settings",
		"CsrfToken":             h.csrfToken(w, r),
	}
	for k, v := range extra {
		data[k] = v
	}
	return data
}

func (h *Handler) WhatsNew(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "settings/whats_new.html", map[string]any{
		"ReleaseNotes": h.releaseNotes,
		"ActiveTab":    "settings",
	})
}

func (h *Handler) Settings(w http.ResponseWriter, r *http.Request) {
	if h.db != nil {
		r, _ = h.withUser(r)
	}
	h.render(w, r, "settings/settings.html", h.settingsData(w, r, nil))
}

// SettingsAttachmentCategoriesSave persists the attachment-category list to
// app_config. It has its own endpoint so this partial form can't blank the
// path fields that SettingsSave writes from the main settings form.
func (h *Handler) SettingsAttachmentCategoriesSave(w http.ResponseWriter, r *http.Request) {
	if h.db != nil {
		cats := strings.Join(splitCSV(r.FormValue("attachment_categories")), ",")
		if err := h.appConfigSet(r.Context(), "attachment_categories", cats); err != nil {
			log.Printf("warning: could not save attachment_categories: %v", err)
		}
	}
	http.Redirect(w, r, "/settings", http.StatusFound)
}

// SettingsAccentColorSave persists the logged-in user's accent color theme
// preference (Settings → My Preferences tab, issue #537). It has its own
// endpoint so this partial form can't blank the fields the main settings
// form writes.
func (h *Handler) SettingsAccentColorSave(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	color := r.FormValue("accent_color")
	if !isValidAccentTheme(color) {
		http.Redirect(w, r, "/settings#preferences", http.StatusFound)
		return
	}
	if _, err := h.execContext(r.Context(), fmt.Sprintf(
		`UPDATE %s SET accent_color = @p1 WHERE id = @p2`,
		h.cfg.UsersTable()), color, u.ID); err != nil {
		log.Printf("warning: could not save accent_color: %v", err)
	}
	h.invalidateUserCache(u.ID)
	http.Redirect(w, r, "/settings#preferences", http.StatusFound)
}

// SettingsDefaultRouteSave persists the logged-in user's post-login landing
// page preference (Settings → My Preferences tab, issue #282). The choice is
// either a preset tab path or a sanitized custom relative path. It has its own
// endpoint so this partial form can't blank the fields the main settings
// form writes.
func (h *Handler) SettingsDefaultRouteSave(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	var route string
	choice := r.FormValue("landing_choice")
	if choice == "custom" {
		s, ok := sanitizeLandingRoute(r.FormValue("custom_route"))
		if !ok {
			http.Redirect(w, r, "/settings#preferences", http.StatusFound)
			return
		}
		route = s
	} else if isPresetLanding(choice) {
		route = choice
	} else {
		http.Redirect(w, r, "/settings#preferences", http.StatusFound)
		return
	}
	if _, err := h.execContext(r.Context(), fmt.Sprintf(
		`UPDATE %s SET default_route = @p1 WHERE id = @p2`,
		h.cfg.UsersTable()), route, u.ID); err != nil {
		log.Printf("warning: could not save default_route: %v", err)
	}
	h.invalidateUserCache(u.ID)
	http.Redirect(w, r, "/settings#preferences", http.StatusFound)
}

// SettingsCompanyLogoSave stores an uploaded logo as a base64 data URI in
// app_config. It has its own endpoint so this partial form can't blank the
// path fields that SettingsSave writes from the main settings form.
func (h *Handler) SettingsCompanyLogoSave(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		http.Redirect(w, r, "/settings", http.StatusFound)
		return
	}
	settingsError := func(msg string) {
		h.render(w, r, "settings/settings.html", h.settingsData(w, r, map[string]any{"Error": msg}))
	}
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		settingsError("Logo upload failed: " + err.Error())
		return
	}
	file, _, err := r.FormFile("company_logo")
	if err != nil {
		settingsError("Logo upload failed: " + err.Error())
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		settingsError("Logo upload failed: " + err.Error())
		return
	}
	mime := http.DetectContentType(data)
	if _, ok := pasteImageExts[mime]; !ok {
		settingsError("Unsupported image type: " + mime)
		return
	}
	dataURI := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
	if err := h.appConfigSet(r.Context(), "company_logo", dataURI); err != nil {
		log.Printf("warning: could not save company_logo: %v", err)
	}
	h.companyLogo = dataURI
	http.Redirect(w, r, "/settings", http.StatusFound)
}

// SettingsCompanyLogoRemove clears the stored company logo.
func (h *Handler) SettingsCompanyLogoRemove(w http.ResponseWriter, r *http.Request) {
	if h.db != nil {
		if err := h.appConfigSet(r.Context(), "company_logo", ""); err != nil {
			log.Printf("warning: could not clear company_logo: %v", err)
		}
		h.companyLogo = ""
	}
	http.Redirect(w, r, "/settings", http.StatusFound)
}

func (h *Handler) SettingsSave(w http.ResponseWriter, r *http.Request) {

	dbServer := strings.TrimSpace(r.FormValue("db_server"))
	dbName := strings.TrimSpace(r.FormValue("db_name"))
	dbUser := strings.TrimSpace(r.FormValue("db_user"))
	password := strings.TrimSpace(r.FormValue("db_password"))
	// Test-mode connection profile. Server/engine/user are blank-clearable so a
	// test override can be removed; a blank field then inherits the prod value.
	testDBServer := strings.TrimSpace(r.FormValue("test_db_server"))
	testEngine := strings.TrimSpace(r.FormValue("test_engine"))
	testDBName := strings.TrimSpace(r.FormValue("test_db_name"))
	testDBUser := strings.TrimSpace(r.FormValue("test_db_user"))
	testPassword := strings.TrimSpace(r.FormValue("test_db_password"))
	docRoot := strings.TrimSpace(r.FormValue("doc_control_root"))
	poRoot := strings.TrimSpace(r.FormValue("po_folder_root"))
	supplierFilesRoot := strings.TrimSpace(r.FormValue("supplier_files_root"))
	imageRoot := strings.TrimSpace(r.FormValue("image_root"))

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
	if dbUser != "" {
		local.DBUser = dbUser
		h.cfg.DBUser = dbUser
	}

	// Test-profile server/engine/user are written verbatim (blank clears the
	// override). TestDBName keeps the "only if non-empty" guard so the ArxDev
	// default is never wiped by an empty submit.
	local.TestDBServer = testDBServer
	h.cfg.TestDBServer = testDBServer
	local.TestEngine = testEngine
	h.cfg.TestEngine = testEngine
	local.TestDBUser = testDBUser
	h.cfg.TestDBUser = testDBUser
	if testDBName != "" {
		local.TestDBName = testDBName
		h.cfg.TestDBName = testDBName
	}

	debugMode := r.FormValue("debug_mode") == "1"
	testMode := r.FormValue("test_mode") == "1"
	testModeChanged := testMode != h.cfg.TestMode
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

	// Connect with the active profile's password: prefer a freshly-entered value,
	// then the stored one. This lets test-mode toggles take effect immediately
	// without re-entering credentials. In test mode the test password wins and
	// falls back to the prod password when unset (mirrors Base.activePassword).
	var connectWith string
	if h.cfg.TestMode {
		connectWith = firstNonEmpty(testPassword, h.cfg.TestDBPassword, password, h.cfg.DBPassword)
	} else {
		connectWith = firstNonEmpty(password, h.cfg.DBPassword)
	}

	var connErr string
	dbSwapped := false
	if connectWith != "" {
		dsn := h.cfg.BuildDSN(connectWith)
		newDB, newDialect, err := arxdb.Connect(h.cfg.DBEngine(), dsn)
		if err != nil {
			connErr = err.Error()
		} else {
			oldDB := h.db
			h.db = newDB
			h.dialect = newDialect
			dbSwapped = true
			if password != "" {
				local.DBPassword = password
				h.cfg.DBPassword = password
			}
			if testPassword != "" {
				local.TestDBPassword = testPassword
				h.cfg.TestDBPassword = testPassword
			}
			h.CheckSchemaVersion(r.Context())
			h.loadCompanyLogo(r.Context())
			h.loadPartCategories(r.Context())
			if oldDB != nil {
				oldDB.Close()
			}
		}
	}

	if err := arxbase.SaveLocal(local); err != nil {
		log.Printf("warning: could not save local config: %v", err)
	}

	if connErr != "" {
		h.render(w, r, "settings/settings.html", h.settingsData(w, r, map[string]any{
			"Error": "Connection failed: " + connErr,
		}))
		return
	}

	if testModeChanged && dbSwapped {
		// Auth is per-database: each DB has its own users table, so the
		// current session does not identify a real user in the database we
		// just switched to. Clear it and force a re-login so writes are
		// attributed to a valid user in the now-active DB (#631).
		sess := h.session(r)
		delete(sess.Values, "user_id")
		sess.Save(r, w)
		msg := "Test Mode switched the active database to " + h.cfg.ActiveDBName() + ". Please sign in again."
		http.Redirect(w, r, "/login?notice="+url.QueryEscape(msg), http.StatusSeeOther)
		return
	}

	if h.db != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	h.render(w, r, "settings/settings.html", h.settingsData(w, r, map[string]any{
		"Success": "Settings saved. Enter your database password to connect.",
	}))
}

// SettingsPreferencesSave persists the logged-in user's per-user PO defaults
// (Settings → My Preferences tab, issue #463). It has its own endpoint so this
// partial form can't blank the fields the main settings form writes.
func (h *Handler) SettingsPreferencesSave(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	contactID, _ := strconv.Atoi(r.FormValue("po_default_contact_id"))
	receiverID, _ := strconv.Atoi(r.FormValue("po_default_receiver_id"))

	// Store 0 as NULL so an unset default leaves new POs' receiver/contact blank.
	var contactArg, receiverArg any
	if contactID > 0 {
		contactArg = contactID
	}
	if receiverID > 0 {
		receiverArg = receiverID
	}

	if _, err := h.execContext(r.Context(), fmt.Sprintf(
		`UPDATE %s SET default_po_contact_id = @p1, default_po_receiver_id = @p2 WHERE id = @p3`,
		h.cfg.UsersTable()), contactArg, receiverArg, u.ID); err != nil {
		h.render(w, r, "settings/settings.html", h.settingsData(w, r, map[string]any{
			"Error": "Could not save preferences: " + err.Error(),
		}))
		return
	}

	// User rows are cached (auth.go); drop the stale entry so the redirect below
	// re-reads the new defaults (currentUser is resolved once per request).
	h.invalidateUserCache(u.ID)
	http.Redirect(w, r, "/settings#preferences", http.StatusSeeOther)
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
