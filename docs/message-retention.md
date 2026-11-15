# Message retention

The engine applies the local expiry and XMPP recovery rules in the
[specification](oie-spec.md#71-message-retention). Open **Settings → This device →
Message retention** for the engine defaults, or **Details → Message retention**
for a conversation override. API 0.33 provides the same controls through
`GET` and `PUT` on `/api/v1/engine/retention` and
`/api/v1/conversations/{conversationId}/retention`. The generated CLI includes
the `engine retention` and `conversations retention` commands.

## Engine behavior

The first sweep runs on startup. Subsequent sweeps run hourly and after a
policy change. Each bounded write batch checks the current policy, message
stars, outbox commands and latest 100 logical messages of a room. A cutoff is
inclusive and measured in UTC days. A scheduled send ages from its recorded
successful submission.

Expiry deletes a logical message and its stored correction sources. Durable
receipt IDs and fingerprints survive so a repeated send still cannot create a
duplicate. An expired send has no acceptance payload. Clients can verify the
exact expired intent by sending the original composition with
`X-Oie-Verify-Expired-Send: true`. That check confirms the receipt without
dispatching. Event replay loses the purged payloads, and `conversation.history_pruned` identifies the conversation
and expired IDs so clients can refresh snapshots. Oiê keeps content-free reply
references for the unavailable-original placeholder. SQLite can reuse freed
pages. Downloaded files and pre-existing migration backups retain their own
storage behavior.

Unsolicited delayed history and account-wide discovery are checked at
ingestion. Peer and room MAM requests use the appropriate time bound, and
room recovery reopens the expanded range even if the policy changed while
offline. A policy change invalidates active archive cursors before the next
page or checkpoint write. Ingestion always checks the current cutoff.

History sync and merge restore have no runtime in this version. Their agreed
retention behavior remains in sections 9.2 and 12 of the specification.

## Settings acceptance

| State | Action | Expected result |
| --- | --- | --- |
| Global defaults | Open retention settings | Rooms show 180 days and direct conversations show Unlimited. |
| Finite room policy | Choose Unlimited and save | Show the storage and XMPP consequences before save. The drawer shows the stored policy after saving. |
| Conversation override | Choose a period, then choose the default | Save the period. Choosing the default clears the override and shows the actual inherited period. |
| Stale settings in two tabs | Save a different period from the stale tab | Show a conflict and the current settings. A repeated request for the current settings produces no additional change. |
| Invalid period or missing CSRF token | Submit | Report the failure in the localized drawer. Existing policy remains in effect. |
| Engine unavailable | Open settings or submit a change | Show a visible recovery message. An uncertain response never shows a success message. |
