/* =============================================================
   Arx Parts Master — application JavaScript
   ============================================================= */

const ROWS_PER_PAGE = 30;
let currentPage = 1;
let allRows = [];
let sortCol = null;
let sortDir = 'asc';
// The table driving the current data-rows-url flow (#875). Most pages have exactly
// one <table>/<tbody>, so a bare document.querySelector('tbody') always found the
// right one; pages with extra plain tables (e.g. the lot/unit genealogy trace pages)
// need lookups scoped to this table specifically. Set once in loadListRows().
let activeTable = null;

// PO status / BOM cost-source badge markup, rendered client-side for the
// /pos and BOM table views. Mirrors the po_status_badge / bom_source_badge
// templates in shared/partials.html — keep both in sync.
const PO_STATUS_BADGES = {
    rfq:                '<span class="badge bg-dark">RFQ</span>',
    draft:              '<span class="badge bg-secondary">Draft</span>',
    open:               '<span class="badge bg-info text-dark">Open</span>',
    sent:               '<span class="badge badge-sent">Sent</span>',
    partially_received: '<span class="badge bg-warning text-dark">Partially Received</span>',
    closed:             '<span class="badge bg-success">Closed</span>',
    cancelled:          '<span class="badge bg-danger">Cancelled</span>',
};

const BOM_SOURCE_BADGES = {
    rollup: '<span class="badge bg-info text-dark">Rollup</span>',
    price: '<span class="badge bg-success">Price</span>',
    labor: '<span class="badge badge-labor">Labor</span>',
    current_cost: '<span class="badge bg-secondary">Cost</span>',
    missing: '<span class="badge bg-warning text-dark">Missing</span>',
};

// --- Filter/sort state persistence across navigation (#390) ---
// Saves to sessionStorage so the list restores its view when you hit Back.

function _filterStorageKey() {
    return 'arx.filter.' + window.location.pathname;
}

function saveFilterState() {
    if (!document.querySelector('tr.filter-row')) return;
    const filters = getFilterColumns();
    const rfqs     = document.getElementById('show-rfqs');
    const inactive = document.getElementById('show-inactive');
    try {
        sessionStorage.setItem(_filterStorageKey(), JSON.stringify({
            filters,
            sort: { col: sortCol, dir: sortDir },
            showRFQs:     rfqs     ? rfqs.checked     : null,
            showInactive: inactive ? inactive.checked : null,
            page: currentPage
        }));
    } catch (e) {}
}

function restoreFilterState() {
    try {
        const raw = sessionStorage.getItem(_filterStorageKey());
        if (!raw) return;
        _applyState(JSON.parse(raw));
    } catch (e) {}
}

// Apply a parsed view-state object (filters/sort/toggles/page) to the DOM and
// engine globals. Shared by the sessionStorage and URL hydration paths.
function _applyState(st) {
    if (st.filters) {
        const ths = Array.from(document.querySelectorAll('tr.filter-row th'));
        st.filters.forEach((f, i) => {
            const th = ths[i];
            if (!th || !f) return;
            if (f.type === 'date') {
                const from = th.querySelector('.date-filter-from');
                const to   = th.querySelector('.date-filter-to');
                if (from) from.value = f.from || '';
                if (to)   to.value   = f.to   || '';
                updateDateFilterButton(th);
            } else {
                const input = th.querySelector('input, select');
                if (input) input.value = f.value || '';
            }
        });
    }
    if (st.sort && st.sort.col !== null) {
        sortCol = st.sort.col;
        sortDir = st.sort.dir || 'asc';
    }
    const rfqs     = document.getElementById('show-rfqs');
    const inactive = document.getElementById('show-inactive');
    if (rfqs     && st.showRFQs     !== null) rfqs.checked     = st.showRFQs;
    if (inactive && st.showInactive !== null) inactive.checked = st.showInactive;
    if (st.page) currentPage = st.page;
}

// --- Shareable/bookmarkable views via the URL query string (#513) ---
// Mirror the same state into the URL (replaceState, no reload). A pasted link
// reproduces the sender's view; URL state wins over sessionStorage on load.
//
// Column filters are keyed by position: f{i} for text/select, df{i}/dt{i} for a
// date column's from/to. Sort is sort={col}.{dir}; toggles rfqs/inactive are
// emitted only when on; page only when past 1. Column types are read back from
// the DOM, so they never need to live in the URL.
function saveFilterStateToURL() {
    if (!document.querySelector('tr.filter-row')) return;
    const p = new URLSearchParams();
    getFilterColumns().forEach((c, i) => {
        if (c.type === 'date') {
            if (c.from) p.set('df' + i, c.from);
            if (c.to)   p.set('dt' + i, c.to);
        } else if (c.value) {
            p.set('f' + i, c.value);
        }
    });
    if (sortCol !== null) p.set('sort', sortCol + '.' + sortDir);
    const rfqs     = document.getElementById('show-rfqs');
    const inactive = document.getElementById('show-inactive');
    if (rfqs     && rfqs.checked)     p.set('rfqs', '1');
    if (inactive && inactive.checked) p.set('inactive', '1');
    if (currentPage > 1) p.set('page', String(currentPage));
    const qs = p.toString();
    const url = window.location.pathname + (qs ? '?' + qs : '') + window.location.hash;
    try { history.replaceState(null, '', url); } catch (e) {}
}

// Hydrate from the URL query string. Returns true if any recognized view param
// was present, so the caller can skip the sessionStorage fallback (URL wins).
function restoreFilterStateFromURL() {
    if (!document.querySelector('tr.filter-row')) return false;
    const p = new URLSearchParams(window.location.search);
    let had = false;
    const filters = getFilterColumns().map((c, i) => {
        if (c.type === 'date') {
            const from = p.get('df' + i);
            const to   = p.get('dt' + i);
            if (from !== null || to !== null) had = true;
            return { type: 'date', from: from || '', to: to || '' };
        }
        const v = p.get('f' + i);
        if (v !== null) had = true;
        return { type: 'text', value: v || '' };
    });
    const sortRaw     = p.get('sort');
    const pageRaw     = p.get('page');
    const rfqsRaw     = p.get('rfqs');
    const inactiveRaw = p.get('inactive');
    if (!had && sortRaw === null && pageRaw === null && rfqsRaw === null && inactiveRaw === null) {
        return false;
    }
    let sort = { col: null, dir: 'asc' };
    if (sortRaw !== null) {
        const [colStr, dirStr] = sortRaw.split('.');
        const col = parseInt(colStr, 10);
        if (!Number.isNaN(col)) sort = { col, dir: dirStr === 'desc' ? 'desc' : 'asc' };
    }
    // On a shared link the toggles default off unless their param is present,
    // so the receiver's view matches the sender's exactly.
    _applyState({
        filters,
        sort,
        showRFQs:     rfqsRaw     !== null,
        showInactive: inactiveRaw !== null,
        page: pageRaw ? (parseInt(pageRaw, 10) || 1) : 1,
    });
    return true;
}

function escHtml(s) {
    if (s == null) return '';
    return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

// One row HTML builder per endpoint. Dates arrive pre-formatted "YYYY-MM-DD" or "".
const ROW_BUILDERS = {
    '/api/parts/rows': r => `<tr${r.active === false ? ' class="row-inactive"' : ''}>
        <td data-col="col-pn">${r.thumb
            ? `<span class="pn-thumb" data-thumb="${escHtml(r.thumb)}"><a href="/part/${r.id}" class="part-number-link">${escHtml(r.pn)}</a></span>`
            : `<a href="/part/${r.id}" class="part-number-link">${escHtml(r.pn)}</a>`}${r.active === false ? ' <span class="badge bg-secondary ms-1">Inactive</span>' : ''}${r.belowMin ? ' <span class="badge bg-warning text-dark ms-1" title="Stock on hand is below the reorder minimum">Below Min</span>' : ''}</td>
        <td data-col="col-rev">${escHtml(r.rev)}</td>
        <td data-col="col-title">${escHtml(r.description)}</td>
        <td data-col="col-detail">${escHtml(r.detail)}</td>
        <td data-col="col-reqby">${escHtml(r.reqBy)}</td>
        <td data-col="col-date">${r.date || 'N/A'}</td>
        <td data-col="col-cat">${escHtml(r.cat)}</td>
        <td data-col="col-modified">${r.modified || 'N/A'}</td>
        <td data-col="col-attach" class="text-end">${r.attach}</td>
        <td data-col="col-polines" class="text-end">${r.poLines}</td>
    </tr>`,

    '/api/suppliers/rows': r => `<tr${r.active === false ? ' class="row-inactive"' : ''}>
        <td><a href="/supplier/${r.id}" class="part-number-link">${escHtml(r.name)}</a></td>
        <td>${r.active ? 'Active' : 'Inactive'}</td>
        <td>${escHtml(r.country)}</td>
        <td>${r.links}</td>
        <td>${r.pos}</td>
        <td>${escHtml(r.contact)}</td>
        <td>${escHtml(r.code)}</td>
    </tr>`,

    '/api/contacts/rows': r => `<tr>
        <td>${r.suid ? `<a href="/supplier/${r.suid}" class="part-number-link">${escHtml(r.supplier)}</a>` : escHtml(r.supplier)}</td>
        <td><a href="/contact/${r.id}" class="part-number-link">${escHtml(r.name)}</a></td>
        <td>${escHtml(r.email)}</td>
        <td>${escHtml(r.country)}</td>
        <td>${escHtml(r.state)}</td>
        <td>${escHtml(r.city)}</td>
        <td>${escHtml(r.phone)}</td>
        <td>${escHtml(r.web)}</td>
        <td>${r.modified || 'N/A'}</td>
        <td>${escHtml(r.notes)}</td>
        <td>${r.active ? 'Yes' : 'No'}</td>
    </tr>`,

    '/api/pos/rows': r => {
        const vendor = r.sid
            ? `<a href="/supplier/${r.sid}" class="part-number-link">${escHtml(r.supplier)}</a>`
            : escHtml(r.supplier);
        return `<tr>
            <td><a href="/po/${escHtml(r.num)}" class="part-number-link">${escHtml(r.num)}</a></td>
            <td>${PO_STATUS_BADGES[r.status] || `<span class="badge bg-secondary">${escHtml(r.status)}</span>`}</td>
            <td>${vendor}</td>
            <td>${r.ordered || '—'}</td>
            <td>${r.closed  || '—'}</td>
            <td>${escHtml(r.orderer)}</td>
            <td style="text-align:right;">$${r.cost.toFixed(2)}</td>
        </tr>`;
    },
};

// Per-column text for filter matching — column order must match the thead.
const CELL_TEXT = {
    '/api/parts/rows':     r => [r.pn, r.rev, r.description, r.detail, r.reqBy, r.date, r.cat, r.modified, String(r.attach), String(r.poLines)],
    '/api/suppliers/rows': r => [r.name, r.active ? 'active' : 'inactive', r.country, String(r.links), String(r.pos), r.contact, r.code],
    '/api/contacts/rows':  r => [r.supplier, r.name, r.email, r.country, r.state, r.city, r.phone, r.web, r.modified, r.notes, r.active ? 'yes' : 'no'],
    '/api/pos/rows':       r => [r.num, r.status, r.supplier, r.ordered, r.closed, r.orderer, String(r.cost)],
};

// Status badge markup shared by the TR records row builder (mirrors the badges
// records_index.html used to render server-side).
const TR_STATUS_BADGE = {
    approved: '<span class="badge bg-success"><i class="bi bi-patch-check-fill"></i> Approved</span>',
    complete: '<span class="badge bg-secondary"><i class="bi bi-check2-circle"></i> Complete</span>',
    wip:      '<span class="badge bg-info text-dark">WIP</span>',
};

// Pattern-keyed row builders, for endpoints whose URL varies (e.g. embeds an id).
// Checked when an exact ROW_BUILDERS/CELL_TEXT key isn't found.
const ROW_BUILDER_PATTERNS = [
    {
        // /api/forms/{id}/records/rows — column order: select, sn, pn, desc, date, type, status, form-rev.
        // data-col attributes match records_index.html so the #386 column-visibility toggle keeps working.
        test: /\/api\/forms\/\d+\/records\/rows$/,
        build: r => {
            const pn = r.pnId
                ? `<a href="/part/${r.pnId}">${escHtml(r.snPN)}</a>`
                : escHtml(r.snPN);
            return `<tr>
                <td data-col="col-select"><input type="checkbox" class="row-select" value="${r.id}"></td>
                <td data-col="col-sn"><a href="/records/${r.id}" class="fw-semibold">${escHtml(r.sn)}</a></td>
                <td data-col="col-pn">${pn}</td>
                <td data-col="col-desc">${escHtml(r.snDesc)}</td>
                <td data-col="col-date" class="text-nowrap">${r.date || ''}</td>
                <td data-col="col-type">${escHtml(r.type)}</td>
                <td data-col="col-status" class="text-center">${TR_STATUS_BADGE[r.status] || ''}</td>
                <td data-col="col-form-rev" class="text-center">${escHtml(r.formRev)}</td>
            </tr>`;
        },
        cellText: r => ['', r.sn, r.snPN, r.snDesc, r.date, r.type, r.status, r.formRev],
    },
    {
        // /api/part/{id}/records/rows, /api/part/{id}/lots/{lotID}/records/rows,
        // /api/part/{id}/units/{unitID}/records/rows — #875. Same column shape as
        // the per-form records table, minus the select checkbox (read-only,
        // cross-form view), plus a trailing Form column (records span multiple
        // forms in these scoped views).
        test: /\/api\/part\/\d+\/(?:lots\/\d+\/|units\/\d+\/)?records\/rows$/,
        build: r => {
            const pn = r.pnId
                ? `<a href="/part/${r.pnId}">${escHtml(r.snPN)}</a>`
                : escHtml(r.snPN);
            return `<tr>
                <td data-col="col-sn"><a href="/records/${r.id}" class="fw-semibold">${escHtml(r.sn)}</a></td>
                <td data-col="col-pn">${pn}</td>
                <td data-col="col-desc">${escHtml(r.snDesc)}</td>
                <td data-col="col-date" class="text-nowrap">${r.date || ''}</td>
                <td data-col="col-type">${escHtml(r.type)}</td>
                <td data-col="col-status" class="text-center">${TR_STATUS_BADGE[r.status] || ''}</td>
                <td data-col="col-form-rev" class="text-center">${escHtml(r.formRev)}</td>
                <td data-col="col-form"><a href="/forms/${r.formId}/records">${escHtml(r.formLabel)}</a></td>
            </tr>`;
        },
        cellText: r => [r.sn, r.snPN, r.snDesc, r.date, r.type, r.status, r.formRev, r.formLabel],
    },
];

function resolveRowConfig(url) {
    if (ROW_BUILDERS[url]) return { build: ROW_BUILDERS[url], cellText: CELL_TEXT[url] };
    const match = ROW_BUILDER_PATTERNS.find(p => p.test.test(url));
    return match ? { build: match.build, cellText: match.cellText } : null;
}

// One descriptor per filter-row <th>, in column order. A column with no
// filter control (e.g. a select/checkbox column) reads as an inert text
// filter. Read from the th itself (not its inputs) so columns with zero,
// one, or two controls each still occupy exactly one positional slot.
// Compiles a `*`/`?` glob into a case-insensitive, whole-field RegExp.
// Returns null when the trimmed value has no wildcard chars, so callers
// fall back to the existing substring `includes` behavior unchanged (#51).
function compileGlob(raw) {
    const v = (raw || '').trim();
    if (!v || !/[*?]/.test(v)) return null;
    const pattern = v
        .replace(/[.+^${}()|[\]\\]/g, '\\$&') // escape regex specials (not * or ?)
        .replace(/\*/g, '.*')
        .replace(/\?/g, '.');
    return new RegExp('^' + pattern + '$', 'i');
}

function getFilterColumns() {
    return Array.from(document.querySelectorAll('tr.filter-row th')).map(th => {
        if (th.dataset.filterType === 'date') {
            const from = th.querySelector('.date-filter-from');
            const to   = th.querySelector('.date-filter-to');
            return { type: 'date', from: from ? from.value : '', to: to ? to.value : '' };
        }
        const input = th.querySelector('input, select');
        const raw = input ? input.value : '';
        return { type: 'text', value: raw, regex: compileGlob(raw) };
    });
}

function matchesRow(row, cols) {
    return cols.every((c, i) => {
        const text = row._text[i] || '';
        if (c.type === 'date') {
            if (!c.from && !c.to) return true;
            const d = text.slice(0, 10); // ISO date prefix
            if (!d) return false;
            if (c.from && d < c.from) return false;
            if (c.to && d > c.to) return false;
            return true;
        }
        if (c.regex) return c.regex.test(text);
        const v = (c.value || '').toLowerCase().trim();
        return !v || text.includes(v);
    });
}

// Engine-rendered date-range popover for any filter-row <th data-filter-type="date">.
// Templates only mark the column; the engine supplies the control.
function initDateFilters() {
    document.querySelectorAll('tr.filter-row th[data-filter-type="date"]').forEach(th => {
        th.innerHTML = `
            <div class="dropdown date-filter">
                <button type="button" class="btn btn-sm btn-outline-secondary date-filter-btn" data-bs-toggle="dropdown" data-bs-auto-close="outside" aria-label="Date range filter" title="Filter by date range">
                    <i class="bi bi-calendar-range"></i>
                </button>
                <div class="dropdown-menu p-3" style="min-width:230px">
                    <label class="form-label mb-1 small text-muted">From</label>
                    <input type="date" class="form-control form-control-sm mb-2 date-filter-from">
                    <label class="form-label mb-1 small text-muted">To</label>
                    <input type="date" class="form-control form-control-sm mb-3 date-filter-to">
                    <div class="d-flex gap-2">
                        <button type="button" class="btn btn-sm btn-outline-secondary date-filter-clear">Clear</button>
                    </div>
                </div>
            </div>`;
        const from  = th.querySelector('.date-filter-from');
        const to    = th.querySelector('.date-filter-to');
        const clear = th.querySelector('.date-filter-clear');
        const onChange = () => { updateDateFilterButton(th); applyFilters(true); };
        from.addEventListener('change', onChange);
        to.addEventListener('change', onChange);
        clear.addEventListener('click', () => { from.value = ''; to.value = ''; onChange(); });
    });
    // The date-filter dropdown toggle is created above, after the shared
    // DOMContentLoaded listener that applies Popper's 'fixed' strategy to
    // dropdowns already in the DOM — apply it here too so this toggle isn't
    // clipped by .table-wrapper's computed overflow-y (#52).
    useFixedDropdownStrategy();
}

function updateDateFilterButton(th) {
    const btn  = th.querySelector('.date-filter-btn');
    const from = th.querySelector('.date-filter-from');
    const to   = th.querySelector('.date-filter-to');
    if (!btn) return;
    const active = (from && from.value) || (to && to.value);
    btn.classList.toggle('btn-primary', !!active);
    btn.classList.toggle('btn-outline-secondary', !active);
}

function applySort() {
    if (sortCol === null) return;
    allRows.sort((a, b) => {
        const va = a._text[sortCol] || '';
        const vb = b._text[sortCol] || '';
        const cmp = va.localeCompare(vb, undefined, { numeric: true, sensitivity: 'base' });
        return sortDir === 'asc' ? cmp : -cmp;
    });
}

function updateSortHeaders() {
    (activeTable || document).querySelectorAll('thead tr:first-child th').forEach((th, i) => {
        th.classList.remove('sort-asc', 'sort-desc');
        if (i === sortCol) th.classList.add(sortDir === 'asc' ? 'sort-asc' : 'sort-desc');
    });
}

function sortByCol(colIndex) {
    if (sortCol === colIndex) {
        sortDir = sortDir === 'asc' ? 'desc' : 'asc';
    } else {
        sortCol = colIndex;
        sortDir = 'asc';
    }
    applySort();
    updateSortHeaders();
    currentPage = 1;
    applyFilters(false);
}

function applyFilters(resetPage = true) {
    if (!document.querySelector('tr.filter-row')) return;
    if (resetPage) currentPage = 1;
    let rows = allRows.filter(r => matchesRow(r, getFilterColumns()));
    // PO list only: hide RFQ quotes (any row in an RFQ group) unless the
    // "Show RFQs" switch is on. Converted POs have no group id and always show.
    const showRFQs = document.getElementById('show-rfqs');
    if (showRFQs && !showRFQs.checked) {
        rows = rows.filter(r => r.gid == null);
    }
    // Parts/Vendors lists: hide soft-deleted (inactive) rows unless the
    // "Show inactive" switch is on. Endpoints without an active flag are kept.
    const showInactive = document.getElementById('show-inactive');
    if (showInactive && !showInactive.checked) {
        rows = rows.filter(r => r.active !== false);
    }
    renderRows(rows);
    saveFilterState();
    saveFilterStateToURL();
}

function applyTruncationTooltips(tbody) {
    tbody.querySelectorAll('td').forEach(td => {
        if (td.scrollWidth > td.clientWidth) {
            td.title = td.textContent.trim();
        } else {
            td.removeAttribute('title');
        }
    });
}

function renderRows(rowsToShow) {
    const tbody = (activeTable || document).querySelector('tbody');
    if (!tbody) return;

    // Full filtered+sorted set (pre-pagination), exposed for callers that need
    // to act across all matching rows rather than just the visible page (e.g.
    // a "select all" that should reach rows on other pages).
    window.currentFilteredRows = rowsToShow;

    const totalPages = Math.max(1, Math.ceil(rowsToShow.length / ROWS_PER_PAGE));
    if (currentPage > totalPages) currentPage = totalPages;

    const start = (currentPage - 1) * ROWS_PER_PAGE;
    const end   = start + ROWS_PER_PAGE;

    tbody.innerHTML = rowsToShow.slice(start, end).map(r => r._html).join('');
    applyTruncationTooltips(tbody);

    const count = rowsToShow.length;
    const pi = document.querySelector('.pagination-info');
    const rc = document.querySelector('.record-count');
    if (rc) rc.textContent = count;
    if (pi) pi.textContent = count === 0
        ? 'No results'
        : `Showing ${start + 1}–${Math.min(end, count)} of ${count} (Page ${currentPage} of ${totalPages})`;

    const prev = document.querySelector('.page-nav.prev');
    const next = document.querySelector('.page-nav.next');
    if (prev) prev.disabled = currentPage === 1;
    if (next) next.disabled = currentPage >= totalPages;

    if (window.onRowsRendered) window.onRowsRendered();
}

function prevPage() {
    if (currentPage > 1) { currentPage--; applyFilters(false); }
}

function nextPage() {
    currentPage++;
    applyFilters(false);
}

function loadListRows() {
    const table = document.querySelector('table[data-rows-url]');
    if (!table) return;
    activeTable = table;
    const url = table.dataset.rowsUrl;
    const cfg = resolveRowConfig(url);
    if (!cfg) return;
    const buildRow = cfg.build;
    const cellText = cfg.cellText;

    const t0 = performance.now();
    fetch(url)
        .then(r => {
            if (!r.ok) throw new Error('HTTP ' + r.status);
            return r.json();
        })
        .then(data => {
            const tFetch = performance.now();
            allRows = (data || []).map(item => {
                item._html = buildRow(item);
                item._text = cellText(item).map(s => (s == null ? '' : String(s)).toLowerCase());
                return item;
            });
            table.querySelectorAll('thead tr:first-child th').forEach((th, i) => {
                th.classList.add('sortable');
                th.addEventListener('click', () => sortByCol(i));
            });
            // URL state wins over sessionStorage so a shared link reproduces
            // the sender's exact view; fall back to the saved session otherwise.
            if (!restoreFilterStateFromURL()) restoreFilterState();
            applySort();
            updateSortHeaders();
            applyFilters(false);
            const tDone = performance.now();
            console.log(`[rows] ${url}: fetch=${Math.round(tFetch - t0)}ms  render=${Math.round(tDone - tFetch)}ms  rows=${allRows.length}`);
        })
        .catch(err => {
            const tbody = table.querySelector('tbody');
            const cols  = table.querySelectorAll('thead tr:first-child th').length;
            if (tbody) tbody.innerHTML =
                `<tr><td colspan="${cols}" class="no-results">Error loading data: ${err.message}</td></tr>`;
        });
}

function exportCSV() {
    const table = document.querySelector('table[data-rows-url]');
    if (!table) return;
    const url = table.dataset.rowsUrl;

    // Apply same filters as the current view (all matching rows, not just current page)
    let rows = allRows.filter(r => matchesRow(r, getFilterColumns()));
    const showInactive = document.getElementById('show-inactive');
    if (showInactive && !showInactive.checked) rows = rows.filter(r => r.active !== false);
    const showRFQs = document.getElementById('show-rfqs');
    if (showRFQs && !showRFQs.checked) rows = rows.filter(r => r.gid == null);

    const configs = {
        '/api/parts/rows': {
            filename: 'parts.csv',
            headers: ['Part Number','Revision','Description','Detail','Requested By','Date','Category','Modified','Attachments','PO Lines','Active'],
            row: r => [r.pn, r.rev, r.description, r.detail, r.reqBy, r.date, r.cat, r.modified, r.attach, r.poLines, r.active ? 'true' : 'false'],
        },
        '/api/pos/rows': {
            filename: 'purchase-orders.csv',
            headers: ['PO Number','Status','Supplier','Date Ordered','Date Closed','Orderer','Total Cost'],
            row: r => [r.num, r.status, r.supplier, r.ordered || '', r.closed || '', r.orderer, r.cost.toFixed(2)],
        },
    };

    const cfg = configs[url];
    if (!cfg) return;

    const csvContent = [cfg.headers, ...rows.map(cfg.row)].map(row =>
        row.map(v => { const s = v == null ? '' : String(v); return /[",\n]/.test(s) ? '"' + s.replace(/"/g, '""') + '"' : s; }).join(',')
    ).join('\r\n');

    const a = document.createElement('a');
    a.href = URL.createObjectURL(new Blob([csvContent], { type: 'text/csv' }));
    a.download = cfg.filename;
    a.click();
    URL.revokeObjectURL(a.href);
}

// Dropdown menus (e.g. row-action kebabs) default to Popper's 'absolute'
// strategy, which is clipped by any scrollable ancestor — .table-wrapper's
// overflow-x:auto clips rows near the bottom of a table. 'fixed' strategy
// positions relative to the viewport instead, escaping that clipping.
function useFixedDropdownStrategy(root = document) {
    root.querySelectorAll('[data-bs-toggle="dropdown"]').forEach(el => {
        bootstrap.Dropdown.getOrCreateInstance(el, {
            popperConfig: defaultConfig => Object.assign({}, defaultConfig, { strategy: 'fixed' }),
        });
    });
}
document.addEventListener('DOMContentLoaded', () => useFixedDropdownStrategy());

document.addEventListener('DOMContentLoaded', () => {
    initDateFilters();
    loadListRows();
    // Direct children only — excludes the date-popover's own from/to inputs,
    // which are nested deeper and wire their own listeners in initDateFilters().
    document.querySelectorAll('tr.filter-row > th > input, tr.filter-row > th > select')
        .forEach(i => {
            // input fires on every keystroke (and on select changes), so it
            // fully covers re-filtering. A change listener here is redundant and
            // fires on blur — i.e. exactly when the user clicks a row link —
            // re-rendering the tbody and detaching the in-flight anchor, which
            // swallows the first click (#525).
            i.addEventListener('input', () => applyFilters(true));
        });
    document.getElementById('show-rfqs')?.addEventListener('change', () => applyFilters(true));
    document.getElementById('show-inactive')?.addEventListener('change', () => applyFilters(true));
});

// --- Column visibility toggle (#386) ---
// Dropdowns use data-col-table="tableId"; checkboxes use data-toggle-col="key".
// Hidden state persisted to localStorage as arx.cols.<tableId>.
document.addEventListener('DOMContentLoaded', function () {
  document.querySelectorAll('[data-col-table]').forEach(function (container) {
    var tableId = container.dataset.colTable
    var storageKey = 'arx.cols.' + tableId
    var table = document.getElementById(tableId)
    if (!table) return

    var stored = localStorage.getItem(storageKey)
    var hidden = stored ? JSON.parse(stored) : []

    function applyState() {
      table.querySelectorAll('[data-col]').forEach(function (el) {
        el.classList.remove('col-hidden')
      })
      hidden.forEach(function (key) {
        table.querySelectorAll('[data-col="' + key + '"]').forEach(function (el) {
          el.classList.add('col-hidden')
        })
      })
      container.querySelectorAll('input[data-toggle-col]').forEach(function (cb) {
        cb.checked = hidden.indexOf(cb.dataset.toggleCol) === -1
      })
    }

    container.querySelectorAll('input[data-toggle-col]').forEach(function (cb) {
      cb.addEventListener('change', function () {
        var key = cb.dataset.toggleCol
        if (cb.checked) {
          hidden = hidden.filter(function (k) { return k !== key })
        } else {
          if (hidden.indexOf(key) === -1) hidden.push(key)
        }
        localStorage.setItem(storageKey, JSON.stringify(hidden))
        applyState()
      })
    })

    var resetBtn = container.querySelector('[data-col-reset]')
    if (resetBtn) {
      resetBtn.addEventListener('click', function (e) {
        e.preventDefault()
        hidden = []
        localStorage.removeItem(storageKey)
        applyState()
      })
    }

    // Exposed so pages whose rows render asynchronously (e.g. the shared
    // API-driven table) can re-apply hidden columns after each render.
    window.__trColState = window.__trColState || {}
    window.__trColState[tableId] = applyState

    applyState()
  })
})

// --- BOM expand/collapse (#579) ---
// Assembly BOM rows with a sub-BOM get a caret toggle that lazily fetches and
// inserts the child part's own BOM lines directly after the row, indented and
// numbered "{parentItem}.{childIndex}". The same toggle applies recursively to
// any inserted row whose ChildHasBOM is true, so nesting depth is unbounded.

function bomSourceBadge(source) {
    return BOM_SOURCE_BADGES[source] || ''
}

// Mirrors the server's `{{printf "%.5g" .PLQty}}` formatting.
function formatBOMQty(v) {
    if (v == null) return ''
    var s = Number(v).toPrecision(5)
    if (s.indexOf('e') === -1 && s.indexOf('.') !== -1) {
        s = s.replace(/0+$/, '').replace(/\.$/, '')
    }
    return s
}

function formatBOMCost(v) {
    return v ? '$' + v.toFixed(4) : ''
}

function buildBOMSubRow(item, num) {
    var depth = (num.match(/\./g) || []).length
    var tr = document.createElement('tr')
    tr.className = 'bom-sub-row'
    tr.dataset.item = num
    var toggle = item.ChildHasBOM
        ? '<button type="button" class="bom-expand-toggle" data-part-id="' + item.ComponentPartID + '" data-item="' + num + '" aria-expanded="false" onclick="toggleBOMRow(this)">&#9656;</button> '
        : ''
    tr.innerHTML =
        '<td data-col="col-item" style="--bom-depth:' + depth + '">' + toggle + num + '</td>' +
        '<td data-col="col-pn"><a href="/part/' + item.ComponentPartID + '" class="part-number-link">' + escHtml(item.PartNumber) + '</a></td>' +
        '<td data-col="col-title">' + escHtml(item.Description) + '</td>' +
        '<td data-col="col-rev">' + escHtml(item.Revision) + '</td>' +
        '<td data-col="col-cat">' + escHtml(item.Category) + '</td>' +
        '<td data-col="col-qty" class="text-end">' + formatBOMQty(item.Qty) + '</td>' +
        '<td data-col="col-unit-cost" class="text-end">' + formatBOMCost(item.LineUnitCost) + '</td>' +
        '<td data-col="col-ext-cost" class="text-end">' + formatBOMCost(item.LineExtCost) + '</td>' +
        '<td data-col="col-source">' + bomSourceBadge(item.CostSource) + '</td>' +
        '<td data-col="col-attach" class="text-end">' + item.AttachCount + '</td>' +
        '<td data-col="col-polines" class="text-end">' + item.POLineCount + '</td>'
    return tr
}

function insertBOMChildren(parentTr, parentNum, children) {
    var insertAfter = parentTr
    children.forEach(function (child, i) {
        var row = buildBOMSubRow(child, parentNum + '.' + (i + 1))
        insertAfter.insertAdjacentElement('afterend', row)
        insertAfter = row
    })
    if (window.__trColState && window.__trColState['bom-table']) window.__trColState['bom-table']()
}

// Resolves true on success, false on failure (never rejects) — leaves the
// toggle in its original collapsed state on failure so the user can retry,
// and so expandAllBOM's loop below can detect and stop on failure instead of
// retrying the same row forever.
function expandBOMRow(btn) {
    var tr = btn.closest('tr')
    var itemNum = tr.dataset.item
    return fetch('/api/part/' + btn.dataset.partId + '/bom-children')
        .then(function (r) {
            if (!r.ok) throw new Error('HTTP ' + r.status)
            return r.json()
        })
        .then(function (children) {
            insertBOMChildren(tr, itemNum, children || [])
            btn.setAttribute('aria-expanded', 'true')
            btn.innerHTML = '&#9662;'
            return true
        })
        .catch(function (err) {
            console.error('Failed to load BOM children:', err)
            return false
        })
}

function collapseBOMRow(btn) {
    var tr = btn.closest('tr')
    var table = tr.closest('table')
    var prefix = tr.dataset.item + '.'
    table.querySelectorAll('tbody tr[data-item]').forEach(function (row) {
        if (row.dataset.item.indexOf(prefix) === 0) row.remove()
    })
    btn.setAttribute('aria-expanded', 'false')
    btn.innerHTML = '&#9656;'
}

function toggleBOMRow(btn) {
    if (btn.getAttribute('aria-expanded') === 'true') {
        collapseBOMRow(btn)
    } else {
        expandBOMRow(btn)
    }
}

async function expandAllBOM() {
    var table = document.getElementById('bom-table')
    if (!table) return
    var MAX_LEVELS = 25 // guards against a circular BOM reference hanging the browser
    var toggles, level = 0
    while ((toggles = Array.from(table.querySelectorAll('.bom-expand-toggle[aria-expanded="false"]'))).length) {
        if (++level > MAX_LEVELS) {
            console.error('Expand All: stopped after ' + MAX_LEVELS + ' levels — check for a circular BOM reference.')
            return
        }
        for (var i = 0; i < toggles.length; i++) {
            if (!(await expandBOMRow(toggles[i]))) return
        }
    }
}

function collapseAllBOM() {
    var table = document.getElementById('bom-table')
    if (!table) return
    table.querySelectorAll('.bom-sub-row').forEach(function (row) { row.remove() })
    table.querySelectorAll('.bom-expand-toggle[aria-expanded="true"]').forEach(function (btn) {
        btn.setAttribute('aria-expanded', 'false')
        btn.innerHTML = '&#9656;'
    })
}

// Hover preview for the /parts part-number thumbnail (#696). The parts table cells
// use overflow:hidden and the wrapper scrolls, which clip an in-cell tooltip — so
// this uses a single position:fixed element appended to <body> that escapes the
// clipping, positioned by JS relative to the hovered part number. Only activates
// for .pn-thumb elements, so it's inert on pages without them.
(function () {
    var tip, tipImg
    function ensureTip() {
        if (tip) return
        tip = document.createElement('div')
        tip.className = 'pn-thumb-tip hover-preview-box'
        tipImg = document.createElement('img')
        tipImg.alt = 'preview'
        tip.appendChild(tipImg)
        document.body.appendChild(tip)
    }
    document.addEventListener('mouseover', function (e) {
        var wrap = e.target.closest ? e.target.closest('.pn-thumb') : null
        if (!wrap) return
        ensureTip()
        if (tipImg.getAttribute('src') !== wrap.dataset.thumb) { tipImg.setAttribute('src', wrap.dataset.thumb) }
        var r = wrap.getBoundingClientRect()
        var above = r.top > 260
        tip.style.left = r.left + 'px'
        tip.style.top = (above ? r.top - 8 : r.bottom + 8) + 'px'
        tip.style.transform = above ? 'translateY(-100%)' : 'none'
        tip.classList.add('show')
    })
    document.addEventListener('mouseout', function (e) {
        var wrap = e.target.closest ? e.target.closest('.pn-thumb') : null
        if (!wrap) return
        if (e.relatedTarget && wrap.contains(e.relatedTarget)) return
        if (tip) tip.classList.remove('show')
    })
})()
