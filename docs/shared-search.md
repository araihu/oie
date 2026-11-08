# Search while typing

The shared-content text field searches after 300 ms without input. HTMX replaces
the results and category links, retaining the form, focus and caret. Requests are
cancelled before a different screen loads. A response for an older query is
discarded, including an older SSE refresh. IME composition waits until completion.
Enter and the Search button remain native-form fallbacks. Account choices retain
their existing explicit-submit behavior.

The engine searches all scoped retained metadata before pagination. Comparison
ignores accents and case. Exact substrings and prefixes match first; otherwise
all terms must match metadata words or their prefixes. Terms with letters allow
one edit at lengths four through seven, or two at greater lengths. Adjacent
transpositions count as one edit. Short terms, pure numbers and symbols remain
exact. Ordering stays chronological. Relevance ranking and a dedicated FTS index
remain separate work.

The SQLite adapter registers a deterministic matcher. Existing rows and source
text stay unchanged; no schema migration or catalog rebuild is required.
The query contract is documented in `api/openapi.yaml`, with bindings generated
through the existing tools module.

## Verification

Native `make check` passes. The new browser fixture uses an isolated native
engine, SQLite and BFF; no XMPP account is connected. It verifies matches beyond
the first page, typo/accent handling, debounce, input identity/caret, IME, stale
HTTP/SSE responses, category changes during a request and account isolation at
390 and 1440 pixels. The existing shared-content fixture also passes its 16
responsive/theme/locale variants. Screenshots and reports are under
`tests/browser/artifacts/`.

Dagger `make ci-check` was attempted locally. The first run stopped progressing
after its race/build output and was terminated. A final attempt with a 180-second
limit timed out during generation checks after lint passed. Full Dagger pipeline
completion remains unverified. Logs are `/tmp/oie-fuzzy-dagger.log` and
`/tmp/oie-fuzzy-dagger-final.log`; native checks and browser runs passed separately.

## Live preview integration

The live preview now runs the combined build from `codex/shared-fuzzy-preview`
in `/home/gui/.codex/worktrees/oie-search-preview/oie`, based on the main
checkout's committed `a1e6311`. Search commits were reapplied with generated
bindings rebuilt from the combined OpenAPI, SQL and templ sources. Video,
bookmark autojoin and the growing composer are included.

The main checkout's deployed `app.css` and `chat-pattern.svg` were copied into
this preview worktree to preserve its existing wallpaper. They remain local
changes here; their source hashes are in `/tmp/oie-search-preview-source.json`.
Unfinished pagination work in the main checkout was not included. Its source,
Git state and data directories were left intact.

Both engine and BFF were restarted using this worktree's `bin/oie`, keeping
their original arguments and working directory. The engine still uses the
existing `data/live-engine` directory in the main checkout. Process records
`/tmp/oie-live-engine.json` and `/tmp/oie-live-web.json` identify the active
executable. Future preview updates must account for that executable path.
Previous executable copies and process records are retained under
`/tmp/oie-search-preview-previous-*` and `/tmp/oie-search-preview-rollback.json`.

For this combined build, `make check`, the isolated real-engine search browser
fixture and the video browser fixture passed. A read-only browser check against
the live preview confirmed typo matching against retained video metadata,
results-only swaps, stable input/focus/caret, the preserved video player and no
JavaScript errors. It sent no messages or other application mutations. The
engine identity and all three account identities were preserved, and Suchat
returned to connected status. The check screenshot is
`/tmp/oie-search-preview-live.png`.

The combined-build Dagger attempt failed while establishing client session
attachables, before pipeline checks started. The executor reported
`context deadline exceeded`. See `/tmp/oie-search-preview-dagger.log`.
Native verification logs are `/tmp/oie-search-preview-check.log`,
`/tmp/oie-search-preview-browser.log` and `/tmp/oie-search-preview-video.log`.
