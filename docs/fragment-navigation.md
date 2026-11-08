# Fragment navigation

Ordinary in-app GET navigation uses the bundled HTMX 4 runtime. Chat selection, account switching, inbox filters/search, media categories/pages, retained source jumps and account-management links replace the application fragment. Chat-info categories, search, pagination and Back replace only the drawer's content. Native links and GET forms retain their addresses and work without JavaScript. Mutation forms retain their existing operation keys, CSRF validation and asynchronous submission handlers.

The browser keeps the head, styles and runtime. `#oie-app` carries the selected engine/account/conversation, event cursor, locale/theme and presentation state. The BFF returns that fragment for an HTMX request, or `#chat-info-content` for a scoped info request. Both representations are uncached and vary by fragment headers. Native direct entry returns the full document. Unknown routes retain an application recovery screen.

Each JavaScript module registers a route initializer with `OieRuntime`. Disposal removes its listeners, cancels timers/frames, disconnects observers, aborts outstanding fetches and closes its live subscription. Obsolete async completions cannot modify the next route. Drafts, uncertain operation keys and device preferences retain an in-memory session fallback when browser storage is unavailable. Navigating away from an uncertain send does not create a new intent when the user returns.

Navigation accepts only the latest request's response. Invalid fragments and transport failures leave the current view usable. Back/Forward fetch server truth through the same fragment boundary. Route scroll positions are bounded to 100 entries; explicit message targeting takes priority over a remembered timeline position. Focus and edited controls remain local to their account/conversation. Drawer reads capture the current star generation so a delayed snapshot cannot overwrite a newer star result.

## Composition

The existing Oie identity and public Goshtoso Drawer, Avatar, Panel, Accordion, Link and form controls remain in use. Chat information is a compact detail view with centered identity, available description/topic, participant observations and scoped content rows. Secondary connection/protocol information stays collapsed initially. The application owns fragment boundaries, navigation state and media-date grouping; no dependency change is required.

| Interaction | Expected behavior | Preserved state |
|---|---|---|
| Chat/account/filter/category | HTMX application fragment | URL, scoped draft and operation key, device preferences |
| Info category/search/page/Back | HTMX drawer fragment | Composer node/draft, main query/filter, timeline and drawer return position |
| Source message | Canonical account/conversation/message navigation | Original source identity; hidden filters cleared |
| Browser Back/Forward | Fresh fragment through history handler | Addressed view, bounded scroll history, scoped draft |
| Delayed or failed navigation | Ignore obsolete response or retain current view with recovery feedback | Current identity and unsent input |
| Send reply lost while navigating | Reconcile original operation key on return | One accepted engine effect per intent |
| Native fallback | Full server-rendered destination | Link/form semantics and localized shell |

Browser coverage includes repeated swaps, source/account isolation, draft restoration with denied storage, one live stream after settlement, exactly one mutation after remounts, overlapping requests, transport failure, native fallback and accessible mobile layouts. Chat-info checks also cover duplicate image projections, source-message stars and closing the viewer while keeping its underlying drawer open.
