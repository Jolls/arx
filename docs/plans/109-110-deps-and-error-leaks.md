# #109 + #110 — dependency advisories, raw driver errors on /login

## #109
1. `arx_go/go.mod`: chi v5.1.0→v5.3.0, x/crypto v0.53.0→v0.56.0, x/image v0.44.0→v0.45.0 (`go get` in `arx_go`, then `go mod tidy`).
2. `arxlib/go.mod`: `go get golang.org/x/crypto@v0.56.0` (indirect, currently v0.18.0), `go mod tidy`.
3. `.github/workflows/test.yml`: add a govulncheck step per module (`go run golang.org/x/vuln/cmd/govulncheck@latest ./...`).
4. Verify: govulncheck in both modules, `go build/vet/test ./...` in both.

## #110
1. `arx_go/auth.go`: add `serverError(w, msg, err)` — logs `msg: err` via `log.Printf`, responds with generic `msg` and 500.
2. Replace the three `"database error: "+err.Error()` sites (LoginGet, LoginPost x2) with `serverError(w, "database error", err)`.
3. `arx_go/auth_test.go`: register a driver whose `Open` fails with a host/user/db-bearing error; assert `POST /login` body contains none of those strings and the log does.
4. The other ~158 sites are out of scope (issue: optional cleanup).
