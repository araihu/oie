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

## Integration boundary

This increment is on `codex/shared-fuzzy-search` in its own managed worktree.
The main thread's unfinished video work, data directories and live processes
were not changed. Apply the source changes after coordinating that work and
regenerate bindings from the combined OpenAPI and SQL sources; never resolve
generated-file conflicts by hand. The main live preview does not contain this
increment yet.
