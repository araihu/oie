# Message reaction interactions

The WhatsApp reference guides placement and interaction. Received messages expose
the shortcut to their right; own messages expose it to their left. Hover and
keyboard focus reveal the control without changing the bubble's dimensions.
A compact capsule slightly overlaps the source bubble's bottom border and shows
the available emoji counts. Its
Goshtoso Popover provides emoji filters and an explicit action to remove an own
reaction or open the existing selector. Opening or filtering that panel sends
nothing.

Compose public Goshtoso Button, Tooltip and Popover components in ChatBubble's
public rich-content slot. Reuse the existing reaction Dialog and public Dropdown
trigger. Message-specific gesture arbitration belongs to Oie's scoped browser
runtime; it is not a replacement overlay or dependency-internal DOM workaround.
API 0.27 extends the existing read snapshot with verified actor sets. The BFF
read contract references that same snapshot; mutations keep the existing contract.

The actor roster comes from the same canonical latest nonempty observations as
emoji totals. It includes only verified protocol addresses. An active contact
name takes precedence over the reaction's observed name, with the address as a
fallback. Cached avatar hashes resolve only within the same account and exact
verified address. Reading does not discover profiles, join rooms or infer a
personal identity from a nickname. Missing images use Goshtoso Avatar initials.
Names and addresses are text, never markup or executable expressions.

The Popover lists each actor with avatar, name and emoji set. Filters retain only
actors who selected that emoji. Other actors' emojis are read-only. Each own emoji
is an explicit remove action using the existing scoped preflight and immutable
own-set command. A stale removal never turns into an addition if another device
already cleared that emoji. Older engines that omit the optional actor projection
retain the aggregate-only panel without invented identities.

On touch and pen input, two taps within 320 ms and 24 px open the same read-only
eligibility check and selector as the menu. Holding for 500 ms opens the existing
message-actions Dropdown. Moving more than 10 px cancels the hold. These numeric
thresholds are implementation choices, not new cross-platform product rules.
The release click after a hold is consumed before document action handlers, so
lifting the finger cannot select an item from the newly opened menu.

Image/document thumbnail single taps wait for the double-tap interval and then
retain their primary action. A second tap cancels that delayed opening. Links,
inputs and audio/video controls retain native behavior. Scroll, canceled pointers,
Escape, hidden documents and fragment disposal clear pending gesture work. Scope
and message identity are still checked by the existing reaction preparation.

The isolated reaction browser fixture covers the original set/clear, CAS receipt
and recovery cases plus verified actor names, cached photos and missing-photo
initials, literal rendering of markup-like names, stale-removal refusal, summary
accessibility, shortcut side and geometry, summary filters, own removal,
mobile double tap, hold/release, drag cancellation and thumbnail single/double
taps. It samples delayed-preflight geometry and checks English/Portuguese,
light/dark and 390/1440 px layouts with accessibility checks. Changing the capsule
content must not widen short messages. A first pending reaction with no observed
counts uses a side overlay; painted-frame assertions keep the source, composer
and timeline unchanged until a real reaction count arrives.

Verified locally with `make check`, Dagger `make ci-check` and
`tests/browser/reactions.cjs`. The browser matrix covers eight locale/theme/width
combinations and separate touch gesture cases. This increment did not run the
entire Dagger browser pipeline. A read-only smoke check of the isolated live BFF
loaded 50 retained messages at 390 px without JavaScript errors; protocol
mutations were blocked during that smoke check.

For a separate interactive UI fixture without provider traffic, build the BFF
and run `OIE_REACTION_PREVIEW_LISTEN=8088 OIE_WEB_BINARY=... node tests/browser/reactions.cjs`.
This opt-in path retains fixture HTTP state and does not run the browser suite.
It is separate from the real engine and uses test actors and a test avatar.
