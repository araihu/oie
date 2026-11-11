# Retained message reactions

API 0.26 adds bounded reaction snapshots and durable full-set replacement or
clearing of the owner's reactions. Each retained canonical message remains the
source. Reactions have separate state and do not change source text, content
revisions, creation dates, conversation activity, unread counters or original
message outboxes.

The reaction snapshot includes aggregate emoji counts, which choices belong to
the owner, nullable observed revisions, effective known selection restrictions,
advisory mutation eligibility and any unresolved own command. The conversation
batch accepts at most 100 exact retained message UUIDs. Reads allocate no
revisions, contact no provider and do not mark messages read. Another sender's
reaction does not invalidate the owner's optimistic revision precondition.

A mutation freezes the complete desired own set, target, sender identity and
session in a distinct command. It uses an operation UUID and a separate outbox.
Only one queued, dispatching or unknown command may exist per source. A missing
receipt requires keeping the same immutable key and payload. Only definite
non-transmission returns to the automatic retry queue. Interrupted or uncertain
writes become Unknown and cannot be blindly retransmitted. Submitted means
written to the protocol connection, not confirmed recipient delivery.

At dispatch, the engine checks the own revision and active room intent again;
the adapter revalidates its frozen target, identity, session and restrictions.
A newer observed own set prevents a queued stale command from dispatching.
An own update observed during transmission is preserved when finishing the
older command. A later provider rejection excludes only the rejected command's
observations; it does not erase another device's subsequent own update.

## Incoming observations and ordering

Migration 16 adds separate append-only protocol observations and command tables,
preserving existing data. Each adapter-verified full actor set replaces that
actor's earlier set for the same logical source. An empty set clears that actor
without removing other people's reactions. Querying uses SQL latest-per-actor
selection after source-reference and identity-collision checks, rather than
loading the complete observation log into the interface.

Verified timestamps order actor updates, with provider event IDs and payload
fingerprints breaking exact ties deterministically. A delayed historical update
cannot overwrite a newer live set merely because it arrived later. Live events
without a provider timestamp use the observed UTC time. Historical controls
without a usable timestamp are ignored. Gross future timestamps are rejected.
Provider/archive aliases deduplicate replay. Conflicting observations claiming
the same actor/event identity are retained as evidence and excluded from the
current projection.

Unresolved controls remain scoped observations without creating conversations
or phantom text messages. When the original message and its verified references
are retained later, ordinary reads can resolve those observations. Ambiguous
raw references produce no reaction association. Verified correction aliases
resolve to the same canonical source.

The XMPP adapter uses XEP-0444. Direct messages use origin/message identifiers;
room targets require room-issued stanza identifiers from a room advertising
stable assignment. In a joined non-anonymous room, live reactions use the
verified real bare JID. In a joined semianonymous room advertising XEP-0421,
live and archived reactions use a validated occupant ID. The engine binds its
own ID to the room's self-presence confirmation and scopes opaque actor keys
to the account and room. The reaction list shows those participants by room
nickname, without contact addresses or profile photos. The engine accepts
archived reactions from authenticated, stable room archive pages and orders
them by the archive timestamp. Once per join, it rereads the latest archive
page for already synchronized rooms to recover recent reactions previously
skipped. That read leaves text history checkpoints intact. Advertised
restrictions constrain outgoing choices;
an absent advertisement is distinct from a known zero or empty allowlist.

Own and incoming sets are bounded to eight distinct Unicode emoji graphemes.
Unicode 17 base data comes from the pinned picker catalog, while independent
Unicode grapheme segmentation preserves modifier, flag, keycap and ZWJ sequences.
Unknown or malformed sets are ignored as complete controls, never applied as
partial clears. The validation generator is checked independently from localized
UI catalogs. See `tools/generate-reaction-emoji-bases.py --check`.

## Verification

Store/application tests cover complete replacement and clear, own versus peer
revision races, duplicate and out-of-order observations, retained orphans,
ambiguous targets, conflicting event IDs, atomic acceptance/materialization
rollback, frozen bodyless dispatch, known-unsent-only retry, interruption to
Unknown, verified echo recovery, and late rejection preserving a newer own set.
HTTP tests exercise nullable revisions, bounded scope, same-key replay, conflicts,
receipt reconciliation and unchanged source/conversation projections. These tests
use synthetic accounts. Live-account message transmission is not part of them.
