// Shared client-side column sort for server-rendered tables.
// Headers wire up via onclick="sortTable(this)" (PM and TR templates).
function sortTable(th) {
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
