# Message link cards and image viewing

Agreed behavior carries forward from specification sections 7 and 8. Render
structured link cards and image previews. Fetch small supplied previews
automatically. An original image URL without a supplied thumbnail gets a Load
image action. Explicit loading retains the original and creates a local thumbnail;
the retained original can then open in an image viewer. Generating an automatic
thumbnail must not trigger an original-file download.

This increment starts with HTTP/HTTPS URLs in retained message text, up to three
per message. Link enrichment uses bounded HTML metadata and small page images.
Shared references retain account, conversation and message identity. This is not
attachment sending, arbitrary document transfer, global catalog search, or complete
XMPP attachment metadata support.

## Download transitions and invariants

| Current original state | Explicit load | Background behavior |
| --- | --- | --- |
| not_downloaded | Queue one download | Never fetch original automatically |
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
preview state, an idempotent original-image ensure operation, local image reads,
and message.preview_changed events. Implementation and acceptance verification
are in progress. No completion claim follows from the contract alone.
