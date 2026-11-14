# Cache decorators

AGREED: cache policy is a cross-cutting concern. Data loaders and resource
handlers retain ownership, validation and rendering logic. Decorators own
reuse, expiry, eviction, cancellation sharing and HTTP cache policy.

## Browser results

`OieCache.decorate` wraps a loader accepting plain input and an AbortSignal.
Its per-page RAM tier uses the locally served browser build of `lru-cache` 11.2.6, with at most
128 entries, 16 MiB of accounted payload/key size, and 2 MiB per entry. The
content budget is not a measurement of the browser's complete JS heap.
Default TTL is 30 minutes, without renewal on access. Expired entries are
removed lazily; hard byte/count limits bound retention while idle. There
are no timers per entry. Cache failures use a separate 30-second TTL.
Explicit refresh bypasses a cached failure. A successful plain-text fallback
for a failed Markdown render also uses a 30-second TTL, permitting recovery.

The shared decorator deduplicates identical loads, bounds active jobs at 16,
and removes abort listeners on completion or cancellation. A caller leaving
cancels only its subscription. The underlying work is aborted when its last
subscriber leaves. DOM nodes, object URLs, Request/Response objects, workers
and other non-data instances are rejected from cache retention. Blob URLs are
created for mounted previews and revoked after load, failure or cancellation.
PDF workers and range transports are disposed by their renderer.

Document preview keys include engine, account, conversation, message, catalog
item, both revisions, source address, renderer version and output density.
Plain-text excerpts, static Margo Markdown fragments and generated PDF PNG
thumbnails use the same IndexedDB storage adapter. They share a seven-day,
64-entry, 16-MiB device cache. Plain-text fallbacks for failed Markdown renders
stay in RAM for 30 seconds. When a card enters view, a bounded conditional range
request checks the cached representation's ETag. A 304 reuses the rendered
preview without transferring source bytes. A changed representation is rendered
from the bytes returned by the conditional request. If the source omits an ETag,
the renderer can reuse a preview after comparing the digest of its complete
input. This covers the bounded text excerpt and PDFs that fit in the initial
range. Larger PDFs without an ETag render again within the range budget.
Existing entries without validators are regenerated when they are next used.

The LRU library applies TTL and eviction inside transactions. Database
connections are shared, closed on version change or page exit, and bounded by
a store registry. Persistence failures fall back to RAM or normal rendering.
Invalid entries are discarded and regenerated. Cached Markdown markup is
checked against the static preview allowlist before it enters the page.
This cache is distinct from drafts, pending submissions and retained files.

## HTTP representations

`cachehttp.Policy.Wrap` decorates an HTTP handler without buffering its body
or keeping a server-side result cache. It runs the handler first, allowing
current ownership and representation validation to fail normally. Only tagged
successful GET/HEAD representations without Set-Cookie receive cache policy.
Errors, missing validators and commands remain `no-store`.

The decorator handles weak If-None-Match comparison, lists and wildcard tags.
Matching responses become bodyless 304, with Content-Length/Content-Range
removed. ResponseController still unwraps for deadlines. A conditional stream
ends before transferring the body. HEAD retains metadata without a body.

| Resource | Policy and integration |
| --- | --- |
| Document source ranges and inline originals | With valid engine scope and a provider ETag, `private, no-cache`; browser storage is allowed but each reuse requires revalidation. Downloads, missing scope/validators and failures remain `no-store`. |
| Retained avatars | Existing private immutable hash URLs, now using the shared HTTP decorator. |
| Retained image previews, site icons and video posters | Same private immutable hash policy through the shared image handler. |
| UI scripts/styles and cache runtime | Content ETag plus `no-cache`, permitting browser 304 while checking deployments and development rebuilds. |
| PDF.js display/worker/font assets | Same shared revalidation decorator. |
| Emoji scripts/styles and localized catalogs | Content ETag plus shared revalidation; catalogs previously had no ETag. |
| Conversation pages, SSE, presence, capability snapshots, pending sends and account state | Excluded. Fresh mutable state retains its existing behavior. |

The browser contract is `api/cache.openapi.yaml`, with generated models/spec,
Vacuum validation and generation-drift checks. Engine original-stream contracts
and provider-network policy remain unchanged. A provider ETag is a validator,
not proof of a verified file hash. Server-generated document thumbnail storage
and HTTP serving of those derived thumbnails remain a possible later adapter;
the current implementation stores derived previews in the browser.

## Verification

`tests/browser/cache.cjs` verifies TTL, byte/count/entry limits, resource rejection,
engine/account/revision separation, shared cancellation, negative-cache refresh,
active-job limits and a real browser HTTP 304. The document suite exercises
bounded PDF range reads, fragment/reload reuse, changed revisions/accounts,
corrupt persistence recovery, disk quota/expiry, Markdown rendering, focus,
original downloads and English/Portuguese accessibility. HTTP tests cover
conditional GET/HEAD, lists with commas inside tags, failures, changed engine
identity, explicit-download exclusions and shared asset behavior.

The integrated source passes `make check`, native HTTP/web race tests and both
browser suites. The browser reports a 15,730,918-byte accounted cache after
24 one-MiB insertions, with 15 retained entries and a 16-job concurrency ceiling.
The document fixture reports no unexpected engine writes, external browser
requests or page errors. Dagger engine logs confirm successful checks, full
native race tests and application/CLI/mock builds for the cache increment;
the local client ended with status 143 despite completed engine tasks. Logs
are `/tmp/oie-preview-cache-dagger.log` and
`/tmp/oie-preview-cache-dagger-remote.log`. This is not recorded as a clean
Dagger client completion.

## Integration into the primary development checkout

Integrated into `codex/message-previews` in commit `904d4f7`, preserving local
uncommitted UI and CI work. The compact document cards and source captions remain
in place. `make check` and the cache browser suite pass on that checkout. The
main preview on port 8080 serves the shared cache runtime and returns 304 for a
matching ETag, with no page errors in the read-only startup check.

The complete document browser fixture reaches its final assertion but reports
`refreshInfo` in `stars.js` receiving an undefined page during the pending
`navigation.js` fragment changes. The clean integration source passes that
fixture. The primary checkout's pending CI-runner lifecycle tests also fail
before Dagger starts, with status 5 instead of the expected runner status.
These unrelated edits are retained outside the cache integration commit.
Evidence: `/tmp/oie-cache-main-check.log`, `/tmp/oie-cache-main-browser.log`,
`/tmp/oie-cache-main-documents.log`, `/tmp/oie-cache-main-live.log`,
`/tmp/oie-cache-main-dagger.log` and `/tmp/oie-cache-integration-documents.log`.
