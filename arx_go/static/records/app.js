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
    var controls = table.nextElementSibling
    var hasControls = controls && controls.classList.contains('pagination-controls')

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
      if (prev) prev.disabled = currentPage === 1
      if (next) next.disabled = currentPage >= totalPages
    }
  }

  document.addEventListener('DOMContentLoaded', function () {
    var table = document.querySelector('table[data-paginate]')
    if (!table) return
    var controls = table.nextElementSibling
    if (controls && controls.classList.contains('pagination-controls')) {
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

function pfBadge(val, min, max, pfType) {
  var v = (val || '').trim()
  if (pfType === 'filled' || pfType === 'attach') {
    return v ? '<span class="badge bg-success">PASS</span>' : MISSING
  }
  if (pfType === 'comment') {
    return '<span class="badge bg-success">PASS</span>'
  }
  // range (default)
  if (!v) return (min || max) ? MISSING : ''
  var f = parseFloat(v)
  if (isNaN(f)) return ''
  var pass = true
  if (min) pass = pass && f >= parseFloat(min)
  if (max) pass = pass && f <= parseFloat(max)
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

// Auto-load named query options for query: rows on the edit page.
// Supports live re-resolution: when any result changes, named queries whose
// spec contains {id} cross-references are re-fetched with the updated value.
;(function () {
  // Live map: step-id (string) → current result value.
  // Updated whenever any result input or named-query select changes.
  var liveResults = {}

  function setLive(sid, val) {
    liveResults[String(sid)] = val || ''
  }

  // Resolve {id} tokens in rawSpec using liveResults.
  // {record.X} tokens are already resolved server-side; only numeric {id} remain.
  function resolveIds(raw) {
    return raw.replace(/\{(\d+)\}/g, function (_, id) {
      var v = liveResults[id]
      return (v !== undefined && v !== '') ? v : '{' + id + '}'
    })
  }

  // Load (or reload) a named-query wrapper with the given resolved spec.
  // Preserves any currently-selected value across reloads.
  function loadNamedQuery(wrap, resolvedSpec) {
    var inputName  = wrap.dataset.inputName
    var sid        = wrap.dataset.stepId
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
            if (sid) setLive(sid, mHidden.value)
            updatePF(mHidden)
            schedulResolve()
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
          if (sid) setLive(sid, mHidden.value)
          updatePF(mHidden)
          schedulResolve()
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
          if (sid) setLive(sid, hidden.value)
          updateOpenLink()
          updatePF(hidden)
          schedulResolve()
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

        if (sid) setLive(sid, hidden.value)
        updateOpenLink()
        updatePF(hidden)
        // After loading, check if any downstream queries need re-fetching.
        schedulResolve()
      })
      .catch(function (err) {
        var msg = document.createElement('span')
        msg.className = 'text-danger small'
        msg.textContent = String(err && err.message ? err.message : err)
        wrap.innerHTML = ''
        wrap.appendChild(msg)
      })
  }

  // Evaluate a basic arithmetic expression string (only digits, +, -, *, /, ., parens).
  // Returns the numeric result or null if the expression is invalid/unsafe.
  function evalMath(expr) {
    // Reject date-like strings (e.g. "12/29/2026") — slashes would be evaluated as division.
    if (/^\s*\d{1,2}\/\d{1,2}\/\d{2,4}/.test(expr)) return null
    if (!/^[\d\s+\-*/.()]+$/.test(expr)) return null
    try { return Function('"use strict"; return (' + expr + ')')() } catch (e) { return null }
  }

  // Apply data-formula inputs: resolve {id} tokens, evaluate math, update value + P/F.
  // Inputs are marked readonly and highlighted when the formula fully resolves.
  function applyFormulas() {
    document.querySelectorAll('input[data-formula]').forEach(function (input) {
      var formula = input.dataset.formula
      if (!formula) return
      var resolved = resolveIds(formula)
      var hasUnresolved = resolved.indexOf('{') !== -1
      if (hasUnresolved) {
        input.readOnly = false
        input.classList.remove('formula-computed')
        return
      }
      var result = evalMath(resolved.trim())
      if (result === null || !isFinite(result)) return
      var val = String(parseFloat(result.toFixed(10)))
      input.readOnly = true
      input.classList.add('formula-computed')
      if (input.value !== val) {
        input.value = val
        var sid = input.name ? input.name.slice(7) : ''
        if (sid) setLive(sid, val)
        updatePF(input)
      }
    })
  }

  // Re-resolve all named-query wrappers that have a raw spec with {id} tokens.
  // Re-fetches only those whose resolved spec actually changed.
  var resolveTimer = null
  function schedulResolve() {
    clearTimeout(resolveTimer)
    resolveTimer = setTimeout(function () {
      document.querySelectorAll('[data-raw-spec]').forEach(function (wrap) {
        var raw = wrap.dataset.rawSpec
        if (!raw) return
        var resolved = resolveIds(raw)
        if (resolved !== wrap.dataset.querySpec) {
          wrap.dataset.querySpec = resolved
          loadNamedQuery(wrap, resolved)
        }
      })
      applyFormulas()
    }, 120)
  }

  document.addEventListener('DOMContentLoaded', function () {
    // Seed live map from any plain result inputs already on the page.
    document.querySelectorAll('[name^="result_"]').forEach(function (el) {
      var sid = el.name.slice(7)
      setLive(sid, el.value)
    })

    // Initial load of all named-query wrappers.
    document.querySelectorAll('[data-query-spec]').forEach(function (wrap) {
      loadNamedQuery(wrap, wrap.dataset.querySpec)
    })

    // Compute initial P/F for any pre-filled plain inputs.
    document.querySelectorAll('[data-pf-target]').forEach(updatePF)

    // Apply any formula-driven result inputs.
    applyFormulas()
  })

  // Keep live map in sync when plain text/select result inputs change.
  document.addEventListener('input', function (e) {
    var m = e.target.name && e.target.name.match(/^result_(\d+)$/)
    if (m) { setLive(m[1], e.target.value); schedulResolve() }
  })
  document.addEventListener('change', function (e) {
    var m = e.target.name && e.target.name.match(/^result_(\d+)$/)
    if (m) { setLive(m[1], e.target.value); schedulResolve() }
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

  function setActiveDot(dot) {
    dots.forEach(function (d) { d.classList.remove('active') })
    dot.classList.add('active')
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
    label.textContent = 'Showing state before: ' + dotLabel
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
  timeline.addEventListener('click', function (e) {
    var dot = e.target.closest('.def-timeline-dot')
    if (!dot) return
    if (dot.dataset.now) { resetToNow(); return }
    setActiveDot(dot)
    fetch('/api/forms/' + formId + '/def/history?at=' + encodeURIComponent(dot.dataset.at))
      .then(function (r) { return r.json() })
      .then(function (data) { applyHistory(data, dot.title) })
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

// spec_nom syntax lint for the form definition editor.
// Validates query:name(@param=value) syntax on blur; clears on focus.
document.addEventListener('DOMContentLoaded', function () {
  var table = document.getElementById('steps-table')
  if (!table) return

  function lintSpecNom(input) {
    var val = (input.value || '').trim()
    var msg = ''

    if (val.startsWith('query:')) {
      var m = val.match(/^query:(\w*)\(([^)]*)\)$/)
      if (!m) {
        msg = val.indexOf('(') === -1
          ? 'Expected format: query:name(@param=value, …)'
          : 'Missing closing )'
      } else {
        var name = m[1], paramsStr = m[2].trim()
        if (!name) {
          msg = 'Missing query name after query:'
        } else if (paramsStr) {
          var bad = paramsStr.split(',').map(function (p) { return p.trim() })
            .filter(function (p) { return p && !p.startsWith('@') })
            .map(function (p) { return p.split('=')[0].trim() })
          if (bad.length) {
            msg = 'Param' + (bad.length > 1 ? 's' : '') + ' missing @: ' +
              bad.map(function (p) { return '@' + p }).join(', ')
          }
        }
      }
    }

    var existing = input.parentNode.querySelector('.spec-nom-lint')
    if (existing) existing.remove()
    input.classList.toggle('is-invalid', !!msg)
    if (msg) {
      var div = document.createElement('div')
      div.className = 'spec-nom-lint text-danger small mt-1'
      div.textContent = msg
      input.insertAdjacentElement('afterend', div)
    }
  }

  function clearLint(input) {
    input.classList.remove('is-invalid')
    var existing = input.parentNode.querySelector('.spec-nom-lint')
    if (existing) existing.remove()
  }

  function isSpecNom(el) {
    return el.tagName === 'INPUT' && (el.name || '').indexOf('spec_nom') !== -1
  }

  // focusout/focusin bubble (unlike blur/focus), so event delegation works without capture.
  table.addEventListener('focusout', function (e) { if (isSpecNom(e.target)) lintSpecNom(e.target) })
  table.addEventListener('focusin',  function (e) { if (isSpecNom(e.target)) clearLint(e.target) })

  // Lint any pre-filled values on load.
  table.querySelectorAll('input[name*="spec_nom"]').forEach(lintSpecNom)
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
