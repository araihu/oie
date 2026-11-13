# XMPP room entry

Open **New conversation** to enter a room by address or create a room. In a
message, activate an `xmpp:room@service?join` link to open the same entry form
with the address filled in. Visible invitations also show a compact room card
in the message bubble. **View room** opens the same entry form. The card
loads automatically for MUC services discovered on the account server. For
other services, opening the invitation from a message or selecting **Preview
room** loads the preview. You can also paste the link into the room address
field. Choose the XMPP account and optionally enter a room nickname before
confirming. The room preview can show the published name, description, topic,
occupant count, and avatar. Its availability depends on what the room publishes
to the selected account.

This flow accepts bare room addresses and `?join` links without credentials.
It handles link activation inside Oie and supports the entry form at an
in-app URL. System-wide `xmpp:` registration and password-protected entry are
outside this increment. Creation enables persistence on services that advertise
the XMPP room configuration form, uses their other defaults, and accepts an
optional published name.

## Engine behavior

- Preview uses account-scoped, bounded discovery and vCard IQs to the bare
  room JID. It can use disco information when vCard fields are unavailable.
  Inline raster avatars pass through the existing thumbnail validator and
  same-origin image endpoint. External vCard photo URLs are ignored. Preview
  does not save a conversation, send presence or change read state.
- The timeline renders a stable card for up to three distinct invite addresses
  per message, using the same parser as its rendered links. It looks up visible
  cards with the conversation account and reuses a result per destination within
  that view. The XMPP adapter limits these lookups to MUC services advertised
  by the account server. Each view starts up to twelve lookups with two in
  flight. Explicit preview fills the matching card with the same response.
  Opening an invitation URL directly fills the room and account, with a
  separate action to fetch the preview.
- Opening a room atomically saves the conversation, its entry intent and the
  operation receipt. Replaying a command returns the existing conversation
  without reactivating a later leave. An empty direct conversation at the
  same address can be promoted in that transaction, preserving its identifier
  and preferences. Direct history and staged attachments prevent promotion.
  The room worker confirms membership from self-presence. Ordinary entry checks discovery and cancels a locked room
  if the service reports accidental creation after a race. An authoritative
  room-not-found result stops retries until the user requests entry again.
- Creation proceeds when discovery reports an absent room on a MUC service.
  Only owner self-presence with status 201 allows configuration. A positive
  configuration reply clears pending creation and enables normal rejoin.
  If the outcome becomes uncertain after sending the configuration, a later
  creation attempt detects the existing room. Entering by address can recover
  access. Existing room configuration is left intact.

[XEP-0045 Multi-User Chat](https://xmpp.org/extensions/xep-0045.html) and
[XEP-0486 MUC Avatars](https://xmpp.org/extensions/xep-0486.html) define the wire
behavior.

## Acceptance invariants

| State | Action | Expected response | Context and focus | Effects |
| --- | --- | --- | --- | --- |
| Invitation card visible | Load room details | Show available name, photo and description in the existing card | Keep the action, composer and timeline geometry stable. | Read-only preview for the conversation account |
| Invitation card with missing details | View room | Open the entry form from the fallback card | Keep the invite address and restore focus on dismissal. | Zero room-entry commands |
| Invitation in a message | Activate the link | Open the prefilled room form and request a preview | Keep the conversation, scroll and draft. Escape returns focus to the link. | Zero room-entry commands |
| Preview pending | Change account or dismiss | Discard the stale response. Request a new preview for the selected account when the form stays open. | Keep typed input and the source conversation geometry. | Zero room-entry commands |
| Preview unavailable | Retry preview or explicitly confirm entry | Show available information or a localized lookup result | Keep the selected account, room and nickname. | Retry sends only a preview request |
| Valid room entry | Confirm | Return the saved room and show entry status until confirmed | Open the conversation for the selected account. | One durable entry intent |
| Missing room | Confirm entry | Report that the room is unavailable | Keep the room conversation available for status and retry. | Zero new XMPP rooms |
| Valid new address | Create | Confirm configuration before showing joined | Open the new room conversation. | One persistent XMPP room |
| Occupied address or competing command | Create | Report a room conflict or an existing room | Keep the form or show the room status. | Zero changes to existing room configuration |
| Operation response lost or later leave | Retry with the same key | Reuse the original result | The form keeps its original account, address and nickname until the retry resolves. | No repeated join or creation |
| Missing request token or stale engine | Submit preview or entry | Reject the request | Keep the form context. | Zero provider requests |
