# Document viewer

The Documents catalog and a conversation's Documents panel can open PDF and
text attachments inside Oie. Markdown uses Margo semantic rendering in its card
preview and expanded viewer. TXT, LOG, CSV, JSON and XML remain plain text.
A static host projection excludes resource-loading elements, scripts, document
IDs, authored attributes and document runtimes. External links open explicitly.
If Margo cannot render a source, the readable original remains available as
plain text. Markdown image references currently trigger this fallback under
Margo's offline image policy.
Other document types retain an action to open the original provider address.

Visible document cards show a text excerpt or the first PDF page. The preview
and filename form one keyboard-accessible control that opens the viewer.
Previews read through the existing scoped engine route, with two concurrent
jobs and a 12-second timeout. Text and Markdown read at most a 64 KiB prefix.
The browser sends Markdown text to the BFF-only `/ui/document-markdown`
presentation operation, specified in `api/bff.openapi.yaml` with generated
models. It performs no engine reads or writes and accepts no fetch address.
Rendering has two concurrent slots, a two-second context deadline, a 2 MiB
UTF-8 input limit and an 8 MiB HTML output limit. PDF previews
request 64 KiB ranges with a 2 MiB total budget. Unsupported, unavailable or
over-budget documents keep a filename and icon that still open the viewer.
Each PDF serializes its range reads, so PDF.js request bursts cannot exhaust
the engine's four concurrent media readers while two previews are active.

Generated first-page PDF previews are cached as PNG thumbnails in IndexedDB.
Keys include engine, account, conversation, source message/catalog revisions,
source address, pixel density and renderer version. Reusing a valid thumbnail
performs no PDF range reads or PDF.js page rendering. The persistent cache uses
LRU eviction with 64 entries, 16 MiB and seven-day expiry; a separate memory
fallback holds at most 16 entries and 4 MiB. Storage failures leave preview
rendering usable. Invalid thumbnails are discarded and regenerated through the
existing bounded reader. These disposable caches contain no original PDF bytes
and do not change downloaded-original retention. Opening the full viewer still
reads the original independently of the preview cache.

Opening the viewer explicitly starts its independent original-file read.
Text is limited to 2 MiB and PDF to 100 MiB. The engine
checks the retained canonical reference, extension, media type and complete
resource size before exposing it. Byte ranges, cancellation and the existing
public-network policy apply. Codeberg Markdown source-page links resolve to
the same repository's raw file before those checks. The retained catalog URL
and original-address action still identify the shared source page. There is
no arbitrary provider-URL proxy.

The viewer uses a compact filename header with download, original-address and
close actions. Text supports UTF-8 and UTF-16 with a byte-order mark. Invalid
text encoding produces a readable failure with the original-address action.
PDF has page navigation, fit, zoom and a selectable text layer. Closing the
viewer or leaving the screen releases pending reads and rendering resources.
Browser downloads preserve the original bytes and filename.

These reads do not add a retained engine download, calculate an integrity
digest, make a file available offline, or extract its contents into search.
Document-text extraction remains in the backlog. Office conversion, PDF
annotation/forms and a password-entry flow for protected PDFs are outside this
viewer milestone.

## Components and assets

The selected Goshtoso version supplies public `modal.Dialog`, `card`, `button`
and `link` components. Oie composes their slots with application-owned document
content. Goshtoso does not provide a PDF decoder in the selected version.
Mozilla PDF.js 6.5.438 supplies its public display API, worker and text layer.
Its assets load from the BFF when a PDF preview enters view or a PDF is opened. No PDF scripting
sandbox or third-party CDN is used. The selected assets, licenses and pinned
checksums are described in [the third-party notices](../THIRD_PARTY_NOTICES.md).

The shared-content parser version is 2. The existing bounded derived-index
worker reclassifies retained LOG, JSON and XML links as documents while
preserving their catalog UUIDs. No SQLite migration is needed.

## Verification and rollout

The card-preview increment passes the document browser suite, including actual
painted PDF thumbnails, escaped text excerpts, primary-control activation,
viewer actions, focus restoration and English/Portuguese accessibility.
A 4,195,385-byte PDF preview used 66,617 bytes in two range reads. Another PDF
that needed more content stopped below the 2 MiB budget and retained its
clickable fallback. The fixture recorded zero unexpected mutations, browser
errors or external browser requests. The live catalog also rendered text
excerpts and a first-page PDF thumbnail without the separate Open document
button, using only scoped GET requests.
The final card-preview source passes `make check`, the full native race suite
and local Dagger checks, including application, CLI and mock builds.

`make check` passes on the final dependency pin, with Vacuum's rule timeout
set to 30 seconds. The final web package also passes the race detector in
7.776 seconds. Backend application, provider-reader and HTTP package race
checks passed before rollout.

The document browser fixture passes actual two-page PDF.js rendering, selectable
text, page/zoom controls, original downloads, text escaping/encoding, failures,
retry, cancellation, browser history, drawer focus and English/Portuguese
responsive accessibility. It runs with native PDF viewing disabled and reports
zero page errors, external browser requests and mutations. The shared-content,
video and image-preview regression suites also pass. Logs are
`/tmp/oie-documents-final.log`, `/tmp/oie-documents-shared-aligned.log`,
`/tmp/oie-documents-video-final.log` and
`/tmp/oie-documents-previews-final.log`.

The lifecycle test exposed a queued Goshtoso Dialog effect calling `showModal`
after removal. Application cleanup alone still reproduced it. The generic
connected-element guard is published in
[upstream PR 377](https://github.com/araihu/goshtoso/pull/377) and included in
[the consumer integration PR 375](https://github.com/araihu/goshtoso/pull/375).
Oie pins `v0.6.2-0.20261108232401-14f588cfc5a5`. The strict document fixture
passes after that update.

A bounded Dagger attempt on the document transport snapshot passed contract
lint, golangci-lint, generation checks and native tests, then exceeded its
360-second limit during the full race suite. It exited 124. That run predates
the final modal dependency pin, and is not a completed final CI validation.
Its log is `/tmp/oie-documents-dagger.log`. The full `make ci` browser pipeline
was not run; the relevant browser suites ran locally against isolated fixtures.

The live engine was updated once to API 0.15 while retaining its identity, all
5,759 then-stored messages and the same clean migration version. Air now
supervises only the BFF on port 8080. A read-only live browser check found ten
document cards, an active development reload marker, no original-file or PDF
library fetches before activation, no mutations and no JavaScript errors. The
log is `/tmp/oie-documents-live.log` and screenshot is
`/tmp/oie-documents-live.png`. See
[the frontend development loop](frontend-development.md) for supervisor usage.

The Markdown increment reuses Margo's public Compile, Render and RenderHTML APIs.
The host enables intermediate authored HTML so placeholders such as `<user>` do
not reject an entire changelog, then applies its static HTML allowlist before
any content reaches the browser. It never serves the intermediate fragment or
initializes its runtimes. No dependency update or upstream change is required.

The Markdown implementation passes `make check`, including lint, OpenAPI
validation and generated-model drift checks. The document browser suite passes
rendered Markdown headings/lists/code, plain-text fallback, unchanged original
downloads, PDF regressions, abort/retry, drawer focus and EN/PT accessibility.
Native tests verify static HTML projection, active-content removal, bounded
inputs and Codeberg URL identity handling. Read-only live checks render the
shared Tusky CHANGELOG in the card and viewer at 390 and 1211 pixels, with 84
viewer headings and no browser errors or engine mutations. An index-only
source export also builds and passes the complete document browser fixture,
independently of other pending UI changes. A consistent SQLite
backup preceded the local engine update; no migration was needed.

The Dagger run completed checks, race tests and application/CLI/mock builds in
the remote engine logs. Its client timed out after 180 seconds with exit 124,
so this is not a successful client completion. Evidence is recorded in
`/tmp/oie-markdown-dagger.log` and `/tmp/oie-markdown-dagger-remote.log`.

The PDF preview-cache increment passes `make check` and the document browser
suite, including an index-only source build. Browser checks cover fragment and
reload reuse without PDF range reads, account/revision isolation, corrupt PNG
recovery, byte/count/expiry eviction and storage-denied memory fallback. The
existing viewer, PDF, Markdown, download and accessibility checks still pass.
Read-only inspection of the real shared PDF confirms identical cached pixels
and zero additional source reads after reload and fragment navigation. Logs
are `/tmp/oie-pdf-preview-cache-browser.log`,
`/tmp/oie-pdf-preview-cache-index-browser.log` and
`/tmp/oie-pdf-preview-cache-live.log`.

Dagger's engine completed checks, race tests and application/CLI/mock builds.
The client timed out after 180 seconds with exit 124, so CLI completion is not
claimed. Evidence is `/tmp/oie-pdf-preview-cache-dagger.log` and
`/tmp/oie-pdf-preview-cache-dagger-remote.log`.
