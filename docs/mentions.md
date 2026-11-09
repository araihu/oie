# Room mentions

AGREED: typing `@` at a word boundary opens participant suggestions in a room.
Typing filters the available names and nicknames. Selecting a participant inserts
readable text without sending. Escape dismisses the list. Keyboard selection,
touch, IME composition and the existing composer draft behavior remain available.

The menu lists only participants the owning account can currently observe.
It never joins a room or requests hidden identities. An incomplete list is not
a complete membership roster. Loading failure does not prevent plain text entry.

[XEP-0372](https://xmpp.org/extensions/xep-0372.html) describes a client reference
to a real bare JID with Unicode code-point ranges. It is experimental and is not
a server-wide guarantee that all occupants' clients understand mentions.
[XEP-0045](https://xmpp.org/extensions/xep-0045.html) governs room occupant privacy;
[XEP-0421](https://xmpp.org/extensions/xep-0421.html) supplies an opaque occupant
identifier, not a replacement for the real JID required by XEP-0372.
Use protocol metadata only where the adapter has an appropriate observed target.
Otherwise send the same readable `@nickname` text without a structured reference.
Do not infer a real JID from a nickname or treat a room address as the person.

The composer reuses public Goshtoso Textarea, Panel, Avatar and Button components.
The public Combobox owns a separate field, so the existing multiline composer
keeps its text input while an app-owned listbox supplies chat-specific matching.
The browser sends only the opaque participant selection and authored-text ranges.
The engine validates the current room, freezes the delivery plan in the durable
send intent, and owns protocol translation. The frontend has no XMPP client.

## State and action invariants

| State | Action | Required result |
| --- | --- | --- |
| Room draft | Type `@`, then a query | Read-only candidate load, local filtering, no send or join |
| Loading/error/partial candidates | Continue typing or retry | Preserve text/caret; explain incomplete observations or failure |
| Candidate selected | Enter/click/tap | Complete the nickname without duplicating letters after the caret, preserve adjacent punctuation/spacing and retain textarea focus |
| Focus leaves the composer | Tab or outside click while loading | Keep suggestions closed when the delayed response arrives |
| Suggestion panel focused | Escape | Return focus to the composer without reopening the dismissed list |
| IME composition or email text | Type/confirm | Preserve normal text input; no accidental selection or send |
| Selected text edited | Insert/delete/replace | Shift untouched ranges; discard overlapping selections |
| Another account/chat/engine | Restore or delayed response | Keep draft ownership; reject stale candidate results |
| Pending acceptance | Retry | Preserve exact text, selected ranges, reply target and operation key |
| Existing accepted operation | Repeated request | Return the frozen receipt before observing changed participants |
| Forged participant/range/URI | Submit | Reject without creating a send; never accept browser protocol identifiers |
| Plain fallback | Send | Preserve readable text; do not claim remote notification or delivery |

## Verification

`make check` passes, including vacuum, generation drift, golangci-lint, vet,
native tests and Compose validation. The isolated mention browser fixture passes
in English and Brazilian Portuguese at 390 and 1440 pixels, under light/dark
Goshtoso and Minimal themes, including accessibility checks. It verifies Unicode
ranges, edits, IME, drafts, scope changes, failed/partial lists, and recovery of
a lost acceptance response without a duplicate send. Regression cases also cover
delayed responses after Tab/outside dismissal, explicit return to the composer,
Escape from the focused scrollable panel and completion inside an existing
nickname with punctuation or Unicode text. An index-only source export
also builds and passes the fixture, independently of other pending UI edits.

The development Prosody test passes both through Compose and the host endpoint.
It confirms that another client receives the XEP-0372 element and Unicode ranges
in a configured non-anonymous room. Adapter tests cover anonymous, unknown and
contradictory privacy evidence, departures, text fallback and reply-prefix offsets.
Read-only live inspection confirms that the user's room exposes text-mode
candidates and that selecting a suggestion does not send a message.

Dagger's engine logs confirm successful completion of `make check test-race
build build-cli` and the mock build for this run. The local Dagger client later
timed out with exit 124, so this is not a successful CLI completion. Evidence is
saved in `/tmp/oie-mentions-dagger-remote-summary.log` and
`/tmp/oie-mentions-dagger.log`.

The existing reply-composition browser fixture times out at its soft room-route
step with both the preceding binary and this increment. It is not recorded as
passing; logs are `/tmp/oie-mentions-reply-regression.log` and
`/tmp/oie-mentions-reply-baseline.log`. Mention and reply serialization/receipt
checks pass separately in the native and protocol tests above.

The composer-focus follow-up passes `make check` in the shared checkout. The
extended browser fixture passes against builds from both the shared checkout
and an export of committed source plus this JavaScript change. Read-only live inspection verifies Tab dismissal, returning
to the composer and selecting a participant, with no writes or page errors.
For this follow-up, Dagger engine logs again confirm successful checks, race
tests and builds, but the client exits 124 after the 180-second timeout. Logs
are `/tmp/oie-mentions-continuation-dagger.log` and
`/tmp/oie-mentions-continuation-dagger-remote.log`.
