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

The focused browser fixture is `tests/browser/conversation-navigation.cjs`.
It uses isolated HTTP engines and checks unchanged DOM nodes and scroll,
drafts, details, Back/Forward, stale requests, failure preservation, real inbox
updates, one live stream, mobile navigation and native links without JavaScript.
