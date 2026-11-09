# Saved protocol contacts

Global search includes saved XMPP roster entries by name, address, groups and
owning account label. These are provider contacts, separate from inferred message
peers, room occupants and future app-owned identity linking. Reading search does
not retrieve a roster, open a conversation, add contacts or request presence.

API 0.22 adds `GET /api/v1/contacts`, with optional query/account filters and a
cursor bound to the engine and those filters. Matching happens before pagination.
Entries include stable contact/revision UUIDs and an existing conversation UUID
when one exists. A contact without a conversation opens the existing new-chat
form with its account and address filled in; starting it remains explicit.

The native adapter reads a complete roster on connection. Valid roster pushes
are acknowledged and coalesced into paced complete refreshes. This avoids
initial-snapshot versus push ordering races. The implementation follows
[RFC 6121 roster queries and push authorization](https://www.rfc-editor.org/rfc/rfc6121.html#section-2.1.6).
Only omitted or owning-bare-JID push senders are accepted. Unknown subscription
attribute values in a complete result are ignored, rather than deleting an entry.
Reads are bounded to 2 MiB and 10,000 entries; failed or malformed observations
retain the previous cache. No presence-subscription stanzas or roster writes are
part of production contact collection.

Migration 12 adds the contact cache, immutable observed revisions and account
coverage. Unchanged observations preserve revision IDs. Provider removal makes
the directory entry inactive without deleting its identity, revisions or chat
history. Reappearance reuses the identity. The last successful snapshot remains
searchable after disconnect or restart. `unknown`, `known` and `unavailable`
source states distinguish an observed empty roster from missing coverage.

This increment does not implement contact add/remove controls, subscription
request acceptance, identity linking, app sync or contact backup/merge restore.
The cache is a provider observation; future app-owned contact relationships keep
their own lifecycle.

## Validation

SQLite and HTTP tests cover scope-bound paging, Unicode/hostile text, filtering
before limits, stable revisions, removal, unavailable caches, transaction and
clock rollback, restart and absence of messaging effects. Session ownership
rejects obsolete workers; wall-clock time never overrides observation ancestry.
The generated CLI is
exercised against the real engine store.

Independent Mellium tests cover push authorization, limits, cancellation and
session ownership. The development Prosody integration exercises initial roster
recovery, push-driven rename/groups/removal, stable identity on reappearance and
retained cache after restart. Fixtures use disposable development contacts.
Whole-pipeline evidence is recorded in [local CI](local-ci.md).

The full Dagger graph passed on `ef9a9a6`, including roster integration, generated
CLI checks and real-engine browser messaging. The live preview was upgraded from
migration 11 to 12 after a consistent SQLite backup. Integrity checks passed;
all previously stored account, conversation, message, revision and operation UUIDs
were retained, and the previously connected account reconnected. No stored
credentials were exposed during verification.
