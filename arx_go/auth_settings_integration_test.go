//go:build integration

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Characterization tests for the auth + settings handlers (#251, #247, #224): gaps the older
// integration tests left, written against the pre-sqlc code. Fixture users/companies/contacts are
// committed rows removed with t.Cleanup; seeded users 8001/8002 are only read.

// asUser puts u on req's context, the shape RequireAuth injects.
func asUser(req *http.Request, u *User) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), ctxUserKey, u))
}

// authUser inserts a user row (committed) and returns its id. defaultRoute/accent may be nil.
func authUser(t *testing.T, h *Handler, username string, active bool, defaultRoute, accent *string) int {
	t.Helper()
	return repInsert(t, h, `INSERT INTO users (username, display_name, password_hash, is_active, default_route, accent_color)
		VALUES ($1, 'Auth Fixture', $2, $3, $4, $5) RETURNING id`,
		[]any{username, "hash-" + username, active, defaultRoute, accent},
		`DELETE FROM users WHERE id=$1`)
}

func userField[T any](t *testing.T, h *Handler, col string, id int) T {
	t.Helper()
	var v T
	if err := h.queryRowContext(context.Background(), fmt.Sprintf(`SELECT %s FROM users WHERE id=$1`, col), id).Scan(&v); err != nil {
		t.Fatalf("read users.%s: %v", col, err)
	}
	return v
}

func TestIntegration_AuthUserByID_FieldsAndInactive(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	ctx := context.Background()

	u, err := h.userByID(ctx, 8001)
	if err != nil || u == nil {
		t.Fatalf("userByID(8001) = %v, %v", u, err)
	}
	if u.Username != "admin" || u.DisplayName != "Admin User" || !u.CanApprovePO || !u.CanApproveRecords || !u.IsAdmin ||
		u.AccentColor == "" || u.Timezone != "America/Los_Angeles" || u.DefaultPOContactID != 2005 || u.DefaultPOReceiverID != 1003 {
		t.Errorf("userByID(8001) = %+v (ArxDev may need reseeding)", *u)
	}

	un := smokeUniq("auth-null")
	nullID := authUser(t, h, un, true, nil, nil)
	nu, err := h.userByID(ctx, nullID)
	if err != nil || nu == nil {
		t.Fatalf("userByID(null fixture) = %v, %v", nu, err)
	}
	if nu.AccentColor != "" || nu.DefaultRoute != "" || nu.DefaultPOContactID != 0 || nu.DefaultPOReceiverID != 0 ||
		nu.Timezone != "America/Los_Angeles" || nu.CanApprovePO || nu.CanApproveRecords || nu.IsAdmin {
		t.Errorf("NULL-column user = %+v, want zero prefs and DDL-default timezone", *nu)
	}

	inactiveID := authUser(t, h, smokeUniq("auth-off"), false, nil, nil)
	if got, err := h.userByID(ctx, inactiveID); got != nil || err != nil {
		t.Errorf("userByID(inactive) = %v, %v, want nil, nil", got, err)
	}
	if got, err := h.userByID(ctx, 987654321); got != nil || err != nil {
		t.Errorf("userByID(missing) = %v, %v, want nil, nil", got, err)
	}
}

func TestIntegration_AuthUserByUsername(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	ctx := context.Background()

	route := "/pos"
	name := smokeUniq("auth-un")
	id := authUser(t, h, name, true, &route, nil)
	u, hash, err := h.userByUsername(ctx, name)
	if err != nil || u == nil {
		t.Fatalf("userByUsername = %v, %v", u, err)
	}
	if u.ID != id || u.Username != name || u.DisplayName != "Auth Fixture" || u.DefaultRoute != "/pos" || hash != "hash-"+name {
		t.Errorf("userByUsername = %+v hash %q", *u, hash)
	}

	nullName := smokeUniq("auth-unnull")
	authUser(t, h, nullName, true, nil, nil)
	if u, _, err := h.userByUsername(ctx, nullName); err != nil || u == nil || u.DefaultRoute != "" {
		t.Errorf("NULL default_route: %v, %v, want DefaultRoute \"\"", u, err)
	}

	offName := smokeUniq("auth-unoff")
	authUser(t, h, offName, false, nil, nil)
	if u, hash, err := h.userByUsername(ctx, offName); u != nil || hash != "" || err != nil {
		t.Errorf("inactive userByUsername = %v, %q, %v, want nil", u, hash, err)
	}
	// Username match is exact (case-sensitive), which the login throttle key does not mirror.
	if u, _, err := h.userByUsername(ctx, strings.ToUpper(name)); u != nil || err != nil {
		t.Errorf("uppercased username matched: %v, %v", u, err)
	}
}

func TestIntegration_AuthLoginPost_InactiveUserRefused(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	name := smokeUniq("auth-login-off")
	if err := h.createUser(context.Background(), name, "Off", "pw-12345", false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { smokeExec(context.Background(), h, `DELETE FROM users WHERE username=$1`, name) })
	repExec(t, h, `UPDATE users SET is_active=FALSE WHERE username=$1`, name)

	rec := httptest.NewRecorder()
	h.LoginPost(rec, postForm("/login", url.Values{"username": {name}, "password": {"pw-12345"}}))
	assertStatus(t, "LoginPost(inactive)", rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/login?error=invalid+username+or+password" {
		t.Errorf("Location = %q", loc)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Error("session cookie set for an inactive user")
	}
}

func TestIntegration_AuthLoginPost_LandsOnDefaultRoute(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	name := smokeUniq("auth-login-route")
	if err := h.createUser(context.Background(), name, "Route", "pw-12345", false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { smokeExec(context.Background(), h, `DELETE FROM users WHERE username=$1`, name) })
	repExec(t, h, `UPDATE users SET default_route='/reports' WHERE username=$1`, name)

	rec := httptest.NewRecorder()
	h.LoginPost(rec, postForm("/login", url.Values{"username": {name}, "password": {"pw-12345"}}))
	if loc := rec.Header().Get("Location"); loc != "/reports" {
		t.Errorf("Location = %q, want /reports", loc)
	}
}

func TestIntegration_AuthUserCountAndList(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	ctx := context.Background()

	before, err := h.userCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prefix := smokeUniq("auth-list")
	aID := authUser(t, h, prefix+"-b", true, nil, nil)
	offID := authUser(t, h, prefix+"-a", false, nil, nil)
	after, err := h.userCount(ctx)
	if err != nil || after != before+1 {
		t.Errorf("userCount %d -> %d (err %v), want +1: only the active fixture counts", before, after, err)
	}

	list, err := h.listUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var mine []map[string]any
	for _, r := range list {
		if strings.HasPrefix(r["Username"].(string), prefix) {
			mine = append(mine, r)
		}
	}
	if len(mine) != 2 || mine[0]["ID"] != offID || mine[1]["ID"] != aID {
		t.Fatalf("fixture rows = %+v, want [%d(-a inactive) %d(-b)] ordered by username", mine, offID, aID)
	}
	want := map[string]any{"ID": offID, "Username": prefix + "-a", "DisplayName": "Auth Fixture",
		"IsActive": false, "CanApprovePO": false, "CanApproveRecords": false, "IsAdmin": false}
	if !reflect.DeepEqual(mine[0], want) {
		t.Errorf("inactive row = %#v, want %#v", mine[0], want)
	}
}

func TestIntegration_AuthCreateUser_HashAndDuplicate(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	ctx := context.Background()
	name := smokeUniq("auth-create")
	t.Cleanup(func() { smokeExec(ctx, h, `DELETE FROM users WHERE username=$1`, name) })

	if err := h.createUser(ctx, name, "Created", "s3cret-pw", true); err != nil {
		t.Fatal(err)
	}
	u, hash, err := h.userByUsername(ctx, name)
	if err != nil || u == nil {
		t.Fatalf("lookup: %v, %v", u, err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("s3cret-pw")) != nil {
		t.Error("stored hash does not verify against the password")
	}
	if strings.Contains(hash, "s3cret-pw") {
		t.Error("stored hash contains the plaintext password")
	}
	if !userField[bool](t, h, "is_admin", u.ID) {
		t.Error("is_admin false, want true (admin arg)")
	}
	if userField[bool](t, h, "can_approve_po", u.ID) || userField[bool](t, h, "can_approve_records", u.ID) {
		t.Error("approver flags set on a new user")
	}
	if err := h.createUser(ctx, name, "Dup", "other", false); err == nil {
		t.Error("duplicate username accepted, want unique-constraint error")
	}
}

func TestIntegration_AuthUserUpdates_BumpUpdatedAtAndTargetOneRow(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	a := authUser(t, h, smokeUniq("auth-upd-a"), true, nil, nil)
	b := authUser(t, h, smokeUniq("auth-upd-b"), true, nil, nil)
	reset := func() {
		repExec(t, h, `UPDATE users SET updated_at=$1 WHERE id IN ($2,$3)`, old, a, b)
	}
	stamp := func(id int) time.Time { return userField[time.Time](t, h, "updated_at", id) }

	// Each user-admin handler bumps updated_at on the target only. Each needs an admin caller (8001).
	type call struct {
		name string
		do   func(id int) *httptest.ResponseRecorder
	}
	mk := func(fn func(http.ResponseWriter, *http.Request), path string, vals url.Values) func(int) *httptest.ResponseRecorder {
		return func(id int) *httptest.ResponseRecorder {
			rec := httptest.NewRecorder()
			fn(rec, withUserID(adminCtx(postForm(fmt.Sprintf("/settings/users/%d/%s", id, path), vals)), id))
			return rec
		}
	}
	for _, c := range []call{
		{"reset", mk(h.SettingsUsersResetPassword, "password", url.Values{"password": {"new-pw"}})},
		{"toggle-active", mk(h.SettingsUsersToggleActive, "toggle-active", url.Values{})},
		{"toggle-approve", mk(h.SettingsUsersToggleApprove, "toggle-approve", url.Values{})},
		{"toggle-approve-records", mk(h.SettingsUsersToggleApproveRecords, "toggle-approve-records", url.Values{})},
		{"toggle-admin", mk(h.SettingsUsersToggleAdmin, "toggle-admin", url.Values{})},
	} {
		reset()
		rec := c.do(a)
		assertStatus(t, c.name, rec, http.StatusSeeOther)
		if loc := rec.Header().Get("Location"); loc != "/settings?tab=users" {
			t.Errorf("%s: Location = %q", c.name, loc)
		}
		if !stamp(a).After(old) {
			t.Errorf("%s: updated_at not bumped on the target", c.name)
		}
		if !stamp(b).Equal(old) {
			t.Errorf("%s: updated_at changed on an untouched user", c.name)
		}
	}

	// Reset stores a verifying hash.
	reset()
	c := call{"reset", mk(h.SettingsUsersResetPassword, "password", url.Values{"password": {"final-pw"}})}
	c.do(a)
	if bcrypt.CompareHashAndPassword([]byte(userField[string](t, h, "password_hash", a)), []byte("final-pw")) != nil {
		t.Error("reset password hash does not verify")
	}
	if userField[string](t, h, "password_hash", b) != "hash-"+userField[string](t, h, "username", b) {
		t.Error("reset changed another user's hash")
	}
}

func TestIntegration_AuthToggle_MissingUserIsSilentSuccess(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	for name, fn := range map[string]func(http.ResponseWriter, *http.Request){
		"active": h.SettingsUsersToggleActive, "approve": h.SettingsUsersToggleApprove,
		"approve-records": h.SettingsUsersToggleApproveRecords, "admin": h.SettingsUsersToggleAdmin,
	} {
		rec := httptest.NewRecorder()
		fn(rec, withUserID(adminCtx(postForm("/settings/users/987654321/x", url.Values{})), 987654321))
		assertStatus(t, name, rec, http.StatusSeeOther)
		if loc := rec.Header().Get("Location"); loc != "/settings?tab=users" {
			t.Errorf("toggle-%s missing user: Location = %q, want success redirect (0 rows updated is not an error)", name, loc)
		}
	}
	rec := httptest.NewRecorder()
	h.SettingsUsersResetPassword(rec, withUserID(adminCtx(postForm("/settings/users/987654321/password", url.Values{"password": {"x"}})), 987654321))
	if loc := rec.Header().Get("Location"); loc != "/settings?tab=users" {
		t.Errorf("reset missing user: Location = %q", loc)
	}
}

func TestIntegration_SettingsPrefs_SaveAndInvalid(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	id := authUser(t, h, smokeUniq("auth-prefs"), true, nil, nil)
	repExec(t, h, `UPDATE users SET updated_at=$1 WHERE id=$2`, old, id)
	me := &User{ID: id}
	// valid: the write happened, so the user cache entry must be dropped; invalid input returns early.
	call := func(fn func(http.ResponseWriter, *http.Request), valid bool, vals url.Values) *httptest.ResponseRecorder {
		h.userCache[id] = &userCacheEntry{user: me, expires: time.Now().Add(time.Minute)}
		rec := httptest.NewRecorder()
		fn(rec, asUser(postForm("/settings/x", vals), me))
		if _, ok := h.userCache[id]; ok == valid {
			t.Errorf("user cache entry kept=%v for valid=%v input", ok, valid)
		}
		return rec
	}

	call(h.SettingsAccentColorSave, true, url.Values{"accent_color": {"teal"}})
	if got := userField[sql.NullString](t, h, "accent_color", id); got.String != "teal" {
		t.Errorf("accent_color = %v, want teal", got)
	}
	call(h.SettingsAccentColorSave, false, url.Values{"accent_color": {"not-a-theme"}})
	if got := userField[sql.NullString](t, h, "accent_color", id); got.String != "teal" {
		t.Errorf("invalid accent changed the value to %v", got)
	}

	call(h.SettingsTimezoneSave, true, url.Values{"timezone": {"America/New_York"}})
	if got := userField[string](t, h, "timezone", id); got != "America/New_York" {
		t.Errorf("timezone = %q", got)
	}
	call(h.SettingsTimezoneSave, false, url.Values{"timezone": {"Mars/Olympus"}})
	if got := userField[string](t, h, "timezone", id); got != "America/New_York" {
		t.Errorf("invalid timezone changed the value to %q", got)
	}

	call(h.SettingsDefaultRouteSave, true, url.Values{"landing_choice": {"/pos"}})
	if got := userField[sql.NullString](t, h, "default_route", id); got.String != "/pos" {
		t.Errorf("default_route = %v, want /pos", got)
	}
	call(h.SettingsDefaultRouteSave, true, url.Values{"landing_choice": {"custom"}, "custom_route": {"http://evil.example/parts?f=1"}})
	if got := userField[sql.NullString](t, h, "default_route", id); got.String != "/parts?f=1" {
		t.Errorf("custom default_route = %v, want /parts?f=1 (host dropped)", got)
	}
	call(h.SettingsDefaultRouteSave, false, url.Values{"landing_choice": {"bogus"}})
	if got := userField[sql.NullString](t, h, "default_route", id); got.String != "/parts?f=1" {
		t.Errorf("invalid landing choice changed the value to %v", got)
	}

	// None of the preference saves touch updated_at.
	if got := userField[time.Time](t, h, "updated_at", id); !got.Equal(old) {
		t.Errorf("preference saves bumped updated_at to %v", got)
	}

	// PO defaults: ids stored, 0 stored as NULL.
	co, _ := repCompany(t, h)
	ct := repInsert(t, h, `INSERT INTO contact (display_name, company_id) VALUES ('Auth Contact', $1) RETURNING id`, []any{co},
		`DELETE FROM contact WHERE id=$1`)
	// The users row references these ids logically only, so clear them before the company/contact go.
	t.Cleanup(func() {
		smokeExec(context.Background(), h, `UPDATE users SET default_po_contact_id=NULL, default_po_receiver_id=NULL WHERE id=$1`, id)
	})
	rec := call(h.SettingsPreferencesSave, true, url.Values{"po_default_contact_id": {fmt.Sprint(ct)}, "po_default_receiver_id": {fmt.Sprint(co)}})
	assertStatus(t, "SettingsPreferencesSave", rec, http.StatusSeeOther)
	if got := userField[sql.NullInt64](t, h, "default_po_contact_id", id); got.Int64 != int64(ct) {
		t.Errorf("default_po_contact_id = %v, want %d", got, ct)
	}
	if got := userField[sql.NullInt64](t, h, "default_po_receiver_id", id); got.Int64 != int64(co) {
		t.Errorf("default_po_receiver_id = %v, want %d", got, co)
	}
	call(h.SettingsPreferencesSave, true, url.Values{"po_default_contact_id": {"0"}, "po_default_receiver_id": {"abc"}})
	if got := userField[sql.NullInt64](t, h, "default_po_contact_id", id); got.Valid {
		t.Errorf("contact 0 stored as %v, want NULL", got)
	}
	if got := userField[sql.NullInt64](t, h, "default_po_receiver_id", id); got.Valid {
		t.Errorf("receiver non-numeric stored as %v, want NULL", got)
	}
	if got := userField[time.Time](t, h, "updated_at", id); !got.Equal(old) {
		t.Errorf("PO-default save bumped updated_at to %v", got)
	}
}

func TestIntegration_SettingsPrefs_AnonymousRedirectsToLogin(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	for name, fn := range map[string]func(http.ResponseWriter, *http.Request){
		"accent": h.SettingsAccentColorSave, "tz": h.SettingsTimezoneSave,
		"route": h.SettingsDefaultRouteSave, "prefs": h.SettingsPreferencesSave,
	} {
		rec := httptest.NewRecorder()
		fn(rec, postForm("/settings/x", url.Values{"accent_color": {"teal"}}))
		assertStatus(t, name, rec, http.StatusSeeOther)
		if loc := rec.Header().Get("Location"); loc != "/login" {
			t.Errorf("%s anonymous: Location = %q, want /login", name, loc)
		}
	}
}

func TestIntegration_SettingsOptions_ContactsAndSuppliers(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)

	co, coName := repCompany(t, h)
	off := repInsert(t, h, `INSERT INTO company (name, supplier_code, is_active) VALUES ($1,'OFF',FALSE) RETURNING id`,
		[]any{smokeUniq("RPT-OFF")}, `DELETE FROM company WHERE id=$1`)
	other, _ := repCompany(t, h)
	ins := func(name string, company int, active any) int {
		return repInsert(t, h, `INSERT INTO contact (display_name, company_id, is_active) VALUES ($1,$2,$3) RETURNING id`,
			[]any{name, company, active}, `DELETE FROM contact WHERE id=$1`)
	}
	zed := ins("ZZ-auth-zed", co, true)
	amy := ins("ZZ-auth-amy", co, true)
	ins("ZZ-auth-off", co, false)
	ins("ZZ-auth-null", co, nil)
	stray := ins("ZZ-auth-other", other, true)

	scoped := h.fetchContactOptions(req, co)
	if want := []contactOption{{ID: amy, Name: "ZZ-auth-amy"}, {ID: zed, Name: "ZZ-auth-zed"}}; !reflect.DeepEqual(scoped, want) {
		t.Errorf("fetchContactOptions(company) = %+v, want %+v (active only, ordered by name)", scoped, want)
	}
	all := h.fetchContactOptions(req, 0)
	var ids []int
	for _, c := range all {
		if strings.HasPrefix(c.Name, "ZZ-auth-") {
			ids = append(ids, c.ID)
		}
	}
	if want := []int{amy, stray, zed}; !reflect.DeepEqual(ids, want) {
		t.Errorf("fetchContactOptions(0) fixture ids = %v, want %v", ids, want)
	}

	sups := h.fetchSupplierOptions(req)
	var gotCo, gotOff bool
	for _, s := range sups {
		if s.ID == co {
			gotCo = s.Name == coName
		}
		if s.ID == off {
			gotOff = true
		}
	}
	if !gotCo || gotOff {
		t.Errorf("supplier options: active fixture present=%v, inactive present=%v", gotCo, gotOff)
	}
}

func TestIntegration_SettingsAttachmentCategories_OrderAndClear(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	withRestoredAttachmentCategories(t, h)
	ctx := context.Background()

	if err := h.saveAttachmentCategories(ctx, []string{"b", "a", "c"}); err != nil {
		t.Fatal(err)
	}
	if got := h.loadAttachmentCategories(ctx); !reflect.DeepEqual(got, []string{"b", "a", "c"}) {
		t.Errorf("loaded = %v, want insertion order [b a c]", got)
	}
	// Same sort_order can't collide here, but ties fall back to display_name.
	repExec(t, h, `UPDATE attachment_category SET sort_order=0`)
	if got := h.loadAttachmentCategories(ctx); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("loaded with tied sort_order = %v, want name order [a b c]", got)
	}
	if err := h.saveAttachmentCategories(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if got := h.loadAttachmentCategories(ctx); len(got) != 0 {
		t.Errorf("after saving an empty list: %v, want none", got)
	}
}

// backupNames is the set of CSVs the backup writes, in order: the table list, then users, then app_config.
var backupNames = []string{
	"part", "bom", "company", "contact", "purchase_order", "po_line", "part_attachment", "price",
	"mfg_part", "supplier_part", "company_attachment", "uom",
	"form", "form_record", "result", "form_row", "form_events", "record_events",
	"named_queries", "form_row_history", "inventory_transaction", "build", "lot", "unit",
	"genealogy", "purchase_order_history", "record_event_results", "part_category", "attachment_category",
	"users", "app_config",
}

func TestIntegration_SettingsBackup_FileSetAndSecretRows(t *testing.T) {
	h, done := liveHandler(t)
	t.Cleanup(done)
	ctx := context.Background()
	secret := smokeUniq("secret_backup_test")
	plain := smokeUniq("plain_backup_test")
	t.Cleanup(func() { smokeExec(ctx, h, `DELETE FROM app_config WHERE setting_key IN ($1,$2)`, secret, plain) })
	if err := h.appConfigSet(ctx, secret, "TOPSECRET"); err != nil {
		t.Fatal(err)
	}
	if err := h.appConfigSet(ctx, plain, "visible"); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.SettingsBackup(rec, httptest.NewRequest(http.MethodGet, "/settings/backup", nil))
	assertStatus(t, "SettingsBackup", rec, http.StatusOK)
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	files := map[string]string{}
	for _, f := range zr.File {
		names = append(names, strings.TrimSuffix(f.Name, ".csv"))
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		files[strings.TrimSuffix(f.Name, ".csv")] = string(b)
	}
	if !reflect.DeepEqual(names, backupNames) {
		t.Errorf("backup files = %v\nwant %v", names, backupNames)
	}
	if !strings.Contains(files["app_config"], plain) {
		t.Error("app_config.csv lacks a normal row")
	}
	if strings.Contains(files["app_config"], secret) || strings.Contains(files["app_config"], "TOPSECRET") {
		t.Error("app_config.csv carries a secret_ row")
	}
	if !strings.Contains(files["users"], "admin") || strings.Contains(strings.ToLower(files["users"]), "password_hash") {
		t.Error("users.csv should list users but never password_hash")
	}
	if !strings.Contains(files["part"], "ASM-1001") {
		t.Error("part.csv lacks the seeded ASM-1001 row")
	}
}
