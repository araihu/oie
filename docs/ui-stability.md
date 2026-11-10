# Visual stability policy

**AGREED:** avoid layout shifts caused by asynchronous work, feedback and control state changes. An operation that cannot proceed must leave the current screen, draft, focus and scroll position intact.

- Check eligibility before displaying an editor or another action-specific view. A rejected ArrowUp edit uses a temporary, dismissible Goshtoso toast. Do not briefly render the editor, return a replacement fragment for an unchanged view, or leave an error bar in the composer.
- Keep implementation documentation out of the product interface. Show concise user-relevant states and available actions. Put explanations of engine acceptance, operation keys and internal recovery mechanisms in developer documentation. Technical details belong in a view only when explicitly requested or necessary for a concrete user decision.
- Keep input and action geometry stable during loading, validation, success, failure and retry. Keep control slots in place. Use an overlay or a deliberately reserved area for transient controls and status, without adding a large empty region to the idle screen. Toasts must not participate in the page layout.
- Preserve focus, selection, drafts and the visible message anchor across asynchronous responses and fragment swaps. A delayed response must not replace text entered while the request was pending. Dispose responses and effects belonging to a replaced screen.
- Give media, previews and loading placeholders explicit dimensions or aspect ratios. Replacement content should use the same geometry. Avoid scrollbar changes that unexpectedly resize adjacent content.
- Do not animate height, margins or position to conceal reflow. Respect reduced-motion preferences.

Intentional changes remain allowed: navigation, explicit expansion, opening the keyboard, viewport resizing and the specified textarea autosizing. Text can grow the composer up to its existing limit. An edit toolbar or status must not add another height change on top of that text growth. Entering edit mode reuses the composer, its submit button saves the edit, and X or Escape cancels before dispatch. Existing send-key and IME rules still apply.

## Verification

For interactions affected by a change, browser regressions must include delayed responses, refusal, successful preparation and cancellation. Cover desktop and narrow mobile widths in English and Brazilian Portuguese. Add retry, fragment replacement or media loading cases when the change affects them.

Sample actual element rectangles and scroll positions before, during and after the interaction, including every painted frame during the delayed response. Allow at most one CSS pixel of unintended movement. Assert that an ineligible operation never reveals its editor and that transient feedback disappears or can be dismissed. Verify text entered during the request survives. Measure textarea growth separately from surrounding control geometry.

Do not rely on the cumulative layout shift score alone. It can exclude shifts following recent user input, including the ArrowUp regression that prompted this policy. Screenshots complement geometry and interaction assertions.

This policy applies to new and changed UI. Existing screens need verification before anyone claims the entire app is free of layout shifts.
