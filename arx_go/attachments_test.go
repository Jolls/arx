package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildAttachmentFileName(t *testing.T) {
	cases := []struct {
		partNumber, rev, title, category, ext string
		want                                  string
	}{
		{"1234-567", "B", "Widget Bracket", "Drawing", ".pdf", "1234-567 B Widget Bracket Drawing.pdf"},
		{"1234-567", "", "Widget", "Drawing", ".pdf", "1234-567 Widget Drawing.pdf"},
		{"1234-567", "", "", "", ".pdf", "1234-567.pdf"},
		{"1234-567", "B", "", "", ".pdf", "1234-567 B.pdf"},
		// whitespace-only fields are treated as blank
		{"1234-567", "", "   ", "  ", ".pdf", "1234-567.pdf"},
		// title longer than titleMaxLen (20) is truncated, trailing space trimmed
		{"1234-567", "B", "This is a very long part title here", "Drawing", ".pdf", "1234-567 B This is a very long Drawing.pdf"},
		// illegal filesystem characters are replaced with '-'
		{"AB/CD", "R?2", "Ti:tle", "Spec\\1", ".dwg", "AB-CD R-2 Ti-tle Spec-1.dwg"},
	}
	for _, c := range cases {
		got := buildAttachmentFileName(c.partNumber, c.rev, c.title, c.category, c.ext)
		if got != c.want {
			t.Errorf("buildAttachmentFileName(%q,%q,%q,%q,%q) = %q, want %q",
				c.partNumber, c.rev, c.title, c.category, c.ext, got, c.want)
		}
	}
}

func TestCopyIntoDocControl(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(t.TempDir(), "source.pdf")
	if err := os.WriteFile(src, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	// fresh copy
	existed, err := copyIntoDocControl(root, "1234_Drawing_A.pdf", src)
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
	if err := os.WriteFile(src, []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	existed, err = copyIntoDocControl(root, "1234_Drawing_A.pdf", src)
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

	// missing source: error, and no orphan target left behind
	_, err = copyIntoDocControl(root, "new.pdf", filepath.Join(t.TempDir(), "nope.pdf"))
	if err == nil {
		t.Fatal("missing source did not error")
	}
	if _, statErr := os.Stat(filepath.Join(root, "new.pdf")); !os.IsNotExist(statErr) {
		t.Fatal("missing-source copy left an orphan target file")
	}
}
