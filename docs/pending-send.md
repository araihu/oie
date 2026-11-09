# Pending message bubbles

The browser projects the existing immutable pending submission as an outgoing
Goshtoso ChatBubble, with Badge status and Button/Dropdown retry actions. It
moves the submitted text and reply preview out of the composer. The composer
stays read-only until acceptance is resolved, as it did before this change.
There is one unresolved submission per engine/account/conversation. This is
not a multi-message browser outbox, attachment upload queue or offline delivery
implementation.

A missing or lost response means acceptance is unconfirmed. It does not prove
that provider delivery failed. `POST /ui/messages/reconcile` reads the existing
operation and message, checks the complete frozen intent, and never calls send.
Live refresh uses that receipt before merging the timeline. Explicit retry
reuses `/ui/messages/send` with the same key and frozen body, reply and mentions.
The engine owns transport retry after it accepts the intent.

Accepted placeholders remain visible until the bounded timeline contains their
canonical message ID. Equal text never establishes identity. Explicit server
rejection restores the editable draft and its mention/reply selections.

The application composes public Goshtoso ChatBubble, Badge, Dropdown, Button
and Heroicons APIs. Its own JavaScript supplies submission lifecycle and receipt
reconciliation, which are application behavior rather than generic components.

| State / request | Allowed action | Result | Preserved context and focus | Send effects |
| --- | --- | --- | --- | --- |
| Ready | Submit | Local pending bubble; no composer progress row | Frozen key/body/reply/mentions; empty read-only input | At most 1 |
| In flight | Repeated submit or retry | Deduplicated; retry controls disabled | Same immutable submission | No extra effect |
| Unconfirmed | Retry shortcut or menu | Same submission reconciled or resent idempotently | Exact key/body/reply/mentions; input receives focus afterward | At most 1 total |
| Unconfirmed / reload | Restore | Pending bubble and retry restored; receipt checked | Same scoped journal | Receipt lookup adds 0 |
| Missing receipt | Read-only reconcile | 404; remains unconfirmed | Original submission remains retained | 0 |
| Matching accepted receipt | Read-only reconcile | Canonical message ID and next operation key | Local bubble retired when that ID is present | 0 additional |
| Conflicting body/account/conversation/reply | Read-only reconcile | 409 | Original submission unchanged | 0 additional |
| Invalid CSRF / key | Read-only reconcile | 403 / 422 | No acceptance claimed | 0 |
| Explicit rejection | Edit draft | Pending projection removed; error and editable draft restored | Original text, reply and mentions | 0 |
| Accepted | Retry | Removed from local bubble | Engine transport evidence stays authoritative | 0 additional |

Browser checks cover 390 and 1440 px in light/dark modes, keyboard shortcut and
menu retry, reload, Unicode mentions and replies, lost responses, exact effect
counts, receipt scope/CSRF checks, and unchanged single-line composer geometry.
These fixtures do not contact a real XMPP account or prove provider delivery.

Verification for this increment:

- `make check` passed, including OpenAPI lint, generated-source checks, Go lint,
  vet, tests and Compose configuration.
- `live-chat.cjs` passed the pending/retry/receipt and geometry matrix.
- `mentions.cjs` passed the immutable pending-mention recovery tests.
- `OIE_PENDING_SEND_ONLY=1 reply-compose.cjs` isolates the reply bubble, receipt,
  menu retry, canonical replacement and effect-count checks.
- The full reply-compose fixture reaches the later denied-storage mobile room
  navigation check, then times out waiting for `#room-session-status` after a
  conversation fragment swap. This is outside the pending-send checks. The
  earlier fixture also failed room-fragment readiness before submission.
- Dagger was not run in this side conversation while the main conversation was
  changing its runner. No shared engine, preview, CI session or tunnel was
  restarted.
