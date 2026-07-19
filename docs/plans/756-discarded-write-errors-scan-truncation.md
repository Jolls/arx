# #756 — Ruling + fix on discarded write errors and scan truncation (M8-M10)

Scope: `arx_go/pos.go` and `arx_go/records.go` only (per issue body and task instructions). Line
numbers below are current as of this read (issue's own line numbers had drifted slightly).

## Summary

Three related defect classes, one ruling each:

- **M8** — a write (`execContext`/`tx.ExecContext`) whose returned `error` is never checked. The
  caller proceeds (often redirecting 303/302) as if the write succeeded.
- **M9** — a `for rows.Next() { ... rows.Scan(...) ... }` loop that swallows a `Scan` error via
  `continue` (or an `if err == nil` guard with no `else`), and/or never checks `rows.Err()` after
  the loop, so a stream that failed partway renders as a short-but-complete list.
- **M10** — `fetchPOItems` takes a `w http.ResponseWriter` it never writes to, and returns `nil`
  silently on a query error. Every caller treats `nil` as "PO has no lines" rather than "the query
  failed."

Actual site counts found by reading both files in full: **13 discarded-write-error sites** (1 in
pos.go, 12 in records.go — vs. the issue's approximate "12 total / 11 in records.go") and **~19
scan-loop sites** missing the abort-on-Scan-error / `rows.Err()` discipline (vs. the issue's
approximate "11 sites", which appears to have been counted loosely or partly across other files —
see Open Questions). This plan fixes every site actually found in these two files.

---

## Ruling 1 — discarded write errors (M8)

**A failed write must abort the request with an error response; it must never fall through to a
success redirect.** Concretely:

- Outside a transaction (plain `h.execContext(...)`): change to
  `if _, err := h.execContext(...); err != nil { <error-response>; return }`.
- Inside a transaction (`tx.ExecContext(...)`, with `defer tx.Rollback()` already in place):
  same shape — checking the error and returning is always safe because the deferred rollback
  fires when `committed` is never set to `true` (pos.go) or unconditionally via bare
  `defer tx.Rollback()` before commit (records.go's `SaveFormDef`/`ArchiveStep` style, where a
  rollback after a successful commit is a documented no-op).
- Error-response convention matches the file: **pos.go** handlers use
  `h.renderError(w, r, "<Context>: "+err.Error())` then `return`; **records.go** handlers use
  `http.Error(w, "<context> error: "+err.Error(), http.StatusInternalServerError)` then `return`.
  Don't introduce the other file's convention into either file.
- This ruling does **not** mandate wrapping currently-non-transactional two-step
  update-then-insert sequences (`ApproveRecord`, `UnlockRecord`, `LockForm`, `UnlockForm`) in a new
  transaction. That is a separate, larger architectural question (the first write already commits
  outside any tx today) — out of scope here. The fix is only: check the second write's error and
  surface it, same as any other write. See Open Questions for the transactional question.

## Ruling 2 — scan-loop truncation (M9)

- **On a `Scan` error inside a loop that produces the request's primary data** (the rows the
  handler was called to fetch/act on — e.g., `fetchPOItems`, `RFQCompare`'s comparison rows,
  `SaveResults`'/`ResyncRecord`'s step/result rows, `copyFormSteps`), abort the loop immediately
  and surface an error to the caller. Since several of these are helper functions with no `error`
  return value today, this requires **adding an `error` return** to their signature and updating
  every call site to check it (see per-function list below).
- **On a `Scan` error inside a loop that only decorates an otherwise-successful page** (dropdown
  contact lists, "suggested link/price" banners, PO history/receipts timelines, form history dots,
  a type-filter datalist) — abort that loop immediately too (don't keep scanning a broken stream),
  but log the error (`log.Printf`) rather than failing the whole page. A blank dropdown or missing
  history entry is a degraded page, not a wrong one; a page that 500s because a decorative list
  glitched is a worse outcome. This preserves each of these functions' existing signature (no
  `error` return) — see Open Questions for confirming this split before implementing.
- **`rows.Err()` must be checked after every scan loop**, in both categories above, because it
  catches errors that terminated iteration early even when no per-row `Scan` ever failed
  (network/driver errors mid-stream). Primary-data loops: return/propagate the error. Decorative
  loops: log it.
- `RecordsRows` (records.go:315-342) and `PORows` (pos.go:302-333) already do this correctly for
  the primary-data case — use them as the reference shape for other primary-data loops.

## M10 — fetchPOItems

`fetchPOItems(w http.ResponseWriter, r *http.Request, num string) []models.PurchaseOrderLine`
(pos.go:1862) takes `w` but never calls anything on it; on a query error (line 1877-1879) it
returns `nil` silently, and the per-row scan loop (line 1882-1921) also silently drops rows on
`Scan` failure with no `rows.Err()` check.

**Fix:** change the signature to return `([]models.PurchaseOrderLine, error)` and drop the unused
`w` parameter. Every caller must check the error and render/respond with it instead of treating
`nil` as "no lines."

Callers to update (all in pos.go):
- `PODetail` (line 345) — `h.fetchPOItems(w, r, num)` → check err, `h.renderError` + return.
- `POEdit` (line 629) — same.
- `PODuplicate` (line 878) — same (source PO's items feed the duplicate form).
- `POPrint` (line 948) — same.
- `POReceive` (line 1500) — same; a failure here must not fall through to
  `parseReceiveDeltas` treating an empty item list as "enter a quantity" (this is the exact
  bug named in the issue).
- `RFQAddSupplier` (line 2056) — same.

---

## Site-by-site edit list

### pos.go

**M8 — discarded write error**

1. **pos.go:982-984** (`POMarkPrinted`)
   ```go
   // before
   h.execContext(r.Context(), fmt.Sprintf(
       `UPDATE %s SET date_printed=@p1 WHERE number=@p2`, h.cfg.POTable(),
   ), time.Now(), num)
   w.WriteHeader(http.StatusNoContent)
   ```
   ```go
   // after
   if _, err := h.execContext(r.Context(), fmt.Sprintf(
       `UPDATE %s SET date_printed=@p1 WHERE number=@p2`, h.cfg.POTable(),
   ), time.Now(), num); err != nil {
       http.Error(w, err.Error(), http.StatusInternalServerError)
       return
   }
   w.WriteHeader(http.StatusNoContent)
   ```
   (Uses `http.Error`, not `h.renderError`, because this handler is a no-body 204 endpoint with no
   HTML error page — matches its existing `http.Error(w, "PO is not approved", ...)` a few lines
   above.)

**M10 — fetchPOItems**

2. **pos.go:1862-1923** (`fetchPOItems` itself)
   ```go
   // before
   func (h *Handler) fetchPOItems(w http.ResponseWriter, r *http.Request, num string) []models.PurchaseOrderLine {
       pol, po, parts, fil := h.cfg.POLineTable(), h.cfg.POTable(), h.cfg.PartsTable(), h.cfg.AttachmentsTable()
       rows, err := h.queryContext(r.Context(), fmt.Sprintf(`...`, pol, po, parts, fil), num)
       if err != nil {
           return nil
       }
       defer rows.Close()
       var items []models.PurchaseOrderLine
       for rows.Next() {
           var item models.PurchaseOrderLine
           ...
           if err := rows.Scan(...); err == nil {
               ...
               items = append(items, item)
           }
       }
       return items
   }
   ```
   ```go
   // after
   func (h *Handler) fetchPOItems(r *http.Request, num string) ([]models.PurchaseOrderLine, error) {
       pol, po, parts, fil := h.cfg.POLineTable(), h.cfg.POTable(), h.cfg.PartsTable(), h.cfg.AttachmentsTable()
       rows, err := h.queryContext(r.Context(), fmt.Sprintf(`...`, pol, po, parts, fil), num)
       if err != nil {
           return nil, err
       }
       defer rows.Close()
       var items []models.PurchaseOrderLine
       for rows.Next() {
           var item models.PurchaseOrderLine
           ...
           if err := rows.Scan(...); err != nil {
               return nil, err
           }
           ...
           items = append(items, item)
       }
       if err := rows.Err(); err != nil {
           return nil, err
       }
       return items, nil
   }
   ```

3. **pos.go:345** (`PODetail`)
   ```go
   // before
   items := h.fetchPOItems(w, r, num)
   ```
   ```go
   // after
   items, err := h.fetchPOItems(r, num)
   if err != nil {
       h.renderError(w, r, "Error loading PO items: "+err.Error())
       return
   }
   ```

4. **pos.go:629** (`POEdit`) — same pattern as #3.

5. **pos.go:878** (`PODuplicate`)
   ```go
   // before
   sourceItems := h.fetchPOItems(w, r, num)
   ```
   ```go
   // after
   sourceItems, err := h.fetchPOItems(r, num)
   if err != nil {
       h.renderError(w, r, "Error loading PO items: "+err.Error())
       return
   }
   ```

6. **pos.go:948** (`POPrint`) — same pattern as #3.

7. **pos.go:1500** (`POReceive`)
   ```go
   // before
   items := h.fetchPOItems(w, r, num)
   ```
   ```go
   // after
   items, err := h.fetchPOItems(r, num)
   if err != nil {
       h.renderError(w, r, "Error loading PO items: "+err.Error())
       return
   }
   ```
   (`err` is later reused at line 1507 `deltas, err := parseReceiveDeltas(...)` — declare with
   `:=` here since this is the first `err` in scope at this point in the function; check the
   surrounding function for an existing `err` declared earlier and use `=` instead if so — verify
   at implementation time.)

8. **pos.go:2056** (`RFQAddSupplier`)
   ```go
   // before
   items := h.fetchPOItems(w, r, num)
   ```
   ```go
   // after
   items, err := h.fetchPOItems(r, num)
   if err != nil {
       h.renderError(w, r, "Error loading PO items: "+err.Error())
       return
   }
   ```

**M9 — scan-loop truncation**

9. **pos.go:41-72** (`contactsForSupplier`) — decorative (contact dropdown). Add `rows.Err()`
   check + log; keep silent per-row drop as a logged (not silent) skip.
   ```go
   // before (tail of loop + return)
       for rows.Next() {
           var c ContactSummary
           ...
           if rows.Scan(&c.ID, &name, &addr, &city, &state, &zip, &country, &phone, &fax, &email) == nil {
               ...
               out = append(out, c)
           }
       }
       return out
   ```
   ```go
   // after
       for rows.Next() {
           var c ContactSummary
           ...
           if err := rows.Scan(&c.ID, &name, &addr, &city, &state, &zip, &country, &phone, &fax, &email); err != nil {
               log.Printf("contactsForSupplier: scan error: %v", err)
               break
           }
           ...
           out = append(out, c)
       }
       if err := rows.Err(); err != nil {
           log.Printf("contactsForSupplier: rows error: %v", err)
       }
       return out
   ```

10. **pos.go:83-117** (`fetchSuggestLinks`) — decorative (suggestion banner). Same shape as #9.

11. **pos.go:128-163** (`fetchSuggestPrices`) — decorative. Same shape as #9.

12. **pos.go:278-335** (`PORows`) — primary data; scan-error path is already correct
    (`http.Error` + `return` at line 308-312). Add the missing `rows.Err()` check after the loop:
    ```go
    // after loop (before "log.Printf(\"[rows] pos:...")
    if err := rows.Err(); err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    ```

13. **pos.go:1290-1319** (`fetchPOHistory`) — decorative (activity timeline). Currently
    `if rows.Scan(...) != nil { continue }`. Change `continue` → log + `break`, add `rows.Err()`
    log check before `return out`.

14. **pos.go:1935-1967** (`fetchPOReceipts`) — decorative (receipts list). Same shape as #13
    (currently `if err := rows.Scan(...); err == nil { ...; out = append(...) }`).

15. **pos.go:2203-2257** (`RFQCompare`) — primary data (the whole page is the comparison grid).
    Currently `if rows.Scan(...) != nil { continue }` at line 2225-2228. Change to abort:
    `h.renderError(w, r, "Error loading RFQ group: "+err.Error()); return`. Add a `rows.Err()`
    check (same response) after the loop, before `suppliers, orderedRows := buildRFQGrid(lines)`.

16. **pos.go:2282-2297** (`RFQCompareSave`, `idRows` loop) — primary data (the ids gate which
    lines may be updated — a truncated id list means some submitted costs silently get skipped
    with no allowlist error). Currently `if idRows.Scan(&id) == nil { polIDs = append(...) }`.
    ```go
    // after
    for idRows.Next() {
        var id int
        if err := idRows.Scan(&id); err != nil {
            idRows.Close()
            h.renderError(w, r, "Error loading RFQ lines: "+err.Error())
            return
        }
        polIDs = append(polIDs, id)
    }
    if err := idRows.Err(); err != nil {
        idRows.Close()
        h.renderError(w, r, "Error loading RFQ lines: "+err.Error())
        return
    }
    idRows.Close()
    ```

17. **pos.go:2464-2478** (`RFQConvert`, `sibRows` loop) — primary write-adjacent data (drives
    which sibling quotes get declined). Same fix shape as #16, using `h.renderError(w, r, "Error
    finding sibling quotes: "+err.Error())`.

18. **pos.go:2609-2657** (`POsExportCSV`) — this is `H6` from the audit (dead route, already a
    known separate finding) but its scan loop matches M9 exactly: `if err := rows.Scan(...); err
    != nil { return }` silently truncates the CSV with no status/log. Since this route is dead
    (per the audit, no template links it) and is a `sev: high` item tracked separately (H6), this
    plan leaves it untouched — flagged under Open Questions to confirm.

---

### records.go

**M8 — discarded write error**

19. **records.go:748-800** (`SaveFormDef`, per-step UPDATE loop, line 778)
    ```go
    // before
    tx.ExecContext(r.Context(), fmt.Sprintf(`
        UPDATE %s SET ... WHERE id=@p16 AND form_id=@p17`, h.cfg.StepsTable()),
        stepType, ..., id, formID,
    )
    ```
    ```go
    // after
    if _, err := tx.ExecContext(r.Context(), fmt.Sprintf(`
        UPDATE %s SET ... WHERE id=@p16 AND form_id=@p17`, h.cfg.StepsTable()),
        stepType, ..., id, formID,
    ); err != nil {
        http.Error(w, "could not save step: "+err.Error(), http.StatusInternalServerError)
        return
    }
    ```

20. **records.go:891-901** (`SaveFormDef`, new-row INSERT loop) — err is checked but only
    `log.Printf` + `continue`, silently skipping that one new row while the request still succeeds
    (inconsistent with #19's sibling UPDATE loop and with pos.go's analogous new-line-insert loops
    in `POCreate`/`POUpdate`, which abort on the first error). Change to abort the request:
    ```go
    // before
    ).Scan(&newID); err2 != nil {
        log.Printf("insert new test step: %v", err2)
        continue
    }
    ```
    ```go
    // after
    ).Scan(&newID); err2 != nil {
        http.Error(w, "could not insert new step: "+err2.Error(), http.StatusInternalServerError)
        return
    }
    ```

21. **records.go:960-964** (`SaveFormDef`, `test_order` UPDATE)
    ```go
    // before
    if normalizeOrder(stepOrder) != normalizeOrder(currentOrder) {
        h.execContext(r.Context(), fmt.Sprintf(
            "UPDATE %s SET test_order=@p1 WHERE id=@p2", h.cfg.FormsTable()),
            normalizeOrder(stepOrder), formID)
    }
    ```
    ```go
    // after
    if normalizeOrder(stepOrder) != normalizeOrder(currentOrder) {
        if _, err := h.execContext(r.Context(), fmt.Sprintf(
            "UPDATE %s SET test_order=@p1 WHERE id=@p2", h.cfg.FormsTable()),
            normalizeOrder(stepOrder), formID); err != nil {
            http.Error(w, "could not save step order: "+err.Error(), http.StatusInternalServerError)
            return
        }
    }
    ```
    Note: this exec runs *after* `tx.Commit()` (line 904) has already succeeded, outside the tx —
    a failure here is a real partial-success state (steps saved, order not). This plan only adds
    the missing error check/response; whether this update should instead happen inside the same
    tx as the step saves is a separate design question — see Open Questions.

22. **records.go:971-976** (`SaveFormDef`, `record_types`/`instrument_types` UPDATE) — same fix
    shape as #21, same post-commit caveat.

23. **records.go:1843-1855** (`ApproveRecord`, event-history INSERT)
    ```go
    // before
    if n, _ := res.RowsAffected(); n > 0 {
        h.execContext(r.Context(), fmt.Sprintf(
            "INSERT INTO %s (form_record_id, event_type, username, event_date) VALUES (@p1, 'approved', @p2, GETDATE())",
            h.cfg.RecordEventsTable()), recordID, u.Username)
    }
    ```
    ```go
    // after
    if n, _ := res.RowsAffected(); n > 0 {
        if _, err := h.execContext(r.Context(), fmt.Sprintf(
            "INSERT INTO %s (form_record_id, event_type, username, event_date) VALUES (@p1, 'approved', @p2, GETDATE())",
            h.cfg.RecordEventsTable()), recordID, u.Username); err != nil {
            http.Error(w, "approve error: "+err.Error(), http.StatusInternalServerError)
            return
        }
    }
    ```

24. **records.go:1899-1911** (`UnlockRecord`, event-history INSERT) — same shape as #23, message
    `"unlock error: "+err.Error()`. (This is the addendum-flagged site: "`UnlockRecord`'s `unlocked`
    event-history INSERT result is unchecked; a failed history write still redirects as a
    successful unlock.")

25. **records.go:2092-2104** (`LockForm`, event-history INSERT) — same shape, message
    `"lock error: "+err.Error()`.

26. **records.go:2133-2145** (`UnlockForm`, event-history INSERT) — same shape, message
    `"unlock error: "+err.Error()`.

27. **records.go:2320-2338** (`SaveResults`, per-field UPDATE on existing result row)
    ```go
    // before
    h.execContext(r.Context(), fmt.Sprintf(`
        UPDATE %s SET result=@p1, comment=@p2, pass_fail=@p3, updated_at=GETDATE()
        WHERE id=@p4`, h.cfg.ResultsTable()),
        result, comment, passFail, prev.ID)
    ```
    ```go
    // after
    if _, err := h.execContext(r.Context(), fmt.Sprintf(`
        UPDATE %s SET result=@p1, comment=@p2, pass_fail=@p3, updated_at=GETDATE()
        WHERE id=@p4`, h.cfg.ResultsTable()),
        result, comment, passFail, prev.ID); err != nil {
        http.Error(w, "could not save result: "+err.Error(), http.StatusInternalServerError)
        return
    }
    ```
    This exec is inside a `for key, vals := range r.Form` loop over every submitted field — an
    early `return` here means any fields not yet processed in map iteration order are dropped.
    That's consistent with every other "abort the whole write on first error" site in this plan;
    flagged under Open Questions since map iteration order is non-deterministic (which field's
    error surfaces first will vary run to run, though the *fact* that the request errors out
    would not).

28. **records.go:2339-2360** (`SaveResults`, legacy-materialize INSERT) — same fix shape as #27,
    message `"could not save result: "+err.Error()`.

29. **records.go:2373-2382** (`SaveResults`, record UPDATE — `record_date` parses, first branch)
    ```go
    // before
    if parseErr == nil {
        h.execContext(r.Context(), fmt.Sprintf(
            "UPDATE %s SET record_date=@p1, comments=@p2, instrument_type=@p3, lot_id=@p4, build_id=@p5, updated_at=GETDATE() WHERE id=@p6",
            h.cfg.RecordsTable()), rd, comments, instrumentType, lotArg, buildArg, recordID)
    } else {
    ```
    ```go
    // after
    if parseErr == nil {
        if _, err := h.execContext(r.Context(), fmt.Sprintf(
            "UPDATE %s SET record_date=@p1, comments=@p2, instrument_type=@p3, lot_id=@p4, build_id=@p5, updated_at=GETDATE() WHERE id=@p6",
            h.cfg.RecordsTable()), rd, comments, instrumentType, lotArg, buildArg, recordID); err != nil {
            http.Error(w, "could not save record: "+err.Error(), http.StatusInternalServerError)
            return
        }
    } else {
    ```

30. **records.go:2383-2387** (`SaveResults`, record UPDATE — `record_date` parse failed, second
    branch, no `record_date` in the SET list) — same fix shape as #29.

31. **records.go:2388-2391** (`SaveResults`, record UPDATE — no `record_date` submitted at all,
    `else` of the outer `if rdStr := ...`) — same fix shape as #29. (Sites 29-31 are three mutually
    exclusive branches of one logical "save the record header" step — see Open Questions on
    whether the original audit counted these as one site or three.)

32. **records.go:2528-2533** (`ResyncRecord`, `test_order`/`form_revision` UPDATE) — already
    checked correctly (`if _, err := tx.ExecContext(...); err != nil { http.Error(...); return }`).
    **No change needed** — listed here only because it's adjacent to the fixed sites and worth
    confirming it's already correct so the implementer doesn't re-touch it.

**M9 — scan-loop truncation**

33. **records.go:193-221** (`FormsList`, primary data — the forms list *is* the page). Currently
    `if err := rows.Scan(...); err != nil { continue }` at line 210-212, no `rows.Err()` check.
    ```go
    // after (loop body)
    if err := rows.Scan(&f.ID, &f.PartNumberID, &f.IsLocked, &f.Revision, &f.PartNumber, &f.Title); err != nil {
        http.Error(w, "scan error: "+err.Error(), http.StatusInternalServerError)
        return
    }
    forms = append(forms, f)
    ```
    ```go
    // after the loop, before h.renderRecords(...)
    if err := rows.Err(); err != nil {
        http.Error(w, "rows error: "+err.Error(), http.StatusInternalServerError)
        return
    }
    ```

34. **records.go:252-265** (`RecordsList`, `typeRows` — decorative type-filter datalist). Currently
    `if typeRows.Scan(&c) == nil { typeOptions = append(...) }`, no `Err()` check. Decorative fix
    shape (log + break, log `Err()`), matching pos.go site #9-14.

35. **records.go:374-429** (`FormDef`, `stepRows` — primary data, the form definition itself).
    Currently `if err := stepRows.Scan(...); err != nil { continue }` at line 405-407, no
    `rows.Err()` check. Change `continue` → `http.Error(w, "scan error: "+err.Error(),
    http.StatusInternalServerError); return`; add `rows.Err()` check (same response) after the
    loop, before `ids := form.OrderedTestIDs()`.

36. **records.go:469-484** (`FormDef`, `hRows` — decorative history timeline dots). Currently
    `if err := hRows.Scan(&hp.At, &hp.Count); err == nil { histPoints = append(...) }` inside an
    `if err == nil { defer hRows.Close(); for hRows.Next() {...} }` guard. Decorative fix shape:
    log on Scan error and `break`; log `hRows.Err()` after the loop.

37. **records.go:570-586** (`FormDefHistory`, `rows` — primary data, this is a JSON API endpoint
    whose entire payload is the scanned rows). Currently `if err := rows.Scan(...); err != nil {
    continue }` at line 577-579, no `rows.Err()` check.
    ```go
    // after (loop body)
    if err := rows.Scan(&s.ID, &s.Type, &s.Parameter,
        &s.SpecNom, &s.SpecMin, &s.SpecMax,
        &s.SpecUnits, &s.PFType, &s.DefaultResult, &s.HideFormula, &changed,
    ); err != nil {
        http.Error(w, "scan error: "+err.Error(), http.StatusInternalServerError)
        return
    }
    s.Changed = changed == 1
    steps = append(steps, s)
    ```
    ```go
    // after the loop, before w.Header().Set(...)
    if err := rows.Err(); err != nil {
        http.Error(w, "rows error: "+err.Error(), http.StatusInternalServerError)
        return
    }
    ```

38. **records.go:617-656** (`EditFormDef`, `stepRows` — primary data, the editable step list).
    Currently `continue` on Scan error at line 638-640, no `rows.Err()` check. Same fix shape as
    #35: abort with `http.Error(...)`, add `rows.Err()` check before `ids := form.OrderedTestIDs()`.

39. **records.go:1214-1228** (`RecordDetail`, `eventRows` — the lifecycle audit trail; arguably
    primary content of the page per issue #250/#251, not purely decorative, since this *is* the
    audit trail feature). Currently:
    ```go
    eventRows, err := h.queryContext(...)
    if err == nil {
        for eventRows.Next() {
            var ev models.RecordEvent
            if err := eventRows.Scan(...); err != nil {
                continue
            }
            events = append(events, ev)
        }
        eventRows.Close()
    }
    ```
    The outer query error is already silently dropped too (`if err == nil` with no `else`) — this
    page has already rendered other primary content by this point (record, form, rows) via earlier
    `return`-on-error checks, so failing the whole page here would be a regression (a working
    record view would start 500ing because its audit-trail sidebar query failed). Given
    `RecordDetail` is otherwise a "abort on any error" handler for its *own* record/form/rows data,
    but this section reads as an already-deliberate degrade-gracefully choice (no early return on
    the outer query error), this is flagged under **Open Questions** rather than guessed: does the
    audit trail here get promoted to "abort the whole page" (consistent with the rest of the
    handler) or stay "log and show a partial trail" (consistent with its current no-return
    behavior on query failure)? Whichever is chosen, add: abort-the-loop-on-Scan-error (`break`
    instead of `continue`, at minimum) and a `rows.Err()` check, logged either way.

40. **records.go:1231** (`loadEventSnapshots` call, `snapshots, _ := h.loadEventSnapshots(...)`) —
    the error is explicitly discarded here at the call site. `loadEventSnapshots` itself is in
    `records_history.go`, **outside this issue's stated file scope** (pos.go/records.go only); its
    internal scan-loop behavior is not audited by this plan. Flagged under Open Questions since the
    call site here silently drops whatever error that function does return.

41. **records.go:2294-2303** (`SaveResults`, `exRows` — primary data; these are the "existing
    result" rows the whole save logic diffs against). Currently `if err := exRows.Scan(...); err
    != nil { continue }`, no `rows.Err()` check. Change `continue` → `http.Error(w, "scan error:
    "+err.Error(), http.StatusInternalServerError); return`; add `exRows.Err()` check after the
    loop, before `for key, vals := range r.Form {`.

42. **records.go:2253-2273** (`SaveResults`, `stepRows` — primary data, the step definitions used
    to compute pass/fail and materialize snapshots). Same fix shape as #41 (currently `continue` on
    Scan error at line 2260-2262, no `rows.Err()` check).

43. **records.go:2455-2464** (`ResyncRecord`, `resRows` — primary data, the existing-results map
    driving the whole resync). Currently `if err := resRows.Scan(...); err != nil { continue }`,
    manual `resRows.Close()` at line 2464, no `rows.Err()` check.
    ```go
    // after
    for resRows.Next() {
        var id, tid int
        var result string
        if err := resRows.Scan(&id, &tid, &result); err != nil {
            resRows.Close()
            http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
            return
        }
        existing[tid] = id
        curResults[tid] = &models.TestResult{ID: id, TestID: tid, Result: result}
    }
    if err := resRows.Err(); err != nil {
        resRows.Close()
        http.Error(w, "query error: "+err.Error(), http.StatusInternalServerError)
        return
    }
    resRows.Close()
    ```

44. **records.go:2567-2573** (`formPNList`) — already aborts correctly on Scan error
    (`return nil, err`). Missing only a `rows.Err()` check before the final `return list, nil`:
    ```go
    // add before "return list, nil"
    if err := rows.Err(); err != nil {
        return nil, err
    }
    ```

45. **records.go:2619-2631** (`copyFormSteps`, `stepRows` — primary data for this helper, which
    already returns `error`). Already aborts correctly on Scan error (`return err`). Missing only
    a `rows.Err()` check:
    ```go
    // add after the loop, before "// Walk steps in source test_order sequence."
    if err := stepRows.Err(); err != nil {
        return err
    }
    ```

46. **records.go:2694-2700** (`NewForm`, `rows` — decorative "copy steps from" dropdown source
    list). Currently `if err := rows.Scan(...); err != nil { continue }`, no `rows.Err()` check.
    Decorative fix shape: log + `break` on Scan error, log `rows.Err()`.

47. **records.go:2978-2989** (`TestReport`, `resRows` — primary data, the whole report). Currently
    `if err := resRows.Scan(...); err != nil { continue }`, no `rows.Err()` check. Change
    `continue` → `http.Error(w, "scan error: "+err.Error(), http.StatusInternalServerError);
    return`; add `resRows.Err()` check (same response) after the loop, before
    `h.renderRecords(w, r, "test_report.html", ...)`.

---

## Open questions

1. **Decorative vs. primary-data split (Ruling 2).** This plan classifies loops as
   "decorative" (log-and-degrade) vs. "primary" (abort-and-error) based on judgment about what
   each page is *for*. A few are genuinely borderline and should be confirmed before
   implementation:
   - `RecordDetail`'s `eventRows` lifecycle audit trail (site #39) — is a failed audit-trail query
     acceptable to degrade silently on an otherwise-working record page, or does #250/#251's
     "audit trail" framing mean it must be treated as primary (abort the whole page)?
   - `FormDef`'s `hRows` history timeline dots (site #36) vs. `FormDefHistory`'s `rows` (site #37,
     the actual point-in-time reconstruction) — these are closely related but classified
     oppositely (decorative vs. primary) here; confirm that split is right.

2. **`snapshots, _ := h.loadEventSnapshots(...)`** (records.go:1231) discards whatever error that
   function returns, but `loadEventSnapshots` itself lives in `records_history.go`, outside this
   issue's file scope. Should this call site at least log the discarded error even though the
   callee isn't being fixed here? Left unfixed pending a decision on whether to touch it at all
   without also fixing the callee.

3. **Post-commit non-transactional writes in `SaveFormDef`** (sites #21/#22, records.go:960-976)
   run *after* `tx.Commit()` has already succeeded. Adding the missing error check (this plan's
   fix) surfaces the failure but leaves the already-committed step changes in place with a stale
   `test_order`/`record_types` — a real partial-success state that predates this issue. Should
   these two updates move *inside* the transaction (a slightly larger, more correct fix) instead of
   just gaining an error check? Flagging rather than guessing since it changes transaction
   boundaries, not just error handling.

4. **`SaveResults`' per-field/per-record UPDATE aborts mid-loop** (sites #27/#28) iterate
   `r.Form` (a Go map) in nondeterministic order. Aborting the whole request on the first write
   error (this plan's fix, matching every other "abort on first write error" site) means *which*
   field's error the user sees can vary run to run, though some fields may already be persisted
   from earlier loop iterations before the error — same partial-write risk as `SaveFormDef`'s
   per-step loop (already accepted there). Confirming this is acceptable, not a reason to design
   something more clever (e.g., collect-all-errors) for this issue.

5. **pos.go:2609 `POsExportCSV`** (site #18) has the exact M9 shape (silent scan-error return, no
   status/log) but is tracked separately in the audit as `H6` (dead route, always-500 due to
   nonexistent columns, `sev: high`). Left untouched in this plan — confirm it should stay
   out of scope for #756 and be picked up whenever H6 is filed/fixed.

6. **Site-count mismatch with the issue body.** The issue cites "12 discarded write errors" and
   "11 sites" of scan truncation, sourced from an audit pass's approximate tally (possibly across
   more files than just these two, per the M9 addendum which also named sites in
   `records_yield.go`, `records_failure_modes.go`, `records_history.go`, `named_query.go`, and
   `api.go` — all outside this issue's stated pos.go/records.go scope). A full line-by-line read of
   just these two files found 13 write sites and ~19 scan-loop sites needing a fix. This plan
   fixes every site found by that read rather than stopping at the issue's approximate count;
   flagging the discrepancy rather than silently under- or over-delivering relative to the
   original estimate.
