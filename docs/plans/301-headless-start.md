# Issue #301: Headless start (`ARX_HEADLESS=1` skips the systray)

## Files
- `arx_go/main.go` (edit)
- `arx_go/headless_test.go` (new; no build tag, package `main`, compiles on Windows and `!windows`)
- `CHANGELOG.md` (edit, one entry under the PR's version, `### Added`)

No change to `console_windows.go` / `console_other.go`; `openDebugConsole()` stays in the shared startup. `systray` import stays.

## Changes to `arx_go/main.go`

New imports: `os`, `os/signal`, `syscall`, `sync` is not needed.

1. `main()`:
```go
func main() {
	if headlessEnabled() {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		code := runHeadless(ctx, startServer, func() { h.CloseDB() })
		stop()
		os.Exit(code)
	}
	systray.Run(onReady, onExit)
}
```
2. `headlessEnabled() bool` returns `os.Getenv("ARX_HEADLESS") == "1"` (exactly `"1"`).
3. `startServer(onStopped func()) (*http.Server, string)`: move the body of `onReady` lines 32-70 into it unchanged (config load, DB connect, `h = New(...)`, templates, startup loads, `openDebugConsole()` if `cfg.DebugMode`, `buildRouter`, `http.Server{Addr: "127.0.0.1:"+cfg.Port}`, listener goroutine). In the goroutine replace `systray.Quit()` with `onStopped()`. Return `server, cfg.Port`.
4. `onReady()`:
```go
func onReady() {
	server, port := startServer(systray.Quit)
	_ = server
	url := "http://localhost:" + port
	go openWhenReady(url, port)
	// icon / tooltip / menu / click goroutine: lines 75-91 unchanged
}
```
(drop the `_ = server` line by using `_, port := startServer(systray.Quit)`.)
5. `runHeadless(ctx context.Context, start func(onStopped func()) (*http.Server, string), closeDB func()) int`:
   - `stopped := make(chan struct{}, 1)`
   - `server, _ := start(func() { select { case stopped <- struct{}{}: default: } })`
   - `select`:
     - `<-ctx.Done()`: `shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)`; `err := server.Shutdown(shutdownCtx)`; `cancel()`; `closeDB()`; log shutdown error if any; return `0` (return `1` if `Shutdown` errored).
     - `<-stopped`: `log.Printf("Arx: listener failed, exiting")`; `closeDB()`; return `1`.
   - `startServer` must pass `func() { h.CloseDB() }` equivalent: `closeDB` in `main()` reads global `h` lazily (closure above), so it is valid after `start` assigns `h`.
   - No browser open, no tray in this path.
6. `onExit()` unchanged.

Notes for implementer: the listener goroutine's callback also fires after a normal `Shutdown` (`http.ErrServerClosed`); the non-blocking send into the buffered channel makes that harmless.

## Test plan

### (1) Coverage audit
- `arx_go/middleware_test.go`: `TestBuildRouter_RejectsForeignHost` (Host allowlist via `buildRouter`, unchanged by this work), route allowlist test around line 353 (`buildRouter`).
- `arx_go/settings_admin_gate_test.go` (line 147): drives `buildRouter`.
- `arx_go/icon_test.go`: `TestAppIcon_NonEmpty`, `TestAppIcon_ValidICOHeader`, `TestAppIcon_TotalLength`, `TestAppIcon_FallsBackOnDecodeError` (tray icon, untouched).
- `arx_go/reports_integration_test.go:638` calls `h2.CloseDB()` incidentally (integration tag).
- Not covered: `main`, `onReady`, `onExit`, `openWhenReady`, `Handler.CloseDB` directly, the listener goroutine.

### (2) Characterization tests (new file `arx_go/headless_test.go`; pass on unchanged code)
```go
func TestCloseDB_NoDBIsNoop(t *testing.T) {
	testHandler().CloseDB() // must not panic
}

func TestCloseDB_ClosesConnection(t *testing.T) {
	h := testHandlerWithDB()
	db := h.DB()
	h.CloseDB()
	if err := db.Ping(); err == nil {
		t.Fatal("expected error pinging a closed DB")
	}
}
```
Pins what `onExit`/headless shutdown relies on. Reuses `testHandler`/`testHandlerWithDB` from `middleware_test.go`.

### (3) Red tests (same file)
Stubs added to `main.go` first so tests compile and fail:
```go
func headlessEnabled() bool { return false }
func runHeadless(ctx context.Context, start func(onStopped func()) (*http.Server, string), closeDB func()) int {
	panic("not implemented")
}
```
- `TestHeadlessEnabled`: `t.Setenv("ARX_HEADLESS", v)`; true only for `"1"`; false for `""`, `"0"`, `"true"`. Fails today: stub returns false, so the `"1"` case fails.
- `TestRunHeadless_ContextCancelShutsDownAndClosesDB`: `start` fake listens on `127.0.0.1:0`, runs `server.Serve(ln)` in a goroutine, calls `onStopped` if Serve returns, returns `(server, port)`. Cancel the ctx; assert return `0`, `closeDB` called exactly once, and `net.Dial` to the listener address then fails. Fails today: stub panics.
- `TestRunHeadless_ListenerFailureReturnsNonZero`: occupy a port with a `net.Listen`; fake `start` builds a server on that same address, runs `ListenAndServe` in a goroutine, calls `onStopped` on error. ctx never cancelled; assert return is non-zero and `closeDB` called once. Fails today: stub panics.
- Limitation: these exercise `runHeadless` with a fake `start`; real `startServer` (config/DB) and real SIGINT/SIGTERM delivery are manual-only.

### (4) Manual-only
- Linux container, no `DISPLAY`, no D-Bus, `ARX_HEADLESS=1`: binary serves on 4568 (needs `libayatana-appindicator3-dev` in image to link).
- `docker stop` (SIGTERM) and Ctrl+C (SIGINT): log shows clean shutdown, process exit code 0.
- Port already in use in headless mode: process exits non-zero.
- Windows tray build without `ARX_HEADLESS`: tray icon, tooltip, "Open Arx", "Quit", browser auto-open on launch, tray quits when the server stops (e.g. port in use).
- `go build ./...`, `go vet ./...`, `go test ./...` on Linux (WSL) and `arx_go\build.bat` on Windows.

## Open questions
1. Debug console: in headless mode on Windows, `cfg.DebugMode` would still call `openDebugConsole()` (kept in shared startup, no behavior change). OK, or skip it when headless?
2. `startServer` returns `(*http.Server, string)` with the port so `onReady` can build the URL. Acceptable, or prefer another way to get the port (e.g. `h`'s config)?
3. Shutdown timeout 10s: acceptable?

## Resolved decisions
- Headless mode skips `openDebugConsole()` even when `cfg.DebugMode` is on (keep it in the tray path only).
- `startServer` returns `(*http.Server, string)` (server, port): accepted.
- Shutdown timeout: 10s: accepted.
