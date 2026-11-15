# Diagnostics

Open **Settings → Diagnostics**, select a time range, and use **Refresh** to
read the latest records. Select **Copy** to copy the records on screen or open
the menu next to **Copy** and select **Download** to save them as
`oie-diagnostics.jsonl`. Each row shows the event, severity, time, and
component. Expand a row to inspect its JSON fields. Both export actions
use the original log data.
The view shows up to 500 records. Use the stored files for a longer export.
The browser fetches the viewer script and records when the drawer opens.

Desktop displays **This device** even if the engine fails to start or the
server is unreachable. For a configured remote engine, select **Server**
to fetch the hosted records. For an authenticated hosted engine, launch
desktop with the [engine token file](hosted-auth.md#create-and-revoke-api-keys).
For an older engine, enter its diagnostics access key if prompted. That key
is discarded when the drawer closes.

## Retrieve logs outside the app

- macOS desktop stores one directory per profile under
  `~/Library/Logs/oie/`.
- Linux desktop stores one directory per profile under
  `$XDG_STATE_HOME/oie/`, or `~/.local/state/oie/` when XDG state is unset.
- The browser/server executable keeps files under `<data-dir>/diagnostics/`
  by default. Use `--diagnostics-dir` to choose another private, writable
  directory. Use a persistent directory for logs that must survive container
  replacement.
- Docker and Helm capture the same operational records from stderr.
  Use `docker logs oie` or `kubectl logs -n oie statefulset/oie`.
  Docker and Kubernetes control retention for their copies.

Each process keeps one active file and up to two rotated files, each at most
5 MiB. Files rotate on size and on the first log write of a new UTC day.
The application prunes files older than seven days when it writes logs.
The viewer filters records to at most seven days. Files are private to the
application's OS user. If the disk sink fails, the process reports a
sanitized storage warning to stderr and keeps running.

## Access server diagnostics

In [hosted mode](hosted-auth.md), sign in as the owner to view Diagnostics.
Scripted clients can fetch `/ui/diagnostics/log` with an Oiê API key in
`Authorization: Bearer …` and the `X-Oie-Diagnostics: 1` header. The log
endpoint also works on a hosted data-only instance.

A local browser on a loopback listener can read Diagnostics directly.
For a legacy installation using a private, non-loopback listener and an
external authenticated proxy, set `OIE_DIAGNOSTICS_KEY` to a separate
random key of 16 to 512 characters. Enter it in Diagnostics when prompted.
In Docker, set the variable through a private `--env-file`. In Helm, set
`diagnostics.existingSecret` to the Secret holding the legacy key.
The entry defaults to `key` and can be changed with `diagnostics.secretKey`.

## What a report contains

Each record has UTC time, an event and severity, the component, build and
platform. Some events add a duration, retry attempt, HTTP status, or a
one-way reference to an individual frontend-to-engine request. Match
`request_ref` across desktop and server logs for an HTTP failure. The same
recording rules apply when `OIE_LOG_LEVEL=debug` is set before launch.

Logs contain predefined operational fields. Message and draft text, search
terms, XMPP addresses and stanzas, account and message IDs, credentials,
HTTP bodies and headers, URLs, IP addresses, filenames, local paths and raw
error strings are excluded. Open and inspect the snapshot before choosing
whether to share it. Oiê has no automatic log upload. Proxy and XMPP-provider
logs have their own configuration.
