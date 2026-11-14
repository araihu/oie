# DuckDB for interactive search

**Status:** Rejected for interactive search on 2026-11-14.

## Decision

Keep the SQLite store for interactive search. A native DuckDB copy sped up several broad reads, but the measured latencies remained in seconds and one paginated edit-history query exhausted its temporary storage allowance. The experiment did not establish that a second database could meet the latency and freshness needs of search while typing. The evidence concerns the queries and layouts tested. Aggregate analysis of historical snapshots remains an independent question under [O17 and B35](oie-spec.md#7-history-search-and-shared-links).

## Context

The [navigation performance pass](https://github.com/araihu/oie/commit/c247491b0cabd36bd95966d918cd196a5732cad2) had already added an indexed catalog projection, cursor seeks for bounded source windows, a WAL reader pool for navigation during writes, and a provider-identity lookup for archive imports. It also reduced the size of the source fragment and the browser work required to display it. The DuckDB experiment targeted database reads. It did not measure protocol archive fetches, transactional ingestion or browser rendering. See [catalog reads](media-gallery-scroll.md#indexed-catalog-reads) and [source navigation](timeline-pagination.md#opening-a-shared-source).

The proof of concept ran outside the application, against synthetic data, using the SQL and matcher from Oiê [`dd2e610`](https://github.com/araihu/oie/commit/dd2e6106e56f34c86a1b74c6dd989ecdbd350bc6). No application migrations or dependencies were introduced.

## Bounded navigation and analytical reads

The first probe compared SQLite 3.46.1 with DuckDB 2.0.0 through Python. It used warm connections in a shared Linux ARM64 VM and configured four DuckDB workers. The timeline and analytical probe used synthetic fixtures with 100,000 and 1,000,000 messages, skewed toward a large conversation and including timestamp ties, hidden scheduled messages and canonical aliases. The timeline test retrieved 16 rows using two cursor-bounded queries. The aggregation counted messages and distinct conversations by account and month. SQLite had the existing timeline index shape and a covering index for the aggregation. The DuckDB variants read a native table, a Zstandard Parquet file, or the SQLite copy via read-only `ATTACH`.

The catalog probe used the performance pass's separate fixture of 200,000 messages and 20,000 shared references. It applied migration 27 to a copy, producing 60,000 ordered entries, then measured the same global catalog query at different cursor positions and in all three tabs. It requested up to 51 rows per page. The count stopped at the configured limit. The probe compared ordered row contents and counts across engines.

Median SQL times in milliseconds. Each timeline figure uses 31 windows, each aggregation figure uses 15 executions, and each catalog figure uses 33 executions.

| Operation | SQLite | DuckDB native | DuckDB over Parquet | DuckDB via SQLite attachment |
| --- | ---: | ---: | ---: | ---: |
| Timeline, 100,000 messages | 0.091 | 27.562 | 76.126 | 114.952 |
| Timeline, 1,000,000 messages | 0.031 | 48.444 | 38.173 | 144.697 |
| Global catalog page, 200,000 messages | 0.408 | 22.113 | — | 129.812 |
| Capped catalog count, same fixture | 0.042 | 2.526 | — | 14.491 |
| Account/month aggregation, 100,000 messages | 18.719 | 4.933 | 22.924 | 27.793 |
| Account/month aggregation, 1,000,000 messages | 65.742 | 6.325 | 6.191 | 426.906 |

The SQLite query plan used an indexed seek for the source window and selected catalog references by the projection's ordered primary key before hydrating the page. The columnar copies helped with the broad aggregation at one million rows. A separate export of just the three grouping columns from SQLite to DuckDB took 705 ms, followed by 19 ms to checkpoint DuckDB and 38 ms to write Parquet. This export and the broader search snapshot below used different schemas. [SQLite extension](https://duckdb.org/docs/2.0/core_extensions/sqlite) · [Parquet reads](https://duckdb.org/docs/2.0/data/parquet/overview)

## Search while typing

The second probe used Go 1.27.4, the Oiê driver's SQLite 3.54.1 and the official DuckDB Go driver v2.20000.0. It ran the original SQLite queries and a DuckDB port over the same data through `database/sql`. The port adapted JSON scalar extraction and null handling, 64-bit timestamps, typed cursor comparisons, revision insertion order, and the join that establishes which canonical message owns a correction. Both executed the original Go matcher, including case and accent folding, prefix edit distance, adjacent transpositions and the handling of symbols. Example queries included `reuniao logsitica 🔧` and `confernecia`. [Matcher](../internal/adapters/sqlite/search_match.go) · [Message search SQL](../internal/adapters/sqlite/queries/message_search.sql) · [Catalog SQL](../internal/adapters/sqlite/queries/shared_content.sql) · [DuckDB Go driver](https://duckdb.org/docs/2.0/clients/go/overview)

The fixtures had 50,000, 200,000 and 1,000,000 original messages, plus correction rows, across 12 accounts and 240 conversations. They used repeated Portuguese and English text, rare terms, shared references with previews, timestamp ties, old parser versions, pending conversations, hidden scheduled messages and correction aliases. The largest fixture contained 1,965 correction rows and 142,858 shared references. The three-account filter selected accounts containing 417,488 message rows before visibility filtering.

The message timings include the retained message query, selected previews and catalog indexing progress. The catalog timings include the query for the requested tab, capped counts for the other tabs on the first page, and indexing progress. Pages used a limit of 50 and requested a 51st row as a continuation marker. The second-page tests used a cursor taken from the SQLite first page. The harness compared ordered result rows, selected previews, counts and indexing progress. An explicit control matched 20 historical revisions with history enabled and zero with it disabled on both smaller fixtures.

The 50,000- and 200,000-message runs used five warm samples per engine and scenario. The one-million-message run used three, with both database files on a RAM-backed filesystem, SQLite memory mapping capped at about 2 GiB, and DuckDB configured with one worker and a 1 GB buffer manager limit. Sample wall times varied substantially on the shared VM. The settings and sample counts differ across volumes, so the table supports comparisons within a run and does not establish a scaling curve.

Median SQL times in seconds, displayed as **SQLite / native DuckDB**. A dash indicates that the scenario has no comparable median in that run.

| Scenario | 50,000 messages | 200,000 messages | 1,000,000 messages |
| --- | ---: | ---: | ---: |
| Global, frequent literal | 0.713 / 0.364 | 12.613 / 5.068 | — |
| Global, typo | 0.897 / 0.401 | 7.695 / 3.400 | 101.65 / 26.55 |
| Global, rare match | 0.695 / 0.329 | 6.371 / 2.097 | — |
| Global, no match | 0.649 / 0.362 | 3.394 / 1.498 | — |
| Global, three accounts, metadata | 0.288 / 0.206 | 1.820 / 0.694 | 50.59 / 82.90 |
| Global, three accounts, edit history and second page | 0.466 / 0.397 | 2.870 / 1.453 | DuckDB exceeded temporary storage |
| Catalog, three accounts, empty query | 0.050 / 0.085 | 0.161 / 0.107 | 25.10 / 6.16 |
| Catalog, three accounts, literal | 0.076 / 0.154 | 0.293 / 0.276 | 16.29 / 2.77 |
| Catalog, three accounts, typo | 0.112 / 0.194 | 0.349 / 0.455 | 6.29 / 2.83 |
| Catalog, three accounts, typo and second page | 0.094 / 0.066 | 0.680 / 0.191 | 26.00 / 3.92 |
| Catalog, three accounts, no match | 0.140 / 0.162 | 1.028 / 0.335 | 66.14 / 13.84 |

At one million messages, the global typo samples ranged from 50.50 to 151.57 s in SQLite and 12.23 to 148.18 s in DuckDB. The edit-history query returned an older revision on the SQLite page. DuckDB exhausted the RAM-backed temporary allowance, then exhausted the configured 12 GB disk allowance on retry (reported as 11.1 GiB). The direct SQLite attachment had already been dropped from the main search comparison after edit-history preflight runs grew large temporary files.

Building the native search snapshot from the SQLite fixture took 323 s for one million messages. The full SQLite fixture occupied 2.66 GB and the selected DuckDB tables occupied 323 MB. The latter is a read copy of only the tables and view needed for the probe. It does not represent total disk usage if the production SQLite database and a DuckDB copy are both retained. The official Go driver also requires CGO, while the engine container currently builds with `CGO_ENABLED=0`. [Engine Dockerfile](../Dockerfile) · [DuckDB linking](https://duckdb.org/docs/2.0/clients/go/troubleshoot)

## Limits of the result

These measurements cover isolated SQL on synthetic data. They exclude concurrent backfill and edits, synchronization of an analytical copy, API and browser latency, cold-start latency, and the conversation and contact groups of global search. The experiment gives evidence about replacing the engine under the tested queries. It leaves open the performance of reducing the number of candidates that reach the fuzzy matcher, including with a derived index in SQLite. The separate long-term history evaluation in B35 remains open.
