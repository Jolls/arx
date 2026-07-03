// Clipboard-paste image attachments for test-record result steps (pf_type = "attach").
// One button per qualifying step (record_edit.html), unlike the parts version
// which has a single page-global button.

function uploadPastedResultImage(cell, blob) {
    var btn = cell.querySelector('[data-attach-btn]');
    var errEl = cell.querySelector('[data-attach-error]');
    if (errEl) { errEl.classList.add('d-none'); errEl.textContent = ''; }
    var reader = new FileReader();
    reader.onload = function () {
        var recordId = cell.dataset.recordId;
        var testId = cell.dataset.testId;
        var url = '/api/record/' + recordId + '/step/' + testId + '/paste-image?csrf_token=' + encodeURIComponent(PASTE_RESULT_CSRF_TOKEN);
        if (btn) { btn.disabled = true; }
        fetch(url, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ image_data: reader.result })
        })
            .then(function (r) {
                return r.json()
                    .catch(function () { return {}; })
                    .then(function (data) { return { ok: r.ok, data: data }; });
            })
            .then(function (result) {
                if (!result.ok) { throw new Error(result.data.error || 'Upload failed'); }
                applyPastedResultImage(cell, result.data.filename);
            })
            .catch(function (err) {
                if (errEl) { errEl.textContent = err.message; errEl.classList.remove('d-none'); }
            })
            .then(function () {
                if (btn) { btn.disabled = false; }
            });
    };
    reader.readAsDataURL(blob);
}

// Updates the hidden result input and preview slot in place — no page reload,
// so other unsaved edits on the record survive. The filename is only
// persisted to the DB when the user clicks Save (SaveResults handler).
function applyPastedResultImage(cell, filename) {
    var inputName = cell.dataset.resultInput;
    var input = document.querySelector('#edit-form [name="' + inputName + '"]');
    if (input) {
        input.value = filename;
        input.dispatchEvent(new Event('input'));
        input.dispatchEvent(new Event('change'));
    }

    // Matches the Go "imageURL" template func exactly (simple concatenation, no encoding).
    var partNumber = cell.dataset.partNumber;
    var imgURL = '/images/' + partNumber + '/' + filename;

    var preview = cell.querySelector('[data-attach-preview]');
    if (preview) {
        preview.dataset.src = imgURL;
        preview.classList.remove('d-none');
        var link = preview.querySelector('[data-attach-link]');
        if (link) { link.setAttribute('href', imgURL); }
        var img = preview.querySelector('.img-hover-preview img');
        if (img) { img.removeAttribute('src'); }
        wireImgHoverPreview(preview);
    }
}

// Mirrors the lazy-load-on-hover behavior in static/tr/app.js, applied to a
// preview element created/updated after the initial page load.
function wireImgHoverPreview(wrap) {
    var loaded = false;
    wrap.addEventListener('mouseenter', function () {
        if (loaded) return;
        var img = wrap.querySelector('.img-hover-preview img');
        if (img) { img.src = wrap.dataset.src; }
        loaded = true;
    });
}

function pasteResultImageFromClipboard(btn) {
    var cell = btn.closest('[data-attach-cell]');
    if (!cell) return;
    var errEl = cell.querySelector('[data-attach-error]');
    if (errEl) { errEl.classList.add('d-none'); errEl.textContent = ''; }
    if (!navigator.clipboard || !navigator.clipboard.read) {
        if (errEl) { errEl.textContent = 'Clipboard access is not available; use Ctrl+V instead.'; errEl.classList.remove('d-none'); }
        return;
    }
    navigator.clipboard.read().then(function (items) {
        for (var i = 0; i < items.length; i++) {
            var imgType = items[i].types.find(function (t) { return t.indexOf('image/') === 0; });
            if (imgType) {
                items[i].getType(imgType).then(function (blob) { uploadPastedResultImage(cell, blob); });
                return;
            }
        }
        if (errEl) { errEl.textContent = 'No image found on the clipboard.'; errEl.classList.remove('d-none'); }
    }).catch(function (err) {
        if (errEl) { errEl.textContent = 'Could not read clipboard: ' + err.message; errEl.classList.remove('d-none'); }
    });
}

document.addEventListener('DOMContentLoaded', function () {
    document.querySelectorAll('[data-attach-btn]').forEach(function (btn) {
        btn.addEventListener('click', function () { pasteResultImageFromClipboard(btn); });
    });
});

// Ctrl+V anywhere on the page pastes into the focused attach cell, if any.
document.addEventListener('paste', function (e) {
    var active = document.activeElement;
    var cell = active && active.closest ? active.closest('[data-attach-cell]') : null;
    if (!cell) return;
    var items = (e.clipboardData || {}).items || [];
    for (var i = 0; i < items.length; i++) {
        if (items[i].type.indexOf('image/') === 0) {
            uploadPastedResultImage(cell, items[i].getAsFile());
            e.preventDefault();
            return;
        }
    }
});
