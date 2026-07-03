// Clipboard-paste image attachments (Add Attachment form, part_attachments.html).

function uploadPastedImage(blob) {
    var btn = document.getElementById('paste-clipboard-btn');
    var errEl = document.getElementById('paste-error');
    if (errEl) { errEl.style.display = 'none'; }
    var reader = new FileReader();
    reader.onload = function () {
        var body = {
            image_data: reader.result,
            rev: (document.getElementById('FILPNRev') || {}).value || '',
            order_id: (document.getElementById('order_id') || {}).value || '',
            comment: (document.querySelector('textarea[name="comment"]') || {}).value || ''
        };
        var url = '/api/part/' + PASTE_ATTACHMENT_PART_ID + '/paste-attachment?csrf_token=' + encodeURIComponent(PASTE_ATTACHMENT_CSRF_TOKEN);
        if (btn) { btn.disabled = true; }
        fetch(url, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body)
        })
            .then(function (r) {
                return r.json()
                    .catch(function () { return {}; })
                    .then(function (data) { return { ok: r.ok, data: data }; });
            })
            .then(function (result) {
                if (!result.ok) { throw new Error(result.data.error || 'Upload failed'); }
                location.reload();
            })
            .catch(function (err) {
                if (errEl) { errEl.textContent = err.message; errEl.style.display = ''; }
                if (btn) { btn.disabled = false; }
            });
    };
    reader.readAsDataURL(blob);
}

function pasteAttachmentFromClipboard(btn) {
    var errEl = document.getElementById('paste-error');
    if (errEl) { errEl.style.display = 'none'; }
    if (!navigator.clipboard || !navigator.clipboard.read) {
        if (errEl) { errEl.textContent = 'Clipboard access is not available; use Ctrl+V instead.'; errEl.style.display = ''; }
        return;
    }
    navigator.clipboard.read().then(function (items) {
        for (var i = 0; i < items.length; i++) {
            var imgType = items[i].types.find(function (t) { return t.indexOf('image/') === 0; });
            if (imgType) {
                items[i].getType(imgType).then(uploadPastedImage);
                return;
            }
        }
        if (errEl) { errEl.textContent = 'No image found on the clipboard.'; errEl.style.display = ''; }
    }).catch(function (err) {
        if (errEl) { errEl.textContent = 'Could not read clipboard: ' + err.message; errEl.style.display = ''; }
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
