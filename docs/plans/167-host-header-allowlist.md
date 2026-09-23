# #167 Host-header allowlist

- `arx_go/main.go` `buildRouter`: add `r.Use(h.RequireLocalHost)` right after `chi.NewRouter()` (before Logger).
- Add `RequireLocalHost` to `arx_go/auth.go`-adjacent middleware file (`middleware.go` if present, else `auth.go`): allow `r.Host` in `localhost:<port>`, `127.0.0.1:<port>`, `[::1]:<port>` where `<port>` = `h.cfg.Port`; otherwise 421 Misdirected Request, plain-text body.
- Existing tests driving `buildRouter` must set `req.Host = "localhost:<port>"` (httptest default `example.com` is now rejected).
- New test: unexpected `Host` on `/login` and `/settings` → 421; allowed hosts pass.

Resolved decision: strict host:port match (per issue).
