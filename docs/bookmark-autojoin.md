# Bookmark autojoin

Agreed behavior: on account connection, honor `autojoin="true"` or `"1"` in
modern XMPP bookmarks and legacy private bookmarks. Use the account's username
as the initial nickname, such as `guicastro` for `guicastro@suchat.org`.
An explicit local nickname, leave or access revocation takes precedence.

The XMPP adapter imports a provider-neutral room bookmark. The application
restores its conversation and records an initial join intent only when none
exists. The existing worker sends room presence and waits for confirmed
self-presence. Repeated imports reuse the intent; an optimistic revision check
prevents an import racing with a manual command from replacing that decision.

Existing SQLC queries and the room-intent transaction are reused. No schema,
HTTP operation or generated file changes are required. The default nickname
follows the existing 100-character limit. A nickname conflict remains an error;
the client does not silently invent a different nickname.

Scope: bookmark configuration is read when the account connects. Live bookmark
edit notifications are not subscribed to by this change. Reading room history
alone, finding the user through another client, or inspecting participants does
not establish this client's room session.

Verification covers both bookmark XML formats, account-user nickname selection,
concurrent initial imports, protocol worker dispatch, repeat imports, and
preservation of manual nicknames, leave decisions and access revocation. The
current live room was entered through the existing API as `guicastro`, and the
server confirmed `joined`. The running engine was not replaced during the
parallel video work; bookmark-triggered entry is verified with isolated adapter,
application and SQLite tests rather than a forced live account reconnect.

Native `make check`, focused adapter/HTTP/SQLite race tests, and Dagger
`make ci-check` pass in the managed autojoin worktree. No full live-account
reconnect or full browser/XMPP pipeline was forced for this increment.

## Integration verification

Merged into the primary checkout as `10930b0`. Native `make check` also passes
with the integrated source. Its additional Dagger repeat passed native checks,
then stopped producing progress during race tests and was interrupted; completion
of that integrated repeat remains unverified. The isolated feature Dagger run
completed successfully. Other sessions and their uncommitted edits were retained.
