# #88 — Oversized upload returns generic 403 instead of "file too large"

## Open questions (none block-blocking, but confirm before implementing)

None. All previously-open questions below were resolved during investigation:

- **`*http.MaxBytesError` shape**: confirmed via local Go 1.27 toolchain source
  (`net/http/request.go:1217`): `type MaxBytesError struct { Limit int64 }`,
  `Error() string` returns the fixed string `"http: request body too large"`
  (comment: "Due to Hyrum's law, this text cannot be changed"). `errors.As`
  against `*http.MaxBytesError` is the correct check.
- **Does the `MaxBytesReader` wrap apply to all POSTs, not just multipart?**
  Yes — `main.go`'s middleware wraps `r.Body` unconditionally for every
  request, regardless of method or content type (`main.go:122-127`, runs
  before `RequireCsrfOnPost`).
- **Does non-multipart form-encoded POST need separate handling?** No.
  `r.FormValue` (used today in `verifyCsrf`) already calls
  `r.ParseMultipartForm(defaultMaxMemory)` internally for *every* POST
  regardless of content type (confirmed in `net/http/request.go:1442-1450`,
  `FormValue`); `ParseMultipartForm` in turn calls `ParseForm` first
  (`request.go:1393-1401`). So today, a single code path already runs for
  both multipart and urlencoded POSTs — the fix only needs to call that same
  path explicitly (instead of implicitly via `FormValue`) so the error isn't
  discarded. `defaultMaxMemory` is unexported in `net/http`; its value is
  `32 << 20` (32 MB) — the plan below hardcodes this literal to reproduce
  identical behavior, not the unrelated `maxUploadBytes` (100 MB) constant
  used elsewhere for a different purpose (see next point).
- **Interaction with `files.go:276`'s `r.ParseMultipartForm(maxUploadBytes)`**:
  unaffected. That call already runs after `RequireCsrfOnPost` and is already
  documented as a no-op today (`main.go:118-121`: "subsequent calls have no
  effect") because `verifyCsrf`'s `r.FormValue` call parses the body first.
  Making the parse explicit in `RequireCsrfOnPost` doesn't change this — it's
  the same call, just no longer silently swallowed.
- **JSON POST body handler (`api.go:249`, `json.NewDecoder(r.Body).Decode`)**:
  no new impact. `FormValue` already fully consumes `r.Body` via
  `ParseMultipartForm`/`ParseForm` before that handler runs, today, at HEAD —
  this is pre-existing behavior, not something this change introduces. Out of
  scope for #88.
- **User-facing message wording**: using the wording suggested directly in
  the issue body: `"Uploaded file is too large (max 100 MB)"`. The 100 MB
  figure matches `maxUploadBytes` in `arx_go/files.go:17`.

## Files to change

### `arx_go/handlers.go`

Current code (lines 621-641, confirmed against current HEAD):

```go
func (h *Handler) verifyCsrf(r *http.Request) bool {
	sess := h.session(r)
	token, _ := sess.Values["csrf_token"].(string)
	got := r.FormValue("csrf_token")
	if token == "" || got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

// RequireCsrfOnPost is middleware that rejects any POST whose csrf_token form
// value does not match the session token.
func (h *Handler) RequireCsrfOnPost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && !h.verifyCsrf(r) {
			http.Error(w, "Invalid form submission", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

Replace with:

```go
func (h *Handler) verifyCsrf(r *http.Request) bool {
	sess := h.session(r)
	token, _ := sess.Values["csrf_token"].(string)
	got := r.FormValue("csrf_token")
	if token == "" || got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

// RequireCsrfOnPost is middleware that rejects any POST whose csrf_token form
// value does not match the session token.
func (h *Handler) RequireCsrfOnPost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			// Parse the body explicitly (instead of leaving it to verifyCsrf's
			// r.FormValue call) so a body that exceeds the main.go
			// MaxBytesReader cap surfaces as *http.MaxBytesError instead of
			// being silently swallowed and misreported as a bad CSRF token
			// (#88). 32<<20 matches net/http's own unexported defaultMaxMemory,
			// which is what FormValue passes to ParseMultipartForm internally
			// — this preserves today's in-memory/on-disk multipart threshold.
			if err := r.ParseMultipartForm(32 << 20); err != nil {
				var maxErr *http.MaxBytesError
				if errors.As(err, &maxErr) {
					http.Error(w, "Uploaded file is too large (max 100 MB)", http.StatusRequestEntityTooLarge)
					return
				}
			}
			if !h.verifyCsrf(r) {
				http.Error(w, "Invalid form submission", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
```

`verifyCsrf` itself is unchanged — its `r.FormValue` call becomes a no-op
re-read of the already-parsed form (per the existing `main.go` comment),
exactly as it already is for the `files.go:276` case.

### Import to add

`arx_go/handlers.go` does not currently import `"errors"` (confirmed against
the current import block, lines 3-30). Add it alphabetically among the
standard-library imports:

```go
import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	ioFS "io/fs"
	"log"
	"net/http"
	"net/url"
	"path"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/sessions"

	"arx/arx_go/models"
	arxbase "arx/arxlib/config"
	arxdb "arx/arxlib/db"
	"arx/arxlib/urlutil"
)
```

(Only the single `"errors"` line is new; everything else in the block is
unchanged — shown in full only so the alphabetical insertion point is
unambiguous.)

## Rationale

- The 403 vs. 413 confusion happens because `verifyCsrf` calls
  `r.FormValue`, which discards the `ParseMultipartForm`/`ParseForm` error
  and returns `""`, indistinguishable from a genuinely missing token.
- The minimal fix is to parse the form explicitly one level up, in
  `RequireCsrfOnPost`, where the error is visible, and branch on
  `errors.As(err, &*http.MaxBytesError)` before falling through to the
  existing CSRF check. This adds one `if` block and one import; `verifyCsrf`
  and every other code path are untouched.
- No new constants, no configurability added — `32 << 20` is a literal
  matching net/http's own internal default (not a new tunable), and the
  message hardcodes the existing 100 MB figure from `maxUploadBytes` (as
  text, not by formatting the constant, since the message doesn't need to
  track it dynamically — the issue didn't ask for that and it would be
  speculative flexibility for a value that only appears once).

## Existing tests — impact

`arx_go/middleware_test.go`:
- `TestRequireCsrfOnPost_RejectsBadPost` — both subtests use
  `application/x-www-form-urlencoded` bodies well under any size limit;
  `ParseMultipartForm` returns no `MaxBytesError` for either, so the new
  branch is not taken and behavior (403) is unchanged.
- `TestRequireCsrfOnPost_AllowsGet` — GET requests skip the whole `if
  r.Method == http.MethodPost` block, including the new parse call;
  unchanged.
- `TestRequireCsrfOnPost_AllowsValidPost` — valid urlencoded body under the
  limit; unchanged.
- `TestBuildRouter_AllAppRoutesRequireAuth` — POSTs a small urlencoded body
  with a valid CSRF token through the full router; unaffected by the new
  branch since the body is tiny.

None of the three existing `RequireCsrfOnPost` tests need modification.

## Suggested new test (not written — human decides)

A `TestRequireCsrfOnPost_RejectsOversizedBody` test could construct a request
whose body exceeds a small `http.MaxBytesReader`-wrapped limit (the real
`maxUploadBytes` is 100 MB, too large to allocate in a unit test — the test
would need to wrap `r.Body` with a small `http.MaxBytesReader(w, r.Body, N)`
directly in the test, the same way `main.go`'s middleware does, rather than
relying on the production 100 MB constant) and assert:
- status is `http.StatusRequestEntityTooLarge` (413)
- response body contains "too large"

This would catch a regression where a future refactor of
`RequireCsrfOnPost`/`verifyCsrf` reintroduces the swallowed-error behavior.
Worth adding given the bug this issue fixes was itself a silent-swallow; not
written here per instructions — left for the human to decide when
implementing.
