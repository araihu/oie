# Oiê drawer design study

**Review brief.** Someone reading a conversation should be able to find a file, change a personal preference, or manage the room and return to the same place. Use Oiê’s existing identity, type hierarchy and blue link color. Give every destination one title, a predictable way back and one scrolling body. On a phone the drawer uses the available screen. On a wide display the study docks it beside the conversation. Motion is optional. A public edit must be reviewable using touch, keyboard or reduced motion.

Open `index.html` in the repository or serve the checkout locally and visit `/docs/prototypes/drawers/`. All interactions use disposable in-memory fixtures. The initial example is a joined room viewed by an owner using a hosted session. The account, room and files are fictional. Close the drawer to try the conversation, account and settings entry points. Use `?client=desktop#engine` for desktop engine controls, `?role=member#room` for a participant, and `?lang=en#settings` for English. A reload resets edits. This proposal revisits the settings tabs agreed in section 11.5 of the product spec.

## Navigation map

| Entry today | Proposed group and behavior |
| --- | --- |
| Settings / General, Composition | Keep appearance, interface language, display time zone and emoji shortcut languages together. The composition tab currently owns only the last field. Outgoing media quality is also related to composing, but has device scope and belongs with device media controls. |
| Settings / This device | Group media quality, rendered Markdown, automatic images and video thumbnails under media and display. Group system alerts, background notifications and tones under notifications. Group offline text quota, download retention and removal under storage. Preserve browser and device eligibility. |
| Settings / Engine tab and Engine subpage | Use one Engine destination. Group global history retention and receipt defaults here. Offer engine selection, URL and credentials only on clients supporting engine switching. Keep the hosted sign-out confirmation reachable from the Settings footer. Keep passkeys, diagnostics, about and destructive actions reachable at their existing capability gates. |
| Room and direct-chat information | Start with identity and available topic. Group media, links, documents and starred messages together. Group receipt, retention, mute, offline history and eligible archive controls under conversation preferences. Place account connection, session and advertised properties on their own page. Preserve message search and the separately opened encryption workflow. Direct peers have their own observed presence and no room roster or management. |
| Participant lists, participant detail, sender, bans | Use the same blue full-row destination with an avatar or icon, label, optional value and trailing chevron. Distinguish visible occupants, bans, and verified identity. Give access-limited or partial lists their own message. Reuse the same participant detail from info and management, with actions gated by actual privileges. Sender info may be opened directly from the timeline. |
| Room management, invitations, moderation | Group public identity and topic together, followed by participants, invitations and bans as authorized. Keep destructive operations separate. Review changed public fields before publishing. Use explicit target and consequence when confirming a ban, removal or room destruction. |
| Accounts, add account and connection | Account list → detail → connection form when eligible. Keep Add account and Disconnect in their respective footers. Keep connection status distinct from hosted sign-out. Show advanced server overrides only when needed. |
| New conversation, join room, create room | Use a task title with the account in context, labelled fields and one named submit action. Keep server discovery, address entry and permission errors in the task. Review shared room identity before creation. |
| Conversation schedules and edit history | Group scheduled items by day with scoped item actions. Keep revision history and sender details read-only unless a real action is available. Leave the drawer for an explicit jump to a source message or composer edit. |
| About, diagnostics, access keys, reset, protected operations | Use the common detail header. Put technical observations in diagnostics. Show a named recovery action where a read fails. Destructive actions retain their specific confirmation. |

## Components and behavior

The pinned Goshtoso v0.6.4 exports `drawer.Drawer` with a body target, `link.Link`, `button.Button`, `avatar.Avatar`, `form.FieldGroup`, `select.Select`, `combobox.Combobox`, `toggle.Toggle`, `form.FlipSection`, `skeleton.Skeleton`, `alert.Alert` and `emptystate.EmptyState`. Compose Oiê-owned header, scroll body and footer inside the drawer. A destination gets a native link, a boolean gets a labelled switch, a fixed single choice gets a select or radios, and multiple emoji languages remain independently selectable. Keep searchable production comboboxes for the full language/time-zone lists. Tabs are appropriate for sibling media categories with the same filters.

| Interaction | Proposed contract |
| --- | --- |
| Enter, back, close | Swap the active body in one drawer. Enter focuses the destination heading. Back restores originating row and scroll. Close restores the original trigger. At phone widths, contain focus and provide both Back and Close. Keep chat drafts and timeline position across navigation. |
| Read receipts | The heading names the setting. Give the control an accessible name and include the effective inherited choice. Commit a changed selection immediately and announce its result. |
| Retention | Preserve inheritance, unlimited and a custom number of days. Explain the deletion consequence before the choice. Commit a selected preset immediately. Commit a valid custom value on blur or Enter. Do not submit incomplete digits, the first day typed, or a placeholder when custom is chosen. |
| Shared room identity | Goshtoso FlipSection provides read and edit faces. Its Done action alone does not review or submit. Compose an explicit review step that shows changed fields and their previous values. Edit again preserves the draft, cancel discards it, and only Publish changes public identity. Check field permissions and revision on submit. |
| Collections | Full-width search with a separately wrapped include-retracted filter. Keep media chronological in the DOM. Assign natural-height audio and image tiles to lanes, falling back to one column when controls would be cramped. Documents and links remain readable lists. |
| Refresh and failures | A details read already requests a bounded background refresh. Drop the permanent refresh link from the healthy overview. Retry the affected read where it fails. Show stale observations as such when freshness matters. Unknown occupancy and unavailable presence are distinct from zero and offline. |

## Implementation sequence after prototype review

1. Introduce the drawer frame and shared destination row over existing scoped URLs. Migrate Settings, info, then participant/account detail. Preserve direct links, Back/Forward, focus, scroll, locale, theme, chat and account across HTMX swaps. Retain old tab links as aliases.
2. Move settings and chat subpages into that frame. Initial reads carry only identity, available actions and already-known summaries. Fetch management privileges, participants, engine defaults and shared content on entering the corresponding view. Honor server authorization on every read and write.
3. Adapt the existing chronological masonry measurement in `internal/adapters/web/masonry.js`. The drawer currently forces audio to span every column in `shared-actions.css`. Keep media playback, source navigation, stars, dates, pagination and search focus intact.
4. Reuse the existing versioned preference writes. Serialize or coalesce rapid changes, announce pending/confirmed/error, and restore current server truth on conflict. Preserve a failed draft for correction. Retry a read after an ambiguous write before offering another submission. Revalidate a room edit if permission or revision changes during review.
5. Test delayed open/close and cross-chat switches, stale responses, denied permissions, localization, larger text, keyboard traversal, iPhone safe areas and theme variants. Check that an update touches only the active region and its affected summaries. Compare first-open payload, DOM size and time to usable content with realistic lists before rolling out.

The HTML exercises navigation, filters, autosave, room review and representative secondary drawers. Full account provisioning, credential entry, moderation, destructive operations and backend conflict handling belong to the implementation pass.

## Design review references

The baseline is the annotated hosted UI and the templates at Oiê revision `1e923dc1`. Impeccable’s guidance for operational screens informed the single active task, restrained use of color and repeatable row geometry. The following Checklist Design items informed the prototype and its acceptance checks.

- Group settings by the decisions they control. The web checklist suggests elevating frequently changed preferences. The mobile checklist calls for labelled groups and direct access to specific settings. In Oiê, device, engine and conversation are meaningful scopes. [Web app settings](https://www.checklist.design/web-app/settings) · [Mobile settings](https://www.checklist.design/mobile/settings)
- A toggle should expose an unambiguous on/off state. Use one for mute or automatic downloads. An inherited receipt preference has three states and needs a single-choice control that names the effective default. [Toggle](https://www.checklist.design/design-system/toggle)
- Mobile search should update as the query changes and give a useful empty result. Keep the query and filter together while browsing. A single retraction filter fits below the full-width field. [Mobile search](https://www.checklist.design/mobile/search)

### Quick review routes

| Fragment or query | What to try |
| --- | --- |
| `#room` | Open a participant, go back, then open conversation preferences. |
| `#settings`, `?client=desktop#engine` | Compare hosted and desktop settings. Hosted sign-out stays in the root footer. |
| `#media`, `#documents` | Play an audio tile, search documents, include retracted results and clear the search. |
| `#retention` | Try a preset, then a custom period. Tab away or press Enter to commit the day count. |
| `#manage` | Open **Manage room** beneath the room identity. The panel shows **Edit name and topic** followed by the current values. Review changes before publishing. Oiê also displays the room description when supplied by the service. Its current management actions support editing the name and topic. |
| `?role=member#room`, `?state=error#connection` | Inspect permission-limited navigation and recover a failed read. |
| `?state=loading#participants`, `?save=error#receipts` | See a delayed list read, or change a selection to try a failed write and retry. |
| `#accounts` | Add a fixture identity, inspect its connection and return to the list. |

## Prototype verification

- Chromium journeys cover 320, 390, 1105 and 1440 px. Checks exercise search and Back focus, inherited preferences, custom retention, shared-content filters, audio playback and room review. At 390 px the fixture shows two masonry columns. At 320 px it uses one.
- WebKit touch-mode checks on the Mac cover modal keyboard containment, focus return, preserved chat draft, account creation, canceling a delayed read and retrying a failed preference change. Safari in an iPhone simulator was also inspected visually on the room and media screens.
- A local Chromium sample loaded 23,742 bytes of HTML for the room, with 83 elements inside the drawer and no media tiles mounted. Selecting Media mounted four fixture tiles and reused images already present in the chat. These figures describe the static study. Production HTTP reads, authorization and conflict handling require separate measurements against the live application.
