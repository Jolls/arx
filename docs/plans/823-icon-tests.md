# Plan: #823 — icon.go test coverage

Issue: arx_go/icon.go's ICO byte-encoding logic (appIcon, fallbackIcon, buildICO) has zero
test coverage. Add arx_go/icon_test.go with real assertions. Test-only; no changes to icon.go.

## Background — byte layout (worked out from current icon.go, not guessed)

icon.png (embedded) is 32x32 RGBA PNG.

buildICO(w, h, xor, and) produces:
- ICONDIR (6 bytes): reserved=0 (u16), type=1 (u16), count=1 (u16)
- ICONDIRENTRY (16 bytes) at offset 6:
  - width = byte(w), height = byte(h)   (uint8 — wraps if w/h >= 256; not a concern at 32x32/16x16)
  - colorCount=0 (u8), reserved=0 (u8)
  - planes=1 (u16), bitCount=32 (u16)
  - bytesInRes = uint32(len(imgData))  (u32 LE)
  - imageOffset = uint32(22)           (u32 LE)  -- offset = 6+16
- BITMAPINFOHEADER (40 bytes) at offset 22:
  - biSize=40 (u32), biWidth=w (i32), biHeight=h*2 (i32)
  - biPlanes=1 (u16), biBitCount=32 (u16)
  - biCompression=0 (u32), biSizeImage=w*h*4 (u32)
  - biXPelsPerMeter=0, biYPelsPerMeter=0, biClrUsed=0, biClrImportant=0 (all u32/i32, all 0)
- Pixel data at offset 62: xor mask (w*h*4 bytes) then and mask (h*stride bytes),
  stride = ((w+31)/32)*4

Total length = 62 + w*h*4 + h*stride. bytesInRes (ICONDIRENTRY field, offset 14) must equal
40 + w*h*4 + h*stride (i.e. total length - 22).

For appIcon() at w=h=32: stride=4, total = 62 + 4096 + 128 = 4286 bytes.
For fallbackIcon() at w=h=16 (hardcoded consts in source): stride=4, total = 62 + 1024 + 64 = 1150 bytes.

## fallbackIcon() testability
Directly callable in-package (package main, same package as test) — no need to trigger it via
appIcon()'s error branch. Test it standalone: non-empty, valid minimal ICO, all header fields
consistent with w=h=16.

## appIcon() error-branch testability
`iconPNG` is a package-level `var` (not `const`), populated via `//go:embed`. A test can save/restore
it and temporarily reassign it to invalid PNG bytes to force `png.Decode` to fail, exercising the
`return fallbackIcon()` branch inside appIcon() itself — no production code changes needed:
```go
orig := iconPNG
iconPNG = []byte("not a png")
defer func() { iconPNG = orig }()
got := appIcon()
// assert got equals fallbackIcon() output (or at least matches its length/header)
```

## File change: arx_go/icon_test.go (new file)

Package `main`. Add these test functions:

1. `TestAppIcon_NonEmpty`
   - `got := appIcon()`; assert `len(got) > 0`.

2. `TestAppIcon_ValidICOHeader`
   - Decode source PNG directly (`png.Decode(bytes.NewReader(iconPNG))`) to get `w, h := bounds.Dx(), bounds.Dy()` dynamically (don't hardcode 32 — stay correct if icon.png changes).
   - `got := appIcon()`.
   - Assert `binary.LittleEndian.Uint16(got[0:2]) == 0` (reserved).
   - Assert `binary.LittleEndian.Uint16(got[2:4]) == 1` (type).
   - Assert `binary.LittleEndian.Uint16(got[4:6]) == 1` (count).
   - Assert `got[6] == byte(w)` and `got[7] == byte(h)` (ICONDIRENTRY width/height).
   - Assert `got[8] == 0` (colorCount) and `got[9] == 0` (reserved).
   - Assert `binary.LittleEndian.Uint16(got[10:12]) == 1` (planes).
   - Assert `binary.LittleEndian.Uint16(got[12:14]) == 32` (bitCount).
   - Compute `stride := ((w + 31) / 32) * 4`, `wantImgDataLen := 40 + w*h*4 + h*stride`.
   - Assert `binary.LittleEndian.Uint32(got[14:18]) == uint32(wantImgDataLen)` (bytesInRes).
   - Assert `binary.LittleEndian.Uint32(got[18:22]) == 22` (imageOffset).
   - Assert `binary.LittleEndian.Uint32(got[22:26]) == 40` (biSize).
   - Assert `int32(binary.LittleEndian.Uint32(got[26:30])) == int32(w)` (biWidth).
   - Assert `int32(binary.LittleEndian.Uint32(got[30:34])) == int32(h*2)` (biHeight).
   - Assert `binary.LittleEndian.Uint16(got[34:36]) == 1` (biPlanes).
   - Assert `binary.LittleEndian.Uint16(got[36:38]) == 32` (biBitCount).
   - Assert `binary.LittleEndian.Uint32(got[38:42]) == 0` (biCompression).
   - Assert `binary.LittleEndian.Uint32(got[42:46]) == uint32(w*h*4)` (biSizeImage).
   - Assert bytes[46:62] are all zero (biXPels/biYPels/biClrUsed/biClrImportant).

3. `TestAppIcon_TotalLength`
   - Recompute expected total length the same way as above: `62 + w*h*4 + h*stride`.
   - Assert `len(got) == wantTotal`.

4. `TestFallbackIcon_NonEmpty`
   - `got := fallbackIcon()`; assert `len(got) > 0`.

5. `TestFallbackIcon_ValidICOHeader`
   - Same header-field assertions as `TestAppIcon_ValidICOHeader`, but with hardcoded `w, h := 16, 16` (matching fallbackIcon's unexported consts) and `bitCount == 32`.
   - Assert total length == 1150 (62 + 1024 + 64), per computed formula above.

6. `TestAppIcon_FallsBackOnDecodeError`
   - Save `orig := iconPNG`; `defer func() { iconPNG = orig }()`.
   - Set `iconPNG = []byte("not a png")`.
   - `got := appIcon()`; `want := fallbackIcon()`.
   - Assert `len(got) == len(want)` and (optionally) `bytes.Equal(got, want)` — fallbackIcon() is deterministic (fixed RGB constants), so exact equality is safe.

Imports needed: `bytes`, `encoding/binary`, `image/png`, `testing` (all already used in icon.go except `testing`, so no new external deps).

## Verification
- `cd arx_go; go build ./... ; go vet ./...` then `go test ./... -run Icon -v` (or full `go test ./...`) to confirm all six tests pass.
- No changes to icon.go — confirm via `git diff arx_go/icon.go` showing no diff.
- Follow existing test-file conventions in arx_go (e.g. named_query_unit_test.go): package main, plain `t.Errorf`/`t.Fatalf`, no external test framework.

### Critical Files for Implementation
- arx_go/icon_test.go (new)
- arx_go/icon.go (read-only reference, no changes)
