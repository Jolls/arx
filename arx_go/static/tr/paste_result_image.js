// Clipboard-paste image attachments for test-record result steps (pf_type = "attach").
// One button per qualifying step (record_edit.html), unlike the parts version
// which has a single page-global button. Uses the shared
// readImageFromClipboard/postImageDataURL helpers in static/clipboard_paste.js
// and wireImgHoverPreview in static/tr/app.js.

function uploadPastedResultImage(cell, blob) {
    var btn = cell.querySelector('[data-attach-btn]');
    var errEl = cell.querySelector('[data-attach-error]');
    if (errEl) { errEl.classList.add('d-none'); errEl.textContent = ''; }
    var recordId = cell.dataset.recordId;
    var testId = cell.dataset.testId;
    var url = '/api/record/' + recordId + '/step/' + testId + '/paste-image?csrf_token=' + encodeURIComponent(PASTE_RESULT_CSRF_TOKEN);
    if (btn) { btn.disabled = true; }
    postImageDataURL(url, blob, function (dataURL) {
        return { image_data: dataURL };
    }, function (data) {
        applyPastedResultImage(cell, data.filename);
        if (btn) { btn.disabled = false; }
    }, function (msg) {
        if (errEl) { errEl.textContent = msg; errEl.classList.remove('d-none'); }
        if (btn) { btn.disabled = false; }
    });
}

// Updates the hidden result input and preview slot in place — no page reload,
// so other unsaved edits on the record survive. The filename is only
// persisted to the DB when the user clicks Save (SaveResults handler).
// cell.dataset.partNumber is rendered server-side via the "sanitizedPartNumber"
// template func, matching the folder APIRecordPasteResultImage writes to.
function applyPastedResultImage(cell, filename) {
    var inputName = cell.dataset.resultInput;
    var input = document.querySelector('#edit-form [name="' + inputName + '"]');
    if (input) {
        input.value = filename;
        input.dispatchEvent(new Event('input'));
        input.dispatchEvent(new Event('change'));
    }

    var imgURL = '/images/' + cell.dataset.partNumber + '/' + filename;

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

function pasteResultImageFromClipboard(btn) {
    var cell = btn.closest('[data-attach-cell]');
    if (!cell) return;
    var errEl = cell.querySelector('[data-attach-error]');
    if (errEl) { errEl.classList.add('d-none'); errEl.textContent = ''; }
    readImageFromClipboard(function (blob) { uploadPastedResultImage(cell, blob); }, function (msg) {
        if (errEl) { errEl.textContent = msg; errEl.classList.remove('d-none'); }
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
