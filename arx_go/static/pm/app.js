/* =============================================================
   Arx Parts Master — application JavaScript
   Filter/pagination logic for list pages lives here.
   ============================================================= */

const ROWS_PER_PAGE = 20;
let currentPage = 1;
let allRows = [];

// Read cell text from the DOM once at page load and cache it on each row
// object. Filtering then works against plain JS strings — no DOM reads
// per keystroke.
function initRows() {
    allRows = Array.from(document.querySelectorAll('tbody tr'));
    allRows.forEach(row => {
        row._cellText = Array.from(row.querySelectorAll('td'))
            .map(td => td.textContent.toLowerCase());
    });
}

function getFilterValues() {
    return Array.from(document.querySelectorAll('tr.filter-row input'))
        .map(i => i.value.toLowerCase().trim());
}

function matchesRow(row, filters) {
    const cells = row._cellText || [];
    return filters.every((f, idx) => !f || (cells[idx] || '').includes(f));
}

function applyFilters(resetPage = true) {
    if (!document.querySelector('tr.filter-row')) return;
    if (resetPage) currentPage = 1;
    renderRows(allRows.filter(r => matchesRow(r, getFilterValues())));
}

function renderRows(rowsToShow) {
    const tbody = document.querySelector('tbody');
    if (!tbody) return;

    const totalPages = Math.max(1, Math.ceil(rowsToShow.length / ROWS_PER_PAGE));
    if (currentPage > totalPages) currentPage = totalPages;

    const start = (currentPage - 1) * ROWS_PER_PAGE;
    const end   = start + ROWS_PER_PAGE;

    // Swap only the visible page's rows into the DOM — no show/hide loop.
    // Rows not on this page stay in the allRows array but not in the document.
    tbody.replaceChildren(...rowsToShow.slice(start, end));

    const count = rowsToShow.length;
    const pi = document.querySelector('.pagination-info');
    const rc = document.querySelector('.record-count');
    if (rc) rc.textContent = count;
    if (pi) pi.textContent = count === 0
        ? 'No results'
        : `Showing ${start + 1}-${Math.min(end, count)} of ${count} results (Page ${currentPage} of ${totalPages})`;

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

function clearFilters() {
    currentPage = 1;
    document.querySelectorAll('tr.filter-row input').forEach(i => i.value = '');
    applyFilters(true);
}

document.addEventListener('DOMContentLoaded', () => {
    if (document.querySelector('tr.filter-row')) {
        initRows();
        applyFilters(false);
        document.querySelectorAll('tr.filter-row input')
            .forEach(i => i.addEventListener('input', () => applyFilters(true)));
    }
});
