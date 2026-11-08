# Message link cards and image viewing

Agreed behavior carries forward from specification sections 7 and 8. Render
structured link cards and image previews. Images automatically download as they
enter view by default. A per-device setting disables automatic originals and
restores the Load image action when no supplied thumbnail exists. The retained
original opens in the image viewer. Other files remain on demand.

This increment starts with HTTP/HTTPS URLs in retained message text, up to three
per message. Link enrichment uses bounded HTML metadata and small page images.
Shared references retain account, conversation and message identity. This is not
attachment sending, arbitrary document transfer, global catalog search, or complete
XMPP attachment metadata support.

## Download transitions and invariants

| Current original state | Explicit load | Background behavior |
| --- | --- | --- |
| not_downloaded | Queue one download | Visible images queue when device auto-download is enabled |
| queued | Reuse queued work | One engine worker claims it |
| downloading | Return current state | Concurrent requests share the transfer |
| ready | Reuse retained file | Never refetch a successful download |
| failed | Queue a retry | Show stable localized failure code |

An interrupted download can resume as fresh queued work after restart. Partial
files never become visible. Store original bytes and normalized thumbnails outside
SQLite, with computed SHA-256 fingerprints. Bare message URLs provide no expected
sender digest, so integrity is labeled computed_only. Original image support is
bounded PNG, JPEG and GIF, up to 32 MiB and bounded decoded dimensions.

All remote requests validate HTTP/HTTPS destinations, DNS and every redirect hop.
They reject private/local destinations, credentials, environment proxies, oversized
responses and unsupported image types. The browser receives plain structured
metadata and local verified images, never provider markup or embedded scripts.
Failure must preserve the original message and usable URL.

API-first contract 0.5.0 defines conversation batch preview reads, individual
preview state, an idempotent original-image ensure operation used by manual actions and device auto-download policy, local image reads,
and message.preview_changed events. The engine and BFF implement this contract.
Unit/race tests, browser workflows and the real-account preview verify the
implemented scope. The highlighted
provider image was explicitly loaded and expanded through the cached-image API.

## Room authorship and avatar fallback

Archive parsing now preserves unambiguous MUC author JIDs supplied by the
validated archive. Matching the authenticated account marks a room message as
own; nicknames alone never do. Migration 005 replays available room archives once
with paced requests to repair previously retained author projections. Archive
aliases preserve message UUIDs, and identity reconciliation creates an immutable
revision without changing text, timestamps, unread state or outbox intent.

The interface renders own messages on the right using the primary bubble color.
Initials-only avatars no longer mount an empty image element, removing the broken
image outline before Alpine initialization and after an avatar is cleared.

## Preview quality

Message previews retain up to 1024 pixels on either axis and use Catmull-Rom
resampling. JPEG sources produce static quality-90 JPEG previews; other supported
sources produce PNG previews. Originals retain their exact bytes. Existing image
previews regenerate from the retained original; link cards refresh their bounded
page preview once. An internal renderer version prevents repeated upgrade work.
The separate profile-avatar cache keeps its 256-pixel PNG contract.

## Image presentation and source details

Standalone image URLs represented by an image preview are omitted from the
displayed message text. Captions, mixed text, code and ordinary article links
remain visible. Stored message text and Copy original remain unchanged.

Image info in the expanded viewer exposes the original URL, filename and
available MIME type and size. Opening info does not start a download.
