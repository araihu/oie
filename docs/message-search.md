# Retained message search

This increment searches the selected engine's retained message text and available
metadata. It does not claim complete provider history, offline device coverage,
OCR, document extraction, transcription, retraction support or live enrichment.
Conversation-list title filtering remains separate.

## Surface brief

Primary task: find a saved message, inspect its source and open its current
canonical message in the bounded timeline. The existing Oiê identity, system
font, compact rows and semantic color tokens stay in use. Search is a public
Goshtoso Drawer alongside the current conversation, with the same form and
results available at `/search` for native GET navigation. Query/filter changes
replace only results. Result links use existing conversation context navigation.
Motion is limited to the library drawer; results do not animate into place.

The selected Goshtoso version is `v0.6.2-0.20261109092607-b79c3a7a8527`.
Public `search.Search` provides a client-ranked command palette and JSON item
loading, which does not own the engine-scoped history cursor contract. Compose
`drawer`, `textinput`, searchable `combobox`, `checkbox`, `link`, `button`,
`badge`, `spinner` and `emptystate` instead. Native account selection uses public
Radio inputs inside a noscript fieldset. No dependency internals or copied
component DOM are used. App-owned code owns debounce, request scope, history and
bounded result pagination.

## Invariant ledger

| State | Action | Response and preserved context | Effect count |
|---|---|---|---|
| Blank query | Open or clear search | Local empty state; no search operation or enrichment; workspace/draft stay mounted | 0 |
| Valid query | Type, submit or change scope | Scoped retained results; initiating input/caret and one live stream remain | 0 |
| History off | Search a corrected message | Only current retained content matches | 0 |
| History on | Search a retained earlier version | Prior-version label and matched excerpt; link opens canonical message UUID | 0 |
| Historical source jump | Activate result | Close overlay; bounded canonical context, never install old text in current bubble | 0 search effects |
| Foreign or stale cursor | Request next page | In-shell error with restart/retry; no foreign or unscoped hits | 0 |
| Held older query | Type a newer query/filter | Older response rejected before swap; current controls and results agree | 0 |
| Transport failure | Search or load another page | Visible retry; valid filters/query and already shown pages retained | 0 |
| Engine changed | Receive an old search response | Clear old results and reject stale scope; no automatic engine fallback | 0 |
| Search navigation | Source then Back | Query/history/account/conversation restored from addressable search context | 0 search effects |
| Native browser | GET `/search` and submit | Localized shell, controls, source links and explicit retry remain usable | 0 |
| Drawer open | Inspect results | Results do not emit read receipts or request provider history/profile/media | 0 |

Queries are limited to 200 Unicode code points. Include edit history is off by
default and independent of archive provenance. Excerpts are escaped plain text,
not trusted HTML or pre-highlighted fragments. Retraction/deletion controls are
outside this increment; no Include retracted control is introduced.

## Validation record

Required checks include scoped BFF/HTTP tests, independent browser fixtures,
390px and 1440px, Goshtoso and Minimal, light/dark, English/Brazilian Portuguese,
keyboard/touch, real transport failure, stale requests, bounded cursor loading,
source Back navigation, no horizontal overflow and axe scans. Results and
unavailable checks are recorded by the implementation handoff after verification.

The matched content revision can differ from the canonical transport revision
after sending or cancellation. The explicit historical flag identifies earlier
content. Result ordering follows engine UTC millisecond precision, then UUID.

The browser keeps search request state separate from inbox `q`. Account selection
uses the public `combobox:load` event. The native account radio fieldset is disabled
when JavaScript initializes, including after HTMX parses a full application
fragment. Only `/search` accepts `nativeAccountId`; fragments always use the
explicit `accountId` contract.

Source navigation carries an engine-bound pending search intent through a pane
swap when the user immediately reopens search. New typing and scope choices win.
Closing search cancels pending reads and pagination, and ignores the native
search-input clear event that follows Escape. No deferred restore can reopen a
closed drawer. Engine replacement requires the explicit restart action before
rebinding the search page.

Focused validation: web package tests and race tests pass. Scoped
`golangci-lint` passes. Independent browser QA verified global/account/chat
queries, paginated results, prior-version labels with canonical bounded source
jumps, Back, held older queries, transport failure and retry, Escape cancellation,
rapid source navigation, explicit engine recovery and native GET. Eight actual
drawer layouts passed axe at 390px and 1440px in both designs and light/dark.
Mobile and desktop screenshots were reviewed. The coordinator records full
Dagger CI and the isolated development-XMPP integration result separately.
