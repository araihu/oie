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
The 1,024 candidate budget includes pending IDs and exceptions that belong to
the requested account or conversation scope. Unscoped searches keep an
engine-wide budget. SQL handles
candidate overflow, partial or failed lookup, a crossed publication, unavailable
generations, rooms with up to 512 stored messages and queries without usable
tokens. Bleve lookup has a 25 ms cancellation target. The duration can
exceed it during fuzzy dictionary expansion. Both search paths feed the same [fuzzy highlight presentation](message-search.md).

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
arriving after a batch reads its inputs. A paused lookup test also verifies
that a query crossing a publication can still return the match from its
original SQLite snapshot after Bleve drops the old posting.

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
Both databases are loaded before the timed arms. The initial Bleve build
finishes before the two arms run back to back.

| p95 | SQL, lower host load | Bleve + SQL, lower host load | SQL, high host load | Bleve + SQL, high host load |
| --- | ---: | ---: | ---: | ---: |
| `arqiuvo` | 2.95 s | 54.1 ms | 44.18 s | 5.00 s |
| `docun` | 3.60 s | 42.9 ms | 45.67 s | 11.56 s |
| `faturamento` | 2.44 s | 39.1 ms | 43.65 s | 6.97 s |
| `viedo` | 2.35 s | 30.0 ms | 43.58 s | 7.46 s |
| Archive message acceptance | 22.9 ms | 22.4 ms | 147 ms | 3.24 s |

Both runs used Linux arm64, Go 1.27.4, `GOMAXPROCS=4` and a tmpfs data
directory on the same shared host. Under lower host load, initial index
readiness took 38.4 s. The indexed path completed 60 of 120 searches,
with 60 using SQL after candidate lookup declined. The pending queue
peaked at 10 sampled IDs and drained within 109 ms of the last write.
Under high host load, initial readiness took 1 min 24 s. Bleve served 54
searches and candidate lookup declined for 66. Pending work peaked at 11
sampled IDs and drained within 6.74 s of the last write. Both runs
recorded zero fallbacks for crossed publications. All four arms committed
all 240 messages. The two runs show how much latency and ingest cost can
vary with the host. O08 still needs a target environment and repeated
measurements before production activation.

This workload covers first-page global search after initial indexing,
during local archive receipt. Room, deep-page, history, and XMPP transport
latency, startup upgrade latency, disk usage, and ingest during a full
rebuild require separate measurements. The [comparative prototype](message-search-index-benchmark.md)
covers FTS5 under its own workload.

## Measurement through the browser server

The browser-server mode measures loopback HTTP through both the engine
and the BFF. It reads the full HTML response and checks for real highlights.
Conversation responses must also contain source ranges. The engine uses the
same SQLite schema in both arms. The control calls the chronological SQL path
used by the experimental fallback.

```sh
OIE_BLEVE_BENCH=1 GOMAXPROCS=4 GOWORK=off go test ./internal/adapters/sqlite \
  -run '^TestExperimentalMessageIndexIngestionBenchmark$' -count=1 -timeout=14m -v \
  -oie-index-bench-web
```

The workload adds 52 long messages to 100,000 retained messages in a saved
room. Four search workers offer 210 requests at four per second while one
writer offers 20 archive messages per second. Global requests ask for 20
results and room requests ask for 50. The measured latency includes queueing,
engine verification, highlight generation and HTML transfer.
