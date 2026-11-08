# Document viewer

The Documents catalog and a conversation's Documents panel can open PDF and
plain-text attachments inside Oie. TXT, LOG, Markdown, CSV, JSON and XML appear
as plain text. Their content never becomes HTML or executable JavaScript.
Other document types retain an action to open the original provider address.

Opening a supported document explicitly starts its read through the engine.
Mounting a card, loading another catalog page or receiving a live update does
not fetch the original. Text is limited to 2 MiB and PDF to 100 MiB. The engine
checks the retained canonical reference, extension, media type and complete
resource size before exposing it. Byte ranges, cancellation and the existing
public-network policy apply. There is no arbitrary provider-URL proxy.

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
Its assets load from the BFF only after a PDF is opened. No PDF scripting
sandbox or third-party CDN is used. The selected assets, licenses and pinned
checksums are described in [the third-party notices](../THIRD_PARTY_NOTICES.md).

The shared-content parser version is 2. The existing bounded derived-index
worker reclassifies retained LOG, JSON and XML links as documents while
preserving their catalog UUIDs. No SQLite migration is needed.

## Verification and rollout

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
