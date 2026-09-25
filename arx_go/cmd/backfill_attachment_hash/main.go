// Command backfill_attachment_hash fills part_attachment.hash and
// company_attachment.hash for rows written before #71. Run once per database,
// from the arx_go directory so config/local.json resolves:
//
//	cd arx_go
//	go run ./cmd/backfill_attachment_hash            # dry run: reports counts only
//	go run ./cmd/backfill_attachment_hash -apply     # writes
//
// Reads DOC_CONTROL_ROOT / SUPPLIER_FILES_ROOT the same way the app does, which is
// why this is a Go command and not part of the SQL migration. Set TEST_MODE=true
// (or test_engine/test_db_name in local.json) to target ArxDev.
//
// This command is standalone (does not import the arx_go package) so it restates
// the attachment hashing rule from arx_go/attachments.go's computeAttachmentHash;
// keep the two in sync if that rule ever changes.
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	arxbase "arx/internal/config"
	arxdb "arx/internal/db"
	"arx/internal/urlutil"
)

func hashLinkString(link string) string {
	sum := sha256.Sum256([]byte(link))
	return hex.EncodeToString(sum[:])
}

// safePath sanitises a stored LOCAL: path and resolves it under root, refusing
// anything that would escape root. Mirrors arx_go/files.go's safePath.
func safePath(root, splat string) (string, bool) {
	var clean []string
	for _, seg := range strings.Split(splat, "/") {
		seg = strings.TrimSpace(seg)
		if seg == "" || seg == "." || seg == ".." {
			continue
		}
		base := filepath.Base(seg)
		if base != "" && base != "." && base != ".." {
			clean = append(clean, base)
		}
	}
	result := root
	if len(clean) > 0 {
		result = filepath.Join(root, filepath.Join(clean...))
	}
	absRoot, err1 := filepath.Abs(root)
	absResult, err2 := filepath.Abs(result)
	if err1 != nil || err2 != nil {
		return "", false
	}
	if !strings.HasPrefix(absResult+string(filepath.Separator), absRoot+string(filepath.Separator)) &&
		absResult != absRoot {
		return "", false
	}
	return result, true
}

// computeAttachmentHash mirrors arx_go/attachments.go's function of the same
// name: file content for a single-file LOCAL: link, else the link string itself.
func computeAttachmentHash(root, link string) string {
	if root == "" || !urlutil.IsLocalFile(link) || urlutil.IsLocalDir(link) {
		return hashLinkString(link)
	}
	rel := strings.ReplaceAll(urlutil.StripLocalPrefix(link), "\\", "/")
	path, ok := safePath(root, rel)
	if !ok {
		return hashLinkString(link)
	}
	f, err := os.Open(path)
	if err != nil {
		return hashLinkString(link)
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return hashLinkString(link)
	}
	return hex.EncodeToString(sum.Sum(nil))
}

func backfillTable(ctx context.Context, db *sql.DB, dialect arxdb.Dialect, table, idCol, linkCol, root string, apply bool) (updated, failed int) {
	rows, err := db.QueryContext(ctx, dialect.Rewrite(
		fmt.Sprintf(`SELECT %s, %s FROM %s WHERE hash IS NULL`, idCol, linkCol, table),
	))
	if err != nil {
		log.Fatalf("querying %s: %v", table, err)
	}
	type pending struct {
		id   any
		link string
	}
	var toUpdate []pending
	for rows.Next() {
		var id any
		var link sql.NullString
		if err := rows.Scan(&id, &link); err != nil {
			rows.Close()
			log.Fatalf("scanning %s: %v", table, err)
		}
		toUpdate = append(toUpdate, pending{id: id, link: link.String})
	}
	rows.Close()

	for _, p := range toUpdate {
		hash := computeAttachmentHash(root, p.link)
		if !apply {
			updated++
			continue
		}
		if _, err := db.ExecContext(ctx, dialect.Rewrite(
			fmt.Sprintf(`UPDATE %s SET hash=@p1 WHERE %s=@p2`, table, idCol),
		), hash, p.id); err != nil {
			log.Printf("updating %s %v: %v", table, p.id, err)
			failed++
			continue
		}
		updated++
	}
	return updated, failed
}

func main() {
	apply := flag.Bool("apply", false, "write hashes (default is a dry run that only reports counts)")
	flag.Parse()

	cfg := arxbase.Load("backfill-attachment-hash")
	dsn := cfg.DSN()
	if dsn == "" {
		log.Fatal("no database password configured — run the app once and configure it via Settings first")
	}
	db, dialect, err := arxdb.Connect(cfg.DBEngine(), dsn)
	if err != nil {
		log.Fatalf("connecting to database: %v", err)
	}
	defer db.Close()

	fmt.Printf("Target database: %s\n", cfg.ConnectionSummary())
	if !*apply {
		fmt.Println("Dry run (pass -apply to write). Reporting counts only.")
	}

	ctx := context.Background()

	supplierRoot := cfg.SupplierFilesRoot
	if supplierRoot == "" {
		supplierRoot = cfg.DocControlRoot
	}

	pu, pf := backfillTable(ctx, db, dialect, cfg.AttachmentsTable(), "id", "file_name", cfg.DocControlRoot, *apply)
	fmt.Printf("%s: %d updated, %d failed\n", cfg.AttachmentsTable(), pu, pf)

	cu, cf := backfillTable(ctx, db, dialect, cfg.CompanyAttachmentsTable(), "supplier_attachment_id", "file_path", supplierRoot, *apply)
	fmt.Printf("%s: %d updated, %d failed\n", cfg.CompanyAttachmentsTable(), cu, cf)
}
