# Account capability evidence

Account management remains the default, compact sidebar page. Connection controls come first; a Capabilities link opens a wider page for that same account, with a native back link. The chat, inbox filter, device language/theme and composer draft survive navigation. The matrix compares Oie's implementation, protocol support, and cached server evidence. A separate discovery section lists observed entities, identities and namespaces.

Surface brief: the account owner needs to diagnose feature availability without hiding connection management. Use Oie's existing colors/type, compact rows, Goshtoso Table with stacked mobile fields, Link, Panel and Accordion. Show account identity before the matrix, then discovery details. Use ordinary browser navigation and the existing read-only live refresh. No new dependency or upstream component API is required.

API 0.8.0 extends the existing account-capabilities query. Application support comes from the application feature catalog; an adapter cannot enable an unimplemented Oie operation by advertising a protocol feature. Server observations are session-local diagnostics, never operation authorization. `unknown`, `not_advertised`, `not_required` and `scope_dependent` have distinct meanings. In particular, omission from successful discovery is not a definitive denial, and client features cannot be judged from server namespaces alone. Room history evidence applies only to observed saved rooms. Mixed or failed room evidence remains partial.

The XMPP adapter queries account/server discovery once per connection, plus at most twelve advertised root services on the account's server domain or subdomains. The worker has a sixty-second budget and uses the existing serialized IQ gate. Authorized saved-room history discovery contributes room evidence without joining rooms. There are at most 32 entity observations, 256 namespaces and 16 identities per entity. Bounded/invalid omissions are marked truncated; negative conclusions stay unknown. Foreign or skipped service items prevent a complete negative inventory. HTTP reads return snapshots and do not start discovery. Errors retain unknown states without exposing credentials or endpoint configuration. Reconnect/disconnect clears evidence; a previous session cannot repopulate the new snapshot.

The protocol evidence follows [XEP-0030 service discovery](https://xmpp.org/extensions/xep-0030.html). Upload capability uses the advertised service from [XEP-0363](https://xmpp.org/extensions/xep-0363.html). Peer-dependent delivery-receipt support is not a server-wide flag, as described in [XEP-0184](https://xmpp.org/extensions/xep-0184.html). Reports do not test recipient support, room permissions, uploads or unimplemented operations.

| Route or state | Action or request | Allowed? | Expected response | Input and context preserved | Focus or destination | Effect count |
|---|---|---:|---|---|---|---:|
| Account detail | Open Capabilities | yes | Wider account-specific comparison page | Chat, inbox filter, theme, locale, draft | Capabilities heading | 0 mutations |
| Capabilities | Back | yes | Same account management page | Account, chat and draft | Capabilities link | 0 mutations |
| Pending/partial discovery | Read or refresh | yes | Cached explicit evidence, unknown kept distinct | Account and drawer scroll | Existing focused control preserved | 0 provider queries |
| Disconnected | Read | yes | App/protocol rows, cleared service observations | Same account | Matrix | 0 provider queries |
| Server advertises unimplemented feature | Read | yes | Server supported, app not implemented | Same account | Matrix | 0 protocol operations |
| API unavailable/missing projection | Retry | yes | In-shell recovery; no fabricated false support | Account and chat | Retry/current page | 0 mutations |
| Unknown account | Read | no | Engine 404, web account-missing recovery | Shell and known account navigation | Recovery link | 0 |
| Previous session completes discovery | Cache write | no | Ignore outdated observation | Current session snapshot | Unchanged | 0 |

## Verification on 2026-11-08

Native `make check` passes, including generated OpenAPI/sqlc drift checks, vacuum with zero warnings, golangci-lint with zero findings, formatting, vet and tests. Engine/API/XMPP and web race suites pass. Generated CLI tests read the diagnostic report without changing application state. Development Prosody integration verifies actual account/server discovery, the unchanged direct-text contract, disconnect invalidation, live messages and history recovery. One initial provider-rejection timeout passed on a focused rerun and the subsequent full integration run.

`tests/browser/capabilities.cjs` verifies the invariant rows, legacy/foreign/unavailable API recovery, exact navigation context, focused/expanded discovery details during updates, drafts, zero writes and sixteen viewport/theme/color/language combinations with zero WCAG A/AA violations. Existing live-chat and reply suites pass. The source-star suite passed on retry after one timeout at its existing keyboard-focus assertion. The complete real-engine messaging browser workflow passes. The Dagger browser stage now includes the capabilities fixture.

Before updating the live engine, an online SQLite backup was made. Verification retained all existing account, conversation, message, content-revision and star IDs, exact source text, outbox and engine identity, with schema 7 unchanged. The real connected account returned 23 feature rows and nine discovered entities. A read-only browser check opened that account's capability matrix and returned to its management controls with focus restored and no mutations. Private account details are not public fixtures.

The bounded full `make ci` attempt exited 124 after 180 seconds without returning Dagger stage results. Native check completion is verified; full Dagger completion remains unverified on this host.
