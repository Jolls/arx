package main

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

// Red (#446): attachResultExts/decodeDataURL must accept a PDF data: URL for
// test-record attach steps, while decodePastedImage's four-type map (used by
// the part-Photo/company-logo clipboard-paste paths) stays PDF-free. Fails
// today — attachResultExts and decodeDataURL don't exist yet.
func TestDecodeDataURL_AttachResultExtsAcceptsPDF(t *testing.T) {
	payload := []byte("%PDF-1.4")
	b64 := base64.StdEncoding.EncodeToString(payload)
	dataURL := "data:application/pdf;base64," + b64

	ext, data, err := decodeDataURL(dataURL, attachResultExts)
	if err != nil {
		t.Fatalf("decodeDataURL with attachResultExts: unexpected error: %v", err)
	}
	if ext != ".pdf" {
		t.Errorf("ext = %q, want %q", ext, ".pdf")
	}
	if string(data) != string(payload) {
		t.Errorf("data = %q, want %q", data, payload)
	}

	_, _, err = decodePastedImage(dataURL)
	const wantErrSub = "Unsupported image type: application/pdf"
	if err == nil || !strings.Contains(err.Error(), wantErrSub) {
		t.Fatalf("decodePastedImage(pdf data url) err = %v, want containing %q", err, wantErrSub)
	}
}

func TestDecodePastedImage(t *testing.T) {
	payload := []byte("hello world")
	b64 := base64.StdEncoding.EncodeToString(payload)

	cases := []struct {
		name       string
		dataURL    string
		wantExt    string
		wantData   []byte
		wantErrSub string
	}{
		{"png", "data:image/png;base64," + b64, ".png", payload, ""},
		{"jpeg", "data:image/jpeg;base64," + b64, ".jpg", payload, ""},
		{"webp", "data:image/webp;base64," + b64, ".webp", payload, ""},
		{"gif", "data:image/gif;base64," + b64, ".gif", payload, ""},
		{"no data prefix", "image/png;base64," + b64, "", nil, "image_data must be a data: URL"},
		{"no comma", "data:image/png;base64", "", nil, "image_data must be a data: URL"},
		{"unsupported mime", "data:image/bmp;base64," + b64, "", nil, "Unsupported image type: image/bmp"},
		{"invalid base64", "data:image/png;base64,!!!not-base64!!!", "", nil, "Invalid base64 image data"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ext, data, err := decodePastedImage(c.dataURL)
			if c.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErrSub) {
					t.Fatalf("err = %v, want containing %q", err, c.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ext != c.wantExt {
				t.Errorf("ext = %q, want %q", ext, c.wantExt)
			}
			if string(data) != string(c.wantData) {
				t.Errorf("data = %q, want %q", data, c.wantData)
			}
		})
	}
}

func TestIsGeneratedCategory(t *testing.T) {
	cases := []struct {
		category string
		want     bool
	}{
		{previewCategory, true},
		{thumbnailCategory, true},
		{"Photo", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isGeneratedCategory(c.category); got != c.want {
			t.Errorf("isGeneratedCategory(%q) = %v, want %v", c.category, got, c.want)
		}
	}
}

func TestLockPartForThumbnail(t *testing.T) {
	unlock1 := lockPartForThumbnail("ITEST-lock-1")

	acquired := make(chan struct{})
	go func() {
		unlock2 := lockPartForThumbnail("ITEST-lock-1")
		close(acquired)
		unlock2()
	}()

	select {
	case <-acquired:
		t.Fatal("second lockPartForThumbnail on the same partID acquired while the first was still held")
	case <-time.After(100 * time.Millisecond):
	}

	unlock1()

	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("second lockPartForThumbnail did not acquire after the first unlocked")
	}

	// A different partID must not block on the first partID's lock.
	unlock3 := lockPartForThumbnail("ITEST-lock-1")
	done := make(chan struct{})
	go func() {
		unlock4 := lockPartForThumbnail("ITEST-lock-2")
		unlock4()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("lockPartForThumbnail on a different partID blocked unexpectedly")
	}
	unlock3()
}
