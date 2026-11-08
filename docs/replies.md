# Received reply references

API 0.7.0 adds optional `Message.reply` and `GET /api/v1/conversations/{conversationId}/messages/{messageId}/context`. The context response contains the target and a bounded window in newest-first order, with `hasOlder` and `hasNewer`. Its ordering uses original UTC milliseconds and UUID ties, matching retained message pagination. It reads local history and does not request protocol history or send read receipts.

`reply.bodyText` removes only valid protocol-marked fallback spans. `originalText` and content revision identities remain unchanged. `reply.excerpt` and `senderName` are plain text bounded to 512 and 300 Unicode code points. `available` is true only for a single validated retained target in the same account and conversation. A missing or ambiguous original retains the fallback excerpt without a target UUID. Ordinary Markdown quotes do not become protocol references.

The XMPP adapter implements received [XEP-0461](https://xmpp.org/extensions/xep-0461.html) metadata, [XEP-0428](https://xmpp.org/extensions/xep-0428.html) fallback ranges and [XEP-0426](https://xmpp.org/extensions/xep-0426.html) Unicode offsets. Direct source IDs prefer an origin ID, otherwise the message ID, scoped by sender. Room sources require a stanza ID issued by that room and stable-ID discovery, following [XEP-0359](https://xmpp.org/extensions/xep-0359.html). Duplicate IDs with conflicting content are unresolved. Reply text or timestamps never identify an original.

Migration 007 adds append-only observed reference and fallback metadata. It resets existing archive checkpoints once so the existing paced serial MAM reader can enrich retained messages through archive aliases. It preserves accounts, conversations, messages, content revisions, stars, outbox and engine identity. Replayed metadata produces a message invalidation without a content revision or unread increment. Down migration removes these derived indexes only. Browser fallback/copy behavior does not depend on modifying stored source text.

The browser keeps the target window pinned during live refresh. It preserves drafts and provides a return-to-latest action when newer messages exist. Pointer, keyboard and touch navigation use the same conversation-scoped URL. Available excerpts use public Goshtoso ChatBubble, Card and Link components. There is no new upstream API requirement.

This increment originally added received reply rendering. The subsequent [outgoing replies increment](outgoing-replies.md) adds reply composition and basic saved-public-room sending. Reactions and forwarding remain outside these increments.

## Verification on 2026-11-08

Native `make check` covers generated contracts, sqlc drift, vacuum, golangci-lint, Go formatting/vet and tests. Changed engine packages pass race tests. Development Prosody integration verifies live direct replies to Oie's outgoing IDs and archive reply recovery, including Unicode fallback removal and retained target resolution. The generated CLI exercises the bounded context operation.

`tests/browser/replies.cjs` checks scoped navigation, late resolution, exact original copy, pinned context updates, drafts/focus/scroll, image-preview coexistence and mobile pt-BR accessibility. Existing preview, Markdown, star, avatar and live-chat suites pass. `make test-live-browser` verifies the real engine and development XMPP without touching the user's engine data. The Dagger browser stage includes the reply fixture.

A backup preceded the live schema upgrade. Verification retained all prior account, conversation, message, message-revision and star IDs, exact source bodies, outbox and engine identity. A read-only browser observation of a recovered real-server reply confirmed its card navigates to the original with focus/highlight. This observation did not send protocol messages or browser mutations. Private source content is not saved as a public test fixture.

Full Dagger CI remains unverified on this host. The bounded `make ci` attempt timed out after 180 seconds with exit 124 before returning stage results. The successful native checks above are not a claim that the full Dagger pipeline completed.
