# Bounded conversation timelines

The accepted behavior is automatic loading of older retained messages when scrolling up. Opening a source message loads a bounded context page with paging in both directions. Jump to latest remains available. Paging preserves the visible anchor and does not load the full retained archive into the document.

The initial implementation uses pages of 50 messages and a maximum of 200 rendered message rows. It evicts distant rows from the opposite edge while retaining cursors to retrieve them again. Focused controls, open menus and active media need protection when selecting an eviction boundary. If the opposite boundary cannot be trimmed safely, loading pauses rather than growing the document without a bound. The SQLite history itself retains its existing explicit-deletion policy.

## Engine contract

API 0.16 extends the existing `listMessages` operation with optional `direction=older|newer`. Older remains the default; omitting the cursor reads the latest page. Newer requires a retained conversation-scoped message UUID and selects the nearest messages after it. Both directions return newest-first rows. `nextCursor` continues in the requested direction and is the oldest returned UUID for older pages or the newest for newer pages.

Ordering is original UTC creation time at millisecond precision, then message UUID. UUID ordering alone is insufficient. The ascending SQLite query selects a bounded nearest-newer window before the HTTP adapter reverses the page; it does not read the entire newer archive. Canonical correction aliases remain logical message identities. The existing context operation provides the target and bounded neighbors; its extreme UUIDs and availability flags seed outward paging.

These operations read retained local state. They do not fetch protocol archives, join rooms, send messages or emit read receipts. SQL queries are generated with sqlc; this increment adds no schema migration or application dependency.

## Browser and BFF

Pagination belongs to the conversation region, keeping the composer and conversation list in place. It uses app-owned HTML fragments composed from the existing public Goshtoso message components. Paging leaves addressable source URLs and browser history intact, deduplicates messages and rejects foreign or nonadvancing pages. Each mounted route owns its observers, requests and retries, which are disposed on navigation.

Live snapshots must preserve the loaded window. A historical window cannot receive a disconnected latest page across an unloaded gap. Visible revisions and metadata can refresh through bounded reads of the existing interval. New arrivals follow the bottom only at a contiguous live edge; otherwise the shortcut takes the user to a fresh latest page.

Jump to latest replaces the timeline contents and positions the viewport at the bottom before paint, including when older paging left the conversation URL unchanged. Browser Back and Forward continue to restore saved reading positions.

The selected Goshtoso `scrollregion.ScrollRegion` supplies a bounded viewport and visual boundary cues. It does not manage message cursors, keyed message reconciliation or row eviction. Oie retains the existing conversation viewport and composes public ChatBubble and Link controls; the protocol-neutral paging lifecycle remains app-owned.

## Verification

The isolated browser fixture retains 3,000 messages with shuffled UUIDs and
millisecond timestamp ties. It passes both paging directions, the 200-row cap,
eviction and reloading, active-media protection, source context and distant
correction aliases. Viewport anchors stay within three pixels. Historical live
updates preserve the mounted interval, and stale or malformed pages require an
explicit retry. Route changes abort old requests. The mobile Portuguese layout
passes the accessibility checks, with zero unexpected writes or page errors.
The complete fixture passed twice consecutively after the loading-status fix.

The live-chat regression fixture and the full native race suite also pass.
`make check` and the final local Dagger checks pass, including the race suite
and application, CLI and mock builds. The Dagger run used the persistent engine
tunnel after a separate SSH-session transport stalled despite completed remote
execution.
Read-only checks against the running API 0.17 engine verified 50-message latest,
older, newer and context pages, exact scope/order and nearest-neighbor results
while preserving the engine identity. Paging itself did not issue protocol
operations.

Loaded videos use the existing video reconciler for authoritative message
updates. Matching players keep their playback and native focus. Removing the
canonical video reference releases its player. This inner update does not relax
the protections against evicting playing media, open menus or text selections.
The native-video and bounded-timeline browser fixtures pass this combination.
`make check` passes after this video reconciliation fix. The final Dagger engine
completed `make check test-race build build-cli` and the mock build without an
error, as recorded in `/tmp/oie-video-reconcile-dagger-remote-proof.log`. Its
client transport subsequently failed its health check, so that attempt does
not provide a successful CLI completion. The preceding footer-removal Dagger
run did exit successfully.

## Opening a shared source

The enhanced shared-item action starts with 16 messages around the source
and keeps the existing older and newer paging controls. If the destination
uses the mounted conversation scope, the response contains only the
conversation panel. From the global catalog it uses the workspace route and
reuses the parked inbox when its scope matches. Native links and ordinary
conversation navigation use the existing timeline page size.

The source-context engine endpoint provides the owning conversation and the
window's presentation data in one response. The browser server reads the
account list, selected account's avatars and cold inbox separately. It can
still use the individual endpoints with an older engine. The source route
keeps its mounted settings and viewer panels and negotiates gzip for the
fragment. The event stream stays connected while the foreground jump defers
the automatic HTML refresh. Refresh resumes on the destination or after a
failed or cancelled jump.

For a compact source route, Oiê validates the fragment HTMX already parsed
before committing the URL and mounting the workspace. HTMX processes the new
links once. The browser positions the source before the first painted frame.
The message menus use the existing Goshtoso dropdown and initialize on the
first pointer or keyboard activation. Inbox initials need no Alpine scope
until a photo becomes available. When leaving the catalog, only items with
live preview state need their cached markup reopened.

SQLite keeps one writer and a small pool of read-only connections in WAL
mode. The event cursor, timeline and retained metadata can read committed
data while archive imports and indexing write. A reaction batch loads the
requested messages in one read transaction and shares conversation and
account checks across the batch. Cached previews use a read before falling
back to the transactional insert for missing entries.
