# Live browser with the real XMPP engine

The chat interface now consumes the canonical generated engine client. The
independent mock remains a development fixture; it does not back the live preview.
Existing-account history recovery and read-only room scope are described in
[milestone three](milestone-three-history.md).
See [engine behavior and limits](milestone-two-engine.md) and
[UI mutation invariants](live-ui-integration.md).

## Local development

```sh
make dev-up
make build
mkdir -p bin
docker compose cp xmpp:/var/lib/prosody/certs/localhost.crt bin/xmpp-localhost.crt
bin/oie --listen 127.0.0.1:8080 --data-dir ./data/live-engine \
  --xmpp-ca-file bin/xmpp-localhost.crt
```

Open http://localhost:8080/. The default local role embeds the engine and serves
the BFF in one executable. The BFF still calls the same structured HTTP API.

From Accounts, register an existing protocol identity with a display name, then
connect it. The development fixtures are:

| Address | Password | TCP endpoint override |
| --- | --- | --- |
| alice@localhost | alice-local-only | 127.0.0.1:5222 |
| bob@localhost | bob-local-only | 127.0.0.1:5222 |

These are public local-development fixtures. Register both, connect both, then
start a conversation from Alice to bob@localhost. Use the account filter to switch
to Bob and reply. Separate browser tabs can keep one account/conversation each.
The header identifies the owning account, and new incoming messages appear live.

The composer accepts plain text. A successful engine acceptance clears the draft;
queued/dispatching/submitted/unknown/failed/cancelled remain actual engine states.
Submitted is a stream write, not recipient delivery/read evidence. Disconnect an
account to queue a send; reconnect to resume it, or cancel while it remains queued.
Attachments and protocol features not implemented by the engine remain unavailable.

Passwords are never restored into the form or saved in browser draft journals.
The engine retains them in its private files as described in the engine notes.
Browser drafts and unresolved submission keys are scoped to that device and engine.

## Hosted engine or separate development BFF

```sh
bin/oie --role hosted --listen 127.0.0.1:18084 --data-dir ./data/live-engine \
  --xmpp-ca-file bin/xmpp-localhost.crt
bin/oie --role bff --listen 127.0.0.1:8080 --engine-url http://127.0.0.1:18084
```

This is how the current live preview runs. The engine, BFF and optional development
TCP bridge are detached from verification sessions so closing a test does not
stop them. They are not installed as permanent system services. Process records
and logs live under `/tmp/oie-live-*`; engine data remains in `data/live-engine`.

For a remote Docker host reachable through `host.docker.internal`, whose published
loopback port is outside the native engine's network namespace, run `make dev-bridge`
in another terminal. This optional Go tool forwards local 127.0.0.1:5222 to
host.docker.internal:5222 without parsing XMPP or terminating TLS. Normal local
Docker installations do not need the bridge. Its source is in the tools module.

## Verification

After installing the browser-check dependencies and Chromium:

```sh
make check
make test-race
make dev-up
make test-engine-xmpp
make test-live-browser
```

The live browser test starts isolated engine/BFF processes and a fresh temporary
data directory, keeping the user preview and data untouched. It drives account
registration/connection forms, account-scoped conversation creation, two-way real
XMPP, live draft preservation, disconnected sends, queued cancellation, reconnect,
and a narrow Brazilian Portuguese view. It checks recipient effects through the
engine API, rather than accepting a rendered success message as delivery proof.
Screenshots and a compact report go into `tests/browser/artifacts`. CI repeats this
workflow against its own Prosody instance.

The previous mock preview is still available through `make mock-api` for fixture
and presentation checks. Mock-only media examples are not claims of implemented
media transfer, history backfill, receipts, sync, or other alpha/backlog features.
