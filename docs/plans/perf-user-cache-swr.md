# Session-user cache: stale-while-revalidate for GET requests

No GitHub issue yet (create one and rename this file `<issue>-user-cache-swr.md`).

## Problem

Measured 2026-10-01 on ArxProd (Azure Postgres, ~80 ms ping RTT):

- Opening `/parts` after >60 s idle makes two sequential DB round trips:
  `GetActiveUserByID` (169 ms, trivial query, ~2 RTT) then `ListParts` (89 ms).
  Server total 313 ms.
- Postgres execution is ~7 ms (EXPLAIN ANALYZE). The cost is network lag, not the query.
- Cause of the first round trip: `cachedUserByID` ([auth.go:43](../../arx_go/auth.go))
  has a 60 s TTL (`userCacheTTL`, [handlers.go:154](../../arx_go/handlers.go)). An expired
  entry blocks the request on a DB lookup. Every navigation after a >60 s pause pays 80–170 ms.

## Constraint

The cache is per `Arx.exe` process. `invalidateUserCache` evicts only the local entry, so
an admin deactivating/demoting someone in their own process does not reach that user's
process. The TTL is the real revocation window; do not raise it.

## Design (decided)

Keep the 60 s TTL. Change only what happens when an entry is expired, based on HTTP method:

| Situation | Behaviour |
|---|---|
| Fresh entry | Return it (unchanged) |
| Expired entry, GET/HEAD | Return the stale user immediately; refresh in the background |
| Expired entry, POST/PUT/PATCH/DELETE | Synchronous lookup (unchanged) |
| No entry (first request after startup) | Synchronous lookup, any method |

Rationale: reads should be instant; a few hundred ms after a write is not noticed.
Writes never act on stale permissions.

Background refresh details:
- One refresh in flight per user ID (flag on the entry, guarded by `userMu`), so concurrent
  GETs don't pile up lookups.
- Use `context.Background()` with a timeout, not the request context (it is cancelled when
  the response finishes).
- Refresh returns nil/inactive user: evict the entry so the next request is rejected,
  same as today's "never cache inactive" rule.
- Refresh errors (DB hiccup): keep the stale entry, clear the in-flight flag, retry on the
  next request.
- `invalidateUserCache` and `userCacheTTL` unchanged.

Accepted trade-off: a user deactivated while idle gets one more GET served with stale data
before rejection. Revocation lag is ~TTL + one request, same as today.

## Changes

- `arx_go/auth.go`: `cachedUserByID` takes the request method (or a `stalePermitted bool`);
  `withUser` passes `r.Method`. Add in-flight flag to `userCacheEntry`; add background
  refresh helper.
- `arx_go/auth_test.go` (existing `userByID` seam / fake DB): see Verification.
- `CHANGELOG.md`: one `### Changed` line under the branch's version entry.

## Verification

Unit tests (fake `userByID`):
1. Expired entry + GET returns stale user without calling the DB synchronously; cache holds
   the fresh user after the refresh completes.
2. Expired entry + POST blocks and returns the fresh user.
3. Refresh that reports an inactive/missing user evicts the entry; next request is rejected.
4. N concurrent GETs on an expired entry trigger exactly one refresh.
5. Refresh error keeps the stale entry and allows a later retry.

Then `go build ./...`, `go vet ./...`, `go test ./...` from repo root.

Manual (user runs, `DEBUG_MODE=true`, `go run .` in `arx_go/`): load `/parts`, wait ~70 s,
reload. Expect no `GetActiveUserByID` / `[SLOW SQL]` line before `/api/parts/rows`
(it appears afterwards, from the background refresh) and `[PERF SUMMARY] /parts` to drop
by roughly 80–170 ms. Then POST something after >60 s idle and confirm it still works.

## Not in this change (follow-ups from the same investigation)

- Statement-prepare cost: first execution of a statement on a pooled connection costs ~2 RTT
  (pgx default `cache_statement`); simple protocol or pool warm-up would make it 1 RTT.
- `/api/parts/rows` is ~531 KB; ETag/short-lived cache would skip ~89 ms DB + ~44 ms Go work.
- Missing measurement: browser console `[rows] … fetch=… render=…` line, to rule out local
  cost.
- Check that PO/record approval handlers re-verify the user from the DB (relevant to any
  future TTL increase).
