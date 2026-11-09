# Message-scoped read state

The unread counter previously increased on incoming live messages but had no read operation. Opening or viewing a conversation could not reduce it.

Engine API 0.17 adds GET and POST `/api/v1/conversations/{conversationId}/read-state`. POST accepts at most 100 exact message UUIDs with an `Idempotency-Key`. The engine merges those identities transactionally. Own and historical messages are ignored; other conversations are rejected as a whole. A later message remains unread even if its timestamp precedes an observed message. Reading never changes message content, the latest-message preview, activity ordering or outbox work.

Migration 11 adds independent read revisions, a current conversation read head and per-message read evidence. It preserves existing accounts, messages, unread counters, media and pending sends. The first read batch computes the retained unread count using incoming, non-historical logical messages and recorded read evidence. Verified correction aliases resolve to their canonical message for both reads and counting. Read batches and receipts survive engine restart. Repeating an acknowledged batch emits no extra event; retrying a previously committed operation returns its original receipt snapshot.

The browser preflights the read endpoint and records messages only in the active, focused, visible conversation. The current defaults are 500 ms and 60% of the available message height. A tall message uses the timeline viewport as the height limit. Open modal overlays suspend observation. Each request preserves its operation key across uncertain retries and is bound to the current engine, account and conversation. Fragment disposal aborts observation and outstanding browser requests. Unsupported older engines remain readable without automatic read mutations.

The BFF proxies the existing read-state API with browser CSRF protection and engine/account binding. It supplies UUIDv7 operation keys, so device clocks do not order read evidence. It never treats an ordinary GET, history fetch or indexing operation as a read. On success, the browser requests current engine state instead of applying an old receipt count over a newer arrival. Conversation events update list badges and account totals across hosted clients.

This implementation records app-owned read evidence. It does not emit XMPP read receipts, synchronize independent engines, implement manual unread reminders or promise persistent offline read commands. Those capabilities retain their separate specification and backlog status.

Verification includes concurrent receive/read batches, partial visibility, offscreen messages, focus and visibility guards, modal overlays, lost acknowledgements with stable keys, later arrivals, atomic rollback, account/conversation isolation, restart persistence and preservation of message/outbox state. Browser fixtures use an isolated local API and BFF. They do not connect to the real account or change its unread counters.

## Verification on 2026-11-08

`make check` passes, including OpenAPI validation, golangci-lint, generated-file checks, vet and native application/CLI tests. The full host `go test -race -timeout 90s ./...` passes. The real SQLite engine/API/BFF integration and browser read-state fixture pass, including conversation badges and account unread totals. Existing fragment-navigation and live-chat browser fixtures pass.

The first bounded local Dagger run completed containerized `make check`, then stopped progressing during the full race-test stage after the avatarfiles result. It was terminated at the four-minute limit, with exit status 143. A subsequent integration run, `timeout 180 env OIE_CI_SSH_HOST=dagger-vm make ci-check`, passed the complete container check, race tests and builds in approximately 109 seconds.

The feature was merged with development commit `0d6f586` in `c3f7390`, then fast-forwarded into the primary development branch, `codex/message-previews`. Uncommitted pagination and other local edits were preserved. The merged committed version passed read-state, fragment-navigation and gallery-action browser fixtures. On the primary working tree, `make build`, `make check` and the read-state browser fixture passed. The read-state fixture now provides canonical message lookup and cursor-based older/newer pages for the in-progress timeline controller. Additional navigation and timeline-scroll runs timed out at conversation settling and prepend anchoring respectively; those working-tree interactions remain unverified.

The primary preview engine was restarted after a consistent SQLite backup. It reports API 0.17, retains its engine identity and uses migration 11 without a dirty migration. All 5,763 messages and both existing outbox identities were retained at upgrade. The connected account reconnected. The supervised web preview remains on port 8080. Verification requests to the real preview were read-only; synthetic browser fixtures used isolated API servers.
