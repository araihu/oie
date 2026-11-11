# Oiê API and CLI errors

The engine returns [RFC 9457 Problem Details](https://www.rfc-editor.org/rfc/rfc9457.html)
as `application/problem+json`. Each problem has a stable `type` URI of the
form `urn:oie:problem:<code>`, a short `code`, a `title`, and the HTTP
`status`. When present, `documentation` is an HTTPS link to guidance for
the problem. Its destination may change. Use `type` to identify the problem
in client code.

## invalid_request

**Invalid request · HTTP 400, 405, 413, 415.** Check the required path, query,
header, and body fields for the operation. For 405, use a method listed in
`Allow`. For 413, keep the request body within 1 MiB. For 415, send a media
type supported by the operation.

## not_found

**Not Found · HTTP 404.** Check the route, resource ID, and engine you are
connected to. If you used an ID from an earlier listing, refresh that listing.

## idempotency_conflict

**Idempotency key conflict · HTTP 409.** The engine has already accepted a
different request with this idempotency key. To recover an earlier acceptance,
look up the original operation or retry with the original key and input. Use
a new key for a new operation.

## account_exists

**Account already exists · HTTP 409.** An account with this protocol and
address is already registered. List the accounts and use the existing account
ID.

## invalid_cursor

**Invalid event cursor · HTTP 409.** The engine rejected the supplied cursor.
For pagination, restart the listing using the intended filters. For an event
stream, get a fresh engine cursor, refresh the relevant snapshots, then
resume from that cursor and deduplicate events already applied.

## history_changed

**History changed · HTTP 409.** The message edit history changed while you
were paging through it. Start again at the first page.

## unavailable

**Service Unavailable · HTTP 503.** The engine could not complete the request.
Retry a read after the engine recovers. For an idempotent write with an
uncertain outcome, look up the operation or retry with the same key and input.

## state_conflict

**State conflict · HTTP 409.** The current resource state prevents the
operation. Refresh the resource and reconsider the action using its current
revision. The response can include `currentMessage`, `currentConversation`,
`currentStar`, or `currentRoomSession`. For an operation that requires a
joined room, check the room session first.

## protocol_unavailable

**Protocol unavailable · HTTP 503.** The requested protocol service is
unavailable. Check the account's connection and capabilities before retrying.

## media_blocked

**Media access blocked · HTTP 422.** The engine blocked access to the media
source. Use a different permitted source if one is available.

## media_fetch_failed

**Media fetch failed · HTTP 502.** The engine could not fetch a usable
response from the media source. Check whether the source is still reachable
before retrying the download.

## media_too_large

**Media too large · HTTP 413.** The media exceeds the engine's download
limit. Use a smaller version if one is available.

## media_unsupported

**Unsupported media · HTTP 415.** The source returned a media type the engine
cannot serve for this operation. Use a supported version of the file.

## range_not_satisfiable

**Range not satisfiable · HTTP 416.** The requested byte range cannot be
served. Use the length in `Content-Range` when present to select a new range,
or request the whole resource.

## media_busy

**Media service busy · HTTP 503.** The media service is at capacity. Wait
before retrying the download.

## media_timeout

**Media request timed out · HTTP 504.** The media source took too long to
respond. Retry the download if the source is still available.

## oiectl local errors

With `--output json`, oiectl reports local failures as Problem Details on
stderr. The process exit code is available in `exitCode`.

### usage_error

**Invalid command usage · exit 2.**
Type `urn:cli:oiectl:problem:usage_error`.

Check the diagnostic for the missing or invalid argument. Run the command
with `--help` to see its required flags and accepted values.

### transport_error

**Transport error · exit 3.**
Type `urn:cli:oiectl:problem:transport_error`.

For a connection failure, check that the engine is running and that
`--server` or `OIECTL_SERVER` points to it. The detail can also report a
timeout, cancellation, or response read failure. If a write's outcome is
uncertain, look up the operation or retry with its original idempotency key
and input when the command supports idempotency.
