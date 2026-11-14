# Experimental Bleve index in the engine

**Experimental implementation of R08.** Start the local or hosted engine
with `OIE_EXPERIMENTAL_BLEVE_SEARCH=1` in its environment. The engine can
serve SQL searches as soon as SQLite opens. Restart without the variable
to use the existing search path. Migration 28 runs at database upgrade,
including in engines with the feature off. The existing upgrade backup and
the creation of the global timeline index are synchronous. Before the
first activation, message writes create no pending entries. After first
activation, SQLite keeps recording a coalesced pending entry per changed
message even while the feature is off, so the index can catch up on the
next activation.

## Index and query behavior

The engine owns a single Scorch index in `private/message-search` under its
data directory. The worker creates or opens it after SQLite is ready.
The initial scan uses UUID keyset pagination, up to 64 messages per batch.
SQLite records concurrent changes in the same transactions as the source
writes. Search uses the index once the scan completes and always includes
pending message IDs and projection exceptions from the same SQLite read
snapshot. After catch-up, the worker checks for new work every 500 ms and
yields between batches. Derived tokens can contain private message data.
Engine reset removes the sidecar and clears its state with the database.
Physical purge remains part of O10.

Bleve supplies candidate UUIDs from normalized trigrams and fuzzy prefix
tokens, scoped by account and conversation. SQLite applies the existing
matcher, verifies current content and authorized retained edit history,
checks visibility, and applies the chronological cursor before hydration.
The 1,024 candidate budget includes pending IDs and exceptions. SQL handles
candidate overflow, partial or failed lookup, a crossed publication, unavailable
generations, rooms with up to 512 stored messages and queries without usable
tokens. Bleve lookup has a 25 ms cancellation target. The duration can
exceed it during fuzzy dictionary expansion. The existing highlight and
excerpt code still renders the verified results.

The worker advances a publication counter before starting a durable Scorch
batch. A reader records the active generation and counter before its first
SQLite read. It collects pending IDs and runs the Bleve lookup, then checks
both values again. If either changed, it falls back in the pinned SQLite
snapshot. A read starting during the current batch sees the pending IDs
that cover its changes or the acknowledgement after the durable batch.
The acknowledgement updates exception coverage and deletes a pending ID
only if its token still matches the version from the batch. The lock only
protects the in-memory state. A newly built index serves candidates only
after SQLite confirms the ready checkpoint.

## Recovery

A matching engine identity, projection version and batch checkpoint allow
reopen to reuse the previous generation, including a partial initial build.
A missing or unreadable index at open, or a checkpoint mismatch such as
an interrupted acknowledgement, causes a fresh scan while SQL serves search.
An uncertain batch or acknowledgement causes the worker to drop the active
handle and retry. A read-time lookup failure falls back for that request.
Persistent read-time errors that leave the worker healthy are reassessed
on engine reopen. Rebuild uses one generation at a time. An in-place
restore that bypasses the engine's data lifecycle requires invalidating the
sidecar before opening the restored engine.

Automated tests compare complete pages and cursors with the existing SQL
query across typo, literal, accent, CJK, metadata, account and conversation
cases, including retained edits and oversized projection exceptions. They
exercise clean reopen, resuming a partial scan with a concurrent change,
a subprocess crash after the durable Bleve batch and before SQLite
acknowledgement, a missing or corrupt sidecar, reset, and a newer write
arriving after a batch reads its inputs.

## Measurement with concurrent archive ingestion

Run from the repository root.

```sh
OIE_BLEVE_BENCH=1 GOMAXPROCS=4 GOWORK=off go test ./internal/adapters/sqlite \
  -run '^TestExperimentalMessageIndexIngestionBenchmark$' -count=1 -timeout=14m -v
```

The experiment seeds 100,000 retained messages in one direct conversation,
then offers 120 global HTTP searches at 10 per second and 240 archive
messages at 20 per second. Four search workers request the first 20 hits
through the engine's HTTP handler over loopback and decode the JSON page.
One ingestion worker uses the production SQLite message acceptance and
archive alias path. The p95 includes queueing from the scheduled arrival.
Each search term has 30 samples. The SQL control uses the chronological
query on the same read pool with the same schema. The run checks that all
archive IDs resolve to distinct retained messages and compares all pages
for the newly ingested search term once the pending queue has drained.

This workload covers first-page global search after initial indexing,
during local archive receipt. Room, deep-page, history, and XMPP transport
latency, startup upgrade latency, disk usage, and ingest during a full
rebuild require separate measurements. The [comparative prototype](message-search-index-benchmark.md)
covers FTS5 under its own workload.
