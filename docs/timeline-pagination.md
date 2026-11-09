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
