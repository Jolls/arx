// Shared clipboard-image-paste helpers, used by both the parts attachment paste
// flow (static/parts/paste_attachment.js) and the test-record result paste flow
// (static/records/paste_result_image.js).

// readImageFromClipboard reads the system clipboard and calls onBlob with the
// first image found, or onError(message) if there's no image / access fails.
function readImageFromClipboard(onBlob, onError) {
    if (!navigator.clipboard || !navigator.clipboard.read) {
        onError('Clipboard access is not available; use Ctrl+V instead.');
        return;
    }
    navigator.clipboard.read().then(function (items) {
        for (var i = 0; i < items.length; i++) {
            var imgType = items[i].types.find(function (t) { return t.indexOf('image/') === 0; });
            if (imgType) {
                items[i].getType(imgType).then(onBlob);
                return;
            }
        }
        onError('No image found on the clipboard.');
    }).catch(function (err) {
        onError('Could not read clipboard: ' + err.message);
    });
}

// postRequestJSON sends a fetch request (JSON body, multipart form, etc. —
// whatever init describes) and calls onSuccess(responseData) or
// onError(message), treating a non-ok response's parsed body as carrying the
// server's error message. Shared by postImageDataURL (JSON body) and the
// file-browse upload path in paste_result_image.js (multipart body).
function postRequestJSON(url, init, onSuccess, onError) {
    fetch(url, init)
        .then(function (r) {
            return r.json()
                .catch(function () { return {}; })
                .then(function (data) { return { ok: r.ok, data: data }; });
        })
        .then(function (result) {
            if (!result.ok) { throw new Error(result.data.error || 'Upload failed'); }
            onSuccess(result.data);
        })
        .catch(function (err) {
            onError(err.message);
        });
}

// postImageDataURL reads blob as a data: URL, POSTs JSON.stringify(buildBody(dataURL))
// to url, and calls onSuccess(responseData) or onError(message).
function postImageDataURL(url, blob, buildBody, onSuccess, onError) {
    var reader = new FileReader();
    reader.onload = function () {
        postRequestJSON(url, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(buildBody(reader.result))
        }, onSuccess, onError);
    };
    reader.readAsDataURL(blob);
}
