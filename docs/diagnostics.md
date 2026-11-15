# Diagnostics

Open **Settings → Diagnostics**, select a time range, and use **Refresh** to
read the latest records. **Copy** and **Download** export the exact snapshot
on screen. Download saves a single `oie-diagnostics.jsonl` file.
The view shows up to 500 records. Use the stored files for a longer export.
The browser fetches the viewer script and records when the drawer opens.

Desktop displays **This device** even if the engine fails to start or the
server is unreachable. For a configured remote engine, select **Server**
to fetch the hosted record from a supported HTTPS or loopback URL. Use an
owner diagnostics access key when prompted. The key stays in the open
drawer for the current session and is discarded when it closes.

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

## Enable hosted viewing

A local browser connected directly to the loopback listener can read
Diagnostics. A hosted role or a non-loopback listener requires
`OIE_DIAGNOSTICS_KEY` to be set in the Oiê process. This also applies to the
container's default listener. Set a randomly generated key of 16 to 512
characters, for example the output of `openssl rand -hex 32`, and restart the instance. Enter the key in **Diagnostics** when
prompted. Keep the browser interface and engine behind an authenticated
HTTPS proxy for remote access. The diagnostics key grants access only to
the log snapshot.

For Docker, pass `--env-file /path/to/private/oie.env` with the variable set.
For Helm, put the key in an existing Secret in the release namespace and
configure `diagnostics.existingSecret`. The Secret entry defaults to `key`
and can be changed with `diagnostics.secretKey`. For a data-only instance, use the **Server** source in desktop Diagnostics
or retrieve the files through the server operator.

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
