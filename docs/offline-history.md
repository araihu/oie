# Recent conversations offline

The standalone web app saves text from the latest page of conversations
opened in a browser. When the browser cannot reach Oiê, or the BFF cannot
reach its engine, the inbox offers recent conversations in a read-only view.
**Retry** returns to the normal conversation when the connection is available.
Previously saved drafts and queued sends are separate and are recovered in
the normal composer.

In conversation details, **Keep available offline** protects that chat’s
saved messages from the seven-day expiration and automatic cache eviction.
The setting applies to this browser profile. Turn it off to return the chat
to ordinary cache behavior. The cache holds up to 12 saved conversations
within a combined 1 MiB. A preference whose snapshot was invalidated does
not occupy a snapshot slot.
If the space already used by protected chats leaves too little room, the
setting stays off and Oiê asks you to free space. Open the chat online to
refresh its saved messages. If a protected chat grows beyond the available
space, Oiê reports that the latest messages could not be saved. Free space
and reopen the chat online to refresh the offline copy. Offline reading
covers the saved text only.

## Storage

The root service worker precaches a small page with English and Brazilian
Portuguese variants, plus its scripts and stylesheet. It tries the network first for full
workspace navigation. Fragment requests stay on the page. A failed
conversation navigation can reopen the cached view. The notification worker keeps its existing scope.
The root worker is limited to the standalone web presentation on a secure
origin, including localhost. Authenticated hosted sessions and the native
desktop WebView do not register it.

IndexedDB holds snapshots for the last verified engine, keyed by owning
account and conversation. A snapshot contains text from at most 50 messages
in the latest loaded page. A snapshot is bounded to 192 KiB. The browser keeps at most
12 such snapshots from the last seven days within a combined 1 MiB budget.
Authentication failures, engine changes, resets and observed account,
conversation or history removals discard affected snapshots, including
protected snapshots. Removing a chat or account, resetting app data, or
changing engines clears the affected preferences. History changes and
unobserved event gaps may invalidate saved text while retaining the setting.
The next eligible chat view refreshes the saved text. Expired unprotected
entries are removed on the next access. The service worker does not cache
authenticated HTML, original withdrawn content, drafts, uploads or files.

The configurable device limit and search over saved text remain in
[section 11.4](oie-spec.md#114-pwa-and-offline-data).

## Browser verification

```sh
make build
npm --prefix tests/browser ci
OIE_OFFLINE_HISTORY_ONLY=1 node tests/browser/live-chat.cjs
```

The disposable fixture exercises cold offline navigation, browser and engine
outages, age and capacity limits, keeping and releasing a chat, withdrawn
messages, account and engine scoping, and draft recovery after reconnecting.
