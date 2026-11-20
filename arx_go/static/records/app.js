// --- Archived step rows toggle (#403) ---
// A [data-archived-toggle] switch reveals rows marked .step-archived in the table named by
// its data-archived-table attribute. Used on both the definition view and its editor.
document.addEventListener('DOMContentLoaded', function () {
  document.querySelectorAll('[data-archived-toggle]').forEach(function (toggle) {
    var table = document.getElementById(toggle.dataset.archivedTable)
    if (!table) return
    toggle.addEventListener('change', function () {
      table.classList.toggle('show-archived', toggle.checked)
    })
  })
})

// --- Collapsible record headers (#509) ---
// .row-heading-N rows (type 1/2/3 steps) collapse every row that follows,
// including nested headers, until the next header whose level is the same
// or shallower (data-level <= this row's level).
document.addEventListener('DOMContentLoaded', function () {
  function recompute(tbody) {
    var active = []
    Array.from(tbody.children).forEach(function (row) {
      var level = parseInt(row.dataset.level || '0', 10)
      if (level > 0) {
        while (active.length && level <= active[active.length - 1]) active.pop()
        row.classList.toggle('row-collapsed', active.length > 0)
        if (row.classList.contains('row-collapsed-self')) active.push(level)
      } else {
        row.classList.toggle('row-collapsed', active.length > 0)
      }
    })
  }

  document.querySelectorAll('.row-collapsible').forEach(function (row) {
    var tbody = row.closest('tbody')
    row.addEventListener('click', function () {
      row.classList.toggle('row-collapsed-self')
      recompute(tbody)
    })
  })

  document.querySelectorAll('tbody').forEach(function (tbody) {
    if (tbody.querySelector('.row-collapsible')) recompute(tbody)
  })
})

// --- Client-side pagination for server-rendered list tables ---
// Operates on tables marked with data-paginate; shows 30 rows per page by
// toggling display, and re-applies after sortTable() reorders the rows.
;(function () {
  var ROWS_PER_PAGE = 30
  var currentPage = 1

  function dataRows(tbody) {
    // Real rows only — skip the empty-state row (single td with colspan) and any
    // rows hidden by the client-side column filters (data-filtered-out).
    return Array.from(tbody.children).filter(function (tr) {
      return !tr.querySelector('td[colspan]') && tr.dataset.filteredOut !== '1'
    })
  }

  function render(table) {
    var tbody = table.querySelector('tbody')
    if (!tbody) return
    var rows = dataRows(tbody)
    var controls = (table.closest('.table-responsive-sm') || table).nextElementSibling
    var hasControls = controls && controls.matches('nav[aria-label="Pagination"]')

    var rc = document.querySelector('.record-count')
    if (rc) rc.textContent = rows.length

    if (rows.length <= ROWS_PER_PAGE) {
      rows.forEach(function (tr) { tr.style.display = '' })
      if (hasControls) controls.style.display = 'none'
      return
    }
    if (hasControls) controls.style.display = ''

    var totalPages = Math.ceil(rows.length / ROWS_PER_PAGE)
    if (currentPage > totalPages) currentPage = totalPages
    var start = (currentPage - 1) * ROWS_PER_PAGE
    var end = start + ROWS_PER_PAGE
    rows.forEach(function (tr, i) {
      tr.style.display = (i >= start && i < end) ? '' : 'none'
    })

    if (hasControls) {
      var info = controls.querySelector('.pagination-info')
      var prev = controls.querySelector('.page-nav.prev')
      var next = controls.querySelector('.page-nav.next')
      if (info) info.textContent = 'Showing ' + (start + 1) + '–' + Math.min(end, rows.length) +
        ' of ' + rows.length + ' (Page ' + currentPage + ' of ' + totalPages + ')'
      if (prev) {
        prev.disabled = currentPage === 1
        prev.parentElement.classList.toggle('disabled', prev.disabled)
      }
      if (next) {
        next.disabled = currentPage >= totalPages
        next.parentElement.classList.toggle('disabled', next.disabled)
      }
    }
  }

  document.addEventListener('DOMContentLoaded', function () {
    var table = document.querySelector('table[data-paginate]')
    if (!table) return
    var controls = (table.closest('.table-responsive-sm') || table).nextElementSibling
    if (controls && controls.matches('nav[aria-label="Pagination"]')) {
      var prev = controls.querySelector('.page-nav.prev')
      var next = controls.querySelector('.page-nav.next')
      if (prev) prev.addEventListener('click', function () {
        if (currentPage > 1) { currentPage--; render(table) }
      })
      if (next) next.addEventListener('click', function () { currentPage++; render(table) })
    }
    window.__trRepaginate = function () { render(table) }
    render(table)
  })
}())

// --- Pass/fail badge computation for edit view ---

var MISSING = '<span class="badge bg-warning text-dark">MISSING</span>'

// strictNum parses a whole trimmed plain decimal number (NaN otherwise) — the same strings as the
// server's DecimalNumber (models/trmodels.go), so this badge and the saved P/F agree.
function strictNum(s) {
  s = (s || '').trim()
  return /^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$/.test(s) ? Number(s) : NaN
}

function pfBadge(val, min, max, pfType) {
  var v = (val || '').trim()
  if (pfType === 'filled' || pfType === 'attach') {
    return v ? '<span class="badge bg-success">PASS</span>' : MISSING
  }
  if (pfType === 'comment') {
    return '<span class="badge bg-success">PASS</span>'
  }
  // range (default). Strict numbers, as the server's ComputePassFail: "4.5mW" is not 4.5, and a
  // bound that isn't a number leaves P/F blank rather than failing or passing (#468).
  if (!v) return (min || max) ? MISSING : ''
  var f = strictNum(v), lo = strictNum(min), hi = strictNum(max)
  if (isNaN(f) || (min && isNaN(lo)) || (max && isNaN(hi))) return ''
  var pass = true
  if (min) pass = pass && f >= lo
  if (max) pass = pass && f <= hi
  return pass
    ? '<span class="badge bg-success">PASS</span>'
    : '<span class="badge bg-danger">FAIL</span>'
}

function updatePF(el) {
  var targetId = el.dataset.pfTarget
  if (!targetId) return
  var target = document.getElementById(targetId)
  if (!target) return
  target.innerHTML = pfBadge(
    el.value,
    el.dataset.specMin || '',
    el.dataset.specMax || '',
    el.dataset.pfType  || 'range'
  )
}

document.addEventListener('input',  function (e) { updatePF(e.target) })
document.addEventListener('change', function (e) { updatePF(e.target) })

// Live step resolution on the record edit page (#469). Every change posts the page's step context
// (STEP_CTX, embedded by record_edit.html) plus the current values to /api/steps/resolve — the same
// server engine a save uses — and paints what comes back: parameter/specification text, bounds (and
// so the P/F badges), defaults, hidden rows, and query pickers whose resolved spec changed. Arx.exe is
// local and a resolve without new queries touches no database, so the debounce is short.
;(function () {
  if (typeof STEP_CTX === 'undefined') return

  var knownQueries = {} // query spec → first-row value, echoed back so each query runs once per page load
  var failedQueries = {} // query specs that errored — echoed back so they aren't retried every keystroke
  var seq = 0           // drops responses that arrive after a newer request
  var timer = null

  function scheduleResolve() {
    clearTimeout(timer)
    timer = setTimeout(function () { resolveSteps(true) }, 50)
  }

  function currentValues() {
    var values = {}
    document.querySelectorAll('#edit-form [name^="result_"]').forEach(function (el) {
      var m = el.name.match(/^result_(\d+)$/)
      if (m) values[m[1]] = (el.value || '').trim()
    })
    return values
  }

  // runQueries=false resolves against the values the last save stored (no query runs).
  function resolveSteps(runQueries) {
    var my = ++seq
    var rt = document.querySelector('#edit-form [name="record_type"]')
    var record = Object.assign({}, STEP_CTX.record)
    if (rt) record.type = rt.value.trim()
    var body = Object.assign({}, STEP_CTX, {
      record: record, values: currentValues(), queries: knownQueries,
      failed_queries: Object.keys(failedQueries), run_queries: runQueries
    })
    return fetch('/api/steps/resolve?csrf_token=' + encodeURIComponent(PASTE_RESULT_CSRF_TOKEN), {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body)
    })
      .then(function (r) { return r.ok ? r.json() : null })
      .then(function (data) {
        if (!data) return
        // Keep what ran even from a superseded response, so it isn't run again.
        Object.assign(knownQueries, data.queries || {})
        Object.keys(data.query_errors || {}).forEach(function (q) { failedQueries[q] = true })
        if (my === seq) paint(data.steps || {})
      })
      .catch(function () {}) // a failed refresh leaves the page as it was; saving still evaluates
  }

  function paint(steps) {
    var defaultsChanged = false
    Object.keys(steps).forEach(function (id) {
      var s = steps[id]
      var tr = document.querySelector('#edit-form tr[data-step-id="' + id + '"]')
      if (!tr) return
      tr.style.display = s.hidden ? 'none' : ''
      var param = tr.querySelector('.step-param')
      if (param && param.textContent !== s.parameter) param.textContent = s.parameter
      var spec = tr.querySelector('.step-spec')
      if (spec && spec.textContent !== s.specification) spec.textContent = s.specification
      tr.querySelectorAll('[data-pf-target]').forEach(function (el) {
        el.dataset.specMin = s.spec_min
        el.dataset.specMax = s.spec_max
        if (el.name) updatePF(el)
      })
      var wrap = tr.querySelector('[data-query-spec]')
      if (wrap && s.spec_nom.indexOf('query:') === 0 && s.spec_nom !== wrap.dataset.querySpec) {
        wrap.dataset.querySpec = s.spec_nom
        loadNamedQuery(wrap, s.spec_nom)
      }
      var input = tr.querySelector('input[data-formula]')
      if (input && applyDefault(input, s.default_result)) defaultsChanged = true
    })
    // A default that changed a value feeds the steps that reference it.
    if (defaultsChanged) scheduleResolve()
  }

  // Apply a step's resolved default ("" = can't be computed yet) to its input. Returns true when the
  // input's value changed. A value saved on load that differs from the default is a manual override
  // (#133); double-click toggles override (see below).
  function applyDefault(input, val) {
    var before = input.value
    if (val === '') {
      input.readOnly = false
      input.classList.remove('formula-computed', 'formula-overridden')
      input.removeAttribute('title')
      if (!input.dataset.override && input.value !== '' && input.value === input.dataset.computed) {
        input.value = ''
        updatePF(input)
      }
      return input.value !== before
    }
    if (!input.dataset.seen) {
      input.dataset.seen = '1'
      if (input.value !== '' && input.value !== val) input.dataset.override = '1'
    }
    // Cleared override reverts to the computed default.
    if (input.dataset.override && input.value === '') delete input.dataset.override
    if (input.dataset.override) {
      input.readOnly = false
      input.classList.remove('formula-computed')
      var differs = input.value !== val
      input.classList.toggle('formula-overridden', differs)
      if (differs) input.title = 'Overrides default: ' + val
      else input.removeAttribute('title')
      return false
    }
    input.readOnly = true
    input.classList.remove('formula-overridden')
    input.removeAttribute('title')
    input.classList.add('formula-computed')
    input.dataset.computed = val
    if (input.value !== val) {
      input.value = val
      updatePF(input)
    }
    return input.value !== before
  }

  // Load (or reload) a named-query wrapper with the given resolved spec.
  // Preserves any currently-selected value across reloads.
  function loadNamedQuery(wrap, resolvedSpec) {
    var inputName  = wrap.dataset.inputName
    // On reload, keep whatever value the user has already selected.
    var existingHidden = wrap.querySelector('input[type="hidden"]')
    var currentVal = existingHidden ? existingHidden.value : (wrap.dataset.currentVal || '')

    fetch('/api/named-query?spec=' + encodeURIComponent(resolvedSpec))
      .then(function (r) {
        if (!r.ok) return r.text().then(function (t) { throw new Error(t.trim() || r.statusText) })
        return r.json()
      })
      .then(function (data) {
        var rows   = data.rows || []
        var inList = rows.some(function (r) { return r.value === currentVal })

        // --- multi-select: checkboxes, comma-joined hidden value ---
        if (data.result_type === 'multi') {
          var listValues = rows.map(function (r) { return r.value })
          var currentVals = currentVal ? currentVal.split(',').map(function (v) { return v.trim() }).filter(Boolean) : []
          var otherVals = currentVals.filter(function (v) { return listValues.indexOf(v) === -1 })

          var mHidden = document.createElement('input')
          mHidden.type = 'hidden'
          mHidden.name = inputName
          mHidden.value = currentVal
          mHidden.dataset.specMin  = wrap.dataset.specMin  || ''
          mHidden.dataset.specMax  = wrap.dataset.specMax  || ''
          mHidden.dataset.pfType   = wrap.dataset.pfType   || 'range'
          mHidden.dataset.pfTarget = wrap.dataset.pfTarget || ''

          var mBox = document.createElement('div')
          mBox.className = 'border rounded p-1'
          mBox.style.maxHeight = '140px'
          mBox.style.overflowY = 'auto'

          var otherText = document.createElement('input')
          otherText.type = 'text'
          otherText.className = 'form-control form-control-sm font-mono mt-1'
          otherText.placeholder = 'Custom values, comma-separated…'
          otherText.value = otherVals.join(', ')

          var otherCb = null // reference set below after mBox is populated

          function syncMulti() {
            var checked = Array.from(mBox.querySelectorAll('input[type="checkbox"]')).filter(function (cb) {
              return cb !== otherCb && cb.checked
            }).map(function (cb) { return cb.value })
            if (otherCb && otherCb.checked) {
              otherText.value.split(',').map(function (v) { return v.trim() }).filter(Boolean).forEach(function (v) {
                checked.push(v)
              })
            }
            mHidden.value = checked.join(',')
            updatePF(mHidden)
            scheduleResolve()
          }

          rows.forEach(function (row) {
            var lbl = document.createElement('label')
            lbl.className = 'form-check-label d-flex align-items-center gap-1 px-1 py-0'
            lbl.style.cursor = 'pointer'
            var cb = document.createElement('input')
            cb.type = 'checkbox'
            cb.className = 'form-check-input flex-shrink-0'
            cb.value = row.value
            if (currentVals.indexOf(row.value) !== -1) cb.checked = true
            cb.addEventListener('change', syncMulti)
            lbl.appendChild(cb)
            lbl.appendChild(document.createTextNode(
              (row.label && row.label !== row.value) ? row.label + ' — ' + row.value : row.value
            ))
            mBox.appendChild(lbl)
          })

          // Other checkbox
          var otherLbl = document.createElement('label')
          otherLbl.className = 'form-check-label d-flex align-items-center gap-1 px-1 py-0 text-muted'
          otherLbl.style.cursor = 'pointer'
          otherCb = document.createElement('input')
          otherCb.type = 'checkbox'
          otherCb.className = 'form-check-input flex-shrink-0'
          otherCb.value = '__other__'
          if (otherVals.length > 0) otherCb.checked = true
          otherCb.addEventListener('change', function () {
            otherText.style.display = otherCb.checked ? '' : 'none'
            if (otherCb.checked) otherText.focus()
            syncMulti()
          })
          otherLbl.appendChild(otherCb)
          otherLbl.appendChild(document.createTextNode('— Other —'))
          mBox.appendChild(otherLbl)

          otherText.style.display = otherVals.length > 0 ? '' : 'none'
          otherText.addEventListener('input', syncMulti)

          wrap.innerHTML = ''
          wrap.appendChild(mHidden)
          wrap.appendChild(mBox)
          wrap.appendChild(otherText)
          updatePF(mHidden)
          scheduleResolve()
          return
        }
        // --- end multi-select ---

        var hidden = document.createElement('input')
        hidden.type = 'hidden'
        hidden.name = inputName
        hidden.value = currentVal
        hidden.dataset.specMin  = wrap.dataset.specMin  || ''
        hidden.dataset.specMax  = wrap.dataset.specMax  || ''
        hidden.dataset.pfType   = wrap.dataset.pfType   || 'range'
        hidden.dataset.pfTarget = wrap.dataset.pfTarget || ''

        var sel = document.createElement('select')
        sel.className = 'form-select form-select-sm'
        var emptyOpt = document.createElement('option')
        emptyOpt.value = ''
        sel.appendChild(emptyOpt)
        rows.forEach(function (row) {
          var opt = document.createElement('option')
          opt.value = row.value
          opt.textContent = (row.label && row.label !== row.value)
            ? row.label + ' — ' + row.value : row.value
          if (row.value === currentVal) opt.selected = true
          sel.appendChild(opt)
        })
        var otherOpt = document.createElement('option')
        otherOpt.value = '__other__'
        otherOpt.textContent = '— Other —'
        if (!inList && currentVal) otherOpt.selected = true
        sel.appendChild(otherOpt)

        var textInput = document.createElement('input')
        textInput.type = 'text'
        textInput.className = 'form-control form-control-sm font-mono mt-1'
        textInput.placeholder = 'Enter custom value…'
        textInput.value = (!inList && currentVal) ? currentVal : ''
        textInput.style.display = (!inList && currentVal) ? '' : 'none'

        // Open-link icon shown when the value is a LOCAL: path or HTTP URL.
        var openLink = document.createElement('a')
        openLink.target = '_blank'
        openLink.className = 'ms-1 text-muted'
        openLink.title = 'Open'
        openLink.innerHTML = '<i class="bi bi-box-arrow-up-right"></i>'
        openLink.style.display = 'none'

        function updateOpenLink() {
          var v = hidden.value
          if (v && v.startsWith('LOCAL:')) {
            openLink.href = '/local/' + v.slice(6).replace(/\\/g, '/')
            openLink.style.display = ''
          } else if (v && (v.startsWith('http://') || v.startsWith('https://'))) {
            openLink.href = v
            openLink.style.display = ''
          } else {
            openLink.style.display = 'none'
          }
        }

        function syncHidden() {
          if (sel.value === '__other__') {
            hidden.value = textInput.value
            textInput.style.display = ''
          } else {
            hidden.value = sel.value
            textInput.style.display = 'none'
          }
          updateOpenLink()
          updatePF(hidden)
          scheduleResolve()
        }

        sel.addEventListener('change', function () {
          if (sel.value === '__other__') textInput.focus()
          syncHidden()
        })
        textInput.addEventListener('input', syncHidden)

        // single: auto-fill first option if nothing recorded yet
        if (data.result_type === 'single' && !currentVal && rows.length === 1) {
          sel.value = rows[0].value
          syncHidden()
        }

        wrap.innerHTML = ''
        wrap.appendChild(hidden)
        wrap.appendChild(sel)
        wrap.appendChild(textInput)
        wrap.appendChild(openLink)

        updateOpenLink()
        updatePF(hidden)
        // The picked value may feed other steps.
        scheduleResolve()
      })
      .catch(function (err) {
        var msg = document.createElement('span')
        msg.className = 'text-danger small'
        msg.textContent = String(err && err.message ? err.message : err)
        wrap.innerHTML = ''
        wrap.appendChild(msg)
      })
  }

  document.addEventListener('DOMContentLoaded', function () {
    // Initial load of all named-query pickers (their options).
    document.querySelectorAll('[data-query-spec]').forEach(function (wrap) {
      loadNamedQuery(wrap, wrap.dataset.querySpec)
    })
    // Compute initial P/F for any pre-filled plain inputs.
    document.querySelectorAll('[data-pf-target]').forEach(updatePF)
    // First resolve against the stored values, so a saved default that matches what the last save
    // computed isn't mistaken for a manual override; then re-run the page's queries.
    resolveSteps(false).then(function () { resolveSteps(true) })
  })

  function onResultChange(e) {
    var n = e.target.name || ''
    if (/^result_\d+$/.test(n) || n === 'record_type') scheduleResolve()
  }
  document.addEventListener('input', onResultChange)
  document.addEventListener('change', onResultChange)
  // Double-click a locked default to override it; double-click an overridden one to revert (#133).
  document.addEventListener('dblclick', function (e) {
    var input = e.target
    if (!input.matches || !input.matches('input[data-formula]')) return
    if (input.classList.contains('formula-computed')) {
      input.dataset.override = '1'
    } else if (input.dataset.override) {
      input.value = ''
    } else return
    scheduleResolve()
  })
}())

// Definition history timeline.
document.addEventListener('DOMContentLoaded', function () {
  var timeline = document.querySelector('.def-timeline')
  if (!timeline) return

  var formId = timeline.dataset.formId
  var table  = document.getElementById('def-table')
  var label  = document.getElementById('def-history-label')
  var nowDot = timeline.querySelector('.def-timeline-now')
  var dots   = Array.from(timeline.querySelectorAll('.def-timeline-dot'))

  // Store original cell text so we can restore when Now is clicked.
  var originals = {}
  table.querySelectorAll('tr[data-step-id]').forEach(function (tr) {
    var id = tr.dataset.stepId
    originals[id] = {}
    tr.querySelectorAll('[data-field]').forEach(function (td) {
      originals[id][td.dataset.field] = td.textContent
    })
  })

  // Prev/next step through the dots in DOM order (oldest … Now); disabled at either end.
  var prevBtn = document.getElementById('def-history-prev')
  var nextBtn = document.getElementById('def-history-next')
  function activeIndex() { return dots.findIndex(function (d) { return d.classList.contains('active') }) }
  function step(delta) {
    var dot = dots[activeIndex() + delta]
    if (dot) dot.click()
  }
  prevBtn.addEventListener('click', function () { step(-1) })
  nextBtn.addEventListener('click', function () { step(1) })

  function setActiveDot(dot) {
    dots.forEach(function (d) { d.classList.remove('active') })
    dot.classList.add('active')
    var i = activeIndex()
    prevBtn.disabled = i <= 0
    nextBtn.disabled = i >= dots.length - 1
  }

  function applyHistory(data, dotLabel) {
    var stepMap = {}
    ;(data.steps || []).forEach(function (s) {
      if (!stepMap[s.id] || s.changed) stepMap[s.id] = s
    })

    var rows = table.querySelectorAll('tr[data-step-id]')

    rows.forEach(function (tr) {
      var id = parseInt(tr.dataset.stepId)
      var s = stepMap[id]
      if (!s) return
      if (s.changed) {
        tr.classList.add('row-changed')
      } else {
        tr.classList.remove('row-changed')
      }
      tr.querySelectorAll('[data-field]').forEach(function (td) {
        var field = td.dataset.field
        td.textContent = s[field] !== undefined ? s[field] : ''
      })
    })
    label.textContent = 'Showing state after: ' + dotLabel
  }

  function resetToNow() {
    table.querySelectorAll('tr[data-step-id]').forEach(function (tr) {
      tr.classList.remove('row-changed')
      var id = tr.dataset.stepId
      tr.querySelectorAll('[data-field]').forEach(function (td) {
        td.textContent = (originals[id] && originals[id][td.dataset.field]) || ''
      })
    })
    setActiveDot(nowDot)
    label.textContent = ''
  }

  // Event delegation — single listener handles all dots including Now.
  var histReq = 0
  timeline.addEventListener('click', function (e) {
    var dot = e.target.closest('.def-timeline-dot')
    if (!dot) return
    var req = ++histReq // drop out-of-order responses when stepping quickly with prev/next
    if (dot.dataset.now) { resetToNow(); return }
    setActiveDot(dot)
    fetch('/api/forms/' + formId + '/def/history?at=' + encodeURIComponent(dot.dataset.at))
      .then(function (r) { return r.json() })
      .then(function (data) { if (req === histReq) applyHistory(data, dot.title) })
  })
})

// Row change highlighting for the step editor table.
document.addEventListener('DOMContentLoaded', function () {
  var table = document.getElementById('steps-table')
  if (!table) return

  function rowChanged(tr) {
    return Array.from(tr.querySelectorAll('[data-original]')).some(function (el) {
      if (el.type === 'checkbox') {
        var orig = el.dataset.original === 'HIDE'
        return el.checked !== orig
      }
      return el.value !== el.dataset.original
    })
  }

  function updateRow(el) {
    var tr = el.closest('tr')
    if (tr) tr.classList.toggle('row-modified', rowChanged(tr))
  }

  table.addEventListener('input',  function (e) { updateRow(e.target) })
  table.addEventListener('change', function (e) { updateRow(e.target) })
})

// Form definition editor: lint (#471) and per-step preview (#469). Both post the page's unsaved rows to
// /api/steps/resolve — the engine records use — so nothing is saved and no history rows are written.
// Lint is syntax-only (no queries, no database); a preview runs the step's queries against a sample record.
document.addEventListener('DOMContentLoaded', function () {
  var table = document.getElementById('steps-table')
  if (!table || typeof STEP_EDITOR === 'undefined') return
  var FIELDS = ['type', 'parameter', 'specification', 'spec_nom', 'spec_min', 'spec_max', 'spec_units',
    'pf_type', 'default_result', 'hide']

  // A saved row's id, or a negative id for an unsaved row (data-step-id "new_N").
  function newIndex(tr) { var m = /^new_(\d+)$/.exec(tr.dataset.stepId); return m ? m[1] : null }
  function rowKey(tr) { var n = newIndex(tr); return n === null ? parseInt(tr.dataset.stepId, 10) : -(parseInt(n, 10) + 1) }
  function fieldInput(tr, field) {
    var n = newIndex(tr)
    var name = n === null ? field + '_' + tr.dataset.stepId : 'new_row[' + n + '][' + field + ']'
    return tr.querySelector('[name="' + name + '"]')
  }
  function rowDefs() {
    return Array.from(table.querySelectorAll('tbody tr[data-step-id]')).map(function (tr) {
      var d = { id: rowKey(tr) }
      FIELDS.forEach(function (f) {
        var el = fieldInput(tr, f)
        d[f] = el ? el.value : ''
      })
      d.type = parseInt(d.type || '0', 10)
      return d
    })
  }
  function post(body) {
    return fetch('/api/steps/resolve?csrf_token=' + encodeURIComponent(STEP_EDITOR.csrf), {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body)
    }).then(function (r) {
      if (!r.ok) return r.text().then(function (t) { throw new Error(t.trim() || r.statusText) })
      return r.json()
    })
  }

  // --- lint: re-run when focus leaves a field; warnings sit under the field they're about ---
  var lintTimer = null, lintSeq = 0
  function lint() {
    var my = ++lintSeq
    post({ steps: rowDefs(), run_queries: false, lint: true }).then(function (data) {
      if (my !== lintSeq) return
      table.querySelectorAll('.step-lint').forEach(function (el) { el.remove() })
      table.querySelectorAll('.border-warning').forEach(function (el) { el.classList.remove('border-warning') })
      table.querySelectorAll('tbody tr[data-step-id]').forEach(function (tr) {
        var s = (data.steps || {})[rowKey(tr)]
        ;((s && s.warnings) || []).forEach(function (w) {
          var el = fieldInput(tr, w.field)
          if (!el) return
          el.classList.add('border-warning')
          var div = document.createElement('div')
          div.className = 'step-lint small text-warning-emphasis mt-1'
          div.textContent = w.msg
          el.parentNode.appendChild(div)
        })
      })
    }).catch(function () {})
  }
  function scheduleLint() { clearTimeout(lintTimer); lintTimer = setTimeout(lint, 300) }
  table.addEventListener('focusout', scheduleLint)
  table.addEventListener('change', scheduleLint)
  lint()

  // --- preview ---
  var panel = document.getElementById('step-preview')
  if (!panel) return
  var previewKey = null, previewSeq = 0
  var body = panel.querySelector('[data-preview-body]')

  // "15=4.9, {16}=OK" → { "15": "4.9", "16": "OK" }
  function sampleValues(text) {
    var v = {}
    text.split(',').forEach(function (p) {
      var i = p.indexOf('=')
      if (i <= 0) return
      var id = p.slice(0, i).trim().replace(/[{}]/g, '')
      if (/^\d+$/.test(id)) v[id] = p.slice(i + 1).trim()
    })
    return v
  }

  function addRow(tbl, label, value, cls) {
    var tr = tbl.insertRow()
    var th = document.createElement('th')
    th.className = 'text-nowrap'
    th.textContent = label
    tr.appendChild(th)
    var td = tr.insertCell()
    td.className = cls || 'font-mono'
    td.textContent = value
  }

  function renderPreview(data, my) {
    var s = (data.steps || {})[previewKey] || {}
    var picker = /^query:/.test(s.spec_nom || '') ? s.spec_nom : ''
    body.innerHTML = ''
    var ctx = document.createElement('div')
    ctx.className = 'text-muted mb-2'
    ctx.textContent = data.sample_record ? 'Sample record ' + data.sample_record
      : 'No sample record: record tokens stay unresolved'
    body.appendChild(ctx)
    var tbl = document.createElement('table')
    tbl.className = 'table table-sm table-bordered mb-2 w-auto'
    addRow(tbl, 'Parameter', s.parameter || '')
    addRow(tbl, 'Specification', s.specification || '')
    addRow(tbl, 'spec_nom', s.spec_nom || '')
    addRow(tbl, 'spec_min', s.spec_min || '')
    addRow(tbl, 'spec_max', s.spec_max || '')
    addRow(tbl, 'spec_units', s.spec_units || '')
    addRow(tbl, 'default_result', s.default_result || '')
    addRow(tbl, 'Hidden', s.hidden ? 'yes' : 'no', '')
    Object.keys(data.queries || {}).forEach(function (q) {
      if (q === picker) return // shown below with every option, not just the first row
      addRow(tbl, q, data.queries[q] === '' ? '(no rows)' : data.queries[q])
    })
    if (picker && !(data.query_errors || {})[picker]) {
      // A picker's options: every row, as the record edit page loads them.
      addRow(tbl, picker, 'Loading…')
      var cell = tbl.rows[tbl.rows.length - 1].cells[1]
      fetch('/api/named-query?spec=' + encodeURIComponent(picker)).then(function (r) {
        if (!r.ok) return r.text().then(function (t) { throw new Error(t.trim() || r.statusText) })
        return r.json()
      }).then(function (q) {
        if (my !== previewSeq) return
        var rows = q.rows || []
        cell.textContent = rows.length ? rows.map(function (o) { return o.label || o.value }).join('; ') : '(no rows)'
      }).catch(function (err) {
        if (my !== previewSeq) return
        cell.className = 'text-danger'
        cell.textContent = String(err && err.message ? err.message : err)
      })
    }
    Object.keys(data.query_errors || {}).forEach(function (q) {
      addRow(tbl, q, data.query_errors[q], 'text-danger')
    })
    body.appendChild(tbl)
    ;(s.warnings || []).forEach(function (w) {
      var div = document.createElement('div')
      div.className = 'text-warning-emphasis'
      div.textContent = w.field + ': ' + w.msg
      body.appendChild(div)
    })
  }

  function preview() {
    var my = ++previewSeq
    var rec = panel.querySelector('[data-preview-record]').value.trim()
    body.textContent = 'Resolving…'
    post({
      steps: rowDefs(), show: [previewKey], form_id: STEP_EDITOR.formId,
      record_id: rec ? parseInt(rec, 10) : -1,
      values: sampleValues(panel.querySelector('[data-preview-values]').value), run_queries: true, lint: true
    }).then(function (data) { if (my === previewSeq) renderPreview(data, my) })
      .catch(function (err) { if (my === previewSeq) body.textContent = String(err && err.message ? err.message : err) })
  }

  table.addEventListener('click', function (e) {
    var btn = e.target.closest('[data-step-preview]')
    if (!btn) return
    previewKey = rowKey(btn.closest('tr'))
    panel.querySelector('[data-preview-id]').textContent = previewKey > 0 ? previewKey : '(new row)'
    panel.classList.remove('d-none')
    preview()
  })
  panel.querySelector('[data-preview-refresh]').addEventListener('click', preview)
  panel.querySelector('[data-preview-close]').addEventListener('click', function () { panel.classList.add('d-none') })
  // The panel sits inside the steps form: Enter refreshes the preview instead of saving the definition.
  panel.addEventListener('keydown', function (e) {
    if (e.key === 'Enter') { e.preventDefault(); preview() }
  })
})

// Lazy-load an image preview on first hover — avoids fetching every image on
// page load. Shared with paste_result_image.js, which calls this for preview
// elements created/updated after the initial page load.
function wireImgHoverPreview(wrap) {
  var loaded = false
  wrap.addEventListener('mouseenter', function () {
    if (loaded) return
    var img = wrap.querySelector('.img-hover-preview img')
    if (img) img.src = wrap.dataset.src
    loaded = true
  })
}

document.addEventListener('DOMContentLoaded', function () {
  document.querySelectorAll('.img-hover-wrap').forEach(wireImgHoverPreview)
})
