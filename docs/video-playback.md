# Video playback increment

Oiê starts received videos with native HTML video controls. After Play, a decode
error or an advancing clock without a picture can switch the card to a local
libmedia player. It reads the same scoped original in byte ranges. The player
and the required WebAssembly decoders load on demand from this Oiê instance.
Versioned decoder assets can be reused from the browser cache. The application
does not store a converted file. Slow devices can download the original.

An eligible PeerTube single-file fragmented MP4 can use Media Source with
the browser's codecs. After Play, the engine reads the same-origin PeerTube
metadata and the playlist for the exact rendition. It requires the original
URL to match the video metadata and a finite, contiguous index for the full
original. It gives the browser only the codec identifiers and bounded byte
ranges and durations. The browser requests those ranges through Oiê and can
seek immediately beyond its buffered data. If indexed startup fails, Oiê tries
the regular native player and may use the on-demand decoder.

`GET /api/v1/shared-content/{itemId}/content` opens a current retained video
reference through the owning engine. It accepts one byte range and a strong
entity tag or HTTP date through `If-Range`. A provider that ignores Range
returns a complete 200 response. Valid partial responses retain their byte
positions and complete size. Unsatisfiable selections return 416 with a known
complete size. HEAD returns 405 without a provider request.
An explicit `index=true` request returns the seek index when eligible and
cannot be combined with byte ranges or downloads.

The catalog supports `type=video` and up to 100 unique `messageId` filters when a
conversation is supplied. Both filters apply before pagination. The effective
catalog kind includes existing image metadata, so a URL recognized as an image
cannot consume a video-only page. A playback request checks its current
canonical message and reference again. Removed references, correction aliases,
missing sources and non-video items cannot start network retrieval. Retraction
and deletion operations remain outside the implemented engine scope.

Playback starts only after explicit user activation. Listing media, opening a
conversation, and mounting a player do not authorize provider requests. Known
video, audio and document URL references are excluded from automatic link
preview requests; legacy queued preview work for those references becomes
unavailable without fetching. A browser must receive only same-origin engine
or BFF media URLs. It must not receive provider redirects or use provider URLs
as video sources.

Gallery cards, including the conversation's shared-media panel, show the video
player without a filename caption or routine playback instructions. Errors and
the retry action remain visible when playback fails. The filename remains
available to existing metadata and download actions. These cards do not repeat
the source-message link in their footer; it remains in the chevron menu.
Browser checks cover hidden captions during playback, live metadata refresh,
retry and restored gallery history, while errors remain visible and listing
cards still starts no video transfer.

The reader accepts `video/mp4`, `video/webm`, `video/ogg` and
`video/quicktime`, plus Matroska and AVI. It requires a known complete size no greater than 2 GiB,
identity encoding and consistent range metadata. Unknown-size responses,
HTML, multipart ranges, arbitrary HLS/DASH streams, transcoding and codec
conversion are outside this reader's scope. Eligible MP4 or M4V video URLs
served as `application/octet-stream` use the MP4 MIME type for playback. DNS results and redirect destinations follow the
same public-address policy as preview retrieval. Connections use verified
literal addresses; proxies, cookies, authorization and referrer forwarding are
disabled for media reads.

Resource bounds are four concurrent readers, two connections per provider,
five redirects, 32 KiB response headers, five-second response-header/TLS
handshake timeouts and a fifteen-minute read context. HTTP writes and flushes
renew a thirty-second downstream deadline. Headers and each bounded chunk are
flushed as available. Closing the player or cancelling navigation must clear
the source and cancel the browser request; downstream disconnect closes the
engine reader and cancels the provider request. Truncated streams end without
appending a JSON problem to video bytes.

Streaming does not retain an original in the media store, set download state,
produce a full-file hash, verify a sender digest, or promise offline playback.
Browser buffering is not an app-owned retained download. Application-managed video saving,
resume across process restarts, video-file sync and persistent video transfer
management require a later retention increment. Existing image download and
hash behavior is unchanged. No send, room join, message revision or outbox
mutation occurs during playback.

## Verified scope

Native `make check`, backend and BFF race tests, and the native-video browser
fixture pass. The browser fixture decodes an original 1502-byte VP8/WebM clip,
checks real playback, pause, seeking, keyboard control and exact Range bytes,
and preserves playback through gallery, conversation and scoped-drawer refreshes.
It covers explicit retries, offline recovery, unsupported content, focus/drafts,
reference removal and cleanup on navigation or drawer close. The shared-content
regression passes all 16 responsive/theme/locale accessibility variants.

Indexed MP4 regression coverage uses a fragmented VP9 fixture and seeks past
the initial buffer. The reported PeerTube 1080p H.264/AAC original also plays
and seeks across its full 53-minute timeline in Chrome using the provider's
byte ranges. Neither MP4 test needs the WebAssembly decoder.

The initial video player source also passed isolated web/API package tests independently
of concurrent working-tree changes. A read-only live check finds three mounted
players with no source, autoplay, video-body requests or mutations before Play.
The compact controls fit at 390 and 1290 pixels. No real provider video was
retrieved during that initial validation. Unsupported formats retain the
original link and explicit retry.

The live engine advertises API 0.13.0. Its existing data directory remains in
place with clean migration version 10; retained messages increased from 5746
to 5754 while the live account received traffic. No migration was required.

Dagger `make ci-check` passed the engine/player increment before the final
runtime-version alignment and compact-control adjustment. The final repeat
was terminated with exit 143 while connecting to the runner. Completion of the
final-snapshot Dagger run remains unverified. Full development XMPP messaging
integration was not rerun for this receive-side media feature.
