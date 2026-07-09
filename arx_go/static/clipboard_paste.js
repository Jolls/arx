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

// postImageDataURL reads blob as a data: URL, POSTs JSON.stringify(buildBody(dataURL))
// to url, and calls onSuccess(responseData) or onError(message).
function postImageDataURL(url, blob, buildBody, onSuccess, onError) {
    var reader = new FileReader();
    reader.onload = function () {
        fetch(url, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(buildBody(reader.result))
        })
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
    };
    reader.readAsDataURL(blob);
}
