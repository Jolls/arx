# DigiKey API User Agreement — reference pointer

Reference note for the DigiKey integration added in #27 (`arx_go/digikey.go`, merged
via PR #54). Last reviewed 2026-09-14.

The agreement text is **not reproduced here**. It is DigiKey's copyrighted document and
this repository's AGPL-3.0 licence does not cover it.

Canonical source: DigiKey's developer portal, <https://developer.digikey.com/>, under
the API User Agreement. Per its own modification clause DigiKey may revise it at any
time by posting a new version, so the portal is the only authoritative copy — re-check
there rather than relying on a snapshot.

Points that bear on how this integration is built, recorded here as our own summary
rather than quotation:

- API access is granted for defined permitted purposes only; an internal purchasing
  application is one of them.
- Catalogue data returned by the API is DigiKey's and carries its own usage
  restrictions, which is why `arx_go/testdata/digikey_productdetails.json` is a
  synthetic fixture rather than a captured live response.
- Credentials are per-registered-application. They belong in the app's configuration,
  never in this repository.

Re-read the agreement on the portal before changing what the integration requests or
stores.
