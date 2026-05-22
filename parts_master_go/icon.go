package main

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"image/png"
)

//go:embed icon.png
var iconPNG []byte

func appIcon() []byte {
	img, err := png.Decode(bytes.NewReader(iconPNG))
	if err != nil {
		return fallbackIcon()
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	// XOR mask — 32-bit BGRA, bottom-up row order (ICO convention).
	xor := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			row := h - 1 - y
			idx := (row*w + x) * 4
			rv, gv, bv, av := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			xor[idx+0] = byte(bv >> 8)
			xor[idx+1] = byte(gv >> 8)
			xor[idx+2] = byte(rv >> 8)
			xor[idx+3] = byte(av >> 8)
		}
	}

	// AND mask — all zeros means opaque; alpha channel handles per-pixel transparency.
	stride := ((w + 31) / 32) * 4
	and := make([]byte, h*stride)

	return buildICO(w, h, xor, and)
}

// fallbackIcon returns a minimal solid-colour ICO used if the embedded PNG fails to decode.
func fallbackIcon() []byte {
	const (
		w, h    = 16, 16
		r, g, b = 102, 126, 234
	)
	xor := make([]byte, w*h*4)
	for i := 0; i < len(xor); i += 4 {
		xor[i+0] = b
		xor[i+1] = g
		xor[i+2] = r
		xor[i+3] = 255
	}
	return buildICO(w, h, xor, make([]byte, h*4))
}

func buildICO(w, h int, xor, and []byte) []byte {
	const bpp = 32
	le := binary.LittleEndian

	var bih bytes.Buffer
	binary.Write(&bih, le, uint32(40))
	binary.Write(&bih, le, int32(w))
	binary.Write(&bih, le, int32(h*2))
	binary.Write(&bih, le, uint16(1))
	binary.Write(&bih, le, uint16(bpp))
	binary.Write(&bih, le, uint32(0))
	binary.Write(&bih, le, uint32(w*h*4))
	binary.Write(&bih, le, int32(0))
	binary.Write(&bih, le, int32(0))
	binary.Write(&bih, le, uint32(0))
	binary.Write(&bih, le, uint32(0))

	imgData := append(bih.Bytes(), xor...)
	imgData = append(imgData, and...)

	const offset = 6 + 16
	var ico bytes.Buffer
	binary.Write(&ico, le, uint16(0))
	binary.Write(&ico, le, uint16(1))
	binary.Write(&ico, le, uint16(1))
	binary.Write(&ico, le, uint8(w))
	binary.Write(&ico, le, uint8(h))
	binary.Write(&ico, le, uint8(0))
	binary.Write(&ico, le, uint8(0))
	binary.Write(&ico, le, uint16(1))
	binary.Write(&ico, le, uint16(bpp))
	binary.Write(&ico, le, uint32(len(imgData)))
	binary.Write(&ico, le, uint32(offset))
	ico.Write(imgData)

	return ico.Bytes()
}
