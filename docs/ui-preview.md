# Chat UI preview

This records the mock-only UI delivered in PR #3. The current integrated browser
uses the real engine; see [live preview setup](live-preview.md). The independent
mock remains useful for deterministic presentation checks.

The UI work is developed independently of the engine in the managed worktree
`oie-chat-ui`, branch `codex/chat-interface`, stacked on the foundation. It uses
an independent read-only mock HTTP API rather than BFF-owned sample data or the
real engine store.

## Run

In one terminal:

```sh
make mock-api
```

In another:

```sh
go run ./cmd/oie --role bff --listen 127.0.0.1:8080 --engine-url http://127.0.0.1:18082
```

Open `http://localhost:8080/?theme=dark&lang=pt-BR` for the reference-oriented
preview. The mock responds with `X-Oie-Mock: true`, and the UI labels its sample
conversations. The BFF remains a generated HTTP-client consumer; it does not open
SQLite or include a chat protocol client. `go run ./cmd/oie-mock --empty` provides
an empty-state API. Stopping the mock exercises engine-unavailable recovery.

The preview is kept running on port 8080 during UI development. The engine work
uses separate ports and its own data directory.

## Implemented presentation

The layout follows the supplied WhatsApp desktop reference: navigation, a dense
searchable/filterable conversation list, contained timeline scrolling, compact
chat header, plain incoming/outgoing bubbles, sample media card, and a fixed
composer. On narrow screens the conversation list and selected chat occupy one
primary pane at a time with a back action. The decorative pattern and landscape
illustration are original SVG assets.

Chat selection, search submission, filters, archived/favorite navigation, and
account/detail/settings drawers work against the mock. Language and appearance
choices remain per-device cookies. The product catalogs include English and
Brazilian Portuguese; system appearance remains the default. The current media
library is an empty-state placeholder. Sending and attachment actions are
explicitly disabled: the mock does not accept mutations, connect accounts,
change protocol read state, or fabricate delivery receipts.

`api/ui-preview.openapi.yaml` is a **draft integration contract**, separate from
the engine-owned canonical contract. Its bindings live under
`internal/adapters/previewapi`. Both OpenAPI sources are linted and regeneration
is checked through the dedicated tools module (`go tool -modfile=tools/go.mod`). The shared draft uses UUIDv7 entity/revision/account IDs, UTC times,
`items`/`nextCursor`, opaque conversation cursors, newest-first message pages,
structured sender identity, `originalText`, `isOwn`, and actual send-state fields.
The BFF fetches a conversation header separately and reverses a message page for
chronological display. Mock own-message samples use unknown send evidence; no
delivery/read badge is manufactured.
Mock fixtures live only under `internal/adapters/mockengine/fixtures`.

The engine session owns canonical API, domain/application rules, SQLite, and the
XMPP adapter. Before integration, converge this draft with the engine's actual
conversation/message schemas and generate the BFF client from that contract.
Do not treat the mock's sample media or unread summaries as completed engine
features.

## Goshtoso dependency

The UI composes public AppShell, Sidebar/Overlay, Avatar, ChatBubble, TextInput,
Textarea, Button, Link, Dropdown, Drawer, Card, and EmptyState APIs. App CSS owns
layout, brand tokens, and supported root/attribute hooks; it does not reach into
private component DOM.

The dense media card required a generic compact density preset. That change is
open in [Goshtoso PR #367](https://github.com/araihu/goshtoso/pull/367), commit
`937106f79abc`, pinned as
`v0.6.2-0.20261107211339-937106f79abc`. Oie resolves that published pseudo-version
without a sibling `replace`. The PR keeps default cards unchanged and adds
library/demo/browser coverage. Replace the temporary pin with an appropriate
release after upstream merge/release.

Rich content inside ChatBubble remains a later generic upstream gap; this
preview uses existing plain bubbles and a compact Card instead.

## Verification

```sh
make check
make test-race
make build
npm --prefix tests/browser ci
npx --prefix tests/browser playwright install chromium
npm --prefix tests/browser test
npm --prefix tests/browser run test:chat
npm --prefix tests/browser run test:states
```

The workspace checks cover real HTTP mock reads, stable fixture identities,
read-only mutations, selection/search/filter behavior, missing conversations,
mobile back/navigation, settings/Escape, empty and actual transport-failure
recovery (including a selected mobile chat), missing routes, 390/1440 px, Goshtoso/Minimal themes,
light/dark, en/pt-BR, contained geometry, accessibility, and console errors.
Screenshots/reports are ignored under `tests/browser/artifacts`.

Local verification passed `make check`, `make test-race`, both 16-combination
loaded-workspace browser matrices, selection/search/filter/navigation/settings
checks, and 64 empty/transport-failure/selected-chat/missing-route states. The
checked WCAG A/AA scans reported zero violations and loaded workspaces had no
console errors. Desktop, narrow, and recovery screenshots were inspected.
Goshtoso PR #367 also passed its Required CI gate, including lint/drift/build,
site/published consumer, full tests/coverage, and image smoke checks.
