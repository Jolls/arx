package main

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// pdfthumbBlackPDF is black.pdf from github.com/klippa-app/go-pdfium's own
// test suite (shared_tests/testdata/black.pdf, MIT licensed, Copyright (c)
// 2022 Klippa App BV) — a single 612x792pt page filled solid black. Reusing
// it avoids relying on a hand-rolled minimal PDF byte literal that may or
// may not parse; this exact file is already validated against this pdfium
// build by the dependency's own tests.
//
//go:embed testdata/pdfthumb_black.pdf
var pdfthumbBlackPDF []byte

func TestGetPdfiumPool_ReturnsSingletonPool(t *testing.T) {
	p1, err := getPdfiumPool()
	if err != nil {
		t.Fatalf("getPdfiumPool: %v", err)
	}
	if p1 == nil {
		t.Fatal("getPdfiumPool returned nil pool with nil error")
	}
	p2, err := getPdfiumPool()
	if err != nil {
		t.Fatalf("getPdfiumPool (2nd call): %v", err)
	}
	if p1 != p2 {
		t.Fatal("getPdfiumPool did not return the cached pool on second call")
	}
}

func TestRenderPDFFirstPage_ValidPDF(t *testing.T) {
	img, err := renderPDFFirstPage(pdfthumbBlackPDF, 36)
	if err != nil {
		t.Fatalf("renderPDFFirstPage: %v", err)
	}
	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		t.Fatalf("rendered image has empty bounds: %v", b)
	}
	// black.pdf is a full-page solid black fill; the centre pixel should be
	// (near-)black, proving actual rasterisation occurred.
	cx, cy := b.Dx()/2, b.Dy()/2
	r, g, bl, _ := img.At(cx, cy).RGBA()
	if r > 0x1000 || g > 0x1000 || bl > 0x1000 {
		t.Fatalf("expected near-black centre pixel, got r=%d g=%d b=%d", r, g, bl)
	}
}

func TestRenderPDFFirstPage_InvalidPDF(t *testing.T) {
	if _, err := renderPDFFirstPage([]byte("not a pdf"), 72); err == nil {
		t.Fatal("expected error for invalid PDF bytes, got nil")
	}
}

func TestEncodePNG_RoundTrips(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			src.Set(x, y, color.RGBA{R: 10, G: 20, B: 30, A: 255})
		}
	}
	data, err := encodePNG(src)
	if err != nil {
		t.Fatalf("encodePNG: %v", err)
	}
	if !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatal("encodePNG output missing PNG signature")
	}
	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	if decoded.Bounds() != src.Bounds() {
		t.Fatalf("decoded bounds %v != src bounds %v", decoded.Bounds(), src.Bounds())
	}
}

// TestRenderPDFFirstPage_EndToEnd mirrors the real call sequence in api.go's
// thumbnail handler: renderPDFFirstPage -> resizeLongEdge -> encodePNG.
func TestRenderPDFFirstPage_EndToEnd(t *testing.T) {
	page, err := renderPDFFirstPage(pdfthumbBlackPDF, 150)
	if err != nil {
		t.Fatalf("renderPDFFirstPage: %v", err)
	}
	data, err := encodePNG(resizeLongEdge(page, 250))
	if err != nil {
		t.Fatalf("encodePNG(resizeLongEdge): %v", err)
	}
	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	b := decoded.Bounds()
	if b.Dx() > 250 || b.Dy() > 250 {
		t.Fatalf("expected long edge <= 250, got %dx%d", b.Dx(), b.Dy())
	}
}
