# XMPP profile images

The engine retrieves published account, direct-peer and saved-room images. It
tries PEP avatar metadata/data first and falls back to inline legacy vCards.
Retrieval runs in the background, shares the history query gate and paces requests.
Provider errors preserve the last cached image. An explicit published removal
restores initials. Missing or inaccessible images also use initials.

The API contract is version 0.4.0. Account-scoped, paginated avatar metadata is
available at `GET /api/v1/accounts/{accountId}/avatars`. Images are served by
`GET /api/v1/avatars/{hash}`. A `profile.avatar_changed` event identifies updates
and removals. Stable entity UUIDs, immutable revision UUIDs and account isolation
follow the existing engine conventions. Provider digest tokens stay internal.

SQLite migration 004 stores identity, revision and image hashes. Image bytes live
under `media/avatars` in the engine data directory. The cache accepts bounded
inline PNG, JPEG and GIF input, validates provider SHA-1 digests when present,
and generates static PNG thumbnails up to 256 pixels. Files use SHA-256 content
addresses, private permissions and atomic replacement. Cache reads verify hashes.
External image URLs are never fetched. The browser receives only verified PNGs
through the BFF, with private immutable caching and conditional requests.

The interface uses images in the account switcher, connection details,
conversation list, header and conversation details. Direct-peer message bubbles
can use the peer image. Saved-room speakers remain room-scoped nicknames, without
verified personal addresses. Oie does not guess their identities or display the
room image as a participant image. Room joining and participant discovery remain
separate work.

Live PEP events and vCard presence hints invalidate known profile identities.
Unknown publishers and room-occupant presence cannot overwrite room images.
Reconnect refreshes available profiles; provider failures retry after five minutes.
Metadata polling is a fallback for providers without update notifications.
Profile events preserve drafts, focus, scroll position and open account forms.

Verification covers file bounds and integrity, metadata/revision isolation,
explicit removal, API ETags, BFF proxy validation and reactive browser behavior.
The development Prosody integration publishes fixture avatars and verifies
retrieval, live changes, removal and reconnect. No real-account publication is
part of this test.

References: [PEP avatars](https://xmpp.org/extensions/xep-0084.html),
[vCard avatars](https://xmpp.org/extensions/xep-0153.html),
[entity capabilities](https://xmpp.org/extensions/xep-0115.html).
