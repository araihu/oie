# Video playback increment

The browser uses its native HTML5 video controls. This increment introduces no
bundled third-party player or codec dependency. Actual codec support belongs to
the browser; a supported container does not guarantee that every codec plays.

`GET /api/v1/shared-content/{itemId}/content` opens a current retained video
reference through the owning engine. It accepts one byte range and a strong
entity tag or HTTP date through `If-Range`. A provider that ignores Range
returns a complete 200 response. Valid partial responses retain their byte
positions and complete size. Unsatisfiable selections return 416 with a known
complete size. HEAD returns 405 without a provider request.

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

The reader accepts `video/mp4`, `video/webm`, `video/ogg` and
`video/quicktime`. It requires a known complete size no greater than 2 GiB,
identity encoding and consistent range metadata. Unknown-size responses,
HTML, multipart ranges, playlists, HLS/DASH manifests, transcoding and codec
conversion are not supported. DNS results and redirect destinations follow the
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
