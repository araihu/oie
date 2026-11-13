# Media gallery scrolling

The global Media and Links gallery and the conversation's media panel load older items as the user approaches the bottom. The existing engine cursor API supplies each page. The BFF returns HTML fragments, so no new engine operation or browser protocol client is needed.

Pages append to the current collection without adding a cursor to browser history. Account, conversation, category and search filters remain in effect. Month headings use the device timezone. A native pagination link remains available without JavaScript and becomes an explicit retry after a failed request.

Each response must match the engine, collection and requested cursor. The browser checks reference identities and chronological order before accepting it. Changing the collection or closing the conversation panel cancels its pending request. Existing items remain visible after a failed page load.

Live updates reconcile the current page with the loaded older items. Older windows are rechecked at a paced rate, so changes outside the current page can take another refresh to appear. Active video elements and unchanged cards remain connected during updates. Returning through browser history can restore the loaded depth and scroll position from a bounded, temporary tab cache. That cache strips active media sources and is not offline storage.

The gallery does not download full videos automatically. Image download preferences still apply.

## Verification

The integrated source passes `make check` with Vacuum's rule timeout increased from 5 to 30 seconds. The first attempt hit the default timeout on the UI preview schema. Generation, golangci-lint, vet, native tests and Compose validation passed on the subsequent run.

The isolated browser suites for infinite scrolling, shared content, native video, shared search and link cards pass. They cover repeated or malformed cursors, retry latching, stale responses, navigation and drawer cancellation, live head and older-page reconciliation, browser history, keyboard/no-JavaScript fallback, and active video continuity. Shared content and link cards also pass 16 responsive accessibility variants each.

The live BFF was rebuilt and restarted without restarting the engine. A read-only browser probe loaded 50 then 100 unique real catalog references with an unchanged URL, zero JavaScript errors, no mutations and no video body requests. Its output is `/tmp/oie-infinite-live.log`; the screenshot is `/tmp/oie-infinite-live.png`.

The bounded `OIE_CI_SSH_HOST=dagger-vm make ci-check` attempt reached native tests after passing contract lint, golangci-lint and generated-file checks. It exceeded the 240-second limit there and exited 124. The final Dagger run is incomplete; its later race tests and builds are not claimed as passed. The log is `/tmp/oie-infinite-dagger.log`. The full `make ci` browser pipeline was not run; the relevant browser suites ran locally against isolated fixtures.

## Indexed catalog reads

The SQLite migration builds ordered entries for the global, account and
conversation scopes. Each key orders a tab by message position and stable
reference ID. An ordinary page seeks from its cursor and selects a bounded
set of references before reading their payloads. First-page counts use the
same scope and stop at the page size. Text search and multi-account selection
retain their existing filtering path.

The source projection tracks eligibility and parser version. SQLite triggers
keep the ordered entries and per-conversation progress totals in the same
transaction as source visibility, reference, position and preview category
changes. The indexer reads pending sources by parser version and position.
Rebuilding the projection preserves reference IDs and the existing cursor format.
