# Conversation details

API 0.9.0 adds `GET /api/v1/conversations/{conversationId}/details`. It combines the canonical owning account connection with available room metadata or observed direct-peer presence. Details do not edit retained conversations or message history. Name, description, topic, occupancy count and room properties are shown only when supplied by the protocol. Explicit zero occupancy is distinct from an absent count.

The XMPP adapter reads [XEP-0045](https://xmpp.org/extensions/xep-0045.html) room information through [XEP-0030](https://xmpp.org/extensions/xep-0030.html). Only typed room-info data forms and normalized room flags enter the application port. Visible occupants are read from room discovery items if permitted. Items must name an occupant of the exact requested room; item nodes, bare personal addresses and other rooms are excluded. Display names and room-scoped opaque IDs never establish a verified personal identity or authorize avatar lookup. A discovery list is not a persistent member roster and does not establish presence.

The own account connection, room session and participant presence are separate states. The current room session is `not_joined`; this increment does not join rooms. A server can report occupancy while withholding its occupant listing. That remains a partial or forbidden snapshot, not an empty room. Direct-peer presence changes only after authorized traffic from a known peer. Unknown is never inferred offline; an unavailable phone does not override an active second device. Presence requests, subscriptions and probes are not sent.

Details are session-local observations. A GET returns immediately and deduplicates a background room-info/items refresh to at most once per minute for each requested conversation. The worker uses the existing serialized IQ gate and a thirty-second budget. Cache limits are 256 conversations, 200 occupants per room, 256 peer identities and sixteen resources per peer. Oversized/invalid lists remain partial. Disconnect/reconnect clears observations, and obsolete workers cannot populate the current session. No SQLite migration or new root dependency is needed.

The existing details action opens a localized Goshtoso Drawer composed from public Panel, Avatar, Badge and Link APIs. Room participant avatars are initials only. Section digests preserve identity DOM, focused controls, drawer scroll and the composer draft during unrelated updates. Retry/Refresh uses the same account/conversation URL; it does not promise a synchronous probe. User text is escaped plain text.

| State | Read/action | Expected result | Preserved context | Effects |
|---|---|---|---|---|
| Connected room | Open/refresh | Available metadata plus visible-occupant scope | Conversation/account, draft, language/theme | Bounded reads, zero joins/messages |
| Occupancy known, list withheld | Read | Count retained; partial/forbidden list | Same room | Zero invented members/presence |
| Direct peer without observations | Read | Unknown presence | Canonical peer | Zero probes/subscriptions |
| One peer device unavailable | Presence update | Another active resource remains visible | Peer identity | Cache observation only |
| Disconnected | Read | Account state retained, current metadata unavailable | Retained conversation/history | Zero network work |
| Wrong room/namespace or oversized list | Parse | Reject/limit observations; mark partial/unavailable | Exact account/room scope | Zero cross-room identities |
| Previous session finishes | Cache completion | Ignore obsolete observation | Current connection snapshot | Zero stale writes |
| API missing/error/foreign projection | Retry | Visible scoped recovery | Shell, drawer context, draft | GET only |

## Verification on 2026-11-08

`make check` passes, including vacuum without warnings, golangci-lint without findings, generated-contract checks, vet and native tests. Engine/API/XMPP and web race tests pass. The CLI exercises scoped detail reads. Development Prosody integration creates a separate fixture room, verifies metadata and unchanged occupancy while Oie reads details without joining, confirms hidden occupants stay partial, and checks cached reads and disconnect clearing. Unit tests cover typed room forms, Unicode/plain text, rejected room IDs/nodes, bounded lists, multiple presence resources, malformed priority, snapshot copies and obsolete-session queue isolation.

`tests/browser/conversation-details.cjs` covers the ledger across sixteen Goshtoso/Minimal light/dark EN/PT variants with zero WCAG A/AA violations. It verifies error/legacy/foreign projections, unknown and explicit unavailable presence, partial/forbidden listings, exact room scope, safe initial avatars, keyboard/Escape behavior, and live focus/scroll/draft preservation. Existing live-chat, capabilities, avatars and replies regressions pass. The real-engine messaging browser workflow passes. The Dagger browser stage includes the details fixture.

An online backup preceded the live update. Source IDs and exact message bodies, revisions, stars, outbox and engine identity remained intact; schema 7 is unchanged. The existing real account returned room name, description and 106 visible occupants; no topic was advertised in that observation. A read-only browser check confirmed the live participant list, initials-only participant avatars, separate account/room states, scrolling and accessibility, with no browser mutations.

The bounded full `make ci` attempt timed out after 180 seconds with exit 124 before returning Dagger stage results. Successful native and browser checks are not a claim of full Dagger completion.
