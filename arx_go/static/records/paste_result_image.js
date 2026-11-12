// Clipboard-paste and file-browse attachments for test-record result steps
// (pf_type = "attach"). One button/input per qualifying step (record_edit.html),
// unlike the parts version which has a single page-global button. Uses the
// shared readImageFromClipboard/postImageDataURL helpers in
// static/clipboard_paste.js and wireImgHoverPreview in static/tr/app.js.
// Clipboard paste stays image-only (browsers only sanction image types for
// navigator.clipboard.read(), which readImageFromClipboard uses); PDFs come
// in via the file-browse input (uploadBrowsedResultFile) or the raw Ctrl+V
// paste listener below, which sees a file's real MIME type.

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

// Uploads a file browsed from disk via the new multipart endpoint (#446) —
// used for PDFs and as an alternative to clipboard paste for images.
function uploadBrowsedResultFile(cell, file) {
    var errEl = cell.querySelector('[data-attach-error]');
    if (errEl) { errEl.classList.add('d-none'); errEl.textContent = ''; }
    var recordId = cell.dataset.recordId;
    var testId = cell.dataset.testId;
    var url = '/api/record/' + recordId + '/step/' + testId + '/upload-result-file?csrf_token=' + encodeURIComponent(PASTE_RESULT_CSRF_TOKEN);
    var fd = new FormData();
    fd.append('file', file);
    postRequestJSON(url, { method: 'POST', body: fd }, function (data) {
        applyPastedResultImage(cell, data.filename);
    }, function (msg) {
        if (errEl) { errEl.textContent = msg; errEl.classList.remove('d-none'); }
    });
}

// Updates the hidden result input and preview slot in place — no page reload,
// so other unsaved edits on the record survive. The filename is only
// persisted to the DB when the user clicks Save (SaveResults handler).
// cell.dataset.partNumber is rendered server-side via the "sanitizedPartNumber"
// template func, matching the folder APIRecordPasteResultImage/
// APIRecordUploadResultFile write to.
function applyPastedResultImage(cell, filename) {
    var inputName = cell.dataset.resultInput;
    var input = document.querySelector('#edit-form [name="' + inputName + '"]');
    if (input) {
        input.value = filename;
        input.dispatchEvent(new Event('input'));
        input.dispatchEvent(new Event('change'));
    }

    var fileURL = '/images/' + cell.dataset.partNumber + '/' + filename;
    var isPdf = /\.pdf$/i.test(filename);

    var preview = cell.querySelector('[data-attach-preview]');
    var pdfLink = cell.querySelector('[data-attach-pdf-link]');
    if (preview) {
        if (isPdf) {
            preview.classList.add('d-none');
        } else {
            preview.dataset.src = fileURL;
            preview.classList.remove('d-none');
            var link = preview.querySelector('[data-attach-link]');
            if (link) { link.setAttribute('href', fileURL); }
            var img = preview.querySelector('.img-hover-preview img');
            if (img) { img.removeAttribute('src'); }
            wireImgHoverPreview(preview);
        }
    }
    if (pdfLink) {
        if (isPdf) {
            pdfLink.querySelector('[data-attach-pdf-href]').setAttribute('href', fileURL);
            pdfLink.classList.remove('d-none');
        } else {
            pdfLink.classList.add('d-none');
        }
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
    document.querySelectorAll('[data-attach-file-input]').forEach(function (input) {
        input.addEventListener('change', function () {
            var cell = input.closest('[data-attach-cell]');
            var file = input.files && input.files[0];
            if (cell && file) { uploadBrowsedResultFile(cell, file); }
            input.value = ''; // allow re-selecting the same file after an error
        });
    });
});

// Ctrl+V anywhere on the page pastes into the focused attach cell, if any.
// Checks the real clipboard item type (unlike readImageFromClipboard's
// navigator.clipboard.read(), which browsers restrict to image types), so a
// PDF file copied in a file manager and pasted here is also accepted.
document.addEventListener('paste', function (e) {
    var active = document.activeElement;
    var cell = active && active.closest ? active.closest('[data-attach-cell]') : null;
    if (!cell) return;
    var items = (e.clipboardData || {}).items || [];
    for (var i = 0; i < items.length; i++) {
        if (items[i].type.indexOf('image/') === 0 || items[i].type === 'application/pdf') {
            uploadPastedResultImage(cell, items[i].getAsFile());
            e.preventDefault();
            return;
        }
    }
});
