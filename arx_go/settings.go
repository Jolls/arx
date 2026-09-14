package main

import (
	"archive/zip"
	"encoding/base64"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	arxbase "arx/arxlib/config"
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

// selectConnectPassword picks the password SettingsSave connects with: in test
// mode, a freshly-posted test password wins, falling back through the stored
// test password and the prod password/stored-prod-password (mirrors
// Base.activePassword); in prod mode, only the prod posted/stored pair applies.
// allowStored is false for an unauthenticated caller (see SettingsSave): the
// stored secrets are then ignored entirely, so only a freshly-typed password
// can produce a connect attempt.
func selectConnectPassword(testMode, allowStored bool, testPosted, storedTest, posted, storedDB string) string {
	if !allowStored {
		storedTest, storedDB = "", ""
	}
	if testMode {
		return firstNonEmpty(testPosted, storedTest, posted, storedDB)
	}
	return firstNonEmpty(posted, storedDB)
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
	q := fmt.Sprintf(`SELECT id, display_name FROM %s WHERE is_active = %s`, h.cfg.ContactTable(), h.dia().BoolLiteral(true))
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
		fmt.Sprintf(`SELECT id, name FROM %s WHERE is_active = %s ORDER BY name`,
			h.cfg.CompanyTable(), h.dia().BoolLiteral(true)))
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
	timezonePref := defaultTimezone
	if h.database() != nil {
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
			if isValidTimezone(u.Timezone) {
				timezonePref = u.Timezone
			}
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

	connected := h.database() != nil
	data := map[string]any{
		"Connected": connected,
		// PasswordOptional: true when a blank db_password field will reuse the
		// stored password (an authenticated caller, or a working connection
		// with no dbConnError). An unauthenticated caller during a #852
		// connection-error bypass must type the password (see SettingsSave).
		"PasswordOptional":      connected && (h.dbConnError == "" || h.currentUser(r) != nil),
		"DBConnError":           h.dbConnError,
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
		"DigiKeyClientID":       h.cfg.DigiKeyClientID,
		"DigiKeyClientSecretSet": h.cfg.DigiKeyClientSecret != "",
		"CompanyLogo":           h.companyLogoURL(),
		"AccentColor":           accentColor,
		"AccentThemes":          accentThemes,
		"LandingRoute":          landingRoutePref,
		"LandingRouteIsCustom":  !isPresetLanding(landingRoutePref),
		"LandingPresets":        landingPresets,
		"Timezone":              timezonePref,
		"Timezones":             commonTimezones,
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
	maps.Copy(data, extra)
	return data
}

func (h *Handler) WhatsNew(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "settings/whats_new.html", map[string]any{
		"ReleaseNotes": h.releaseNotes,
		"ActiveTab":    "settings",
	})
}

func (h *Handler) Settings(w http.ResponseWriter, r *http.Request) {
	if h.database() != nil {
		r, _ = h.withUser(w, r)
	}
	h.render(w, r, "settings/settings.html", h.settingsData(w, r, nil))
}

// SettingsAttachmentCategoriesSave persists the attachment-category list to
// app_config. It has its own endpoint so this partial form can't blank the
// path fields that SettingsSave writes from the main settings form.
func (h *Handler) SettingsAttachmentCategoriesSave(w http.ResponseWriter, r *http.Request) {
	if h.database() != nil {
		cats := strings.Join(splitCSV(r.FormValue("attachment_categories")), ",")
		if err := h.appConfigSet(r.Context(), "attachment_categories", cats); err != nil {
			log.Printf("warning: could not save attachment_categories: %v", err)
		}
	}
	http.Redirect(w, r, "/settings", http.StatusFound)
}

// SettingsDigiKeySave persists the shop's DigiKey API client ID/secret to the
// per-user secrets store (issue #27). It has its own endpoint so this partial
// form can't blank the fields the main settings form writes. A blank secret
// field keeps the stored secret (matches the DB-password reuse convention);
// a blank client ID clears both, since a secret with no ID is unusable.
func (h *Handler) SettingsDigiKeySave(w http.ResponseWriter, r *http.Request) {
	clientID := strings.TrimSpace(r.FormValue("digikey_client_id"))
	clientSecret := strings.TrimSpace(r.FormValue("digikey_client_secret"))

	secrets, err := arxbase.LoadSecrets()
	if err != nil {
		// A genuine read error (corrupt file, permissions) — not "file doesn't
		// exist", which LoadSecrets already handles by returning an empty,
		// non-nil config. Saving over it would blindly wipe the DB password,
		// test DB password, and session secret already stored there.
		h.render(w, r, "settings/settings.html", h.settingsData(w, r, map[string]any{
			"Error": "Could not save DigiKey credentials: the secrets store could not be read (" + err.Error() + "). Fix that first so other stored secrets aren't lost.",
		}))
		return
	}
	if secrets == nil {
		secrets = &arxbase.SecretsConfig{}
	}
	if clientID == "" {
		secrets.DigiKeyClientID = ""
		secrets.DigiKeyClientSecret = ""
	} else {
		secrets.DigiKeyClientID = clientID
		if clientSecret != "" {
			secrets.DigiKeyClientSecret = clientSecret
		}
	}
	if err := arxbase.SaveSecrets(secrets); err != nil {
		log.Printf("warning: could not save DigiKey credentials: %v", err)
	}
	h.cfg.DigiKeyClientID = secrets.DigiKeyClientID
	h.cfg.DigiKeyClientSecret = secrets.DigiKeyClientSecret
	http.Redirect(w, r, "/settings#configuration", http.StatusFound)
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

// SettingsTimezoneSave persists the logged-in user's timezone preference
// (Settings → My Preferences tab, issue #847). The zone determines which local
// calendar day a UTC audit timestamp falls on in the form-definition history
// view. It has its own endpoint so this partial form can't blank the fields the
// main settings form writes.
func (h *Handler) SettingsTimezoneSave(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	tz := r.FormValue("timezone")
	if !isValidTimezone(tz) {
		http.Redirect(w, r, "/settings#preferences", http.StatusFound)
		return
	}
	if _, err := h.execContext(r.Context(), fmt.Sprintf(
		`UPDATE %s SET timezone = @p1 WHERE id = @p2`,
		h.cfg.UsersTable()), tz, u.ID); err != nil {
		log.Printf("warning: could not save timezone: %v", err)
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
	if h.database() == nil {
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
	if h.database() != nil {
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
	secrets, _ := arxbase.LoadSecrets()
	if secrets == nil {
		secrets = &arxbase.SecretsConfig{}
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
	// Tokenize a leading %USERPROFILE% before persisting so a shared
	// config/local.json stays portable across users (#731); h.cfg keeps the
	// expanded, absolute path the handlers already expect.
	local.DocControlRoot = arxbase.TokenizeUserPath(docRoot)
	local.POFolderRoot = arxbase.TokenizeUserPath(poRoot)
	local.SupplierFilesRoot = arxbase.TokenizeUserPath(supplierFilesRoot)
	local.ImageRoot = arxbase.TokenizeUserPath(imageRoot)
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
	//
	// An unauthenticated caller can only reach this handler because the DB is
	// unusable — no connection at all, or h.dbConnError set (#852) — so
	// RequireAuthOnceConnected let the request through without a login. Such a
	// caller must not be able to point db_server at an arbitrary host and have
	// the app dial it with the *stored* password: that hands the secret to the
	// attacker's endpoint, which is exactly what #748 blocked. So the
	// blank-means-reuse-stored-password convenience is limited to a logged-in
	// admin; anonymous callers must type the password to trigger any connect.
	authenticated := h.currentUser(r) != nil
	connectWith := selectConnectPassword(h.cfg.TestMode, authenticated, testPassword, h.cfg.TestDBPassword, password, h.cfg.DBPassword)

	var connErr string
	dbSwapped := false
	if connectWith != "" {
		dsn := h.cfg.BuildDSN(connectWith)
		newDB, newDialect, err := h.connectDB(h.cfg.DBEngine(), dsn)
		if err != nil {
			connErr = err.Error()
		} else {
			old := h.conn.Load()
			h.conn.Store(&dbConn{db: newDB, dialect: newDialect})
			dbSwapped = true
			if password != "" {
				secrets.DBPassword = password
				h.cfg.DBPassword = password
			}
			if testPassword != "" {
				secrets.TestDBPassword = testPassword
				h.cfg.TestDBPassword = testPassword
			}
			h.CheckSchemaVersion(r.Context())
			h.loadCompanyLogo(r.Context())
			h.loadPartCategories(r.Context())
			if old != nil && old.db != nil {
				old.db.Close()
			}
		}
	}

	if err := arxbase.SaveLocal(local); err != nil {
		log.Printf("warning: could not save local config: %v", err)
	}
	if err := arxbase.SaveSecrets(secrets); err != nil {
		log.Printf("warning: could not save secrets config: %v", err)
	}

	if connErr != "" {
		h.render(w, r, "settings/settings.html", h.settingsData(w, r, map[string]any{
			"Error": "Connection failed: " + connErr,
		}))
		return
	}

	if !authenticated && connectWith == "" {
		// No connect was attempted: the other fields are saved, but an
		// anonymous caller has to supply the password (see above).
		h.render(w, r, "settings/settings.html", h.settingsData(w, r, map[string]any{
			"Error": "Settings saved, but not connected: enter your database password to connect.",
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
		delete(sess.Values, "csrf_token")
		sess.Save(r, w)
		msg := "Test Mode switched the active database to " + h.cfg.ActiveDBName() + ". Please sign in again."
		http.Redirect(w, r, "/login?notice="+url.QueryEscape(msg), http.StatusSeeOther)
		return
	}

	// Reaching here means authenticated == true (the unauthenticated,
	// not-yet-connected case already returned above), which is only possible
	// when h.database() != nil (RequireAuthOnceConnected only stashes a user
	// once the connection is usable) — so this is always a redirect home.
	http.Redirect(w, r, "/", http.StatusSeeOther)
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
		h.cfg.AttachmentsTable(), h.cfg.PriceTable(),
		h.cfg.MfgPartTable(), h.cfg.SupplierPartTable(), h.cfg.CompanyAttachmentsTable(),
		h.cfg.UomTable(), h.cfg.AppConfigTable(),
		h.cfg.FormsTable(), h.cfg.RecordsTable(), h.cfg.ResultsTable(),
		h.cfg.StepsTable(), h.cfg.FormEventsTable(), h.cfg.RecordEventsTable(),
		h.cfg.NamedQueriesTable(), h.cfg.FormRowHistoryTable(),
		h.cfg.InventoryTxnTable(), h.cfg.BuildTable(), h.cfg.LotTable(),
		h.cfg.GenealogyTable(), h.cfg.POHistoryTable(), h.cfg.RecordEventResultsTable(),
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

	// Match exclusions case-insensitively: Postgres folds unquoted mixed-case
	// columns to lowercase, so rows.Columns() casing can differ from the exclude
	// keys (e.g. password_hash) by engine. Normalizing both keeps the exclusion
	// robust regardless of which engine's casing convention is in play.
	excluded := make(map[string]bool, len(excludeCols))
	for _, c := range excludeCols {
		excluded[strings.ToLower(c)] = true
	}

	// Build index map of columns to include.
	include := make([]int, 0, len(cols))
	filteredCols := make([]string, 0, len(cols))
	for i, c := range cols {
		if !excluded[strings.ToLower(c)] {
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
