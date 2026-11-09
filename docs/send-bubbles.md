# Outgoing bubble presentation

Outgoing send states use the same bubble structure throughout submission.

- Device-local pending submissions and engine `queued`/`dispatching` messages use 80% opacity, without a visible progress label. Pending timestamps inherit the bubble foreground to retain text contrast. The earlier 55% treatment failed accessibility checks.
- Confirmed protocol submission restores normal opacity. Engine acceptance alone does not imply protocol submission or recipient delivery.
- Failures and uncertain outcomes appear below the bubble. Existing retry and queued cancellation actions remain available. Cancelled messages retain their terminal label.
- Local progress announcements remain available to screen readers.
- Local pending bubbles include the timestamp and reply-card structure used by canonical bubbles. They occupy the same timeline position, including day dividers. Queued cancellation is available through the message menu in the enhanced UI; the native form remains available without scripting.

The implementation reuses Goshtoso ChatBubble and Card. The templ outputs were regenerated. `live-chat.cjs` checks local pending rendering, canonical queued/dispatching transitions, normal submitted opacity, below-bubble failures and uncertain outcomes, and retry recovery without duplicate effects. `send-geometry.cjs` checks short, long and multiline plain text at 390 px and 1440 px. It compares bubble position, dimensions and row height through submission, including animation frames during identity replacement.

Native validation passes web Go tests, the complete `live-chat.cjs` and `reply-compose.cjs` fixtures, and the geometry fixture. Geometry covers 24 sends across 390 px/1440 px, light/dark, Goshtoso/Minimal and three text shapes, with 32 accessibility scans. The browser checks use independent HTTP fixtures. Evidence is under the ignored `tests/browser/artifacts/pending-send-*` paths.

The Dagger browser stage runs both the complete live-chat fixture and its `OIE_SEND_GEOMETRY_ONLY=1` mode. Full pipeline validation is recorded in [local CI](local-ci.md). These checks do not send messages through live user accounts.
