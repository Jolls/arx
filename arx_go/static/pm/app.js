/* =============================================================
   Arx Parts Master — application JavaScript
   ============================================================= */

const ROWS_PER_PAGE = 20;
let currentPage = 1;
let allRows = [];
let sortCol = null;
let sortDir = 'asc';

function escHtml(s) {
    if (s == null) return '';
    return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

// One row HTML builder per endpoint. Dates arrive pre-formatted "YYYY-MM-DD" or "".
const ROW_BUILDERS = {
    '/api/parts/rows': r => `<tr>
        <td><a href="/part/${r.id}" class="part-number-link">${escHtml(r.pn)}</a></td>
        <td>${escHtml(r.rev)}</td>
        <td>${escHtml(r.title)}</td>
        <td>${escHtml(r.detail)}</td>
        <td>${escHtml(r.reqBy)}</td>
        <td>${r.date || 'N/A'}</td>
        <td>${escHtml(r.cat)}</td>
        <td>${r.modified || 'N/A'}</td>
    </tr>`,

    '/api/suppliers/rows': r => `<tr>
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
        const badges = {
            pending:   '<span class="badge badge-info">Pending</span>',
            placed:    '<span class="badge badge-active">Placed</span>',
            on_hold:   '<span class="badge badge-active">On Hold</span>',
            complete:  '<span class="badge badge-neutral">Complete</span>',
            cancelled: '<span class="badge badge-inactive">Cancelled</span>',
        };
        const vendor = r.sid
            ? `<a href="/supplier/${r.sid}" class="part-number-link">${escHtml(r.supplier)}</a>`
            : escHtml(r.supplier);
        return `<tr>
            <td><a href="/po/${escHtml(r.num)}" class="part-number-link">${escHtml(r.num)}</a></td>
            <td>${badges[r.status] || `<span class="badge badge-neutral">${escHtml(r.status)}</span>`}</td>
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
    '/api/parts/rows':     r => [r.pn, r.rev, r.title, r.detail, r.reqBy, r.date, r.cat, r.modified],
    '/api/suppliers/rows': r => [r.name, r.active ? 'active' : 'inactive', r.country, String(r.links), String(r.pos), r.contact, r.code],
    '/api/contacts/rows':  r => [r.supplier, r.name, r.email, r.country, r.state, r.city, r.phone, r.web, r.modified, r.notes, r.active ? 'yes' : 'no'],
    '/api/pos/rows':       r => [r.num, r.status, r.supplier, r.ordered, r.closed, r.orderer, String(r.cost)],
};

function getFilterValues() {
    return Array.from(document.querySelectorAll('tr.filter-row input'))
        .map(i => i.value.toLowerCase().trim());
}

function matchesRow(row, filters) {
    return filters.every((f, i) => !f || (row._text[i] || '').includes(f));
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
    document.querySelectorAll('thead tr:first-child th').forEach((th, i) => {
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
    renderRows(allRows.filter(r => matchesRow(r, getFilterValues())));
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
    const tbody = document.querySelector('tbody');
    if (!tbody) return;

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
    const url = table.dataset.rowsUrl;
    const buildRow = ROW_BUILDERS[url];
    const cellText = CELL_TEXT[url];
    if (!buildRow || !cellText) return;

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
            document.querySelectorAll('thead tr:first-child th').forEach((th, i) => {
                th.classList.add('sortable');
                th.addEventListener('click', () => sortByCol(i));
            });
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

document.addEventListener('DOMContentLoaded', () => {
    loadListRows();
    document.querySelectorAll('tr.filter-row input')
        .forEach(i => i.addEventListener('input', () => applyFilters(true)));
});
