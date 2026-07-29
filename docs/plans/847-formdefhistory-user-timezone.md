# #847 — FormDefHistory calendar-day bucketing uses UTC, not the user's local day

## Problem

`trg_form_row_history` (`SQL/triggers.sql:129`) stamps `changed_at` with `GETDATE()`. Azure SQL
runs in UTC, so `changed_at` is UTC. Two queries in `arx_go/records.go` bucket that column by
calendar date with `CAST(changed_at AS DATE)` and compare it against a bare `YYYY-MM-DD` string
with no timezone conversion. After ~5pm Pacific, UTC has rolled to tomorrow, so "what changed
today" finds nothing and the definition-history view silently falls back to current values —
a wrong audit trail, not just a test flake.

## Fix summary

Store a per-user IANA `timezone` on `users`, and do all UTC↔local-day math in **Go**, never in
SQL. Both query sites stop using `CAST(... AS DATE)`.

## Chosen approach: (a) — bucket in Go, not SQL

SQL Server's `AT TIME ZONE` needs Windows display names ("Pacific Standard Time"); Postgres
needs IANA ("America/Los_Angeles"). Rather than carry an IANA→Windows mapping table (option b),
all timezone math happens in Go with `time.LoadLocation`, and SQL sees only plain UTC timestamps:

- **`FormDefHistory`**: Go converts the `at=YYYY-MM-DD` param into a UTC half-open range
  `[localMidnight, localMidnight+24h)` and the SQL predicate becomes
  `changed_at >= @p2 AND changed_at < @p3`. The `LEFT JOIN` shape is unchanged — no need to pull
  rows into Go, and a range scan on a timestamp column is strictly better than a non-sargable
  `CAST(col AS DATE) = ...`.
- **`FormDef` timeline dots**: drop the SQL `GROUP BY` entirely, `SELECT changed_at, form_row_id`
  raw, and group in Go by the user's local calendar day. Row volume is one form's edit history —
  tens of rows, not thousands — so client-side grouping is free.

Both are dialect-portable and survive the #625 Postgres migration untouched.

**Critical prerequisite:** `time.LoadLocation` on Windows reads `$GOROOT/lib/time/zoneinfo.zip`,
which does not exist on a machine without Go installed — `Arx.exe` ships to shops that have no Go
toolchain. The binary MUST embed the tz database via a blank import of `time/tzdata` (adds ~450KB).
Without this every `LoadLocation` call fails at runtime on user machines while working fine on a
dev box. See §8.

## Files touched

| File | Reason |
|---|---|
| `SQL/users.sql` | Add `timezone VARCHAR(64) NOT NULL DEFAULT 'America/Los_Angeles'` |
| `SQL/migrations/migrate_847_users_timezone.sql` | New migration (idempotent, ArxDev-pinned) |
| `SQL/seed_test_data.sql` | Add `timezone` to the seeded `users` INSERT |
| `SQL/schema.md` | Document the new column in the `users` table-reference row |
| `arx_go/auth.go` | `User.Timezone` field + `userByID` SELECT/scan |
| `arx_go/handlers.go` | `commonTimezones` list, `isValidTimezone`, `userLocation(r)` helper |
| `arx_go/settings.go` | `SettingsTimezoneSave` handler + `Timezone`/`Timezones` template data |
| `arx_go/main.go` | Route `POST /settings/timezone`; blank import `_ "time/tzdata"` |
| `arx_go/templates/settings/settings.html` | Timezone `<select>` card in My Preferences |
| `arx_go/records.go` | Both calendar-bucketing query sites rewritten |
| `arx_go/integration_test.go` | Make `TestIntegration_FormDefHistory_ReturnsPreChangeSnapshot` tz-deterministic |
| `CHANGELOG.md` | One `### Fixed` entry citing #847 |

**Audited, no change needed:** `purchase_order_history.changed_at` is used in `pos.go`
(578, 1331-1332, 1441, 1672, 1758, 2512-2562) and `reports.go` (296, 380-381, 728-729) only for
`ORDER BY` / `MAX()` / `LEAD()` duration pairing — never bucketed or compared by calendar date, so
UTC-vs-local skew cannot change its results. No other `CAST(... AS DATE)` calendar-bucketing
pattern exists in `arx_go/*.go`.

---

## 1. `SQL/users.sql`

Insert after the `default_route` line, before `created_at`:

```sql
    timezone      VARCHAR(64)   NOT NULL DEFAULT 'America/Los_Angeles',  -- per-user IANA timezone for calendar-day bucketing of UTC audit timestamps (issue #847)
```

Note this is the first per-user preference column that is `NOT NULL` with a `DEFAULT`
(`accent_color`/`default_route` are nullable) — deliberate, so every code path has a usable zone
without a NULL branch.

## 2. `SQL/migrations/migrate_847_users_timezone.sql` (new)

**Verified SQL Server behavior — no explicit backfill needed.** `ALTER TABLE ... ADD col <type>
NOT NULL DEFAULT <constant>` populates every existing row with the default value as part of the
ADD (and since SQL Server 2012 / all Azure SQL it is a metadata-only online operation for a
non-nullable column with a constant default). This is exactly why the statement is legal at all on
a non-empty table — a `NOT NULL` add without a `DEFAULT` is rejected. So there is **no** backfill
`UPDATE`, and therefore **no** `EXEC(N'...')` dynamic-SQL wrapper is required here (unlike
`migrate_750`, whose backfill referenced the just-added column in the same batch). Postgres 11+
behaves identically.

Full file:

```sql
-- migrate_847_users_timezone.sql
-- Bug fix (#847): add `users.timezone`, a per-user IANA timezone identifier
-- (e.g. 'America/Los_Angeles'). `form_row_history.changed_at` is stamped by
-- trg_form_row_history with GETDATE(), which on Azure SQL is UTC. The form-definition
-- history view buckets those timestamps by calendar day; without a per-user zone it
-- used the raw UTC day, so after ~5pm Pacific every edit landed on tomorrow's date and
-- "what changed today" returned nothing. See SQL/users.sql and arx_go/records.go.
--
-- BACKFILL: none needed. SQL Server populates existing rows with the DEFAULT as part of
-- an `ADD <col> NOT NULL DEFAULT <constant>` (that is what makes the statement legal on a
-- non-empty table), so every existing user gets 'America/Los_Angeles' — the shop's current
-- timezone, i.e. today's de facto behavior for everyone. Users change it per-account in
-- Settings -> My Preferences.
--
-- BACKWARD-COMPATIBLE — schema_version is deliberately NOT bumped. timezone is a new NOT
-- NULL column with a DEFAULT, so an INSERT from an old binary (which doesn't set it) still
-- succeeds. An old binary keeps running against the migrated DB, so the mismatch banner
-- must not fire.
--
-- SAFETY: pinned to ArxDev via the USE below. To apply to ArxProd, remove/change that
-- single line — nothing else in the script names a database. This is a script for a
-- human to run, not for an agent (see CLAUDE.md "ArxProd is off-limits").
--
-- Idempotent (guarded; safe to re-run). Runs as a single batch (no `GO`).

USE ArxDev;   -- SAFETY: pinned to ArxDev. Remove/change this line to apply to ArxProd.

IF COL_LENGTH('dbo.users', 'timezone') IS NULL
    ALTER TABLE dbo.users ADD timezone VARCHAR(64) NOT NULL DEFAULT 'America/Los_Angeles';
-- Postgres: ALTER TABLE users ADD COLUMN IF NOT EXISTS timezone VARCHAR(64) NOT NULL DEFAULT 'America/Los_Angeles';
```

## 3. `SQL/seed_test_data.sql`

Current block (lines ~158-162) — add the column and both values:

```sql
    INSERT INTO dbo.users (id, username, display_name, password_hash, is_active, can_approve_po, can_approve_records, is_admin, default_po_contact_id, default_po_receiver_id, accent_color, timezone, updated_at) VALUES
        (8001, 'admin',  'Admin User',  '$2a$10$0mUf8G9KxL6jGVWpUJZnaO40arOpPIqW09xqPyiyqnrBCR/VxffQe', 1, 1, 1, 1, 2005, 1003, 'teal', 'America/Los_Angeles', '2020-01-01T00:00:00'),
        (8002, 'tester', 'Test User',   '$2a$10$B29vUdQg85rwb53HIcltUuhdIb17PrSSVF32tJNJ/TQ1JyHvVYer2', 1, 0, 0, 0, NULL, NULL, NULL, 'America/Los_Angeles', '2020-01-01T00:00:00');
```

Both seeded users get `America/Los_Angeles` (matches the migration backfill; keeps every existing
integration test's date expectations on one zone). Extend the comment block above it (~line 157):

```
    -- Both users are seeded in 'America/Los_Angeles' — the migration's backfill value (#847).
```

## 4. `SQL/schema.md`

In the `users` row of the table-reference section (line 165), append after the `default_route`
sentence, before the trailing `CONTEXT_INFO` sentence:

> `timezone` (NOT NULL, default `America/Los_Angeles`) = per-user IANA timezone identifier chosen
> from a curated dropdown on the Settings → My Preferences tab; used to convert UTC audit
> timestamps (`form_row_history.changed_at`, stamped by `GETDATE()` which is UTC on Azure SQL)
> into the user's local calendar day for the form-definition history timeline and snapshot lookup
> (issue #847). All conversion happens in Go via `time.LoadLocation`, never via SQL Server's
> `AT TIME ZONE` (which requires Windows zone names, not IANA). Run
> `SQL/migrations/migrate_847_users_timezone.sql` to add this column to existing databases; SQL
> Server backfills existing rows with the default, so no separate backfill step is needed.

Leave the ID-range table (line 88) and `ExpectedSchemaVersion` (`arxlib/config/config.go:21`,
currently `"8"`) unchanged — this migration is backward-compatible.

## 5. `arx_go/auth.go`

**User struct** (after the `DefaultRoute` field, line 40):

```go
	// Per-user IANA timezone (issue #847), e.g. "America/Los_Angeles"; used to
	// bucket UTC audit timestamps into the user's local calendar day. NOT NULL in
	// the DB with a default, but "" (or an unknown zone) falls back to
	// defaultTimezone.
	Timezone string
```

**`userByID`** (lines 49-52) — add the column to the SELECT and the scan target. `timezone` is
`NOT NULL`, so scan straight into `u.Timezone`:

```go
	err := h.queryRowContext(ctx, fmt.Sprintf(
		`SELECT id, username, display_name, can_approve_po, can_approve_records, is_admin, default_po_contact_id, default_po_receiver_id, accent_color, default_route, timezone FROM %s WHERE id = @p1 AND is_active = %s`,
		h.cfg.UsersTable(), h.dia().BoolLiteral(true)), id,
	).Scan(&u.ID, &u.Username, &u.DisplayName, &u.CanApprovePO, &u.CanApproveRecords, &u.IsAdmin, &defContact, &defReceiver, &accentColor, &defaultRoute, &u.Timezone)
```

**No other auth.go changes:**
- `userByUsername` (line 106) stays as-is — it selects only what login needs (`default_route` for
  the post-login redirect). The timezone is loaded on the next request by `cachedUserByID`.
- `saveSessionUser` (line 338) persists only `user_id` in the session; the `User` struct is never
  serialized, so nothing to register.
- `createUser` (line 351) does not name `timezone`, so new users take the column DEFAULT. No change.
- `cachedUserByID` picks the new field up automatically; `SettingsTimezoneSave` must call
  `h.invalidateUserCache`.

## 6. `arx_go/handlers.go`

Add immediately after `accentThemeClass` (ends line 281), mirroring the accent-theme closed-list
pattern. Validation is **purely against this curated list** — no `time.LoadLocation`-based
acceptance of arbitrary input, matching how `accent_color` is validated:

```go
// defaultTimezone is the fallback IANA zone when a user is absent or carries an
// empty/unrecognized value (issue #847). Matches the migration's backfill.
const defaultTimezone = "America/Los_Angeles"

// timezoneOption is one preset timezone offered in Settings (#847).
type timezoneOption struct {
	Key   string // stored IANA identifier, e.g. "America/Los_Angeles"
	Label string // shown in the UI
}

// commonTimezones are the timezone choices offered in Settings, in display
// order. Deliberately a short curated list (US shop floors plus UTC), not the
// full IANA database — a closed list keeps validation trivial and the dropdown
// usable. Add entries here as needed.
var commonTimezones = []timezoneOption{
	{"America/Los_Angeles", "Pacific (Los Angeles)"},
	{"America/Denver", "Mountain (Denver)"},
	{"America/Phoenix", "Arizona (Phoenix, no DST)"},
	{"America/Chicago", "Central (Chicago)"},
	{"America/New_York", "Eastern (New York)"},
	{"America/Anchorage", "Alaska (Anchorage)"},
	{"Pacific/Honolulu", "Hawaii (Honolulu)"},
	{"UTC", "UTC"},
}

func isValidTimezone(key string) bool {
	for _, t := range commonTimezones {
		if t.Key == key {
			return true
		}
	}
	return false
}

// userLocation returns the *time.Location for the logged-in user's timezone
// preference (issue #847), falling back to defaultTimezone when logged out, unset,
// or unrecognized — and to time.UTC only if the tz database itself can't be loaded
// (should not happen: main.go blank-imports time/tzdata so the zone data is
// compiled into the binary).
func (h *Handler) userLocation(r *http.Request) *time.Location {
	tz := defaultTimezone
	if u := h.currentUser(r); u != nil && isValidTimezone(u.Timezone) {
		tz = u.Timezone
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		log.Printf("warning: could not load timezone %q: %v", tz, err)
		return time.UTC
	}
	return loc
}
```

Check `handlers.go`'s import block for `time` and `net/http` (both near-certainly present; add
`time` if not).

## 7. `arx_go/settings.go`

**`settingsData`** — declare alongside `accentColor` (line 151):

```go
	timezonePref := defaultTimezone
```

inside the `if u := h.currentUser(r); u != nil` block, next to the accent check (after line 167):

```go
			if isValidTimezone(u.Timezone) {
				timezonePref = u.Timezone
			}
```

and in the `data` map, next to `AccentThemes` (after line 208):

```go
		"Timezone":              timezonePref,
		"Timezones":             commonTimezones,
```

**New handler** — insert after `SettingsAccentColorSave` (ends line 279), before
`SettingsDefaultRouteSave`:

```go
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
```

## 8. `arx_go/main.go`

**Route** — after line 169 (`/settings/default-route`):

```go
		r.Post("/settings/timezone", h.SettingsTimezoneSave)
```

**Embedded tz database** — add to the import block (stdlib group, after `"time"`):

```go
	_ "time/tzdata" // embed the IANA tz database: time.LoadLocation must work on machines with no Go toolchain (#847)
```

This is mandatory, not optional — see the prerequisite note at the top. It also keeps the
Linux/cross-platform target self-contained (no dependency on `/usr/share/zoneinfo`).

## 9. `arx_go/templates/settings/settings.html`

Insert a new form between the `/settings/accent-color` form (closes at the line before
`<form method="post" action="/settings/default-route">`) and the Landing Page form. Follows the
Landing Page `<select>` markup but auto-submits like the accent radios, so no Save button:

```html
    <form method="post" action="/settings/timezone">
        <input type="hidden" name="csrf_token" value="{{.CsrfToken}}">
        <div class="detail-section mt-4">
            <h3>Timezone</h3>
            <p class="text-muted" style="margin:0 0 12px; font-size:0.82em;">
                Your local timezone, for you only. Audit timestamps are recorded in UTC; this
                controls which calendar day they show up on — for example in the form
                definition history timeline.
            </p>
            <div class="d-flex align-items-center gap-2 mb-2 flex-wrap">
                <select name="timezone" class="form-select" style="width:auto;"
                        onchange="this.form.submit()">
                    {{range .Timezones}}
                    <option value="{{.Key}}" {{if eq $.Timezone .Key}}selected{{end}}>{{.Label}}</option>
                    {{end}}
                </select>
            </div>
        </div>
    </form>
```

## 10. `arx_go/records.go`

`time` is already imported (used at lines 480/508). No new imports.

### 10a. `FormDef` timeline dots (current lines 478-518)

**Before:**

```go
	// Load history timestamps for timeline dots.
	type HistoryPoint struct {
		At      time.Time
		Count   int
		PctLeft float64 // position along timeline bar (5–95%)
	}
	hRows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT CAST(changed_at AS DATE) AS day, COUNT(DISTINCT form_row_id) AS cnt
		FROM %s
		WHERE form_row_id IN (SELECT id FROM %s WHERE form_id = @p1)
		GROUP BY CAST(changed_at AS DATE) ORDER BY day ASC`,
		h.cfg.FormRowHistoryTable(), h.cfg.StepsTable()), formID)
	var histPoints []HistoryPoint
	if err == nil {
		defer hRows.Close()
		for hRows.Next() {
			var hp HistoryPoint
			if err := hRows.Scan(&hp.At, &hp.Count); err != nil {
				log.Printf("FormDef: history scan error: %v", err)
				break
			}
			histPoints = append(histPoints, hp)
		}
		if err := hRows.Err(); err != nil {
			log.Printf("FormDef: history rows error: %v", err)
		}
	}
```

**After** (SQL returns raw UTC timestamps; day-bucketing and the DISTINCT-per-day count move to Go
in the viewing user's zone — issue #847):

```go
	// Load history timestamps for timeline dots. changed_at is stamped by
	// trg_form_row_history with GETDATE() = UTC on Azure SQL, so the calendar-day
	// bucketing happens in Go in the viewing user's timezone (#847) — SQL-side
	// AT TIME ZONE would need Windows zone names, not the IANA names we store.
	type HistoryPoint struct {
		At      time.Time
		Count   int
		PctLeft float64 // position along timeline bar (5–95%)
	}
	loc := h.userLocation(r)
	hRows, err := h.queryContext(r.Context(), fmt.Sprintf(`
		SELECT changed_at, form_row_id
		FROM %s
		WHERE form_row_id IN (SELECT id FROM %s WHERE form_id = @p1)
		ORDER BY changed_at ASC`,
		h.cfg.FormRowHistoryTable(), h.cfg.StepsTable()), formID)
	var histPoints []HistoryPoint
	if err == nil {
		defer hRows.Close()
		// day key ("2006-01-02" in loc) → set of distinct form_row_ids changed that day.
		dayRows := map[string]map[int]bool{}
		var dayOrder []string
		for hRows.Next() {
			var changedAt time.Time
			var rowID int
			if err := hRows.Scan(&changedAt, &rowID); err != nil {
				log.Printf("FormDef: history scan error: %v", err)
				break
			}
			key := changedAt.In(loc).Format("2006-01-02")
			if dayRows[key] == nil {
				dayRows[key] = map[int]bool{}
				dayOrder = append(dayOrder, key)
			}
			dayRows[key][rowID] = true
		}
		if err := hRows.Err(); err != nil {
			log.Printf("FormDef: history rows error: %v", err)
		}
		for _, key := range dayOrder {
			day, err := time.ParseInLocation("2006-01-02", key, loc)
			if err != nil {
				continue
			}
			histPoints = append(histPoints, HistoryPoint{At: day, Count: len(dayRows[key])})
		}
	}
```

Notes:
- `ORDER BY changed_at ASC` in SQL means `dayOrder` is already ascending, preserving the previous
  `ORDER BY day ASC` contract that the PctLeft block below (`histPoints[0].At` = earliest) relies
  on. No sort needed.
- `HistoryPoint.At` is now local midnight in `loc` rather than UTC midnight. The template renders
  `.At.Format "2006-01-02"` (`templates/records/form_def.html:80-81`) and emits it as `data-at`,
  which the JS sends straight to `FormDefHistory?at=` — so both ends now agree on the same local
  day. The PctLeft math (lines 505-518) compares `.Unix()` values and needs no change.
- `changed_at` is scanned as `time.Time`; go-mssqldb returns `DATETIME` with a UTC location, so
  `.In(loc)` converts correctly.

### 10b. `FormDefHistory` (current lines 534-583)

**Before** (lines 540-545 and the JOIN predicate at 579, param at 583):

```go
	atStr := r.URL.Query().Get("at")
	at, err := time.Parse("2006-01-02", atStr)
	if err != nil {
		http.Error(w, "bad at param", http.StatusBadRequest)
		return
	}
	...
		    WHERE CAST(changed_at AS DATE) = CAST(@p2 AS DATE)
	...
		formID, at)
```

**After:**

```go
	// `at` is a calendar day in the viewing user's timezone (#847); changed_at is UTC
	// (GETDATE() on Azure SQL), so resolve the day to a half-open UTC range in Go rather
	// than CAST(changed_at AS DATE) — SQL Server's AT TIME ZONE wants Windows zone names,
	// not the IANA names we store.
	atStr := r.URL.Query().Get("at")
	loc := h.userLocation(r)
	dayStart, err := time.ParseInLocation("2006-01-02", atStr, loc)
	if err != nil {
		http.Error(w, "bad at param", http.StatusBadRequest)
		return
	}
	dayEnd := dayStart.AddDate(0, 0, 1)
```

JOIN predicate (line 579):

```go
		    WHERE changed_at >= @p2 AND changed_at < @p3
```

query args (line 583):

```go
		formID, dayStart.UTC(), dayEnd.UTC())
```

Notes:
- `AddDate(0,0,1)` (not `Add(24*time.Hour)`) so a DST-transition day is still exactly one local
  calendar day (23h or 25h of UTC span).
- `.UTC()` on both bounds is explicit rather than relying on driver conversion of a
  location-tagged `time.Time`.
- Half-open `[start, end)` avoids the boundary double-count an inclusive `BETWEEN` would produce.
- Behavior is otherwise identical, including the pre-existing (unchanged) property that multiple
  history rows for one `form_row_id` within the same day yield duplicate joined rows.

## 11. `arx_go/integration_test.go` — regression test

`TestIntegration_FormDefHistory_ReturnsPreChangeSnapshot` (starts line 3777). Two problems today:
it computes `today` from `time.Now()` in the **test process's** local zone, and it calls
`h.FormDefHistory` with no user on the context, so the handler's zone was implicitly the DB's UTC.
Both must be pinned to one explicit zone.

Add a helper next to `adminCtx` (line 2457):

```go
// userCtxTZ returns req with a user carrying an explicit timezone on the context,
// so timezone-sensitive handlers (#847) are deterministic regardless of where the
// test process or the DB server thinks "today" is.
func userCtxTZ(req *http.Request, tz string) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), ctxUserKey,
		&User{ID: 8001, Username: "admin", IsAdmin: true, Timezone: tz}))
}
```

In the test body, replace:

```go
	today := time.Now().Format("2006-01-02")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/forms/%d/def/history?at=%s", formID, today), nil)
	h.FormDefHistory(rec, withID(req, formID))
```

with:

```go
	// The trigger stamps changed_at with GETDATE() = UTC on Azure SQL. Ask for the day
	// that UTC "now" falls on *in the user's zone*, which is what a real user's browser
	// would request, and pin that zone on the request context (#847). Deriving the
	// expected day from time.Now().UTC() rather than the test host's local clock keeps
	// this deterministic no matter where the test runs.
	const testTZ = "America/Los_Angeles"
	loc, err := time.LoadLocation(testTZ)
	if err != nil {
		t.Fatalf("LoadLocation(%s): %v", testTZ, err)
	}
	today := time.Now().UTC().In(loc).Format("2006-01-02")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/forms/%d/def/history?at=%s", formID, today), nil)
	h.FormDefHistory(rec, userCtxTZ(withID(req, formID), testTZ))
```

and the "yesterday" half (currently `yesterday := time.Now().AddDate(0, 0, -1).Format(...)`):

```go
	// No history that day (yesterday, in the same zone) — falls back to the CURRENT form_row value.
	yesterday := time.Now().UTC().In(loc).AddDate(0, 0, -1).Format("2006-01-02")
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/forms/%d/def/history?at=%s", formID, yesterday), nil)
	h.FormDefHistory(rec2, userCtxTZ(withID(req2, formID), testTZ))
```

`err` is already declared earlier in the function (`tx, err := h.beginTx(ctx)`), so use `=` not
`:=` for the `LoadLocation` assignment if the compiler complains about redeclaration in the same
scope — the snippet above uses `loc, err :=`, valid because `loc` is new.

This is the actual regression test: pre-fix, running it between 00:00 and 08:00 UTC (i.e. evening
Pacific) fails with `Changed = false` / `SpecMax = "150"`; post-fix it passes at any hour.

**Also check** `TestIntegration_FormDef_...` timeline coverage around line 3971 (which queries
`FormRowHistoryTable`/`StepsTable` directly) — it asserts against the seeded history row written by
`seed_test_data.sql`'s post-insert UPDATE of step 6103. Verify it does not assert on a specific
day string; if it does, pin it the same way. Read it before editing.

## 12. `CHANGELOG.md`

Add one entry under the next patch version, `### Fixed`:

```
- Form-definition history bucketed audit timestamps by UTC calendar day, so edits made in the
  evening local time were attributed to the next day and the pre-change snapshot silently fell
  back to current values; timestamps are now bucketed in each user's timezone, set on the
  Settings → My Preferences tab ([#847](https://github.com/Jolls/arx-legacy/issues/847))
```

Version number is chosen by the implementing session at commit time per repo convention. If the
new timezone dropdown is considered user-facing enough, also mention it under `### Added` in the
same entry.

## Verification

1. `cd arx_go; go build ./... && go vet ./... && go test ./...`
2. Human runs `SQL/migrations/migrate_847_users_timezone.sql` against ArxDev, then reseeds
   (`SQL/seed_test_data.sql`) so the `users` INSERT with `timezone` applies.
3. `$env:ARX_TEST_DSN=...ArxDev...; go test -tags integration ./arx_go/...` — the target test must
   pass. If practical, re-run it once during 00:00-08:00 UTC (or temporarily set the request's
   `at` to a UTC-tomorrow day) to confirm the original failure mode is gone.
4. Manual: Settings → My Preferences shows the Timezone dropdown, changing it auto-submits and
   persists across a reload; a form-definition page's timeline dot labels and the snapshot it
   loads use the selected zone (switch between Pacific and UTC and confirm a late-evening edit
   moves between two dots).
