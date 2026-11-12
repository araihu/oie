# Conversation requests

A person reviewing an unexpected conversation needs the sender or room identity and a decision before seeing messages. The existing conversation list and header provide context. A compact Goshtoso panel with native buttons replaces the message area while the request is pending. The layout follows the current themes and uses no animation. The current conversation URL identifies the selected request.

| State | Action | Result | Context and effect |
| --- | --- | --- | --- |
| First incoming direct message | Open | Show the sender and available actions | Keep the conversation selected and the content hidden |
| Earlier history exists locally or is verified in the account archive | Open | Show the normal conversation | No acceptance operation |
| Direct request | Accept | Reveal the conversation and allow replies | One retained acceptance, including on replay |
| Pending direct request or room invitation | Archive | Move to Archived, keeping its request status | New messages and repeated invitations keep the archived state |
| Room invitation | Join | Request entry and show messages after the server confirms | Existing room intent and retry rules apply |
| Stale or foreign action | Submit | Reject the invalid transition | Retain the selected record and show current actions |

A direct request is scoped to the owning account and canonical peer. Local history is checked before starting a bounded MAM lookup. A result must precede the triggering message in the account archive. An unavailable or inconclusive archive leaves the decision with the user. The user can decide while the check is running or if the connection drops. A late lookup respects explicit decisions. Rooms appear when Oiê receives a direct or mediated MUC invitation. The room server decides whether entry is allowed.

The request body is kept out of conversation previews, the timeline, message events and search until the request is resolved. Accepting an archived direct request restores it to the inbox. Joining an archived room preserves its archive preference. An invitation password is kept in the private engine database for entry and reconnect, separate from API responses and events.
