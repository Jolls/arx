package main

import (
	"image"
	"testing"
)

func TestResizeLongEdge(t *testing.T) {
	cases := []struct {
		name         string
		w, h, maxPx  int
		wantW, wantH int
	}{
		{"landscape", 400, 200, 100, 100, 50},
		{"portrait", 200, 400, 100, 50, 100},
		{"square", 300, 300, 150, 150, 150},
		{"no-upscale", 80, 60, 100, 80, 60},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resizeLongEdge(image.NewRGBA(image.Rect(0, 0, c.w, c.h)), c.maxPx)
			if b := got.Bounds(); b.Dx() != c.wantW || b.Dy() != c.wantH {
				t.Errorf("got %dx%d, want %dx%d", b.Dx(), b.Dy(), c.wantW, c.wantH)
			}
		})
	}
}
