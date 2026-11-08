# Link-card fallbacks

The shared link-card component serves messages and the global/per-conversation
catalog. It keeps the page title primary and displays the actual destination
domain once. Missing metadata falls back to a decoded, bounded URL path and
domain; the clickable destination keeps the full original URL.

Cards without an image do not allocate an image column. Missing or broken
images fall back to a locally cached site icon, then a decorative local link
icon. The engine fetches declared site icons and a conventional favicon through
the existing bounded public-address/redirect policy. PNG, JPEG, GIF and PNG
entries embedded in ICO files are supported. SVG and DIB-only ICO files fall
back to the local icon. The browser never uses provider URLs for these images.

The implementation composes Goshtoso's public Card body, Link, Dropdown and
Button components. Metadata is rendered as text. The original message and its
copy action are unchanged. Enrichment updates the existing card through SSE.

## Explicit refresh

API version 0.14 adds `POST /api/v1/previews/{previewId}/refresh` and the optional
`MessagePreview.faviconHash`. The refresh operation verifies that the link still
belongs to the current retained message before queuing work. Pending requests
share the persisted refresh intent. An optimistic revision prevents an older
worker from overwriting it, while cache invalidation prevents an older in-flight
fetch from repopulating the metadata cache.

More options exposes the localized refresh action. The BFF requires CSRF and the
current engine identity. Existing metadata remains visible while refreshing and
after failures; canonical completion clears stale pending/error feedback. The
operation does not edit a message or download an original image.

Favicons are normalized and hash-addressed outside SQLite. Their references and
the refresh flag use the existing backward-compatible preview JSON payload, so
this change requires no database migration or modified SQL query.

## Integration

This feature is isolated in `codex/link-card-fallbacks`, based on `6308b24`.
It does not restart or replace the main session's live preview or engine.
Concurrent video work in the original worktree is intentionally preserved.

When integrating alongside that work, combine the OpenAPI/source changes and
regenerate clients with `make generate`; do not hand-merge generated Go or CLI
files. Keep the video source guards in the preview worker and use API version
0.14 or newer for the combined contract.

## Verification

- `make generate`, `make check` and `make build` passed.
- Race tests passed for preview fetching, HTTP API, web and application packages.
- The isolated link-fallback browser suite passed 16 combinations of viewport,
  theme and locale with no axe violations, external browser requests or
  unexpected mutations. It covers broken images/icons, URL-only cards, CSRF,
  refresh coalescing, same-node SSE updates and failure preservation.
- Existing shared-content and preview browser regression suites passed.
- Dagger CI was attempted with `make ci-check`. One attempt reached race tests
  before the 120-second execution limit. The final 240-second attempt stalled
  while connecting to the Dagger engine and ended with termination status 143
  from make. A complete Dagger run remains unverified.

The local check used a separate temporary directory for the linter lock because
the original worktree was running its own linter. The browser suites used the
existing, matching browser dependencies through `NODE_PATH`; they did not
change the original worktree or its dependencies.
