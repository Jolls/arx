package main

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"io"
	"sync"
	"time"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"

	xdraw "golang.org/x/image/draw"
)

// PDFium runs as an embedded WebAssembly module (wazero) — pure Go, no cgo.
// Creating the pool (spinning up the wasm runtime) is the expensive one-time
// cost, so the pool itself is created lazily and reused; a transient creation
// failure is not cached, so a later render can retry instead of being
// permanently broken until the process restarts. Each render checks out its
// own instance via pool.GetInstance/Close — with MaxTotal:1 that alone
// serialises concurrent renders (a pdfium instance isn't safe for concurrent
// use), so no separate application-level lock is needed on top of the pool.
var (
	pdfiumPool   pdfium.Pool
	pdfiumPoolMu sync.Mutex
)

func getPdfiumPool() (pdfium.Pool, error) {
	pdfiumPoolMu.Lock()
	defer pdfiumPoolMu.Unlock()
	if pdfiumPool != nil {
		return pdfiumPool, nil
	}
	// Stdout/Stderr must be set explicitly: go-pdfium defaults them to os.Stdout/os.Stderr,
	// which wazero wires into the wasm module's WASI stdio. Under -H windowsgui (no console)
	// those are invalid handles, and wazero's instantiation-time GetFileType check on them fails.
	pool, err := webassembly.Init(webassembly.Config{
		MinIdle: 1, MaxIdle: 1, MaxTotal: 1,
		Stdout: io.Discard, Stderr: io.Discard,
	})
	if err != nil {
		return nil, err
	}
	pdfiumPool = pool
	return pool, nil
}

// renderPDFFirstPage rasterises page 1 of pdfBytes at the given DPI and returns
// an owned *image.RGBA copy (PDFium frees its own buffer on Cleanup, so the
// pixels must be copied out before returning).
func renderPDFFirstPage(pdfBytes []byte, dpi int) (image.Image, error) {
	pool, err := getPdfiumPool()
	if err != nil {
		return nil, fmt.Errorf("pdfium init: %w", err)
	}
	instance, err := pool.GetInstance(time.Second * 30)
	if err != nil {
		return nil, fmt.Errorf("pdfium instance: %w", err)
	}
	defer instance.Close()

	doc, err := instance.OpenDocument(&requests.OpenDocument{File: &pdfBytes})
	if err != nil {
		return nil, fmt.Errorf("open pdf: %w", err)
	}
	defer instance.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: doc.Document})

	count, err := instance.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: doc.Document})
	if err != nil {
		return nil, fmt.Errorf("page count: %w", err)
	}
	if count.PageCount < 1 {
		return nil, fmt.Errorf("pdf has no pages")
	}

	render, err := instance.RenderPageInDPI(&requests.RenderPageInDPI{
		DPI:  dpi,
		Page: requests.Page{ByIndex: &requests.PageByIndex{Document: doc.Document, Index: 0}},
	})
	if err != nil {
		return nil, fmt.Errorf("render page: %w", err)
	}
	defer render.Cleanup()

	src := render.Result.Image
	out := image.NewRGBA(src.Bounds())
	xdraw.Copy(out, image.Point{}, src, src.Bounds(), xdraw.Src, nil)
	return out, nil
}

// resizeLongEdge scales img so its longest edge is at most maxPx, preserving the
// aspect ratio. Images already within maxPx are returned unchanged (no upscaling).
func resizeLongEdge(img image.Image, maxPx int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxPx && h <= maxPx {
		return img
	}
	nw, nh := w, h
	if w >= h {
		nw = maxPx
		nh = h * maxPx / w
	} else {
		nh = maxPx
		nw = w * maxPx / h
	}
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Over, nil)
	return dst
}

// encodePNG encodes img to PNG bytes.
func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
