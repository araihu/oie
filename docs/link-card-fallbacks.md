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

The main checkout integrated the fallback commits with video playback and
bookmark autojoin in `99f6a29`. Generated OpenAPI, CLI and templ files were rebuilt
from the combined sources. Video source guards remain in the preview worker.

The deployed preview then advanced to `ef77640` in `codex/link-card-fallbacks`,
including the search build that was already live. Its existing wallpaper is
preserved as local `app.css` and `chat-pattern.svg` changes in this worktree.
The unfinished pagination edits in the main checkout remain there and are not
part of the published binary. A diagnostic run against those in-progress edits
failed its SSE scroll-anchor assertion; it is not a validation of this rollout.

Engine and BFF use this worktree's `bin/oie`, with their previous flags and
working directory. The engine retains the main checkout's `data/live-engine`.
The current process records are `/tmp/oie-live-engine.json` and
`/tmp/oie-live-web.json`; use their executable path for later preview updates.
An online backup, previous executables and rollout logs are retained under
`/tmp/oie-link-rollout-byjbgdul`. No database migration was needed. Verification
preserved engine/account identities, all retained source messages and revisions,
stars and outbox rows, with schema 10 clean. The real account reconnected.

A read-only browser check found 39 compact fallback cards and a cached favicon,
opened the metadata-refresh menu and verified search swaps with a stable input.
It observed no JavaScript errors, external browser requests or mutations.
The live screenshot is `/tmp/oie-link-live-preview.png`.

## Verification

The combined preview snapshot passes `make generate`, `make check` and
`make build`. The link-fallback browser suite passes 16 combinations of
viewport, theme and locale with no axe violations, external browser requests or
unexpected mutations. It covers broken images/icons, URL-only cards, CSRF,
refresh coalescing, same-node SSE updates and failure preservation.
The native-engine shared-search, video and shared-content browser suites pass.

`OIE_CI_SSH_HOST=dagger-vm make ci-check` passed through Dagger, including lint,
generation checks, native/race tests and application/CLI builds. Its final log
is `/tmp/oie-combined-final-dagger.log`. Earlier timeout attempts do not affect
this completed result. The full browser pipeline through `make ci` was not run;
the relevant browser suites ran locally against isolated engines and fixtures.

The local check used a separate temporary directory for the linter lock because
the original worktree was running its own linter. The browser suites used the
existing, matching browser dependencies through `NODE_PATH`; they did not
change the original worktree's dependencies.
