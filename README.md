# Oiê

A protocol-adaptable chat application in Go with a Goshtoso web interface.
XMPP is the first protocol; local and hosted engines share the same API.

The display name is Oiê. The shared identity, browser icons and distribution
provenance are documented in [Branding](docs/branding.md).

The alpha targets one owner with multiple protocol accounts. Device sync remains
planned.

The current release candidate is **v0.0.1-rc.0**. The first alpha, **v0.0.1**,
requires improved initial room-history recovery. That is the first backlog
priority, tracked as [B25](docs/history-bootstrap-discussion.md); the current
recovery behavior is accepted in rc.0. macOS Apple Silicon and Ubuntu Linux
x86_64 release packages and the per-user installer are available on
[GitHub Releases](https://github.com/araihu/oie/releases/tag/v0.0.1-rc.0). See the
[release notes](docs/releases/v0.0.1-rc.0.md).

- [Desktop installation on macOS and Linux](docs/installation.md)
- [Live browser setup and development accounts](docs/live-preview.md)
- [Local CI loop](docs/local-ci.md)
- [Native desktop build and lifecycle](docs/desktop.md)
- [First specification](docs/oie-spec.md)
- [Decision history](docs/unified-chat-discussion.md)
- [Formatting feasibility](docs/oie-formatting-feasibility.md)

## Current implementation

The engine now connects existing XMPP accounts and sends/receives direct plain
text through the API. Immediate sends use a transactional SQLite outbox with
idempotent acceptance, restart recovery, explicit uncertain outcomes, and guarded
queued cancellation. Enabled accounts reconnect after network failure/restart.
The API also exposes retained conversations/messages and durable SSE updates.

The Goshtoso interface now uses the real engine: register/connect accounts, choose
a sending account, start direct conversations, send/cancel queued messages, and
receive live updates. Device drafts and unresolved acceptance keys survive refresh.
The independent read-only mock remains available for presentation tests.
Existing account text archives now recover serially with durable checkpoints,
original timestamps, replay deduplication and progress through the API/UI. Saved
rooms support explicit entry/leave, basic text and replies while joined.
Attachment uploads, schedules, sync, PWA/offline and backup/restore remain later
work.
See [history recovery scope](docs/milestone-three-history.md). See [engine milestone notes](docs/milestone-two-engine.md) and
[foundation notes](docs/milestone-one.md) for scope and invariants.

The Wails desktop command embeds this same engine and interface. Closing its
window keeps the engine running; explicit Quit stops it. See the
[installation instructions](docs/installation.md) for the release installer,
runtime dependencies and source installation through Go. The installer checks
archive checksums and preserves app data during manual updates.

Published account, peer and saved-room profile images now load into a local
thumbnail cache and appear in the interface. Missing or inaccessible images keep
initials. Anonymous room speakers retain initials until their identity can be
verified. See [profile image scope](docs/profile-images.md).

Incoming image links and social cards now have retained previews. Nearby images
download automatically by default, with a per-device override. Expansion uses the
retained original; Image info provides its source URL and file details. Standalone
image URLs appear only as the image. Messages render Markdown by default through
Margo, with a device override and an action to copy the original plain text. See
[preview scope](docs/message-previews.md) and the agreed
[viewer actions](docs/image-viewer-discussion.md). Source messages can now be
starred from a contextual chevron menu or the image toolbar. Star state has its
own guarded revisions and durable receipts; the engine API and generated CLI
provide a paginated starred-message collection. The image toolbar shows the
sender and local device time, and cached originals can be saved from the message
menu. See [message stars](docs/message-stars.md).

Verified incoming corrections retain their previous versions. Outgoing editing
currently covers the latest own plain text original sent by this engine in the
current verified connection. Imported messages, earlier originals, replies and
structured mentions are outside outgoing correction support.

API 0.26 adds retained emoji reaction counts and full own-set replacement or
removal, with a searchable English/Portuguese picker. Direct reactions have real
development-server coverage. Room support requires trusted stanza IDs and current
verified non-anonymous actor identity. See [reaction API and persistence scope](docs/message-reactions.md)
and [reaction controls](docs/reactions.md).

## Run

Requirements: Go 1.27.3+, Make, and Docker Compose v2 for the development server.
Browser checks additionally use Node 24+.

```sh
make build build-cli
bin/oie --data-dir ./data
```

Open [the local interface](http://127.0.0.1:8080).

The same executable can run a hosted engine or a BFF using another engine:

```sh
bin/oie --role hosted --listen 127.0.0.1:8080 --data-dir ./data
# In another terminal:
bin/oie --role bff --listen 127.0.0.1:8081 --engine-url http://127.0.0.1:8080
```

The default role is local. The BFF calls the generated HTTP client and does not
open the engine database. A sync-service role is not implemented yet.

## API and generated development CLI

The source contract is [api/openapi.yaml](api/openapi.yaml), also served at
`/api/openapi.yaml`. Errors use stable codes in `application/problem+json`.

```sh
bin/oiectl system health
bin/oiectl system engine --output json
printf '%s\n' '{"protocol":"xmpp","address":"alice@localhost","label":"Personal"}' |
  bin/oiectl accounts register \
    --idempotency-key 019a5df0-0000-7000-8000-000000000004 \
    --body-stdin --output json
bin/oiectl accounts list --output json
bin/oiectl operations get \
  --operation-id 019a5df0-0000-7000-8000-000000000004 --output json
```

The example intentionally reuses one operation key: repeating the same normalized
registration returns its original result; changing its payload with that key
returns 409. Use a new UUIDv7 for a new logical operation. Set `OIECTL_SERVER` or
`--server` to use another engine.

Arai Hû's [oapi-cli-gen](https://github.com/araihu/oapi-cli-gen) generates the
standalone [CLI module](cli). The overlay sets the command names. Run
`bin/oiectl messages send --help` to see its flags, or open the
[command reference](docs/installation.md#oiectl-commands) for the full list.

Use raw output for SSE and byte responses. To resume the event stream, supply
the last event ID you processed.

```sh
bin/oiectl events stream --output raw
bin/oiectl events stream --output raw --last-event-id ENGINE_UUID:SEQUENCE
```

## Connect and message through the API

Start the development server, copy its certificate as shown below, then start an
engine trusting that certificate:

```sh
bin/oie --data-dir ./data --xmpp-ca-file bin/xmpp-localhost.crt
```

After registering the fixture accounts, replace `ACCOUNT_UUID` and
`CONVERSATION_UUID` with IDs returned by the API. Connection credentials go on
stdin and are never included in account reads or operation receipts:

```sh
printf '%s\n' '{"password":"alice-local-only","endpoint":"localhost:5222"}' |
  bin/oiectl accounts connect --account-id ACCOUNT_UUID --body-stdin
bin/oiectl accounts connection --account-id ACCOUNT_UUID
printf '%s\n' '{"accountId":"ACCOUNT_UUID","address":"bob@localhost"}' |
  bin/oiectl conversations open \
    --idempotency-key 019a5df0-0000-7000-8000-000000000005 --body-stdin
printf '%s\n' '{"originalText":"Olá from Oiê!"}' |
  bin/oiectl messages send --conversation-id CONVERSATION_UUID \
    --idempotency-key 019a5df0-0000-7000-8000-000000000006 --body-stdin
bin/oiectl messages list --conversation-id CONVERSATION_UUID
```

Connect Bob with `bob-local-only` too to receive the exchange in this engine.
Use new UUIDv7 operation keys for new logical actions. A send acceptance is durable
`queued` intent; read the message for current `sendState`. `submitted` means a
stream write, not recipient delivery. Unknown outcomes are never blindly resent.
Passwords currently reside in private 0600 engine-local JSON files (plaintext);
credential-store/encryption integration remains open.

## Development XMPP server

```sh
make dev-up
make test-xmpp
make test-engine-xmpp
make dev-down
```

Compose runs a digest-pinned Prosody 13 image with:

- XMPP STARTTLS at `localhost:5222`.
- HTTPS/WebSocket and file sharing at `https://localhost:5281`.
- Persistent SQL-backed message archives and rooms.
- Room service `conference.localhost` and upload service `upload.localhost`.
- Fixture accounts `alice@localhost` / `alice-local-only` and
  `bob@localhost` / `bob-local-only`.

These credentials are public development fixtures, provisioned once per volume.
The upload limit is 20 MiB; development uploads expire after seven days.
Message archives have no configured expiry. Ports bind only to host loopback.
Use `OIE_XMPP_PORT` and `OIE_XMPP_HTTPS_PORT` to change published ports.

The volume retains accounts, archives, and the development TLS key/certificate
across restarts. `make dev-down` preserves it. An explicit
`docker compose down -v` resets all development-server data.

For a client that needs the development certificate:

```sh
mkdir -p bin
docker compose cp xmpp:/var/lib/prosody/certs/localhost.crt bin/xmpp-localhost.crt
```

Trust that certificate explicitly for development rather than disabling TLS
verification. `make test-xmpp` runs in the server's network namespace so it works
with a remote Docker daemon too; it verifies TLS, both fixture logins, scoped
MAM/carbons discovery, and an actual message exchange.

This environment's nested Docker builder required the classic-builder fallback:
`DOCKER_BUILDKIT=0 docker build -t oie-dev-xmpp dev/xmpp`, followed by
`docker compose up -d --wait --no-build`. Normal Docker installations can use
`make dev-up`.

## Generation and verification

```sh
make generate        # OpenAPI bindings, templ, CLI overlay/bundle and standalone CLI
make check           # warning-free lint, drift checks, vet, tests and Compose validation
make test-race
make test-live-browser # real UI + engine + Prosody; browser dependencies required
```

Build/generation tools are pinned in the `tools` module; root go.mod contains
application dependencies. Vacuum stays isolated under `tools/vacuum` so its templ
dependency cannot change the application generator/runtime graph. SQL queries are
generated with sqlc; golang-migrate applies embedded migrations on startup and
preserves the earlier foundation database. One process owns each engine data
directory. No sibling checkout or local module replacement is required.

To run browser consumer checks against a running Oiê:

```sh
npm --prefix tests/browser ci
cd tests/browser
npx playwright install chromium
OIE_BROWSER_URL=http://127.0.0.1:8080 npm test
```

The checks cover 390/1440px layouts, Goshtoso/Minimal themes, light/dark modes,
en/pt-BR, focus, accessibility, console errors, and system-theme changes.
Screenshots and a report are written to the ignored `tests/browser/artifacts`.

## License

[Apache License 2.0](LICENSE).
