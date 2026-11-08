# Received message corrections

The duplicated bubble reported in the live preview was an original message followed by an XMPP correction. A bounded read of the two archive records confirmed that the second stanza's XEP-0308 `replace` referred to the first stanza's client ID. Their room-assigned stanza IDs differed, so treating them as repeated delivery of one stanza would have been incorrect.

API 0.12 adds optional `Message.edited` and `editedAt`. A validated correction updates the displayed content revision of the original logical message. Its canonical UUID and original creation time remain stable. `originalText` is the exact selected source body, including Markdown and compatibility quotes. A transport-state revision does not imply a content edit.

Correction matching uses explicit sender message identifiers. XEP-0461 room reply identifiers have a different authority and are not substituted for XEP-0308 identifiers. The account, conversation and sender proof must match. For archived semi-anonymous rooms, Oie requires a single valid XEP-0421 occupant identifier from a room that advertises that extension, together with the full occupant address. Nicknames, matching text and nearby timestamps do not prove identity. Missing, unverified or ambiguous evidence leaves separate source messages.

Source records and observed revisions remain retained. A derived canonical projection combines validated corrections for the timeline and related reads. Existing imported archive records are replayed through their aliases to recover previously discarded correction metadata. This does not create outgoing messages. The UI labels the selected corrected content in English and Brazilian Portuguese.

This increment receives and presents corrections. Sending edits and browsing edit history remain separate work.

## Verification on 2026-11-08

`make check` passes, including vacuum, generated-code drift, golangci-lint with zero findings, vet, native tests, CLI tests and Compose validation. SQLite/application/domain race tests cover immutable sources, canonical events, late originals, replay, ambiguous IDs, hash collisions, cycles, source authorship and frozen outgoing reply plans. Catalog/star regressions pass 25 repetitions under race detection. HTTP tests verify repair of two previously imported records, stable logical identity, current content and scoped alias proofs.

Actual development XMPP tests verify direct corrections and room corrections after reconnect, including advertised occupant identity and the distinction between client correction IDs and room reply IDs. The real-engine browser workflow passes. Browser correction/reply fixtures cover one canonical bubble, the edited label, exact current copying, old URLs, drafts, pending-send identity, stars, scroll/focus and pt-BR accessibility. The correction fixture is included in Dagger CI.

A read-only query-plan check on the existing history found that canonical reference hydration scanned all retained references. The indexed query reduced ten lookups from about 33 seconds to 26 milliseconds; a query-plan regression guards the indexed source and authority checks. After deployment, the preview returned HTTP 200 in 27 milliseconds during archive recovery.

A private online backup preceded migration 10. Verification retained all 5,743 existing source-message UUIDs and exact body hashes, prior content/star revision IDs, outbox IDs, account/conversation IDs, engine identity and archive checkpoint scopes. The migration clears checkpoint positions once to recover previously discarded metadata through paced archive replay. Derived catalog entries follow the selected revision; source bodies remain retained.

A read-only observation of the reported conversation confirmed one edited bubble, no secondary correction bubble, and successful navigation from the old correction URL. The observation attempted zero mutations and reported zero browser errors. The remaining authorized archive replay continues in the background.

Full Dagger completion remains unverified. The first snapshot was stopped after the query optimization superseded it. The final snapshot did not progress beyond connecting to the runner and was stopped after 196 seconds. The shared runner/tunnel was not reset. Native and integration results above are independent of that unavailable full-pipeline result.

Protocol references: [XEP-0308](https://xmpp.org/extensions/xep-0308.html), [XEP-0421](https://xmpp.org/extensions/xep-0421.html).
