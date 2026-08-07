# Plan: Fix Content-Disposition encoding and Cache-Control drift (#861)

Scope: mechanical patch only. `SupplierFile`'s `serveSupplierFile` (arx_go/suppliers.go) and `POFile` (arx_go/pos.go) get the same `mime.FormatMediaType` filename encoding and `Cache-Control: no-cache` header that `arx_go/files.go`'s `ServeLocalFile`/`ServeSupplierFile` already have. No shared helpers (that's #864). Image-inline is explicitly out of scope — see Open Question.

## 1. `arx_go/suppliers.go`

### 1a. Add `"mime"` import

old_string:
```go
import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"arx/arx_go/models"
	"arx/arxlib/urlutil"
)
```
new_string:
```go
import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"arx/arx_go/models"
	"arx/arxlib/urlutil"
)
```

### 1b. `serveSupplierFile` (currently lines 879–884): filename encoding + Cache-Control

old_string:
```go
	if strings.ToLower(filepath.Ext(path)) == ".pdf" {
		w.Header().Set("Content-Disposition", "inline")
	} else {
		w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(path)+`"`)
	}
	http.ServeFile(w, r, path)
}
```
new_string:
```go
	if strings.ToLower(filepath.Ext(path)) == ".pdf" {
		w.Header().Set("Content-Disposition", "inline")
	} else {
		w.Header().Set("Content-Disposition",
			mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(path)}))
	}
	// See ServeLocalFile (files.go): force revalidation so a replaced file
	// isn't served stale from the browser cache under its unchanged URL (#839).
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, path)
}
```

Note: this block is textually identical to pos.go's before the edit — match on surrounding unique context (imports, or a few lines above/below) when applying. In suppliers.go it's right after the `os.Stat`/`IsNotExist` check at ~line 877, followed by `func supplierFromForm`.

`SupplierFile` at line 836 just delegates to `serveSupplierFile`, so it's covered by this one edit.

## 2. `arx_go/pos.go`

### 2a. Add `"mime"` import

old_string:
```go
import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"arx/arx_go/models"
	"arx/arxlib/urlutil"
)
```
new_string:
```go
import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"arx/arx_go/models"
	"arx/arxlib/urlutil"
)
```

### 2b. `POFile` (currently lines 1205–1210): filename encoding + Cache-Control

old_string:
```go
	if strings.ToLower(filepath.Ext(path)) == ".pdf" {
		w.Header().Set("Content-Disposition", "inline")
	} else {
		w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(path)+`"`)
	}
	http.ServeFile(w, r, path)
}
```
new_string:
```go
	if strings.ToLower(filepath.Ext(path)) == ".pdf" {
		w.Header().Set("Content-Disposition", "inline")
	} else {
		w.Header().Set("Content-Disposition",
			mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(path)}))
	}
	// See ServeLocalFile (files.go): force revalidation so a replaced file
	// isn't served stale from the browser cache under its unchanged URL (#839).
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, path)
}
```

In pos.go this block starts right after the `os.Stat`/`IsNotExist` check at ~line 1204.

## 3. Existing tests to update (currently pin the drifted/buggy behavior — must be updated as part of this change)

### 3a. `arx_go/suppliers_test.go`

`TestServeSupplierFile_PDFInlineNoDB` (lines 246–270): Cache-Control assertion flips from empty to `"no-cache"`.

old_string:
```go
	if got := rec.Header().Get("Content-Disposition"); got != "inline" {
		t.Errorf("Content-Disposition = %q, want %q", got, "inline")
	}
	// Unlike files.go's ServeSupplierFile, this codepath sets no Cache-Control
	// header at all — pin that drift.
	if got := rec.Header().Get("Cache-Control"); got != "" {
		t.Errorf("Cache-Control = %q, want empty (no header set)", got)
	}
}

func TestServeSupplierFile_OtherAttachmentNoDB(t *testing.T) {
```
new_string:
```go
	if got := rec.Header().Get("Content-Disposition"); got != "inline" {
		t.Errorf("Content-Disposition = %q, want %q", got, "inline")
	}
	// See ServeLocalFile/ServeSupplierFile in files.go (#839): force
	// revalidation so a replaced file isn't served stale from cache.
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want %q", got, "no-cache")
	}
}

func TestServeSupplierFile_OtherAttachmentNoDB(t *testing.T) {
```

`TestServeSupplierFile_OtherAttachmentNoDB` (lines 272–290): expected literal (`attachment; filename="notes.txt"`) is unchanged for a plain ASCII filename — only the stale comment needs fixing.

old_string:
```go
	h.serveSupplierFile(rec, req, s, "1")
	// Raw string format here, not mime.FormatMediaType like files.go — pin as-is.
	want := `attachment; filename="notes.txt"`
	if got := rec.Header().Get("Content-Disposition"); got != want {
		t.Errorf("Content-Disposition = %q, want %q", got, want)
	}
}
```
new_string:
```go
	h.serveSupplierFile(rec, req, s, "1")
	want := `attachment; filename="notes.txt"`
	if got := rec.Header().Get("Content-Disposition"); got != want {
		t.Errorf("Content-Disposition = %q, want %q", got, want)
	}
}
```

Add a new test exercising the filename-encoding fix specifically (append after `TestServeSupplierFile_OtherAttachmentNoDB`). Before adding, run `Grep 'mime' arx_go/suppliers_test.go` — add `"mime"` to the import block only if not already present.

```go
func TestServeSupplierFile_QuotedFilenameEncodedNoDB(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg.SupplierFilesRoot = root
	base := filepath.Join(root, "ACME")
	if err := os.Mkdir(base, 0755); err != nil {
		t.Fatal(err)
	}
	// A filename containing a double quote would produce a malformed
	// header with naive `filename="` + name + `"` concatenation (#363).
	writeTempFile(t, base, `notes "final".txt`, "hi")
	s := models.Supplier{SUSupplierCode: "ACME"}
	req := httptest.NewRequest(http.MethodGet, `/supplier/1/file/notes "final".txt`, nil)
	rec := httptest.NewRecorder()
	h.serveSupplierFile(rec, req, s, "1")
	got := rec.Header().Get("Content-Disposition")
	if _, params, err := mime.ParseMediaType(got); err != nil {
		t.Fatalf("Content-Disposition = %q is not valid RFC 6266: %v", got, err)
	} else if params["filename"] != `notes "final".txt` {
		t.Errorf("filename param = %q, want %q", params["filename"], `notes "final".txt`)
	}
}
```

### 3b. `arx_go/pos_test.go`

`TestPOFile_PDFInline` (lines 505–528): same Cache-Control flip.

old_string:
```go
	if got := rec.Header().Get("Content-Disposition"); got != "inline" {
		t.Errorf("Content-Disposition = %q, want %q", got, "inline")
	}
	// Unlike files.go's ServeLocalFile/ServeSupplierFile, POFile sets no
	// Cache-Control header at all — pin that drift.
	if got := rec.Header().Get("Cache-Control"); got != "" {
		t.Errorf("Cache-Control = %q, want empty (no header set)", got)
	}
}

func TestPOFile_OtherAttachment(t *testing.T) {
```
new_string:
```go
	if got := rec.Header().Get("Content-Disposition"); got != "inline" {
		t.Errorf("Content-Disposition = %q, want %q", got, "inline")
	}
	// See ServeLocalFile/ServeSupplierFile in files.go (#839): force
	// revalidation so a replaced file isn't served stale from cache.
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want %q", got, "no-cache")
	}
}

func TestPOFile_OtherAttachment(t *testing.T) {
```

`TestPOFile_OtherAttachment` (lines 530–547): fix stale comment only (value unchanged).

old_string:
```go
	h.POFile(rec, req)
	// Raw string format here, not mime.FormatMediaType like files.go — pin as-is.
	want := `attachment; filename="notes.txt"`
	if got := rec.Header().Get("Content-Disposition"); got != want {
		t.Errorf("Content-Disposition = %q, want %q", got, want)
	}
}
```
new_string:
```go
	h.POFile(rec, req)
	want := `attachment; filename="notes.txt"`
	if got := rec.Header().Get("Content-Disposition"); got != want {
		t.Errorf("Content-Disposition = %q, want %q", got, want)
	}
}
```

Add a new `TestPOFile_QuotedFilenameEncoded` test mirroring `TestServeSupplierFile_QuotedFilenameEncodedNoDB` above, using the existing `poFileRequest(t, "PO-100", filename)` helper (pattern from `TestPOFile_PDFInline`) and `writeTempFile(t, base, ...)`. Check `Grep 'mime' arx_go/pos_test.go` first; add `"mime"` import only if missing.

### 3c. `arx_go/files_test.go` — no changes required

The files.go-based tests already assert the correct behavior and are unaffected.

## 4. Verification steps

1. `go build ./...` (from `arx_go/`, or via build.bat) — confirm new `mime` imports compile and are used.
2. `go test ./arx_go/...` — the three identified tests fail until updated per section 3, then pass alongside the new quoted-filename tests.
3. Manually diff the final `serveSupplierFile`/`POFile` disposition+cache blocks against `ServeSupplierFile` in files.go (~lines 127–135) to confirm parity of header-setting logic and ordering.

## Resolved decision: make image-inline uniform

User decision: make `urlutil.IsImage` inline behavior uniform across **all** file-serving sites, matching `ServeLocalFile`. This applies to `ServeSupplierFile` (files.go:100, currently PDF-only), `serveSupplierFile`/`SupplierFile` (suppliers.go), and `POFile` (pos.go) — all three currently PDF-only, all three become `ext == ".pdf" || urlutil.IsImage(path)`.

### 5. `arx_go/files.go` — `ServeSupplierFile` (lines 127–132)

old_string:
```go
	if strings.ToLower(filepath.Ext(path)) == ".pdf" {
		w.Header().Set("Content-Disposition", "inline")
	} else {
		w.Header().Set("Content-Disposition",
			mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(path)}))
	}
	// See ServeLocalFile: force revalidation so a replaced file isn't served
	// stale from the browser cache under its unchanged URL (#839).
```
new_string:
```go
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".pdf" || urlutil.IsImage(path) {
		w.Header().Set("Content-Disposition", "inline")
	} else {
		w.Header().Set("Content-Disposition",
			mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(path)}))
	}
	// See ServeLocalFile: force revalidation so a replaced file isn't served
	// stale from the browser cache under its unchanged URL (#839).
```
`urlutil` is already imported in files.go (used by `ServeLocalFile`).

### 6. `arx_go/suppliers.go` — `serveSupplierFile`, update section 1b's edit

The new_string in section 1b above becomes:
```go
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".pdf" || urlutil.IsImage(path) {
		w.Header().Set("Content-Disposition", "inline")
	} else {
		w.Header().Set("Content-Disposition",
			mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(path)}))
	}
	// See ServeLocalFile (files.go): force revalidation so a replaced file
	// isn't served stale from the browser cache under its unchanged URL (#839).
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, path)
}
```
`urlutil` is already imported in suppliers.go.

### 7. `arx_go/pos.go` — `POFile`, update section 2b's edit

The new_string in section 2b above becomes:
```go
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".pdf" || urlutil.IsImage(path) {
		w.Header().Set("Content-Disposition", "inline")
	} else {
		w.Header().Set("Content-Disposition",
			mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(path)}))
	}
	// See ServeLocalFile (files.go): force revalidation so a replaced file
	// isn't served stale from the browser cache under its unchanged URL (#839).
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, path)
}
```
`urlutil` is already imported in pos.go.

### 8. Test update: `arx_go/files_test.go` — `TestServeSupplierFile_ImageIsAttachmentNotInline` (lines 217–231)

This test currently pins the opposite (now-superseded) behavior. Rename and flip its assertion.

old_string:
```go
// ServeSupplierFile only special-cases .pdf, unlike ServeLocalFile which also
// inlines images via urlutil.IsImage — pinning that drift (#863).
func TestServeSupplierFile_ImageIsAttachmentNotInline(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg.SupplierFilesRoot = root
	writeTempFile(t, root, "pic.png", "fake-png")
	req := httptest.NewRequest(http.MethodGet, "/supplier-local/pic.png", nil)
	rec := httptest.NewRecorder()
	h.ServeSupplierFile(rec, req)
	got := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(got, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment (image inlining not implemented here)", got)
	}
}
```
new_string:
```go
// ServeSupplierFile now inlines images the same way ServeLocalFile does (#861).
func TestServeSupplierFile_ImageInline(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg.SupplierFilesRoot = root
	writeTempFile(t, root, "pic.png", "fake-png")
	req := httptest.NewRequest(http.MethodGet, "/supplier-local/pic.png", nil)
	rec := httptest.NewRecorder()
	h.ServeSupplierFile(rec, req)
	if got := rec.Header().Get("Content-Disposition"); got != "inline" {
		t.Errorf("Content-Disposition = %q, want %q", got, "inline")
	}
}
```

### 9. New tests: image-inline coverage for `SupplierFile` and `POFile`

Add to `arx_go/suppliers_test.go` (after `TestServeSupplierFile_QuotedFilenameEncodedNoDB` from section 3a):
```go
func TestServeSupplierFile_ImageInlineNoDB(t *testing.T) {
	h := filesTestHandler()
	root := t.TempDir()
	h.cfg.SupplierFilesRoot = root
	base := filepath.Join(root, "ACME")
	if err := os.Mkdir(base, 0755); err != nil {
		t.Fatal(err)
	}
	writeTempFile(t, base, "pic.png", "fake-png")
	s := models.Supplier{SUSupplierCode: "ACME"}
	req := httptest.NewRequest(http.MethodGet, "/supplier/1/file/pic.png", nil)
	rec := httptest.NewRecorder()
	h.serveSupplierFile(rec, req, s, "1")
	if got := rec.Header().Get("Content-Disposition"); got != "inline" {
		t.Errorf("Content-Disposition = %q, want %q", got, "inline")
	}
}
```

Add to `arx_go/pos_test.go` (after the new `TestPOFile_QuotedFilenameEncoded` from section 3b), mirroring the same pattern using `poFileRequest`/`writeTempFile` helpers, asserting `Content-Disposition: inline` for a `.png` file.

### Critical files
- arx_go/suppliers.go
- arx_go/pos.go
- arx_go/files.go (reference pattern, not modified)
- arx_go/suppliers_test.go
- arx_go/pos_test.go
