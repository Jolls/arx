package main

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	arxbase "arx/internal/config"
)

// failingDriver's Open always fails with an error shaped like a real driver
// error, carrying the server, login and database name.
type failingDriver struct{}

func (failingDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("unable to open tcp connection with host 'sql-prod.internal:1433': login error for user 'arxsvc' on database 'ArxSecret'")
}

func init() {
	sql.Register("failing-driver", failingDriver{})
}

// TestLoginPost_DBErrorDoesNotLeakConnectionDetails covers #110: a driver
// error on the pre-auth path must reach the log but not the response body.
func TestLoginPost_DBErrorDoesNotLeakConnectionDetails(t *testing.T) {
	db, err := sql.Open("failing-driver", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &arxbase.Config{}
	cfg.SessionSecret = "test-secret"
	h := New(db, cfg, nil, nil)

	var logBuf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(prev)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		logBuf.Reset()
		req := httptest.NewRequest(method, "/login", strings.NewReader("username=alice&password=x"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		if method == http.MethodGet {
			h.LoginGet(rec, req)
		} else {
			h.LoginPost(rec, req)
		}

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%s: status = %d, want 500", method, rec.Code)
		}
		for _, secret := range []string{"sql-prod", "arxsvc", "ArxSecret"} {
			if strings.Contains(rec.Body.String(), secret) {
				t.Errorf("%s: body leaks %q: %q", method, secret, rec.Body.String())
			}
		}
		if !strings.Contains(logBuf.String(), "sql-prod") {
			t.Errorf("%s: log missing driver error detail: %q", method, logBuf.String())
		}
	}
}

// TestCachedUserByID_ReturnsFreshEntryWithoutDB seeds the cache directly (no DB
// call reachable) and confirms cachedUserByID returns the cached user instead
// of calling userByID.
func TestCachedUserByID_ReturnsFreshEntryWithoutDB(t *testing.T) {
	h := testHandler() // db == nil; userByID would panic/fail if called
	want := &User{ID: 1, Username: "alice"}
	h.userCache[1] = &userCacheEntry{user: want, expires: time.Now().Add(time.Minute)}

	got, err := h.cachedUserByID(context.Background(), 1, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want the cached pointer %+v", got, want)
	}
}

// expiredUserHandler returns a handler whose cache holds an expired entry for
// user 1 (Username "old") and whose lookupUser counts calls and returns next.
func expiredUserHandler(next *User, err error) (*Handler, *atomic.Int32) {
	h := testHandler()
	calls := &atomic.Int32{}
	h.lookupUser = func(context.Context, int) (*User, error) {
		calls.Add(1)
		return next, err
	}
	h.userCache[1] = &userCacheEntry{user: &User{ID: 1, Username: "old"}, expires: time.Now().Add(-time.Second)}
	return h, calls
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal("condition not met within 2s")
}

func cachedUsername(h *Handler, id int) string {
	h.userMu.RLock()
	defer h.userMu.RUnlock()
	if e, ok := h.userCache[id]; ok {
		return e.user.Username
	}
	return ""
}

func TestCachedUserByID_StaleGETReturnsImmediatelyAndRefreshes(t *testing.T) {
	h, calls := expiredUserHandler(&User{ID: 1, Username: "new"}, nil)

	got, err := h.cachedUserByID(context.Background(), 1, true)
	if err != nil || got.Username != "old" {
		t.Fatalf("got %+v, %v; want the stale user", got, err)
	}
	waitFor(t, func() bool { return cachedUsername(h, 1) == "new" })
	if n := calls.Load(); n != 1 {
		t.Errorf("lookups = %d, want 1", n)
	}
}

func TestCachedUserByID_ExpiredMutatingRequestLooksUpSynchronously(t *testing.T) {
	h, calls := expiredUserHandler(&User{ID: 1, Username: "new"}, nil)

	got, err := h.cachedUserByID(context.Background(), 1, false)
	if err != nil || got.Username != "new" {
		t.Fatalf("got %+v, %v; want the fresh user", got, err)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("lookups = %d, want 1", n)
	}
}

func TestCachedUserByID_NoEntryLooksUpSynchronouslyEvenForGET(t *testing.T) {
	h, _ := expiredUserHandler(&User{ID: 2, Username: "fresh"}, nil)

	got, err := h.cachedUserByID(context.Background(), 2, true)
	if err != nil || got.Username != "fresh" {
		t.Fatalf("got %+v, %v; want the fresh user", got, err)
	}
}

func TestCachedUserByID_RefreshOfInactiveUserEvictsEntry(t *testing.T) {
	h, _ := expiredUserHandler(nil, nil)

	if _, err := h.cachedUserByID(context.Background(), 1, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return cachedUsername(h, 1) == "" })
}

func TestCachedUserByID_RefreshErrorKeepsStaleEntryAndRetries(t *testing.T) {
	h, calls := expiredUserHandler(nil, errors.New("db down"))

	h.cachedUserByID(context.Background(), 1, true)
	waitFor(t, func() bool { return calls.Load() == 1 })
	waitFor(t, func() bool {
		h.userMu.RLock()
		defer h.userMu.RUnlock()
		return !h.userCache[1].refreshing
	})
	if got := cachedUsername(h, 1); got != "old" {
		t.Errorf("cached user = %q, want the stale entry kept", got)
	}

	h.cachedUserByID(context.Background(), 1, true)
	waitFor(t, func() bool { return calls.Load() == 2 })
}

func TestCachedUserByID_ConcurrentStaleGETsTriggerOneRefresh(t *testing.T) {
	h, calls := expiredUserHandler(&User{ID: 1, Username: "new"}, nil)
	release := make(chan struct{})
	h.lookupUser = func(context.Context, int) (*User, error) {
		calls.Add(1)
		<-release
		return &User{ID: 1, Username: "new"}, nil
	}

	for range 20 {
		if _, err := h.cachedUserByID(context.Background(), 1, true); err != nil {
			t.Fatal(err)
		}
	}
	close(release)
	waitFor(t, func() bool { return cachedUsername(h, 1) == "new" })
	if n := calls.Load(); n != 1 {
		t.Errorf("lookups = %d, want 1", n)
	}
}

func TestCachedUserByID_RefreshResultDroppedAfterInvalidation(t *testing.T) {
	h, calls := expiredUserHandler(nil, nil)
	release := make(chan struct{})
	h.lookupUser = func(context.Context, int) (*User, error) {
		calls.Add(1)
		<-release
		return &User{ID: 1, Username: "pre-change"}, nil
	}

	h.cachedUserByID(context.Background(), 1, true)
	waitFor(t, func() bool { return calls.Load() == 1 })
	h.invalidateUserCache(1)
	close(release)

	time.Sleep(50 * time.Millisecond) // let the refresh goroutine finish
	if got := cachedUsername(h, 1); got != "" {
		t.Errorf("cached user = %q, want none: a pre-invalidation refresh must not re-cache", got)
	}
}

func TestInvalidateUserCache_RemovesEntry(t *testing.T) {
	h := testHandler()
	h.userCache[3] = &userCacheEntry{user: &User{ID: 3}, expires: time.Now().Add(time.Minute)}

	h.invalidateUserCache(3)

	if _, ok := h.userCache[3]; ok {
		t.Error("entry still present after invalidateUserCache")
	}
}

func TestLoginPost_RedirectsToSettingsWhenNoDB(t *testing.T) {
	h := testHandler() // db == nil
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	rec := httptest.NewRecorder()
	h.LoginPost(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if loc := rec.Header().Get("Location"); loc != "/settings" {
		t.Errorf("Location = %q, want /settings", loc)
	}
}

// TestLoginPost_BadFormData confirms r.ParseForm()'s error path is reached
// (and returns 400) before any DB work, using a malformed percent-escape that
// ParseForm reliably rejects (verified experimentally: a malformed multipart
// body does NOT error ParseForm, but a bad %-escape in a urlencoded body does).
func TestLoginPost_BadFormData(t *testing.T) {
	h := testHandlerWithDB()
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=%zz"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.LoginPost(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "bad form data") {
		t.Errorf("body = %q, want it to mention bad form data", rec.Body.String())
	}
}

func TestLogout_ClearsSessionAndRedirects(t *testing.T) {
	h := testHandler()

	// Seed a session cookie carrying user_id.
	seed := httptest.NewRequest(http.MethodPost, "/logout", nil)
	seedRec := httptest.NewRecorder()
	sess := h.session(seed)
	sess.Values["user_id"] = 7
	if err := sess.Save(seed, seedRec); err != nil {
		t.Fatalf("save session: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	for _, c := range seedRec.Result().Cookies() {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.Logout(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}

	// Replay the post-logout cookie and confirm user_id was actually deleted,
	// not just that a redirect happened.
	checkReq := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range rec.Result().Cookies() {
		checkReq.AddCookie(c)
	}
	checkSess := h.session(checkReq)
	if _, ok := checkSess.Values["user_id"]; ok {
		t.Error("user_id still present in session after Logout")
	}
}
