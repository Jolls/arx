// Shared client-side column sort for server-rendered tables.
// Headers wire up via onclick="sortTable(this)" (PM and TR templates).
function sortTable(th) {
    if (typeof collapseAllBOM === 'function') collapseAllBOM();
    var tr = th.closest('tr');
    var table = th.closest('table');
    var tbody = table.querySelector('tbody');
    var ths = Array.from(tr.querySelectorAll('th.sortable'));
    var col = Array.from(tr.querySelectorAll('th')).indexOf(th);
    var asc = !th.classList.contains('sort-asc');
    ths.forEach(function(h) { h.classList.remove('sort-asc', 'sort-desc'); });
    th.classList.add(asc ? 'sort-asc' : 'sort-desc');
    var rows = Array.from(tbody.querySelectorAll('tr'));
    rows.sort(function(a, b) {
        var aText = (a.cells[col] ? a.cells[col].textContent.trim() : '');
        var bText = (b.cells[col] ? b.cells[col].textContent.trim() : '');
        var aNum = parseFloat(aText.replace(/[^0-9.\-]/g, ''));
        var bNum = parseFloat(bText.replace(/[^0-9.\-]/g, ''));
        if (!isNaN(aNum) && !isNaN(bNum)) return asc ? aNum - bNum : bNum - aNum;
        return asc ? aText.localeCompare(bText) : bText.localeCompare(aText);
    });
    rows.forEach(function(row) { tbody.appendChild(row); });
    if (window.__trRepaginate) window.__trRepaginate();
}

// Shared "copy a server-rendered table to the clipboard as TSV" helper (PM and TR
// templates) — pastes cleanly into Excel. header is the array of column names;
// cellFn(cells, i), if given, overrides the default per-cell text extraction
// (e.g. to read a data-* attribute instead of textContent, or skip filtered rows).
function copyTableTSV(tableId, btnId, header, cellFn) {
    var btn = document.getElementById(btnId);
    if (!btn) return;
    var defaultLabel = btn.innerHTML;
    btn.addEventListener('click', function() {
        var lines = [header.join('\t')];
        var tbody = document.querySelector('#' + tableId + ' tbody');
        if (tbody) {
            tbody.querySelectorAll('tr').forEach(function(tr) {
                if (tr.style.display === 'none') return;
                var cells = tr.querySelectorAll('td');
                var out = [];
                for (var i = 0; i < header.length; i++) {
                    out.push(cellFn ? cellFn(tr, cells, i) : (cells[i] ? cells[i].textContent.trim() : ''));
                }
                lines.push(out.join('\t'));
            });
        }
        navigator.clipboard.writeText(lines.join('\n')).then(function() {
            btn.innerHTML = '<i class="bi bi-check-lg"></i> Copied!';
            setTimeout(function() { btn.innerHTML = defaultLabel; }, 2000);
        }).catch(function() {
            btn.textContent = 'Copy failed';
            setTimeout(function() { btn.innerHTML = defaultLabel; }, 2000);
        });
    });
}
