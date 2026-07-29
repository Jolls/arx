package main

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"testing"
)

func decodedIconDims(t *testing.T) (w, h int) {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(iconPNG))
	if err != nil {
		t.Fatalf("decode icon.png: %v", err)
	}
	bounds := img.Bounds()
	return bounds.Dx(), bounds.Dy()
}

func assertICOHeader(t *testing.T, got []byte, w, h int) {
	t.Helper()
	le := binary.LittleEndian
	stride := ((w + 31) / 32) * 4
	wantImgDataLen := 40 + w*h*4 + h*stride

	if got := le.Uint16(got[0:2]); got != 0 {
		t.Errorf("reserved = %d, want 0", got)
	}
	if got := le.Uint16(got[2:4]); got != 1 {
		t.Errorf("type = %d, want 1", got)
	}
	if got := le.Uint16(got[4:6]); got != 1 {
		t.Errorf("count = %d, want 1", got)
	}
	if got[6] != byte(w) {
		t.Errorf("ICONDIRENTRY width = %d, want %d", got[6], byte(w))
	}
	if got[7] != byte(h) {
		t.Errorf("ICONDIRENTRY height = %d, want %d", got[7], byte(h))
	}
	if got[8] != 0 {
		t.Errorf("colorCount = %d, want 0", got[8])
	}
	if got[9] != 0 {
		t.Errorf("reserved (ICONDIRENTRY) = %d, want 0", got[9])
	}
	if v := le.Uint16(got[10:12]); v != 1 {
		t.Errorf("planes = %d, want 1", v)
	}
	if v := le.Uint16(got[12:14]); v != 32 {
		t.Errorf("bitCount = %d, want 32", v)
	}
	if v := le.Uint32(got[14:18]); v != uint32(wantImgDataLen) {
		t.Errorf("bytesInRes = %d, want %d", v, wantImgDataLen)
	}
	if v := le.Uint32(got[18:22]); v != 22 {
		t.Errorf("imageOffset = %d, want 22", v)
	}
	if v := le.Uint32(got[22:26]); v != 40 {
		t.Errorf("biSize = %d, want 40", v)
	}
	if v := int32(le.Uint32(got[26:30])); v != int32(w) {
		t.Errorf("biWidth = %d, want %d", v, w)
	}
	if v := int32(le.Uint32(got[30:34])); v != int32(h*2) {
		t.Errorf("biHeight = %d, want %d", v, h*2)
	}
	if v := le.Uint16(got[34:36]); v != 1 {
		t.Errorf("biPlanes = %d, want 1", v)
	}
	if v := le.Uint16(got[36:38]); v != 32 {
		t.Errorf("biBitCount = %d, want 32", v)
	}
	if v := le.Uint32(got[38:42]); v != 0 {
		t.Errorf("biCompression = %d, want 0", v)
	}
	if v := le.Uint32(got[42:46]); v != uint32(w*h*4) {
		t.Errorf("biSizeImage = %d, want %d", v, w*h*4)
	}
	for i, b := range got[46:62] {
		if b != 0 {
			t.Errorf("byte %d in biXPels/biYPels/biClrUsed/biClrImportant = %d, want 0", 46+i, b)
		}
	}
}

func TestAppIcon_NonEmpty(t *testing.T) {
	if got := appIcon(); len(got) == 0 {
		t.Fatal("appIcon() returned empty slice")
	}
}

func TestAppIcon_ValidICOHeader(t *testing.T) {
	w, h := decodedIconDims(t)
	assertICOHeader(t, appIcon(), w, h)
}

func TestAppIcon_TotalLength(t *testing.T) {
	w, h := decodedIconDims(t)
	stride := ((w + 31) / 32) * 4
	wantTotal := 62 + w*h*4 + h*stride

	got := appIcon()
	if len(got) != wantTotal {
		t.Errorf("len(appIcon()) = %d, want %d", len(got), wantTotal)
	}
}

func TestFallbackIcon_NonEmpty(t *testing.T) {
	if got := fallbackIcon(); len(got) == 0 {
		t.Fatal("fallbackIcon() returned empty slice")
	}
}

func TestFallbackIcon_ValidICOHeader(t *testing.T) {
	const w, h = 16, 16
	got := fallbackIcon()
	assertICOHeader(t, got, w, h)

	const wantTotal = 1150
	if len(got) != wantTotal {
		t.Errorf("len(fallbackIcon()) = %d, want %d", len(got), wantTotal)
	}
}

func TestAppIcon_FallsBackOnDecodeError(t *testing.T) {
	orig := iconPNG
	iconPNG = []byte("not a png")
	defer func() { iconPNG = orig }()

	got := appIcon()
	want := fallbackIcon()
	if !bytes.Equal(got, want) {
		t.Errorf("appIcon() on decode error = %d bytes, want fallbackIcon() output (%d bytes)", len(got), len(want))
	}
}
