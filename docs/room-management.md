# XMPP room management

Open **More → Manage room** in a joined room. The available actions depend on
the account's room permissions. **Details** shows the room image and offers
editable name and multiline topic fields with one **Save changes** button.
Owners can change the name and can replace or remove the room image when
the service supports room vCards. Moderators can change the topic and adjust current occupants' roles.
**More → Invite people** opens the invitation form when the room permits it.
The room may let other occupants edit the topic or invite people. **Participants** groups occupants by room permissions. Select a name to see
the participant's role, affiliation, and the actions available to your account.
Photos use the identity disclosed to this account by the room. An unavailable
photo shows initials.
Administrators and owners use **Bans** to consult bans, ban an address or
lift a ban. A ban made from a participant's profile appears under the nickname
used in the room at that moment, with the address below it. Search by nickname
or address.
A room can omit personal addresses even when it publishes nicknames.

Room creation keeps the service's defaults with persistence enabled. Enter an
existing room by address, a credential-free invitation link, a received
invitation, or a result from **Browse public rooms**. Directory reads never
enter rooms. An optional password is retained in the owning engine for
reconnect. The browser keeps it in memory during an interrupted submission.
Archiving an invitation retains the request and keeps it out of the inbox.

## Protocol behavior

[XEP-0045](https://xmpp.org/extensions/xep-0045.html) provides configuration,
subjects, mediated invitations, roles, and affiliations. A successful IQ
confirms a name, role, or affiliation change. Sending a subject or invitation
confirms submission. The room publishes its new topic and delivers invitations
according to its policies. Name changes preserve the rest of the owner form. Saving both fields checks
both permissions before sending. The room confirms the name first, then Oiê
submits the topic. A partial result identifies the saved name and keeps the
topic available for retry.

The image actions follow [XEP-0486](https://xmpp.org/extensions/xep-0486.html).
The engine validates and resizes the uploaded raster to a static PNG.
It reads the room vCard before writing, preserves other fields, and
waits for the provider IQ result. The cached image refreshes through
the existing profile image events.

A management request names an observed room revision. Permissions come from
self-presence and service discovery. Role changes target a specific observed
occupant, with a fresh opaque identifier after leaving or changing identity. A role or affiliation change invalidates the management revision.
Affiliations use bare addresses entered by the user or disclosed to the
authorized account by the room. The server decides whether each operation
is allowed. A command reserves its idempotency key before the wire attempt.
An interrupted attempt can remain uncertain. Replay reads the same receipt.

[XEP-0425](https://xmpp.org/extensions/xep-0425.html) moderation requires a
room-assigned message identifier and the advertised moderation feature.
A notice from the joined bare room or its authenticated room archive updates
the message projection. A moderation received before its source also applies
when the message arrives. The timeline shows the removal and any reason.
Message search, edit history and the shared-content catalog omit the
moderated text and references. The engine retains the original source rows.

## Acceptance invariants

| Route or state | Action | Expected response | Context and focus | Provider effects |
| --- | --- | --- | --- | --- |
| Room directory | Browse or select a result | List public addresses or open preview | Retain account and conversation draft | Zero joins |
| Protected room | Join with a password | Enter after confirmed self-presence | Open the saved room and retain its history | One durable entry request |
| Room invitation | Archive | Show in Archived | Preserve the room and its request | Zero joins |
| Joined owner, room advertises vCard | Replace or remove image | Confirm and refresh the published image | Keep the selected room | One vCard IQ write |
| Joined owner | Rename | Confirm the change and show the published name | Stay in room controls | One configuration write |
| Joined occupant | Change topic or send invitation | Show submission status | Retain the current conversation | One directed message |
| Joined moderator | Change role or remove occupant | Confirm when accepted | Target the reviewed presence | One admin IQ |
| Joined administrator or owner | Read or change affiliation | Show the requested list or confirm the write | Keep the selected room | Zero writes for a read, one for a change |
| Joined moderator with a verified target | Remove message | Show the removal when the room publishes it | Keep the timeline anchor | One moderation IQ |
| Left, stale, forbidden, or forged | Submit | Show current availability or refusal | Retain the form input | Zero unauthorized writes |
| Response lost or repeated | Submit the same action and key | Return the stored outcome or uncertainty | Keep the same room and command | At most one attempt |
| Untrusted message | Receive a forged moderation notice | Ignore it | Keep the original message | Zero projections changed |

Removing a room from chats stops automatic rejoin and hides it from both Chats
and Archived. The server room and saved history stay available. Open the room by
its address to add it back. A joined owner can instead delete the server room
for everyone from **Manage room**. That action retains Oiê's local history.

| Route or state | Action | Expected response | Context and focus | Effects |
| --- | --- | --- | --- | --- |
| Any saved room, even offline or failed | Cancel removal | Keep the room | Return to the chat menu | Zero changes |
| Any saved room, even offline or failed | Remove from chats | Room disappears from active and archived lists | Return to chat list with the current filters | Disable rejoin; do not delete the server room or history |
| Removed room, stale revision or replay | Repeat removal | Old key returns the receipt; stale revision is refused | Keep the selected chat on refusal | No second change |
| Removed room | Explicitly open by address | Return the same conversation and history | Open the restored conversation | One new entry intent |
| Joined owner | Delete for everyone and confirm | Report success on provider confirmation | Keep the local chat | One owner destroy IQ; disable rejoin |
| Guest, stale owner revision or disconnect | Forge or submit deletion | Refuse or report unavailability | Keep the form and room | Zero unauthorized writes |
| Lost provider reply | Repeat the same deletion key | Return the recorded outcome or uncertainty | Keep the form and room | At most one attempt |
