package main

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/iotest"
)

func TestBuildAttachmentFileName(t *testing.T) {
	cases := []struct {
		partNumber, rev, description, category, ext string
		want                                        string
	}{
		{"1234-567", "B", "Widget Bracket", "Drawing", ".pdf", "1234-567 B Widget Bracket Drawing.pdf"},
		{"1234-567", "", "Widget", "Drawing", ".pdf", "1234-567 Widget Drawing.pdf"},
		{"1234-567", "", "", "", ".pdf", "1234-567.pdf"},
		{"1234-567", "B", "", "", ".pdf", "1234-567 B.pdf"},
		// whitespace-only fields are treated as blank
		{"1234-567", "", "   ", "  ", ".pdf", "1234-567.pdf"},
		// description longer than descriptionMaxLen (20) is truncated, trailing space trimmed
		{"1234-567", "B", "This is a very long part description here", "Drawing", ".pdf", "1234-567 B This is a very long Drawing.pdf"},
		// illegal filesystem characters are replaced with '-'
		{"AB/CD", "R?2", "Ti:tle", "Spec\\1", ".dwg", "AB-CD R-2 Ti-tle Spec-1.dwg"},
	}
	for _, c := range cases {
		got := buildAttachmentFileName(c.partNumber, c.rev, c.description, c.category, c.ext)
		if got != c.want {
			t.Errorf("buildAttachmentFileName(%q,%q,%q,%q,%q) = %q, want %q",
				c.partNumber, c.rev, c.description, c.category, c.ext, got, c.want)
		}
	}
}

func TestBuildResultImageName(t *testing.T) {
	re := regexp.MustCompile(`^SN123_rID45_tID6_\d{8}_\d{6}\.png$`)
	got := buildResultImageName("123", 45, 6, ".png")
	if !re.MatchString(got) {
		t.Errorf("buildResultImageName(...) = %q, want to match %s", got, re.String())
	}

	// illegal filesystem characters in the serial are sanitized
	got = buildResultImageName("AB/CD", 1, 2, ".jpg")
	if !regexp.MustCompile(`^SNAB-CD_rID1_tID2_\d{8}_\d{6}\.jpg$`).MatchString(got) {
		t.Errorf("buildResultImageName with illegal serial chars = %q, want sanitized", got)
	}
}

func TestCopyReaderIntoDocControl(t *testing.T) {
	root := t.TempDir()

	// fresh copy
	existed, err := copyReaderIntoDocControl(root, "1234_Drawing_A.pdf", strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("fresh copy error: %v", err)
	}
	if existed {
		t.Fatal("fresh copy reported existed=true")
	}
	got, err := os.ReadFile(filepath.Join(root, "1234_Drawing_A.pdf"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("copied content = %q err %v, want %q", got, err, "hello")
	}

	// collision: does not overwrite, reports existed
	existed, err = copyReaderIntoDocControl(root, "1234_Drawing_A.pdf", strings.NewReader("changed"))
	if err != nil {
		t.Fatalf("collision returned error: %v", err)
	}
	if !existed {
		t.Fatal("collision reported existed=false")
	}
	got, _ = os.ReadFile(filepath.Join(root, "1234_Drawing_A.pdf"))
	if string(got) != "hello" {
		t.Fatalf("collision overwrote target: got %q, want unchanged %q", got, "hello")
	}

	// failing reader: error, and no orphan target left behind
	_, err = copyReaderIntoDocControl(root, "new.pdf", iotest.ErrReader(io.ErrUnexpectedEOF))
	if err == nil {
		t.Fatal("failing reader did not error")
	}
	if _, statErr := os.Stat(filepath.Join(root, "new.pdf")); !os.IsNotExist(statErr) {
		t.Fatal("failing-reader copy left an orphan target file")
	}
}

func TestReplaceLocalFileFrom(t *testing.T) {
	root := t.TempDir()
	if _, err := copyReaderIntoDocControl(root, "part.pdf", strings.NewReader("original")); err != nil {
		t.Fatal(err)
	}

	if err := replaceLocalFileFrom(root, "part.pdf", strings.NewReader("replaced")); err != nil {
		t.Fatalf("replace error: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "part.pdf"))
	if string(got) != "replaced" {
		t.Fatalf("replaced content = %q, want %q", got, "replaced")
	}

	// a failing reader must leave the original target intact
	if err := replaceLocalFileFrom(root, "part.pdf", iotest.ErrReader(io.ErrUnexpectedEOF)); err == nil {
		t.Fatal("failing reader did not error")
	}
	got, _ = os.ReadFile(filepath.Join(root, "part.pdf"))
	if string(got) != "replaced" {
		t.Fatalf("failed replace altered target: got %q, want unchanged %q", got, "replaced")
	}
}

func TestAttachmentUploads(t *testing.T) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("upload_file", "test.pdf")
	if err != nil {
		t.Fatal(err)
	}
	fw.Write([]byte("data"))
	mw.Close()

	req := httptest.NewRequest("POST", "/", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if err := req.ParseMultipartForm(10 << 20); err != nil {
		t.Fatal(err)
	}
	ups := attachmentUploads(req, "upload_file")
	if len(ups) != 1 || ups[0].Filename != "test.pdf" {
		t.Fatalf("attachmentUploads = %v, want one file named test.pdf", ups)
	}

	if ups := attachmentUploads(req, "other_field"); len(ups) != 0 {
		t.Fatalf("attachmentUploads for missing field = %v, want empty", ups)
	}

	// non-multipart POST: r.MultipartForm is nil
	plainReq := httptest.NewRequest("POST", "/", strings.NewReader("a=b"))
	plainReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if ups := attachmentUploads(plainReq, "upload_file"); ups != nil {
		t.Fatalf("attachmentUploads for non-multipart request = %v, want nil", ups)
	}
}

func TestWriteIntoDocControl(t *testing.T) {
	root := t.TempDir()

	existed, err := writeIntoDocControl(root, "photo.png", []byte("data1"))
	if err != nil || existed {
		t.Fatalf("first write: existed=%v err=%v, want existed=false err=nil", existed, err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "photo.png"))
	if string(got) != "data1" {
		t.Fatalf("file contents = %q, want %q", got, "data1")
	}

	existed, err = writeIntoDocControl(root, "photo.png", []byte("data2"))
	if err != nil || !existed {
		t.Fatalf("collision write: existed=%v err=%v, want existed=true err=nil", existed, err)
	}
	got, _ = os.ReadFile(filepath.Join(root, "photo.png"))
	if string(got) != "data1" {
		t.Fatalf("collision write must not overwrite; got %q", got)
	}
}

func TestWriteIntoDocControlUnique(t *testing.T) {
	root := t.TempDir()

	name, err := writeIntoDocControlUnique(root, "part Photo.png", ".png", []byte("a"))
	if err != nil || name != "part Photo.png" {
		t.Fatalf("first paste: name=%q err=%v, want %q, nil", name, err, "part Photo.png")
	}

	name, err = writeIntoDocControlUnique(root, "part Photo.png", ".png", []byte("b"))
	if err != nil || name != "part Photo (2).png" {
		t.Fatalf("second paste: name=%q err=%v, want %q, nil", name, err, "part Photo (2).png")
	}

	name, err = writeIntoDocControlUnique(root, "part Photo.png", ".png", []byte("c"))
	if err != nil || name != "part Photo (3).png" {
		t.Fatalf("third paste: name=%q err=%v, want %q, nil", name, err, "part Photo (3).png")
	}
}

func TestComputeAttachmentHash(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("same content"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("same content"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "folder"), 0755); err != nil {
		t.Fatal(err)
	}
	contentHash := hashBytes([]byte("same content"))

	cases := []struct {
		name string
		root string
		link string
		want string // "" means "just check it's non-empty and equals hashLinkString(link)"
	}{
		{"single file present", root, "LOCAL:a.txt", contentHash},
		{"identical content, different name, same hash", root, "LOCAL:b.txt", contentHash},
		{"directory-style link hashes the string, not the dir", root, "LOCAL:folder\\", ""},
		{"http url hashes the string", root, "https://example.com/x.pdf", ""},
		{"UNC path hashes the string", root, `\\server\share\x.pdf`, ""},
		{"missing local file falls back to string hash", root, "LOCAL:missing.txt", ""},
		{"path traversal falls back to string hash", root, "LOCAL:..\\escape.txt", ""},
		{"empty root falls back to string hash", "", "LOCAL:a.txt", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := computeAttachmentHash(c.root, c.link)
			if len(got) != 64 {
				t.Fatalf("computeAttachmentHash(%q, %q) = %q, want 64 hex chars", c.root, c.link, got)
			}
			want := c.want
			if want == "" {
				want = hashLinkString(c.link)
			}
			if got != want {
				t.Errorf("computeAttachmentHash(%q, %q) = %q, want %q", c.root, c.link, got, want)
			}
		})
	}

	if got := computeAttachmentHash(root, "LOCAL:a.txt"); got == computeAttachmentHash(root, "LOCAL:folder\\") {
		t.Errorf("file content hash should not equal the directory-link string hash by coincidence in this test")
	}
}
