# Oie unified chat project discussion

Updated: 2026-11-07, America/Sao_Paulo.

## Purpose and working agreement

Explore the new Arai Hû unified chat application, `oie`, before writing its specification. The intended project repository is `araihu/oie`. This is a durable discussion record, not an implementation specification.

The user subsequently authorized the first specification. The consolidated draft is [Oie first specification](oie-spec.md); this file remains its decision history. Recommendations and open choices in that draft are not automatically accepted requirements.

The user wants to discuss **one question at a time**, going deeply into each decision before moving on. Maintain this record as decisions emerge. After context compaction, read this file and resume the current topic; do not restart the questionnaire or treat proposals as accepted decisions.

## Confirmed direction

- Project repository: `araihu/oie`. The user supplied this name; repository creation or an application scaffold has not been requested at this stage.
- Implement the application in Go with a Goshtoso web interface.
- Start with a plain browser experience. Wails is a possible later desktop host, not a selected dependency or first-release requirement.
- Use hexagonal architecture to connect chat protocols through shared application-facing contracts. XMPP is the initial protocol; future protocols should fit through adapters.
- Support both local and remotely hosted deployments of the Go program. The user explicitly chose both when asked where the Go process should run for the first browser version.
- Support standalone use with local SQLite and an optional remote service for device synchronization. Both hosted-engine and local-engine-with-optional-sync modes are confirmed; sync scope, conflict policies, and service packaging remain undecided.
- Keep domain/application contracts independent of wire transports. The frontend consumes a uniform HTTP/WS/SSE application API; other engine adapters and underlying transports, including Unix sockets, remain possible.
- Account for eventual mobile and desktop use, including synchronization of useful data such as contacts, group chats, preferences, and potentially history.
- Initially embed the engine in the native application process. Closing its window keeps the engine running; explicit quit stops it. Preserve the option of a separate daemon later. This native lifecycle cannot be assumed for an engine hosted entirely in a browser.
- Follow API-first development: define the API contract, lint it with vacuum, and generate Go types/client/server bindings with oapi-codegen as supported. Streaming behavior needs explicit contracts alongside generated HTTP bindings.
- The frontend consumes the same application API whether the engine runs as optional browser WASM, a self-hosted server, an embedded native engine, or a future daemon. It contains no XMPP or other chat-provider client logic. A WASM engine artifact may itself include protocol clients; those remain engine-owned.
- Protocol clients live in engine adapters. An optional browser WASM runtime could use a WASM-compatible Go XMPP client, subject to browser-supported transports.
- The user proposes browser-side routing/bridging so a WASM program can interact with the Goshtoso frontend. This is an optional implementation approach, not a reason to require server-side presentation or to abandon the shared API boundary.
- The engine API returns structured data/events, independent of Goshtoso and HTML. A backend for frontend (BFF) owns Goshtoso rendering and serves HTML pages/HTMX fragments using the engine API. The user explicitly accepted this separation.
- Users can choose a hosted engine or a local engine with optional sync. In hosted mode, user interfaces share the hosted engine; in local mode, device engines maintain their own protocol sessions and stores.
- Each device/UI selects one engine at a time for the alpha, local or hosted. That engine supports multiple protocol accounts and search across those accounts. Simultaneous aggregation of multiple engines in one UI is outside the selected alpha scope; independent device engines may still exchange data through optional sync.
- Drift between independent devices is expected when no application sync path is configured. Users must be told clearly which data stays on a device and which data synchronizes. Do not invent a hidden coordination service or make standalone use depend on hosting.
- React to discovered protocol capabilities. The user specifically wants XMPP capability discovery to inform history behavior. Protocol details remain inside adapters and are translated into application-level capabilities for the BFF.
- Engine-held message history should avoid repeated full downloads and support fast message search. Oie automatically backfills all history available to the user through supported provider APIs, fetching serially to avoid overwhelming the server. Retain stored messages until the user explicitly deletes them, regardless of provider retention. Optional Oie sync also transfers retained message history between local engines, including messages no longer available from the protocol archive. Encryption/indexing and media-service retention details remain open.
- Use a transactional outbox for outgoing messages, durably persisting send intent with the message record and retrying recoverable failures. Send immediately without an artificial undo delay. Cancellation is available while queued or awaiting retry, before dispatch starts; once in flight, display the actual status and do not promise cancellation.
- Support scheduled messages. Scheduling persists a future send intent. If its scheduled time is missed because the owning engine is stopped or offline, send as soon as it is running and connected again, unless cancelled. Use UTC for timestamps and scheduling under the hood; timezone conversion is a presentation concern. Changing a device timezone changes the displayed time, not the stored send instant. Handoff behavior remains open.
- Users may edit pending message content and reschedule before dispatch begins, using the same cutoff as cancellation. Once dispatch begins, reject edits/rescheduling/cancellation and return the actual current status. The engine enforces this atomically, including races and stale UI state.
- Allow editing the user's own already-sent messages in alpha when supported by the protocol and effective conversation permissions, with a visible edited label. Sent-message correction is a separate protocol operation; it does not mutate an in-flight original send or relax the pending-outbox cutoff. Correction failure states and indexing behavior remain detailed design work.
- Preserve previous versions of edited messages that Oie actually received/stored. Show the latest version normally with the edited label and offer a localized View edit history action. Do not promise recovery of revisions never obtained.
- Search matches only the latest message version by default. Offer an explicit localized Include edit history option, with historical matches clearly identifying the matched revision. This does not implicitly enable the separate Include retracted filter; both visibility rules apply when relevant, globally and within a conversation.
- Support retracting the user's own sent messages in alpha when the protocol supports it. Offer a distinct localized Retract message action and a removed-message placeholder, with truthful pending/failure states and no guarantee that other clients erase their copies. This is separate from deleting history on one device or across synced Oie copies. Retracted-message retention follows the preservation policy below.
- Preserve already-stored retracted messages behind a localized Message retracted placeholder with an explicit Reveal original action. Retraction alone does not delete the retained original; explicit Oie deletion remains available under the established scopes. This applies to retracted messages generally, including those retracted by another participant. Oie cannot reveal an original it never obtained.
- Hide retracted content from search results and Media/Files/Links browsing by default, both globally and within a conversation. Provide an explicit localized Include retracted filter to retrieve retained retracted content deliberately. This visibility rule does not delete the stored original or restore explicitly deleted data.
- Show pending and scheduled messages across synced devices, identifying their owning engine and indicating stale status. Initially, editing, rescheduling, or cancelling requires reaching that engine for authoritative confirmation before dispatch. This is an interim limitation: full centralized sync should eventually support these actions from any synced device, including when the original local engine is unreachable. The central coordination mechanism and availability tradeoffs remain open; replication alone must not claim a cancellation succeeded or create duplicate dispatchers. Devices using the same reachable hosted engine can already act through that shared engine.
- XMPP end-to-end encryption support is deferred to the backlog at the user's request. Focus on other product/architecture aspects now; do not keep asking for an immediate encryption decision.
- Unified contacts: group a person's multiple protocol identities under one contact, while keeping each protocol/account conversation separate. No merged cross-protocol timeline is required by this decision. The user accepted the recommendation explicitly.
- Identity linking is manual and reversible initially through Link identity / Unlink identity actions. Linking changes grouping without merging histories or changing queued/scheduled message destinations. Automatic linking suggestions are deferred.
- Support adding/removing protocol contacts and accepting/declining contact requests in alpha where supported, scoped to the selected protocol account. Removing a contact preserves retained conversation history. Provider roster/subscription semantics remain adapter-owned and distinct from Oie contact grouping and aliases.
- Allow starting a direct conversation by entering a protocol address without first adding a contact, where the provider permits it. Clearly select the sending account. Starting a chat does not implicitly add a contact, request presence subscription, or link identities.
- Incoming chats from non-contacts appear in the normal conversation list with a localized Not in contacts label. Receiving a chat does not automatically add its sender to contacts; protocol contact/presence requests remain separate workflows. A separate message-request inbox is not selected for alpha.
- Blocking/unblocking protocol identities is explicitly deferred to backlog. Existing conversation mutes remain alpha scope; do not present muting as a protocol block or history deletion.
- Support multiple simultaneous XMPP accounts in the first usable release, for example personal and work accounts. Keep the owning/sending account clearly identified in each conversation, as accepted with the multiple-account recommendation.
- The alpha is single-user: one Oie owner per instance, with multiple simultaneous protocol accounts and multiple devices still supported. Multi-user hosting is explicitly deferred to the backlog. This supersedes the earlier multi-user alpha decision.
- Built-in Oie authentication is explicitly backlogged. The user wants a functioning app before discussing authentication further. Do not require an auth-provider choice now or infer a selected external/reverse-proxy authentication mechanism. XMPP account authentication remains inherent in connecting protocol accounts.
- The alpha supports joining existing group rooms and creating group rooms, subject to provider support and permissions. Group history follows the established engine persistence, serial full available-history backfill, and retention-until-explicit-deletion policies.
- Support sending room invitations and accepting or declining received invitations in alpha when the protocol supports those operations and permissions allow. Expose effective invitation capabilities through adapters; room discovery, joining by address, and creation remain confirmed. Unsupported invitation/decline operations must not be represented as successfully performed.
- Include a room-details panel in alpha showing the room name, topic, and available participant list. Allow editing name and topic when supported by the protocol and permitted for the current account. Reflect only participant information the provider makes available; this does not promise a complete membership roster. Kicking, banning, and role-management controls are explicitly backlogged.
- Automatically rejoin previously active rooms after reconnecting unless the user explicitly left or access was revoked. Preserve explicit leave decisions across engine restarts; do not mistake an incidental disconnect for leaving. Saved history remains under the retain-until-explicit-deletion policy. Protocol-specific handling of access changes and cross-device room participation remains design work.
- Allow media sharing in the alpha when supported by the provider/server. Discover effective upload capability and limits through engine adapters; the frontend/BFF consumes uniform application capabilities rather than XMPP upload protocols.
- Use the user's four WhatsApp desktop screenshots as the UI reference, implemented with Goshtoso. Browser-first delivery remains the current implementation focus; the desktop reference does not select Wails or automatically add every visible WhatsApp feature to the alpha.
- For unresolved user-facing behavior, prefer a WhatsApp-like experience, respecting explicit Oie choices and protocol capabilities. Do not infer WhatsApp's backend or let its behavior erase agreed differences such as visible edit history and retained retracted originals.
- Explore Goshtoso public components and examples before proposing custom UI. Most required primitives should be reused/composed; only concrete remaining limitations justify generic upstream changes.
- Any Arai Hû dependency can receive generic, reusable upstream changes during Oie development. Keep an open upstream PR for those changes and use the Go pseudo-version of a specific PR commit in Oie while developing. This applies beyond Goshtoso, including Margo and App Shells. Follow the affected repository's contribution/worktree instructions; pin resolvable commits rather than floating PR references.
- Include a responsive mobile web layout in alpha: use the desktop multi-panel arrangement on wide screens and one primary panel at a time on narrow phone screens. Preserve access to conversations, navigation, and details through responsive navigation. This does not select a native mobile shell or change the native-Go-engine/browser-first scope.
- Follow the system/device theme by default, with explicit Light and Dark overrides stored per device. Theme remains a presentation preference and does not synchronize as an account setting. Exact palette and visual assets remain design work.
- Composer defaults: desktop Enter sends and Shift+Enter inserts a newline, with Enter-to-send configurable per device. Mobile Enter inserts a newline; the explicit Send button sends. Preserve IME composition rather than treating its confirmation keystroke as Send. Device/input classification and exact keyboard implementation remain design work.
- Incoming messages auto-scroll the conversation only when the user is already at the bottom. Otherwise preserve the reading position and show a localized New messages button. This does not mark off-screen messages read; the accepted visibility/focus rule still applies. Exact bottom threshold, layout-shift anchoring, and own-send scrolling remain implementation design.
- Open a conversation at its first unread message, or at the latest messages if none are unread. Provide a localized Jump to latest action. Explicit search-result/reply navigation still targets the selected message; exact same-session back-navigation and scroll restoration remain design details.
- Incoming media defaults: automatically load small previews when available and download full files on demand. Users can override this in device-scoped preferences; different devices may intentionally use different settings.
- Store media hashes in SQLite for integrity checking, using advertised attachment hashes when available and computing a hash on a complete download when none is provided. Interpret this as keeping media bytes outside SQLite; attachment metadata/reference storage is still necessary and its exact schema is not selected.
- Downloaded full-file retention is configurable per device. Default: retain downloaded files until the user explicitly deletes them. Automatic eviction is not the default; optional cache-limit/eviction behavior requires device configuration.
- Include browser notifications for incoming messages while the app is open, with per-conversation mute controls, in the alpha. Notification availability is subject to browser/platform support and user permission. Web Push/closed-app notification delivery is explicitly backlogged.
- Suppress browser notifications and notification sounds for the conversation visible in the active, focused window. Other conversations remain eligible under their mute settings, account DND, and device notification preferences. A background or unfocused conversation is not treated as actively viewed.
- Coordinate cooperating tabs/windows, including PWA windows, within the same browser profile and app origin so an eligible incoming message produces at most one notification and one notification sound. Honor active-conversation suppression across those views. Do not assume coordination across independent browser profiles, origins, or devices. The coordination mechanism remains implementation design.
- Separate devices notify independently in alpha, even when another device is active. Each still follows effective conversation mutes, account DND, device preferences, local focus suppression, and received read state. Activity-based notification routing/suppression between devices is explicitly backlogged; same-browser-profile deduplication remains alpha scope.
- Make the browser app installable as a PWA in the alpha. Installation does not itself select a browser WASM engine or guarantee an always-running browser process. Offline cached reading and drafts are confirmed separately below.
- When the PWA cannot reach its engine, display previously cached conversations and preserve drafts with clear offline status. An explicit offline Send action durably saves a device-local pending submission and automatically submits it when the engine becomes reachable. Show Waiting for connection until engine outbox acceptance; composing or saving a draft alone never auto-submits. This does not select a browser WASM engine.
- The PWA caches recently viewed conversations under a configurable per-device storage limit. This presentation cache is distinct from the engine's complete retained history. Drafts and pending sends are separate from cache eviction; do not discard them to reclaim ordinary history-cache space. Numeric defaults, browser storage choice, and precise eviction policy remain undecided. Downloaded full files retain their separately accepted device retention policy.
- Provide a device-specific Keep available offline option protecting a conversation's cached messages from automatic app cache eviction. Respect the configured storage limit and report when additional caching cannot fit. This is separate from synced conversation pins/favorites and does not automatically download full attachments or promise all historical messages are cached. Exact protected-history coverage and quota-full UX remain design work; explicit deletion still applies.
- Support offline search over locally cached content, clearly labeling limited coverage globally and within a conversation. Full retained-history search uses the engine when reachable. A cache miss must not be presented as proof that content is absent from engine history. Apply the accepted current-revision/retracted-content visibility filters to cached search; this does not require a browser WASM engine.
- Offer explicit history-deletion choices: “Delete on this device” (the default scope) and “Delete from all my synced Oie copies,” including the sync service. Preserve the chosen deletion scope through sync/backfill so removed content is not resurrected. This does not select protocol retraction or deletion of other participants' copies.
- Deleting a message removes all its retained revisions and associated search results and message-derived Media/Files/Links entries within the selected scope. Preserve entries belonging to other messages, even if they share a URL or file. Include edit history and Include retracted do not reveal explicitly deleted content. Retain minimal scoped exclusion/deletion metadata to prevent resurrection.
- Message deletion offers a separate Also delete downloaded files option, off by default. File removal respects the selected deletion scope and preserves physical files still referenced by other retained messages. Keeping downloaded files does not preserve deleted message revisions or restore their catalog entries. Exact storage management for retained unreferenced files remains design work.
- Media-file sync is a separate optional setting from message-history sync, off by default and explicitly enabled by the user. When enabled, the sync service can retain downloaded full files for other devices to retrieve even if provider copies expire. Each device continues to follow its own media-download preferences; enabling media sync does not force every file onto every device. History and attachment metadata can sync without automatically uploading full files.
- When optional app sync is enabled, synchronize contact aliases/identity links, pins/favorites, conversation archive state, read positions, and conversation mutes. Keep media-download/retention preferences, theme, notification sounds, browser notification permission, and alpha drafts device-specific. Conversation mutes allow optional per-device overrides.
- Keep archived conversations archived when new messages arrive. Continue updating unread counts, with notifications independently governed by mute settings and device notification preferences. Archiving does not leave rooms, stop message reception, delete history, or automatically mute a conversation.
- Keep drafts device-specific for the alpha and preserve them offline. This remains true when devices use the same hosted engine; retaining a draft does not enqueue a send or enable automatic submission on reconnect.
- Synchronize conversation mute settings, with optional per-device overrides. Notification permission and sounds remain device-specific. Without configured sync, independently running engines can retain divergent shared-mute settings as previously agreed.
- Support quoted replies in the alpha. Users can select a message and reply; the new message displays the referenced sender/excerpt. Carry the reply reference through the protocol-neutral engine API and map it through provider adapters.
- Allow manual forwarding across conversations and accounts in alpha, respecting destination protocol capabilities. Forward as a new outgoing message through the destination account and existing outbox; future protocol adapters follow the same contract. This does not select automatic cross-protocol relaying.
- Forwarding uses a generic localized Forwarded label where supported. Omit the original sender and conversation by default; include source attribution only when the user explicitly chooses it. This does not promise authenticated provenance or uniform native forwarded metadata across protocols. Label transport/fallback details remain design work.
- Forwarding media downloads the selected full file if needed and uploads it through the destination account. Show transfer progress and clear errors for unavailable originals, failed transfers, or unsupported destination capabilities; never silently substitute a source link. The explicit forward action authorizes retrieval of that selected full file independently of automatic incoming-media download preferences. Transfer staging, retry checkpoints, and cancellation mechanics remain detailed design work.
- Support audio-file attachments and inline playback in alpha, subject to protocol attachment capability and browser playback support. Full-file download remains on demand under device preferences; showing an audio player does not authorize automatic full downloads. Built-in voice-message recording is explicitly backlogged. Audio transcoding and exact supported formats are not selected.
- Voice and video calls are explicitly deferred to backlog. The WhatsApp reference does not add call controls or calling workflows to alpha.
- Support emoji reactions in the alpha when the protocol supports them, mapped through provider adapters and exposed through effective application capabilities.
- Support WhatsApp-style rich link previews/media cards. The engine provides structured preview metadata for BFF rendering; link-preview generation is an application service rather than a requirement to embed protocol clients in the frontend.
- Catalog shared links as bookmarks so users can search links, media, and files across chat sources globally and within a direct/group conversation. Interpret “bookmarked” as automatic cataloging of shared links for retrieval; explicit manual favorites or browser-bookmark export are not yet requested.
- Alpha search indexes message text, captions, filenames, URLs, page titles/descriptions, and available media metadata. Document body extraction, image OCR, and audio transcription are explicitly backlogged.
- Matrix, IRC, and Nostr are likely future protocol candidates. Use all three as design references for the shared engine/provider contracts. XMPP remains the initial implementation; no order or implementation deadline for the candidates is chosen.
- Protocol adapters are compiled-in Go packages registered at startup for the alpha. A future plugin capability is explicitly backlogged and is likely viable only in hosted-server mode, per the user. Hosted-only plugin support is an expectation to revisit, not a selected implementation mechanism or final portability guarantee.
- Alpha engine API: HTTP for commands/queries and SSE for structured live events. The BFF translates engine data/events into Goshtoso HTML/HTMX updates. WebSocket is a possible later alternative, not an alpha requirement; transport-neutral application contracts remain in place.
- Resolve concurrent edits to ordinary synced contact/preference fields automatically, accepting the recommended deterministic per-field last-writer-wins policy. Messages, deletion markers, and read positions retain distinct merge rules; exact revision/ordering implementation remains open.
- Internationalization is a requirement from the start. Do not hardcode user-facing expressions in application logic/templates. Use Goshtoso's existing internationalization/expression support and expand reusable support where concrete gaps are found. Oie owns product-specific translations and locale selection.
- Ship English (`en`) and Brazilian Portuguese (`pt-BR`) in the alpha. Follow the device/browser language by default unless explicitly overridden by the user on that device; use English when no supported language matches. Language overrides are device-specific presentation preferences.
- Use SQLite for native local engines, hosted engines, and the optional sync service in alpha. Ship one Go executable supporting the deployment roles, with a data directory for the database and separate media files. Persistence remains behind application-owned interfaces.
- Include manual backup and restore of engine data in alpha, with media files optionally included. Restore merges into existing engine data; the user explicitly rejected fresh-directory-only restore with merging deferred. Backup format, credential treatment, snapshot consistency, and exact merge/conflict rules remain undecided. Sync does not substitute for this backup feature.
- Application entities and revisions use UUIDv7 IDs, preserved across sync and backup/restore, so merge can identify existing entities and revisions. The generator/library and monotonicity policy remain implementation choices. Provider IDs remain adapter-owned external references; UUID identity and embedded timestamps do not replace revision ancestry or provider-level duplicate reconciliation.
- Use immutable entity revisions with their own UUIDs and parent revision references; message edit history consists of these revisions. Conversations reference stable message UUIDs and present a derived current revision. A descendant supersedes its ancestor regardless of timestamps; genuinely concurrent heads resolve by revision creation timestamp in UTC, with revision UUID as a deterministic tie-breaker, preserving alternatives. Never use import time to make old revisions win. Backup restore and sync share type-specific conflict/deletion policies; deletion, read-state, and outbox ownership retain their separate rules.
- Pending and scheduled sends recovered from a backup stay paused until the user reviews and explicitly resumes them. Ordinary engine restart/reconnect still resumes eligible sends automatically, including overdue schedules. Backup recovery must not silently replay stale send intents; reconciliation and ownership checks still apply before an explicitly resumed send can dispatch.
- Alpha engines run as native Go, locally or hosted. The browser-hosted WASM engine is explicitly backlogged; keep the shared application API and adapter boundaries ready for a future browser implementation. PWA installation, offline cached reading/drafts, and reconnect submission remain alpha scope.
- Support protocol read receipts in alpha when available, enabled by default with a per-protocol-account setting to disable sending them. Synchronize this account preference across devices when optional app sync is configured. Keep external read receipts distinct from Oie's internal/synchronized read positions and from provider-acceptance or recipient-delivery acknowledgements. Per-conversation or per-device receipt overrides are not selected.
- Automatically mark messages read only when visible in the active, focused conversation. Message arrival, background tabs, and merely selecting a conversation do not mark unseen messages read. Exact viewport thresholds and read-position representation remain design work; external read-receipt transmission is separately capability-/preference-controlled.
- Include manual Mark as read and Mark as unread actions in alpha, synchronized across devices through optional app sync. Mark as unread is a reminder and does not undo already-sent read receipts. Explicit Mark as read sends protocol read receipts when supported and enabled, respecting the per-account setting, even without individually viewing every message. The visibility/focus requirement applies to automatic read marking; manual-action representation and marker clearing remain detailed design questions.
- Support incoming/outgoing typing indicators where the protocol supports them. Sending typing status is enabled by default with a per-protocol-account disable toggle synchronized across devices through optional app sync. Treat typing as ephemeral state with expiry, not a durable message-history item or send intent to replay after reconnect. Synchronize the preference, not historical typing events.
- Show contact presence and support manual per-account Available, Away, and Do not disturb states where the provider supports them. Synchronize this manual account choice across devices through optional app sync. Provider adapters expose effective supported statuses. Automatic idle/away detection is explicitly backlogged; provider-side presence aggregation across clients remains protocol-dependent.
- Setting an account to Do Not Disturb suppresses Oie browser notifications and sounds for that account while message reception and unread-count updates continue. Synced devices apply the manual account status and its DND suppression when received; divergence without sync or while disconnected remains expected.
- Use `araihu/margo` for a small Markdown/chat-formatting subset inspired by WhatsApp. Protocol adapters translate supported formatting into the provider's representation and fall back readably where unavailable. The user explicitly requested a viability check before fixing implementation details; that check is complete and recorded in `docs/oie-formatting-feasibility.md`.
- Preserve Margo's current Markdown syntax and parsing behavior. Do not introduce a WhatsApp-specific dialect. The small chat subset concerns supported constructs/presentation; provider adapters translate normalized formatting into the appropriate wire format. Any required public semantic-output API should be additive, preserving existing Margo behavior.
- Needed public Margo API changes to expose formatting data are authorized as part of Oie integration; both projects are in the same organization. Preserve current behavior while extending supported public surfaces rather than treating the present API gap as a blocker.
- Oie attempts Markdown rendering by default, with a user option to disable it. Preserve authored message source independently of rendered output and retain readable plain-text fallback when a message cannot be rendered.
- Copy always copies the original message text as `text/plain`, preserving Markdown markers; `**hello**` copies as `**hello**`. A separate action to copy rendered text with formatting markers removed is explicitly backlogged. Copy refers to message content, not the serialized protocol envelope.
- Alpha chat-formatting subset: bold, italic, strikethrough, inline code, fenced/preformatted code, links, lists, and blockquotes, using Margo's current syntax. This selects supported chat constructs, not full document Markdown features.
- Alpha onboarding connects existing XMPP accounts. In-app XMPP server-account registration is explicitly deferred to the backlog, independently of the already-backlogged Oie application authentication.
- Support public-room discovery in alpha when the protocol supports it, alongside joining a room by address. Protocol/account adapters expose effective directory/discovery capability and available room information.

### BFF composition and deployment

Accepted packaging: one Go executable supporting native/local, hosted-engine, and optional sync-service roles, with SQLite and a separate media store under a data directory. The BFF remains a presentation module that can be packaged with the engine. UI routes, fragment rendering, and conversion of engine events into UI updates belong here. Protocol connections, messaging rules, authoritative persistence, and synchronization belong behind engine application services. Exact role-selection flags/configuration are not selected. A possible future browser-local runtime would need equivalent presentation routing.

## Latest user clarification

> if there is some limitation on goshtoso components, we can change upstream, change must be generic and make sense to the lib, we keep a open PR for our changes and use this PR pseudoversion while we develop oie, that is valid for any araihu project we may depend on

Accepted: generic upstream dependency changes tracked in open PRs, with Oie pinned to concrete PR-commit pseudo-versions during development, for any Arai Hû dependency. This steers the requested first spec; it does not require fabricating APIs or PRs before implementation begins.

The user also explicitly requested the first spec, clearly separating agreements/recommendations/open choices; directed unresolved user-facing behavior toward WhatsApp; and requested exploring Goshtoso components/examples. Created `docs/oie-spec.md` with these distinctions, a reuse/gap map, dependency workflow, backlog, proposed milestones, and acceptance scenarios.

UI discovery inspected published Goshtoso v0.6.1 and App Shells v0.1.9 (not selected as a tested dependency pair). Plain bubbles/typing, composer controls, upload controls, cards, overlays, shell/theme, and expressions are available. Concrete generic gaps: chatbubble has no rich-body composition slot; inspected consoleshell hardcodes document language and some labels. Margo semantic projection remains a previously identified gap. No upstream source edits, PRs, Oie scaffold, or Oie executable were created in this specification task.

Previous interview-status clarification:

> yes, how many questions left?

Accepted: incoming non-contact chats appear in the normal list with Not in contacts labeling. The user also asks how many questions remain. The core architecture and product scope are sufficiently defined for a first specification; no further mandatory interview questions are needed before that draft. Remaining technical choices can be proposed defaults or explicitly open decisions, not silently treated as accepted. Avoid continuing a long sequence of minor UI questions.

Previous direct-chat clarification:

> yes

Accepted in response to the direct-chat recommendation: start conversations by address without first adding contacts, where permitted, with the sending account clearly selected. Contact management and presence requests remain separate actions.

Previous contact-management clarification:

> yes

Accepted in response to the contact-management recommendation: per-account add/remove and accept/decline contact-request workflows where supported, preserving history on contact removal. This does not select automatic linking or unblock the separately deferred identity-blocking feature.

Previous identity-blocking deferral:

> backlog

Accepted in response to the identity-blocking recommendation: defer blocking and unblocking to backlog. The proposed per-account identity-level behavior is design context for future discussion, not an implemented/confirmed alpha feature. Contact grouping, mutes, and history policies remain unchanged.

Previous explicit-read receipt clarification:

> go with rec, mark as read should send protocol read receipts when supported and enabled, respecting the account setting

Accepted: explicit Mark as read emits protocol read receipts where supported and enabled, respecting the account-level sending preference. This is an explicit user action independent of automatic visible/focused read marking. Command boundaries, deduplication, and reconnect handling remain implementation work; receipt emission must not be triggered anew simply by importing/syncing the same read state.

Previous manual read/unread clarification:

> yes

Accepted in response to the manual read/unread recommendation: provide both actions, synchronizing the choices with optional app sync. Mark as unread acts as a reminder and cannot retract sent read receipts. Whether explicit Mark as read emits external receipts remains unselected.

Previous conversation-opening clarification:

> go with rec, open at the first unread message, or latest if none are unread, with a jump to latest action

Accepted: initial conversation position at the first unread message, falling back to latest when no unread messages exist, with Jump to latest. Keep this distinct from preserving an already-open timeline's reading position when new messages arrive.

Previous incoming-scroll clarification:

> yes

Accepted in response to the timeline-scroll recommendation: auto-scroll incoming messages only when already at the bottom, otherwise preserve position and offer New messages. Conversation-opening position and explicit navigation remain separate decisions.

Previous composer-keyboard clarification:

> it does

Accepted in response to the composer-keyboard recommendation: desktop Enter-to-send and Shift+Enter newline, configurable per device; mobile Enter newline with explicit Send button. This governs deliberate submission and does not change durable offline-send or engine outbox semantics.

Previous theme clarification:

> go with rec, follow system theme by default, with light and dark overrides per device

Accepted: System is the default theme mode, with explicit Light/Dark overrides scoped to the device. The dark reference screenshots are layout/style guidance, not a dark-only requirement.

Previous responsive-layout clarification:

> yws

Interpreted as yes to the immediately preceding responsive-layout recommendation. Accepted: responsive alpha web UI for desktop and phones, with multiple panels on wide screens and one primary panel at a time on narrow screens. Exact breakpoints/navigation and native mobile packaging remain unselected.

Previous offline-search clarification:

> yes

Accepted in response to the offline-search recommendation: search locally cached content while offline and clearly identify limited coverage. Use the engine for full retained-history search when reachable. Offline cached results retain the same revision/retraction visibility policies; exact browser index/store remains an implementation choice.

Previous offline-pinning clarification:

> yes

Accepted in response to the offline-pinning recommendation: device-specific Keep available offline protects cached conversation messages from automatic eviction, within the storage limit, showing when more content cannot fit. This does not waive explicit deletion, change downloaded-file preferences, or guarantee protection from browser/site-data removal.

Previous bounded-cache clarification:

> go with rec, cache recently viewed conversations with a configurable per-device storage limit, keeping drafts and pending sends separate from cache eviction

Accepted: bounded device-scoped PWA caching of recently viewed conversations, protecting drafts and pending sends from ordinary cache eviction. Engine retention is unchanged. This does not select a numeric quota, a browser storage mechanism, or automatic eviction of downloaded full media files.

Previous physical-file deletion clarification:

> Yes

Accepted in response to the downloaded-file deletion recommendation: offer Also delete downloaded files separately, off by default, respecting deletion scope and preserving files referenced by other retained messages. Message/revision/search-entry deletion remains effective even if the binary file is retained.

Previous deletion-extent clarification:

> Yes

Accepted in response to the deletion-extent recommendation: deletion covers all retained revisions and associated search/shared-content entries for that message within the chosen scope, preserving entries belonging to other messages. This does not yet select removal of physical downloaded attachment files. Scoped deletion/exclusion metadata still prevents revival through sync, backfill, or backup merge.

Previous deletion-default clarification:

> Yes

Accepted in response to the history-deletion default recommendation: select Delete on this device by default, keeping Delete from all my synced Oie copies as an explicit alternative. Existing hosted-engine per-device scope and anti-resurrection requirements remain; this does not select physical purge mechanics or protocol retraction.

Previous UUID-version clarification:

> Yes

Accepted in response to the identifier-version recommendation: use UUIDv7 for application entity and revision IDs. Keep explicit revision ancestry, UTC revision creation timestamps, and deterministic concurrent-revision selection as accepted. UUIDv7 ordering does not supersede these merge rules. No particular Go UUID library or schema/storage encoding is selected.

Previous revision-ordering and ULID clarification:

> Yes, I guess that ulid also would depend on device clocks

Accepted in response to the ordering recommendation: ancestry first, then UTC creation timestamp and revision UUID for genuinely concurrent revisions, retaining alternate revisions. Stable logical message references and derived current selection follow the accepted model. The user also asks about ULID clock dependence; this is not a request to replace UUIDs with ULIDs or a selection of UUID version.

Verified: ULID embeds a 48-bit Unix millisecond timestamp and 80 bits of randomness. Its monotonic generator addresses ordering within one generator; it does not synchronize independent device clocks or establish revision ancestry. Sources: https://github.com/ulid/spec#components and https://github.com/ulid/spec#monotonicity . UUIDv7 also embeds Unix milliseconds; neither sortable identifier substitutes for the accepted revision ordering: https://www.rfc-editor.org/rfc/rfc9562.html#section-5.7 . UUIDv7 for entity/revision IDs is the next recommendation, not yet accepted.

Previous revision-model clarification:

> Yes, entities should have revisions also with uuid, ordered by date or explicit marker current, conversation refer to the latest revision of a message, edition history is the revisions, conflict may happen on conversation if imported refers to different revisions I guess, we can resolve by date on conflict, most recent win?

Accepted: reuse sync conflict/deletion policies for backup merge; entities have UUID-identified revisions; message edit history is revision history and the conversation displays the current/latest message revision. The user is exploring date ordering versus an explicit current marker and most-recent-wins conflict resolution; do not record raw timestamp-only LWW or a schema as finalized.

Recommendation pending acceptance: stable message UUID in conversation references, immutable revision UUIDs with entity UUID, UTC authored timestamp, and parent revision references where known. Keep current_revision_id as a derived selection/cache rather than independently authoritative conflicting is_current flags. A descendant supersedes its ancestor even when clocks disagree; among genuinely concurrent heads choose deterministically by authored UTC timestamp and revision UUID as tie-breaker, retaining alternate revisions. Import/receive time must not make stale backup records win. Parent references may be absent/incomplete for provider-imported revisions; adapters must preserve native ordering/provenance evidence rather than invent ancestry. Clock skew can still affect concurrent-head selection. Deletion markers, per-field settings merge, read positions, and outbox dispatch ownership retain their special rules and must not become generic whole-entity LWW.

Verified reference: RFC 9562 section 6.1 discusses clock rollback/adjustments and explicitly provides no guarantee that a UUID timestamp is close to actual time. Time-sortable UUIDs alone are not a causal ordering solution: https://www.rfc-editor.org/rfc/rfc9562.html#section-6.1 .

Previous merge-restore correction:

> No, merge, entities should have uuids

Accepted correction: alpha backup restore merges into existing data, using stable entity UUIDs for identity. Do not defer merge or require an empty/fresh data directory as the only restore mode. The prior accepted pause/review rule for recovered pending sends still applies; exact handling of already-existing current outbox entries versus newly recovered intents needs design. UUIDs identify entities but revision/deletion metadata and type-specific rules are still needed to merge divergent state.

Previous restored-send clarification:

> Yes

Accepted in response to the restored-send recommendation: pause pending/scheduled sends restored from backups until reviewed and explicitly resumed. This is a backup-recovery exception, not a change to ordinary restart/reconnect or overdue-send behavior. An old backup can precede delivery, cancellation, edits, or ownership changes; explicit resume does not itself resolve ambiguous provider acceptance or grant competing dispatch ownership.

Previous backup-scope clarification:

> Yes

Accepted in response to the backup-scope recommendation: manual engine-data backup and restore in alpha, with optional media inclusion. This selects the feature, not its archive format, credential policy, merge semantics, or automatic execution of restored send intents.

Previous media-sync default clarification:

> Yes

Accepted in response to the media-sync default recommendation: optional media-file sync defaults off and requires explicit enablement, separate from history sync. History and attachment metadata sync do not themselves authorize full-file upload. Existing per-device download preferences remain in effect.

Previous cross-device notification clarification:

> Yes

Accepted in response to the cross-device alert-scope recommendation: independent device notifications in alpha, with activity-based routing/suppression between devices deferred to backlog. Existing mute/DND/read-state rules and local focus/tab coordination still apply; this does not disable ordinary read-state sync or require replaying historical messages as alerts.

Previous multi-tab notification clarification:

> Yes

Accepted in response to the multi-tab alert recommendation: deduplicate browser notifications and sounds across cooperating tabs/windows in the same browser profile, including PWA windows. Apply active-conversation suppression across these views. Cross-device alert routing/suppression is not selected by this decision.

Previous focused-conversation notification clarification:

> Yes

Accepted in response to the focused-conversation notification recommendation: suppress browser notifications and sounds for the conversation visible in the active, focused window. Continue applying normal mute/DND/device eligibility to other conversations. Multi-tab/window notification coordination and cross-device behavior remain undecided.

Previous automatic-away scope clarification:

> Yes

Accepted in response to the automatic idle/away recommendation: defer automatic idle/away detection to backlog and retain manual account availability in alpha. This does not suppress truthful connection/disconnection reporting or alter the accepted synced manual status and DND notification policy.

Previous manual-availability scope clarification:

> Yes

Accepted in response to the manual-availability scope recommendation: synchronize manual Available/Away/Do Not Disturb per protocol account across devices with optional app sync. The DND notification policy follows the synced account choice once received. Do not interpret a synchronized desired status as a guarantee of uniform protocol-server presence aggregation or immediate propagation to disconnected engines.

Previous DND-notification clarification:

> Yes

Accepted in response to the DND-notification recommendation: manually setting an account to Do Not Disturb suppresses its browser notifications and sounds; message reception and unread counts continue. This does not yet select whether manual presence/DND propagates across independent device engines.

Previous typing-policy clarification:

> Yes

Accepted in response to the typing-policy recommendation: enable sending typing status by default where supported, controlled by a per-account toggle synchronized across devices with optional app sync. The actual typing events remain ephemeral; disabling sending is separate from receiving/displaying other participants' typing status.

Previous read-receipt scope clarification:

> Yes

Accepted in response to the read-receipt scope recommendation: the receipt-sending preference is per protocol account and synchronized across devices with optional app sync. Personal/work accounts can differ. Devices using a shared hosted engine use its account setting; independent local engines may diverge without sync or temporarily while disconnected, as previously accepted.

Previous read-receipt default clarification:

> Yes

Accepted in response to the read-receipt default recommendation: enable sending read receipts by default where supported, with the already-confirmed disable setting. Internal read tracking and optional read-state sync remain independent. Account/device/conversation preference scope is not yet selected.

Previous automatic-read clarification:

> Yes

Accepted in response to the automatic read-trigger recommendation: mark messages read only when visible in the active, focused conversation. Receipt alone and background UI activity do not mark unseen messages read. This does not decide manual Mark as read behavior or the default/scope for external receipt sending.

Previous archive-behavior clarification:

> go with rec, keep archived conversations archived when new messages arrive, update unread counts, and let mute settings control notifications independently

Accepted: persistent archive placement on new arrivals, live unread-count updates, and independent mute-driven notification behavior. Archive state remains part of optional app sync as previously accepted.

Previous room-rejoin clarification:

> go with rec, automatically rejoin previously active rooms after reconnecting, unless explicitly left or access was revoked

Accepted: automatic room rejoin on reconnect with explicit-leave and revoked-access exceptions. Explicit leave must remain effective across restarts; access/removal failures need truthful status rather than endless join attempts. This does not introduce automatic membership changes on other engines or delete retained history.

Previous moderation-scope clarification:

> deffer

Accepted in response to the moderation-scope recommendation: defer kicking, banning, and role-management controls to backlog. Confirmed room creation, invitations, details, and permitted name/topic editing remain alpha scope. This defers user-facing moderation actions, not handling protocol events or permission failures the engine receives.

Previous room-details clarification:

> go with rec, include a room details panel with name, topic and available participant list, allowing name and topic editing when supported and permitted

Accepted: room-details display of name/topic/available participants and capability-/permission-aware editing of the name and topic. This does not select role assignment, kicking/banning, or advanced room configuration.

Previous room-invitation clarification:

> go with rec, support sending room invitations and accepting or declining received invitations when the protocol supports it and permissions allow

Accepted: capability- and permission-aware sending, acceptance, and decline of room invitations. Advanced room administration, roles, and moderation are not selected by this decision.

Previous calling-scope clarification:

> yes

Accepted in response to the calling-scope recommendation: defer both voice and video calls to backlog. Alpha remains focused on messaging, history, media, and sync; audio-file attachments and inline playback are still confirmed.

Previous audio-scope clarification:

> go with rec, add voice recording to backlog, support audio file attachments and inline playback in alpha

Accepted: audio attachments and inline playback in alpha; built-in microphone/voice-message recording deferred to backlog. Existing download, retention, and optional media-sync policies still apply. This does not decide voice/video calling.

Previous media-forwarding clarification:

> go with rec, forwarding media should download the full file if needed and upload through the destination account, with progress and clear errors, no silent link fallback

Accepted: full-file retrieval as needed and destination-account upload for media forwarding, with progress and actionable errors. No silent source-URL substitution. Preserve the destination account/capability boundary and existing outbox semantics; this does not change automatic incoming-media preferences for unrelated files.

Previous forwarding-attribution clarification:

> yes

Accepted in response to the forwarding attribution recommendation: a generic Forwarded label where supported, with original-sender/conversation attribution included only on explicit user choice. Source identities and conversation names must not be automatically added to the forwarded payload.

Previous manual-forwarding clarification:

> go with rec, allow manual forwarding across conversations and accounts, respecting the destination protocol capabilities

Accepted: manual forwarding between conversations and accounts as a new outgoing message using the destination account, constrained by destination capabilities. Alpha implements XMPP; the contract should accommodate future cross-protocol forwarding. Attribution, attachment re-upload/fallback UX, and any forwarding-specific editing remain detailed discussion topics.

Previous edited-version search clarification:

> yes

Accepted in response to the edited-version search recommendation: search only the latest version by default, offer Include edit history, and clearly label historical matches. Keep this distinct from Include retracted so historical-version search does not bypass retraction visibility.

Previous revision-retention clarification:

> yes

Accepted in response to the revision-history recommendation: retain received/stored previous versions of edited messages, display the latest normally, and provide View edit history. This covers observed sent-message corrections; it does not require recording every draft keystroke or pending-content change. Previous-version search behavior remains open.

Previous retracted-search clarification:

> go with rec, hide retracted content from search and media/files/links by default, with an include retracted filter

Accepted: default exclusion of retracted content from search and shared-content browsing, with an explicit Include retracted filter. Apply consistently to global and per-conversation search and Media/Files/Links views. Retention and Reveal original remain as accepted; query/index design must enforce the visibility filter without treating retraction as deletion.

Previous retracted-original retention clarification:

> go with rec, preserve retracted messages behind a placeholder with a reveal original action

Accepted: retain already-stored originals of retracted messages, show a placeholder by default, and allow explicit Reveal original. This resolves incoming-retraction retention consistently with retain-until-explicit-deletion. The user's wording covers retracted messages generally; do not introduce a purge exception for the user's own retractions. Index/search/library treatment, derived previews, quoted excerpts, and presentation of retained revisions still require design.

Previous own-message retraction clarification:

> yes

Accepted in response to the retraction recommendation: support retracting one's own sent messages where supported, through a separate Retract message action and a removed-message placeholder; removal from other clients cannot be guaranteed. This does not decide whether Oie purges stored originals after incoming or outgoing retraction.

Previous sent-message editing clarification:

> go with rec, allow editing own sent messages when the protocol supports it, with an edited label

Accepted: capability-aware editing of one's own sent messages, with a visible localized edited label. Keep this distinct from pre-dispatch outbox editing. It does not imply provider retraction, forwarding, or a user-visible revision history.

Previous UTC clarification:

> use always utc under the hood, timezone is presentation concern

Accepted: UTC for internal timestamps, persisted scheduling instants, and API time representation. Convert local date/time input at the presentation boundary and display timestamps in the device's presentation timezone. Device timezone changes do not reinterpret an existing scheduled instant. An originating timezone is not required engine scheduling state. Exact timestamp encoding, DST input ambiguity UX, and recurrence are not selected.

Previous cross-device pending-send clarification:

> go with rec, show pending and scheduled messages across synced devices, but require reaching the owning engine to edit, reschedule or cancel, this is temporary, with full fledged sync on centralized host user should be able to do these edits from whatever device

Accepted: synced visibility and initial owning-engine confirmation for pending-send changes. The user explicitly treats this restriction as temporary and wants any-device control through full centralized sync later. Do not make direct reachability of the original local engine a permanent architectural requirement. A future centralized command/dispatch authority, ownership protocol, or equivalent coordination must preserve the pre-dispatch cutoff; exact design and offline dispatch policy are unresolved. A centrally saved change request is not necessarily an effective edit/cancellation until dispatch coordination makes it authoritative.

Previous engine-selection clarification:

> go with rec

Accepted in response to the one-engine-per-device recommendation: each device uses one selected local or hosted engine at a time, with multiple protocol accounts and search across those accounts. This does not transfer pending sends when switching engines; migration and handoff remain unresolved.

Previous public-room discovery clarification:

> go with rec, support public-room discovery when the protocol supports it, alongside joining by room address

Accepted: capability-aware public-room discovery plus room-address entry/joining. Room creation remains accepted; advanced administration is still unspecified.

Previous XMPP registration deferral:

The user deferred in-app XMPP account registration; alpha users connect existing accounts. Oie's own built-in authentication is separately backlogged.

Previous Markdown subset acceptance:

The user accepted bold, italic, strikethrough, inline/fenced code, links, lists, and blockquotes using current Margo syntax. Margo semantic-output API and provider encoding remain design work.

Previous Copy decision:

The user selected Copy original text including Markdown markers as text/plain, and explicitly backlogged the alternative rendered-text copy action. Markdown presentation preference does not change normal Copy output.

Previous Margo integration and presentation direction:

The user authorized needed public Margo APIs, selected Markdown rendering enabled by default with a disable option, and required plain-text clipboard output. Exact toggle scope, chat subset, and semantic API signatures remain design work. This remains pre-spec discussion.

Previous syntax decision:

The user requested keeping Margo's current behavior. Preserve current Markdown semantics; any public API additions should retain existing parsing/rendering behavior.

Previous formatting and feasibility request:

The user requested a small Margo-backed Markdown subset for chat, provider-supported formatting translation, and a viability check first. The check ran a temporary public-API consumer and consulted primary protocol specifications; results are in `docs/oie-formatting-feasibility.md`. No library source was changed.

Previous presence acceptance:

The user accepted contact presence and manual per-account Available/Away/Do not disturb where supported, with adapter-declared statuses. Automatic presence changes, invisible mode, and DND notification behavior remain unspecified.

Previous typing-indicator decision:

The user accepted protocol-supported typing indicators with an option to disable sending typing status. Read receipts and internal read-state sync remain as previously selected.

Previous read-receipt acceptance:

The user accepted protocol-supported read receipts in alpha with a setting to disable sending them. Sharing Oie's own read positions remains separate.

Previous WASM scope decision:

The user explicitly backlogged the browser WASM engine and selected native Go engines for alpha, preserving the shared API for future WASM integration. Existing PWA/offline UI requirements remain.

Previous storage/deployment acceptance:

The user selected SQLite for local/hosted engines and sync service, one executable, and a data directory for database plus separate media files. Exact role flags, schema/driver, migrations, layout, and packaging targets remain implementation decisions.

Previous offline Send acceptance:

The user's “rec” adopted local pending submissions from explicit offline Send, automatic submission on reconnect, and truthful waiting status before engine outbox acceptance. Draft composition alone does not auto-send. Earlier interrupted typo messages did not initiate actions.

Previous language-selection decision:

The user requested device language by default unless explicitly overridden. Use a device-specific override and English fallback when no supported language matches.

Previous initial-language selection:

The user selected English and Brazilian Portuguese for alpha.

Previous sync and internationalization requirements:

The user chose automatic conflict resolution and required internationalization from the start, without hardcoded expressions, using and expanding Goshtoso's support. This remains a requirements discussion; no library or app implementation has been requested yet.

Previous API transport acceptance:

The user accepted HTTP commands/queries plus SSE structured events for the alpha, with BFF-owned HTML updates. WebSocket remains a potential later adapter rather than an alpha requirement.

Previous adapter-packaging decision:

The user selected compiled-in adapters and explicitly backlogged a future plugin capability, likely hosted-server-only. Plugin mechanism/transport remain future design work.

Previous future-protocol candidates:

The user identified Matrix, IRC, and Nostr as likely follow-on protocols and design references. XMPP remains first; no order for the candidates has been selected.

Previous search scope and backlog decision:

The user selected metadata search for the alpha and explicitly backlogged document text extraction, OCR, and audio transcription. Global/per-chat search, link cards, reactions, and automatic link cataloging remain as previously recorded.

Previous reactions and shared-content requirements:

The user requested protocol-supported emoji reactions, WhatsApp-style link-preview media cards, and links “bookmarked” so media/files/links can be searched across sources globally and within a chat/group. Automatic shared-link cataloging is the working interpretation; manual favorite semantics remain separate.

Previous reply acceptance:

The user requested quoted replies, using sender/excerpt previews and a reply reference in the engine API. Full threads and modification of already-dispatched messages remain separate.

Previous mute-scope decision:

The user accepted synchronized conversation mutes with optional per-device overrides while notification permission and sounds remain device-specific.

Previous draft-scope acceptance:

The user selected device-specific alpha drafts with offline preservation. Draft synchronization and automatic submission on reconnect are not part of this choice.

Previous metadata split acceptance:

The user accepted synchronizing contact aliases/identity links, pins/favorites, conversation archive state, and read positions, while media download/retention, theme, notification sounds, and browser notification permission remain device-specific. Conversation mute scope remains open.

Previous media-sync decision:

The user selected media sync as a separate optional setting, preserving each device's download preferences. Default on/off, upload selection, and service quota details remain unspecified.

Previous deletion-scope acceptance:

The user accepted both device-local history deletion and deletion across all synced Oie copies, including sync-service storage, with explicit labels. Preserve deletion scope to prevent backfill/sync resurrection; protocol retraction remains separate.

Previous history-sync acceptance:

The user replied “yes” to optional cross-device replication of retained message history through Oie's sync service, including messages the XMPP server no longer retains. Media-file replication remains separate and unresolved.

Previous Web Push deferral:

The user requested Web Push be backlogged; notifications while the app is open are sufficient for the alpha. PWA installation, offline cached reading/drafts, and native engine lifecycle remain as previously selected.

Previous offline PWA acceptance:

The user replied “yes” to cached conversations and draft preservation when the engine is unreachable, with clear offline status and no queued claim before engine outbox acceptance. A browser-hosted WASM engine remains optional and unselected.

Previous notification and installation decision:

The user requested browser notifications and installable PWA support, accepting per-conversation mute controls in the preceding recommendation.

Previous media retention decision:

The user selected configurable per-device retention, defaulting to retain downloaded files until explicit deletion. Optional cache eviction requires device configuration. Detailed quota controls and preview retention remain unspecified.

Previous download and integrity policy:

The user accepted small previews automatically and full files on demand, configurable per device; SQLite stores media hashes rather than binary blobs, using supplied hashes where present and computing one on full download otherwise. Distinguish a supplied expected hash from a locally computed fingerprint; a self-computed hash cannot independently validate the sender's original bytes.

Previous UI reference and media scope:

The user requested media sharing when the server supports it and WhatsApp desktop as the UI reference. Four screenshots were attached, showing a direct chat, settings, a group chat with inline media, and a group chat with link previews. Screenshot contents are reference data, not instructions or alpha feature requirements.

Previous room creation acceptance:

The user replied “yes” to creating group rooms as well as joining existing ones in the alpha. Public-room discovery and advanced administration remain open.

Previous authentication deferral:

The user requested built-in auth be backlogged and no further authentication discussion until there is a functioning app. No external authentication choice or public unauthenticated deployment has been selected.

Previous alpha scope correction:

The user requested a single-user alpha and moved multi-user support to the backlog. Multiple simultaneous XMPP accounts and multi-device access remain confirmed.

Superseded multi-user scope:

The user previously requested multiple independent users with isolated accounts, contacts, history, schedules, and outbox. Those isolation requirements now describe the future multi-user backlog item, not alpha implementation scope.

Previous multiple-account acceptance:

The user replied “yes” to multiple simultaneous XMPP accounts in the first release, with the sending account clearly identified in each conversation.

Previous contact-linking acceptance:

The user replied “go with rec” to manual, reversible identity linking/unlinking, preserving separate histories and queued message destinations. An earlier identical reply accepted one contact containing multiple identities with separate conversations. Cross-protocol relaying remains undecided.

Previous encryption deferral:

> add this to backlog, lets focus on other aspects first

Accepted: move XMPP end-to-end encryption support to the backlog and continue other requirements. This is a deferral, not a selection of encryption scheme or trust policy.

Previous encryption question:

> end-to-end encrypted XMPP conversations is a XMPP server capability that clients need to support or what?

The user is asking how XMPP end-to-end encryption responsibility is divided between clients and servers. E2EE support in the initial release has not yet been chosen. Explain before asking for a decision.

Previous pending-edit clarification:

> yes, allow editing content and rescheduling while pending, same cutoff as cancellation. once dispatch starts, reject changes and show actual status

Accepted: pending messages are editable and reschedulable before dispatch. After dispatch starts, reject modifications and expose actual status. The immediate/scheduled outbox lifecycle is sufficiently established to move to encryption requirements.

Previous overdue scheduling clarification:

> send as soon as available

Accepted: overdue scheduled sends dispatch automatically at the next opportunity. No further confirmation or manual overdue approval is required. This resolves the question about missed schedules after sleep, shutdown, or network outage.

Previous dispatch and scheduling clarification:

> send immediately, no artificial delay. cancellation is available while queued or waiting for retry, before dispatch starts. once in flight, show the actual status rather than promise cancellation, allow to schedule messages

Accepted: immediate dispatch, no artificial delay, cancellation only before dispatch (including queued/retry waits), truthful in-flight status, and scheduled sending. A past attempt with unknown acceptance must still retain that uncertainty; cancelling future retries cannot guarantee it was never delivered.

Previous retention and outbox clarification:

> yes, retain history until the user explicitly deletes it, regardless of server retention. also, we use transactional outbox pattern to guarantee the eventual message delivery, ui should allow to cancel send

Accepted: independent retention until explicit user deletion, transactional outbox for reliable outgoing sends, and UI cancellation. Qualify delivery guarantees precisely: durable local intent and retries do not by themselves guarantee exactly-once recipient delivery or success despite permanent provider rejection. Cancellation semantics during an in-flight/uncertain send require a focused decision.

Previous backfill clarification:

> oie backfills all available history, but serially in such way to not overwhelm server

Accepted: automatic full available-history backfill with serial, server-friendly retrieval. Exact scheduling limits, page sizes, pacing, and prioritization remain implementation proposals.

Previous repository and history clarification:

> (project repo will be araihu/oie)
>
> should optional sync preserve message history independently of the protocol server, or initially synchronize only app data such as preferences, aliases, pins, and read state?
>
> R: It depends, does XMPP server has operation to expose it capabilities, if so app should react to it, if we store message history can it be used to prevent loading chat/rooms history for server fully everytime? having messages stored outside the protocol would make us able to do message search pretty quickly

The repository is `araihu/oie`. The user wants capability-aware history retrieval and sees value in engine-held history for incremental loading and fast search. This does not yet select full cross-device history replication or a retention policy.

Previous execution mode clarification:

> Both modes, user should be able to choose between a hosted engine or a local engine with optional sync. What are the implications of supporting both? Drift is expected, without a hosted engine there is not a bus to sync preferences and users should know that

Accepted: both execution modes and explicit, expected divergence of app-owned state when no sync path exists. The user is asking for implementation implications before the next decision.

Previous BFF clarification:

> you rec is correct, we will need some sort of bff to serve the htmx fragments

Accepted: structured engine API plus a BFF for Goshtoso/HTMX presentation. The BFF's packaging and runtime placement remain implementation details to settle.

Previous API boundary clarification:

> wasm can interact with goshtoso frontend being a client side router, the wasm program talks to xmpp using a go client (if wasm compilable)
>
> that is just an option so our frontend talks to uniform api be it wasm or self hosted server or daemon, also frontend would not need to bundle a xmpp client or any client of any other chat protocol for that matter, it expects and http/ws/sse contract (api first, we use vacuum and oapi-codegen to generate and lint)

The frontend/engine API boundary and API-first tooling are confirmed. WASM is an optional runtime; it does not move protocol responsibilities into the presentation layer. A WASM adapter would preserve application request/response/event semantics through browser-local routing/bridging rather than require ordinary listening network sockets.

Previous lifecycle and storage question:

> lets go with embedded engine initially, keep it running when the window closes but stop on explicit quit, with the option for a separate daemon later. Now, even for an webapp, we could use wasm for the engine, but i dont think that there is surface to have a sqlite database on the filesystem of the device is there?

The embedded-engine lifecycle is accepted. Browser-hosted WASM is now being explored; it is not a selected runtime or a first-release requirement.

Earlier transport clarification:

> Both, that is, if viable, transport does not need to be strictly http, hexagonal arch should make it fairly easily to use any network protocol, even unix sockets in case of a wails app
>
> I want to go deeper into this before we proceed, write down what we talking so far to protect from compaction

This earlier “Both” referred to local and server-hosted Go deployments. The latest clarification additionally confirms a choice between a hosted engine and local engines with optional sync.

## Architectural distinctions raised

These are considerations and proposals, not accepted implementation choices.

- A repository adapter can expose SQLite or remote persistence behind a contract, but remote persistence alone does not implement device synchronization. Offline writes, conflicts, deletions, retry behavior, and deduplication need explicit rules.
- Distinguish deployment location, UI-to-engine communication, engine-to-chat-provider communication, and persistence/synchronization. Each can have different adapters and transport requirements.
- The BFF renders Goshtoso HTML/HTMX fragments while the engine API exposes structured operations and events. HTTP handlers, a desktop bridge, or an IPC adapter can translate into application operations without putting wire concepts into the domain.
- HTTP is an application protocol; TCP and Unix sockets are underlying transport options. Using a Unix socket does not itself require replacing HTTP. A separate desktop process could also use another IPC protocol.
- An ordinary browser cannot directly open a Unix socket. Such a socket would need a native intermediary or process boundary behind the browser-facing interface.
- Code modularity does not require a separate process for each component. A local UI adapter could invoke an embedded engine directly; a separate daemon is an additional deployment choice.
- Closing a browser tab does not inherently stop the serving Go process. Background engine execution and a separately installed daemon are distinct choices; a desktop host could also keep an embedded engine running after hiding its window.
- Hexagonal boundaries make adapter replacement possible but do not make network transports equivalent. Streaming events, cancellation, authentication, ordering, retries, and error mapping remain adapter concerns with shared behavioral contracts where necessary.
- A proposed protocol design uses common domain operations plus capability discovery for optional features such as reactions, edits, threads, and receipts. The user has not accepted this design yet.
- XMPP can already provide some history and multi-client behavior where supported. Application synchronization should account for protocol-owned state rather than assume it owns every contact, room, or historical message.

## Deployment possibilities discussed

| Possibility | Description | Status |
| --- | --- | --- |
| Local without sync | A local engine connects to chat providers and uses SQLite; app-owned state stays on that device. | Confirmed; expected app-state drift across devices. |
| Hosted engine | Multiple device UIs belonging to the alpha's single owner use a shared hosted engine and its application state. | Confirmed; hosted access authentication, offline cache, and availability details open. |
| Local plus sync | Device-local engines and stores exchange selected app data with an optional sync service. | Confirmed; sync scope, conflict policy, and service packaging open. |

## Backlog

- **Blocking and unblocking protocol identities.** Explicitly deferred by the user. Revisit provider capabilities and block-list ownership, per-account identity scope, linked-contact behavior, incoming effects, and pending/scheduled sends. Conversation mutes remain alpha scope and do not imply protocol blocking.
- **Activity-based notification routing across devices.** Explicitly deferred by the user. Alpha devices notify independently under their effective notification policies. Later consider suppressing or routing alerts based on activity on another device, with coordination, availability, and fallback behavior. This is separate from confirmed alpha deduplication within one browser profile and separately backlogged Web Push.
- **Automatic idle/away detection.** Explicitly deferred by the user. Alpha availability is manual per account and synchronized through optional app sync. Revisit inactivity detection, browser/native activity signals, multiple tabs and hosted sessions, multi-device aggregation, and interaction with manual DND later. Connection/disconnection reporting remains required independently.
- **Room moderation and role-management controls.** Explicitly deferred by the user: kicking participants, banning, and changing roles/affiliations. Alpha still supports the agreed room creation, joining/discovery, invitations, participant display, and permitted name/topic edits. Incoming permission changes, removal events, and operation failures still need truthful handling.
- **Voice and video calls.** Explicitly deferred by the user. Revisit capability-aware calling, signaling/media integration, permissions, call lifecycle, and platform behavior later. Audio-file attachment playback remains alpha scope; built-in voice-message recording is separately backlogged.
- **Built-in voice-message recording.** Explicitly deferred by the user. Alpha supports attaching audio files and inline playback. Revisit microphone capture, recording controls, encoding, interrupted recordings, and submission through the attachment/outbox pipeline later. This does not select voice/video calling or audio transcription, which are separate concerns.
- **Any-device pending-send control through full centralized sync.** Explicit future direction from the user. Remove the initial requirement to reach the original owning engine directly when editing, rescheduling, or cancelling pending/scheduled sends. Design centralized coordination and dispatch ownership so accepted mutations respect the pre-dispatch cutoff, including disconnected local engines. Central acceptance versus pending intent, availability during sync outages, and conflict handling need design. This is separate from ordinary UI clients already sharing one hosted engine.
- **In-app XMPP account registration.** Explicitly deferred by the user. Alpha onboarding connects existing XMPP accounts. Revisit capability-aware server registration workflows separately from Oie's own application authentication.
- **Copy rendered text without Markdown markers.** Explicitly deferred by the user. Alpha Copy preserves the original message text as `text/plain`; a future separate action can copy visible text with formatting stripped. Rich HTML clipboard output has not been requested.
- **Browser-hosted WASM engine.** Explicitly deferred by the user. Alpha engines run native Go locally or hosted. Preserve the shared engine API and provider/persistence boundaries for future browser-local routing, browser-compatible protocol clients, SQLite/OPFS integration, media storage, and event bridging. PWA installation/offline UI remain alpha scope and do not require this engine runtime.
- **Protocol plugin capability.** Explicitly deferred by the user. Alpha adapters are compiled-in Go packages. Future independently installable adapters will likely be viable only in hosted-server mode; revisit process/network boundaries, plugin API/SDK compatibility, lifecycle, deployment, and runtime support. No Go dynamic-plugin or external-process implementation is selected now.
- **Document text extraction.** Explicitly deferred by the user. Later index supported document bodies beyond filename/caption/message metadata; formats, processing lifecycle, and extracted-content retention remain to be designed.
- **Image OCR.** Explicitly deferred by the user. Later extract searchable text from images; alpha search uses available metadata and surrounding messages/captions.
- **Audio transcription.** Explicitly deferred by the user. Later index speech transcripts; alpha does not require transcription to search shared audio metadata.
- **Web Push and closed-app notifications.** Explicitly deferred by the user. Alpha notifications only need to work while the app is open. Future work includes push subscriptions, sender/event-source integration, service-worker delivery, notification deduplication and platform behavior. PWA installation and offline reading remain alpha features.
- **XMPP end-to-end encryption support.** Deferred by the user. Revisit compatible schemes/libraries, device identities, trust and verification, key/session persistence and recovery, encrypted history backfill, search/storage protection, and where encryption terminates in hosted mode. Existing architectural notes below preserve the reasoning; none of these implementation choices is selected. Keep encryption readiness explicit rather than claim that server capability discovery alone enables E2EE.
- **Multi-user hosted instances.** Deferred from the alpha by the user. One deployment should eventually serve multiple independent Oie users with isolated protocol accounts, contacts, history, schedules, and outbox. Revisit user authentication/enrollment, authorization across APIs/search/events/sync, job ownership, administrator capabilities, and storage isolation. Do not implement multi-user flows as an alpha requirement.
- **Built-in application authentication.** Explicitly deferred by the user until there is a functioning app. Revisit single-owner login, credential/session handling, and any future identity-provider integration then. An external authentication alternative has not been selected. Keep this separate from the credentials needed to connect XMPP accounts.

## Questions still open

These are remaining design details, not a mandatory interview checklist. As of the user's question about how many questions remain, there is enough agreed direction for a first specification. Use proposed defaults or explicit open points for unresolved items; ask another question only when a consequential decision truly blocks the next authorized step.

- What minimal exclusion/deletion metadata prevents resurrection, and how is scoped removal implemented for shared hosted storage? Message deletion covers all retained revisions and associated search/shared-content entries within scope. Also delete downloaded files is optional and off by default, preserving other retained-message references; physical cleanup and retained-unreferenced-file management need design. Delete on this device is the default; all-synced-Oie-copy deletion is an explicit alternative.
- What indexing, revision ordering, and deletion mechanics implement the accepted history/search policies? Retracted originals have Reveal original and an Include retracted search/library filter. Observed edited versions have View edit history; search uses the latest version by default with Include edit history for labeled historical matches. Encrypted-content handling remains backlogged with E2EE.
- How should in-flight uncertainty and permanent errors be presented? UTC scheduling with timezone conversion confined to presentation is confirmed; local date/time input and DST ambiguity UX remain presentation design details.
- How do users migrate or switch between local and hosted engines? How is ownership of queued sends transferred or explicitly left in place?
- What is the hosted engine's lifetime and availability policy? What is usable when its API cannot be reached?
- What works when the sync service is unavailable, when XMPP is unavailable, and when the device has no network?
- Which data is local, authoritative at a chat provider, authoritative at the app backend, or synchronized between peers/devices?
- What is the trust boundary for credentials and history? Is encrypted application synchronization needed? XMPP messaging E2EE is explicitly backlogged and should not block other requirements discussion.
- Should automatic relaying across protocols ever be supported? Manual forwarding across conversations/accounts is confirmed; it does not select automatic relaying. Identity linking is settled as manual and reversible initially.
- What exact metadata/schema implements accepted causal revision ordering, shared sync/backup merge policies, and per-field settings resolution? UUID-identified revisions with parent links, descendant precedence, UTC creation-time/UUID tie-breaking for concurrent heads, and derived current message pointers are confirmed. Missing provider ancestry, deletion precedence, per-field metadata, and read-position merge mechanics need design; manual conflict selection is not the default.
- Locale negotiation/persistence, pluralization, date/time/number formatting, and RTL readiness need implementation design. Device-language default with explicit override and English fallback are confirmed.
- What device-level media quota/eviction controls and preview-retention policy should be exposed? Downloaded full files default to retention until explicit deletion, with configurable per-device overrides.
- What remaining attachment types, upload staging/cancellation states, size limits, local storage, and media-sync behavior are required? Audio-file attachments and inline playback are confirmed; voice recording and voice/video calling are backlogged.
- What protocol-specific and cross-device handling implements room leave/rejoin and remaining basic room settings? Auto-rejoin on reconnect except explicit leave/revoked access is confirmed, as are discovery, joining by address, creation, invitations, and room details with permitted name/topic edits. Kicking, banning, and role management are backlogged. Leaving does not delete saved history.
- Which XMPP features and other capabilities are required for the first useful release?
- How are exact read visibility thresholds, manual unread reminders/clearing, offline read-state submission, and cross-device read merging implemented? Synced manual Mark as read/unread actions are confirmed; explicit Mark as read emits supported/enabled receipts. Receipts default on with a synced per-account disable setting. Automatic read marking requires visible messages in the active, focused conversation.
- What additive Margo semantic projection should adapters consume? The permitted alpha subset and existing Margo syntax/behavior are confirmed.
- Automatic idle/away detection is backlogged. Contact presence and synced manual per-account status controls are confirmed; protocol-specific presence aggregation and truthful connection reporting still need implementation design.
- What manual bookmark/favorite actions, link-preview generation/caching policy, and behavior after source-message deletion are needed? Automatic shared-link cataloging is the working interpretation of the user's bookmark request.
- Full threads remain unresolved. Manual forwarding across conversations/accounts and opt-in source attribution with a generic Forwarded label where supported are confirmed. Media forwarding retrieves the full file as needed and uploads through the destination account, with progress, clear errors, and no silent link fallback; transfer staging/retry/cancellation and label transport details remain open. Capability-aware editing and retraction of one's own sent messages are confirmed; correction failure UX, revision ordering/sync details, and search updates need design. Observed earlier versions are retained with View edit history. Retracted originals are preserved behind placeholders with Reveal original. Quoted replies, protocol-supported reactions, and link previews are confirmed.
- How do protocol-account setup, device enrollment, and backend migration work? Per-account contact management, direct chats by address without contact creation, and normal-list placement of incoming non-contact chats with a label are confirmed. Application authentication is deferred; multi-user hosting and identity blocking are backlogged.
- How should full centralized sync coordinate any-device pending-send changes and disconnected dispatchers? Initial synced visibility and owning-engine confirmation are accepted; this reachability restriction is explicitly temporary. Central acceptance semantics and whether dispatch can proceed without central contact remain open.
- What CLI/configuration selects local, hosted-engine, and sync-service roles; what migration and data-directory layout are required? SQLite across native roles and one executable are confirmed. Manual engine-data backup/restore with optional media merges into existing data using UUIDv7 entity/revision IDs; format, generator/library, credentials, consistency, and detailed merge mechanics require design. Recovered pending/scheduled sends require review and explicit resume; treatment of existing outbox entries and reconciliation/ownership mechanics remain open.
- What are the remaining UI interaction and accessibility details, including timeline scrolling/navigation? Responsive layouts, device-specific System(default)/Light/Dark theme modes, and desktop/mobile composer-keyboard defaults with per-device Enter-to-send configuration are confirmed. Native mobile packaging is not selected.
- Which numeric cache defaults, browser store/index, eviction algorithm, and offline rendering approach should the PWA use? Offline search covers cached content with explicit limited-coverage labeling. Recently viewed conversations are cached under a configurable per-device limit, with drafts/pending sends excluded from cache eviction. Device-specific Keep available offline protects cached messages within that limit; exact history coverage/quota-full UX remains open. Engine full-history retention and downloaded-file retention remain distinct policies.
- Remaining browser notification defaults and implementation details need design. Devices notify independently in alpha; activity-based cross-device routing is backlogged. Same-profile/origin tab/window deduplication and active-conversation suppression are confirmed, alongside conversation mute and account DND policies. Web Push is backlogged; alpha notifications only need to work while the app is open.

## Current topic and next conversational step

The user requested and steered the first specification. Draft 1 is now in `docs/oie-spec.md`, consolidating agreed alpha behavior, explicit recommendations/open decisions/backlog, WhatsApp-aligned UI defaults, inspected Goshtoso reuse/gaps, and the generic upstream-PR/pseudo-version workflow. Finish/review this document and report its link. Do not resume the interview or silently implement proposed defaults. Future work should use the spec as the consolidated baseline and this record for decision history; no application scaffold or upstream feature implementation has been started.

### Chat formatting feasibility

See `/home/gui/repos/araihu/docs/oie-formatting-feasibility.md` for the probe, exact inspected revision, public API findings, protocol mappings, and source links.

- Executed nine input cases through a temporary public-API Margo consumer at local commit `4ff5caa8a434d8e1e9f18797fb5cda407dd8779c`; inline primitive assertions passed. Intentional raw-HTML and unsafe-link cases were rejected; headings/lists/frontmatter revealed the need for a chat-specific profile. No library source changed and no live interoperability test was run.
- Reuse/compose the existing Margo primitives, preserving its current grammar as explicitly selected. The concrete gap is a public semantic spans/blocks projection and a supported-construct policy for chat; the user authorizes needed public API additions while preserving existing behavior. Do not import Margo internals, expose its parser implementation as the Oie contract, or rely on regex delimiter replacements/HTML scraping for protocol encoding.
- The inspected checkout is one commit past `v0.0.35`; no Oie dependency version has been selected. Verify the eventual tagged release once the required public surface exists.
- Formatting is protocol/client/content-format capability dependent, not uniformly server-negotiated. XMPP styling needs no new server feature; deprecated XHTML-IM is not the recommended starting point.
- The user selected current Margo delimiters/behavior after the single-asterisk difference was explained. The accepted subset is bold, italic, strikethrough, inline/fenced code, links, lists, and blockquotes. Semantic API signatures and provider mapping details remain implementation design work.
- Subsequent user direction: attempt Markdown presentation by default (disable option available) and copy original message text in text/plain, preserving Markdown markers. This supersedes the earlier proposal to avoid interpreting native plain text as Markdown by default; preserve native/source provenance and readable fallback while respecting the rendering preference. The alternative rendered/plain-visible-text copy action is explicitly backlogged.

### Presence

Accepted: display contact presence and expose manual per-account Available, Away, and Do not disturb when supported by the adapter.

Accepted manual-status scope: per protocol account, synchronized across devices through optional app sync. DND suppresses Oie browser notifications and sounds for the account while continuing reception and unread-count updates. Apply status changes when received; temporary disconnected divergence remains expected.

Proposed interpretation pending detailed specification:

- Preserve supported provider states and their effective mapping rather than promise universal presence features. Presence evidence/visibility may differ by provider and contact relationship.
- Connection/disconnection is separate from availability status; do not imply invisible-but-connected mode from a generic Offline control.
- Native window close keeps the engine running, while hosted engine sessions outlive individual UI sessions. Automatic availability changes based on UI inactivity and multi-device activity aggregation are backlogged.
- Apply the accepted account DND notification suppression policy and synced manual-account status. Protocol availability and notification eligibility remain separate implementation concerns; DND does not disconnect the account or stop history processing. Provider-specific presence aggregation remains to be designed.

### Typing indicators

Accepted: receive/display and send typing state when supported by the provider. Sending is enabled by default with a per-account disable toggle synchronized across devices through optional app sync. Preference sync does not persist or replay typing events; per-device/conversation overrides are not selected.

Proposed behavior:

- Expose typing through the provider-neutral application API and scoped ephemeral events. Clear/expire typing on inactivity, navigation, disconnect, or explicit stopped-state events as applicable.
- Throttle updates and avoid persisting/retrying old typing notifications in the durable message outbox, history sync, or history backfill.
- Respect the user's sending toggle independently of displaying other participants' typing information. Exact expiry/timing constants and mobile/desktop composition triggers are implementation details.

### Read receipts and internal read state

Accepted: alpha supports protocol read receipts when available, enables sending by default, and provides a per-protocol-account disable setting synchronized across devices through optional app sync. Internal read-position sync remains independent; disabling external receipts need not disable the owner's cross-device read state.

Accepted automatic read trigger: the incoming message must be visible in the active, focused conversation. Arrival, background tabs, and selecting a conversation without seeing its messages are insufficient. Manual Mark as read/unread actions synchronize; unread is a reminder without undoing receipts, while explicit Mark as read emits supported/enabled receipts under the account setting. Exact viewport thresholds and marker representation/clearing remain design work.

Proposed details pending specification:

- Preserve distinct provider-accepted, recipient-delivered, and read/displayed evidence. Do not infer read status from transport writes or optional missing receipts. Expose effective receipt capabilities through the engine API.
- Define when a message counts as read/displayed based on actual UI visibility/focus rather than merely fetching history or receiving events. Do not emit receipts for automatic backfill/catalog indexing alone.
- Receipt sending is enabled by default with a synced per-account toggle. Offline read-state submission and reconnect handling remain undecided; per-device/conversation receipt overrides are not selected.
- Quoted replies, reactions, receipt state, and the owner's local read position remain separate domain concepts with shared provider message references where appropriate.

### Alpha executable and persistence

Accepted: one Go executable supporting the selected local/native, hosted-engine, and sync-service deployments. Each native role uses SQLite; media bytes are separate files in a configured data directory. The application/provider/persistence boundaries remain independent of deployment.

Implementation details still open:

- Exact commands/flags/configuration for engine, BFF, and sync-service roles; whether a process combines roles or a role is deployed separately using the same executable.
- SQLite driver/version, schema/migrations, transaction boundaries, event/change-log retention, indexing, and database/file naming. Outgoing message+outbox persistence must retain the accepted atomicity contract.
- Media-store organization, verification metadata, temporary upload/download staging, and crash recovery across filesystem operations and DB metadata updates.
- Data-directory selection, backup/export/restore, upgrade behavior, and packaging. Do not infer a separate database server or a plugin process for alpha.

### Offline explicit Send and reconnect submission

Accepted: an explicit Send action while the PWA cannot reach its engine saves a device-local pending submission and automatically submits it on reconnect. Composing/saving an ordinary draft does not auto-send. Engine-side durable queue acceptance remains a distinct milestone.

Proposed implementation details:

- Assign a stable operation/idempotency identifier before the first network attempt and durably save the pending payload locally before showing a waiting state. Attachments need durable staging appropriate to the browser/runtime; exact browser stores remain undecided.
- Show Waiting for connection/Pending submission until engine acceptance. After the engine transaction commits, reconcile to the engine's authoritative queued/sending/sent status and retire the local submission record without losing its identity.
- Retry the same logical operation on reconnect. If a request may have reached the engine before its response was lost, reconcile/query by operation identifier rather than enqueue another logical message.
- Allow local edits/cancellation of an entirely unsubmitted item. After an ambiguous submission, do not claim an edit/cancel succeeded until the engine confirms the operation's status and applies the established pre-dispatch guard. Retrying an idempotent operation with conflicting payload needs explicit handling; no automatic mutation of an already-dispatched message.
- Reconnect submission requires the PWA/client runtime to execute. Web Push and a permanently running browser worker are not alpha assumptions. Pending submissions persist for the next eligible running/reconnected session.
- Future migration between local/hosted engines must preserve pending-operation ownership and avoid submitting the same send to two independent engines. This remains separate from ordinary history/media replication.

### Internationalization from the start

Accepted: externalize user-facing expressions from application code/templates; use Goshtoso's existing support and expand it where concrete reusable gaps exist. Keep Oie-specific wording/catalogs in the application. Ship English (`en`) and Brazilian Portuguese (`pt-BR`) in alpha.

Verified public Goshtoso surfaces inspected locally:

- `github.com/araihu/goshtoso/expressions` provides typed `expressions.Set`, configured `NewRenderer`, context overrides through `expressions.With`, and component-level `WithExpressions`.
- Expression defaults/overrides are scoped rather than process-global. Language-specific context must reach full-page renders, HTMX fragments, built-in HTTP handlers, and background/SSE-triggered rendering. Device-specific language on a shared engine must not change another device's expressions.
- `LoadFS` (including `embed.FS`), `LoadFile`, `ParseJSON`, and `ParseYAML` load expression catalogs. Loaders validate known fields, duplicate/null values, and supported placeholders; `expressions.JSONSchema()` exposes the schema. These catalogs cover component expressions, not arbitrary Oie product copy.
- Dynamic component messages can use Go callbacks for complete parameterized/pluralized phrases. The application owns locale-specific plural rules, word order, and number formatting. The file format's placeholder substitution alone is not a full pluralization engine.
- Goshtoso does not choose languages, translate caller-owned product content, implement locale negotiation, or supply RTL layout automatically. Missing expression values inherit defaults, ultimately English; changing render context affects subsequent renders rather than already-rendered DOM.

Proposed Oie design implications, not a selected translation library or schema:

- Put all app-authored visible copy into keyed catalogs: navigation, buttons, empty/error states, send/sync/backfill statuses, deletion labels, settings, notifications, tooltips, placeholders, and accessibility text.
- Define complete messages with parameters and plural forms rather than assemble translated sentences by concatenating fragments. Apply locale-aware formatting to times, dates/day separators, counts, sizes, and durations; keep locale and timezone distinct.
- Keep stable API/domain status/error codes and structured arguments. The presentation resolves user-facing messages; identifiers, sync operations, and protocol behavior must not depend on translated labels.
- Reuse one locale/catalog resolution policy across BFF pages, HTMX responses, rendered SSE updates, browser notifications, PWA/offline UI, and future native/optional WASM presentation adapters.
- Cache rendered fragments and resolved expressions with locale-sensitive identity. On a language change, rerender affected UI/refresh subscriptions as needed while preserving drafts, selected conversation, and scroll context.
- Keep translation catalogs complete for selected locales, validate keys/placeholders and fallback behavior, and include locale-switching/accessibility text in appropriate verification when implementation begins. No tests or library changes are required merely for this discussion.
- Confirmed initial locales: English (`en`) and Brazilian Portuguese (`pt-BR`). Accepted selection policy: follow device/browser language by default, an explicit user override on that device takes precedence, and English is the fallback when no supported language matches. Regional language matching and override persistence are implementation details; use the same resolved locale for BFF/HTMX/SSE/offline presentation.
- Any Goshtoso changes should extend its public reusable expression/localization surface; follow that repository's dedicated-worktree and generated-source rules when actual implementation work is authorized. No Goshtoso source was modified here.

Local references inspected:

- `/home/gui/repos/araihu/goshtoso/docs/EXPRESSIONS.md`
- `/home/gui/repos/araihu/goshtoso/docs/EXPRESSION_FILES.md`
- `/home/gui/repos/araihu/goshtoso/expressions/`

### Automatic shared-field conflict resolution

Accepted for ordinary synced settings and contact aliases: automatic deterministic resolution per field rather than interactive conflict prompts or replacing a whole profile. Preserve independent edits to different fields. Exact logical/revision ordering and deterministic tie-breaking remain implementation design work; do not assume device wall clocks agree or guarantee the real-world latest edit from an arbitrary timestamp. Message identity-based merging, scoped deletion markers, and read-position semantics are separate policies.

### Engine API transport and event recovery

Accepted alpha transport: HTTP request/response for commands/queries and structured SSE events for live updates. HTML pages/fragments remain the BFF's responsibility. The general application boundary remains transport-neutral, preserving future IPC/WASM/other adapters.

Proposed implementation behavior, not a wire specification:

- Use the API-first OpenAPI/vacuum/oapi-codegen workflow for HTTP operations and shared payload schemas. SSE streaming handlers/reconnect behavior need explicit implementation and contract tests alongside generated bindings.
- Define event envelopes/identities and recovery semantics for state changes: incoming messages, outgoing states, reactions, history-import progress, and synchronization status. Keep provider-native cursors separate from application event-stream cursors.
- Reconnect should recover committed state changes through event replay or an explicit state refresh. A saved event cursor is not proof that the client received every related UI fragment; define snapshot/subscription handoff and stale-cursor reset behavior to avoid gaps.
- Consider an engine change log committed with relevant state mutations for durable replay. This is conceptually separate from the provider-send outbox even if their implementation shares infrastructure. No event-log framework/schema has been selected.
- Typing and capability-aware presence are confirmed as alpha scope and need freshness/expiry semantics rather than stale event replay. Treat live ephemeral states separately from durable message/status recovery.
- Respect slow-client/backpressure limits; reconnect or refresh state rather than silently discard authoritative updates. The BFF may translate structured changes into fragments or invalidation/refetch operations; exact presentation streaming contract remains open.
- Optional device synchronization and SSE delivery are separate contracts: a live UI subscription alone does not replicate a local engine's database or resolve offline multi-device edits.

### Future protocol design references

User-selected likely candidates: Matrix, IRC, and Nostr. XMPP remains first. These are contract-design references rather than selected libraries, implemented adapters, or alpha feature commitments.

Verified distinctions from primary protocol documentation:

- Matrix exposes room/event APIs, incremental sync, and transaction identifiers for distinguishing send retries from new requests. Its transaction identifier behavior can support provider-side idempotency where used correctly.
- IRC uses network-scoped nicknames and channels. IRCv3 history support is a negotiated extension, including capability requirements, bounded requests, and server/bouncer-specific availability. Nickname/display name must not be treated as a universal stable person identifier.
- Nostr identifies event authors through public keys and signs content-addressed events. Clients exchange subscriptions/events with relays; a relay's acceptance of an event is a different milestone from a recipient receiving/reading it. One identity may use multiple relays.
- Nostr NIP-17 defines private messaging built from encrypted wrapped events and recipient relay information. Nostr scope beyond chat/private messages and any future encryption implementation are not decided. XMPP E2EE's backlog status does not imply Nostr can ignore required protocol cryptography when implemented later.

Proposed shared-contract implications, not a formal spec:

- Use opaque provider-scoped external identities/message references alongside stable Oie IDs. Preserve network/account/room context and provenance; do not require JID syntax, email-shaped usernames, a single server, or a password for every protocol.
- Allow typed provider-specific connection/credential configuration behind the account-management contract. The presentation may render adapter-declared configuration forms without owning protocol logic; exact schema/configuration UX is undecided.
- Keep the common message/conversation API with optional feature contracts for history, rooms, reactions, attachments, receipts, and other operations. Expose effective capabilities at account/conversation scope, including runtime restrictions when relevant.
- Leave pagination tokens, stream checkpoints, relay subscriptions, protocol ID mapping, and raw provider recovery details with their adapters. The engine coordinates common backfill/outbox policies and normalizes application events without assuming all protocols have identical ordering or replay guarantees.
- Distinguish durable engine acceptance, provider submission/acceptance evidence, recipient delivery, and human-read evidence. Preserve uncertainty instead of mapping every successful write/relay response into delivered status.
- Runtime portability is adapter-specific: native Go, hosted Go, and potential browser WASM can share contracts while having different available transports/dependencies. A future browser IRC adapter may need a browser-compatible gateway; no gateway/runtime plugin implementation is selected.
- Accepted initial packaging: compile supported adapters into the Go program and register factories/capabilities at startup. Adding an adapter initially requires a rebuild. Independently installable plugins are explicitly backlogged, with likely hosted-server-only scope. Process/network isolation and SDK compatibility remain future design work.

### Reactions, rich links, and shared content search

Accepted alpha requirements:

- Emoji reactions where supported by the protocol, through provider-neutral engine operations/events and effective capabilities.
- Rich link-preview cards following the supplied WhatsApp desktop reference.
- A searchable catalog of media, files, and shared links across all connected chat sources, both globally and scoped to a direct/group conversation.
- Shared links are to be “bookmarked”; working interpretation is automatic link extraction/cataloging from shared messages so retrieval does not depend on manually saving each link. Optional manual stars/favorites are not selected yet.
- Alpha search covers message text/captions, filenames, available media metadata, URLs, and page titles/descriptions. Document body extraction, OCR, and audio transcription are explicitly deferred to the backlog.

Verified references and proposed implementation details:

- Verified: XEP-0444 specifies reactions, discovery, updates/removal, and optional restrictions. Support can depend on participating clients/components as well as protocol features; it is not just a server-wide flag. The current XEP is Experimental; exact supported versions/library integration remain open.
- Proposed: retain the original target-message identity, sender identity, emoji set, and effective reaction state; handle live/backfilled/replayed updates idempotently. Receiving a reaction must not enqueue another outgoing reaction. Reaction restrictions exposed by an adapter should inform the picker.
- Verified: Open Graph defines title, image, canonical URL, description, and other page metadata suitable for cards. Proposed engine-owned preview resolver with a structured cache can use supplied preview metadata or bounded webpage metadata retrieval, returning a plain-link fallback when enrichment fails. Rendering remains Goshtoso/BFF-owned.
- Proposed card content: image/thumbnail when available, page title, description, source domain, and clickable original URL. Exact composer-preview/removal behavior, refresh policy, and fetched-image retention remain open.
- Propose a bounded, cached preview-enrichment queue separate from provider message-history fetching. Catalog URLs immediately; do not block messaging/history import on fetching every linked page. Timeouts, response limits, and safe network target/redirect handling belong to the resolver implementation.
- Build catalog entries from live messages, provider backfill, and optional Oie history sync. Preserve message/account/provider/conversation identity, sender, timestamp, and original URL/file metadata, enabling global results to navigate back to their source conversation/message.
- Proposed search categories: All, Media, Files, Links; filters can scope by account/provider or conversation. The required scope is global and per-chat/group; exact additional filters/sort UI remain design details.
- Link grouping may deduplicate URLs for display while preserving all source occurrences. Keep original URLs; do not strip arbitrary query parameters or assume one canonical URL erases source context. Handling edited/deleted source messages and independently saved bookmarks remains open.
- Accepted baseline search: indexed message text/captions, filenames, available media metadata, URLs, page titles, and descriptions. Searching document bodies, OCR text, and speech transcripts is explicitly backlogged.
- Search metadata can exist without downloading full attachment bytes. Opening/downloading media obeys device-specific preferences, storage outside SQLite, and established hash checks. Preview-image bytes remain subject to media/preview policies; global search does not force full-file downloads.
- Catalog/index visibility must honor the accepted device-local/all-synced deletion scopes and show partial history coverage during backfill. A locally deleted message must not leak back into that device's results via its derived catalog entry.

### Quoted replies

Accepted: reply action on a selected message, with referenced sender/excerpt rendered in the conversation and a stable reply reference in the application API.

Proposed details pending specification:

- Keep references scoped to the correct conversation, local account, and provider identity. Provider adapters translate reply references and fallbacks while BFF rendering remains provider-neutral.
- Reply composition is part of the device-specific offline draft, and the outgoing reply is persisted with its outbox intent. Pending content/reply selection follows the agreed edit/cancel dispatch cutoff.
- Quoted replies are not a request for separate threaded timelines. The presentation may offer navigation to the referenced message when available; retrieval, missing/deleted references, and quote-snapshot retention remain design details.
- Do not imply that reply availability is only a server feature; the selected provider/client extension and available metadata determine mapping and fallback. No particular XMPP reply library or extension version is selected yet.

### Shared metadata and device preferences

Accepted when optional app sync is configured:

| Shared through sync | Device-specific |
| --- | --- |
| Contact aliases and manual identity links | Media download and downloaded-file retention preferences |
| Pins and favorites | Theme |
| Conversation archive state | Notification sounds |
| Read positions | Browser notification permission |
| Per-account read-receipt sending preference | — |
| Per-account typing-status sending preference | — |
| Manual per-account Available/Away/Do Not Disturb choice | — |
| Conversation mutes | Optional per-device conversation mute overrides |
| — | Language override; follow device language by default |
| — | Drafts, preserved offline in the alpha |

Shared data may diverge without sync or temporarily while disconnected, as already agreed. Shared read positions do not by themselves select protocol read-receipt behavior. Manual read/unread actions synchronize, with unread serving as a reminder; exact merge/marker-clearing rules remain open. Conversation mutes synchronize with optional device overrides; drafts remain device-specific for the alpha. Device-scoped preferences must retain their scope even when multiple UIs share one hosted engine.

Proposed mute-resolution model: default each device override to inherit; an explicit local override chooses muted/unmuted for that conversation on that device. Resolve app notification eligibility from that effective policy and browser/OS permission. Exact mute durations and notification-focus behavior remain open.

### Optional history replication

Accepted: optional Oie sync transfers retained message records between local engines, including messages older than or absent from provider archives. Standalone use without sync remains valid and can retain divergent history.

Proposed consequences, not a selected sync algorithm:

- Replicate logical message records and stable provider/account/identity associations, preserving provenance, edits/status information where appropriate. Rebuild local search indexes from replicated content rather than transferring index files.
- Exchange missing records incrementally and idempotently. A record arriving through history replication must never enqueue another provider send; pending-send ownership remains with its accepting engine.
- The sync service needs to retain or otherwise make replicated records available independently of the provider. Service data format, retention configuration, privacy/encryption, transport, batching, and conflict handling remain design work.
- Accepted: binary media replication is a separate optional setting. When enabled, downloaded files can be retained by the sync service independently of provider retention; receiving devices still fetch according to their own download preferences. History replication alone does not enable media replication, and metadata/hashes alone cannot recover missing file bytes. Media-sync default and retention/quota details remain open.
- Accepted deletion scope choices: explicit local-only deletion or deletion across all synced Oie copies including sync-service storage. Track the chosen scope with exclusion/deletion markers so neither provider backfill nor reconnecting peers restore deliberately deleted content. Protocol-server deletion/retraction is separate; defaults and exact marker format remain open.
- Proposed local-only semantics: suppress the record on the selected device without deleting the sync service's or other devices' copies. On a hosted engine shared by multiple UIs, distinguish per-device suppression from deleting the shared engine record. All-synced deletion propagates when disconnected devices reconnect; do not claim immediate removal from unreachable devices.

### PWA installation and notifications

Accepted alpha features: installable PWA, browser notifications, per-conversation mute controls, offline reading of previously cached conversations, and preserved drafts when the engine cannot be reached.

Implementation proposals and verified platform requirements:

- Provide a web app manifest, app name/icons, start URL/scope, and standalone display mode for supporting browsers. Serve hosted deployments through HTTPS; local loopback development is an allowed installable context. Installation UX and feature availability vary by browser/platform.
- A service worker can cache versioned UI assets and provide an offline launch/fallback experience. Offline cached conversations and drafts are now accepted; exact history coverage, browser-side persistence, and cached presentation routing remain implementation design work. Installability/offline reading do not select WASM or require a protocol client in the presentation bundle.
- Request notification permission through a deliberate user action. Use a service-worker notification path where appropriate, including mobile platforms that do not support constructing ordinary page notifications.
- Proposed notification policy: honor the accepted shared conversation mute plus optional per-device override, deduplicate events across open tabs/windows where possible, and notify on new live incoming messages rather than historical backfill/replay. Permission and sounds are confirmed device-specific; preview visibility, mute durations, and focus suppression remain open.
- Web Push can wake a service worker to receive notifications while the page is not loaded, subject to platform behavior. It requires a separate push subscription/sender path and an available source of incoming-message events. This feature is now explicitly backlogged; alpha notifications only need to work while the app is open.
- Native embedded engine lifecycle remains unchanged: window close keeps it alive and explicit quit stops it. A browser/PWA runtime remains governed by browser lifecycle rules.
- Accepted offline semantics: cached history/drafts are device-local UI data. An explicit offline Send stages a durable local pending submission and automatically submits it when reconnected. A hosted engine's outbox accepts the send only after its transaction commits. Show Waiting for connection before that acceptance; preserving a draft alone does not imply queued/sent state or automatic submission.

### Desktop UI reference

Accepted reference: WhatsApp desktop screenshots supplied by the user. Preserve Oie's Goshtoso implementation and account/protocol identity requirements while using these as layout and interaction guidance.

Observed reference patterns to guide later design, not a blanket feature checklist:

- Three desktop regions: navigation rail/sidebar, searchable/filterable conversation list, and the active conversation workspace.
- Conversation rows include avatars, name, recent message preview, time, unread/mute/pin state, and a clear selected row. Filter chips appear above the list.
- A persistent conversation header, scrollable incoming/outgoing bubble timeline, and a bottom composer with attachment actions. Group messages show sender identity; timeline examples include dates, unread markers, quotes, reactions, images, file cards, and link previews.
- Dark layered surfaces, restrained separators, rounded message bubbles, and a patterned conversation background in the reference. Oie supports System(default), Light, and Dark theme modes per device; exact palette and assets remain design details.
- Settings appear as an overlay panel with search, profile, and grouped settings rows, retaining context behind it.
- Calls, updates/stories, communities, AI, voice-recording controls, and forwarding visible in screenshots are not themselves accepted scope. Reactions and link previews were explicitly selected in a later user message; quoted replies were also explicitly accepted.
- The reference is desktop; responsive browser/mobile navigation still needs design. Native window controls are not required in a plain-browser interface.
- Do not reproduce private names, phone numbers, or conversation text from the screenshots as application fixtures or demo content.

Reference files (already visible in the conversation):

1. Direct chat: `/home/gui/.codex/attachments/ea2a2f77-91fd-4706-9555-90ab52b272bd/codex-clipboard-ea502bf0-3c2f-4861-8812-6521c5450517.png`
2. Settings overlay: `/home/gui/.codex/attachments/86830d4c-a2a4-4769-8b9a-4a9587564362/codex-clipboard-35188aca-438e-4275-b220-7487302ba4ab.png`
3. Group chat with image: `/home/gui/.codex/attachments/683eaaf6-94af-4eae-af1f-b6fc7270000c/codex-clipboard-cd3bf2a1-3631-44e5-9f53-85a14736994b.png`
4. Group chat/link previews: `/home/gui/.codex/attachments/53e52853-b043-4678-925a-76e8976df050/codex-clipboard-f901869a-cbd8-4eb6-aea9-7359c326a7b6.png`

### Media sharing implications

- Accepted defaults: small previews automatically, full files on demand, with configurable device-scoped preferences. A preference change on one device must not silently change another device's policy, including when both UIs use a shared hosted engine. Exact storage of device preferences remains open.
- Accepted full-file retention: configurable per device, defaulting to retain until explicit deletion. Do not silently evict downloaded originals under the default policy. Preview retention and exact optional cache-quota controls remain open; clearing media bytes need not delete message history or its integrity metadata.
- Accepted media storage direction: SQLite stores hashes for verification, with binary previews/full files outside SQLite in a platform-appropriate media store/cache. The user said “only media hash”; this is interpreted as excluding media blobs, not omitting the message/attachment reference, algorithm, and availability information needed to retrieve and validate files.
- Verified: XEP-0447 stateless file sharing uses XEP-0446 file metadata, which can carry content hashes in the XEP-0300 format. These file-sharing/metadata extensions are currently Experimental; support and hash presence must be checked on actual messages/clients. HTTP File Upload support alone does not guarantee that the sender provides a digest.
- Proposed hash record: retain algorithm, expected digest and provenance when supplied, computed digest when downloaded, and verification outcome. A supplied expected hash and a computed-only fingerprint are different evidence. A digest is not by itself proof of sender authenticity.
- Proposed download behavior: hash incrementally while streaming the full file to a temporary media-store entry, compare a supported advertised digest if present, and finalize/cache only on successful completion and integrity checks. A mismatch must not be presented as a verified download; retry/failure handling remains to be specified.
- If no usable expected digest is supplied, compute a local SHA-256 fingerprint on complete download and retain it as a baseline for subsequent integrity checks/content lookup. SHA-256 is a proposed default, not a specifically selected requirement. Do not claim first-download verification against an original when no independent expected hash exists.
- A preview hash is separate from the original file hash. If no small preview is provided, do not automatically fetch a full large file merely to create a thumbnail unless the user's device download policy allows it.
- Verified: XMPP HTTP File Upload (XEP-0363) exposes upload service discovery and may advertise maximum file size. It returns upload/download URLs; the file bytes travel over HTTP. The provider adapter owns this workflow and maps its capabilities/errors into the uniform engine API.
- Verified: media-service retention can differ from archive retention, and files can disappear under quota/age policies. Keeping a message with an attachment URL does not guarantee that the file remains downloadable.
- Proposed: expose effective send-attachment availability and known limits for the selected account/conversation, plus upload/download progress and actionable failure states. Do not silently choose an unrelated external upload service if the provider lacks support.
- Proposed: persist outgoing file content or a durable engine-owned reference before reporting a scheduled/queued attachment send as durable. Avoid depending on a temporary browser file handle surviving engine restarts.
- Upload staging, upload retries, chat-message dispatch, and cancellation need distinct state boundaries. The already-agreed send cutoff remains in force; exact treatment of cancelling a staged/in-progress upload and cleaning abandoned uploads is not yet specified.
- Optional cache-quota controls, preview retention, physical media-store layout, and media-sync service retention remain open. Attachment replication is a separate optional setting, off by default with explicit enablement. Downloaded full-file retention is device-configurable with explicit-delete retention by default. Serial message-history backfill does not imply downloading every historical binary attachment under the accepted on-demand default.

### Hosted user isolation reference for backlog

Future backlog requirement: one hosted deployment can eventually serve multiple independent Oie users, each owning their protocol accounts, contacts, history, schedules, and outbox. The alpha is single-user; the following notes are future design context rather than current implementation requirements.

Proposed implementation implications, not selected storage/authentication mechanisms:

- Resolve an authenticated Oie user independently of the XMPP accounts that user connects. Authentication and account-connection credentials have separate lifecycles.
- Enforce ownership in application services and persistence access, including direct object-ID access, full-text search, pagination, event subscriptions, sync operations, attachments, and exports. Never rely solely on BFF filters or untrusted request-supplied owner IDs.
- Keep dispatcher, backfill, and scheduled jobs scoped to their owning user/account. Rate-limit shared upstream servers across users while preserving ownership and fairness.
- A user quitting a UI, disconnecting an account, or cancelling their work must not stop another user's hosted engine sessions/jobs. Operator shutdown is a separate lifecycle.
- Single database with explicit owner scope versus separate per-user databases is still open; do not infer a physical storage layout from logical isolation.
- OIDC/local credentials, session management, registration/invites, administrative access, and operator trust are still open. Multi-user isolation does not imply that the hosting operator cannot access stored plaintext.

### Unified contact model

Accepted: a person can be presented as one Oie contact containing multiple identities, while protocol/account conversations and their histories remain distinct. Users link/unlink manually and reversibly; the grouping operation preserves histories and send destinations. Automatic suggestions are deferred.

Proposed consequences, not yet individually confirmed:

- An Oie contact is an app-owned grouping with stable identity, aliases, and associations to provider identities. Grouping can remain local without optional app sync; cross-device propagation follows the sync policy still to be selected.
- Preserve provider identity and owning local account on each conversation. Opening a contact can expose its separate conversations with clear protocol/account labels.
- Accepted: linking/unlinking changes organization without merging histories or retargeting queued/scheduled sends. Provider identifiers remain associated with their original conversations.
- Accepted: explicit reversible link/unlink actions initially. Matching names or avatars must not silently merge identities. Suggestions are optional future scope.
- Cross-protocol relaying/bridging remains undecided. Multiple simultaneous XMPP accounts are now confirmed by a subsequent decision; contacts and conversations must retain their owning account scope.

### XMPP encryption responsibility reference for backlog

- Verified: OMEMO (XEP-0384) defines end-to-end encryption for one-to-one and supported group conversations with multiple devices per participant. Compatible participating clients perform encryption/decryption and maintain device keys and cryptographic sessions.
- Verified: OMEMO uses XMPP pubsub/Personal Eventing Protocol facilities to publish and discover device lists/public key bundles. The server provides these supporting services and routes/stores encrypted messages; server support alone does not make an arbitrary client an E2EE client.
- Verified: TLS is transport/channel protection in core XMPP and does not by itself protect message content from the XMPP server processing it. E2EE protects message content between encryption endpoints, while metadata may remain visible.
- Capability design implication: distinguish engine encryption implementation, provider supporting facilities, peer-device compatibility, and per-conversation readiness/trust. Avoid a single server-level E2EE boolean that promises encryption everywhere.
- Oie architectural inference: if encryption/decryption and keys live in its local engine, that local engine is the encryption endpoint. If they live in its hosted engine, the hosted engine is an encryption endpoint with access to decrypted content. XMPP E2EE can protect against XMPP infrastructure while still requiring trust in the Oie engine host.
- Protecting plaintext/keys from the hosted Oie engine would require a separate device/browser-side crypto boundary and implications for BFF rendering/search. This is a different trust-model choice, not an automatic consequence of enabling OMEMO in a server-side adapter.
- Full encrypted archive backfill is conditional on possession of appropriate decryption/session material. Retrieving ciphertext or discovering MAM support does not guarantee a newly enrolled device can decrypt historical messages.
- XMPP E2EE implementation is backlogged. No encryption implementation, OMEMO version/library, mandatory encryption policy, or encrypted local storage scheme has been selected.

### Scheduled sends

Accepted: users can schedule messages, alongside ordinary immediate sends, and missed schedules send automatically at the next available opportunity unless cancelled. Pending content and schedule can be changed before dispatch; after dispatch begins, reject changes and show actual status. The following mechanics are proposals pending detailed specification:

- Accepted time policy: UTC under the hood; timezone conversion belongs to presentation. A device timezone change does not alter a stored send instant. Presentation converts local input into an instant and formats it locally; the engine does not require an originating timezone to execute it. Proposed mechanics: reuse the transactional outbox with an eligibility/not-before timestamp. Recurrence and DST input ambiguity UX are not yet specified.
- Proposed lifecycle: Scheduled -> Queued when due -> Sending -> protocol-specific acknowledgement/delivery states. Accepted: scheduled and other pending pre-dispatch items can be cancelled, edited, or rescheduled using the same atomic state guard. If dispatch wins the race, reject the mutation and return current status; an uncertain previously transmitted attempt is not a safely editable unsent item.
- A schedule belongs to the engine that durably accepted it. Local schedules require that local engine to be running and connected; hosted schedules can execute without open UI clients while their engine is available. Browser WASM schedules inherit browser lifetime restrictions.
- Syncing a scheduled item for visibility must not create multiple executing outbox entries on separate engines. Engine migration or takeover requires explicit ownership transfer, still to be designed.
- Select eligible work by not-before/retry time; a future scheduled message must not block ordinary immediate messages in the same conversation. Ordering policy applies to eligible sends, not blindly to creation order across all scheduled intents.
- Accepted overdue policy: dispatch on next availability after sleep, quit, or network outage, unless cancelled. Proposed UI: show the scheduled time and a waiting/overdue state until dispatch. No automatic expiry or manual overdue confirmation has been requested.

### Retention and outgoing delivery

Accepted: keep stored history independently of server expiry until explicit user deletion; offer explicit device-local and all-synced-Oie-copy deletion choices and prevent resurrection through backfill/sync. Preserve retracted originals behind placeholders with Reveal original; exclude them from search and Media/Files/Links by default with an Include retracted filter. Minimal exclusion/deletion marker format, FTS cleanup, associated attachment deletion, protocol retraction mapping, and secure storage remain to be designed.

Accepted: transactional outbox and cancellation UI. Proposed mechanics and guarantee boundaries:

- Atomically insert the outgoing message and outbox intent in the engine-owned database. A successful Send response means durable acceptance by that engine; UI-only optimistic state must not imply durable queueing when its engine cannot be reached.
- An embedded dispatcher processes committed intents independently of the UI, using persistent retry state and respecting per-conversation ordering. Explicit native quit stops dispatch; restart resumes pending work. No separate OS daemon or message broker is required merely by the outbox pattern.
- Use a client operation/idempotency identifier so retries of the frontend Send request do not enqueue duplicate logical sends. Preserve provider-facing message identity across retries. These do not automatically force a provider or recipient to deduplicate.
- The intended reliability contract is no silently lost committed send intent and eventual submission under recoverable failures when the engine runs and the provider becomes available, unless cancelled. Permanent errors need visible actionable failure; an indefinitely unavailable provider, invalid authorization, or unavailable engine prevents an unconditional delivery guarantee.
- The transactional outbox relay can crash after provider acceptance but before recording success. Retry may therefore duplicate external delivery. Use adapter-supported acknowledgement/resumption/reconciliation before resending ambiguous attempts; do not promise generic exactly-once behavior.
- XMPP Stream Management (XEP-0198) offers peer stream acknowledgements and resumption. Those are distinct from recipient-client delivery receipts (XEP-0184) and human read status. Missing optional receipts alone should not cause repeated sends.
- Pending-send ownership stays with the engine that accepted it. Optional app-data/history sync must not create a second dispatcher for the same outgoing intent. Handoff between engines remains a separate design task.

Cancellation policy: pre-dispatch cancellation and truthful in-flight status are accepted. Detailed handling of uncertain past attempts remains a proposal.

The same accepted dispatch cutoff applies to pending content edits and rescheduling. Proposed API detail: use a revision/concurrency precondition and return a conflict with current state when stale modifications race dispatch or another modification. The API status code and wire shape are not yet specified.

| State | Behavior |
| --- | --- |
| Scheduled, queued, or known-unsent retry | Atomically cancel before dispatcher handoff; no future send attempt. |
| Dispatch starting or transmission in flight | Serialize cancellation against a defined dispatch barrier. If dispatch won, do not promise recall or show a false cancelled result. |
| Acceptance unknown after connection loss | Reconcile using provider capabilities. Any stop-retry action must preserve possible-delivery uncertainty. |
| Accepted by provider or delivered | Send cancellation no longer applies. Protocol-specific retraction would be a separate capability and action. |
| Cancelled before dispatch | Terminal; restarting or reconnecting must not revive the send. An explicit new send creates a new operation. |

No artificial undo delay: ordinary sends become eligible immediately. Deliberately scheduled messages remain ineligible until their selected time.

### Serial background backfill

Accepted requirement: retrieve all available, authorized history automatically, serially and without overwhelming the provider. This does not imply access to every room on a server or recovery beyond archive retention/permissions.

Proposed implementation behavior, not yet individually confirmed:

- Use a bounded history-work queue rather than launch a worker per room. Define the serialization/rate-limit scope explicitly; an initial option is one in-flight history page per account with shared limits for accounts targeting the same server, especially in hosted mode.
- Pace requests as well as limiting concurrency. Respect provider result limits, slow down on throttling/transient errors with bounded exponential backoff and jitter, and avoid tight retry loops. Exact values remain open.
- Prioritize current conversations and recent history so the app is useful immediately, then progress through remaining archives fairly. Live messaging and foreground operations take priority over background history retrieval.
- Persist messages and archive progress together after each successful page. Resume after restart or reconnect and avoid refetching already-covered ranges. Retain contiguous coverage and deduplication rules established above.
- Expose backfill progress, pause/error states, and partial search coverage. Do not present unavailable or inaccessible archives as a completed full import. Avoid percentage claims when the provider does not supply a reliable total.
- Retention is now until explicit deletion. Deletion exclusion metadata must prevent automatic backfill from restoring intentionally removed messages.

### Capability discovery and engine history

Verified protocol facts and implementation proposals are distinguished below.

- Verified: XEP-0030 `disco#info` exposes identities and feature namespaces. Discover archive support on the account/archive JID and room JIDs; a server-wide flag alone is insufficient. Discovery failure or denied access should not be silently treated as permanent lack of support.
- Verified: Message Archive Management (XEP-0313) advertises `urn:xmpp:mam:2` with additional feature namespaces for extended functionality. Capabilities, authorization, archiving policy, and retention determine what can actually be recovered.
- Verified: MAM uses Result Set Management (XEP-0059) for bounded pages, before/after archive IDs, and completion signals. It supports querying history for synchronization rather than downloading an entire archive on every open. Extended query features must only be used when supported.
- Proposal: the engine exposes protocol-neutral, scoped effective capabilities such as remote history retrieval and pagination, alongside engine features such as indexed history search. The BFF uses those application capabilities, not raw XMPP feature names. A conversation can have searchable stored history even when its provider has no remote archive.
- Proposal supporting accepted full backfill: serve conversation timelines from the engine database immediately; catch up newer missing messages after reconnect; automatically fetch older missing ranges through the serial backfill queue, with possible foreground reprioritization; persist and index arrivals from both live delivery and archive queries.
- Proposal: track opaque archive cursors and fetched history ranges per archive/query scope, separately from read positions. Do not advance a contiguous catch-up checkpoint solely because a newer live message arrived while an older gap remains. Commit messages and their coverage/checkpoint together.
- Proposal: deduplicate live/history/sync copies using protocol identity rules, including archive authority and account scope. Archive UIDs are not globally unique and must not be sorted or compared as numeric sequence numbers. Retain links between the same logical message and its different provider/archive identifiers as needed.
- Verified: missing archive cursors can yield item-not-found errors; provider retention may make some messages unrecoverable. Proposal: fall back to bounded overlapping queries where possible, deduplicate, and surface remaining gaps rather than silently claim a complete archive.
- Verified: SQLite FTS5 provides full-text indexing. Proposal: keep the message search index as derived engine data and rebuild it from stored message content rather than replicate the index itself. In hosted mode it lives with the hosted engine; in local mode it lives on the device.
- Search can only cover retrieved/indexed content. Proposal: show history coverage/backfill status and distinguish an incomplete index from no matching messages. Encrypted content needs decryption access before ordinary text indexing; protection of stored content/indexes remains to be decided.
- Local history benefits remain useful whether or not optional app sync is configured. Optional history replication is confirmed: a new device can receive retained message records from other Oie stores through sync, including messages beyond provider retention. Both device-local and all-synced-Oie-copy deletion are confirmed. File-byte replication is confirmed as a separate optional media-sync setting that preserves device download preferences.

### Implications of supporting both modes

These are architectural implications and proposals, not a complete agreed sync design.

- Separate engine execution location from sync configuration. Local-without-sync, local-with-sync, and hosted-engine are distinct user-visible configurations. A sync service can be packaged with the hosted engine but need not run protocol clients or hold their credentials merely to synchronize app data.
- Distinguish device-specific settings, shareable app settings, protocol-authoritative data, and locally retained history. Device-specific differences may be intentional. Protocol-native synchronization can work even without the application's own sync service, depending on capabilities and policy.
- Hosted clients share authoritative hosted app state. Local engines each own their local store and may converge through optional sync. A disconnected local engine should not require sync service availability merely to talk to an available chat provider.
- Define stable account/entity/device identities, operation identifiers, and protocol-qualified message identities before sync. Separate retriable replication of app data from sending messages to a provider; syncing a historical message must not cause another send.
- If sync is enabled, design per-field conflict rules, deletion propagation, and incremental retry/cursor behavior. Use version/concurrency information rather than relying only on device wall clocks. No CRDT or whole-database replication approach has been selected.
- Drift without configured sync is expected. Temporary divergence while configured sync is offline is expected too, but reconnection should follow explicit convergence policies. Expose sync disabled, pending/offline, last success, and actionable conflicts where relevant.
- Hosted-engine unavailability and sync-service unavailability have different effects. Recommend explicit engine switching rather than automatic local takeover, because hidden fallback can duplicate connections and sends. This recommendation is not accepted yet.
- Hosted engine hosts protocol sessions and requires corresponding credential/key access appropriate to the chosen authentication/encryption model. Sync-only hosting has a different trust boundary, still to be decided.
- Native local quit stops its embedded engine; closing a hosted UI must not automatically shut down the shared remote engine. Browser WASM lifetime restrictions remain separate.
- Verify common API behavior across embedded and hosted adapters, plus multi-device cases for offline divergence, concurrent edits, repeated operations, deletions, and reconnects when sync is implemented.

### Background engine tradeoffs under discussion

- Possible benefits while the host is awake and connected: notifications with no chat window open (given a platform notification integration), current local history/search indexes, continued outgoing retries and sync work, and persistent sessions for protocols with weak offline history support.
- A separate daemon additionally provides a lifecycle independent of UI restarts and can be shared by several frontends. These are benefits beyond merely keeping an embedded engine running with its window hidden.
- XMPP Message Archive Management can provide history catch-up when supported, enabled, and within retention limits. Continuous local execution is therefore not always needed to recover message history. Even a daemon still needs reconnection and catch-up after sleep or loss of connectivity.
- Costs include background network and power use, process supervision/startup, local IPC security if split into processes, and explicit presence/notification/quit policies.
- Hosted deployments can keep their engine running independently of browser tabs. A local daemon stops doing useful network work when its computer sleeps or loses connectivity; it does not by itself provide cross-device sync or notifications on a different device.
- Accepted outcome: initially embed the engine, keep it running after native window close, stop on explicit quit, and retain a future separate-daemon option.

### Browser WASM feasibility reference for backlog

- Browser WASM engine implementation is now explicitly backlogged. The following feasibility findings are future reference; alpha engines are native Go and the PWA uses the established API/BFF.
- SQLite's official WASM distribution supports persistent databases in the Origin Private File System (OPFS). Browser storage is scoped to the site origin and browser profile, rather than an arbitrary user-visible filesystem path.
- Browser storage survives ordinary reloads and restarts, subject to quotas and browser storage policy. Explicit site-data clearing removes it. Requesting persistent storage can reduce automatic eviction when granted; backups/export and sync still matter. Private browsing may limit or disable persistence.
- SQLite OPFS implementations use workers and have VFS-specific concurrency and deployment requirements. Multi-tab ownership/locking needs deliberate handling; not every OPFS VFS requires cross-origin-isolation headers.
- A Go WASM engine needs browser-compatible storage integration, such as a bridge to SQLite WASM/OPFS or another compatible driver/VFS. An existing native Go SQLite adapter is not automatically portable to the browser.
- A browser runtime does not provide the agreed native background lifetime. Service workers are event driven and may be terminated; they cannot be treated as an always-running daemon. Push notifications are a separate integration.
- Browser chat adapters must use browser-supported networking. XMPP over WebSocket is standardized by RFC 7395; browser WASM does not grant arbitrary TCP or Unix-socket access.
- A browser-local engine is a third runtime arrangement beyond a browser UI talking to a local native Go process or a hosted Go process. Preserve application contracts while supplying platform-specific adapters if this runtime is pursued.
- Browser-local routing can connect Goshtoso interactions to a WASM runtime. Presentation rendering can be a separate adapter even if packaged in the same WASM/native program. A service worker can intercept fetch requests and synthesize responses, but this alone does not provide an ordinary WebSocket listener; streaming needs an explicit browser-local bridge/adapter.

## References already consulted

- Local Goshtoso integration skill: `/home/gui/.codex/skills/using-goshtoso/SKILL.md`.
- Local Goshtoso README: `/home/gui/repos/araihu/goshtoso/README.md`. Goshtoso provides Go/templ rendering with HTMX and Alpine.js interactions.
- XMPP Message Archive Management: https://xmpp.org/extensions/xep-0313.html
- XMPP Message Carbons: https://xmpp.org/extensions/xep-0280.html
- Go network listeners, including Unix sockets: https://pkg.go.dev/net#Listen
- SQLite WASM persistent storage: https://sqlite.org/wasm/doc/trunk/persistence.md
- Browser OPFS: https://developer.mozilla.org/en-US/docs/Web/API/File_System_API/Origin_private_file_system
- Persistent storage requests: https://developer.mozilla.org/en-US/docs/Web/API/StorageManager/persist
- Browser service worker lifecycle: https://web.dev/learn/pwa/service-workers
- XMPP over WebSocket: https://www.rfc-editor.org/rfc/rfc7395.html
- Go WebAssembly and JavaScript interop: https://go.dev/wiki/WebAssembly
- oapi-codegen: https://github.com/oapi-codegen/oapi-codegen
- vacuum: https://quobix.com/vacuum/
- Service worker request routing: https://developer.mozilla.org/en-US/docs/Web/API/ServiceWorkerGlobalScope/fetch_event
- XMPP service discovery: https://xmpp.org/extensions/xep-0030.html
- XMPP result set management: https://xmpp.org/extensions/xep-0059.html
- SQLite FTS5: https://sqlite.org/fts5.html
- Transactional outbox pattern: https://microservices.io/patterns/data/transactional-outbox.html
- XMPP Stream Management: https://xmpp.org/extensions/xep-0198.html
- XMPP delivery receipts: https://xmpp.org/extensions/xep-0184.html
- XMPP stable stanza identities: https://xmpp.org/extensions/xep-0359.html
- OMEMO encryption: https://xmpp.org/extensions/xep-0384.html
- XMPP Personal Eventing Protocol: https://xmpp.org/extensions/xep-0163.html
- XMPP overview and channel security: https://xmpp.org/about/technology-overview/
- XMPP HTTP File Upload and discovery: https://xmpp.org/extensions/xep-0363.html
- XMPP stateless file sharing: https://xmpp.org/extensions/xep-0447.html
- XMPP file metadata and optional hashes: https://xmpp.org/extensions/xep-0446.html
- XMPP hash algorithm/digest format: https://xmpp.org/extensions/xep-0300.html
- PWA installability: https://developer.mozilla.org/en-US/docs/Web/Progressive_web_apps/Guides/Making_PWAs_installable
- Browser Notifications API: https://developer.mozilla.org/en-US/docs/Web/API/Notifications_API
- Browser Push API: https://developer.mozilla.org/en-US/docs/Web/API/Push_API
- XMPP reactions: https://xmpp.org/extensions/xep-0444.html
- Open Graph page metadata: https://ogp.me/
- Matrix client-server API and transaction identifiers: https://spec.matrix.org/latest/client-server-api/
- IRC client protocol: https://modern.ircdocs.horse/
- IRCv3 history extension: https://ircv3.net/specs/extensions/chathistory
- Nostr event/relay protocol NIP-01: https://github.com/nostr-protocol/nips/blob/master/01.md
- Nostr private messages NIP-17: https://github.com/nostr-protocol/nips/blob/master/17.md
- SSE streaming semantics: https://developer.mozilla.org/en-US/docs/Web/API/Server-sent_events/Using_server-sent_events

The repository name `araihu/oie` is confirmed. No repository, application scaffold, implementation, or formal spec has been created by this discussion.
