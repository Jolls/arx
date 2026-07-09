// Clipboard-paste image attachments (Add/Edit Attachment forms,
// part_attachments.html). Uses the shared readImageFromClipboard/
// postImageDataURL helpers in static/clipboard_paste.js, and the attId(mode,
// name) id-mapping helper defined inline in part_attachments.html.

function uploadPastedImage(blob, mode, attID) {
    mode = mode || 'add';
    var btn = document.getElementById(attId(mode, 'paste-clipboard-btn'));
    var errEl = document.getElementById(attId(mode, 'paste-error'));
    if (errEl) { errEl.style.display = 'none'; }
    var url = mode === 'edit'
        ? '/api/part/' + PASTE_ATTACHMENT_PART_ID + '/attachments/' + attID + '/paste-attachment?csrf_token=' + encodeURIComponent(PASTE_ATTACHMENT_CSRF_TOKEN)
        : '/api/part/' + PASTE_ATTACHMENT_PART_ID + '/paste-attachment?csrf_token=' + encodeURIComponent(PASTE_ATTACHMENT_CSRF_TOKEN);
    if (btn) { btn.disabled = true; }
    postImageDataURL(url, blob, function (dataURL) {
        return {
            image_data: dataURL,
            rev: (document.getElementById(attId(mode, 'FILPNRev')) || {}).value || '',
            order_id: (document.getElementById(attId(mode, 'order_id')) || {}).value || '',
            comment: (document.getElementById(attId(mode, 'comment')) || {}).value || ''
        };
    }, function () {
        location.reload();
    }, function (msg) {
        if (errEl) { errEl.textContent = msg; errEl.style.display = ''; }
        if (btn) { btn.disabled = false; }
    });
}

function pasteAttachmentFromClipboard(btn, mode, attID) {
    mode = mode || 'add';
    var errEl = document.getElementById(attId(mode, 'paste-error'));
    if (errEl) { errEl.style.display = 'none'; }
    readImageFromClipboard(function (blob) { uploadPastedImage(blob, mode, attID); }, function (msg) {
        if (errEl) { errEl.textContent = msg; errEl.style.display = ''; }
    });
}

document.addEventListener('paste', function (e) {
    var categoryInput = document.getElementById('add_category');
    if (!categoryInput || categoryInput.value !== 'Photo') { return; }
    var items = (e.clipboardData || {}).items || [];
    for (var i = 0; i < items.length; i++) {
        if (items[i].type.indexOf('image/') === 0) {
            uploadPastedImage(items[i].getAsFile());
            e.preventDefault();
            return;
        }
    }
});

// Lazy-load image previews on first hover — avoids fetching every image on page load.
document.addEventListener('DOMContentLoaded', function () {
    document.querySelectorAll('.img-hover-wrap').forEach(function (wrap) {
        var loaded = false;
        wrap.addEventListener('mouseenter', function () {
            if (loaded) return;
            var img = wrap.querySelector('.img-hover-preview img');
            img.src = wrap.dataset.src;
            loaded = true;
        });
    });
});
