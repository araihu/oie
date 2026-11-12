# XMPP room entry

Open **New conversation** to enter a room by address or create a room. In a
message, activate an `xmpp:room@service?join` link to open the same entry form
with the address filled in. You can also paste that link into the room address
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
- Opening a room atomically saves the conversation, its entry intent and the
  operation receipt. Replaying a command returns the existing conversation
  without reactivating a later leave. The room worker confirms membership from
  self-presence. Ordinary entry checks discovery and cancels a locked room
  if the service reports accidental creation after a race.
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
| Invitation in a message | Activate the link | Open the prefilled room form and request a preview | Keep the conversation, scroll and draft. Escape returns focus to the link. | Zero room-entry commands |
| Preview pending | Change account or dismiss | Discard the stale response. Request a new preview for the selected account when the form stays open. | Keep typed input and the source conversation geometry. | Zero room-entry commands |
| Preview unavailable | Retry preview or explicitly confirm entry | Show available information or a localized lookup result | Keep the selected account, room and nickname. | Retry sends only a preview request |
| Valid room entry | Confirm | Return the saved room and show entry status until confirmed | Open the conversation for the selected account. | One durable entry intent |
| Missing room | Confirm entry | Report that the room is unavailable | Keep the room conversation available for status and retry. | Zero new XMPP rooms |
| Valid new address | Create | Confirm configuration before showing joined | Open the new room conversation. | One persistent XMPP room |
| Occupied address or competing command | Create | Report a room conflict or an existing room | Keep the form or show the room status. | Zero changes to existing room configuration |
| Operation response lost or later leave | Retry with the same key | Reuse the original result | Open the original conversation. | No repeated join or creation |
| Missing request token or stale engine | Submit preview or entry | Reject the request | Keep the form context. | Zero provider requests |
