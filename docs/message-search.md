# Retained message search

This increment searches the selected engine's retained message text and available
metadata. It does not claim complete provider history, offline device coverage,
OCR, document extraction, transcription, retraction support or live enrichment.
One global field groups conversation names, saved protocol contacts and messages.

## Surface brief

Primary task: find a saved message, inspect its source and open its current
canonical message in the bounded timeline. The existing Oiê identity, system
font, compact rows and semantic color tokens stay in use. The existing
conversation-list search input opens global results in that same column. Conversation-title matches and saved-message matches share the query.
The original filter controls and list remain mounted while hidden and return
when search closes. A separate toolbar above the timeline searches only the
current conversation. Both queries can remain active together. `/search` keeps
native GET navigation available. Query/filter changes replace only results;
result links use the existing bounded conversation context navigation. Results
do not animate into place.

The selected Goshtoso version is `v0.6.2-0.20261110022421-7c69f01af2ef`.
Public `search.Search` provides a client-ranked command palette and JSON item
loading, which does not own the engine-scoped history cursor contract. Compose
`textinput`, searchable `combobox`, `checkbox`, `link`, `button`,
`badge`, `spinner` and `emptystate` instead. Native account selection uses public
checkboxes inside a noscript fieldset. No dependency internals or copied
component DOM are used. App-owned code owns debounce, request scope, history and
bounded result pagination.

## Invariant ledger

| State | Action | Response and preserved context | Effect count |
|---|---|---|---|
| Blank query | Focus or clear the global field | Show the conversation list; workspace and draft stay mounted | 0 |
| Valid query | Type, submit or change scope | Scoped retained results; initiating input/caret and one live stream remain | 0 |
| History off | Search a corrected message | Only current retained content matches | 0 |
| History on | Search a retained earlier version | Prior-version label and matched excerpt; link opens canonical message UUID | 0 |
| Historical source jump | Activate result | Keep global results visible; bounded canonical context, never install old text in current bubble | 0 search effects |
| Foreign or stale cursor | Request next page | In-shell error with restart/retry; no foreign or unscoped hits | 0 |
| Held older query | Type a newer query/filter | Older response rejected before swap; current controls and results agree | 0 |
| Transport failure | Search or load another page | Visible retry; valid filters/query and already shown pages retained | 0 |
| Engine changed | Receive an old search response | Clear old results and reject stale scope; no automatic engine fallback | 0 |
| Search navigation | Source then Back | Query/history/account/conversation restored from addressable search context | 0 search effects |
| Native browser | GET `/search` and submit | Localized shell, controls, source links and explicit retry remain usable | 0 |
| Search active | Inspect results | Search does not issue reads or provider/media requests; normal visibility tracking remains independent in the displayed chat | 0 search effects |

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
uses the public `combobox:load` event. The native account fieldset is disabled
when JavaScript initializes, including after HTMX parses a full application
fragment. Only `/search` accepts `nativeAccountId`. Fragments use `accountId`
for one account and repeated `accountIds` values for a set.

Source navigation carries an engine-bound pending search intent through a pane
swap when the user immediately reopens search. New typing and scope choices win.
Closing global search cancels pending reads and pagination and restores the
original title filter. Escape prevents the browser search-input clear event from
reopening search. No deferred restore can reopen a closed search. Engine replacement requires the
explicit restart action before rebinding the search page.

Focused validation: web package tests and race tests pass. Scoped
`golangci-lint` passes. Independent browser QA verified global/account/chat
queries, paginated results, prior-version labels with canonical bounded source
jumps, Back, held older queries, transport failure and retry, Escape cancellation,
rapid source navigation, explicit engine recovery and native GET. Independent
column/toolbar acceptance passes eight responsive theme layouts through axe.
Mobile and desktop screenshots are reviewed for each accepted layout. The full
isolated development-XMPP browser workflow also passes with simultaneous queries.
The coordinator records whole-pipeline Dagger evidence separately.

## Unified global field and conversation toolbar

Ctrl+K or Cmd+K opens a compact global dialog in the workspace. It searches
all accounts using the same retained result groups and bounded source links as
the sidebar. The dialog query is temporary and independent of the sidebar
query and filters. Escape closes the dialog and returns focus to the previous
control. The dialog uses Goshtoso's public compact Dialog and the existing
search response because the public SearchModal ranks a client-side result set.

In the dialog and sidebar, Down and Up move focus between results. Up from the
first result returns to the field. Enter opens the focused result or the first
result when the field has focus. Tab also reaches the results and controls.
Within a conversation, Down and Up jump to older and newer matches, just as
Enter and Shift+Enter do.

The existing `conversation-search` field opens global search when the user enters
a non-blank query. Focusing the field keeps the conversation list visible.
The X at the right of the field cancels the search and returns focus to the field.
Clearing the query also returns to the list and cancels pending search requests.
Closing clears the search from the current URL and the mounted workspace links
so selecting another conversation or inbox filter keeps search closed.
The edit-history help closes on outside interaction or Escape without changing
the query or the checkbox.
The searchable account picker filters conversation, contact, and message matches
to the checked accounts before pagination. With no boxes checked, search covers
all accounts. The selection belongs to the search URL. Category links pass the
query and a single checked account to the shared-content gallery. With multiple
accounts checked, the gallery opens with all accounts.
The All, Conversations, and Messages filters switch result types in place and
keep the query and account selection. The All view includes contacts.
It uses the native unified `/search` GET page as its fallback. Global mode
shows categories for the existing media, links and documents catalog, matching
saved roster contacts, bounded conversation-title matches and saved messages.
Conversation matches show the conversation avatar. Message matches show the
author's avatar from the owning account when the identity and thumbnail are
available, with initials as the fallback. Search uses retained images and does
not request profiles from the provider.
The avatar ring and author name use the timeline's sender palette. Search hits
carry the source conversation address and the account or peer address when
applicable. A room participant's color uses the room address and nickname.
Engines that omit these fields use a fallback based on the conversation ID.
Message results lead with the author, followed by the account and conversation,
the excerpt, and the timestamp. Address-based labels use the local part as the
name and show the domain separately. Browser timestamps use the local calendar
day and show Today or the date before the time.
It leaves the original conversation list, title query, filter controls and scroll
mounted behind the results. Closing restores that list and query without
reopening search through its own focus restoration.

Global state uses `searchMode`, `searchQuery`, `searchAccount` and
`includeEditHistory`. Conversation search uses `chatSearch`, `chatQuery` and
`chatIncludeHistory`. Both remain visible together. Same-conversation source
navigation retains both queries. Navigating to another conversation clears the
previous conversation query. Back restores its earlier addressable context.

The conversation toolbar searches newest matches first. Older and newer controls
navigate retained results without wrapping. Its loaded-result indicator does not
invent a total before pagination ends. Selecting a match uses the existing scoped
context loader and retains the composer, toolbar and global search column. The
context loader keeps its existing 50-message page and 200-message DOM bounds.
Highlights decorate visible plain text; original message content and plain-text
copy remain unchanged. Historical excerpts stay explicitly labeled and never
replace the canonical current bubble.

Contacts come only from the persisted protocol roster directory. Unknown or
unavailable observations do not become a known-empty contact list. Cached
contacts remain searchable while disconnected. Search does not request a roster,
subscribe to presence or infer contacts from room occupants. An existing contact
conversation opens directly. A contact without one opens the existing new-chat
form with its account and address prefilled; creation still needs explicit form
submission. Category links use the existing catalog and its device preferences.
Search does not fetch shared-content thumbnails or enrich remote previews.

Whole-pipeline validation completed on `ef9a9a6` with exit 0 and all 39 browser
workflows, including the isolated real-engine XMPP workflow. Native `make check`
also passes. Back/Forward restores both the visible query and category links;
details-search links retain their visible localized name and native account/chat
scope. The live preview serves the verified styles and both search controllers,
with its existing account connected. See [local CI](local-ci.md) for the exact
source and evidence directory.
