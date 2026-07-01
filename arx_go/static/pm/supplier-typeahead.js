// Shared supplier search-as-you-type input, used on forms with a single
// supplier field (Add/Edit Price, Contact, Sourcing, Settings). PO/RFQ forms
// use their own copy in po_edit.html since they also drive a linked contact
// dropdown.

function makeSupplierDropdown(inputEl, minWidth) {
    const dd = document.createElement('div');
    dd.className = 'search-dropdown';
    const rect = inputEl.getBoundingClientRect();
    dd.style.top   = (rect.bottom + 2) + 'px';
    dd.style.left  = rect.left + 'px';
    dd.style.width = Math.max(rect.width, minWidth) + 'px';
    document.body.appendChild(dd);
    return dd;
}

function setupSupplierTypeahead(inputEl, idHiddenEl, opts) {
    let dd = null, activeIdx = -1, timer = null, results = [];
    const supplierOnly = opts && opts.supplierOnly;

    inputEl.addEventListener('input', function() {
        clearTimeout(timer);
        idHiddenEl.value = '';
        inputEl.classList.remove('is-invalid');
        const q = this.value.trim();
        if (q.length < 2) { close(); return; }
        timer = setTimeout(() => {
            let url = '/api/suppliers/search?q=' + encodeURIComponent(q);
            if (supplierOnly) url += '&supplier_only=1';
            fetch(url).then(r => r.json()).then(show);
        }, 280);
    });
    inputEl.addEventListener('keydown', e => {
        if (!dd) return;
        if (e.key === 'ArrowDown') { e.preventDefault(); activeIdx = Math.min(activeIdx+1, results.length-1); renderActive(); }
        else if (e.key === 'ArrowUp') { e.preventDefault(); activeIdx = Math.max(activeIdx-1, 0); renderActive(); }
        else if (e.key === 'Enter' && activeIdx >= 0) { e.preventDefault(); select(results[activeIdx]); }
        else if (e.key === 'Escape') close();
    });
    inputEl.addEventListener('blur', () => setTimeout(() => {
        close();
        inputEl.classList.toggle('is-invalid', inputEl.value.trim() !== '' && idHiddenEl.value === '');
    }, 200));

    function show(suppliers) {
        close();
        if (!suppliers.length) return;
        results = suppliers; activeIdx = -1;
        dd = makeSupplierDropdown(inputEl, 280);
        suppliers.forEach((s, i) => {
            const item = document.createElement('div');
            item.className = 'search-dropdown-item';
            item.innerHTML = '<strong>' + escHtml(s.name) + '</strong>' + (s.city ? ' &mdash; ' + escHtml(s.city) : '');
            item.addEventListener('mousedown', e => { e.preventDefault(); select(s); });
            dd.appendChild(item);
        });
    }
    function renderActive() {
        if (!dd) return;
        Array.from(dd.children).forEach((el, i) => el.classList.toggle('active', i === activeIdx));
    }
    function select(s) {
        inputEl.value = s.name; idHiddenEl.value = s.id;
        inputEl.classList.remove('is-invalid');
        close();
        if (opts && opts.onSelect) opts.onSelect(s);
    }
    function close() { dd?.remove(); dd = null; results = []; activeIdx = -1; }
}
