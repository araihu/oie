# Message reactions

This increment adds XMPP emoji reaction sets to retained source messages. The
engine HTTP contracts are defined before their adapters in API 0.26. Browser
presentation operations are defined separately in BFF API 0.4.

The browser reads a bounded batch of snapshots for mounted message UUIDs. Each
snapshot binds the account, conversation and canonical message, includes observed
emoji counts and the owner's selected set, and reports current operation
eligibility. Opening a picker performs another read-only check. It does not join
a room, discover identities, change read state or send a message. Missing support
leaves retained text usable without a reaction action.

Reaction chips show the observed count and indicate whether the current account
is included. Clicking a chip toggles that emoji in the own set. React opens a
Goshtoso dialog with the composer's shared emoji selector. The same component and
controller provide continuous categories, footer navigation, bilingual search,
device recent history and grouped skin-tone choices. Search names follow the UI language and keywords
match both languages. The source composer and ordinary draft are independent.
Escape closes the picker and restores source-action focus. The server's allowed
emoji list filters choices; its maximum applies to the complete own set. With a
maximum of one, choosing another emoji replaces the previous one. Selecting the
same emoji removes it. The alpha app's bound of eight is an implementation limit,
not a protocol-wide or agreed global cardinality rule.

A choice freezes its operation UUID, full desired own set and own revision
precondition. BFF submission reads an existing command receipt before creating
anything. Receipt reconciliation performs only an engine GET and checks all
scope and intent fields before exposing it. The browser retains unresolved
intents in device storage with a memory fallback. Known queued or dispatching
commands are polled, with a spinner beside retained chips. Counts remain observed
state rather than an optimistic delivery claim. Unknown outcomes offer Check;
they never blindly repeat provider transmission. A missing receipt offers an
explicit Retry of the same immutable operation and key. The engine can then
return an existing receipt or accept a command that never arrived.

Pending controls stay in the message region. Eligibility checks, refusal and
transient feedback do not add composer banners or move its controls. Errors use
short localized toasts. New reaction content may add a chip row; loading and
retry states retain that row's geometry, and timeline updates retain the visible
message anchor. Fragment disposal aborts outstanding reads and clears polling.

The UI composes public Goshtoso ChatBubble rich content, Button, native Dialog,
TextInput and Spinner APIs. It imports no dependency demo/internal packages. The
composer emoji selector keeps its existing behavior.

Validation uses an isolated engine fixture and BFF, without a real account.
`tests/browser/reactions.cjs` covers add/remove counts, source immutability,
bilingual search, full-set restrictions, queued versus observed state, lost
acceptance on both sides of receipt creation, same-key retry, unknown command
reload, account/CSRF rejection, delayed refusal without picker flash or composer
movement, keyboard focus and English/Portuguese light/dark layouts at 390 and
1440 px. Web unit checks cover scoped immutable receipts, set-order equivalence,
foreign-account rejection, CSRF and allowlists larger than the selection limit.
Engine, persistence and XMPP integration results are reported separately by the
coordinating session.

## Shared selector invariants

| Context | Action | Preserved state and effect |
|---|---|---|
| Composer | Choose an emoji or tone | Insert at the caret, keep the draft and issue no reaction command. |
| Reactions | Choose an allowed emoji or tone | Keep the composer draft, submit one scoped full-set command and update shared device recents. |
| Restricted protocol | Browse or choose | Show only allowed emoji/tone variants; respect the current selection limit. |
| Nested tone strip | Escape | Close only the strip and focus its trigger; retain the reaction dialog and draft. |
| Delayed or refused preparation | Open reaction selector | Keep the composer/timeline geometry; issue no mutation on refusal. |

`tests/browser/reactions.cjs` exercises these alongside receipt recovery, unknown
outcomes, bilingual search, category navigation, device recents shared in both
directions, complete-set limits and individual-tone restrictions. Its visual
matrix covers 390/1440 px, both locales, Goshtoso/Minimal and light/dark modes.
The composer fixture scopes its queries to its selector instance. Its held cached
opener test starts after the public popover is positioned so it delays application
work without freezing the library's visibility gate.
