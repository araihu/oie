# Conversation navigation

Selecting a conversation in the same inbox replaces `#conversation-pane`.
The application shell, sidebar, conversation list, unfinished search input and
list scroll position remain mounted. Account, search, section, locale or theme
changes still replace the broader application region.

The BFF serves this region for a root GET with `HX-Request: true` and
`X-Oie-Fragment: conversation`. It includes conversation-owned stars, avatar
data and the details drawer. The response carries a scope hash covering the
engine, inbox account/filter/query and presentation settings. No engine API
operation or protocol adapter changes are needed.

The browser validates the requested conversation and scope before replacing
the region. Failed or obsolete responses preserve the active conversation.
Browser history and the mobile Back action use the same fragment lifecycle.
Previous listeners, reads, media and live subscriptions are disposed before the
new conversation starts. Draft ownership remains tied to its engine, account
and conversation.

Live inbox refresh compares a version derived from conversation/account data
instead of HTML changed by HTMX, avatar bindings or localized timestamps.
Selection updates `aria-current` directly. A new conversation response does not
mark the retained inbox data as current, so an intervening incoming message can
still update its preview or unread count.

The fragment root is a flex column occupying one workspace grid cell. The chat
fills that column and the details drawer stays inside it. Using `display:contents`
on this root exposed the drawer's empty wrapper as another grid item, creating a
second row and shortening both the inbox and the chat for short conversations.
On mobile the whole fragment is hidden until a conversation opens.

The conversation list uses the [Oiê palette](branding.md) for its row states.
Hover adds a neutral surface on devices with a hover pointer. The open
conversation retains a blue-tinted surface and a leading stripe even when the
pointer is elsewhere. Keyboard focus draws a full outline inside the row.
The stripe and outline use the high-contrast accent and leave row geometry
stable. See [WCAG non-text contrast](https://www.w3.org/WAI/WCAG22/Understanding/non-text-contrast.html)
and [visible focus](https://www.w3.org/WAI/WCAG22/Understanding/focus-visible.html).

The focused browser fixture is `tests/browser/conversation-navigation.cjs`.
It uses isolated HTTP engines and checks unchanged DOM nodes and scroll,
drafts, details, Back/Forward, stale requests, failure preservation, real inbox
updates, one live stream, mobile navigation and native links without JavaScript.

## Validation

Validated with `make check`, the web package race tests, the focused conversation
navigation browser fixture and the existing live-chat browser fixture. Vacuum
used a 30-second rule timeout for the local check.

After the short-conversation layout fix, both `make check` and Dagger `ci-check`
completed successfully. The Dagger pipeline included race tests and builds.

The existing chat-info browser fixture times out waiting for the scoped media
snapshot version to change. It fails at the same assertion with both this build
and the source worktree's baseline binary. The full browser pipeline was not run.

The browser fixture also checks that the composer and visible inbox footer reach
the viewport bottom for short and overflowing conversations, across desktop,
tablet and mobile sizes. The timeline scrolls independently of the page.

## Main-session integration

Integrated into `codex/message-previews`, retaining the newer sidebar startup
guard, conversation menu and emoji composer. Existing local gallery, preview,
star and theme edits remain uncommitted with their original staging state.
The overlapping template changes were merged and regenerated with templ.

The combined source passes `make check`, Dagger `ci-check` and both conversation
and full-fragment navigation browser fixtures. The first full-fragment run timed
out waiting for a modifier-click popup; the baseline and a repeat combined run
passed. No full browser-pipeline result is claimed.

Air rebuilt the original BFF on port 8080. Read-only browser verification against
that preview retained the same shell and inbox nodes across two conversation
selections, made only one document request and kept the composer at the viewport
bottom. The existing engine process and protocol sessions were retained. The
separate development preview on port 8082 is also still available.

## Navigation icons

The sidebar toggle and mobile menu use the bundled Heroicons Bars 3 symbol.
The Chats link uses the 16px solid Chat Bubble Left glyph from the same
Heroicons v2.2.0 source. Goshtoso's consumer iconpack supplies that glyph as a
local sprite because the bundled selection omits it. The source and integrity
lock live under `tools/icons`.

Regenerate the supplemental sprite from the repository root with the pinned
Goshtoso generator.

```sh
GOWORK=off go -C tools run github.com/araihu/goshtoso/cmd/iconpack@v0.6.2-0.20261110110007-83aed4efdc31 \
  -config icons/.iconpack.yaml -out ../internal/adapters/web/appicons \
  -package appicons -const-prefix Icon -sprite-url /ui/icons/heroicons-extra.svg
```
