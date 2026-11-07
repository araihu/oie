# UI handoff: WhatsApp-like chat workspace

This handoff has been implemented. Current scope, commands, dependency pin and
verification are in [UI preview notes](../ui-preview.md). The mock now uses the
engine-aligned read contract and the UI has no BFF-owned conversation fixtures.

The user explicitly split UI work into another session on 2026-11-07. This
worktree owns UI work; the original session owns the engine. Coordinate through
the OpenAPI contract and later integration, with a mock engine for UI development.

## Location and base

- UI: `/home/gui/.codex/worktrees/oie-chat-ui/oie`, branch `codex/chat-interface`.
- Engine: `/home/gui/.codex/worktrees/oie-api-foundation/oie`, branch `codex/xmpp-messaging` (stacked on the foundation).
- Shared foundation: commit `5b7da71`, draft https://github.com/araihu/oie/pull/1.
- Foundation CI passed all three jobs, including Go/race, browser, and Prosody.
- Read `AGENTS.md`, `docs/oie-spec.md`, and `docs/milestone-one.md`.
- Use the using-goshtoso skill and inspect pinned public components/examples.

## User direction

Make the UI close to the latest WhatsApp desktop screenshot. Main reference:
`/home/gui/.codex/attachments/2977de8a-cf0b-433d-8676-22fdc370a362/codex-clipboard-c4a17135-db8a-43e8-b036-e66148dab60e.png`.
Dark neutral surfaces, a narrow left navigation rail, compact searchable/filterable
conversation list, a large full-height chat pane, patterned dark background,
message bubbles, media/link cards, a compact chat header, and a fixed bottom
composer. Responsive phones show one primary pane with back navigation. Do not
add WhatsApp calls/stories/AI/community features to alpha.

Keep a live preview running so the user can follow. A foundation preview currently
runs on port 8080 from the engine worktree (`bin/oie`, data `./data/preview`),
started in exec session 9865. The app browser currently reaches it via
`http://localhost:53626/` (forwarded port). Preserve the preview until your UI is
ready, then coordinate replacing that owned process with the UI BFF on port 8080.
Use a separate mock API port (for example 18082) and BFF role pointed to it.
Never kill unrelated services. Leave the live preview running at completion.

## Historical starting edits moved here

Only uncommitted UI edits were transferred from engine to this worktree:
`internal/adapters/web/server.go`, both locale catalogs, new `preview.go`, and
`preview-landscape.svg`. These are an incomplete starting sketch, not a working
redesign; `views.templ`/`app.css` still have the original foundation UI.

The sketch puts sample records directly in the BFF behind `?preview=1`. The user
has now asked for a mock server instead: adapt/move these fixtures behind the
mock API, do not preserve this bypass as the final architecture. Fixtures must
never enter the real engine database or masquerade as live protocol state.

Public component discovery found AppShell with RootClass/MainClass/SidebarClass
and attrs hooks, Sidebar/Overlay, Avatar, plain ChatBubble, textinput, textarea,
button/link, dropdown, drawer, card Media/Body/Footer slots. Rich content inside
ChatBubble remains a generic upstream gap; compose existing plain bubbles/cards
where appropriate or use the agreed upstream PR + pinned commit workflow when
needed. Do not copy private DOM/CSS or use internal/demo imports.

## Coordination boundary

- UI owns `internal/adapters/web`, browser checks, mock server and its fixtures,
  product UI documentation. Keep en and pt-BR expressions complete.
- Engine owns domain/application, protocol adapter, SQLite schema, canonical API,
  engine commands and integration checks.
- BFF consumes structured engine contracts through generated clients, never
  protocol clients or SQL/domain stores. No hardcoded browser XMPP client.
- Before defining mock chat endpoints, record a proposed OpenAPI contract and
  coordinate it with the engine session. Do not silently change production engine
  semantics or invent unsupported successful delivery/capability outcomes.
- Keep milestone claims accurate. Foundation stores account metadata; live
  protocol messaging is the engine session's next work.
- Current foundation API: health, engine, account register/list/get/capabilities,
  operation lookup, durable SSE. Contract: `api/openapi.yaml`.
- Generated files must be generated. Run make check and relevant browser tests;
  test real selection/search/filter/navigation/empty/error states, keyboard,
  console, accessibility, and 390/1440 light/dark en/pt-BR. Document mock limits.
- Make UI changes reviewable in their own branch/PR, stacked on the foundation
  until integration. Attach any created PR. Do not merge the foundation PR.
