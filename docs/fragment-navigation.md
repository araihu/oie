# Fragment navigation

Ordinary in-app GET navigation uses the bundled HTMX runtime. Native links and GET forms keep addressable destinations. Mutation forms retain their operation keys, CSRF validation and asynchronous submission handlers.

The document head, styles and runtime scripts stay mounted. Navigation across sections and inbox account scopes can replace `#oie-app`. Conversation selection within the same inbox replaces the conversation pane. Other interactions request the smallest applicable HTML fragment from the BFF.

| Interaction | Updated region | Preserved state |
|---|---|---|
| Inbox filter | Filters and conversation list | Open chat, draft, timeline position and event stream |
| Conversation selection | Conversation pane | Navigation, inbox search, list position and per-chat drafts |
| Account management | Account drawer content | Open chat, draft, timeline position and event stream |
| Media filters, search and tabs | Catalog results and affected account controls | Search field, device preferences and event stream |
| Details subviews and browser history within those subviews | Details drawer content | Composer, timeline and event stream |
| Appearance | Appearance controls and document theme | Open chat, draft, scroll and event stream |
| Jump to latest | Timeline contents | Composer, draft and event stream |
| Global agenda commands, filter and pagination | Agenda results | Agenda controls and event stream |
| Edit a scheduled message in the current chat | Composer mode and its schedule fields | Composer node, separate ordinary draft, timeline position and event stream |

Sidebar items and conversation rows declare their HTMX attributes in server-rendered links. The navigation controller can process a clicked link while the dependency loader is still starting. Modified clicks retain browser behavior. When a fragment replaces a route, its obsolete listeners, timers, observers and requests are disposed. Scoped updates reuse mounted route modules and the current event subscription.

Navigation accepts the latest applicable response. Invalid fragments and transport failures preserve the current view. Back and Forward request server truth through the applicable fragment boundary. Retained scroll history is bounded. Opening or dismissing a message menu uses existing action state. The live subscription, recovery polling and action eligibility checks refresh that state.

## Verification on 2026-11-08

`make check` passes, including contracts, golangci-lint, generated-file checks, vet and native tests. HTTP tests distinguish native documents, application fragments and drawer fragments, and verify account/conversation isolation for both shared-content and starred-message reads. The navigation fixture records eighteen HTMX requests and one document load, two distinct send intents with two accepted effects, one active stream after settlement and zero browser errors. It also covers leaving after a committed send but before its response and reconciling the original key on return.

All host browser suites pass, including smoke, chat, presentation states, account switcher, live-chat, avatars, previews, Markdown, stars, replies, capabilities, account drawer, conversation details, shared content and chat info. The isolated development XMPP browser workflow passes. Visible replies use a route-scoped capture handler to focus their existing source before boosted navigation; distant replies retain fragment navigation. Tests wait for URL and rendered DOM postconditions rather than assuming a click waits for a native document load.

The live BFF was updated independently. A read-only observer confirmed scoped media/category/search/Back swaps, unchanged composer DOM, one document load, zero duplicate IDs, zero browser errors and zero mutations. The existing engine process, identity, API 0.10 and clean schema 8 were preserved.

The final `make ci` run completed containerized checks, race tests, application/CLI builds and isolated Prosody integration. The browser stage could not start because Dagger failed expanding `with-mounted-cache` with `waiting for client session attachables: context deadline exceeded`. A fresh retry reproduced this infrastructure failure. The failed owned client was stopped without changing the shared runner, tunnel or caches. Full Dagger completion for this increment remains unverified; the successful host browser suites above are separate evidence. Dagger now prints plain stage diagnostics by default.

## Sidebar startup regression

A read-only check of the live BFF confirmed that all four sidebar destinations already used fragments after startup: four HTMX requests, one initial document, an unchanged window token and no page errors. No account or message mutations were performed. A complete shell swap can resemble a reload, and the development watcher deliberately reloads the document after a successful source rebuild.

An isolated fixture reproduced an actual startup gap by delaying the final Alpine dependency without changing HTMX or forcing its processing from the test. Clicking the sidebar after `window.htmx` existed but before application startup loaded a second document and made no fragment request. Server-declared attributes and the pre-start click handler address that case. The fixture checks document identity and requests, dependency readiness, desktop/mobile navigation, Back/Forward, drafts and one active live stream.

For this correction, `make check` and containerized `make ci-check` pass, including golangci-lint, generated-file checks, native tests, race tests and application/CLI builds. The delayed-start regression now records one document and one HTMX fragment. Readiness deliberately occurs between swap and settlement; the runtime starts once and retains one live stream. The live BFF serves the updated startup controller and fragment attributes for both sidebar layouts.
