# Search while typing

The shared-content text field searches after 300 ms without input. HTMX replaces
the results and category links, retaining the form, focus and caret. Requests are
cancelled before a different screen loads. A response for an older query is
discarded, including an older SSE refresh. IME composition waits until completion.
The conversation's Media, Links and Documents panel uses the same typing trigger
and has no Search button. Its `infoQuery` remains separate from the inbox's `q`;
new queries clear the conversation pagination cursor. Only the result wrapper is
swapped, so the field, caret and composer remain connected. Closing the drawer
or leaving its shared-content screen cancels pending work. Category links carry
the current typed query, including input whose debounce has not fired yet.

Enter remains a native-form fallback. The global gallery has no Search button.
Selecting an account applies the filter immediately, preserving the typed query
and category while clearing the pagination cursor. Searching account names in
the picker only filters its options. Committing a selection uses the existing
HTMX application-fragment navigation so the active account, sidebar and browser
Back/Forward history stay consistent.

The engine searches all scoped retained metadata before pagination. Comparison
ignores accents and case. Exact substrings and prefixes match first; otherwise
all terms must match metadata words or their prefixes. Terms with letters allow
one edit at lengths four through seven, or two at greater lengths. Adjacent
transpositions count as one edit. Short terms, pure numbers and symbols remain
exact. Ordering stays chronological. Relevance ranking and a dedicated FTS index
remain separate work.

The SQLite adapter registers a deterministic matcher. Existing rows and source
text stay unchanged; no schema migration or catalog rebuild is required.
The query contract is documented in `api/openapi.yaml`, with bindings generated
through the existing tools module.

## Verification

`make check` passes with Vacuum's rule timeout set to 30 seconds. The isolated
real-engine search fixture verifies global and conversation search in English
at 1440 pixels and Brazilian Portuguese at 390 pixels. It covers matches beyond
the first page, typo/accent handling, a single debounced fragment request, stable
input/form/caret/composer, cursor reset, separate inbox/conversation queries,
IME, stale HTTP/SSE responses, category changes, Back navigation and account
isolation. It sends no messages or browser mutations and reports no page errors.
The account-filter browser checks also verify one option commit produces one
request, with no extra document navigation, and retains account history. The
chat-info suite also passes eight responsive/theme/locale variants; the
infinite-scroll suite passes its cursor, cancellation and playback checks.
Logs are `/tmp/oie-drawer-search-check.log`,
`/tmp/oie-drawer-search-browser-target.log`,
`/tmp/oie-drawer-search-info-final.log` and
`/tmp/oie-drawer-search-scroll-typing.log`. The automatic account-filter update
also passes `make check` and the search, shared-content and document browser
suites. Logs are `/tmp/oie-auto-filter-check.log`,
`/tmp/oie-gallery-search-live.log`, `/tmp/oie-gallery-account-live.log` and
`/tmp/oie-gallery-documents-live.log`.

Full Dagger validation remains incomplete. The most recent transport run timed
out after 360 seconds during the race suite, as recorded in
[the document viewer verification](document-viewer.md#verification-and-rollout).
It was not repeated for this browser interaction change. Native checks and the
relevant isolated browser suites passed separately.

## Live preview

The active preview uses this main checkout and its existing engine data
directory. Air supervises only the BFF, compiling `bin/oie-dev-web` and reloading
the browser after successful edits. The engine is separate and retains its
protocol sessions. See [the frontend reload loop](frontend-development.md).

`/tmp/oie-live-web.json` records `mode: air` and the supervisor PID and command.
Do not treat that PID as a standalone BFF or launch another server on its port.
`/tmp/oie-live-engine.json` identifies the separate engine process. The old
isolated search preview checkout is no longer the active executable.
